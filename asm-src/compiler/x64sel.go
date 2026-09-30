package main

// Instruction selection for the common integer operations.
//
// The emitter started as a translator that put every operand through
// rax and rcx: load A into rax, load B into rcx, operate, store rax to
// wherever the result lives. That is correct whatever the operands are,
// which is why it came first, and it is also three or four instructions
// where one would do once values started living in registers:
//
//	mov rax, r8          add r8, 1
//	mov rcx, qword ...   (the constant 1 stored to a slot beforehand)
//	add rax, rcx
//	mov r8, rax
//
// This file picks the shorter form when the operands allow it: an
// operation done in the result's own register, a constant as an
// immediate instead of a register loaded from a slot, a memory operand
// used where x86 accepts one. A constant whose every use became an
// immediate is not materialised at all.
//
// Only with the optimisations on. VEYL_NOOPT keeps the plain form, so
// the two can be compared when something goes wrong.

import (
	"fmt"
	"math"
	"math/bits"
	"strings"
)

// isRegLoc reports whether a location is a machine register rather than
// a memory operand.
func isRegLoc(s string) bool { return !strings.Contains(s, "[") }

func fits32(v int64) bool { return v >= math.MinInt32 && v <= math.MaxInt32 }

// planConsts records every virtual register that holds a constant, and
// which of those are only ever read as an immediate. The IR writes each
// register exactly once, so a register an OpConst defines holds that
// value at every use.
func (e *Emitter) planConsts(f *Func) {
	e.consts = map[Reg]int64{}
	e.dropConst = map[Reg]bool{}
	e.fconsts = map[Reg]int64{}
	e.dropFC = map[Reg]bool{}
	for _, in := range f.Code {
		if in.Op == OpConst && in.Dst != NoReg {
			e.consts[in.Dst] = in.Imm
		}
		if in.Op == OpFConst && in.Dst != NoReg {
			e.fconsts[in.Dst] = in.Imm
		}
	}
	needed := map[Reg]bool{}
	for _, in := range f.Code {
		for _, r := range []Reg{in.A, in.B} {
			if _, ok := e.fconsts[r]; ok && r != NoReg && !(r == in.B && isFArith(in.Op)) {
				needed[r] = true
			}
		}
		if _, ok := e.consts[in.A]; ok && in.A != NoReg {
			if _, imm := e.immA(in); !imm {
				needed[in.A] = true
			}
		}
		if _, ok := e.consts[in.B]; ok && in.B != NoReg {
			if _, imm := e.immB(in); !imm {
				needed[in.B] = true
			}
		}
		for _, a := range in.Args {
			needed[a] = true
		}
	}
	for r := range e.consts {
		if !needed[r] {
			e.dropConst[r] = true
		}
	}
	for r := range e.fconsts {
		if !needed[r] {
			e.dropFC[r] = true
		}
	}
}

func isFArith(op Op) bool { return op == OpFAdd || op == OpFSub || op == OpFMul || op == OpFDiv }

// immA is the constant an instruction's A operand can be written as, if
// the instruction takes one there.
func (e *Emitter) immA(in Instr) (int64, bool) {
	if e.consts == nil || in.A == NoReg {
		return 0, false
	}
	v, ok := e.consts[in.A]
	if !ok || !fits32(v) {
		return 0, false
	}
	switch in.Op {
	case OpStore:
		return v, true
	}
	return 0, false
}

// immB is immA for the B operand.
func (e *Emitter) immB(in Instr) (int64, bool) {
	if e.consts == nil || in.B == NoReg {
		return 0, false
	}
	v, ok := e.consts[in.B]
	if !ok || !fits32(v) {
		return 0, false
	}
	switch in.Op {
	case OpAdd, OpSub, OpBAnd, OpBOr, OpBXor, OpMul,
		OpEq, OpNe, OpLt, OpLe, OpGt, OpGe, OpStoreMem,
		OpLtU, OpLeU, OpGtU, OpGeU:
		return v, true
	case OpShl, OpShr:
		if v >= 0 && v < 64 {
			return v, true
		}
	case OpDiv, OpMod:
		if constDivisor(v) && v <= math.MaxInt32 {
			return v, true
		}
	}
	return 0, false
}

// srcB is the B operand as it should be written: an immediate, or where
// the value lives.
func (e *Emitter) srcB(in Instr) string {
	if v, ok := e.immB(in); ok {
		return fmt.Sprint(v)
	}
	return e.loc(in.B)
}

