package main

// Inline elements and code that can move the block they live in.
//
// An element of an inline list is addressed in place: the loop variable
// of `for p in ps` and the self of `ps[i].scale(2.0)` point into the
// list's element block. A push that outgrows the block copies it to a
// new one, and a write through the old address is then lost. So where
// the code between taking the address and the last use of it may grow
// a list, the address is not held:
//
//   - the loop variable is looked up again as ps[i] at each mention, so
//     it follows the element to the new block;
//   - a method gets a copy of the element as self, and the copy is
//     written back to ps[i] when the method returns.
//
// "May grow a list" is decided from the source, conservatively: a push
// or insert anywhere, a call through a function value, a function
// literal handed to the library, or a call to a function or method of
// the program that may itself do one of those.

import "reflect"

// growsLists reports whether running node can grow any list.
func (l *lowerer) growsLists(node any) bool {
	found := false
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found || !v.IsValid() {
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
		if c, ok := v.Interface().(*Call); ok && l.callGrows(c) {
			found = true
			return
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
	walk(reflect.ValueOf(node))
	return found
}

// callGrows is growsLists for one call, not counting its arguments.
func (l *lowerer) callGrows(c *Call) bool {
	if c.ViaValue {
		return true
	}
	name, dotted := DottedName(c.Callee)
	if !dotted {
		return true
	}
	switch name {
	case "push", "insert":
		return true
	}
	if fd, user := l.fnDecls[name]; user {
		return l.fnGrows(name, fd)
	}
	if fld, isField := c.Callee.(*Field); isField {
		if _, lib := (asmLibrary{}).Signature(name); lib {
			return false
		}
		// A method: the receiver's type is not known here, so any
		// method of that name counts.
		for key, fd := range l.fnDecls {
			if fd.Recv != "" && key == methodName(fd.Recv, fld.Name) && l.fnGrows(key, fd) {
				return true
			}
		}
		return false
	}
	// The library calls back into the program only through function
	// values, which a function literal or a named function passed to
	// it can be; those are counted where they are called (ViaValue) or,
	// for a named function passed as a value, here.
	for _, a := range c.Args {
		if id, ok := a.(*Ident); ok {
			if fd, user := l.fnDecls[id.Name]; user && l.fnGrows(id.Name, fd) {
				return true
			}
		}
		if fl, ok := a.(*FuncLit); ok && fl.Decl != nil && l.growsLists(fl.Decl.Body) {
			return true
		}
	}
	return false
}

// fnGrows is growsLists for a function's body, worked out once. A
// function still being looked at when it is reached again counts as
// growing, which is the safe answer for recursion.
func (l *lowerer) fnGrows(name string, fd *FnDecl) bool {
	if l.grows == nil {
		l.grows = map[string]bool{}
	}
	if g, ok := l.grows[name]; ok {
		return g
	}
	l.grows[name] = true
	g := fd.Body == nil || l.growsLists(fd.Body)
	l.grows[name] = g
	return g
}

// rebindsName reports whether body assigns to name or mentions it inside
// a function literal: a loop variable looked up again as ps[i] cannot
// be either.
func rebindsName(body *Block, name string) bool {
	found := false
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value, inLit bool)
	walk = func(v reflect.Value, inLit bool) {
		if found || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem(), inLit)
			}
			return
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), inLit)
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
			if id, ok := n.Target.(*Ident); ok && id.Name == name {
				found = true
				return
			}
		case *FuncLit:
			inLit = true
		case *Ident:
			if inLit && n.Name == name {
				found = true
				return
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
			walk(s.Field(i), inLit)
		}
	}
	walk(reflect.ValueOf(body), false)
	return found
}

// elemRef is a loop variable that names ps[i] rather than holding it.
type elemRef struct {
	list, idx int64 // slots of the list and the index
	t         vty   // the list's type
}

// loadElemRef lowers a mention of such a loop variable.
func (l *lowerer) loadElemRef(r elemRef) Reg {
	return l.listGet(l.load(r.list, r.t), l.load(r.idx, vInt), r.t.elemType())
}

// movesElement reports whether a method call on ix needs self as a copy:
// ix is an element of an inline list, and the method or the call's
// arguments may grow a list.
func (l *lowerer) movesElement(ix *Index, c *Call, method string) bool {
	t, ok := l.staticTypeOf(ix.X)
	if !ok || t.k != kList || inlineStrideOf(t.elemType()) == 0 {
		return false
	}
	if fd, ok := l.fnDecls[method]; ok && l.fnGrows(method, fd) {
		return true
	}
	for _, a := range c.Args {
		if l.growsLists(a) {
			return true
		}
	}
	return false
}
