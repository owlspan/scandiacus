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
