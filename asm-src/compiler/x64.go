package main

// x86-64 emission, GNU as with `.intel_syntax noprefix`.
//
// Nothing above this file knows an x86 register exists, and nothing here
// decides anything about the program: it turns IR into instructions and
// that is all.
//
// The text it writes is no longer sent to an assembler. encode.go reads
// it back and produces the machine code, link.go lays it out and pe.go
// writes the executable, so this output is now an intermediate form that
// happens to also be readable - which is what `scandiacus asm` is for.
//
// Two rules for anything added here, both of which the encoder relies on:
//
//   1. No assembler conveniences. No macros, no pseudo-instructions, no
//      expressions anything has to evaluate. Every line must map to
//      exactly one instruction, because encode.go turns exactly one line
//      into one instruction and has nowhere to put a second.
//
//   2. One instruction per line, no folding. GNU as would happily pick a
//      shorter encoding for us, and the encoder here is checked against
//      what as produces, so depending on that would be circular.
//
// Register strategy: every virtual register has a frame slot, and
// operands are read into rax and rcx around each instruction - which is
// obviously right, if slow. On top of that, regalloc.go pools a few
// short-lived non-pointer values into r8, r9, r10 and r11 between two
// barriers, and loc() below is where that decision meets this file:
// reads and writes of a pooled value name its machine register instead
// of its slot. With SCANDIACUS_NORA set, or for a function nothing qualified,
// every loc answers with the slot and the output is what the dumbest
// correct strategy always produced.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

type Emitter struct {
	// pushedGP is how many general callee-saved registers the prologue
	// pushed, right under rbp: the frame's slots start below them.
	pushedGP int

	// frameless is set for a function that keeps no frame pointer: its
	// words are addressed from rsp, which the body never moves, and
	// frameBytes is what the prologue subtracted from it. See frameRef.
	frameless  bool
	frameBytes int

	b   strings.Builder
	f   *Func
	mod *Module

	// homes maps the few virtual registers the allocator pooled to their
	// machine register. A register missing from the map lives in its
	// frame slot as before, and a nil map - the pass switched off - is
	// the every-value-in-a-slot emitter this file started as.
	homes map[Reg]string

	// promoted maps the frame slots kept in callee-saved registers, and
	// saved is the registers this function saves and restores; see
	// promote.go.
	promoted map[int64]string
	saved    []string

	polls int // numbers each OpGCPoll's local label
	nAsm  int // numbers each asm statement written, for its labels

	// cold is code placed after the function's epilogue: the stops of
	// its bounds checks, which run at most once, kept out of the way of
	// the loop around them.
	cold  strings.Builder
	nCold int

	// lastUse is the last instruction reading each register, and jumps
	// the jumps to each label, for the function being emitted.
	lastUse map[Reg]int
	jumps   map[int64][]int

	// vslot is each unpooled virtual register's frame word; see
	// layoutRegs.
	vslot   map[Reg]int64
	nvslots int64

	// consts and dropConst are the constants the selector folds into
	// immediates; see x64sel.go. Nil with the optimisations off.
	consts    map[Reg]int64
	dropConst map[Reg]bool
	alias     map[Reg]string // a load read straight from its slot; see aliasLoad
	fconsts   map[Reg]int64  // float constants, by pool index
	dropFC    map[Reg]bool

	// frameAt is the offset in the array area of every register an
	// OpFrameAddr defines, so an element address can be written from
	// rsp instead of from a register holding the base; see memOperand.
	frameAt  map[Reg]int64
	dropAddr map[Reg]bool // those no instruction needs in a register

	// next is nextReal's answer for every instruction of nextFor, worked
	// out in one pass, since scanning for it each time was quadratic
	// over a long run of dropped constants (a long list literal).
	next    []int
	nextFor *Func

	// ranges is what valueRanges knows of this function's registers;
	// nil with the optimisations off.
	ranges map[Reg]span
}

// Windows x64 calling convention.
//
//   - The first four integer arguments go in rcx, rdx, r8, r9. The rest
//     go on the stack at [rsp+32] upwards, in order.
//   - The return value comes back in rax.
//   - The caller reserves 32 bytes of shadow space below the arguments,
//     whether or not the callee uses it.
//   - rsp must be 16-byte aligned at the point of the call instruction.
//   - rbx, rbp, rdi, rsi and r12-r15 belong to the caller. Only rbp is
//     touched here, and it is pushed and restored.
var argRegs = [4]string{"rcx", "rdx", "r8", "r9"}

// xmmArgs holds the floating-point counterpart of argRegs. Which one an
// argument in position i uses is decided purely by that argument's own
// type - a float in position 0 goes in xmm0 and an int in position 1
// still goes in rdx, not rcx's float sibling. The two arrays are parallel
// by position, never combined into one count.
var xmmArgs = [4]string{"xmm0", "xmm1", "xmm2", "xmm3"}

const shadowSpace = 32

func (e *Emitter) line(format string, args ...any) {
	fmt.Fprintf(&e.b, "    "+format+"\n", args...)
}

func (e *Emitter) label(name string) {
	fmt.Fprintf(&e.b, "%s:\n", name)
}

func (e *Emitter) comment(text string) {
	fmt.Fprintf(&e.b, "    # %s\n", text)
}

// The frame, from rbp downwards:
//
//	[rbp-8 ...]                locals and temporaries, NSlots of them
//	[rbp-(NSlots+1)*8 ...]     virtual registers, NRegs of them
//	[rsp+0 .. rsp+outgoing]    shadow space and outgoing arguments
//
// Reserving the outgoing area once in the prologue, rather than pushing
// arguments per call, is what keeps rsp aligned with no per-call
// arithmetic: after `push rbp` rsp is 16-byte aligned, and subtracting a
// multiple of 16 leaves it that way for every call in the body.
func (e *Emitter) slotAddr(slot int64) string {
	if r, ok := e.promoted[slot]; ok {
		return r // see promote.go
	}
	return "qword ptr " + e.frameRef((slot+1)*8+int64(e.pushedGP)*8)
}

// frameRef addresses the frame word off bytes below where rbp would
// point. Without a frame pointer that place is rsp plus the allocation
// plus the pushes, so the layout above is the same either way and only
// the base register changes. rbp is still cheaper to encode by a byte
// per access (rsp as a base needs a SIB byte), but setting it up and
// tearing it down was four instructions on every call, which is most
// of what a small recursive function does.
func (e *Emitter) frameRef(off int64) string {
	if e.frameless {
		return "[rsp+" + strconv.FormatInt(int64(e.frameBytes)+int64(e.pushedGP)*8-off, 10) + "]"
	}
	return "[rbp-" + strconv.FormatInt(off, 10) + "]"
}

func (e *Emitter) regAddr(r Reg) string {
	idx, ok := e.vslot[r]
	if !ok {
		idx = e.nvslots - 1 // pooled: nothing should land here, but a word is kept for it
	}
	off := (idx+int64(e.f.NSlots)+1)*8 + int64(e.pushedGP)*8
	return "qword ptr " + e.frameRef(off)
}

// layoutRegs gives a frame word only to the virtual registers that
// have no machine register, numbered densely. Most of a function's
// values never touch memory once allocation has run, and a frame sized
// for all of them made every call's frame several times larger than
// what it used, which a deep recursion pays for in stack and cache.
//
// Not in a program that starts threads: there, a word a value has
// finished with can still be the one place another thread's collection
// finds an object the thread code holds only as an int, and sharing it
// freed objects that were still in use (tests/threads under eager
// collection). Such a program keeps a word per value.
//
// Values whose lives do not overlap share a word, the way the register
// allocator shares registers. A life is the run from the value's write
// to its last read, widened over any loop it is live across part of: a
// value read inside a loop it was made before stays live until the
// loop's last instruction, since the back edge reads it again, and one
// made inside a loop and read after it is live from the loop's head.
// In a long top level each statement's values used to take words of
// their own, so the frame grew with the program and every access to it
// needed a four-byte offset.
func (e *Emitter) layoutRegs(f *Func) {
	e.vslot = make(map[Reg]int64, f.NRegs)
	defOf := make([]int, f.NRegs)
	lastOf := make([]int, f.NRegs)
	for i := range defOf {
		defOf[i] = -1
	}
	note := func(r Reg, pos int) {
		if r >= 0 && int(r) < f.NRegs {
			if defOf[r] < 0 || pos < defOf[r] {
				defOf[r] = pos
			}
			if pos > lastOf[r] {
				lastOf[r] = pos
			}
		}
	}
	for i := range f.Code {
		in := &f.Code[i]
		note(in.Dst, i)
		note(in.A, i)
		note(in.B, i)
		for _, a := range in.Args {
			note(a, i)
		}
	}
	// Every backward jump closes a loop from its label to itself.
	labelAt := labelPositions(f)
	type loop struct{ head, end int }
	var loops []loop
	for i, in := range f.Code {
		switch in.Op {
		case OpJump, OpJumpIf, OpJumpNot:
			if h, ok := labelAt[in.Imm]; ok && h <= i {
				loops = append(loops, loop{h, i})
			}
		}
	}
	type span struct {
		r         Reg
		def, last int
	}
	var spans []span
	for r := 0; r < f.NRegs; r++ {
		if _, pooled := e.homes[Reg(r)]; pooled || defOf[r] < 0 || e.dropAddr[Reg(r)] {
			continue
		}
		d, l := defOf[r], lastOf[r]
		for changed := true; changed; {
			changed = false
			for _, lp := range loops {
				if d < lp.head && l >= lp.head && l < lp.end {
					l, changed = lp.end, true
				}
				if d > lp.head && d <= lp.end && l > lp.end {
					d, changed = lp.head, true
				}
			}
		}
		spans = append(spans, span{Reg(r), d, l})
	}
	// Linear scan, in order of the start of each life: a word is free
	// again once its value has been read for the last time, at or before
	// this one is written - an instruction reads before it writes.
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].def != spans[j].def {
			return spans[i].def < spans[j].def
		}
		return spans[i].r < spans[j].r
	})
	//
	// In a program with threads a word that holds a pointer is never
	// shared: a thread parked for another's collection is scanned by its
	// stack alone, and a dead word can be the only thing keeping an
	// object that a later instruction still reaches through a derived
	// address. Words holding numbers share as before.
	var busyUntil []int
	var ptrWord []bool
	for _, sp := range spans {
		isPtr := e.mod.Threaded && int(sp.r) < len(f.RegTypes) && f.RegTypes[sp.r].holdsPointer()
		word := -1
		if !isPtr {
			for w, u := range busyUntil {
				if !ptrWord[w] && u <= sp.def {
					word = w
					break
				}
			}
		}
		if word < 0 {
			busyUntil = append(busyUntil, 0)
			ptrWord = append(ptrWord, isPtr)
			word = len(busyUntil) - 1
		}
		busyUntil[word] = sp.last
		e.vslot[sp.r] = int64(word)
	}
	e.nvslots = int64(len(busyUntil)) + 1
}

// loc is where a virtual register lives: a machine register for the few
// the allocator pooled, its frame slot for everything else. Every read
// and write of a virtual register goes through here, which is the whole
// extent of the emitter's awareness that allocation happens.
func (e *Emitter) loc(r Reg) string {
	if a, ok := e.alias[r]; ok {
		return a
	}
	if e.homes != nil {
		if h, ok := e.homes[r]; ok {
			return h
		}
	}
	return e.regAddr(r)
}

// put parks a finished integer result in its home. The move disappears
// when the home is where the value already sits.
func (e *Emitter) put(r Reg, from string) {
	if d := e.loc(r); d != from {
		e.line("mov %s, %s", d, from)
	}
}

// putf is put's floating-point twin.
func (e *Emitter) putf(r Reg, from string) {
	if d := e.loc(r); d != from {
		e.line("movsd %s, %s", d, from)
	}
}

func (e *Emitter) outgoing() int {
	n := shadowSpace
	if e.f.MaxCallArgs > 4 {
		n = e.f.MaxCallArgs * 8
	}
	if n%16 != 0 {
		n += 16 - n%16
	}
	return n
}

