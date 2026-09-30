package main

// Fixed-width numbers: i8, u8, i16, u16, i32, u32, u64 and f32. See
// frontend/fixed.go for the rules; this is how they run.
//
// Each is one word, like an int or a float, and tracked the way a
// pointer or an enum is: kind kInt (kFloat for f32) with the type's
// name in name. So every path that moves a word - a slot, a register,
// a list element, a struct field, an argument - takes one unchanged.
//
// An integer one is kept in canonical form: sign-extended from its
// width if signed, zero-extended if not. Then comparison, equality and
// printing need nothing new, and neither do division and the right
// shift below 64 bits, because the ordinary signed forms give the right
// answer on canonical inputs. What does need something is any operation
// whose result can leave the range - add, subtract, multiply, negate,
// complement, shift left, and a signed division, since i32 min / -1 is
// out of range - which is followed by OpExt to wrap it back, the same
// one instruction a load of that width would be.
//
// u64 has no spare bits to extend into, so it gets the unsigned forms
// of what differs: division, remainder, right shift, the four
// orderings, and conversion to and from a float.
//
// An f32 is held as the double of the same value, and every operation
// on one is rounded to single precision after it (OpF32Round). For +,
// -, *, / that is exactly IEEE single arithmetic: a double carries more
// than twice the bits of a single, so computing in double and rounding
// once gives the correctly rounded single result.

import (
	"fmt"
	"strings"

	front "veylfront"
)

func vFixed(name string) vty {
	if name == "f32" {
		return vty{k: kFloat, name: name}
	}
	return vty{k: kInt, name: name}
}

// fixedExt is the width a fixed-width integer below 64 bits is extended
// from, as one of the mem* kinds. u64 has none.
var fixedExt = map[string]int64{
	"i8": memI8, "u8": memU8, "i16": memI16, "u16": memU16, "i32": memI32, "u32": memU32,
}

// isFixedName reports one of the fixed-width type names.
func isFixedName(n string) bool {
	_, small := fixedExt[n]
	return small || n == "u64" || n == "f32"
}

// fixedOf is t's fixed-width type name, or "".
func fixedOf(t vty) string {
	if t.res || t.null || (t.k != kInt && t.k != kFloat) || !isFixedName(t.name) {
		return ""
	}
	return t.name
}

func isU64(t vty) bool { return fixedOf(t) == "u64" }
func isF32(t vty) bool { return fixedOf(t) == "f32" }

// cScalarVty is what a read of a C scalar gives: the fixed-width type
// of that name, or int, float or bool for i64, ptr, f64 and bool.
func cScalarVty(name string) vty {
	switch name {
	case "i64", "ptr":
		return vInt
	case "f64":
		return vFloat
	case "bool":
		return vBool
	}
	return vFixed(name)
}

// wrapFixed brings the result of an operation back into its type's
// range: extended from the width for an integer, rounded to single for
// an f32. Anything else passes through.
func (l *lowerer) wrapFixed(v Reg, t vty) Reg {
	name := fixedOf(t)
	if name == "" || name == "u64" {
		l.regTy[v] = t
		return v
	}
	d := l.newReg()
	l.regTy[d] = t
	if name == "f32" {
		l.emit(Instr{Op: OpF32Round, Dst: d, A: v, B: NoReg})
		return d
	}
	l.emit(Instr{Op: OpExt, Dst: d, A: v, B: NoReg, Imm: fixedExt[name]})
	return d
}

// fixedIntOp picks the operation for an integer operator on type t,
// which only u64 changes, and says whether the result must be wrapped.
func fixedIntOp(op Op, t vty) (Op, bool) {
	name := fixedOf(t)
	if name == "u64" {
		switch op {
		case OpDiv:
			return OpDivU, false
		case OpMod:
			return OpModU, false
		case OpShr:
			return OpShrU, false
		case OpLt:
			return OpLtU, false
		case OpLe:
			return OpLeU, false
		case OpGt:
			return OpGtU, false
		case OpGe:
			return OpGeU, false
		}
		return op, false
	}
	if name == "" {
		return op, false
	}
	switch op {
	case OpAdd, OpSub, OpMul, OpNeg, OpBNot, OpShl:
		return op, true
	case OpDiv, OpMod:
		// Only a signed one can leave the range: -128 / -1.
		return op, name[0] == 'i'
	}
	return op, false
}

