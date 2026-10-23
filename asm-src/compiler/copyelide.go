package main

// Passing a struct without copying it.
//
// A struct is a value, so an argument that names one - a variable, a
// field, an element - is copied on the way in: a fresh object, every
// field read and written again. That is what makes a callee's p.x = 1
// invisible to the caller. But most functions that take a struct only
// read it - `fn dot(a: V, b: V) -> float { return a.x * b.x + ... }` -
// and for them the copy is an allocation per call, and per trip of
// whatever loop the call is in, that nothing can tell apart from the
// original.
//
// So a parameter whose body only reads fields of it takes the caller's
// object as it is. Only reads: every mention of the name is the base of
// a field access that is read - not assigned to at any depth, not a
// method's receiver (a method may change self), not under & - and the
// name is not captured by a function literal, declared again, or used
// on its own, which would let the object escape to somewhere that could
// change it or keep it after the caller has.

import (
	"reflect"
)

// readOnlyArgs says, for each parameter of a function the program
// declared, whether an argument for it can be passed without a copy:
// the body only reads fields of it, and writes no fields anywhere and
// calls nothing that might - so no other path to the caller's object
// can change it while the callee looks at it either. Worked out once
// per function, when a call to it is first lowered.
func (l *lowerer) readOnlyArgs(name string) []bool {
	if ro, ok := l.readOnly[name]; ok {
		return ro
	}
	if l.readOnly == nil {
		l.readOnly = map[string][]bool{}
	}
	fd := l.fnDecls[name]
	var ro []bool
	if fd != nil && fd.Body != nil && l.writesNothing(fd.Body) {
		ro = make([]bool, len(fd.Params))
		for i, p := range fd.Params {
			if p.Name != "self" {
				ro[i] = onlyFieldReads(fd.Body, p.Name)
			}
		}
	}
	l.readOnly[name] = ro
	return ro
}

// writesNothing reports whether a body assigns to nothing but plain
// names, and calls only the library: a function of the program, a
// method or a function value could change any struct.
func (l *lowerer) writesNothing(body *Block) bool {
	ok := true
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !ok || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
			return
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
			return
		case reflect.Ptr:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
		case reflect.Struct:
		default:
			return
		}
		switch n := v.Interface().(type) {
		case *AssignStmt:
			if _, plain := n.Target.(*Ident); !plain {
				ok = false
				return
			}
		case *FuncLit:
			ok = false
			return
		case *Call:
			if n.ViaValue {
				ok = false
				return
			}
			name, dotted := DottedName(n.Callee)
			if !dotted {
				ok = false
				return
			}
			if _, user := l.sigs[name]; user {
				ok = false
				return
			}
			if _, isField := n.Callee.(*Field); isField {
				if _, lib := (asmLibrary{}).Signature(name); !lib {
					ok = false // a method
					return
				}
			}
		}
		s := v
		if s.Kind() == reflect.Ptr {
			s = s.Elem()
		}
		if s.Kind() != reflect.Struct {
			return
		}
		t := s.Type()
		for i := 0; i < s.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() || f.Type == reflect.TypeOf((*Type)(nil)) ||
				f.Type == reflect.TypeOf([]*Type(nil)) {
				continue
			}
			walk(s.Field(i))
		}
	}
	walk(reflect.ValueOf(body))
	return ok
}

// Where a name is met: read as a value, or in one of the places that
// can change or keep what it names.
const (
	roleRead   = iota
	roleBase   // the X of a field access that is read
	roleTarget // inside an assignment target
	roleCallee // inside a method call's callee
	roleAddr   // under &
)

func onlyFieldReads(body *Block, name string) bool {
	ok := true
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value, role int, inLit bool)
	walk = func(v reflect.Value, role int, inLit bool) {
		if !ok || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem(), role, inLit)
			}
			return
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), role, inLit)
			}
			return
		case reflect.Ptr:
			if v.IsNil() {
				return
			}
			if seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
		default:
			if v.Kind() != reflect.Struct {
				return
			}
		}

		switch n := v.Interface().(type) {
		case *Ident:
			if n.Name == name && (inLit || role != roleBase) {
				ok = false
			}
			return
		case *Field:
			// The base of a read is fine; anything else this access is
			// part of passes its role down to the base.
			r := roleBase
			if role != roleRead && role != roleBase {
				r = role
			}
			walk(reflect.ValueOf(n.X), r, inLit)
			walk(reflect.ValueOf(n.Lit), roleRead, inLit)
			return
		case *AssignStmt:
			walk(reflect.ValueOf(n.Target), roleTarget, inLit)
			walk(reflect.ValueOf(n.Value), roleRead, inLit)
			return
		case *Call:
			if _, isField := n.Callee.(*Field); isField {
				walk(reflect.ValueOf(n.Callee), roleCallee, inLit)
			} else {
				walk(reflect.ValueOf(n.Callee), roleRead, inLit)
			}
			for _, a := range n.Args {
				walk(reflect.ValueOf(a), roleRead, inLit)
			}
			if n.Lit != nil {
				walk(reflect.ValueOf(n.Lit), roleRead, inLit)
			}
			return
		case *Unary:
			r := roleRead
			if n.Op == AMP {
				r = roleAddr
			}
			walk(reflect.ValueOf(n.X), r, inLit)
			return
		case *FuncLit:
			walk(reflect.ValueOf(n.Decl), roleRead, true)
			return
		case *LetStmt:
			if n.Name == name {
				ok = false
				return
			}
		case *ForStmt:
			if n.Var == name || n.Var2 == name {
				ok = false
				return
			}
		case *MatchStmt:
			if n.Temp == name {
				ok = false
				return
			}
		}

		// Any other node: its children, read.
		s := v
		if s.Kind() == reflect.Ptr {
			s = s.Elem()
		}
		if s.Kind() != reflect.Struct {
			return
		}
		t := s.Type()
		for i := 0; i < s.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() || f.Type == reflect.TypeOf((*Type)(nil)) ||
				f.Type == reflect.TypeOf([]*Type(nil)) {
				continue
			}
			walk(s.Field(i), roleRead, inLit)
		}
	}
	walk(reflect.ValueOf(body), roleRead, false)
	return ok
}
