package frontend

// Fixed-width numbers: i8, u8, i16, u16, i32, u32, u64 and f32.
//
// int and float are the language's own numbers, 64 bits each. These are
// the widths C code and file formats use, as real variable types: an
// i32 wraps at 32 bits, a u8 never goes negative, a u64 compares and
// divides unsigned, an f32 rounds to single precision after every
// operation.
//
// A value of one is still a single machine word. The integer ones are
// kept sign- or zero-extended from their width, so everything that moves
// a word around (a slot, a register, a list, an extern argument) needs
// nothing new; only arithmetic has to put a result back into range.
//
// They are a kind of their own rather than a flavour of int, so every
// check written for int keeps rejecting them until it is taught
// otherwise. There are no implicit conversions: i32(x), u8(x), int(x)
// and float(x) convert, wrapping like a C cast. An untyped literal fits
// any of them when its value is in range.
//
// i64 and f64 are other names for int and float.

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// fixedTypes holds one Type per name; nothing ever mutates a Type. A
// literal rather than a loop in init, because cTypes is built from it
// during package initialisation, before any init runs.
var fixedTypes = map[string]*Type{
	"i8": {Kind: KFixed, Name: "i8"}, "u8": {Kind: KFixed, Name: "u8"},
	"i16": {Kind: KFixed, Name: "i16"}, "u16": {Kind: KFixed, Name: "u16"},
	"i32": {Kind: KFixed, Name: "i32"}, "u32": {Kind: KFixed, Name: "u32"},
	"u64": {Kind: KFixed, Name: "u64"}, "f32": {Kind: KFixed, Name: "f32"},
}

// FixedOf is the fixed-width type with this name, or nil.
func FixedOf(name string) *Type { return fixedTypes[name] }

// FixedNames are the conversion functions, one per type.
var FixedNames = []string{"i8", "u8", "i16", "u16", "i32", "u32", "u64", "f32"}

func (t *Type) IsFixed() bool { return t != nil && t.Kind == KFixed }

// IsFixedInt is a fixed-width integer, anything but f32.
func (t *Type) IsFixedInt() bool { return t.IsFixed() && t.Name != "f32" }

// IsInteger is int or a fixed-width integer: what an index, a shift
// count or a bitwise operator takes.
func (t *Type) IsInteger() bool { return t != nil && (t.Kind == KInt || t.IsFixedInt()) }

// IsFloatLike is float or f32.
func (t *Type) IsFloatLike() bool {
	return t != nil && (t.Kind == KFloat || t.IsFixed() && t.Name == "f32")
}

// Bits is the width of a fixed-width type, 64 for int and float.
func (t *Type) Bits() int {
	if !t.IsFixed() {
		return 64
	}
	n, _ := strconv.Atoi(t.Name[1:])
	return n
}

// Unsigned reports a u type.
func (t *Type) Unsigned() bool { return t.IsFixed() && t.Name[0] == 'u' }

// fixedRange is the smallest and largest value a fixed-width integer
// holds.
func fixedRange(t *Type) (lo, hi *big.Int) {
	bits := uint(t.Bits())
	one := big.NewInt(1)
	if t.Unsigned() {
		hi = new(big.Int).Sub(new(big.Int).Lsh(one, bits), one)
		return big.NewInt(0), hi
	}
	hi = new(big.Int).Sub(new(big.Int).Lsh(one, bits-1), one)
	lo = new(big.Int).Neg(new(big.Int).Lsh(one, bits-1))
	return lo, hi
}

// ConstInt evaluates an expression made only of integer literals, in
// exact arithmetic. false means it is not one, or divides by zero.
func ConstInt(e Expr) (*big.Int, bool) {
	switch x := e.(type) {
	case *IntLit:
		v, ok := new(big.Int).SetString(strings.ReplaceAll(x.Val, "_", ""), 0)
		return v, ok
	case *Unary:
		v, ok := ConstInt(x.X)
		if !ok {
			return nil, false
		}
		switch x.Op {
		case MINUS:
			return new(big.Int).Neg(v), true
		case TILDE:
			return new(big.Int).Not(v), true
		}
		return nil, false
	case *Binary:
		a, ok := ConstInt(x.L)
		if !ok {
			return nil, false
		}
		b, ok := ConstInt(x.R)
		if !ok {
			return nil, false
		}
		switch x.Op {
		case PLUS:
			return new(big.Int).Add(a, b), true
		case MINUS:
			return new(big.Int).Sub(a, b), true
		case STAR:
			return new(big.Int).Mul(a, b), true
		case SLASH, PERCENT:
			if b.Sign() == 0 {
				return nil, false
			}
			if x.Op == SLASH {
				return new(big.Int).Quo(a, b), true
			}
			return new(big.Int).Rem(a, b), true
		case AMP:
			return new(big.Int).And(a, b), true
		case PIPE:
			return new(big.Int).Or(a, b), true
		case CARET:
			return new(big.Int).Xor(a, b), true
		case SHL, SHR:
			if b.Sign() < 0 || b.Cmp(big.NewInt(64)) > 0 {
				return nil, false
			}
			if x.Op == SHL {
				return new(big.Int).Lsh(a, uint(b.Int64())), true
			}
			return new(big.Int).Rsh(a, uint(b.Int64())), true
		}
	}
	return nil, false
}