func (e *Emitter) frameSize() int {
	n := (int64(e.f.NSlots) + e.nvslots + 2*int64(len(e.saved)-e.pushedGP)) * 8
	n += int64(e.outgoing()) + e.f.ArrayBytes
	// rsp is aligned after push rbp; the pushes and this together must
	// keep it so. With no rbp pushed the return address alone leaves it
	// eight off.
	lead := int64(0)
	if e.frameless {
		lead = 8
	}
	for (lead+n+int64(e.pushedGP)*8)%16 != 0 {
		n += 8
	}
	return int(n)
}

// simpleIntDef is an instruction that writes its result with no
// register of its own on the side - not a shift, which wants cl, nor a
// division, which takes rdx - so its result may go straight into rax or
// an argument register.
func simpleIntDef(in Instr) bool {
	switch in.Op {
	case OpAdd, OpSub, OpBAnd, OpBOr, OpBXor, OpConst, OpLoad, OpLoadMem:
		return true
	}
	return false
}

// endsFlow reports whether nothing runs after an instruction of this
// kind in a straight line.
func endsFlow(op Op) bool {
	switch op {
	case OpRet, OpJump, OpBoundsFail, OpMustFail:
		return true
	}
	return false
}

// boundsCall reports a failed index: the index in rcx, the length in
// rdx and where it happened in r8, for __vy_bounds to record. Passing
// the place in a register, rather than storing it here, is eleven bytes
// less at every index the program checks.
func (e *Emitter) boundsCall(in Instr, idx, length string) {
	if length == "rcx" {
		e.line("mov rdx, rcx")
		e.line("mov rcx, %s", idx)
	} else {
		e.line("mov rcx, %s", idx)
		e.line("mov rdx, %s", length)
	}
	if in.Imm > 0 {
		e.line("lea r8, __str%d[rip]", in.Imm-1)
	} else {
		e.line("xor r8d, r8d")
	}
	e.line("call __vy_bounds")
}

func Emit(m *Module) string {
	e := &Emitter{mod: m}

	e.b.WriteString("# Generated by scandiacus. Do not edit.\n")
	e.b.WriteString("    .intel_syntax noprefix\n")
	e.b.WriteString("    .global main\n")

	for _, ext := range e.externs() {
		e.b.WriteString("    .extern " + ext + "\n")
	}

	e.b.WriteString("\n    .section .rdata\n")
	if e.mod.Helpers["rodata"] {
		e.label("__rodata_begin") // see delete, in gcoff.go
	}
	e.label("__fmt_int")
	e.b.WriteString("    .asciz \"%lld\\n\"\n")
	e.label("__fmt_str")
	e.b.WriteString("    .asciz \"%s\\n\"\n")
	e.label("__fmt_int_raw")
	e.b.WriteString("    .asciz \"%lld\"\n")
	e.label("__fmt_str_raw")
	e.b.WriteString("    .asciz \"%s\"\n")
	e.label("__fmt_uint")
	e.b.WriteString("    .asciz \"%llu\\n\"\n")
	e.label("__fmt_uint_raw")
	e.b.WriteString("    .asciz \"%llu\"\n")
	e.label("__fmt_at")
	e.b.WriteString("    .asciz \"    at %s\\n\"\n")
	e.label("__fmt_bounds")
	e.b.WriteString("    .asciz \"runtime error: index %lld is out of range for a list of length %lld\\n\"\n")
	e.label("__fmt_must")
	e.b.WriteString("    .asciz \"runtime error: %s\\n\"\n")
	e.strLiteral("__str_true", "true")
	e.strLiteral("__str_false", "false")
	e.label("__fmt_s")
	e.b.WriteString("    .asciz \"%s\"\n")
	e.label("__fmt_c")
	e.b.WriteString("    .asciz \"%c\"\n")
	e.label("__fmt_nl")
	e.b.WriteString("    .asciz \"\\n\"\n")

	if e.mod.Helpers["floatfmt"] {
		e.label("__fmt_star_e")
		e.b.WriteString("    .asciz \"%.*e\"\n")
		e.label("__fmt_star_f")
		e.b.WriteString("    .asciz \"%.*f\"\n")
	}

	for i, s := range m.Strings {
		e.strLiteral(fmt.Sprintf("__str%d", i), s)
	}
	if e.mod.Helpers["rodata"] {
		e.label("__rodata_end")
		e.b.WriteString("    .asciz \"\"\n")
	}

	// Top-level consts. Writable, so they cannot live in .rdata beside
	// the string literals; zeroed, because main fills them in before
	// anything else runs.
	if m.NGlobals > 0 {
		e.b.WriteString("\n    .section .bss\n")
		e.b.WriteString("    .align 8\n")
		e.label("__globals")
		e.b.WriteString(fmt.Sprintf("    .space %d\n", m.NGlobals*wordSize))
		e.b.WriteString("\n    .section .rdata\n")
	}

	if len(m.Floats) > 0 || e.mod.Helpers["floattostr"] {
		e.label("__fmt_g")
		e.b.WriteString("    .asciz \"%.*g\"\n")
		// Negation goes through multiplication rather than flipping the
		// sign bit with a bitwise xor, because that would need a second
		// SSE instruction family (xorpd) and a 16-byte-aligned mask
		// constant for no real saving over one more .rdata entry.
		e.label("__fmt_e")
		e.b.WriteString("    .asciz \"%.*e\"\n")
		e.label("__neg_one")
		e.b.WriteString(fmt.Sprintf("    .quad %#x\n", math.Float64bits(-1)))
	}
	if e.mod.Helpers["floattostr"] {
		e.b.WriteString(ryuTables())
	}
	if len(m.FnTable) > 0 {
		// Each row is two offsets from the row itself: the function and
		// its name. Offsets rather than addresses, so the table needs no
		// relocation when the image moves, and stays eight bytes a row.
		e.b.WriteString("    .align 8\n")
		e.label("__vy_fntab")
		e.b.WriteString(fmt.Sprintf("    .quad %d\n", len(m.FnTable)))
		for _, f := range m.FnTable {
			e.b.WriteString(fmt.Sprintf("    .long %s - .\n", f.Sym))
			e.b.WriteString(fmt.Sprintf("    .long __str%d - .\n", f.Str))
		}
	}

	for i, v := range m.Floats {
		// .quad with the raw IEEE 754 bit pattern, computed here rather
		// than trusting the assembler's own float literal syntax - the
		// same reasoning as the string pool above using .asciz for text
		// rather than a computed encoding: this is data the assembler
		// already turns to bytes for us, not an instruction whose
		// encoding this file is supposed to own.
		e.label(fmt.Sprintf("__flt%d", i))
		e.b.WriteString(fmt.Sprintf("    .quad %#x\n", math.Float64bits(v)))
	}

	e.b.WriteString("\n    .text\n")

	if m.DLL {
		e.dllStart()
	} else {
		e.start()
	}
	e.helpers()

	for _, f := range m.Funcs {
		e.function(f)
	}
	for _, t := range m.Thunks {
		e.thunk(t)
	}
	if m.DLL {
		for _, x := range m.Exports {
			e.thunk(x.Thunk)
		}
	}
	if m.Helpers["crash"] {
		// Where the program's code ends, for the crash handler to tell
		// an address in it from one in a DLL.
		e.label("__vy_text_end")
		e.line("ret")
	}

	return e.b.String()
}

// externs lists the C runtime functions actually referenced. Emitting
// only what is used keeps the generated assembly readable and matches
// how the Go backend pulls in imports on demand.
// sortedKeys makes the .extern block deterministic. Two compilations of
// the same program have to produce the same bytes, and a map does not.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// start is the program's entry point: what Windows jumps to, as opposed
// to what a C runtime would call.
//
// A linked program never needed one, because gcc supplied the CRT
// startup that calls main. pe.go has no CRT to link, so this is it: line
// the stack up, call main, and hand what it returned to exit.
//
// The alignment is done by masking rather than assumed. The convention
// is that rsp is sixteen-byte aligned at a call site, and the loader
// does arrive that way, but nothing here has to depend on it - one
// instruction makes it true whatever the caller did, and an SSE store
// against a misaligned stack is a fault a long way from its cause.
//
// It is written here, in the assembly, rather than as bytes in the
// linker, so that `scandiacus asm` shows the whole program and the byte
// encoder's test covers these instructions like any others.
func (e *Emitter) start() {
	e.b.WriteString("\n")
	e.label("__start")
	e.comment("the entry point Windows calls")
	e.line("and rsp, -16")
	e.line("sub rsp, 32")
	e.line("call main")
	e.line("mov ecx, eax")
	e.line("call exit")
}

func (e *Emitter) externs() []string {
	// exit is always here: the entry point above ends with it, whether
	// or not the program itself can fail.
	out := []string{"printf", "exit"}
	for _, sym := range sortedKeys(e.mod.Externs) {
		out = append(out, sym)
	}
	if e.mod.Helpers["concat"] {
		out = append(out, "malloc", "memmove")
	}
	if e.mod.Helpers["streq"] {
		out = append(out, "memcmp")
	}
	if e.mod.Helpers["printstr"] {
		out = append(out, "printf", "strlen")
	}
	if e.mod.Helpers["strlen"] {
		out = append(out, "strlen")
	}
	if e.mod.Helpers["inttostr"] {
		out = append(out, "malloc", "_snprintf", "memmove")
	}
	if e.mod.Helpers["alloc"] {
		out = append(out, "malloc")
	}
	if e.mod.Helpers["bounds"] {
		out = append(out, "_snprintf", "_write", "exit", "fflush")
	}
	if e.mod.Helpers["must"] {
		out = append(out, "_snprintf", "_write", "exit", "fflush")
	}
	if e.mod.Helpers["floattostr"] {
		out = append(out, "malloc", "strlen", "strcpy", "__stdio_common_vsprintf", "strtod")
	}
	if e.mod.Helpers["fmod"] {
		out = append(out, "fmod")
	}
	out = append(out, "_setmode")

	seen := map[string]bool{}
	var uniq []string
	for _, s := range out {
		if !seen[s] {
			seen[s] = true
			uniq = append(uniq, s)
		}
	}
	return uniq
}

// escapeAsm renders a Scandiacus string as a GNU as .asciz operand.
// strLiteral writes a string constant with the two-word header every
// heap object has in front of it, so its length is read from the header
// like any other string's: the size word is the length plus the
// terminator, shifted past the tag (tagBytes, zero), and the link is
// zero. The collector never meets one on its list, so never frees it.
func (e *Emitter) strLiteral(label, s string) {
	e.b.WriteString("    .align 8\n")
	e.b.WriteString(fmt.Sprintf("    .quad %d\n", (len(s)+1)<<tagShift))
	e.b.WriteString("    .quad 0\n")
	e.label(label)
	e.b.WriteString("    .asciz \"" + escapeAsm(s) + "\"\n")
}

func escapeAsm(s string) string {
	var b strings.Builder
	for _, r := range []byte(s) {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, "\\%03o", r)
			} else {
				b.WriteByte(r)
			}
		}
	}
	return b.String()
}

// pageSize is what Windows commits a stack page at a time in. It is a
// property of the operating system, not of the processor.
const pageSize = 4096

// reserve opens the stack frame.
//
// A frame smaller than a page is one `sub rsp`. A larger one has to
// touch every page on the way down, because Windows grows a thread's
// stack by trapping a write to the single guard page sitting below the
// committed region and moving it down one page. Moving rsp past that
// page without writing to it means the guard is never hit, and the
// first write below it is an access violation instead.
//
// This cost hours. The symptom is a program that segfaults sometimes:
// whether the write lands on an uncommitted page depends on how much
// stack earlier calls happened to commit, so the same executable runs
// and faults on alternate attempts. It appeared when a program first
// had a frame over 4 KB, which made it look like a bug in whatever
// feature that program was using.
//
// The probes are written out rather than looped so that every line is
// still one instruction, and so that nothing needs a scratch register
// at a point where the frame does not exist yet.
func (e *Emitter) reserve(bytes int) {
	for bytes > pageSize {
		e.line("sub rsp, %d", pageSize)
		e.line("mov qword ptr [rsp], 0")
		bytes -= pageSize
	}
	e.line("sub rsp, %d", bytes)
}