// selInstr emits the instructions it has a better form for, and reports
// false for the rest, which go to the plain translator.
func (e *Emitter) selInstr(in Instr) bool {
	if e.consts == nil {
		return false
	}
	switch in.Op {
	case OpConst:
		if e.dropConst[in.Dst] {
			return true
		}
		d := e.loc(in.Dst)
		if isRegLoc(d) || fits32(in.Imm) {
			e.line("mov %s, %d", d, in.Imm)
			return true
		}
		return false

	case OpStore:
		dst := e.slotAddr(in.Imm)
		if a := e.loc(in.A); isXmm(dst) || isXmm(a) {
			// A float slot promoted into an xmm register.
			if a != dst {
				e.line("movsd %s, %s", dst, a)
			}
			return true
		}
		if v, ok := e.immA(in); ok {
			e.line("mov %s, %d", dst, v)
			return true
		}
		a := e.loc(in.A)
		if isRegLoc(a) || isRegLoc(dst) {
			if a != dst {
				e.line("mov %s, %s", dst, a)
			}
			return true
		}
		return false

	case OpLoad:
		d, src := e.loc(in.Dst), e.slotAddr(in.Imm)
		if isXmm(d) || isXmm(src) {
			if d != src {
				e.line("movsd %s, %s", d, src)
			}
			return true
		}
		if isRegLoc(d) || isRegLoc(src) {
			if d != src {
				e.line("mov %s, %s", d, src)
			}
			return true
		}
		return false

	case OpMul:
		if v, ok := e.immB(in); ok {
			e.selMulConst(in, v)
			return true
		}
		e.selArith(in)
		return true

	case OpAdd, OpSub, OpBAnd, OpBOr, OpBXor:
		e.selArith(in)
		return true

	case OpPeek:
		base := e.loc(in.A)
		if !isRegLoc(base) {
			e.line("mov rax, %s", base)
			base = "rax"
		}
		e.emitPeek(in, "["+base+"]")
		return true

	case OpPoke:
		base := e.loc(in.A)
		if !isRegLoc(base) {
			e.line("mov rax, %s", base)
			base = "rax"
		}
		e.emitPoke(in, "["+base+"]", "rcx")
		return true

	case OpFConst:
		if e.dropFC[in.Dst] {
			return true
		}
		if d := e.loc(in.Dst); isXmm(d) {
			e.line("movsd %s, __flt%d[rip]", d, in.Imm)
			return true
		}
		return false

	case OpFAdd, OpFSub, OpFMul, OpFDiv:
		e.selFArith(in)
		return true

	case OpShl, OpShr:
		v, ok := e.immB(in)
		if !ok {
			return false
		}
		m := "shl"
		if in.Op == OpShr {
			m = "sar"
		}
		e.inPlace(in, func(r string) { e.line("%s %s, %d", m, r, v) })
		return true

	case OpNeg, OpBNot:
		m := "neg"
		if in.Op == OpBNot {
			m = "not"
		}
		e.inPlace(in, func(r string) { e.line("%s %s", m, r) })
		return true

	case OpDiv, OpMod:
		if d, ok := e.immB(in); ok {
			e.selDivConst(in, d)
			e.put(in.Dst, "rdx")
			return true
		}
		e.line("mov rax, %s", e.loc(in.A))
		b := e.loc(in.B)
		if !isRegLoc(b) {
			e.line("mov rcx, %s", b)
			b = "rcx"
		}
		e.line("cqo")
		e.line("idiv %s", b)
		if in.Op == OpDiv {
			e.put(in.Dst, "rax")
		} else {
			e.put(in.Dst, "rdx")
		}
		return true

	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe, OpLtU, OpLeU, OpGtU, OpGeU:
		cc := condCodes[in.Op]
		e.line("xor edx, edx")
		e.cmp(in)
		e.line("set%s dl", cc)
		e.put(in.Dst, "rdx")
		return true

	case OpJumpIf, OpJumpNot:
		j := "jne"
		if in.Op == OpJumpNot {
			j = "je"
		}
		e.testZero(e.loc(in.A))
		e.line("%s .L%s_%d", j, e.labelBase(), in.Imm)
		return true

	case OpLoadMem:
		base := e.loc(in.A)
		if !isRegLoc(base) {
			e.line("mov rax, %s", base)
			base = "rax"
		}
		src := fmt.Sprintf("qword ptr [%s+%d]", base, in.Imm)
		if d := e.loc(in.Dst); isRegLoc(d) {
			e.line("mov %s, %s", d, src)
		} else {
			e.line("mov rax, %s", src)
			e.line("mov %s, rax", d)
		}
		return true

	case OpStoreMem:
		base := e.loc(in.A)
		if !isRegLoc(base) {
			e.line("mov rax, %s", base)
			base = "rax"
		}
		dst := fmt.Sprintf("qword ptr [%s+%d]", base, in.Imm)
		if v, ok := e.immB(in); ok {
			e.line("mov %s, %d", dst, v)
			return true
		}
		b := e.loc(in.B)
		if !isRegLoc(b) {
			e.line("mov rcx, %s", b)
			b = "rcx"
		}
		e.line("mov %s, %s", dst, b)
		return true
	}
	return false
}