// IsUntypedFloat reports an expression made of number literals with at
// least one float among them, which fits an f32 the way an untyped int
// fits a float.
func IsUntypedFloat(e Expr) bool {
	_, ok := constFloat(e)
	return ok && !IsUntypedInt(e)
}

func constFloat(e Expr) (float64, bool) {
	switch x := e.(type) {
	case *IntLit:
		v, ok := ConstInt(x)
		if !ok {
			return 0, false
		}
		f, _ := new(big.Float).SetInt(v).Float64()
		return f, true
	case *FloatLit:
		v, err := strconv.ParseFloat(strings.ReplaceAll(x.Val, "_", ""), 64)
		return v, err == nil
	case *Unary:
		if x.Op != MINUS {
			return 0, false
		}
		v, ok := constFloat(x.X)
		return -v, ok
	case *Binary:
		a, ok := constFloat(x.L)
		if !ok {
			return 0, false
		}
		b, ok := constFloat(x.R)
		if !ok {
			return 0, false
		}
		switch x.Op {
		case PLUS:
			return a + b, true
		case MINUS:
			return a - b, true
		case STAR:
			return a * b, true
		case SLASH:
			if IsUntypedInt(x.L) && IsUntypedInt(x.R) {
				return 0, false // integer division, not this
			}
			return a / b, true
		}
	}
	return 0, false
}

// fitLiteral makes an untyped constant expression into a value of the
// fixed-width type want, replacing it in place: an integer by its value
// as the machine holds it, sign- or zero-extended, a float by the
// nearest f32. It reports false, having said why, when the value does
// not fit, and false without a word when the expression is not a
// constant at all.
func (c *Checker) fitLiteral(slot *Expr, want *Type) bool {
	e := *slot
	line, col := e.Pos()
	span := Span{Line: line, Col: col}
	if want.Name == "f32" {
		v, ok := constFloat(e)
		if !ok {
			return false
		}
		if math.Abs(v) > math.MaxFloat32 && !math.IsInf(v, 0) {
			c.ErrorAt(e, "%s is too large for an f32", exprText(e))
			return false
		}
		r := float64(float32(v))
		*slot = &Convert{Span: span, X: &FloatLit{Span: span,
			Val: strconv.FormatFloat(r, 'g', -1, 64)}, T: want}
		return true
	}
	v, ok := ConstInt(e)
	if !ok {
		c.ErrorAt(e, "this constant divides by zero or shifts out of range")
		return false
	}
	lo, hi := fixedRange(want)
	if v.Cmp(lo) < 0 || v.Cmp(hi) > 0 {
		c.ErrorAt(e, "%s does not fit in %s, which holds %s to %s - use %s(...) to wrap it",
			v, want, lo, hi, want)
		return false
	}
	// A u64 above the int range is held as the same 64 bits read signed.
	if v.Sign() > 0 && v.BitLen() == 64 {
		v = new(big.Int).Sub(v, new(big.Int).Lsh(big.NewInt(1), 64))
	}
	*slot = &Convert{Span: span, X: &IntLit{Span: span, Val: v.String()}, T: want}
	return true
}

// isUntypedConst reports an expression fitLiteral can turn into a value
// of this fixed-width type.
func isUntypedConst(e Expr, want *Type) bool {
	if want.Name == "f32" {
		return IsUntypedInt(e) || IsUntypedFloat(e)
	}
	return IsUntypedInt(e) || isBitConst(e)
}

// isBitConst is an integer constant built with the bitwise operators
// too, as in (255 << 16) | 7, which a u32 pixel wants.
func isBitConst(e Expr) bool {
	switch x := e.(type) {
	case *IntLit:
		return true
	case *Unary:
		return (x.Op == MINUS || x.Op == TILDE) && isBitConst(x.X)
	case *Binary:
		switch x.Op {
		case PLUS, MINUS, STAR, SLASH, PERCENT, AMP, PIPE, CARET, SHL, SHR:
			return isBitConst(x.L) && isBitConst(x.R)
		}
	}
	return false
}