func (e *Emitter) function(f *Func) {
	e.f = f
	e.promoted = nil
	if envOff("SCANDIACUS_NORA") || envOff("SCANDIACUS_NOOPT") {
		e.homes = nil
	} else {
		// No slot lives in a register for the whole function when asm
		// may write any register; pooled values stop at each asm
		// statement, as at a call. See asm.go.
		if !envOff("SCANDIACUS_NOPROMOTE") && !hasAsm(f) {
			e.promoted = promoteSlots(f, e.mod.Threaded)
			for s, r := range promoteFloatSlots(f) {
				e.promoted[s] = r
			}
		}
		// The pools: the volatile registers first, which cost nothing,
		// then the callee-saved ones no slot was promoted into, which
		// cost a save and a restore.
		taken := map[string]bool{}
		for _, r := range e.promoted {
			taken[r] = true
		}
		intPool := append([]string{}, raIntRegs[:]...)
		floatPool := append([]string{}, raFloatRegs[:]...)
		if usesVecHoist(f) {
			floatPool = floatPool[:len(floatPool)-1] // xmm5, see vecloop.go
		}
		if f.Name != collectSym {
			for _, r := range calleeSaved {
				if !taken[r] {
					intPool = append(intPool, r)
				}
			}
			for _, r := range calleeSavedXmm {
				if !taken[r] {
					floatPool = append(floatPool, r)
				}
			}
		}
		e.homes = allocateRegsIn(f, intPool)
		if fh := allocateFloatRegsIn(f, floatPool); len(fh) > 0 {
			if e.homes == nil {
				e.homes = map[Reg]string{}
			}
			for r, h := range fh {
				e.homes[r] = h
			}
		}
		// Nothing pooled is still the optimised emitter: a promoted
		// float slot can only be stored to by the selector's movsd, and
		// the plain translator would write "mov xmm6, rax".
		if e.homes == nil {
			e.homes = map[Reg]string{}
		}
	}
	e.consts, e.dropConst = nil, nil
	e.ranges = nil
	if e.homes != nil {
		e.planConsts(f)
		e.ranges = valueRanges(f)
	}
	e.saved = e.savedRegs()
	e.pushedGP = 0
	for _, r := range e.saved {
		if !isXmm(r) {
			e.pushedGP++
		}
	}
	e.layoutRegs(f)
	name := f.Name
	if name != "main" {
		name = fnSym(name) // no chance of colliding with libc
	}

	e.b.WriteString("\n")
	e.label(name)
	e.comment(fmt.Sprintf("%d param(s), %d slot(s), %d vreg(s)",
		f.NParams, f.NSlots, f.NRegs))
	// A frame that fits in a page needs no probes, so its prologue is
	// pushes and one sub, which unwind.go can describe function by
	// function; such a function keeps no frame pointer. A bigger one
	// keeps the rbp frame and the one shared description. With the
	// optimisations off every function keeps rbp, which leaves the
	// differential fuzzers a reference that does not depend on this.
	e.frameless = false
	if !envOff("SCANDIACUS_NOOPT") && !envOff("SCANDIACUS_FRAMEPTR") {
		e.frameless = true
		if e.frameSize() >= pageSize {
			e.frameless = false
		}
	}
	e.frameBytes = e.frameSize()
	if !e.frameless {
		e.line("push rbp")
		e.line("mov rbp, rsp")
	}
	// The general callee-saved registers are pushed, after rbp is the
	// frame pointer so the unwind description stays the same; see
	// pushedGP. The xmm ones go in the frame.
	for _, r := range e.saved {
		if !isXmm(r) {
			e.line("push %s", r)
		}
	}
	e.reserve(e.frameBytes)
	for i, r := range e.saved {
		if isXmm(r) {
			e.saveReg(i, r)
		}
	}

	if f.Env {
		// The environment arrived in r10, which anything at all may
		// clobber. Slot zero is reserved for it and nothing else, so
		// this is the first instruction of the body.
		e.comment("the captured environment")
		e.line("mov %s, r10", e.slotAddr(0))
	}

	if f.Name == "main" && !e.mod.DLL {
		// Put stdout in binary mode. The C runtime otherwise translates
		// every \n into \r\n on the way out, so the same program printed
		// CRLF here and LF through the Go backend. The differential test
		// against ../src caught that on the first program ever compiled.
		// 1 is stdout, 0x8000 is _O_BINARY.
		e.comment("stdout to binary mode, so newlines match the Go backend")
		e.line("mov ecx, 1")
		e.line("mov edx, 32768")
		e.line("call _setmode")

		// stderr needs the same treatment and was missed at first,
		// because nothing wrote there until a runtime abort carried a
		// message. The bounds and must helpers both end up on fd 2, and
		// their text has to match the Go backend byte for byte too.
		e.comment("stderr too, for the runtime abort messages")
		e.line("mov ecx, 2")
		e.line("mov edx, 32768")
		e.line("call _setmode")
	}
	if f.Name == "main" && !e.mod.DLL && e.mod.Helpers["cpufeat"] {
		e.comment("whether the packed loops can use AVX; see vecloop.go")
		e.line("call __vy_cpufeat")
	}

	uses := regUses(f)
	e.alias = map[Reg]string{}
	e.cold.Reset()
	e.nCold = 0
	e.lastUse = map[Reg]int{}
	for i, in := range f.Code {
		for _, r := range append([]Reg{in.A, in.B}, in.Args...) {
			if r != NoReg {
				e.lastUse[r] = i
			}
		}
	}
	e.jumps = jumpsTo(f)
	fixed := e.fixedSlots(f)
	steady := e.steadyLoads(f)
	// The slots written exactly once, whose address is never taken.
	onceSlots := map[int64]bool{}
	stores := map[int64]int{}
	for _, in := range f.Code {
		switch in.Op {
		case OpStore:
			stores[in.Imm]++
		case OpSlotAddr, OpStoreByte:
			stores[in.Imm] += 2
		}
	}
	for slot, n := range stores {
		if n == 1 {
			onceSlots[slot] = true
		}
	}
	for i := 0; i < len(f.Code); i++ {
		in := f.Code[i]
		if in.Op == OpLoad {
			if r, ok := fixed[in.Imm]; ok {
				// The register is the slot, and the slot never changes
				// from here on: read it there, at every use.
				e.alias[in.Dst] = r
				continue
			}
			if steady[i] {
				e.alias[in.Dst] = e.promoted[in.Imm]
				continue
			}
		}
		// The fusions look at the next instructions that emit anything:
		// a constant folded into an immediate sits in between otherwise.
		j := e.nextReal(f, i)
		k := -1
		if j >= 0 {
			k = e.nextReal(f, j)
		}
		if in.Op == OpParam && j >= 0 && f.Code[j].Op == OpStore && f.Code[j].A == in.Dst {
			if r, ok := fixed[f.Code[j].Imm]; ok {
				// A parameter kept in a register for the whole function
				// arrives straight there, rather than by way of its own
				// frame slot, which nothing would read again.
				e.alias[in.Dst] = r
			}
		}
		if in.Op == OpParam && in.Imm < 4 && j >= 0 && f.Code[j].Op == OpStore &&
			f.Code[j].A == in.Dst {
			if r, kept := e.promoted[f.Code[j].Imm]; kept {
				if _, aliased := e.alias[in.Dst]; !aliased {
					// A parameter being stored into a local kept in a
					// register goes from its argument register straight
					// there. Through its frame word it was a store and a
					// load on every call, the load waiting on the store.
					// The word is still written when something else
					// reads the parameter.
					float := e.f.ParamTypes[in.Imm].k == kFloat
					if float == isXmm(r) {
						if uses[in.Dst] > 1 {
							e.instr(in)
						}
						if float {
							e.line("movsd %s, %s", r, xmmArgs[in.Imm])
						} else {
							e.line("mov %s, %s", r, argRegs[in.Imm])
						}
						i = j
						continue
					}
				}
			}
		}
		if in.Op == OpParam && j >= 0 && f.Code[j].Op == OpStore && f.Code[j].A == in.Dst &&
			onceSlots[f.Code[j].Imm] {
			if _, kept := e.promoted[f.Code[j].Imm]; !kept {
				if _, aliased := e.alias[in.Dst]; !aliased {
					// A parameter stored into a local that nothing writes
					// again lives in that local's slot: it arrives there,
					// rather than in a frame word of its own and then a
					// copy.
					e.alias[in.Dst] = e.slotAddr(f.Code[j].Imm)
					e.instr(in)
					i = j
					continue
				}
			}
		}
		if j >= 0 && in.Dst != NoReg && uses[in.Dst] == 1 && e.homes != nil && simpleIntDef(in) &&
			int(in.Dst) < len(f.RegTypes) && f.RegTypes[in.Dst].k != kFloat {
			if _, aliased := e.alias[in.Dst]; !aliased {
				// A value whose one reader is the very next instruction,
				// a return or a call taking it among the first four
				// arguments, is worked out in the register that reader
				// wants: no copy into rax or rcx afterwards.
				nx := f.Code[j]
				if nx.Op == OpRet && nx.A == in.Dst && e.f.Ret.k != kFloat {
					e.alias[in.Dst] = "rax"
				} else if nx.Op == OpCall && len(nx.Args) <= 4 {
					// rcx and rdx only: r8 and r9 are pool registers too,
					// and another argument may still be waiting in one.
					for ai, a := range nx.Args {
						if ai < 2 && a == in.Dst && nx.ArgTypes[ai].k != kFloat {
							e.alias[in.Dst] = argRegs[ai]
						}
					}
				}
			}
		}
		if j >= 0 && in.Op == OpFMul && fmaOn() && uses[in.Dst] == 1 && fmaUser(f.Code[j], in.Dst) {
			e.emitFMA(in, f.Code[j])
			i = j
			continue
		}
		if j >= 0 && e.fuseFloatBranch(in, f.Code[j], uses) {
			i = j
			continue
		}
		if j >= 0 && e.fuseBranch(in, f.Code[j], uses) {
			i = j
			continue
		}
		if j >= 0 && e.fuseIndex(in, f.Code[j], uses) {
			i = j
			continue
		}
		if k >= 0 && e.fuseRMW(in, f.Code[j], f.Code[k], uses) {
			i = k
			continue
		}
		if j >= 0 && e.aliasLoad(in, f.Code[j], uses) {
			continue
		}
		if j >= 0 && e.intoSlot(f, j, in, f.Code[j], uses) {
			i = j
			continue
		}
		e.instr(in)
	}

	// Fallthrough epilogue, for a void function with no explicit return.
	// The lowerer ends nearly every function with a return of its own,
	// and then nothing can reach this; it is written only when the last
	// instruction could fall through.
	if n := len(f.Code); n == 0 || !endsFlow(f.Code[n-1].Op) {
		e.comment("epilogue")
		e.line("xor eax, eax")
		e.epilogue()
	}
	e.b.WriteString(e.cold.String())
}

// fixedSlots lists the promoted slots that are written once, before
// the first label, and so hold the same value at every load - a
// parameter nothing assigns to, most often. A load of one need not copy
// the register into the loaded value's own home: the register survives
// calls, so every use can read it directly, where the copy would have
// had to go to the frame to outlive the first call.
func (e *Emitter) fixedSlots(f *Func) map[int64]string {
	if len(e.promoted) == 0 {
		return nil
	}
	stores := map[int64]int{}
	early := map[int64]bool{}
	label := false
	for _, in := range f.Code {
		switch in.Op {
		case OpLabel:
			label = true
		case OpStore:
			stores[in.Imm]++
			if !label {
				early[in.Imm] = true
			}
		case OpSlotAddr:
			stores[in.Imm] += 2
		}
	}
	out := map[int64]string{}
	for slot, r := range e.promoted {
		if stores[slot] == 1 && early[slot] && isGPR(r) {
			out[slot] = r
		}
	}
	return out
}

