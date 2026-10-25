package main

// Value ranges: the least and greatest value an integer register can
// hold, where that is known.
//
// The one place this pays so far is division by a constant. A dividend
// that cannot be negative divides as an unsigned number - a multiply
// and a shift, with none of the rounding fix a signed one needs - and
// a remainder by a power of two is a single and. `(i * i) % 7` in a
// loop to 400000000 is the case that asked for it: i is the counter of
// a loop with constant bounds (Func.Ranges, set by the lowerer), so
// i * i is at most 1.6e17 and certainly not negative.
//
// Registers are written once and every use comes after the definition
// in the code, so one pass in order sees each definition before its
// uses. Anything not worked out here is simply unknown.

import (
	"math"
	"math/bits"
)

type span struct{ lo, hi int64 }

func valueRanges(f *Func) map[Reg]span {
	out := map[Reg]span{}
	for _, in := range f.Code {
		if in.Dst == NoReg {
			continue
		}
		a, okA := out[in.A]
		b, okB := out[in.B]
		var r span
		ok := false
		switch in.Op {
		case OpConst:
			r, ok = span{in.Imm, in.Imm}, true
		case OpLoad:
			r, ok = f.Ranges[in.Imm]
		case OpAdd:
			if okA && okB {
				r, ok = addSpan(a, b)
			}
		case OpSub:
			if okA && okB {
				r, ok = addSpan(a, span{-b.hi, -b.lo})
				ok = ok && b.lo != math.MinInt64
			}
		case OpMul:
			if okA && okB {
				r, ok = mulSpan(a, b)
			}
		case OpShr:
			if okA && okB && b.lo == b.hi && b.lo >= 0 && b.lo < 64 {
				r, ok = span{a.lo >> b.lo, a.hi >> b.lo}, true
			}
		case OpBAnd:
			// Anything and a number that is not negative is between
			// zero and that number.
			switch {
			case okB && b.lo >= 0:
				r, ok = span{0, b.hi}, true
			case okA && a.lo >= 0:
				r, ok = span{0, a.hi}, true
			}
		case OpMod:
			if okA && okB && a.lo >= 0 && b.lo == b.hi && b.lo > 0 {
				r, ok = span{0, min(a.hi, b.lo-1)}, true
			}
		case OpDiv:
			if okA && okB && a.lo >= 0 && b.lo == b.hi && b.lo > 0 {
				r, ok = span{a.lo / b.lo, a.hi / b.lo}, true
			}
		}
		if ok {
			out[in.Dst] = r
		}
	}
	return out
}

func addSpan(a, b span) (span, bool) {
	lo, ok1 := addInt(a.lo, b.lo)
	hi, ok2 := addInt(a.hi, b.hi)
	return span{lo, hi}, ok1 && ok2
}

func addInt(x, y int64) (int64, bool) {
	s := x + y
	overflow := (x >= 0) == (y >= 0) && (s >= 0) != (x >= 0)
	return s, !overflow
}

// mulSpan is the product's range from the four corner products, when
// none of them overflows.
func mulSpan(a, b span) (span, bool) {
	r := span{math.MaxInt64, math.MinInt64}
	for _, x := range []int64{a.lo, a.hi} {
		for _, y := range []int64{b.lo, b.hi} {
			p, ok := mulInt(x, y)
			if !ok {
				return span{}, false
			}
			r.lo, r.hi = min(r.lo, p), max(r.hi, p)
		}
	}
	return r, true
}

func mulInt(x, y int64) (int64, bool) {
	if x == 0 || y == 0 {
		return 0, true
	}
	ux, uy := uint64(x), uint64(y)
	if x < 0 {
		ux = -ux
	}
	if y < 0 {
		uy = -uy
	}
	hi, lo := bits.Mul64(ux, uy)
	neg := (x < 0) != (y < 0)
	if hi != 0 || lo > math.MaxInt64+1 || (lo == math.MaxInt64+1 && !neg) {
		return 0, false
	}
	if neg {
		return -int64(lo), true
	}
	return int64(lo), true
}

// nonNeg reports whether r is known never to be negative.
func (e *Emitter) nonNeg(r Reg) bool {
	s, ok := e.ranges[r]
	return ok && s.lo >= 0
}
