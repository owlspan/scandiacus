package main

import "fmt"

// Fused multiply-add, off unless SCANDIACUS_FMA=1.
//
// a*b + c is two roundings: the product is rounded to a double, then the
// sum. vfmadd rounds once, which is faster and usually more accurate but
// not the same to the bit, so a program can print a different last
// digit. That is why it is opt-in: by default every float result here
// matches the Go backend exactly. A program built with it also needs a
// CPU with FMA (Intel since 2013, AMD since 2012); one without stops
// with an illegal instruction.
//
// sinkFMul moves a multiply whose only reader is the next add or
// subtract down to sit just before it, past the loads that the lowerer
// put in between; the emitter then writes the two as one instruction.
// Registers are single assignment, so moving a pure instruction later
// within a block cannot change what it reads.

func fmaOn() bool { return envOff("SCANDIACUS_FMA") }

func sinkFMul(fn *Func) {
	uses := regUses(fn)
	for j := range fn.Code {
		add := fn.Code[j]
		if add.Op != OpFAdd && add.Op != OpFSub {
			continue
		}
		for i := j - 1; i >= 0 && j-i <= 8; i-- {
			in := fn.Code[i]
			if in.Op == OpFMul && uses[in.Dst] == 1 && fmaUser(add, in.Dst) {
				copy(fn.Code[i:j-1], fn.Code[i+1:j])
				fn.Code[j-1] = in
				break
			}
			if !(csePure(in.Op) || in.Op == OpLoad || in.Op == OpLoadMem ||
				in.Op == OpConst || in.Op == OpFConst) {
				break
			}
		}
	}
}

// fmaUser reports whether add can take the product p into one fused
// instruction: either side of an add, the right side of a subtract.
func fmaUser(add Instr, p Reg) bool {
	if add.Op == OpFAdd {
		return add.A == p || add.B == p
	}
	return add.Op == OpFSub && add.B == p && add.A != p
}

// emitFMA writes mul and the add or subtract right after it as one
// instruction: the 231 form adds the product of its
// second and third operands into its first, and only the third may be
// memory.
func (e *Emitter) emitFMA(mul, add Instr) {
	other := add.A
	if other == mul.Dst {
		other = add.B
	}
	src := func(r Reg) string {
		if idx, ok := e.fconsts[r]; ok {
			return fmt.Sprintf("qword ptr __flt%d[rip]", idx)
		}
		return e.loc(r)
	}
	a, b := src(mul.A), src(mul.B)
	if !isXmm(a) {
		e.line("movsd xmm1, %s", a)
		a = "xmm1"
	}
	m := "vfmadd231sd"
	if add.Op == OpFSub {
		m = "vfnmadd231sd" // c - a*b
	}
	// Straight into the result's register when writing the addend
	// there first cannot overwrite a factor.
	d := e.loc(add.Dst)
	if !isXmm(d) || d == a || d == b {
		d = "xmm0"
	}
	if c := src(other); c != d {
		e.line("movsd %s, %s", d, c)
	}
	e.line("%s %s, %s, %s", m, d, a, b)
	e.putf(add.Dst, d)
}
