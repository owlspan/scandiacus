package main

// Unwind tables: the .pdata section of a Windows executable.
//
// Windows walks a thread's stack with nothing but these tables. For each
// function there is a RUNTIME_FUNCTION - where it starts, where it ends,
// and where its UNWIND_INFO is - and the UNWIND_INFO says how the
// prologue changed the stack, so the walker can undo it and find the
// return address. A function with no entry is taken to be a leaf that
// never moved rsp, which for anything else gives a wrong return address
// and ends the walk. Without these, a native debugger showed one frame of
// a Scandiacus program, a crash dump said nothing useful, and anything that
// unwinds - RtlCaptureStackBackTrace, the stack-overflow report - went
// wrong at the first Scandiacus frame.
//
// Every function this compiler writes starts the same way:
//
//	push rbp
//	mov  rbp, rsp
//	sub  rsp, N        (or a probe per page, for a big frame)
//
// and keeps rbp as its frame pointer to the end. That makes one
// UNWIND_INFO true of all of them, whatever N is: rbp is the frame
// register at offset zero, set four bytes in, after a push of rbp one
// byte in. Unwinding from anywhere past the prologue sets rsp from rbp,
// which throws away the allocation however big it was, so the
// allocation does not need describing at all and the probe sequence of
// a large frame is no trouble. From inside the prologue the codes whose
// offset has been reached are undone and the rest are not, which is the
// partial unwind Windows expects. The epilogue, mov rsp, rbp then pop
// rbp then ret, needs no description either: at the mov rbp is still
// the frame, and the pop and ret are the shapes the unwinder recognises.
//
// Not described: where a function saved rbx, rsi, rdi or r12-r15. A
// stack walk does not need it - only the return addresses - but a
// debugger shows those registers in a caller's frame as they are in the
// callee. Exception unwinding would need it to restore them, and nothing
// here unwinds through a Scandiacus frame to resume execution.
//
// A function whose frame fits in a page keeps no frame pointer (see
// frameRef in x64.go). Its prologue is
//
//	push r...          (the callee-saved registers it uses, if any)
//	sub  rsp, N
//
// and that needs an UNWIND_INFO of its own, since N and the pushes
// differ from one function to the next: a UWOP_ALLOC for the sub and a
// UWOP_PUSH_NONVOL per push, newest first, each at the offset just past
// its instruction. The epilogue, add rsp, N then the pops then ret, is
// the other shape the unwinder recognises, so it needs nothing either.
// Describing the pushes means a debugger also gets those registers
// right in the caller's frame, which the rbp frame never told it.
//
// A function is found by its bytes, not its name: a text symbol whose
// first four bytes are 55 48 89 E5 starts a frame of the first shape,
// and one that starts with pushes of callee-saved registers and then a
// sub rsp, or with the sub alone, one of the second. The hand-written
// runtime helpers that build neither get no entry, which is correct for
// the leaf ones.

import (
	"bytes"
	"encoding/binary"
	"sort"
	"strings"
)

var framePrologue = []byte{0x55, 0x48, 0x89, 0xE5} // push rbp; mov rbp, rsp

const (
	uwopPushNonvol = 0
	uwopAllocLarge = 1
	uwopAllocSmall = 2
	uwopSetFPReg   = 3
	regRBP         = 5
)

// unwindInfo is the one UNWIND_INFO every rbp frame shares.
func unwindInfo() []byte {
	return []byte{
		1,               // version 1, no handler flags
		4,               // the prologue is four bytes
		2,               // two codes
		regRBP | 0<<4,   // frame register rbp, frame offset 0
		4, uwopSetFPReg, // at 4: mov rbp, rsp
		1, uwopPushNonvol | regRBP<<4, // at 1: push rbp
	}
}

// framelessInfo reads a prologue of pushes and one sub rsp at the start
// of code and returns its UNWIND_INFO, or nil when code does not start
// that way.
func framelessInfo(code []byte) []byte {
	type op struct {
		at   int // offset just past the instruction
		code []byte
	}
	var ops []op
	i := 0
	for {
		reg := -1
		n := 1
		switch {
		case i < len(code) && (code[i] == 0x53 || code[i] == 0x56 || code[i] == 0x57):
			reg = int(code[i] - 0x50) // rbx 3, rsi 6, rdi 7
		case i+1 < len(code) && code[i] == 0x41 && code[i+1] >= 0x54 && code[i+1] <= 0x57:
			reg = 8 + int(code[i+1]-0x50) // r12 to r15
			n = 2
		}
		if reg < 0 {
			break
		}
		i += n
		ops = append(ops, op{i, []byte{byte(i), byte(uwopPushNonvol | reg<<4)}})
	}
	var size int
	switch {
	case i+3 < len(code) && code[i] == 0x48 && code[i+1] == 0x83 && code[i+2] == 0xEC:
		size = int(code[i+3])
		i += 4
	case i+6 < len(code) && code[i] == 0x48 && code[i+1] == 0x81 && code[i+2] == 0xEC:
		size = int(binary.LittleEndian.Uint32(code[i+3:]))
		i += 7
	default:
		return nil
	}
	if size == 0 || size%8 != 0 || size >= 512<<10 {
		return nil
	}
	if size <= 128 {
		ops = append(ops, op{i, []byte{byte(i), byte(uwopAllocSmall | (size/8-1)<<4)}})
	} else {
		ops = append(ops, op{i, []byte{byte(i), byte(uwopAllocLarge), byte(size / 8), byte(size / 8 >> 8)}})
	}
	var codes []byte
	for k := len(ops) - 1; k >= 0; k-- {
		codes = append(codes, ops[k].code...)
	}
	slots := len(codes) / 2
	info := []byte{1, byte(i), byte(slots), 0}
	info = append(info, codes...)
	if slots%2 != 0 {
		info = append(info, 0, 0) // the array is padded to an even count
	}
	return info
}

// buildUnwind returns the unwind data placed in the text: the shared
// UNWIND_INFO first and then one per function with no frame pointer,
// all at infoAt (a multiple of four) once appended. With it comes the
// .pdata section: one RUNTIME_FUNCTION per function, sorted by start
// address. The table is empty when no function has either shape.
func buildUnwind(obj *object, text []byte, textRVA, infoAt int) (infos, pdata []byte) {
	var starts []int
	seen := map[int]bool{}
	for name, s := range obj.sym {
		if s.sec != secText || strings.HasPrefix(name, ".L") || seen[s.off] {
			continue
		}
		seen[s.off] = true
		starts = append(starts, s.off)
	}
	sort.Ints(starts)

	infos = unwindInfo()
	// A function runs to the next symbol, or to the end of the code.
	codeEnd := len(obj.text)
	for i, off := range starts {
		end := codeEnd
		if i+1 < len(starts) && starts[i+1] < end {
			end = starts[i+1]
		}
		if off >= end {
			continue
		}
		info := infoAt
		if !bytes.HasPrefix(text[off:end], framePrologue) {
			fi := framelessInfo(text[off:end])
			if fi == nil {
				continue
			}
			info = infoAt + len(infos)
			infos = append(infos, fi...)
		}
		var e [12]byte
		binary.LittleEndian.PutUint32(e[0:], uint32(textRVA+off))
		binary.LittleEndian.PutUint32(e[4:], uint32(textRVA+end))
		binary.LittleEndian.PutUint32(e[8:], uint32(textRVA+info))
		pdata = append(pdata, e[:]...)
	}
	return infos, pdata
}