var condCodes = map[Op]string{OpEq: "e", OpNe: "ne", OpLt: "l", OpLe: "le", OpGt: "g", OpGe: "ge",
	OpLtU: "b", OpLeU: "be", OpGtU: "a", OpGeU: "ae"}

// cmp compares A with B, using each where it lives. x86 has no compare
// of two memory operands, so one of those goes through rax.
func (e *Emitter) cmp(in Instr) {
	a, b := e.loc(in.A), e.srcB(in)
	if !isRegLoc(a) && !isRegLoc(b) && !isImm(b) {
		e.line("mov rax, %s", a)
		a = "rax"
	}
	e.line("cmp %s, %s", a, b)
}

func isImm(s string) bool { return s != "" && (s[0] == '-' || s[0] >= '0' && s[0] <= '9') }

// testZero sets the flags for a value compared with zero.
func (e *Emitter) testZero(a string) {
	if isRegLoc(a) {
		e.line("test %s, %s", a, a)
		return
	}
	e.line("cmp %s, 0", a)
}

// inPlace applies a one-register operation to A, leaving the result in
// Dst: in Dst's own register when it has one, through rax when not.
func (e *Emitter) inPlace(in Instr, op func(r string)) {
	d, a := e.loc(in.Dst), e.loc(in.A)
	if isRegLoc(d) {
		if d != a {
			e.line("mov %s, %s", d, a)
		}
		op(d)
		return
	}
	e.line("mov rax, %s", a)
	op("rax")
	e.put(in.Dst, "rax")
}

// selFArith is the scalar double arithmetic. The second operand can be
// memory or a pooled register, and a constant is read straight out of
// the pool, so none of the three needs loading into xmm1 first.
func (e *Emitter) selFArith(in Instr) {
	m := map[Op]string{OpFAdd: "addsd", OpFSub: "subsd", OpFMul: "mulsd", OpFDiv: "divsd"}[in.Op]
	d, a := e.loc(in.Dst), e.loc(in.A)
	b := e.loc(in.B)
	if idx, ok := e.fconsts[in.B]; ok {
		b = fmt.Sprintf("__flt%d[rip]", idx)
	}
	if isXmm(d) && (d != b || d == a) {
		if d != a {
			e.line("movsd %s, %s", d, a)
		}
		e.line("%s %s, %s", m, d, b)
		return
	}
	e.line("movsd xmm0, %s", a)
	e.line("%s xmm0, %s", m, b)
	e.putf(in.Dst, "xmm0")
}

// selArith is add, sub, imul, and, or and xor in two-operand form. The
// result's own register is the accumulator when it has one, so
// `total += x` with total in rbx is one add.
func (e *Emitter) selArith(in Instr) {
	m := map[Op]string{
		OpAdd: "add", OpSub: "sub", OpMul: "imul",
		OpBAnd: "and", OpBOr: "or", OpBXor: "xor",
	}[in.Op]
	d, a, b := e.loc(in.Dst), e.loc(in.A), e.srcB(in)
	if b == "0" && in.Op != OpMul && in.Op != OpBAnd {
		// x + 0, which is how the lowerer gives a value another type
		// (retype): only the move is left.
		if d != a {
			if !isRegLoc(d) && !isRegLoc(a) {
				e.line("mov rax, %s", a)
				a = "rax"
			}
			e.line("mov %s, %s", d, a)
		}
		return
	}
	if isRegLoc(d) {
		if d == b && d != a {
			// The result's register holds B, so loading A into it first
			// would lose B. Either order works for the commutative ones,
			// and a - b is -(b) + a.
			switch in.Op {
			case OpSub:
				e.line("neg %s", d)
				e.line("add %s, %s", d, a)
			default:
				e.line("%s %s, %s", m, d, a)
			}
			return
		}
		if d != a {
			e.line("mov %s, %s", d, a)
		}
		e.line("%s %s, %s", m, d, b)
		return
	}
	e.line("mov rax, %s", a)
	e.line("%s rax, %s", m, b)
	e.put(in.Dst, "rax")
}