// convert is the conversion functions: int(x), float(x), i32(x) and
// the rest. An integer narrows by keeping its low bits, like a C cast;
// a float truncates toward zero first. What a float out of the target's
// range becomes is whatever the hardware gives.
func (l *lowerer) convert(v Reg, to vty) Reg {
	from := l.regTy[v]
	fromFloat := from.k == kFloat
	toName := fixedOf(to)
	switch {
	case to.k == kFloat && toName == "":
		// To float.
		if fromFloat {
			if isF32(from) {
				// An f32 is already the double of its value.
				return l.unaryOp(OpF32Round, v, vFloat)
			}
			return v
		}
		if isU64(from) {
			return l.unaryOp(OpU64ToFloat, v, vFloat)
		}
		return l.unaryOp(OpIntToFloat, v, vFloat)

	case toName == "f32":
		if fromFloat {
			return l.unaryOp(OpF32Round, v, to)
		}
		if isU64(from) {
			return l.unaryOp(OpU64ToF32, v, to)
		}
		return l.unaryOp(OpIntToF32, v, to)
	}

	// To an integer.
	if fromFloat {
		if toName == "u64" {
			d := l.newReg()
			l.regTy[d] = to
			l.emit(Instr{Op: OpFloatToU64, Dst: d, A: v, B: l.floatConst(9223372036854775808.0)})
			return d
		}
		v = l.unaryOp(OpFloatToInt, v, vInt)
	}
	if name := fixedOf(to); name != "" && name != "u64" {
		if fixedOf(l.regTy[v]) == name {
			return v
		}
		if fitsWithout(fixedOf(l.regTy[v]), name) {
			return l.retype(v, to)
		}
		d := l.newReg()
		l.regTy[d] = to
		l.emit(Instr{Op: OpExt, Dst: d, A: v, B: NoReg, Imm: fixedExt[name]})
		return d
	}
	if l.regTy[v].eq(to) {
		return v
	}
	return l.retype(v, to)
}

func (l *lowerer) unaryOp(op Op, v Reg, t vty) Reg {
	d := l.newReg()
	l.regTy[d] = t
	l.emit(Instr{Op: op, Dst: d, A: v, B: NoReg})
	return d
}

// printFlavor is the Imm a print, write or to-string op carries: 1 for
// a value that needs its own rendering (a u64 read unsigned, an f32 at
// single precision), 0 otherwise.
func printFlavor(t vty) int64 {
	if isU64(t) || isF32(t) {
		return 1
	}
	return 0
}

// conversionTarget is the type a conversion builtin makes, by name.
func conversionTarget(name string) (vty, bool) {
	switch name {
	case "int":
		return vInt, true
	case "float":
		return vFloat, true
	}
	if isFixedName(name) {
		return vFixed(name), true
	}
	return vVoid, false
}

func init() {
	sigs["int"] = conversionSig(Int)
	sigs["float"] = conversionSig(Float)
	for _, n := range front.FixedNames {
		sigs[n] = conversionSig(front.FixedOf(n))
	}
}

func conversionSig(to *Type) front.Signature {
	return front.Signature{
		Params: []*Type{Any},
		Ret:    to,
		Check: func(c *Checker, x *Call, args []*Type) *Type {
			return c.CheckConversion(x, to, args)
		},
	}
}

// ---- emission ----

// fixLabel is a fresh local label for the branches inside one fixed-
// width instruction.
func (e *Emitter) fixLabel() string {
	e.polls++
	return fmt.Sprintf(".L%s_fx%d", e.labelBase(), e.polls)
}

