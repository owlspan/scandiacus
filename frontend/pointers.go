package frontend

// Typed pointers: *i32, *Player, **u8.
//
// An extern struct value was already an address with a layout, which
// covers one structure at a time. A pointer adds what C code does with
// addresses all day: an array of them (p[i] steps by the size), a
// scalar on its own (*p reads an i32 at p), arithmetic that counts in
// elements, and the address of a field to hand to somebody else.
//
// Nothing here checks that an address is valid, the same as C. What
// the types do buy is that the width and stride come from the
// declaration instead of from a mem.readI32 or a `* 4` written at every
// use, where getting one wrong reads garbage without complaint.

// deref types `*p`.
func (c *Checker) deref(x *Unary) *Type {
	t := c.expr(x.X)
	if t.IsUnknown() {
		return Unknown
	}
	if !t.IsPtr() {
		if t.Kind == KInt {
			c.ErrorAt(x, "'*' reads through a pointer, and this is an int - make it one first, as in (addr as *i32)")
		} else {
			c.ErrorAt(x, "'*' reads through a pointer, and %s is not one", t)
		}
		return Unknown
	}
	return t.Pointee()
}

// addrOf types `&place`. Only memory has an address: a field of an
// extern struct, *p, or p[i]. A Veyl variable lives in a register or a
// stack slot the collector and the register allocator both move
// things around in, so it has no address to give out.
func (c *Checker) addrOf(x *Unary) *Type {
	switch inner := x.X.(type) {
	case *Unary:
		if inner.Op == STAR {
			// &*p is p.
			t := c.expr(inner.X)
			if t.IsUnknown() || t.IsPtr() {
				return t
			}
		}
	case *Index:
		c.index(inner)
		t := inner.T // the indexed thing, which index records
		if t.IsUnknown() || t.IsPtr() {
			return t
		}
	case *Field:
		recv := c.expr(inner.X)
		if recv.IsUnknown() {
			return Unknown
		}
		if recv.IsPtr() {
			recv = recv.Elem
		}
		if c.isExternStruct(recv) {
			f := externField(c.structs[recv.Name], inner.Name)
			if f == nil {
				c.ErrorAt(inner, "%s has no field called %q - it has: %s",
					recv.Name, inner.Name, c.fieldNames(recv.Name))
				return Unknown
			}
			return fieldPtr(f)
		}
	}
	c.ErrorAt(x, "'&' takes the address of memory: a field of an extern struct, *p or p[i] - "+
		"a Veyl variable has no fixed address")
	return Unknown
}

// fieldPtr is the pointer type &v.f has.
func fieldPtr(f *StructField) *Type {
	if f.Len > 0 || f.T.IsUnknown() {
		// An array already reads as a pointer to its first element.
		return f.T
	}
	if f.T.IsPtr() || f.T.Kind == KStruct {
		return PtrOf(f.T)
	}
	return PtrToC(f.Type)
}

// cast types `x as T`. It changes what an address is seen as, never
// the address.
func (c *Checker) cast(x *Cast) *Type {
	got := c.expr(x.X)
	want := c.resolveAnnotation(x.Type, x)
	x.T = want
	if got.IsUnknown() || want.IsUnknown() {
		return want
	}
	isAddr := func(t *Type) bool {
		return t.Kind == KInt || t.IsPtr() || c.isExternStruct(t) || t.IsFixed() && t.Name == "u64"
	}
	if !isAddr(want) || !isAddr(got) {
		c.ErrorAt(x, "'as' turns one kind of address into another - an int, a pointer or an "+
			"extern struct - and cannot make %s into %s", got, want)
	}
	return want
}

// ptrBinary types the operators that mean something on a pointer:
// + and - with an int count in elements, the difference of two
// pointers is how many elements apart they are, and pointers compare.
// It answers handled=false when neither side is a pointer.
func (c *Checker) ptrBinary(x *Binary, lt, rt *Type) (t *Type, handled bool) {
	if !lt.IsPtr() && !rt.IsPtr() {
		return nil, false
	}
	switch x.Op {
	case PLUS:
		if lt.IsPtr() && rt.IsInteger() {
			return lt, true
		}
		if lt.IsInteger() && rt.IsPtr() {
			return rt, true
		}
	case MINUS:
		if lt.IsPtr() && rt.IsInteger() {
			return lt, true
		}
		if lt.IsPtr() && lt.Equal(rt) {
			return Int, true
		}
	case EQ, NEQ, LT, LTE, GT, GTE:
		if lt.Equal(rt) || (x.Op == EQ || x.Op == NEQ) && (lt.Kind == KNilLit || rt.Kind == KNilLit) {
			return Bool, true
		}
		c.ErrorAt(x, "cannot compare %s with %s - convert one with 'as'", lt, rt)
		return Unknown, true
	}
	c.ErrorAt(x, "'%s' does not work on %s and %s - a pointer adds or subtracts an int, "+
		"subtracts another pointer, or compares", OpText(x.Op), lt, rt)
	return Unknown, true
}

// externFieldType resolves the type of an extern struct field written
// as a pointer, *T. The layout is the same whatever T is - one machine
// word - which is why a structure can point at its own kind.
func (c *Checker) externFieldType(f *StructField) *Type {
	t := ParseType(f.Type)
	if t == nil || !t.IsPtr() {
		c.ErrorAt(f, "unknown field type %q", f.Type)
		return Unknown
	}
	if bad := c.ptrTarget(t); bad != "" {
		c.ErrorAt(f, "%s", bad)
		return Unknown
	}
	return t
}

// ptrTarget explains what is wrong with the thing a pointer type points
// at, or returns "". Only memory with a C layout can be pointed at: a
// Veyl struct is an object the collector owns and may move.
func (c *Checker) ptrTarget(t *Type) string {
	for t.IsPtr() && t.Elem != nil && t.Elem.IsPtr() {
		t = t.Elem
	}
	if !t.IsPtr() || t.Elem == nil {
		return ""
	}
	d, ok := c.structs[t.Elem.Name]
	if !ok {
		return "unknown type \"" + t.Elem.Name + "\""
	}
	if !d.Extern {
		return t.Elem.Name + " is a Veyl struct, and a pointer can only point at memory with a C " +
			"layout - make it an extern struct"
	}
	return ""
}
