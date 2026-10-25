package main

import (
	"math"
	"math/bits"
	"math/rand"
	"testing"
)

// mulHiSigned is the high word of a signed 64 x 64 product, what the
// one-operand imul leaves in rdx.
func mulHiSigned(a, b int64) int64 {
	hi, _ := bits.Mul64(uint64(a), uint64(b))
	if a < 0 {
		hi -= uint64(b)
	}
	if b < 0 {
		hi -= uint64(a)
	}
	return int64(hi)
}

// simulate runs the instruction sequences selDivConst emits, in Go.
func simulate(n, d int64) (q, r int64) {
	if isPow2(d) {
		k := bits.TrailingZeros64(uint64(d))
		adj := int64(uint64(n>>63) >> (64 - k))
		t := n + adj
		return t >> k, n - (t & -d)
	}
	m, sh := divMagic(d)
	q = (mulHiSigned(m, n) + n) >> sh
	q -= n >> 63
	return q, n - q*d
}

// TestConstDivision holds the multiply-and-shift division to Go's own
// / and %, which truncate towards zero exactly as idiv does.
func TestConstDivision(t *testing.T) {
	divisors := []int64{2, 3, 5, 6, 7, 9, 10, 11, 12, 13, 25, 60, 100, 125, 641, 1000,
		1000003, 7919, 1 << 20, 1<<31 - 1, 1 << 31, 1<<31 + 1, 1<<40 + 7}
	for d := int64(2); d < 300; d++ {
		divisors = append(divisors, d)
	}
	rng := rand.New(rand.NewSource(1))
	edges := []int64{0, 1, -1, 2, -2, math.MaxInt64, math.MinInt64, math.MaxInt64 - 1,
		math.MinInt64 + 1, 1 << 62, -(1 << 62)}
	for _, d := range divisors {
		ns := append([]int64{}, edges...)
		for _, k := range []int64{-3, -2, -1, 0, 1, 2, 3} {
			ns = append(ns, d*k, d*k+1, d*k-1, d*1000+k)
		}
		for i := 0; i < 2000; i++ {
			ns = append(ns, int64(rng.Uint64()), rng.Int63n(1<<20)-1<<19)
		}
		for _, n := range ns {
			q, r := simulate(n, d)
			if q != n/d || r != n%d {
				t.Fatalf("%d / %d: got %d rem %d, want %d rem %d", n, d, q, r, n/d, n%d)
			}
		}
	}
}

// TestUnsignedConstDivision holds the unsigned form, used for a
// dividend known not to be negative, to Go's division over the whole
// range it is used for, 0 to 2^63 - 1.
func TestUnsignedConstDivision(t *testing.T) {
	divisors := []int64{3, 5, 6, 7, 9, 10, 11, 12, 13, 25, 60, 100, 125, 641, 1000,
		1000003, 7919, 1<<31 - 1, 1<<31 + 1, 1<<40 + 7, math.MaxInt64, math.MaxInt64 - 2}
	for d := int64(3); d < 300; d++ {
		if !isPow2(d) {
			divisors = append(divisors, d)
		}
	}
	rng := rand.New(rand.NewSource(2))
	for _, d := range divisors {
		m, sh := udivMagic(d)
		ns := []int64{0, 1, 2, d - 1, d, d + 1, math.MaxInt64, math.MaxInt64 - 1, 1 << 62}
		for k := int64(1); k < 4; k++ {
			ns = append(ns, math.MaxInt64/d*d-k, math.MaxInt64/d*d+k-1)
		}
		for i := 0; i < 3000; i++ {
			ns = append(ns, int64(rng.Uint64()>>1), rng.Int63n(1<<24))
		}
		for _, n := range ns {
			if n < 0 {
				continue
			}
			hi, _ := bits.Mul64(uint64(n), m)
			q := int64(hi >> sh)
			if q != n/d || n-q*d != n%d {
				t.Fatalf("%d / %d: got %d, want %d", n, d, q, n/d)
			}
		}
	}
}

// TestValueRanges checks the ranges worked out for a counted loop's
// counter and what is computed from it, and that an overflow, a
// negative or an unknown operand gives no range.
func TestValueRanges(t *testing.T) {
	f := &Func{Ranges: map[int64]span{0: {0, 400000000}, 1: {-5, 5}, 2: {0, 3037000500}}}
	c := func(d Reg, v int64) Instr { return Instr{Op: OpConst, Dst: d, A: NoReg, B: NoReg, Imm: v} }
	ld := func(d Reg, s int64) Instr { return Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: s} }
	op := func(o Op, d, a, b Reg) Instr { return Instr{Op: o, Dst: d, A: a, B: b} }
	f.Code = []Instr{
		ld(1, 0),           // i in [0, 4e8]
		op(OpMul, 2, 1, 1), // i*i in [0, 1.6e17]
		c(3, 7),
		op(OpMod, 4, 2, 3), // [0, 6]
		c(5, 3),
		op(OpShr, 6, 1, 5), // [0, 5e7]
		ld(7, 1),           // [-5, 5]
		op(OpMod, 8, 7, 3), // unknown: may be negative
		ld(9, 2),
		op(OpMul, 10, 9, 9),  // overflows: unknown
		op(OpSub, 11, 1, 7),  // [-5, 400000005]
		op(OpBAnd, 12, 7, 3), // [0, 7]
		ld(13, 5),            // no range
		op(OpAdd, 14, 13, 1), // unknown
	}
	r := valueRanges(f)
	want := map[Reg]span{1: {0, 400000000}, 2: {0, 160000000000000000}, 4: {0, 6},
		6: {0, 50000000}, 7: {-5, 5}, 11: {-5, 400000005}, 12: {0, 7}}
	for reg, w := range want {
		if got, ok := r[reg]; !ok || got != w {
			t.Errorf("v%d: %v %v, want %v", reg, got, ok, w)
		}
	}
	for _, reg := range []Reg{8, 10, 13, 14} {
		if got, ok := r[reg]; ok {
			t.Errorf("v%d: %v, want unknown", reg, got)
		}
	}
}