// fixedBinary types an operator with a fixed-width number on at least
// one side. Both sides must be the same type once an untyped literal on
// either has taken the other's; a shift's count may be any integer.
func (c *Checker) fixedBinary(x *Binary, lt, rt *Type) (*Type, bool) {
	if !lt.IsFixed() && !rt.IsFixed() {
		return nil, false
	}
	isShift := x.Op == SHL || x.Op == SHR
	if isShift {
		if !lt.IsFixedInt() {
			c.ErrorAt(x, "'%s' works on integers, got %s and %s", OpText(x.Op), lt, rt)
			return Unknown, true
		}
		if rt.IsInteger() {
			if IsUntypedInt(x.R) {
				if v, ok := ConstInt(x.R); ok && v.Sign() < 0 {
					c.ErrorAt(x.R, "a shift count cannot be negative")
					return Unknown, true
				}
			}
			return lt, true
		}
		c.ErrorAt(x, "a shift count must be an integer, got %s", rt)
		return Unknown, true
	}
	switch {
	case lt.IsFixed() && !rt.IsFixed() && isUntypedConst(x.R, lt):
		if !c.fitLiteral(&x.R, lt) {
			return Unknown, true
		}
		rt = lt
	case rt.IsFixed() && !lt.IsFixed() && isUntypedConst(x.L, rt):
		if !c.fitLiteral(&x.L, rt) {
			return Unknown, true
		}
		lt = rt
	}
	if !lt.Equal(rt) {
		if hint := bitwiseHint(x, lt, rt); hint != "" && (x.Op == AMP || x.Op == PIPE || x.Op == CARET) {
			c.ErrorAt(x, "'%s' works on integers, got %s and %s%s", OpText(x.Op), lt, rt, hint)
			return Unknown, true
		}
		c.ErrorAt(x, "cannot mix %s and %s in '%s' - convert one, as in %s(...)",
			lt, rt, OpText(x.Op), lt)
		return Unknown, true
	}
	t := lt
	switch x.Op {
	case EQ, NEQ, LT, LTE, GT, GTE:
		return Bool, true
	case PLUS, MINUS, STAR, SLASH:
		return t, true
	case PERCENT, AMP, PIPE, CARET:
		if t.Name == "f32" {
			c.ErrorAt(x, "'%s' works on integers, got f32", OpText(x.Op))
			return Unknown, true
		}
		return t, true
	}
	c.ErrorAt(x, "'%s' does not work on %s", OpText(x.Op), t)
	return Unknown, true
}

// convertible reports whether a value of this type can be converted to
// a number with i32(...) and the rest.
func convertible(t *Type) bool { return t.IsNumeric() || t.IsFixed() }

// CheckConversion types a conversion call, int(x), i32(x) and so on.
func (c *Checker) CheckConversion(x *Call, to *Type, args []*Type) *Type {
	if len(x.Args) != 1 {
		c.ErrorAt(x, "%s(...) takes one value, got %d", to, len(x.Args))
		return to
	}
	got := args[0]
	if got.IsUnknown() {
		return to
	}
	if !convertible(got) {
		c.ErrorAt(x, "%s(...) converts a number, got %s", to, got)
		return to
	}
	// A constant converts at compile time, wrapping as the machine would,
	// so i8(200) is -56 without a word and u64(-1) is the largest u64.
	if to.IsFixed() && isUntypedConst(x.Args[0], to) {
		if v, ok := ConstInt(x.Args[0]); ok && to.IsFixedInt() {
			x.Args[0] = &IntLit{Span: spanOf(x.Args[0]), Val: wrapConst(v, to).String()}
		}
	}
	return to
}

func spanOf(e Expr) Span {
	line, col := e.Pos()
	return Span{Line: line, Col: col}
}

// wrapConst is v cut to the width of t and read back the way t reads
// its bits, then held as a signed 64-bit word the way the machine does.
func wrapConst(v *big.Int, t *Type) *big.Int {
	bits := uint(t.Bits())
	mod := new(big.Int).Lsh(big.NewInt(1), bits)
	r := new(big.Int).Mod(v, mod) // 0 <= r < 2^bits
	if !t.Unsigned() || bits == 64 {
		half := new(big.Int).Lsh(big.NewInt(1), bits-1)
		if r.Cmp(half) >= 0 {
			r.Sub(r, mod)
		}
	}
	return r
}

// fixedCompound checks `x += y` and the rest where x is a fixed-width
// number: the same rules as the operator written out.
func (c *Checker) fixedCompound(st *AssignStmt, op Kind, want, got *Type) {
	if got.IsUnknown() {
		return
	}
	switch op {
	case SHL, SHR:
		if !want.IsFixedInt() {
			c.ErrorAt(st, "%s needs an integer, but %s is %s", AssignOpText(st.Op), describeTarget(st.Target), want)
		} else if !got.IsInteger() {
			c.ErrorAt(st, "a shift count must be an integer, got %s", got)
		}
		return
	case PERCENT, AMP, PIPE, CARET:
		if !want.IsFixedInt() {
			c.ErrorAt(st, "%s needs an integer, but %s is %s", AssignOpText(st.Op), describeTarget(st.Target), want)
			return
		}
	case PLUS, MINUS, STAR, SLASH:
	default:
		c.ErrorAt(st, "%s does not work on %s", AssignOpText(st.Op), want)
		return
	}
	if !got.IsFixed() && isUntypedConst(st.Value, want) {
		c.fitLiteral(&st.Value, want)
		return
	}
	if !got.Equal(want) {
		c.ErrorAt(st, "cannot apply %s with %s to %s, which is %s - convert it, as in %s(...)",
			AssignOpText(st.Op), got, describeTarget(st.Target), want, want)
	}
}
