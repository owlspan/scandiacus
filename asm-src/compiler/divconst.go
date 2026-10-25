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

// udivMagic is the multiplier and shift for dividing a number known
// not to be negative by d >= 2 that is not a power of two:
//
//	q = hi(n * m) >> s        unsigned, for 0 <= n < 2^63
//
// with m = ceil(2^p / d) for the smallest p from 64 that keeps the
// error m*d - 2^p within 2^(p-63), Granlund and Montgomery's condition
// for 63-bit dividends. p = 63 + ceil(log2 d) always qualifies and
// keeps m below 2^64, so the search ends.
func udivMagic(d int64) (uint64, uint) {
	bd := big.NewInt(d)
	for p := uint(64); ; p++ {
		two := new(big.Int).Lsh(big.NewInt(1), p)
		m := new(big.Int).Add(two, new(big.Int).Sub(bd, big.NewInt(1)))
		m.Quo(m, bd)
		if m.BitLen() > 64 {
			continue
		}
		err := new(big.Int).Sub(new(big.Int).Mul(m, bd), two)
		if err.Cmp(new(big.Int).Lsh(big.NewInt(1), p-63)) <= 0 {
			return m.Uint64(), p - 64
		}
	}
}

// selDivConst emits n / d or n % d for a constant d the check above
// accepted. The result is left in rdx, unless the
// return value says it was already put in the destination.
func (e *Emitter) selDivConst(in Instr, d int64) bool {
	n := e.loc(in.A)
	if e.nonNeg(in.A) {
		return e.selUDivConst(in, d, n)
	}
	e.line("mov rcx, %s", n)
	if isPow2(d) {
		k := bits.TrailingZeros64(uint64(d))
		// (n + (n < 0 ? d-1 : 0)) >> k rounds towards zero.
		e.line("mov rdx, rcx")
		e.line("sar rdx, 63")
		e.line("shr rdx, %d", 64-k)
		e.line("add rdx, rcx")
		if in.Op == OpDiv {
			e.line("sar rdx, %d", k)
			return false
		}
		// n - (q << k): the low bits are what the rounding dropped.
		e.line("and rdx, %d", -d)
		e.line("mov rax, rcx")
		e.line("sub rax, rdx")
		e.line("mov rdx, rax")
		return false
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
		return false
	}
	// n - q*d.
	e.line("mov rax, %d", d)
	e.line("imul rax, rdx")
	e.line("mov rdx, rcx")
	e.line("sub rdx, rax")
	return false
}

// selUDivConst is selDivConst for a dividend known not to be negative
// (see ranges.go): unsigned, so no rounding fix, and a power of two is
// one shift or one and. clang does the same once it knows the sign.
// n is where the dividend sits. A pooled register is read in place; rax
// and rdx are overwritten and anything in memory is read twice, so
// those two go through rcx.
func (e *Emitter) selUDivConst(in Instr, d int64, n string) bool {
	if !isRegLoc(n) || n == "rax" || n == "rdx" {
		e.line("mov rcx, %s", n)
		n = "rcx"
	}
	if isPow2(d) {
		e.line("mov rdx, %s", n)
		if in.Op == OpDiv {
			e.line("shr rdx, %d", bits.TrailingZeros64(uint64(d)))
		} else if fits32(d - 1) {
			e.line("and rdx, %d", d-1)
		} else {
			e.line("mov rax, %d", d-1)
			e.line("and rdx, rax")
		}
		return false
	}
	m, sh := udivMagic(d)
	e.line("mov rax, %d", int64(m))
	e.line("mul %s", n)
	if sh > 0 {
		e.line("shr rdx, %d", sh)
	}
	if in.Op == OpDiv {
		return false
	}
	// n - q*d.
	if fits32(d) {
		e.line("imul rdx, rdx, %d", d)
	} else {
		e.line("mov rax, %d", d)
		e.line("imul rdx, rax")
	}
	// Subtract into the result's own register when it has one, which
	// saves the trip through rdx.
	if r := e.loc(in.Dst); isRegLoc(r) && r != "rax" && r != "rcx" && r != "rdx" {
		if r != n {
			e.line("mov %s, %s", r, n)
		}
		e.line("sub %s, rdx", r)
		return true
	}
	e.line("neg rdx")
	e.line("add rdx, %s", n)
	return false
}
