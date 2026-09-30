package frontend

import "strings"

// TypeKind is the category of a Veyl type.
type TypeKind int

const (
	// KUnknown is the error-suppression type. Once an expression is
	// unknown, every check downstream of it stays quiet, so a single
	// mistake produces one error instead of ten.
	KUnknown TypeKind = iota
	KVoid             // no value at all: a function with no return type
	KInt
	KFloat
	KStr
	KBool

	// KBytes is raw binary: what comes off a socket, out of a file, or
	// into a hash. Carrying it in a str works until the data is not
	// valid UTF-8, at which point the conversion silently corrupts it.
	KBytes

	KList
	KMap
	KStruct
	KFunc
	KNullable

	// KEnum is a value of a declared enum: one of a fixed set of named
	// variants, compared by identity and printed by name. Name is the
	// enum's.
	KEnum

	// KResult is `T!` - either a T, or a reason it is missing.
	KResult

	// KErrLit is the type of `fail("...")`, which fits into any T! the
	// way nil fits into any ?T.
	KErrLit

	// KPtr is a typed pointer, `*T`: an address with a known thing at
	// it. T is a C scalar (Name holds i32, f32 and so on), an extern
	// struct, or another pointer (both in Elem). Reading through one
	// loads at T's width, indexing steps by T's size, and it passes
	// anywhere an address as an int does.
	KPtr

	// KFixed is a fixed-width number: i8, u8, i16, u16, i32, u32, u64
	// or f32, named by Name. See fixed.go.
	KFixed

	// KNilLit is the type of the bare literal `nil`. It fits into any
	// nullable type and nothing else, the way an untyped integer literal
	// fits into a float.
	KNilLit

	// Signature-only kinds. These never appear as the type of a real
	// expression - they exist so a builtin can say "any number" or
	// "anything at all" in its parameter list.
	KNumeric
	KAny
)

// Type is a Veyl type. Composite types point at their element types,
// so nesting like [][]int or {str: []int} needs no special cases.
type Type struct {
	Kind TypeKind
	Elem *Type  // list element, map value, or a function's return type
	Key  *Type  // map key
	Name string // struct name

	// Params is a function type's parameter list. Elem carries its
	// return type, or Void.
	Params []*Type
}

// FuncOf builds a function type. A nil return means it produces nothing.
func FuncOf(params []*Type, ret *Type) *Type {
	if ret == nil {
		ret = Void
	}
	return &Type{Kind: KFunc, Params: params, Elem: ret}
}

func (t *Type) IsFunc() bool { return t != nil && t.Kind == KFunc }

// PtrOf is *T for an extern struct or another pointer.
func PtrOf(elem *Type) *Type { return &Type{Kind: KPtr, Elem: elem} }

// PtrToC is *T for a C scalar such as i32 or f64.
func PtrToC(name string) *Type { return &Type{Kind: KPtr, Name: name} }

func (t *Type) IsPtr() bool { return t != nil && t.Kind == KPtr }

// Pointee is what reading through a pointer gives: int, float or bool
// for a C scalar, a view for an extern struct, the inner pointer for a
// pointer to one.
func (t *Type) Pointee() *Type {
	if t.Elem != nil {
		return t.Elem
	}
	if ct, ok := cTypes[t.Name]; ok {
		return ct.t
	}
	return Unknown
}

// StructOf names a struct type. Two struct types are the same exactly
// when their names are, so this carries no field list - the declaration
// table in the checker is the single source of truth for those.
func StructOf(name string) *Type { return &Type{Kind: KStruct, Name: name} }

// EnumOf is a value of the enum with this name.
func EnumOf(name string) *Type { return &Type{Kind: KEnum, Name: name} }

// The scalar types are singletons; nothing ever mutates a Type.
var (
	Unknown = &Type{Kind: KUnknown}
	Void    = &Type{Kind: KVoid}
	NilLitT = &Type{Kind: KNilLit}
	ErrLitT = &Type{Kind: KErrLit}
	Int     = &Type{Kind: KInt}
	Float   = &Type{Kind: KFloat}
	Str     = &Type{Kind: KStr}
	Bool    = &Type{Kind: KBool}
	Bytes   = &Type{Kind: KBytes}
	Numeric = &Type{Kind: KNumeric}
	Any     = &Type{Kind: KAny}
)

func ListOf(elem *Type) *Type    { return &Type{Kind: KList, Elem: elem} }
func MapOf(key, val *Type) *Type { return &Type{Kind: KMap, Key: key, Elem: val} }

// NullableOf wraps a type so it can also hold nil. Wrapping twice is a
// no-op: ??int is just ?int, because there is only one nil.
func NullableOf(inner *Type) *Type {
	if inner == nil || inner.Kind == KNullable {
		return inner
	}
	return &Type{Kind: KNullable, Elem: inner}
}

