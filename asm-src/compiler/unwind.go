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
// A function is found by its bytes, not its name: a text symbol whose
// first four bytes are 55 48 89 E5 starts a frame of this shape. The
// hand-written runtime helpers that do not build one get no entry, which
// is correct for the leaf ones.

import (
	"bytes"
	"encoding/binary"
	"sort"
	"strings"
)

var framePrologue = []byte{0x55, 0x48, 0x89, 0xE5} // push rbp; mov rbp, rsp

const (
	uwopPushNonvol = 0
	uwopSetFPReg   = 3
	regRBP         = 5
)

// unwindInfo is the one UNWIND_INFO every function shares.
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

// buildPdata returns the .pdata section: one RUNTIME_FUNCTION per
// function, sorted by start address, each naming the UNWIND_INFO at
// infoRVA. Empty when no function has the shape.
func buildPdata(obj *object, text []byte, textRVA, infoRVA int) []byte {
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

	// A function runs to the next symbol, or to the end of the code.
	codeEnd := len(obj.text)
	var blob []byte
	for i, off := range starts {
		if off+len(framePrologue) > codeEnd || !bytes.Equal(text[off:off+len(framePrologue)], framePrologue) {
			continue
		}
		end := codeEnd
		if i+1 < len(starts) && starts[i+1] < end {
			end = starts[i+1]
		}
		var e [12]byte
		binary.LittleEndian.PutUint32(e[0:], uint32(textRVA+off))
		binary.LittleEndian.PutUint32(e[4:], uint32(textRVA+end))
		binary.LittleEndian.PutUint32(e[8:], uint32(infoRVA))
		blob = append(blob, e[:]...)
	}
	return blob
}