// intoSlot computes an arithmetic result straight into the register of
// the promoted slot it is stored to, when that store is its only use:
// `j += i` is one add to the slot's register, where it was a copy out,
// the add, and a copy back.
func (e *Emitter) intoSlot(f *Func, at int, op, st Instr, uses map[Reg]int) bool {
	switch op.Op {
	case OpAdd, OpSub, OpBAnd, OpBOr, OpBXor:
	default:
		return false
	}
	if e.consts == nil || st.Op != OpStore || st.A != op.Dst {
		return false
	}
	r, ok := e.promoted[st.Imm]
	if !ok || !isGPR(r) {
		return false
	}
	// Read again later - by a loop's test, say - it is still in the
	// register, if nothing stores to the slot first.
	if uses[op.Dst] != 1 && !e.slotHolds(f, at, e.lastUse[op.Dst], st.Imm) {
		return false
	}
	e.alias[op.Dst] = r
	e.instr(op)
	return true
}

// steadyLoads marks the loads of a slot promoted into a general
// register that need not copy it: the slot is not written again before
// the loaded value's last use, and no label on the way lets control in
// from elsewhere (the rule spanClear applies). Every use can then read
// the slot's own register - `i < n` in a loop compares rbx with r12
// rather than first copying rbx.
func (e *Emitter) steadyLoads(f *Func) map[int]bool {
	if len(e.promoted) == 0 {
		return nil
	}
	last := map[Reg]int{}
	for i, in := range f.Code {
		for _, r := range append([]Reg{in.A, in.B}, in.Args...) {
			if r != NoReg {
				last[r] = i
			}
		}
	}
	out := map[int]bool{}
	for i, in := range f.Code {
		if in.Op != OpLoad {
			continue
		}
		r, ok := e.promoted[in.Imm]
		if !ok || !isGPR(r) {
			continue
		}
		end, used := last[in.Dst]
		if !used || end <= i {
			continue
		}
		// The last use itself may be the store back into the slot,
		// which reads the value before it writes.
		out[i] = e.slotHolds(f, i, end, in.Imm)
	}
	return out
}

// slotHolds reports whether a slot keeps the value it has just after
// instruction from until instruction to reads it: no store to it in
// between, and no label control could reach from outside that stretch.
func (e *Emitter) slotHolds(f *Func, from, to int, slot int64) bool {
	for p := from + 1; p < to; p++ {
		q := f.Code[p]
		switch q.Op {
		case OpStore:
			if q.Imm == slot {
				return false
			}
		case OpLabel:
			for _, j := range e.jumps[q.Imm] {
				if j <= from || j >= p {
					return false
				}
			}
		}
	}
	return true
}

// checkIndex is OpCheckIndex: 0 <= A < B as one unsigned compare, since
// a negative index read unsigned is larger than any length. The stop
// is written to the cold block, reading A and B from where they are
// here - nothing runs between the jump and it.
func (e *Emitter) checkIndex(in Instr) {
	a, b := e.loc(in.A), e.loc(in.B)
	reg := a
	if !isGPR(a) {
		e.line("mov rax, %s", a)
		reg = "rax"
	}
	if n, err := strconv.ParseInt(b, 10, 64); err == nil && (n < -1<<31 || n >= 1<<31) {
		e.line("mov rcx, %s", b)
		b = "rcx"
	}
	e.nCold++
	label := fmt.Sprintf(".Lbad_%s_%d", e.labelBase(), e.nCold)
	e.line("cmp %s, %s", reg, b)
	e.line("jae %s", label)

	main := e.b
	e.b = e.cold
	e.label(label)
	e.boundsCall(in, a, b)
	e.cold = e.b
	e.b = main
}

// isGPR reports whether an operand is a 64-bit general register.
func isGPR(s string) bool {
	switch s {
	case "rax", "rbx", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10",
		"r11", "r12", "r13", "r14", "r15":
		return true
	}
	return false
}