func (t *Type) IsNullable() bool { return t != nil && t.Kind == KNullable }
func (t *Type) IsResult() bool   { return t != nil && t.Kind == KResult }

// ResultOf wraps a type so it can also carry a failure. Wrapping twice
// is a no-op - there is no useful T!!.
func ResultOf(inner *Type) *Type {
	if inner == nil || inner.Kind == KResult {
		return inner
	}
	return &Type{Kind: KResult, Elem: inner}
}

// Unwrap strips one layer of nullability, which is what narrowing
// inside an `if x != nil` produces.
func (t *Type) Unwrap() *Type {
	if t.IsNullable() {
		return t.Elem
	}
	return t
}

func (t *Type) IsUnknown() bool    { return t == nil || t.Kind == KUnknown }
func (t *Type) IsNumeric() bool    { return t != nil && (t.Kind == KInt || t.Kind == KFloat) }
func (t *Type) IsCollection() bool { return t != nil && (t.Kind == KList || t.Kind == KMap) }

// NeedsShow reports whether printing this type has to go through the
// Veyl formatting helper. Scalars are fine with Go's own %v; anything
// with structure would otherwise leak Go's notation.
func (t *Type) NeedsShow() bool {
	if t == nil {
		return false
	}
	return t.IsCollection() || t.Kind == KStruct || t.Kind == KNullable ||
		t.Kind == KBytes
}

// String prints a type in Veyl's vocabulary. The whole point of the
// checker is that users never see "float64" or "string" again.
func (t *Type) String() string {
	if t == nil {
		return "unknown"
	}
	switch t.Kind {
	case KVoid:
		return "nothing"
	case KInt:
		return "int"
	case KFloat:
		return "float"
	case KStr:
		return "str"
	case KBool:
		return "bool"
	case KBytes:
		return "bytes"
	case KList:
		return "[]" + t.Elem.String()
	case KMap:
		return "{" + t.Key.String() + ": " + t.Elem.String() + "}"
	case KStruct, KEnum, KFixed:
		return t.Name
	case KPtr:
		if t.Elem != nil {
			return "*" + t.Elem.String()
		}
		return "*" + t.Name
	case KFunc:
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			parts[i] = p.String()
		}
		sig := "fn(" + strings.Join(parts, ", ") + ")"
		if t.Elem != nil && t.Elem.Kind != KVoid {
			sig += " -> " + t.Elem.String()
		}
		return sig
	case KNullable:
		return "?" + t.Elem.String()
	case KResult:
		if t.Elem != nil && t.Elem.Kind == KVoid {
			return "void!"
		}
		return t.Elem.String() + "!"
	case KNilLit:
		return "nil"
	case KErrLit:
		return "a failure"
	case KNumeric:
		return "int or float"
	case KAny:
		return "any"
	}
	return "unknown"
}

// Equal reports whether two types are identical.
func (t *Type) Equal(u *Type) bool {
	if t == nil || u == nil {
		return false
	}
	if t.Kind != u.Kind {
		return false
	}
	switch t.Kind {
	case KList:
		return t.Elem.Equal(u.Elem)
	case KMap:
		return t.Key.Equal(u.Key) && t.Elem.Equal(u.Elem)
	case KStruct, KEnum, KFixed:
		return t.Name == u.Name
	case KPtr:
		if (t.Elem == nil) != (u.Elem == nil) {
			return false
		}
		if t.Elem != nil {
			return t.Elem.Equal(u.Elem)
		}
		return t.Name == u.Name
	case KNullable, KResult:
		return t.Elem.Equal(u.Elem)
	case KFunc:
		if len(t.Params) != len(u.Params) {
			return false
		}
		for i := range t.Params {
			if !t.Params[i].Equal(u.Params[i]) {
				return false
			}
		}
		return t.Elem.Equal(u.Elem)
	}
	return true
}

