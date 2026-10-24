package main

// Shortest float formatting: Ryu (Ulf Adams, PLDI 2018), the algorithm
// Go's strconv also uses for the shortest form.
//
// The old __vy_floattostr found the shortest decimal that reads back to
// the same double by asking sprintf for 1, 2, 3, ... digits and reading
// each answer back with strtod, then printed it twice more for the tie
// and the notation: up to twenty C library calls, about 5 us a float.
// Ryu gets the same digits with integer arithmetic only: three 64x128
// bit multiplies by a table entry, then removing digits while the
// interval of values that round to the double still holds a shorter
// number. No C library call is left but the allocation of the result.
//
// The digits match Go exactly, ties included (Ryu keeps the closest
// candidate and breaks a tie to even, as strconv does). The notation is
// Go's %v: exponential when the decimal exponent is below -4 or at
// least 6, with a two-digit exponent at least.
//
// Infinities and NaN still take the old path, which spells them, and so
// does an f32, which needs the round trip judged at single precision.

import (
	"fmt"
	"math/big"
	"strings"
)

const (
	ryuPow5InvBits = 125 // DOUBLE_POW5_INV_BITCOUNT
	ryuPow5Bits    = 125 // DOUBLE_POW5_BITCOUNT
	ryuInvCount    = 342
	ryuPowCount    = 326
)

// ryuTables writes the two tables of 128-bit multipliers, low quad
// first. __ryu_inv[q] is floor(2^k / 5^q) + 1 with k = bits(5^q) - 1 +
// 125, used for a positive binary exponent; __ryu_pow[i] is 5^i cut or
// widened to its top 125 bits, for a negative one.
func ryuTables() string {
	var b strings.Builder
	quad := func(v *big.Int) {
		mask := new(big.Int).SetUint64(^uint64(0))
		lo := new(big.Int).And(v, mask).Uint64()
		hi := new(big.Int).Rsh(v, 64).Uint64()
		fmt.Fprintf(&b, "    .quad %#x\n    .quad %#x\n", lo, hi)
	}
	b.WriteString("    .align 8\n__ryu_inv:\n")
	for q := 0; q < ryuInvCount; q++ {
		p := new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(q)), nil)
		k := uint(p.BitLen() - 1 + ryuPow5InvBits)
		v := new(big.Int).Lsh(big.NewInt(1), k)
		v.Div(v, p)
		v.Add(v, big.NewInt(1))
		quad(v)
	}
	b.WriteString("    .align 8\n__ryu_pow:\n")
	for i := 0; i < ryuPowCount; i++ {
		p := new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(i)), nil)
		shift := p.BitLen() - ryuPow5Bits
		if shift >= 0 {
			p.Rsh(p, uint(shift))
		} else {
			p.Lsh(p, uint(-shift))
		}
		quad(p)
	}
	return b.String()
}

// The frame, from rsp after the prologue. rdi is the write pointer into
// the output buffer for the whole function.
const (
	ryuDbl    = 40  // the double, to read its bits
	ryuOut    = 48  // output text, 64 bytes
	ryuDigEnd = 144 // the digits are written backwards, ending here
	ryuVr     = 144
	ryuVp     = 152
	ryuVm     = 160
	ryuE10    = 168
	ryuVmTZ   = 176
	ryuVrTZ   = 184
	ryuFrame  = 264 // with the seven pushes, keeps rsp 16-aligned
)

// The unsigned division by ten as a multiply: floor(x * ceil(2^67/10)
// / 2^67) is x/10 for every 64-bit x.
const ryuDiv10 = "0xCCCCCCCCCCCCCCCD"

type ryuGen struct {
	b strings.Builder
	n int
}

func (g *ryuGen) l(format string, args ...any) {
	g.b.WriteString("    ")
	fmt.Fprintf(&g.b, format, args...)
	g.b.WriteString("\n")
}

func (g *ryuGen) label(s string) { g.b.WriteString(s + ":\n") }

func (g *ryuGen) fresh() string {
	g.n++
	return fmt.Sprintf(".Lryu_%d", g.n)
}