// emitFixed writes the fixed-width ops, and the u64 and f32 forms of
// printing, reporting false for anything else.
func (e *Emitter) emitFixed(in Instr) bool {
	switch in.Op {
	case OpExt:
		if e.consts != nil && e.selExt(in) {
			return true
		}
		e.line("mov rax, %s", e.loc(in.A))
		switch in.Imm {
		case memI8:
			e.line("movsx rax, al")
		case memU8:
			e.line("movzx eax, al")
		case memI16:
			e.line("movsx rax, ax")
		case memU16:
			e.line("movzx eax, ax")
		case memI32:
			e.line("movsxd rax, eax")
		case memU32:
			// Writing a 32-bit register clears the upper half.
			e.line("mov eax, eax")
		}
		e.put(in.Dst, "rax")

	case OpF32Round:
		e.line("movsd xmm0, %s", e.loc(in.A))
		e.line("cvtsd2ss xmm0, xmm0")
		e.line("cvtss2sd xmm0, xmm0")
		e.putf(in.Dst, "xmm0")

	case OpF32Bits:
		e.line("movsd xmm0, %s", e.loc(in.A))
		e.line("cvtsd2ss xmm0, xmm0")
		e.putf(in.Dst, "xmm0")

	case OpF32FromBits:
		e.line("movsd xmm0, %s", e.loc(in.A))
		e.line("cvtss2sd xmm0, xmm0")
		e.putf(in.Dst, "xmm0")

	case OpIntToF32:
		// Straight to single: through a double first would round twice,
		// which is wrong for integers above 2^53.
		e.line("mov rax, %s", e.loc(in.A))
		e.line("cvtsi2ss xmm0, rax")
		e.line("cvtss2sd xmm0, xmm0")
		e.putf(in.Dst, "xmm0")

	case OpU64ToFloat, OpU64ToF32:
		// cvtsi2sd reads its source signed. Below 2^63 that is the same
		// thing; above, halve the value first, keeping the lost low bit
		// ORed in so the rounding still sees it, convert, and double.
		cvt := "cvtsi2sd"
		if in.Op == OpU64ToF32 {
			cvt = "cvtsi2ss"
		}
		big, done := e.fixLabel(), e.fixLabel()
		e.line("mov rax, %s", e.loc(in.A))
		e.line("test rax, rax")
		e.line("js %s", big)
		e.line("%s xmm0, rax", cvt)
		if in.Op == OpU64ToF32 {
			e.line("cvtss2sd xmm0, xmm0")
		}
		e.line("jmp %s", done)
		e.label(big)
		e.line("mov rcx, rax")
		e.line("shr rcx, 1")
		e.line("and eax, 1")
		e.line("or rcx, rax")
		e.line("%s xmm0, rcx", cvt)
		if in.Op == OpU64ToF32 {
			e.line("cvtss2sd xmm0, xmm0")
		}
		e.line("addsd xmm0, xmm0")
		e.label(done)
		e.putf(in.Dst, "xmm0")

	case OpFloatToU64:
		// cvttsd2si reads its result signed. At 2^63 and above, take
		// 2^63 off first and put the top bit back after.
		big, done := e.fixLabel(), e.fixLabel()
		e.line("movsd xmm0, %s", e.loc(in.A))
		e.line("movsd xmm1, %s", e.loc(in.B))
		e.line("comisd xmm0, xmm1")
		e.line("jae %s", big)
		e.line("cvttsd2si rax, xmm0")
		e.line("jmp %s", done)
		e.label(big)
		e.line("subsd xmm0, xmm1")
		e.line("cvttsd2si rax, xmm0")
		e.line("mov rcx, 1")
		e.line("shl rcx, 63")
		e.line("xor rax, rcx")
		e.label(done)
		e.put(in.Dst, "rax")

	case OpDivU, OpModU:
		e.line("mov rax, %s", e.loc(in.A))
		if b := e.loc(in.B); b != "rcx" {
			e.line("mov rcx, %s", b)
		}
		e.line("xor edx, edx")
		e.line("div rcx")
		if in.Op == OpDivU {
			e.put(in.Dst, "rax")
		} else {
			e.put(in.Dst, "rdx")
		}

	case OpShrU:
		e.line("mov rax, %s", e.loc(in.A))
		if b := e.loc(in.B); b != "rcx" {
			e.line("mov rcx, %s", b)
		}
		e.line("shr rax, cl")
		e.put(in.Dst, "rax")

	case OpLtU, OpLeU, OpGtU, OpGeU:
		e.line("mov rax, %s", e.loc(in.A))
		if b := e.loc(in.B); b != "rcx" {
			e.line("mov rcx, %s", b)
		}
		e.line("xor edx, edx")
		e.line("cmp rax, rcx")
		e.line("set%s dl", condCodes[in.Op])
		e.put(in.Dst, "rdx")

	case OpPrintInt, OpWriteInt, OpIntToStr:
		if in.Imm != 1 {
			return false
		}
		switch in.Op {
		case OpPrintInt:
			e.line("lea rcx, __fmt_uint[rip]")
			e.line("mov rdx, %s", e.loc(in.A))
			e.line("call printf")
		case OpWriteInt:
			e.line("lea rcx, __fmt_uint_raw[rip]")
			e.line("mov rdx, %s", e.loc(in.A))
			e.line("call printf")
		default:
			e.line("mov rcx, %s", e.loc(in.A))
			e.line("call __vy_uinttostr")
			e.put(in.Dst, "rax")
		}

	case OpPrintFloat, OpWriteFloat, OpFloatToStr:
		if in.Imm != 1 {
			return false
		}
		e.line("movsd xmm0, %s", e.loc(in.A))
		e.line("call __vy_f32tostr")
		switch in.Op {
		case OpPrintFloat:
			e.line("lea rcx, __fmt_str[rip]")
			e.line("mov rdx, rax")
			e.line("call printf")
		case OpWriteFloat:
			e.line("lea rcx, __fmt_str_raw[rip]")
			e.line("mov rdx, rax")
			e.line("call printf")
		default:
			e.put(in.Dst, "rax")
		}

	default:
		return false
	}
	return true
}

