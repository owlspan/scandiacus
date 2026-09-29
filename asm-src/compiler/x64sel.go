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
	for _, in := range f.Code {
		if in.Op == OpConst && in.Dst != NoReg {
			e.consts[in.Dst] = in.Imm
		}
	}
	needed := map[Reg]bool{}
	for _, in := range f.Code {
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
}

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
	case OpAdd, OpSub, OpBAnd, OpBOr, OpBXor,
		OpEq, OpNe, OpLt, OpLe, OpGt, OpGe, OpStoreMem:
		return v, true
	case OpShl, OpShr:
		if v >= 0 && v < 64 {
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
		if isRegLoc(d) || isRegLoc(src) {
			if d != src {
				e.line("mov %s, %s", d, src)
			}
			return true
		}
		return false

	case OpAdd, OpSub, OpMul, OpBAnd, OpBOr, OpBXor:
		e.selArith(in)
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

	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
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

var condCodes = map[Op]string{OpEq: "e", OpNe: "ne", OpLt: "l", OpLe: "le", OpGt: "g", OpGe: "ge"}

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

// selArith is add, sub, imul, and, or and xor in two-operand form. The
// result's own register is the accumulator when it has one, so
// `total += x` with total in rbx is one add.
func (e *Emitter) selArith(in Instr) {
	m := map[Op]string{
		OpAdd: "add", OpSub: "sub", OpMul: "imul",
		OpBAnd: "and", OpBOr: "or", OpBXor: "xor",
	}[in.Op]
	d, a, b := e.loc(in.Dst), e.loc(in.A), e.srcB(in)
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