func (e *Emitter) instr(in Instr) {
	if in.Comment != "" {
		e.comment(in.Comment)
	}
	if e.selInstr(in) || e.emitFixed(in) {
		return
	}

	switch in.Op {
	case OpConst:
		e.line("mov rax, %d", in.Imm)
		e.put(in.Dst, "rax")

	case OpStr:
		e.line("lea rax, __str%d[rip]", in.Imm)
		e.put(in.Dst, "rax")

	case OpStackPtr:
		e.line("mov rax, rsp")
		e.put(in.Dst, "rax")

	case OpSymAddr:
		e.line("lea rax, %s[rip]", in.Sym)
		e.put(in.Dst, "rax")

	case OpAsm:
		e.asm(e.f.Asm[in.Imm])

	case OpFrameAddr:
		// The array area sits just above the outgoing arguments, and
		// rsp stays put for the whole body, so it is rsp-relative with
		// or without a frame pointer.
		e.line("lea rax, [rsp+%d]", int64(e.outgoing())+in.Imm)
		e.put(in.Dst, "rax")

	case OpSlotAddr:
		e.line("lea rax, %s", strings.TrimPrefix(e.slotAddr(in.Imm), "qword ptr "))
		e.put(in.Dst, "rax")

	case OpGlobalAddr:
		// Static storage, so the address is a link-time constant and the
		// slot's offset folds into the lea rather than needing an add.
		e.line("lea rax, __globals[rip+%d]", in.Imm*wordSize)
		e.put(in.Dst, "rax")

	case OpLoad:
		if d := e.loc(in.Dst); d == e.regAddr(in.Dst) {
			e.line("mov rax, %s", e.slotAddr(in.Imm))
			e.line("mov %s, rax", d)
		} else {
			// The value is going to a register anyway; one move reads it
			// straight there.
			e.line("mov %s, %s", d, e.slotAddr(in.Imm))
		}

	case OpStore:
		e.line("mov rax, %s", e.loc(in.A))
		e.line("mov %s, rax", e.slotAddr(in.Imm))

	case OpAdd, OpSub, OpMul, OpBAnd, OpBOr, OpBXor:
		mnemonic := map[Op]string{
			OpAdd: "add", OpSub: "sub", OpMul: "imul",
			OpBAnd: "and", OpBOr: "or", OpBXor: "xor",
		}[in.Op]
		e.line("mov rax, %s", e.loc(in.A))
		if b := e.loc(in.B); b != "rcx" {
			e.line("mov rcx, %s", b)
		}
		e.line("%s rax, rcx", mnemonic)
		e.put(in.Dst, "rax")

	case OpShl, OpShr:
		// The variable shift forms take their count in cl and nowhere
		// else, which is why the count is loaded second and into rcx
		// specifically. sar rather than shr: Scandiacus integers are signed,
		// so -8 >> 1 has to stay negative.
		mnemonic := "shl"
		if in.Op == OpShr {
			mnemonic = "sar"
		}
		e.line("mov rax, %s", e.loc(in.A))
		if b := e.loc(in.B); b != "rcx" {
			e.line("mov rcx, %s", b)
		}
		e.line("%s rax, cl", mnemonic)
		e.put(in.Dst, "rax")

	case OpDiv, OpMod:
		// idiv divides rdx:rax and is the one instruction here with
		// fixed register operands. cqo sign-extends rax into rdx first;
		// without it a negative dividend divides by a garbage high half.
		// Quotient lands in rax, remainder in rdx.
		e.line("mov rax, %s", e.loc(in.A))
		if b := e.loc(in.B); b != "rcx" {
			e.line("mov rcx, %s", b)
		}
		e.line("cqo")
		e.line("idiv rcx")
		if in.Op == OpDiv {
			e.put(in.Dst, "rax")
		} else {
			e.put(in.Dst, "rdx")
		}

	case OpNeg:
		e.line("mov rax, %s", e.loc(in.A))
		e.line("neg rax")
		e.put(in.Dst, "rax")

	case OpFConst:
		e.line("movsd xmm0, __flt%d[rip]", in.Imm)
		e.putf(in.Dst, "xmm0")

	case OpFAdd, OpFSub, OpFMul, OpFDiv:
		mnemonic := map[Op]string{
			OpFAdd: "addsd", OpFSub: "subsd", OpFMul: "mulsd", OpFDiv: "divsd",
		}[in.Op]
		e.line("movsd xmm0, %s", e.loc(in.A))
		if b := e.loc(in.B); b != "xmm1" {
			e.line("movsd xmm1, %s", b)
		}
		e.line("%s xmm0, xmm1", mnemonic)
		e.putf(in.Dst, "xmm0")

	case OpFNeg:
		// mulsd by -1.0 rather than flipping the sign bit: see the
		// comment on __neg_one in Emit for why xorpd was not worth it.
		if d, a := e.loc(in.Dst), e.loc(in.A); isXmm(d) {
			// The constant is the multiply's memory operand, so it
			// needs no register and no separate load.
			if d != a {
				e.line("movsd %s, %s", d, a)
			}
			e.line("mulsd %s, __neg_one[rip]", d)
			break
		}
		e.line("movsd xmm0, %s", e.loc(in.A))
		e.line("mulsd xmm0, __neg_one[rip]")
		e.putf(in.Dst, "xmm0")

	case OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe:
		// comisd sets the flags the way an unsigned integer compare
		// does - CF for below, ZF for equal - and sets all three of CF,
		// ZF and PF when either side is a NaN. Every comparison with a
		// NaN has to come out false except !=, so each one is chosen to
		// be false on that pattern:
		//
		//   a > b    seta, which wants CF and ZF both clear
		//   a >= b   setae, which wants CF clear
		//   a < b    the same as b > a, operands swapped
		//   a <= b   the same as b >= a
		//   a == b   ZF set and PF clear
		//   a != b   ZF clear or PF set
		//
		// setb and setbe would read an unordered pair as below, which
		// is why the swap rather than the obvious condition code.
		x, y := in.A, in.B
		if in.Op == OpFLt || in.Op == OpFLe {
			x, y = y, x
		}
		e.line("movsd xmm0, %s", e.loc(x))
		e.line("movsd xmm1, %s", e.loc(y))
		e.line("xor eax, eax")
		switch in.Op {
		case OpFEq, OpFNe:
			e.line("xor ecx, ecx")
			e.line("comisd xmm0, xmm1")
			if in.Op == OpFEq {
				e.line("sete al")
				e.line("setnp cl")
				e.line("and eax, ecx")
			} else {
				e.line("setne al")
				e.line("setp cl")
				e.line("or eax, ecx")
			}
		case OpFGt, OpFLt:
			e.line("comisd xmm0, xmm1")
			e.line("seta al")
		default:
			e.line("comisd xmm0, xmm1")
			e.line("setae al")
		}
		e.put(in.Dst, "rax")

	case OpIntToFloat:
		e.line("mov rax, %s", e.loc(in.A))
		e.line("cvtsi2sd xmm0, rax")
		e.putf(in.Dst, "xmm0")

	case OpFloatToInt:
		// cvttsd2si: the double-t is truncating rather than
		// round-to-nearest, which is what Scandiacus's int() promises and
		// what int(-3.7) being -3 rather than -4 depends on.
		e.line("movsd xmm0, %s", e.loc(in.A))
		e.line("cvttsd2si rax, xmm0")
		e.put(in.Dst, "rax")

	case OpSqrt:
		e.line("movsd xmm0, %s", e.loc(in.A))
		e.line("sqrtsd xmm0, xmm0")
		e.putf(in.Dst, "xmm0")

	case OpFMod:
		// fmod has a fixed double,double prototype rather than a
		// variadic one, so unlike the printf/snprintf calls elsewhere
		// in this file its float arguments go in xmm0/xmm1 alone - no
		// duplication into the integer registers is needed here.
		e.line("movsd xmm0, %s", e.loc(in.A))
		e.line("movsd xmm1, %s", e.loc(in.B))
		e.line("call fmod")
		e.putf(in.Dst, "xmm0")

	case OpBNot:
		e.line("mov rax, %s", e.loc(in.A))
		e.line("not rax")
		e.put(in.Dst, "rax")

	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
		// cmp sets the flags; setcc turns one flag combination into a
		// single 0 or 1 byte. setcc writes only 8 bits, so the register
		// is zeroed first - otherwise the upper 56 bits keep whatever
		// the last operation left there, and a "true" compares unequal
		// to 1. The zeroing comes before cmp, because xor writes flags
		// of its own.
		//
		// The signed forms are correct because Scandiacus integers are signed.
		cc := map[Op]string{
			OpEq: "e", OpNe: "ne", OpLt: "l", OpLe: "le", OpGt: "g", OpGe: "ge",
		}[in.Op]
		e.line("mov rax, %s", e.loc(in.A))
		if b := e.loc(in.B); b != "rcx" {
			e.line("mov rcx, %s", b)
		}
		e.line("xor edx, edx")
		e.line("cmp rax, rcx")
		e.line("set%s dl", cc)
		e.put(in.Dst, "rdx")

	case OpNot:
		e.line("mov rax, %s", e.loc(in.A))
		e.line("xor edx, edx")
		e.line("test rax, rax")
		e.line("sete dl")
		e.put(in.Dst, "rdx")

	case OpLabel:
		e.label(fmt.Sprintf(".L%s_%d", e.labelBase(), in.Imm))

	case OpJump:
		e.line("jmp .L%s_%d", e.labelBase(), in.Imm)

	case OpJumpIf:
		e.line("mov rax, %s", e.loc(in.A))
		e.line("test rax, rax")
		e.line("jne .L%s_%d", e.labelBase(), in.Imm)

	case OpJumpNot:
		e.line("mov rax, %s", e.loc(in.A))
		e.line("test rax, rax")
		e.line("je .L%s_%d", e.labelBase(), in.Imm)

	case OpParam:
		if in.Imm < 4 {
			// Which register holds this parameter depends only on its
			// own position and its own type - a float in position 0
			// arrives in xmm0 whether or not any earlier parameter was
			// also a float, because the Windows x64 convention keeps
			// the integer and floating-point register files in lock
			// step by position rather than counting each kind
			// separately.
			if e.f.ParamTypes[in.Imm].k == kFloat {
				e.putf(in.Dst, xmmArgs[in.Imm])
			} else {
				e.put(in.Dst, argRegs[in.Imm])
			}
			return
		}
		// Arguments past the fourth sit in the caller's outgoing area.
		// Working out where, from in here: at the call rsp was R and the
		// caller wrote argument i at [R+i*8], with 0..3 being the shadow
		// space it reserves but does not fill. call pushed the return
		// address and the prologue pushed rbp, so rbp is R-16 and
		// argument i is at [rbp+16+i*8] - argument 4 at rbp+48. With
		// no rbp pushed, R-8 is where frameRef counts from.
		//
		// Getting this wrong is quiet: the first four arguments still
		// arrive correctly, so a six-argument function returns garbage
		// while every smaller one looks fine.
		if e.frameless {
			e.line("mov rax, qword ptr %s", e.frameRef(-8-in.Imm*8))
		} else {
			e.line("mov rax, qword ptr [rbp+%d]", 16+in.Imm*8)
		}
		e.put(in.Dst, "rax")

	case OpCall:
		e.call(in)

	case OpCallClosure:
		e.callClosure(in)

	case OpCallAddr:
		e.callAddr(in)

	case OpGCPoll:
		e.polls++
		skip := fmt.Sprintf(".L%s_poll%d", e.labelBase(), e.polls)
		e.line("lea rcx, __globals[rip]")
		e.line("mov rax, qword ptr [rcx+%d]", gcBytesSlot*wordSize)
		e.line("cmp rax, qword ptr [rcx+%d]", gcNextSlot*wordSize)
		e.line("jle %s", skip)
		e.line("call %s", fnSym(in.Sym))
		e.label(skip)

	case OpAtomicAdd, OpAtomicSwap:
		e.line("mov rcx, %s", e.loc(in.A))
		e.line("mov rax, %s", e.loc(in.B))
		if in.Op == OpAtomicAdd {
			e.line("lock xadd qword ptr [rcx], rax")
		} else {
			e.line("xchg qword ptr [rcx], rax")
		}
		if in.Dst != NoReg {
			e.put(in.Dst, "rax")
		}

	case OpAtomicCAS:
		e.line("mov rcx, %s", e.loc(in.Args[0]))
		e.line("mov rax, %s", e.loc(in.Args[1]))
		e.line("mov rdx, %s", e.loc(in.Args[2]))
		e.line("lock cmpxchg qword ptr [rcx], rdx")
		e.line("sete al")
		e.line("movzx eax, al")
		if in.Dst != NoReg {
			e.put(in.Dst, "rax")
		}

	case OpRet:
		switch {
		case in.A != NoReg && e.f.Ret.k == kFloat:
			e.line("movsd xmm0, %s", e.loc(in.A))
		case in.A != NoReg:
			if src := e.loc(in.A); src != "rax" {
				e.line("mov rax, %s", src)
			}
		default:
			e.line("xor eax, eax")
		}
		e.epilogue()

	case OpPrintInt:
		e.line("lea rcx, __fmt_int[rip]")
		e.line("mov rdx, %s", e.loc(in.A))
		e.line("call printf")

	case OpPrintFloat:
		// Via __vy_floattostr rather than handing the double to printf
		// directly, which would put it in the vararg position that the
		// Windows ABI requires to be duplicated into an integer register
		// as well as xmm1 - the same complication OpFloatToStr already
		// has to solve once, inside the helper, rather than solving it
		// again at every call site that wants to print a float.
		e.line("movsd xmm0, %s", e.loc(in.A))
		e.line("call __vy_floattostr")
		e.line("lea rcx, __fmt_str[rip]")
		e.line("mov rdx, rax")
		e.line("call printf")

	case OpPrintStr:
		e.line("mov rcx, %s", e.loc(in.A))
		e.line("call __vy_printstr")

	case OpPrintBool:
		// cmov rather than a branch: it picks between the two string
		// pointers with no jump. The false pointer is loaded first so
		// cmovne can overwrite it, since cmov cannot take an address as
		// its source operand.
		e.line("mov rax, %s", e.loc(in.A))
		e.line("lea rcx, __str_false[rip]")
		e.line("lea rdx, __str_true[rip]")
		e.line("test rax, rax")
		e.line("cmovne rcx, rdx")
		e.line("mov rdx, rcx")
		e.line("lea rcx, __fmt_str[rip]")
		e.line("call printf")

	case OpConcat:
		e.line("mov rcx, %s", e.loc(in.A))
		e.line("mov rdx, %s", e.loc(in.B))
		e.line("call __vy_concat")
		e.put(in.Dst, "rax")

	case OpStrEq:
		e.line("mov rcx, %s", e.loc(in.A))
		e.line("mov rdx, %s", e.loc(in.B))
		e.line("call __vy_streq")
		e.put(in.Dst, "rax")

	case OpStrLen:
		// The length is in the header, not found by scanning: O(1), and
		// a zero byte inside the string does not end it.
		e.line("mov rcx, %s", e.loc(in.A))
		e.line("mov rax, qword ptr [rcx-16]")
		e.line("shr rax, %d", tagShift)
		e.line("sub rax, 1")
		e.put(in.Dst, "rax")

	case OpIntToStr:
		e.line("mov rcx, %s", e.loc(in.A))
		e.line("call __vy_inttostr")
		e.put(in.Dst, "rax")

	case OpFloatToStr:
		e.line("movsd xmm0, %s", e.loc(in.A))
		e.line("call __vy_floattostr")
		e.put(in.Dst, "rax")

	case OpBoolToStr:
		e.line("mov rax, %s", e.loc(in.A))
		e.line("lea rcx, __str_false[rip]")
		e.line("lea rdx, __str_true[rip]")
		e.line("test rax, rax")
		e.line("cmovne rcx, rdx")
		e.put(in.Dst, "rcx")

	case OpAlloc:
		e.line("mov rcx, %s", e.loc(in.A))
		e.line("call malloc")
		e.put(in.Dst, "rax")

	case OpIndexAddr:
		// lea with a scale of 8 is one instruction for base + index*8,
		// which is the whole reason every element is a word wide.
		e.line("mov rax, %s", e.loc(in.A))
		if b := e.loc(in.B); b != "rcx" {
			e.line("mov rcx, %s", b)
		}
		e.line("lea rax, [rax+rcx*%d]", indexScale(in))
		e.put(in.Dst, "rax")

	case OpLoadMem:
		e.line("mov rax, %s", e.loc(in.A))
		e.line("mov rax, qword ptr [rax+%d]", in.Imm)
		e.put(in.Dst, "rax")

	case OpStoreMem:
		e.line("mov rax, %s", e.loc(in.A))
		if b := e.loc(in.B); b != "rcx" {
			e.line("mov rcx, %s", b)
		}
		e.line("mov qword ptr [rax+%d], rcx", in.Imm)

	case OpWriteStr:
		e.line("lea rcx, __fmt_str_raw[rip]")
		e.line("mov rdx, %s", e.loc(in.A))
		e.line("call printf")

	case OpWriteInt:
		e.line("lea rcx, __fmt_int_raw[rip]")
		e.line("mov rdx, %s", e.loc(in.A))
		e.line("call printf")

	case OpWriteFloat:
		e.line("movsd xmm0, %s", e.loc(in.A))
		e.line("call __vy_floattostr")
		e.line("lea rcx, __fmt_str_raw[rip]")
		e.line("mov rdx, rax")
		e.line("call printf")

	case OpPeek:
		e.line("mov rax, %s", e.loc(in.A))
		switch in.Imm {
		case memU8:
			e.line("movzx eax, byte ptr [rax]")
		case memI8:
			e.line("movsx rax, byte ptr [rax]")
		case memU16:
			e.line("movzx eax, word ptr [rax]")
		case memI16:
			e.line("movsx rax, word ptr [rax]")
		case memU32:
			// A 32-bit move clears the upper half by itself.
			e.line("mov eax, dword ptr [rax]")
		case memI32:
			e.line("movsxd rax, dword ptr [rax]")
		case memF32:
			e.line("cvtss2sd xmm0, dword ptr [rax]")
			e.putf(in.Dst, "xmm0")
			return
		case memF64:
			e.line("movsd xmm0, qword ptr [rax]")
			e.putf(in.Dst, "xmm0")
			return
		default:
			e.line("mov rax, qword ptr [rax]")
		}
		e.put(in.Dst, "rax")

	case OpPoke:
		e.line("mov rax, %s", e.loc(in.A))
		switch in.Imm {
		case memF32:
			e.line("movsd xmm0, %s", e.loc(in.B))
			e.line("cvtsd2ss xmm0, xmm0")
			e.line("movss dword ptr [rax], xmm0")
			return
		case memF64:
			e.line("movsd xmm0, %s", e.loc(in.B))
			e.line("movsd qword ptr [rax], xmm0")
			return
		}
		if b := e.loc(in.B); b != "rcx" {
			e.line("mov rcx, %s", b)
		}
		switch in.Imm {
		case memU8, memI8:
			e.line("mov byte ptr [rax], cl")
		case memU16, memI16:
			e.line("mov word ptr [rax], cx")
		case memU32, memI32:
			e.line("mov dword ptr [rax], ecx")
		default:
			e.line("mov qword ptr [rax], rcx")
		}

	case OpLoadByte:
		// movzx, not mov: reading a byte into a 64-bit register has to
		// clear the upper bits explicitly, or the value carries whatever
		// the last operation left above bit 8.
		e.line("mov rax, %s", e.loc(in.A))
		if b := e.loc(in.B); b != "rcx" {
			e.line("mov rcx, %s", b)
		}
		e.line("movzx eax, byte ptr [rax+rcx]")
		e.put(in.Dst, "rax")

	case OpStoreByte:
		// Three operands, so the value comes from the slot named by Imm
		// rather than from a third register field.
		e.line("mov rax, %s", e.loc(in.A))
		if b := e.loc(in.B); b != "rcx" {
			e.line("mov rcx, %s", b)
		}
		e.line("mov rdx, %s", e.slotAddr(in.Imm))
		e.line("mov byte ptr [rax+rcx], dl")

	case OpCheckIndex:
		e.checkIndex(in)

	case OpVLoad, OpVStore:
		base := e.loc(in.A)
		if !isGPR(base) {
			e.line("mov rax, %s", base)
			base = "rax"
		}
		if in.Op == OpVLoad {
			e.line("%s %s, [%s%s]", vecMov(in.Imm), vecReg(in.Imm, in.Imm&15), base, vecDisp(in.Imm))
		} else {
			e.line("%s [%s%s], %s", vecMov(in.Imm), base, vecDisp(in.Imm), vecReg(in.Imm, in.Imm&15))
		}

	case OpVSplat:
		r := in.Imm &^ vecWide
		src := e.loc(in.A)
		if in.Imm&vecWide != 0 {
			// vbroadcastsd takes a register only from AVX2 on, which
			// the wide loop needs anyway; see vecloop.go.
			if !isXmm(src) && !strings.HasPrefix(src, "qword ptr") {
				src = "qword ptr " + src
			}
			e.line("vbroadcastsd ymm%d, %s", r, src)
			break
		}
		// From a register, movapd: movsd between registers keeps the
		// upper half of the destination, which made each trip wait on
		// the result the last one left in it.
		if isXmm(src) {
			e.line("movapd xmm%d, %s", r, src)
		} else {
			e.line("movsd xmm%d, %s", r, src)
		}
		e.line("unpcklpd xmm%d, xmm%d", r, r)

	case OpVArith:
		op, a, b := in.Imm&^vecWide>>8, in.Imm>>4&15, in.Imm&15
		if in.Imm&vecWide != 0 {
			m := [...]string{"vaddpd", "vsubpd", "vmulpd"}[op]
			e.line("%s ymm%d, ymm%d, ymm%d", m, a, a, b)
			break
		}
		m := [...]string{"addpd", "subpd", "mulpd"}[op]
		e.line("%s xmm%d, xmm%d", m, a, b)

	case OpVZeroUpper:
		e.line("vzeroupper")

	case OpBoundsFail:
		e.boundsCall(in, e.loc(in.A), e.loc(in.B))

	case OpMustFail:
		e.where(in)
		e.line("mov rcx, %s", e.loc(in.A))
		e.line("call __vy_must")

	default:
		e.comment(fmt.Sprintf("unhandled op %s", in.Op))
	}
}