// ---- folding ----

// extend is what OpExt does to a constant.
func extend(v, kind int64) int64 {
	switch kind {
	case memI8:
		return int64(int8(v))
	case memU8:
		return int64(uint8(v))
	case memI16:
		return int64(int16(v))
	case memU16:
		return int64(uint16(v))
	case memI32:
		return int64(int32(v))
	case memU32:
		return int64(uint32(v))
	}
	return v
}

// foldFixed folds the fixed-width ops on constants, the way the
// hardware would compute them. Floats fold only to values the pool can
// hold exactly as the program would have made them: not NaN, not an
// infinity, not zero (internFloat cannot tell -0 from 0).
func foldFixed(m *Module, in Instr, ic map[Reg]int64, fc map[Reg]float64) (Instr, bool) {
	a, aok := ic[in.A]
	b, bok := ic[in.B]
	toInt := func(v int64) (Instr, bool) {
		return Instr{Op: OpConst, Dst: in.Dst, A: NoReg, B: NoReg, Imm: v}, true
	}
	toFloat := func(v float64) (Instr, bool) {
		if isNaNInf(v) || v == 0 {
			return Instr{}, false
		}
		return Instr{Op: OpFConst, Dst: in.Dst, A: NoReg, B: NoReg, Imm: m.internFloat(v)}, true
	}
	bool01 := func(c bool) (Instr, bool) {
		if c {
			return toInt(1)
		}
		return toInt(0)
	}
	fa, faok := fc[in.A]
	switch in.Op {
	case OpExt:
		if aok {
			return toInt(extend(a, in.Imm))
		}
	case OpF32Round:
		if faok {
			return toFloat(float64(float32(fa)))
		}
	case OpIntToF32:
		if aok {
			return toFloat(float64(float32(a)))
		}
	case OpU64ToFloat:
		if aok {
			return toFloat(float64(uint64(a)))
		}
	case OpU64ToF32:
		if aok {
			return toFloat(float64(float32(uint64(a))))
		}
	case OpDivU:
		if aok && bok && b != 0 {
			return toInt(int64(uint64(a) / uint64(b)))
		}
	case OpModU:
		if aok && bok && b != 0 {
			return toInt(int64(uint64(a) % uint64(b)))
		}
	case OpShrU:
		if aok && bok {
			return toInt(int64(uint64(a) >> (uint64(b) & 63)))
		}
	case OpLtU:
		if aok && bok {
			return bool01(uint64(a) < uint64(b))
		}
	case OpLeU:
		if aok && bok {
			return bool01(uint64(a) <= uint64(b))
		}
	case OpGtU:
		if aok && bok {
			return bool01(uint64(a) > uint64(b))
		}
	case OpGeU:
		if aok && bok {
			return bool01(uint64(a) >= uint64(b))
		}
	}
	return Instr{}, false
}