// fuseIndex folds an element address into the load or store that is its
// only reader, so xs[i] is one mov with a [base+index*8] operand rather
// than an lea into a register and a move through it. A pointer's p[i]
// is the same with its element's size as the scale.
func (e *Emitter) fuseIndex(ix, next Instr, uses map[Reg]int) bool {
	if e.consts == nil || ix.Op != OpIndexAddr || uses[ix.Dst] != 1 || next.A != ix.Dst {
		return false
	}
	switch next.Op {
	case OpLoadMem, OpPeek:
	case OpStoreMem, OpPoke:
		if next.B == ix.Dst {
			return false
		}
	default:
		return false
	}
	if (next.Op == OpLoadMem || next.Op == OpStoreMem) && next.Imm != 0 {
		return false // an offset, which indexed addressing here has no room for
	}
	base := e.loc(ix.A)
	if !isRegLoc(base) {
		e.line("mov rax, %s", base)
		base = "rax"
	}
	idx := e.loc(ix.B)
	if !isRegLoc(idx) {
		e.line("mov rcx, %s", idx)
		idx = "rcx"
	}
	if next.Op == OpPeek {
		e.emitPeek(next, fmt.Sprintf("[%s+%s*%d]", base, idx, indexScale(ix)))
		return true
	}
	if next.Op == OpPoke {
		e.emitPoke(next, fmt.Sprintf("[%s+%s*%d]", base, idx, indexScale(ix)), "rdx")
		return true
	}
	addr := fmt.Sprintf("qword ptr [%s+%s*%d]", base, idx, indexScale(ix))
	if next.Op == OpLoadMem {
		if d := e.loc(next.Dst); isRegLoc(d) {
			e.line("mov %s, %s", d, addr)
		} else {
			e.line("mov rax, %s", addr)
			e.line("mov %s, rax", d)
		}
		return true
	}
	if v, ok := e.immB(next); ok {
		e.line("mov %s, %d", addr, v)
		return true
	}
	b := e.loc(next.B)
	if !isRegLoc(b) {
		e.line("mov rdx, %s", b)
		b = "rdx"
	}
	e.line("mov %s, %s", addr, b)
	return true
}

// indexScale is an element address's scale; see OpIndexAddr.
func indexScale(in Instr) int64 {
	if in.Imm == 0 {
		return 8
	}
	return in.Imm
}

// low32 names the low half of a general register, which a 32-bit write
// zero-extends into the whole of it.
func low32(r string) string {
	switch r {
	case "rax", "rbx", "rcx", "rdx", "rsi", "rdi":
		return "e" + r[1:]
	}
	return r + "d" // r8 to r15
}

// emitPeek reads memory at a C width into a Veyl int or float, straight
// into the result's register when it has one.
func (e *Emitter) emitPeek(in Instr, addr string) {
	d := e.loc(in.Dst)
	switch in.Imm {
	case memF32, memF64:
		t := "xmm0"
		if isXmm(d) {
			t = d
		}
		if in.Imm == memF32 {
			e.line("cvtss2sd %s, dword ptr %s", t, addr)
		} else {
			e.line("movsd %s, qword ptr %s", t, addr)
		}
		e.putf(in.Dst, t)
		return
	}
	t := "rax"
	if isRegLoc(d) && !isXmm(d) {
		t = d
	}
	switch in.Imm {
	case memU8:
		e.line("movzx %s, byte ptr %s", low32(t), addr)
	case memI8:
		e.line("movsx %s, byte ptr %s", t, addr)
	case memU16:
		e.line("movzx %s, word ptr %s", low32(t), addr)
	case memI16:
		e.line("movsx %s, word ptr %s", t, addr)
	case memU32:
		e.line("mov %s, dword ptr %s", low32(t), addr)
	case memI32:
		e.line("movsxd %s, dword ptr %s", t, addr)
	default:
		e.line("mov %s, qword ptr %s", t, addr)
	}
	e.put(in.Dst, t)
}