// call places the arguments and jumps.
//
// Every argument is read out of wherever it lives - slot or pooled
// register - into the right position. The stack arguments go first,
// then the register window, which is what keeps a source read from
// being walked over by a target written before it; loadWindowArgs has
// the one remaining ordering subtlety.
func (e *Emitter) call(in Instr) {
	// Beyond the fourth argument, everything - float or int - is just
	// eight raw bytes written to its stack slot. There is exactly one
	// storage location at that position, so unlike the register window
	// there is no float/int choice to make and no duplication rule to
	// honour; a plain integer-register move carries the bits unchanged
	// either way.
	for i := len(in.Args) - 1; i >= 4; i-- {
		e.stackArg(i, e.loc(in.Args[i]), "rax")
	}
	e.loadWindowArgs(in)
	if in.Extern {
		e.line("call %s", in.Sym)
	} else {
		e.line("call %s", fnSym(in.Sym))
	}
	e.callResult(in)
}

// loadWindowArgs fills rcx, rdx, r8 and r9 with the first four
// arguments, each from wherever its virtual register lives.
//
// Position decides which file an argument uses: its own type, not how
// many floats or ints came before it. A variadic float goes in both
// files, because a callee walking a va_list cannot know what it was
// handed; omitting that is quiet in the worst way, since printf("%d")
// still works and printf("%f") prints garbage.
//
// The ordering matters once an argument already sits in r8 or r9 - the
// two registers both the pool and the window claim. Writing one can
// destroy a value another move still wants to read, and with chains
// like rdx<-r8<-r9 there is no fixed order that works. The moves go
// through a small scheduler instead: emit any move whose target no
// remaining move reads, because clobbering an unread register is free.
// Only an r8/r9 pair each holding what the other wants can deadlock,
// and xchg settles that.
func (e *Emitter) loadWindowArgs(in Instr) {
	n := len(in.Args)
	if n > 4 {
		n = 4
	}

	risky := false
	for i := 0; i < n; i++ {
		switch src := e.loc(in.Args[i]); src {
		case "r8", "r9":
			risky = true
		}
	}
	if !risky {
		for i := 0; i < n; i++ {
			src := e.loc(in.Args[i])
			if in.ArgTypes[i].k == kFloat {
				e.line("movsd %s, %s", xmmArgs[i], src)
				if in.Variadic && src != argRegs[i] {
					e.line("mov %s, %s", argRegs[i], src)
				}
			} else if src != argRegs[i] {
				e.line("mov %s, %s", argRegs[i], src)
			}
		}
		return
	}

	type move struct{ dst, src string }
	var pending []move
	for i := 0; i < n; i++ {
		src := e.loc(in.Args[i])
		dst := argRegs[i]
		if in.ArgTypes[i].k == kFloat {
			e.line("movsd %s, %s", xmmArgs[i], src)
			// The copy in the integer register waits its turn with the
			// rest: written now, it could land on r8 or r9 while another
			// argument still has to be read out of it.
			if in.Variadic && src != dst {
				pending = append(pending, move{dst, src})
			}
			continue
		}
		if src != dst {
			pending = append(pending, move{dst, src})
		}
	}
	for len(pending) > 0 {
		ready := -1
		for i, m := range pending {
			read := false
			for j, o := range pending {
				if j != i && o.src == m.dst {
					read = true
					break
				}
			}
			if !read {
				ready = i
				break
			}
		}
		if ready < 0 {
			// Every remaining move writes a register another one is
			// still waiting to read. rcx and rdx are never sources, so
			// the only cycle two window registers can form is a swap.
			e.line("xchg r8, r9")
			kept := pending[:0]
			for _, m := range pending {
				switch m.src {
				case "r8":
					m.src = "r9"
				case "r9":
					m.src = "r8"
				}
				if m.src != m.dst {
					kept = append(kept, m)
				}
			}
			pending = kept
			continue
		}
		e.line("mov %s, %s", pending[ready].dst, pending[ready].src)
		pending = append(pending[:ready], pending[ready+1:]...)
	}
}

func (e *Emitter) callResult(in Instr) {
	if in.Dst == NoReg {
		return
	}
	if in.Ret32 {
		e.line("movsxd rax, eax")
	}
	if in.RetType.k == kFloat {
		e.putf(in.Dst, "xmm0")
	} else {
		e.put(in.Dst, "rax")
	}
}

// stackArg writes argument i, read from src, to its place in the
// outgoing area, by way of scratch. A float the allocator kept in an
// xmm register goes there with one movsd: "mov rax, xmm12" is not an
// instruction, and a sixth argument of type float in a function with
// enough float values to fill the pool used to stop the build.
func (e *Emitter) stackArg(i int, src, scratch string) {
	if isXmm(src) {
		e.line("movsd qword ptr [rsp+%d], %s", i*8, src)
		return
	}
	e.line("mov %s, %s", scratch, src)
	e.line("mov qword ptr [rsp+%d], %s", i*8, scratch)
}

// callClosure calls through a function value.
//
// With allocation off this emits exactly what it always has, so that
// switching the pass on is the only thing that can move a byte. With it
// on, two orderings change for reasons the old one cannot survive:
//
// The function's address is read into rax first, before anything can
// walk over it: argument setup writes rcx, rdx, r8 and r9, and the
// value being called may itself live in one of those when it was
// pooled. The stack arguments then take their scratch from rcx rather
// than rax, which would destroy the address they exist to serve;
// argument setup never touches rax.
//
// The environment travels in r10 and the entry address in r11, loaded
// after the register window because an argument may itself sit in
// either - they are pool registers like any other, and by the time
// they are overwritten every argument has been read out of them.
//
// r10 carrying the environment is the whole reason a declared function
// can be a function value with no wrapper: a lifted literal reads it
// in its prologue, and a plain function ignores it.
func (e *Emitter) callClosure(in Instr) {
	if e.homes == nil {
		e.comment("closure: environment then code, before the arguments")
		e.line("mov rax, %s", e.regAddr(in.A))
		e.line("mov r10, qword ptr [rax+%d]", cloEnvOff)
		e.line("mov r11, qword ptr [rax+%d]", cloCodeOff)

		for i := len(in.Args) - 1; i >= 4; i-- {
			e.line("mov rax, %s", e.regAddr(in.Args[i]))
			e.line("mov qword ptr [rsp+%d], rax", i*8)
		}
		for i := 0; i < len(in.Args) && i < 4; i++ {
			if in.ArgTypes[i].k == kFloat {
				e.line("movsd %s, %s", xmmArgs[i], e.regAddr(in.Args[i]))
			} else {
				e.line("mov %s, %s", argRegs[i], e.regAddr(in.Args[i]))
			}
		}
		e.line("call r11")
		e.callResult(in)
		return
	}

	e.comment("closure: code to rax first, then arguments, then env and entry")
	e.line("mov rax, %s", e.loc(in.A))

	for i := len(in.Args) - 1; i >= 4; i-- {
		// rax holds the function until the end, so the stack arguments
		// scratch with rcx. Not r10: it is a pool register and may hold
		// one of the four arguments still to be loaded. rcx never holds
		// a value here and is only written once these are done.
		e.stackArg(i, e.loc(in.Args[i]), "rcx")
	}
	e.loadWindowArgs(in)

	e.line("mov r10, qword ptr [rax+%d]", cloEnvOff)
	e.line("mov r11, qword ptr [rax+%d]", cloCodeOff)
	e.line("call r11")
	e.callResult(in)
}

// callAddr calls native code at a computed address. The address goes
// to rax before anything else, for the reason callClosure gives: it may
// sit in a register the arguments are about to overwrite, and argument
// setup never touches rax.
func (e *Emitter) callAddr(in Instr) {
	e.comment("mem.call: the address to rax first, then the arguments")
	e.line("mov rax, %s", e.loc(in.A))
	for i := len(in.Args) - 1; i >= 4; i-- {
		e.stackArg(i, e.loc(in.Args[i]), "rcx")
	}
	e.loadWindowArgs(in)
	e.line("call rax")
	e.callResult(in)
}