// mulShift sets the slot dst to ((m * table[r8]) >> j) where m is in
// r10, the 128-bit entry is at [r8] and j is in r9: the sum of the two
// partial products is 128 bits, and Ryu's ranges keep j-64 in [2, 59],
// so the shift is one funnel of two 64-bit shifts.
func (g *ryuGen) mulShift(dst int) {
	g.l("mov rax, r10")
	g.l("mul qword ptr [r8]")
	g.l("mov r11, rdx")
	g.l("mov rax, r10")
	g.l("mul qword ptr [r8+8]")
	g.l("add rax, r11")
	g.l("adc rdx, 0")
	g.l("mov ecx, r9d")
	g.l("sub ecx, 64")
	g.l("shr rax, cl")
	g.l("neg ecx")
	g.l("add ecx, 64")
	g.l("shl rdx, cl")
	g.l("or rax, rdx")
	g.l("mov qword ptr [rsp+%d], rax", dst)
}

// mulShiftAll computes vr, vp and vm from mv = 4*m2 (rbx) and mmShift (r14):
// the double's value and the two ends of the interval that rounds to
// it, all times four so the ends are integers.
func (g *ryuGen) mulShiftAll() {
	g.l("lea r10, [rbx+2]")
	g.mulShift(ryuVp)
	g.l("mov r10, rbx")
	g.mulShift(ryuVr)
	g.l("lea r10, [rbx-1]")
	g.l("sub r10, r14")
	g.mulShift(ryuVm)
}

// multipleOfPow5 leaves 1 in rax when the value in rax has at least q
// (rsi) factors of five, else 0. The value is never zero.
func (g *ryuGen) multipleOfPow5() {
	loop, done := g.fresh(), g.fresh()
	g.l("xor r11d, r11d")
	g.label(loop)
	g.l("xor edx, edx")
	g.l("mov ecx, 5")
	g.l("mov r10, rax")
	g.l("div rcx")
	g.l("test rdx, rdx")
	g.l("jne %s", done)
	g.l("add r11, 1")
	g.l("jmp %s", loop)
	g.label(done)
	g.l("xor eax, eax")
	g.l("cmp r11, rsi")
	g.l("setge al")
}

// div10 sets dst to src/10, using rax and rdx; rcx holds ryuDiv10.
func (g *ryuGen) div10(dst, src string) {
	g.l("mov rax, %s", src)
	g.l("mul rcx")
	g.l("shr rdx, 3")
	if dst != "rdx" {
		g.l("mov %s, rdx", dst)
	}
}

// mod10 sets dst to src - 10*quot.
func (g *ryuGen) mod10(dst, src, quot string) {
	g.l("lea %s, [%s+%s*4]", dst, quot, quot)
	g.l("add %s, %s", dst, dst)
	g.l("neg %s", dst)
	g.l("add %s, %s", dst, src)
}

func (g *ryuGen) put(c string) {
	g.l("mov byte ptr [rdi], %s", c)
	g.l("add rdi, 1")
}