// emitPoke writes a value to memory at a C width. scratch is rcx or
// rdx, whichever the address does not already use.
func (e *Emitter) emitPoke(in Instr, addr, scratch string) {
	b := e.loc(in.B)
	switch in.Imm {
	case memF32:
		e.line("movsd xmm0, %s", b)
		e.line("cvtsd2ss xmm0, xmm0")
		e.line("movss dword ptr %s, xmm0", addr)
		return
	case memF64:
		if !isXmm(b) {
			e.line("movsd xmm0, %s", b)
			b = "xmm0"
		}
		e.line("movsd qword ptr %s, %s", addr, b)
		return
	case memI64:
		if !isRegLoc(b) {
			e.line("mov %s, %s", scratch, b)
			b = scratch
		}
		e.line("mov qword ptr %s, %s", addr, b)
		return
	}
	if b != scratch {
		e.line("mov %s, %s", scratch, b)
	}
	low := map[string][3]string{"rcx": {"cl", "cx", "ecx"}, "rdx": {"dl", "dx", "edx"}}[scratch]
	switch in.Imm {
	case memU8, memI8:
		e.line("mov byte ptr %s, %s", addr, low[0])
	case memU16, memI16:
		e.line("mov word ptr %s, %s", addr, low[1])
	default:
		e.line("mov dword ptr %s, %s", addr, low[2])
	}
}

// selMulConst multiplies by a constant: a shift for a power of two, the
// three-operand imul otherwise.
func (e *Emitter) selMulConst(in Instr, v int64) {
	d, a := e.loc(in.Dst), e.loc(in.A)
	t := d
	if !isRegLoc(d) {
		t = "rax"
	}
	if isPow2(v) {
		if t != a {
			e.line("mov %s, %s", t, a)
		}
		if k := bits.TrailingZeros64(uint64(v)); k > 0 {
			e.line("shl %s, %d", t, k)
		}
	} else {
		e.line("imul %s, %s, %d", t, a, v)
	}
	if t == "rax" {
		e.put(in.Dst, "rax")
	}
}

// fuseRMW turns `load slot; op; store slot` - what x += y lowers to -
// into one instruction on the slot's own home when neither value in
// between is read anywhere else: add rbx, r9 rather than three moves.
func (e *Emitter) fuseRMW(ld, op, st Instr, uses map[Reg]int) bool {
	if e.consts == nil || ld.Op != OpLoad || st.Op != OpStore || st.Imm != ld.Imm {
		return false
	}
	m, ok := map[Op]string{OpAdd: "add", OpSub: "sub", OpBAnd: "and", OpBOr: "or", OpBXor: "xor"}[op.Op]
	if !ok || op.A != ld.Dst || op.B == ld.Dst || st.A != op.Dst ||
		uses[ld.Dst] != 1 || uses[op.Dst] != 1 {
		return false
	}
	slot := e.slotAddr(ld.Imm)
	if isXmm(slot) {
		return false
	}
	b := e.srcB(op)
	if !isRegLoc(slot) && !isRegLoc(b) && !isImm(b) {
		e.line("mov rax, %s", b)
		b = "rax"
	}
	e.line("%s %s, %s", m, slot, b)
	return true
}

// nextReal is the index of the next instruction after i that emits
// anything, or -1. Constants folded away emit nothing.
func (e *Emitter) nextReal(f *Func, i int) int {
	for j := i + 1; j < len(f.Code); j++ {
		in := f.Code[j]
		if in.Op == OpConst && e.dropConst[in.Dst] || in.Op == OpFConst && e.dropFC[in.Dst] {
			continue
		}
		return j
	}
	return -1
}

// aliasLoad skips a load whose one reader is the very next instruction,
// and lets that reader take the value straight from the slot's home:
// cmp rdi, r15 rather than copying both into scratch registers first.
// Nothing can write the slot in between, since nothing is in between.
func (e *Emitter) aliasLoad(ld, next Instr, uses map[Reg]int) bool {
	if e.consts == nil || ld.Op != OpLoad || uses[ld.Dst] != 1 {
		return false
	}
	reads := next.A == ld.Dst || next.B == ld.Dst
	for _, a := range next.Args {
		reads = reads || a == ld.Dst
	}
	if !reads || next.Op == OpLabel {
		return false
	}
	e.alias[ld.Dst] = e.slotAddr(ld.Imm)
	return true
}
