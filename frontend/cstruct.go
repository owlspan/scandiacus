package frontend

// C memory by hand: new and delete.
//
// An extern struct already describes bytes laid out the way C lays them
// out, and a pointer already reads and writes through them. What was
// missing is somewhere for the bytes to come from other than mem.alloc
// and a size worked out by hand. `new T` is that: zeroed memory for one
// T from the C heap, as a *T. `new [n]T` is n of them in a row, and
// `new T{x: 1}` fills fields in. delete(p) gives it back.
//
// The memory is the C runtime's, not the collector's: it is never
// collected, never moved, and native code may keep it or free it. That
// is the difference from a literal, T{...}, whose bytes the collector
// owns and frees once nothing refers to them.

import "strings"

func (c *Checker) newExpr(x *NewExpr) *Type {
	if x.Count != nil {
		if t := c.expr(x.Count); !t.IsUnknown() && !t.IsInteger() {
			c.ErrorAt(x.Count, "the count in new [n]T must be an integer, got %s", t)
		}
	}
	var elem, ptr *Type
	name := strings.TrimSpace(x.Type)
	switch {
	case name == "int":
		elem, ptr = Int, PtrToC("i64")
	case name == "float":
		elem, ptr = Float, PtrToC("f64")
	case cTypes[name].size > 0:
		elem, ptr = cTypes[name].t, PtrToC(name)
	default:
		t := c.resolveAnnotation(name, x)
		switch {
		case t == nil || t.IsUnknown():
			return Unknown
		case t.IsPtr():
			elem, ptr = t, PtrOf(t)
		case c.isExternStruct(t):
			elem, ptr = t, PtrOf(t)
		case t.Kind == KStruct:
			c.ErrorAt(x, "%s is a Veyl struct, which the collector owns - make one with %s{...}, "+
				"or declare it extern struct to give it a C layout new can allocate", t, t)
			return Unknown
		default:
			c.ErrorAt(x, "new allocates C memory for a C scalar (i8 to u64, f32, f64, bool, ptr), "+
				"an extern struct or a pointer - not %s", t)
			return Unknown
		}
	}
	if x.Lit != nil {
		if !c.isExternStruct(elem) {
			c.ErrorAt(x, "new %s{...} needs an extern struct", name)
		} else {
			c.structLit(x.Lit)
		}
	}
	x.T, x.Elem = ptr, elem
	return ptr
}