// Accepts reports whether a value of type `got` may be passed where this
// type is expected. It is deliberately almost as strict as Equal -
// Veyl has no implicit conversions. The exceptions are the two
// signature-only kinds and KUnknown, which is already an error.
func (t *Type) Accepts(got *Type) bool {
	if t == nil || got == nil {
		return true
	}
	if t.Kind == KUnknown || got.Kind == KUnknown {
		return true // an error was already reported; don't pile on
	}
	switch t.Kind {
	case KAny:
		// "Anything" still excludes a result. Passing one to print would
		// show the wrapper rather than the value, and silently treating a
		// failure as printable output defeats the point of having the
		// type at all - unwrap it first.
		return got.Kind != KVoid && got.Kind != KResult && got.Kind != KErrLit
	case KNumeric:
		return got.IsNumeric()
	case KNullable:
		// nil fits any nullable, and so does a plain value of the inner
		// type - widening a T into a ?T loses nothing. The checker
		// inserts the wrapping where this fires.
		return got.Kind == KNilLit || t.Elem.Accepts(got) || t.Equal(got)
	case KResult:
		// Same shape: a plain T is a successful T!, and a failure fits
		// any of them.
		return got.Kind == KErrLit || t.Elem.Accepts(got) || t.Equal(got)
	case KInt:
		// A pointer is an address, so it goes anywhere an address as an
		// int does: mem.*, an extern's ptr, arithmetic of your own. The
		// other way needs `as`, so an int never turns into a pointer by
		// accident.
		return got.Kind == KInt || got.Kind == KPtr
	case KPtr:
		// nil is the null pointer.
		return got.Kind == KNilLit || t.Equal(got)
	}
	// A nullable never satisfies a plain type: unwrapping is what the
	// nil check is for, and doing it implicitly would defeat the point.
	return t.Equal(got)
}

// NeedsWrap reports whether assigning `got` into this type requires
// boxing a plain value into a nullable.
func (t *Type) NeedsWrap(got *Type) bool {
	if got == nil || got.IsUnknown() {
		return false
	}
	switch {
	case t.IsNullable():
		return got.Kind != KNilLit && got.Kind != KNullable
	case t.IsResult():
		return got.Kind != KErrLit && got.Kind != KResult
	}
	return false
}

// Go returns the Go type this compiles to.
func (t *Type) Go() string {
	if t == nil {
		return "any"
	}
	switch t.Kind {
	case KInt:
		return "int"
	case KFloat:
		return "float64"
	case KStr:
		return "string"
	case KBool:
		return "bool"
	case KBytes:
		return "[]byte"
	case KList:
		return "[]" + t.Elem.Go()
	case KMap:
		return "map[" + t.Key.Go() + "]" + t.Elem.Go()
	case KStruct:
		return t.Name
	case KPtr:
		return "uintptr"
	case KFunc:
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			parts[i] = p.Go()
		}
		sig := "func(" + strings.Join(parts, ", ") + ")"
		if t.Elem != nil && t.Elem.Kind != KVoid {
			sig += " " + t.Elem.Go()
		}
		return sig
	case KNullable:
		// A pointer, uniformly. Some inner types have their own nil in
		// Go, but using one representation everywhere means narrowing
		// and wrapping have exactly one shape to handle.
		return "*" + t.Elem.Go()
	case KResult:
		// An action that can fail but produces no value: `void!`. Go
		// has no void to instantiate __Res with, so an empty struct
		// stands in. It costs nothing at runtime.
		if t.Elem != nil && t.Elem.Kind == KVoid {
			return "__Res[__Unit]"
		}
		return "__Res[" + t.Elem.Go() + "]"
	}
	return "any"
}

// Zero is the Go expression for this type's zero value. Needed wherever
// codegen must produce a value out of nothing - a missing map key, an
// out-of-range index, a declared-but-unassigned variable.
func (t *Type) Zero() string {
	if t == nil {
		return "nil"
	}
	switch t.Kind {
	case KInt:
		return "0"
	case KFloat:
		return "0.0"
	case KStr:
		return `""`
	case KBool:
		return "false"
	case KBytes:
		return "[]byte{}"
	case KList, KMap:
		return t.Go() + "{}"
	case KStruct:
		return t.Name + "{}"
	case KNullable, KFunc:
		return "nil"
	case KResult:
		return t.Go() + "{}"
	}
	return "nil"
}

// ---- parsing type annotations ----