func ryuFloatToStr() string {
	g := &ryuGen{}
	g.b.WriteString("\n# Ryu: see ryu.go.\n")
	g.label("__vy_floattostr")
	g.l("push rbp")
	g.l("mov rbp, rsp")
	for _, r := range []string{"rbx", "rsi", "rdi", "r12", "r13", "r14", "r15"} {
		g.l("push %s", r)
	}
	g.l("sub rsp, %d", ryuFrame)
	g.l("movsd qword ptr [rsp+%d], xmm0", ryuDbl)
	g.l("mov rax, qword ptr [rsp+%d]", ryuDbl)
	g.l("mov rcx, rax")
	g.l("shr rcx, 52")
	g.l("and ecx, 2047")
	g.l("cmp ecx, 2047")
	g.l("je .Lryu_slow")
	g.l("lea rdi, [rsp+%d]", ryuOut)
	g.l("test rax, rax")
	g.l("jns .Lryu_pos")
	g.put("45")
	g.label(".Lryu_pos")
	g.l("mov rdx, rax")
	g.l("shl rdx, 12")
	g.l("shr rdx, 12")
	g.l("mov r8, rcx")
	g.l("or r8, rdx")
	g.l("jne .Lryu_nonzero")
	g.put("48")
	g.l("jmp .Lryu_text")

	g.label(".Lryu_nonzero")
	// mmShift: the gap below is half the gap above only at a power of
	// two that is not the smallest normal.
	g.l("xor r14d, r14d")
	g.l("test rdx, rdx")
	g.l("jne .Lryu_mm1")
	g.l("cmp ecx, 1")
	g.l("jg .Lryu_mm0")
	g.label(".Lryu_mm1")
	g.l("mov r14d, 1")
	g.label(".Lryu_mm0")
	g.l("test ecx, ecx")
	g.l("jne .Lryu_normal")
	g.l("mov r12, rdx")
	g.l("mov r13, -1076")
	g.l("jmp .Lryu_have")
	g.label(".Lryu_normal")
	g.l("mov r12, 1")
	g.l("shl r12, 52")
	g.l("or r12, rdx")
	g.l("lea r13, [rcx-1077]")
	g.label(".Lryu_have")
	// r12 = m2, r13 = e2, r14 = mmShift, r15 = acceptBounds (m2 even),
	// rbx = mv = 4*m2.
	g.l("mov r15, r12")
	g.l("not r15")
	g.l("and r15d, 1")
	g.l("mov qword ptr [rsp+%d], 0", ryuVmTZ)
	g.l("mov qword ptr [rsp+%d], 0", ryuVrTZ)
	g.l("mov rbx, r12")
	g.l("shl rbx, 2")
	g.l("test r13, r13")
	g.l("js .Lryu_neg")

	// e2 >= 0: q = log10(2^e2), less one past e2 = 3.
	g.l("mov eax, 78913")
	g.l("imul rax, r13")
	g.l("shr rax, 18")
	g.l("cmp r13, 3")
	g.l("jle .Lryu_q1")
	g.l("sub rax, 1")
	g.label(".Lryu_q1")
	g.l("mov rsi, rax")
	g.l("mov qword ptr [rsp+%d], rax", ryuE10)
	// j = -e2 + q + 125 + pow5bits(q) - 1, pow5bits(q) = (q*1217359 >> 19) + 1
	g.l("mov eax, 1217359")
	g.l("imul rax, rsi")
	g.l("shr rax, 19")
	g.l("add rax, %d", ryuPow5InvBits)
	g.l("sub rax, r13")
	g.l("add rax, rsi")
	g.l("mov r9, rax")
	g.l("mov r8, rsi")
	g.l("shl r8, 4")
	g.l("lea rax, __ryu_inv[rip]")
	g.l("add r8, rax")
	g.mulShiftAll()
	g.l("cmp rsi, 21")
	g.l("jg .Lryu_remove")
	g.l("mov rax, rbx")
	g.l("xor edx, edx")
	g.l("mov ecx, 5")
	g.l("div rcx")
	g.l("test rdx, rdx")
	g.l("jne .Lryu_pnot5")
	g.l("mov rax, rbx")
	g.multipleOfPow5()
	g.l("mov qword ptr [rsp+%d], rax", ryuVrTZ)
	g.l("jmp .Lryu_remove")
	g.label(".Lryu_pnot5")
	g.l("test r15, r15")
	g.l("je .Lryu_pnoacc")
	g.l("lea rax, [rbx-1]")
	g.l("sub rax, r14")
	g.multipleOfPow5()
	g.l("mov qword ptr [rsp+%d], rax", ryuVmTZ)
	g.l("jmp .Lryu_remove")
	g.label(".Lryu_pnoacc")
	g.l("lea rax, [rbx+2]")
	g.multipleOfPow5()
	g.l("mov rdx, qword ptr [rsp+%d]", ryuVp)
	g.l("sub rdx, rax")
	g.l("mov qword ptr [rsp+%d], rdx", ryuVp)
	g.l("jmp .Lryu_remove")

	// e2 < 0: q = log10(5^-e2), less one past -e2 = 1.
	g.label(".Lryu_neg")
	g.l("mov r11, r13")
	g.l("neg r11")
	g.l("mov eax, 732923")
	g.l("imul rax, r11")
	g.l("shr rax, 20")
	g.l("cmp r11, 1")
	g.l("jle .Lryu_q2")
	g.l("sub rax, 1")
	g.label(".Lryu_q2")
	g.l("mov rsi, rax")
	g.l("lea rax, [rsi+r13]")
	g.l("mov qword ptr [rsp+%d], rax", ryuE10)
	// i = -e2 - q; j = q - (pow5bits(i) - 125)
	g.l("mov rax, r11")
	g.l("sub rax, rsi")
	g.l("mov r8, rax")
	g.l("mov edx, 1217359")
	g.l("imul rdx, rax")
	g.l("shr rdx, 19")
	g.l("add rdx, 1")
	g.l("sub rdx, %d", ryuPow5Bits)
	g.l("mov r9, rsi")
	g.l("sub r9, rdx")
	g.l("shl r8, 4")
	g.l("lea rax, __ryu_pow[rip]")
	g.l("add r8, rax")
	g.mulShiftAll()
	g.l("cmp rsi, 1")
	g.l("jg .Lryu_nbig")
	g.l("mov qword ptr [rsp+%d], 1", ryuVrTZ)
	g.l("test r15, r15")
	g.l("je .Lryu_nnoacc")
	g.l("mov qword ptr [rsp+%d], r14", ryuVmTZ)
	g.l("jmp .Lryu_remove")
	g.label(".Lryu_nnoacc")
	g.l("mov rdx, qword ptr [rsp+%d]", ryuVp)
	g.l("sub rdx, 1")
	g.l("mov qword ptr [rsp+%d], rdx", ryuVp)
	g.l("jmp .Lryu_remove")
	g.label(".Lryu_nbig")
	g.l("cmp rsi, 63")
	g.l("jge .Lryu_remove")
	g.l("mov ecx, esi")
	g.l("mov rax, 1")
	g.l("shl rax, cl")
	g.l("sub rax, 1")
	g.l("and rax, rbx")
	g.l("jne .Lryu_remove")
	g.l("mov qword ptr [rsp+%d], 1", ryuVrTZ)

	// Removing digits. r12 = vr, r13 = vp, r14 = vm, rsi = digits
	// removed, r11 = the last digit removed, rbx = vmTZ, r15 still
	// acceptBounds, rdi = vrTZ (the write pointer is pushed meanwhile).
	// r8 and r10 hold quotients, r9 a remainder.
	g.label(".Lryu_remove")
	g.l("push rdi")
	g.l("sub rsp, 8")
	off := 16 // the push and the pad move every slot
	g.l("mov r12, qword ptr [rsp+%d]", ryuVr+off)
	g.l("mov r13, qword ptr [rsp+%d]", ryuVp+off)
	g.l("mov r14, qword ptr [rsp+%d]", ryuVm+off)
	g.l("mov rbx, qword ptr [rsp+%d]", ryuVmTZ+off)
	g.l("mov rdi, qword ptr [rsp+%d]", ryuVrTZ+off)
	g.l("xor esi, esi")
	g.l("xor r11d, r11d")
	g.l("mov rcx, %s", ryuDiv10)
	g.l("mov rax, rbx")
	g.l("or rax, rdi")
	g.l("je .Lryu_fast")

	// The general case: a trailing-zero flag is live, so track them.
	g.label(".Lryu_loopA")
	g.div10("r8", "r13")
	g.div10("r10", "r14")
	g.l("cmp r8, r10")
	g.l("jbe .Lryu_endA")
	g.mod10("r9", "r14", "r10")
	g.l("test r9, r9")
	g.l("je .Lryu_a1")
	g.l("xor ebx, ebx")
	g.label(".Lryu_a1")
	g.l("test r11, r11")
	g.l("je .Lryu_a2")
	g.l("xor edi, edi")
	g.label(".Lryu_a2")
	g.l("mov r13, r8")
	g.l("mov r14, r10")
	g.div10("r8", "r12")
	g.mod10("r11", "r12", "r8")
	g.l("mov r12, r8")
	g.l("add rsi, 1")
	g.l("jmp .Lryu_loopA")
	g.label(".Lryu_endA")
	g.l("test rbx, rbx")
	g.l("je .Lryu_endB")
	g.label(".Lryu_loopB")
	g.div10("r10", "r14")
	g.mod10("r9", "r14", "r10")
	g.l("test r9, r9")
	g.l("jne .Lryu_endB")
	g.l("mov r14, r10")
	g.div10("r13", "r13")
	g.l("test r11, r11")
	g.l("je .Lryu_b1")
	g.l("xor edi, edi")
	g.label(".Lryu_b1")
	g.div10("r8", "r12")
	g.mod10("r11", "r12", "r8")
	g.l("mov r12, r8")
	g.l("add rsi, 1")
	g.l("jmp .Lryu_loopB")
	g.label(".Lryu_endB")
	// An exact tie (the removed digits are 5 then zeros) goes to even.
	g.l("test rdi, rdi")
	g.l("je .Lryu_nottie")
	g.l("cmp r11, 5")
	g.l("jne .Lryu_nottie")
	g.l("mov eax, r12d")
	g.l("and eax, 1")
	g.l("jne .Lryu_nottie")
	g.l("mov r11d, 4")
	g.label(".Lryu_nottie")
	// output = vr + ((vr == vm && (!accept || !vmTZ)) || last >= 5)
	g.l("cmp r11, 5")
	g.l("jge .Lryu_up")
	g.l("cmp r12, r14")
	g.l("jne .Lryu_out")
	g.l("test r15, r15")
	g.l("je .Lryu_up")
	g.l("test rbx, rbx")
	g.l("jne .Lryu_out")
	g.l("jmp .Lryu_up")

	// The common case: no trailing zeros to track.
	g.label(".Lryu_fast")
	g.label(".Lryu_loopC")
	g.div10("r8", "r13")
	g.div10("r10", "r14")
	g.l("cmp r8, r10")
	g.l("jbe .Lryu_endC")
	g.l("mov r13, r8")
	g.l("mov r14, r10")
	g.div10("r8", "r12")
	g.mod10("r11", "r12", "r8")
	g.l("mov r12, r8")
	g.l("add rsi, 1")
	g.l("jmp .Lryu_loopC")
	g.label(".Lryu_endC")
	g.l("cmp r12, r14")
	g.l("je .Lryu_up")
	g.l("cmp r11, 5")
	g.l("jl .Lryu_out")
	g.label(".Lryu_up")
	g.l("add r12, 1")
	g.label(".Lryu_out")
	g.l("add rsp, 8")
	g.l("pop rdi")
	g.l("add rsi, qword ptr [rsp+%d]", ryuE10)

	// r12 holds the digits, rsi the exponent of the last one. Write the
	// digits backwards so they end at ryuDigEnd; r8 is their start.
	g.l("lea r8, [rsp+%d]", ryuDigEnd)
	g.label(".Lryu_digit")
	g.div10("r10", "r12")
	g.mod10("r9", "r12", "r10")
	g.l("add r9, 48")
	g.l("mov rax, r9")
	g.l("sub r8, 1")
	g.l("mov byte ptr [r8], al")
	g.l("mov r12, r10")
	g.l("test r12, r12")
	g.l("jne .Lryu_digit")
	// r9 = digit count, rsi = X, the exponent of the first digit.
	g.l("lea r9, [rsp+%d]", ryuDigEnd)
	g.l("sub r9, r8")
	g.l("add rsi, r9")
	g.l("sub rsi, 1")
	g.l("cmp rsi, -4")
	g.l("jl .Lryu_exp")
	g.l("cmp rsi, 6")
	g.l("jge .Lryu_exp")
	g.l("test rsi, rsi")
	g.l("js .Lryu_small")

	// Fixed, X >= 0: the point goes after X+1 digits, if there are more;
	// if there are fewer, zeros make up the integer part.
	g.l("xor r10d, r10d")
	g.label(".Lryu_fixed")
	g.l("cmp r10, r9")
	g.l("jge .Lryu_fixpad")
	g.l("lea rax, [rsi+1]")
	g.l("cmp r10, rax")
	g.l("jne .Lryu_fixdig")
	g.put("46")
	g.label(".Lryu_fixdig")
	g.l("movzx eax, byte ptr [r8+r10]")
	g.put("al")
	g.l("add r10, 1")
	g.l("jmp .Lryu_fixed")
	g.label(".Lryu_fixpad")
	g.l("lea rax, [rsi+1]")
	g.l("cmp r10, rax")
	g.l("jge .Lryu_text")
	g.put("48")
	g.l("add r10, 1")
	g.l("jmp .Lryu_fixpad")

	// Fixed, X < 0: 0.000ddd
	g.label(".Lryu_small")
	g.put("48")
	g.put("46")
	g.l("mov r10, rsi")
	g.l("add r10, 1")
	g.label(".Lryu_smallz")
	g.l("test r10, r10")
	g.l("je .Lryu_alldig")
	g.put("48")
	g.l("add r10, 1")
	g.l("jmp .Lryu_smallz")
	g.label(".Lryu_alldig")
	g.l("xor r10d, r10d")
	g.label(".Lryu_alldig2")
	g.l("cmp r10, r9")
	g.l("jge .Lryu_text")
	g.l("movzx eax, byte ptr [r8+r10]")
	g.put("al")
	g.l("add r10, 1")
	g.l("jmp .Lryu_alldig2")

	// Exponential: d.ddde+XX
	g.label(".Lryu_exp")
	g.l("movzx eax, byte ptr [r8]")
	g.put("al")
	g.l("cmp r9, 1")
	g.l("je .Lryu_e")
	g.put("46")
	g.l("mov r10d, 1")
	g.label(".Lryu_edig")
	g.l("cmp r10, r9")
	g.l("jge .Lryu_e")
	g.l("movzx eax, byte ptr [r8+r10]")
	g.put("al")
	g.l("add r10, 1")
	g.l("jmp .Lryu_edig")
	g.label(".Lryu_e")
	g.put("101")
	g.l("test rsi, rsi")
	g.l("js .Lryu_eneg")
	g.put("43")
	g.l("jmp .Lryu_eabs")
	g.label(".Lryu_eneg")
	g.put("45")
	g.l("neg rsi")
	g.label(".Lryu_eabs")
	g.l("cmp rsi, 100")
	g.l("jl .Lryu_e2")
	g.l("mov rax, rsi")
	g.l("xor edx, edx")
	g.l("mov ecx, 100")
	g.l("div rcx")
	g.l("add eax, 48")
	g.put("al")
	g.l("mov rsi, rdx")
	g.label(".Lryu_e2")
	g.l("mov rax, rsi")
	g.l("xor edx, edx")
	g.l("mov ecx, 10")
	g.l("div rcx")
	g.l("add eax, 48")
	g.put("al")
	g.l("add edx, 48")
	g.put("dl")

	// The text is in the buffer up to rdi: copy it into a new str.
	g.label(".Lryu_text")
	g.l("mov byte ptr [rdi], 0")
	g.l("lea rcx, [rsp+%d]", ryuOut)
	g.l("sub rdi, rcx")
	g.l("lea rcx, [rdi+1]")
	g.l("mov rdx, rcx")
	g.l("shl rdx, %d", tagShift)
	g.l("call __vy_talloc")
	g.l("mov rbx, rax")
	g.l("mov rcx, rax")
	g.l("lea rdx, [rsp+%d]", ryuOut)
	g.l("call strcpy")
	g.l("mov rax, rbx")
	g.l("add rsp, %d", ryuFrame)
	for _, r := range []string{"r15", "r14", "r13", "r12", "rdi", "rsi", "rbx"} {
		g.l("pop %s", r)
	}
	g.l("pop rbp")
	g.l("ret")

	g.label(".Lryu_slow")
	g.l("movsd xmm0, qword ptr [rsp+%d]", ryuDbl)
	g.l("add rsp, %d", ryuFrame)
	for _, r := range []string{"r15", "r14", "r13", "r12", "rdi", "rsi", "rbx"} {
		g.l("pop %s", r)
	}
	g.l("pop rbp")
	g.l("jmp __vy_floattostr_slow")
	return g.b.String()
}