// helpers writes the small runtime this backend needs, in assembly, and
// only the parts a program actually uses.
//
// They are written here rather than open-coded at each call site so that
// the byte writer has one copy of each to reproduce, and so that the
// generated assembly stays readable. Each follows the same convention as
// any other function: arguments in rcx and rdx, result in rax, 32 bytes
// of shadow space reserved for whatever it calls.
//
// The three that allocate go through __vy_talloc rather than malloc, so
// that a string built here is on the same object list as everything the
// lowerer allocates. An untracked allocation is not a crash - nothing
// traces into a string - but it is invisible to the collector and to
// mem.used(), which between them are most of what a program allocates.
func (e *Emitter) helpers() {
	// The tracked allocator. rcx is the payload size, rdx the header
	// word; it returns the payload pointer, with the object threaded
	// onto the list at __globals[0] and the counters after it bumped.
	//
	// Emitted whenever anything that allocates is, which is what the
	// three conditions below add up to.
	hasAlloc := false
	for _, f := range e.mod.Funcs {
		if f.Name == "__alloc" {
			hasAlloc = true
		}
	}
	if (e.mod.Helpers["concat"] || e.mod.Helpers["inttostr"] ||
		e.mod.Helpers["floattostr"]) && hasAlloc {
		// Same arguments, same result: small strings become slots in a
		// page like every other object; see heap.go.
		e.b.WriteString(`
__vy_talloc:
    jmp __vy___alloc
`)
	} else if e.mod.Helpers["concat"] || e.mod.Helpers["inttostr"] ||
		e.mod.Helpers["floattostr"] {
		e.b.WriteString(`
__vy_talloc:
    push rbp
    mov rbp, rsp
    sub rsp, 64
    mov qword ptr [rbp-8], rcx
    mov qword ptr [rbp-16], rdx
    mov rcx, qword ptr [rbp-8]
    add rcx, 16
    call malloc
    mov qword ptr [rbp-24], rax
    mov rcx, qword ptr [rbp-16]
    mov qword ptr [rax+0], rcx
    lea rcx, __globals[rip]
    mov qword ptr [rbp-32], 0
    cmp qword ptr [rcx+48], 0
    je __vy_talloc_go
    mov qword ptr [rbp-32], 1
__vy_talloc_spin:
    mov rax, 0
    mov rdx, 1
    lock cmpxchg qword ptr [rcx+64], rdx
    jne __vy_talloc_spin
__vy_talloc_go:
    mov rax, qword ptr [rbp-24]
    mov rdx, qword ptr [rcx+0]
    mov qword ptr [rax+8], rdx
    mov qword ptr [rcx+0], rax
    mov rdx, qword ptr [rcx+8]
    add rdx, 1
    mov qword ptr [rcx+8], rdx
    mov rdx, qword ptr [rcx+16]
    add rdx, qword ptr [rbp-8]
    mov qword ptr [rcx+16], rdx
    mov rdx, qword ptr [rcx+24]
    add rdx, qword ptr [rbp-8]
    mov qword ptr [rcx+24], rdx
    cmp qword ptr [rbp-32], 0
    je __vy_talloc_out
    mov rdx, 0
    xchg qword ptr [rcx+64], rdx
__vy_talloc_out:
    add rax, 16
    mov rsp, rbp
    pop rbp
    ret
`)
	}

	if e.mod.Helpers["concat"] {
		// Both lengths come from the headers and the bytes are copied
		// by count, so a zero byte inside either string is carried over.
		e.b.WriteString(`
__vy_concat:
    push rbp
    mov rbp, rsp
    sub rsp, 64
    mov qword ptr [rbp-8], rcx
    mov qword ptr [rbp-16], rdx
    mov rax, qword ptr [rcx-16]
    shr rax, 8
    sub rax, 1
    mov qword ptr [rbp-24], rax
    mov rax, qword ptr [rdx-16]
    shr rax, 8
    sub rax, 1
    mov qword ptr [rbp-40], rax
    mov rcx, qword ptr [rbp-24]
    add rcx, rax
    add rcx, 1
    mov rdx, rcx
    shl rdx, 8
    call __vy_talloc
    mov qword ptr [rbp-32], rax
    mov rcx, rax
    mov rdx, qword ptr [rbp-8]
    mov r8, qword ptr [rbp-24]
    call memmove
    mov rcx, qword ptr [rbp-32]
    add rcx, qword ptr [rbp-24]
    mov rdx, qword ptr [rbp-16]
    mov r8, qword ptr [rbp-40]
    call memmove
    mov rcx, qword ptr [rbp-32]
    add rcx, qword ptr [rbp-24]
    add rcx, qword ptr [rbp-40]
    xor edx, edx
    mov byte ptr [rcx], dl
    mov rax, qword ptr [rbp-32]
    mov rsp, rbp
    pop rbp
    ret
`)
	}

	if e.mod.Helpers["streq"] {
		// Equal headers mean equal lengths (both are plain bytes); then
		// the bytes, all of them, zero or not.
		e.b.WriteString(`
__vy_streq:
    push rbp
    mov rbp, rsp
    sub rsp, 32
    mov rax, qword ptr [rcx-16]
    mov r8, qword ptr [rdx-16]
    cmp rax, r8
    jne .Lstreq_differ
    shr r8, 8
    sub r8, 1
    call memcmp
    test eax, eax
    mov eax, 0
    sete al
    mov rsp, rbp
    pop rbp
    ret
.Lstreq_differ:
    xor eax, eax
    mov rsp, rbp
    pop rbp
    ret
`)
	}

	if e.mod.Helpers["printstr"] {
		// One printf when the string has no zero byte in it, which is
		// nearly always; otherwise the pieces between them, each zero
		// written with %c, which printf passes through.
		e.b.WriteString(`
__vy_printstr:
    push rbp
    mov rbp, rsp
    sub rsp, 64
    mov qword ptr [rbp-8], rcx
    mov rax, qword ptr [rcx-16]
    shr rax, 8
    sub rax, 1
    mov qword ptr [rbp-16], rax
    call strlen
    mov rcx, qword ptr [rbp-16]
    cmp rax, rcx
    jne .Lps_pieces
    lea rcx, __fmt_str[rip]
    mov rdx, qword ptr [rbp-8]
    call printf
    mov rsp, rbp
    pop rbp
    ret
.Lps_pieces:
    mov qword ptr [rbp-24], 0
.Lps_loop:
    lea rcx, __fmt_s[rip]
    mov rdx, qword ptr [rbp-8]
    add rdx, qword ptr [rbp-24]
    call printf
    mov rcx, qword ptr [rbp-8]
    add rcx, qword ptr [rbp-24]
    call strlen
    add rax, qword ptr [rbp-24]
    mov qword ptr [rbp-24], rax
    mov rcx, qword ptr [rbp-16]
    cmp rax, rcx
    jge .Lps_end
    lea rcx, __fmt_c[rip]
    xor edx, edx
    call printf
    mov rax, qword ptr [rbp-24]
    add rax, 1
    mov qword ptr [rbp-24], rax
    jmp .Lps_loop
.Lps_end:
    lea rcx, __fmt_nl[rip]
    call printf
    mov rsp, rbp
    pop rbp
    ret
`)
	}

	if e.mod.Helpers["bounds"] {
		// Writes to file descriptor 2 with _write rather than fprintf,
		// because reaching stderr through a FILE* means __acrt_iob_func
		// on UCRT and __iob_func on msvcrt, and picking the wrong one is
		// a link error on somebody else's machine. A descriptor is a
		// descriptor everywhere.
		e.b.WriteString(`
__vy_bounds:
    push rbp
    mov rbp, rsp
    sub rsp, 256
    # r8 is where the failed index was, or zero; see boundsCall.
    test r8, r8
    je .Lbounds_nowhere
    lea rax, __globals[rip]
    mov qword ptr [rax+72], r8
.Lbounds_nowhere:
    mov qword ptr [rbp-208], rcx
    mov qword ptr [rbp-216], rdx
    xor ecx, ecx
    call fflush
    lea rcx, [rbp-200]
    mov rdx, 180
    lea r8, __fmt_bounds[rip]
    mov r9, qword ptr [rbp-208]
    mov rax, qword ptr [rbp-216]
    mov qword ptr [rsp+32], rax
    call _snprintf
    mov r8d, eax
    mov ecx, 2
    lea rdx, [rbp-200]
    call _write
    call __vy_where
    mov ecx, 1
    call exit
`)
	}

	if e.mod.Helpers["ccb"] {
		e.b.WriteString(ccbRoutines())
	}

	if e.mod.Helpers["cpufeat"] {
		// 2 in the runtime's cpu word if the wide packed loops can run,
		// 1 if not. AVX2 is CPUID leaf 7 EBX bit 5, but the ymm
		// registers are usable only if the OS saves them on a thread
		// switch: CPUID leaf 1 ECX bits 27 (OSXSAVE) and 28 (AVX), then
		// XGETBV says it saves both the xmm and the ymm state. rbx is
		// saved because cpuid writes it and both ABIs preserve it.
		fmt.Fprintf(&e.b, `
__vy_cpufeat:
    push rbx
    mov r8, 1
    mov eax, 1
    xor ecx, ecx
    cpuid
    mov eax, ecx
    and eax, 402653184
    cmp eax, 402653184
    jne .Lcpufeat_done
    xor ecx, ecx
    xgetbv
    and eax, 6
    cmp eax, 6
    jne .Lcpufeat_done
    mov eax, 7
    xor ecx, ecx
    cpuid
    and ebx, 32
    je .Lcpufeat_done
    mov r8, 2
.Lcpufeat_done:
    lea rcx, __globals[rip]
    mov qword ptr [rcx+%d], r8
    pop rbx
    ret
`, cpuSlot*wordSize)
	}

	if e.mod.Helpers["must"] || e.mod.Helpers["bounds"] {
		e.b.WriteString(whereRoutine)
	}

	if e.mod.Helpers["must"] {
		// The same descriptor-rather-than-FILE* reasoning as __vy_bounds
		// above, and the same fixed buffer. A reason longer than the
		// buffer is truncated rather than overrunning it.
		//
		// The length that goes to _write is clamped, and has to be.
		// _snprintf returns -1 when the text did not fit, and C99's
		// snprintf returns the length it would have needed - so one
		// gives _write an enormous unsigned count and the other tells it
		// to read past the end of the buffer. The unsigned comparison
		// catches both, since -1 is above 180 read that way.
		e.b.WriteString(`
__vy_must:
    push rbp
    mov rbp, rsp
    sub rsp, 256
    mov qword ptr [rbp-208], rcx
    xor ecx, ecx
    call fflush
    lea rcx, [rbp-200]
    mov rdx, 180
    lea r8, __fmt_must[rip]
    mov r9, qword ptr [rbp-208]
    call _snprintf
    mov r8d, eax
    cmp r8d, 180
    jbe .Lmust_write
    mov r8d, 180
.Lmust_write:
    mov ecx, 2
    lea rdx, [rbp-200]
    call _write
    call __vy_where
    mov ecx, 1
    call exit
`)
	}

	if e.mod.Helpers["inttostr"] {
		// The digits are written backwards into a buffer on the stack,
		// dividing by ten as a multiply (x * ceil(2^67/10) >> 67 is x/10
		// for every 64-bit x), then copied into a new str. This used to
		// be _snprintf with "%lld", which cost more than everything else
		// about building a short string. The signed entry negates and
		// joins the unsigned one, so the most negative int comes out
		// right: its negation is itself, read unsigned.
		e.b.WriteString(`
__vy_inttostr:
    push rbp
    mov rbp, rsp
    sub rsp, 96
    xor r10d, r10d
    mov rax, rcx
    test rax, rax
    jns .Litoa_digits
    neg rax
    mov r10d, 1
    jmp .Litoa_digits

# The same for a u64, read unsigned.
__vy_uinttostr:
    push rbp
    mov rbp, rsp
    sub rsp, 96
    xor r10d, r10d
    mov rax, rcx
.Litoa_digits:
    lea r8, [rbp-8]
    mov r9, -3689348814741910323
.Litoa_loop:
    mov r11, rax
    mul r9
    shr rdx, 3
    lea rax, [rdx+rdx*4]
    add rax, rax
    sub r11, rax
    add r11, 48
    mov rcx, r11
    sub r8, 1
    mov byte ptr [r8], cl
    mov rax, rdx
    test rax, rax
    jne .Litoa_loop
    test r10, r10
    je .Litoa_copy
    sub r8, 1
    mov byte ptr [r8], 45
.Litoa_copy:
    lea rcx, [rbp-8]
    sub rcx, r8
    mov qword ptr [rbp-48], rcx
    mov qword ptr [rbp-56], r8
    add rcx, 1
    mov rdx, rcx
    shl rdx, 8
    call __vy_talloc
    mov qword ptr [rbp-64], rax
    mov rcx, rax
    mov rdx, qword ptr [rbp-56]
    mov r8, qword ptr [rbp-48]
    call memmove
    mov rax, qword ptr [rbp-64]
    add rax, qword ptr [rbp-48]
    mov byte ptr [rax], 0
    mov rax, qword ptr [rbp-64]
    mov rsp, rbp
    pop rbp
    ret
`)
	}

	if e.mod.Helpers["floattostr"] {
		e.b.WriteString(ryuFloatToStr())
		// Go's fmt.Println prints a float64 as the shortest decimal that
		// reads back to the exact same bits - strconv's 'g' format with
		// precision -1. There is no equivalent single libc call, so this
		// finds it by brute force: try "%.Ng" for N = 1, 2, 3, ... and
		// stop at the first one that survives a round trip through
		// strtod. That matches Go closely rather than exactly, because
		// both C's %g and Go's shortest-form switch between decimal and
		// exponential notation on the same rule - exponent < -4 or
		// exponent >= the digit count - so once N is the true number of
		// significant digits, the two agree on which form to use. Every
		// float64 round-trips by N=17, so the loop always terminates.
		//
		// snprintf is variadic, and the Windows x64 ABI requires a
		// float argument passed through the four-register window to be
		// duplicated into the matching integer register, since a
		// variadic callee has no static type to know which file to read.
		// "%.*g" sidesteps needing that here: the precision is an int in
		// position 3 (an ordinary register, no duplication), and the
		// value itself falls at position 4, past the register window
		// entirely, where there is exactly one storage location - the
		// stack slot at [rsp+32] - so there is nothing to duplicate.
		e.b.WriteString(`
# An f32 is the same search, with the round trip judged at single
# precision: the shortest decimal that reads back to the same f32, so
# f32(0.1) prints as 0.1 and not as the double it is held in.
__vy_f32tostr:
    mov r10d, 1
    jmp .Lftoa_entry
__vy_floattostr_slow:
    xor r10d, r10d
.Lftoa_entry:
    push rbp
    mov rbp, rsp
    sub rsp, 384
    movsd qword ptr [rbp-8], xmm0
    mov dword ptr [rbp-32], r10d
    lea rax, [rbp-104]
    mov qword ptr [rbp-24], rax
    mov dword ptr [rbp-16], 1
    mov dword ptr [rbp-40], 0
    # Infinities and NaN are spelled out here. msvcrt writes 1.#INF and
    # 1.#QNAN, and the round trip below cannot settle on either, so Go's
    # +Inf, -Inf and NaN are written straight into the buffer: an
    # all-ones exponent is one of the three, and the fraction bits say
    # which.
    mov rax, qword ptr [rbp-8]
    mov rcx, rax
    shr rcx, 52
    and ecx, 2047
    cmp ecx, 2047
    jne .Lftoa_loop
    mov rcx, rax
    shl rcx, 12
    test rcx, rcx
    jne .Lftoa_nan
    test rax, rax
    js .Lftoa_neginf
    mov dword ptr [rbp-104], 1718503723
    mov byte ptr [rbp-100], 0
    jmp .Lftoa_copy
.Lftoa_neginf:
    mov dword ptr [rbp-104], 1718503725
    mov byte ptr [rbp-100], 0
    jmp .Lftoa_copy
.Lftoa_nan:
    mov dword ptr [rbp-104], 5136718
    jmp .Lftoa_copy
.Lftoa_loop:
    mov r9d, dword ptr [rbp-16]
    mov qword ptr [rbp-200], r9
    movsd xmm0, qword ptr [rbp-8]
    movsd qword ptr [rbp-192], xmm0
    mov ecx, 2
    mov rdx, qword ptr [rbp-24]
    mov r8d, 64
    lea r9, __fmt_g[rip]
    xor eax, eax
    mov qword ptr [rsp+32], rax
    lea rax, [rbp-200]
    mov qword ptr [rsp+40], rax
    call __stdio_common_vsprintf
    # MSVCRT pads the exponent to three digits - "1e+008" where Go and a
    # POSIX libc both write "1e+08" - because this links the legacy
    # msvcrt.dll rather than UCRT. Stripping one redundant leading zero
    # fixes it and does not change the value, so normalizing before the
    # round-trip check below is safe.
    mov rax, qword ptr [rbp-24]
.Lftoa_scan:
    movzx ecx, byte ptr [rax]
    test ecx, ecx
    je .Lftoa_stripped
    cmp ecx, 101
    je .Lftoa_strip
    add rax, 1
    jmp .Lftoa_scan
.Lftoa_strip:
    movzx ecx, byte ptr [rax+4]
    cmp ecx, 48
    jl .Lftoa_stripped
    cmp ecx, 57
    jg .Lftoa_stripped
    movzx ecx, byte ptr [rax+2]
    cmp ecx, 48
    jne .Lftoa_stripped
    movzx ecx, byte ptr [rax+3]
    cmp ecx, 48
    jl .Lftoa_stripped
    cmp ecx, 57
    jg .Lftoa_stripped
    lea rdx, [rax+2]
    lea r8, [rax+3]
.Lftoa_shift:
    movzx ecx, byte ptr [r8]
    mov byte ptr [rdx], cl
    add rdx, 1
    add r8, 1
    test ecx, ecx
    jne .Lftoa_shift
.Lftoa_stripped:
    mov rcx, qword ptr [rbp-24]
    xor edx, edx
    call strtod
    movsd xmm1, qword ptr [rbp-8]
    cmp dword ptr [rbp-32], 0
    je .Lftoa_compare
    cvtsd2ss xmm0, xmm0
    cvtss2sd xmm0, xmm0
.Lftoa_compare:
    ucomisd xmm0, xmm1
    je .Lftoa_found
    mov eax, dword ptr [rbp-16]
    add eax, 1
    mov dword ptr [rbp-16], eax
    cmp eax, 18
    jl .Lftoa_loop
.Lftoa_found:
    # A tie: the exact value is the [rbp-16]-digit candidate plus exactly
    # half a unit in its last place, so two candidates are equally near
    # and both read back. Go and glibc take the even one; UCRT rounds
    # up. So the exact digits are written out (thirty past the kept
    # ones, into [rbp-360]) and when they are a 5 then zeros after an
    # even last digit, [rbp-40] is set. UCRT's answer then ends in that
    # digit plus one - never a carry, since it was even - so lowering
    # the last digit of the finished text gives the even candidate.
    mov eax, dword ptr [rbp-16]
    add eax, 29
    mov qword ptr [rbp-200], rax
    movsd xmm0, qword ptr [rbp-8]
    movsd qword ptr [rbp-192], xmm0
    mov ecx, 2
    lea rdx, [rbp-360]
    mov r8d, 96
    lea r9, __fmt_e[rip]
    xor eax, eax
    mov qword ptr [rsp+32], rax
    lea rax, [rbp-200]
    mov qword ptr [rsp+40], rax
    call __stdio_common_vsprintf
    lea rdx, [rbp-360]
    movzx ecx, byte ptr [rdx]
    cmp ecx, 45
    jne .Ltie_start
    add rdx, 1
.Ltie_start:
    xor r8d, r8d
    mov r9d, dword ptr [rbp-16]
    xor r10d, r10d
.Ltie_next:
    movzx ecx, byte ptr [rdx]
    add rdx, 1
    cmp ecx, 46
    je .Ltie_next
    cmp ecx, 48
    jl .Ltie_end
    cmp ecx, 57
    jg .Ltie_end
    sub ecx, 48
    cmp r8d, r9d
    jl .Ltie_kept
    je .Ltie_half
    test ecx, ecx
    jne .Lftoa_notie
    jmp .Ltie_count
.Ltie_kept:
    mov r10d, ecx
    jmp .Ltie_count
.Ltie_half:
    cmp ecx, 5
    jne .Lftoa_notie
.Ltie_count:
    add r8d, 1
    jmp .Ltie_next
.Ltie_end:
    cmp r8d, r9d
    jle .Lftoa_notie
    and r10d, 1
    jne .Lftoa_notie
    mov dword ptr [rbp-40], 1
.Lftoa_notie:
    # [rbp-16] now holds the fewest significant digits that read back as
    # the same double, which is what Go means by the shortest form. It is
    # not what decides fixed against exponential, though.
    #
    # C's %g goes exponential when the decimal exponent is below -4 or at
    # least the precision, so the threshold moves with the digit count.
    # Go's rule fixes that threshold at 6 whatever the digit count, which
    # is why 490 prints as 490 there and came out as 4.9e+02 here: two
    # digits, exponent two, and C called that exponential.
    #
    # So the exponent is read off an %e rendering and the choice is made
    # here rather than left to the C library.
    mov r9d, dword ptr [rbp-16]
    sub r9d, 1
    mov qword ptr [rbp-200], r9
    movsd xmm0, qword ptr [rbp-8]
    movsd qword ptr [rbp-192], xmm0
    mov ecx, 2
    mov rdx, qword ptr [rbp-24]
    mov r8d, 64
    lea r9, __fmt_e[rip]
    xor eax, eax
    mov qword ptr [rsp+32], rax
    lea rax, [rbp-200]
    mov qword ptr [rsp+40], rax
    call __stdio_common_vsprintf
    xor r9d, r9d
    mov rdx, qword ptr [rbp-24]
.Lftoa_seek:
    movzx ecx, byte ptr [rdx]
    test ecx, ecx
    je .Lftoa_fixed
    cmp ecx, 101
    je .Lftoa_at_e
    add rdx, 1
    jmp .Lftoa_seek
.Lftoa_at_e:
    mov r8d, 1
    movzx ecx, byte ptr [rdx+1]
    cmp ecx, 45
    jne .Lftoa_expdigits
    mov r8d, -1
.Lftoa_expdigits:
    add rdx, 2
    mov r10d, 10
.Lftoa_expdigit:
    movzx ecx, byte ptr [rdx]
    cmp ecx, 48
    jl .Lftoa_expdone
    cmp ecx, 57
    jg .Lftoa_expdone
    imul r9d, r10d
    sub ecx, 48
    add r9d, ecx
    add rdx, 1
    jmp .Lftoa_expdigit
.Lftoa_expdone:
    imul r9d, r8d
    cmp r9d, -4
    jl .Lftoa_exponential
    cmp r9d, 6
    jge .Lftoa_exponential
.Lftoa_fixed:
    # Asking for max(digits, exponent+1) digits of precision is what
    # forces C to choose fixed notation, and %g drops the trailing zeros
    # the wider precision would otherwise add - so the digits printed are
    # still the shortest ones.
    mov eax, r9d
    add eax, 1
    cmp eax, dword ptr [rbp-16]
    jge .Lftoa_haveprec
    mov eax, dword ptr [rbp-16]
.Lftoa_haveprec:
    mov r9d, eax
    mov qword ptr [rbp-200], r9
    movsd xmm0, qword ptr [rbp-8]
    movsd qword ptr [rbp-192], xmm0
    mov ecx, 2
    mov rdx, qword ptr [rbp-24]
    mov r8d, 64
    lea r9, __fmt_g[rip]
    xor eax, eax
    mov qword ptr [rsp+32], rax
    lea rax, [rbp-200]
    mov qword ptr [rsp+40], rax
    call __stdio_common_vsprintf
    jmp .Lftoa_copy
.Lftoa_exponential:
    # The buffer already holds the %e rendering; only msvcrt's three-digit
    # exponent needs the same normalising as above.
    mov rax, qword ptr [rbp-24]
.Lftoa_escan:
    movzx ecx, byte ptr [rax]
    test ecx, ecx
    je .Lftoa_copy
    cmp ecx, 101
    je .Lftoa_estrip
    add rax, 1
    jmp .Lftoa_escan
.Lftoa_estrip:
    movzx ecx, byte ptr [rax+4]
    cmp ecx, 48
    jl .Lftoa_copy
    cmp ecx, 57
    jg .Lftoa_copy
    movzx ecx, byte ptr [rax+2]
    cmp ecx, 48
    jne .Lftoa_copy
    movzx ecx, byte ptr [rax+3]
    cmp ecx, 48
    jl .Lftoa_copy
    cmp ecx, 57
    jg .Lftoa_copy
    lea rdx, [rax+2]
    lea r8, [rax+3]
.Lftoa_eshift:
    movzx ecx, byte ptr [r8]
    mov byte ptr [rdx], cl
    add rdx, 1
    add r8, 1
    test ecx, ecx
    jne .Lftoa_eshift
.Lftoa_copy:
    cmp dword ptr [rbp-40], 0
    je .Lftoa_copied
    # The tie: lower the last digit of the mantissa, the one before the
    # e or the end.
    mov rax, qword ptr [rbp-24]
.Lftoa_tend:
    movzx ecx, byte ptr [rax]
    test ecx, ecx
    je .Lftoa_tback
    cmp ecx, 101
    je .Lftoa_tback
    add rax, 1
    jmp .Lftoa_tend
.Lftoa_tback:
    # Only when the C library rounded up, which leaves it odd: glibc
    # already rounds a tie to even, and its answer stands.
    sub rax, 1
    movzx ecx, byte ptr [rax]
    mov edx, ecx
    and edx, 1
    je .Lftoa_copied
    sub ecx, 1
    mov byte ptr [rax], cl
.Lftoa_copied:
    mov rcx, qword ptr [rbp-24]
    call strlen
    mov rcx, rax
    add rcx, 1
    mov rdx, rcx
    shl rdx, 8
    call __vy_talloc
    mov qword ptr [rbp-32], rax
    mov rcx, rax
    mov rdx, qword ptr [rbp-24]
    call strcpy
    mov rax, qword ptr [rbp-32]
    mov rsp, rbp
    pop rbp
    ret
`)
	}
}

// labelBase is the function's name as it goes into its local labels,
// which is the symbol less its prefix: plain for a plain name, spelled
// out for a generic instance's.
func (e *Emitter) labelBase() string {
	if e.f.Name == "main" {
		return "main"
	}
	return strings.TrimPrefix(fnSym(e.f.Name), "__vy_")
}