// ParseType turns the source text of a type annotation into a Type.
// It returns nil for anything it does not recognise, leaving the caller
// to report the error with a position attached.
//
// Grammar, such as it is:
//
//	type := "int" | "float" | "str" | "bool"
//	      | "*" ctype | "*" type
//	      | "[]" type
//	      | "{" type ":" type "}"
func ParseType(s string) *Type {
	s = strings.TrimSpace(s)
	switch s {
	case "int":
		return Int
	case "float":
		return Float
	case "str":
		return Str
	case "bool":
		return Bool
	case "bytes":
		return Bytes
	case "i64":
		return Int
	case "f64":
		return Float
	case "i8", "u8", "i16", "u16", "i32", "u32", "u64", "f32":
		return FixedOf(s)
	case "ptr":
		// A raw machine word, only meaningful on an extern declaration.
		// Everywhere else it behaves exactly like an int; the two spellings
		// differ solely in how an extern call's return value is widened
		// (a C int needs sign extension from eax, a pointer does not).
		return Int
	}

	// *T. The C scalar names are only types behind a `*`; on their own
	// they would read as struct names.
	if strings.HasPrefix(s, "*") {
		rest := strings.TrimSpace(s[1:])
		if _, ok := cTypes[rest]; ok {
			return PtrToC(rest)
		}
		inner := ParseType(rest)
		if inner == nil || (inner.Kind != KStruct && inner.Kind != KPtr) {
			return nil
		}
		return PtrOf(inner)
	}

	// A function type is recognised before the `!` suffix, because the
	// `!` in `fn(str) -> int!` belongs to the return type. Peeling it
	// first would produce a result carrying a function, which is not
	// what anyone writes that to mean.
	if strings.HasPrefix(s, "fn(") {
		close := matchingParen(s, 2)
		if close < 0 {
			return nil
		}
		var params []*Type
		if inner := strings.TrimSpace(s[3:close]); inner != "" {
			for _, part := range splitTopLevel(inner, ',') {
				p := ParseType(part)
				if p == nil {
					return nil
				}
				params = append(params, p)
			}
		}
		rest := strings.TrimSpace(s[close+1:])
		if rest == "" {
			return FuncOf(params, Void)
		}
		if !strings.HasPrefix(rest, "->") {
			return nil
		}
		ret := ParseType(rest[2:])
		if ret == nil {
			return nil
		}
		return FuncOf(params, ret)
	}

	// `void!` is an action that either worked or explains why not. It
	// is only meaningful under `!` - a bare `void` is not a type
	// anybody can hold, so it is not accepted on its own.
	if s == "void!" {
		return ResultOf(Void)
	}

	// `T!` binds loosest of the rest, so it is peeled next: `?int!` is a
	// result carrying a nullable int.
	if strings.HasSuffix(s, "!") {
		inner := ParseType(s[:len(s)-1])
		if inner == nil {
			return nil
		}
		return ResultOf(inner)
	}

	if strings.HasPrefix(s, "?") {
		inner := ParseType(s[1:])
		if inner == nil {
			return nil
		}
		return NullableOf(inner)
	}

	if strings.HasPrefix(s, "[]") {
		elem := ParseType(s[2:])
		if elem == nil {
			return nil
		}
		return ListOf(elem)
	}

	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		inner := s[1 : len(s)-1]
		// Split on the colon that separates key from value, skipping any
		// colon nested inside a braced map type.
		depth := 0
		for i := 0; i < len(inner); i++ {
			switch inner[i] {
			case '{':
				depth++
			case '}':
				depth--
			case ':':
				if depth != 0 {
					continue
				}
				key := ParseType(inner[:i])
				val := ParseType(inner[i+1:])
				if key == nil || val == nil {
					return nil
				}
				// Go map keys must be comparable, and Veyl restricts
				// them further to keep error messages simple.
				if key.Kind != KStr && key.Kind != KInt {
					return nil
				}
				return MapOf(key, val)
			}
		}
	}

	// Box<int>: an instance of a generic struct. Its name is rebuilt from
	// the parsed arguments, so however it was spaced it names the same
	// struct.
	if base, args, ok := SplitGeneric(s); ok {
		canon := make([]string, len(args))
		for i, a := range args {
			t := ParseType(a)
			if t == nil {
				return nil
			}
			canon[i] = t.String()
		}
		return StructOf(GenericName(base, canon))
	}

	// Anything else that looks like a name is taken to be a struct. The
	// checker owns the question of whether that struct was declared -
	// this function has no table to consult.
	if isTypeName(s) {
		return StructOf(s)
	}
	return nil
}

// matchingParen finds the `)` closing the `(` at index open.
func matchingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTopLevel splits on sep, ignoring separators nested inside
// brackets - so fn({str: int}, []int) has two parameters, not three.
func splitTopLevel(s string, sep byte) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '<':
			depth++
		case '>':
			// The arrow of a function type is not a closing bracket.
			if i == 0 || s[i-1] != '-' {
				depth--
			}
		case sep:
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(parts, strings.TrimSpace(s[start:]))
}

// SplitGeneric takes Box<int, str> apart into Box and [int, str].
func SplitGeneric(s string) (string, []string, bool) {
	open := strings.IndexByte(s, '<')
	if open <= 0 || !strings.HasSuffix(s, ">") || !isTypeName(s[:open]) {
		return "", nil, false
	}
	inner := strings.TrimSpace(s[open+1 : len(s)-1])
	if inner == "" {
		return "", nil, false
	}
	return s[:open], splitTopLevel(inner, ','), true
}

func isTypeName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// Qual is how a declaration is keyed once packages exist: "greet.hello"
// for something imported, and the bare name for the program's own code.
// Two packages exporting the same name therefore cannot collide, which
// was the whole reason for namespacing imports.
func Qual(pkg, name string) string {
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}
