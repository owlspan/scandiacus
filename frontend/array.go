package frontend

// Fixed-size arrays: `[N]T`, N values of a C scalar in a row.
//
//	let m: [16]f32               // sixteen zeros
//	let k: [4]i32 = [1, 2, 3]    // the rest are zero
//	m[5] = 1.5
//	print(len(m), m[5])
//	glUniformMatrix4fv(loc, 1, false, m)   // passed as a *f32
//
// An array is a value, like a struct: `let b = a` copies all N
// elements, and so does passing one to a function or returning it. A
// local array lives in its function's frame and a struct field array
// inside the struct, so neither costs an allocation. Where native code
// or mem.* wants an address, an array is the address of its first
// element, as in C.
//
// T is a C scalar: i8 to u64, f32, f64, bool or ptr, and int and float
// as i64 and f64. An array of structs, of arrays or of anything holding
// a pointer is not one yet. An array can itself be the element of a list
// or a map, or sit inside a nullable or a result; the backend keeps its
// bytes inline in a list and in a block of their own elsewhere.

import (
	"strconv"
	"strings"
)

// cName is the C scalar a type name stands for in an array or behind a
// pointer, where int and float mean their 64-bit C forms.
func cName(name string) string {
	switch name {
	case "int":
		return "i64"
	case "float":
		return "f64"
	}
	return name
}

// ArrayOf is [n]T for the C scalar named elem.
func ArrayOf(elem string, n int) *Type {
	elem = cName(elem)
	return &Type{Kind: KArray, Name: elem, Len: n, Elem: cTypes[elem].t}
}

// ArrayElemSize is the size of one element of an array type.
func ArrayElemSize(t *Type) int { return cTypes[t.Name].size }

// parseArrayType reads [N]T, or returns nil.
func parseArrayType(s string) *Type {
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return nil
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(s[1:end]), "_", ""), 0, 64)
	if err != nil || n <= 0 || n > 1<<24 {
		return nil
	}
	elem := cName(strings.TrimSpace(s[end+1:]))
	if _, ok := cTypes[elem]; !ok {
		return nil
	}
	return ArrayOf(elem, int(n))
}

// arrayLit checks [a, b, c] where an array is wanted: at most N
// elements, each a T. The ones left out are zero, as in C.
func (c *Checker) arrayLit(x *ListLit, want *Type) *Type {
	if len(x.Elems) > want.Len {
		c.ErrorAt(x, "%s holds %d elements, but this has %d", want, want.Len, len(x.Elems))
		x.T = Unknown
		return Unknown
	}
	for i := range x.Elems {
		got := c.exprWant(x.Elems[i], want.Elem)
		if !c.coerce(&x.Elems[i], want.Elem, got) {
			c.ErrorAt(x.Elems[i], "%s holds %s, but element %d is %s", want, want.Elem, i+1, got)
			x.T = Unknown
			return Unknown
		}
	}
	x.T = want
	return want
}

// arrayElemName is how an element type is written back: int and float
// rather than i64 and f64, as a program would have spelled them.
func arrayElemName(name string) string {
	switch name {
	case "i64":
		return "int"
	case "f64":
		return "float"
	}
	return name
}

// isArraySpelling reports whether a type as written starts like [N]T,
// anywhere a list or map type lets one sit.
func isArraySpelling(text string) bool {
	i := strings.Index(text, "[")
	return i >= 0 && i+1 < len(text) && text[i+1] >= '0' && text[i+1] <= '9'
}

// isArrayKeySpelling reports whether a type as written has an array
// where a map's key goes, as in {[2]int: str}. ParseType gives up on
// it, and the plainer message here says what is wrong rather than
// blaming the array's own syntax.
func isArrayKeySpelling(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		j := i + 1
		for j < len(text) && text[j] == ' ' {
			j++
		}
		if j+1 < len(text) && text[j] == '[' && text[j+1] >= '0' && text[j+1] <= '9' {
			return true
		}
	}
	return false
}