// subRegs are the narrow names of the registers a value can live in:
// 8, 16 and 32 bits.
var subRegs = map[string][3]string{
	"rax": {"al", "ax", "eax"}, "rcx": {"cl", "cx", "ecx"}, "rdx": {"dl", "dx", "edx"},
	"rbx": {"bl", "bx", "ebx"}, "rsi": {"sil", "si", "esi"}, "rdi": {"dil", "di", "edi"},
	"r8": {"r8b", "r8w", "r8d"}, "r9": {"r9b", "r9w", "r9d"}, "r10": {"r10b", "r10w", "r10d"},
	"r11": {"r11b", "r11w", "r11d"}, "r12": {"r12b", "r12w", "r12d"}, "r13": {"r13b", "r13w", "r13d"},
	"r14": {"r14b", "r14w", "r14d"}, "r15": {"r15b", "r15w", "r15d"},
}

// narrowOperand is a register or a memory operand read at a width:
// 0 for a byte, 1 for a word, 2 for a dword.
func narrowOperand(loc string, w int) (string, bool) {
	if isRegLoc(loc) {
		names, ok := subRegs[loc]
		return names[w], ok
	}
	if !strings.HasPrefix(loc, "qword ptr ") {
		return "", false
	}
	return [3]string{"byte", "word", "dword"}[w] + " ptr " + strings.TrimPrefix(loc, "qword ptr "), true
}

// selExt is OpExt in one instruction when the result lives in a
// register: the extending move straight from wherever A is.
func (e *Emitter) selExt(in Instr) bool {
	d := e.loc(in.Dst)
	if !isRegLoc(d) || subRegs[d][0] == "" {
		return false
	}
	w := map[int64]int{memI8: 0, memU8: 0, memI16: 1, memU16: 1, memI32: 2, memU32: 2}[in.Imm]
	src, ok := narrowOperand(e.loc(in.A), w)
	if !ok {
		return false
	}
	switch in.Imm {
	case memI8, memI16:
		e.line("movsx %s, %s", d, src)
	case memU8, memU16:
		e.line("movzx %s, %s", subRegs[d][2], src)
	case memI32:
		e.line("movsxd %s, %s", d, src)
	case memU32:
		// Writing a 32-bit register clears the upper half.
		e.line("mov %s, %s", subRegs[d][2], src)
	}
	return true
}

// fitsWithout reports whether every value of from is already a value
// of to as the machine holds it, so a conversion between them has
// nothing to do: u8 into u32 or i16, i8 into i32, anything into u64.
func fitsWithout(from, to string) bool {
	if to == "u64" {
		return true // no extension to do; the bits carry over, as in C
	}
	fb, tb := fixedBits(from), fixedBits(to)
	if fb == 0 || tb == 0 || from == "u64" {
		return false
	}
	if from[0] == 'u' {
		return fb < tb || fb == tb && to[0] == 'u'
	}
	return to[0] == 'i' && fb <= tb
}

func fixedBits(name string) int {
	switch name {
	case "i8", "u8":
		return 8
	case "i16", "u16":
		return 16
	case "i32", "u32":
		return 32
	case "u64":
		return 64
	}
	return 0
}
