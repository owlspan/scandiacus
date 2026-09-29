package main

// Division by a constant without idiv.
//
// idiv takes forty to ninety cycles, and `i % 7` in a loop is common
// enough to be the whole cost of the loop. Division by a constant d is
// a multiplication by a precomputed reciprocal instead, which is what
// every C compiler does: Granlund and Montgomery, "Division by
// Invariant Integers using Multiplication", 1994, the signed
// multiply-add form.
//
// For d >= 2 and not a power of two, with l = ceil(log2 d):
//
//	m    = floor(2^(63+l) / d) + 1        between 2^63 and 2^64
//	q    = (hi(n * (m - 2^64)) + n) >> (l - 1)
//	q    = q - (n >> 63)                   round towards zero
//
// where hi is the high word of the signed 128-bit product. A power of
// two is a shift with a correction for negative n. Anything else - one,
// zero, a negative divisor - stays idiv. The magic numbers are checked
// against real division in divconst_test.go.

import (
	"math/big"
	"math/bits"
)

// divMagic is m - 2^64 as a signed word, and the shift, for d >= 2 that
// is not a power of two.
func divMagic(d int64) (int64, uint) {
	l := uint(bits.Len64(uint64(d - 1)))
	m := new(big.Int).Lsh(big.NewInt(1), 63+l)
	m.Quo(m, big.NewInt(d))
	m.Add(m, big.NewInt(1))
	return int64(m.Uint64()), l - 1
}

func isPow2(d int64) bool { return d > 0 && d&(d-1) == 0 }

// constDivisor reports whether a division by d is done here rather
// than with idiv.
func constDivisor(d int64) bool { return d >= 2 }

// selDivConst emits n / d or n % d for a constant d the check above
// accepted. The result is left in rdx.
func (e *Emitter) selDivConst(in Instr, d int64) {
	e.line("mov rcx, %s", e.loc(in.A)) // n
	if isPow2(d) {
		k := bits.TrailingZeros64(uint64(d))
		// (n + (n < 0 ? d-1 : 0)) >> k rounds towards zero.
		e.line("mov rdx, rcx")
		e.line("sar rdx, 63")
		e.line("shr rdx, %d", 64-k)
		e.line("add rdx, rcx")
		if in.Op == OpDiv {
			e.line("sar rdx, %d", k)
			return
		}
		// n - (q << k): the low bits are what the rounding dropped.
		e.line("and rdx, %d", -d)
		e.line("mov rax, rcx")
		e.line("sub rax, rdx")
		e.line("mov rdx, rax")
		return
	}
	m, sh := divMagic(d)
	e.line("mov rax, %d", m)
	e.line("imul rcx")
	e.line("add rdx, rcx")
	if sh > 0 {
		e.line("sar rdx, %d", sh)
	}
	e.line("mov rax, rcx")
	e.line("sar rax, 63")
	e.line("sub rdx, rax")
	if in.Op == OpDiv {
		return
	}
	// n - q*d.
	e.line("mov rax, %d", d)
	e.line("imul rax, rdx")
	e.line("mov rdx, rcx")
	e.line("sub rdx, rax")
}
