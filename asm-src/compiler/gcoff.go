package main

// `gc off`: memory by hand.
//
//	gc off
//
//	let xs = [1, 2, 3]
//	...
//	delete(xs)
//
// A program that says `gc off` is never collected - not at statement
// boundaries, and not by mem.collect, which is an error there - and
// frees what it is done with itself, with delete, the way C++ does.
// Nothing checks that a value is not used after it is deleted, or
// deleted twice; either is a crash or worse, as in C++.
//
// Allocation does not change. Every object is still put on the object
// list, which costs one store, but with no collector nothing ever walks
// that list again, so delete can hand an object straight back to free
// without finding its place in it. That is also why delete is only for
// a `gc off` program: under the collector a freed object would still be
// on the list the next collection walks.
//
// delete frees the value itself, not what it refers to: a list's header
// and its elements' block, a map's header and its two blocks, a struct's
// fields, a string's or bytes' characters. The strings inside a list,
// or a list inside a struct, are values of their own.

// deleteValue lowers delete(x).
func (l *lowerer) deleteValue(c *Call) Reg {
	if len(c.Args) != 1 {
		l.errorAt(c, "delete takes one value, got %d", len(c.Args))
		return l.void()
	}
	v := l.expr(c.Args[0])
	t := l.regTy[v]
	// A pointer is memory from new, which is the C heap's in any
	// program, collected or not; see cstruct.go.
	if isPtr(t) {
		l.freePointer(v)
		return l.void()
	}
	if !l.gcOff {
		l.errorAt(c, "delete is for a program that manages its own memory - put gc off at the top of "+
			"the file, or leave freeing to the collector - or, for memory from new, a pointer")
		return l.void()
	}
	switch {
	case t.null || t.res || t.k == kFunc || l.isView(t):
		l.errorAt(c.Args[0], "delete frees a list, map, struct, string or bytes, not %s", t)
	case t.k == kList:
		l.freeObject(l.field(v, listDataOff, vInt))
		l.freeObject(v)
	case t.k == kMap:
		l.freeObject(l.field(v, mapKeysOff, vInt))
		l.freeObject(l.field(v, mapValsOff, vInt))
		l.freeObject(l.field(v, mapIdxOff, vInt))
		l.freeObject(v)
	case t.k == kStruct, t.k == kBytes:
		l.freeObject(v)
	case t.k == kStr:
		// A string written in the program lives in the executable, not
		// on the heap, and freeing it would be freeing the program.
		l.mod.needs("rodata")
		skip := l.newLabel()
		begin, end := l.symAddr("__rodata_begin"), l.symAddr("__rodata_end")
		inImage := l.arith(OpBAnd, l.compare(OpGe, v, begin), l.compare(OpLt, v, end))
		l.emit(Instr{Op: OpJumpIf, A: inImage, Dst: NoReg, Imm: skip})
		l.freeObject(v)
		l.mark(skip)
	default:
		l.errorAt(c.Args[0], "delete frees a list, map, struct, string or bytes, not %s", t)
	}
	return l.void()
}

// freeObject gives one object back to malloc and takes it off the
// counts mem.used and mem.objects report.
func (l *lowerer) freeObject(obj Reg) {
	sym := l.helperFunc("__free_obj", []vty{vInt}, vVoid, func(a []Reg) {
		none := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, a[0], l.constant(0)), Dst: NoReg, Imm: none})
		raw := l.arith(OpSub, a[0], l.constant(objHeader))
		size := l.jsonObjectSize(l.peekWord(raw, objTagOff))
		l.rtStore(gcLiveSlot, l.arith(OpSub, l.rtLoad(gcLiveSlot), l.constant(1)))
		l.rtStore(gcBytesSlot, l.arith(OpSub, l.rtLoad(gcBytesSlot), size))
		l.ccall("free", []Reg{raw}, []vty{vInt}, vVoid, false, false)
		l.mark(none)
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
	as := l.newReg()
	l.regTy[as] = vInt
	l.emit(Instr{Op: OpAdd, Dst: as, A: obj, B: l.constant(0)})
	l.callHelper(sym, []Reg{as}, []vty{vInt}, vVoid)
}

// symAddr is the address of a label in the image.
func (l *lowerer) symAddr(sym string) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpSymAddr, Dst: d, A: NoReg, B: NoReg, Sym: sym, Comment: sym})
	return d
}

// freshStr reports whether an expression's string is one this program
// just made and nothing else holds: the result of joining two strings,
// an interpolation of more than one piece, or str() of an int. Under gc
// off such a string, used as one side of a join, is freed as soon as
// the join is done - it is a temporary nobody could delete, since
// nobody has a name for it.
//
// Anything else may be shared: a variable, a field, what a function
// returned, str() of a string, which is that string, or of a bool,
// which is one of two constants.
func freshStr(e Expr) bool {
	switch x := e.(type) {
	case *Binary:
		return x.Op == PLUS && x.T != nil && x.T.Kind == KStr
	case *Interp:
		pieces := 0
		for _, p := range x.Parts {
			if p.Lit != "" {
				pieces++
			}
			if p.X != nil {
				pieces++
			}
		}
		return pieces >= 2
	case *Call:
		name, ok := DottedName(x.Callee)
		return ok && name == "str" && !x.ViaValue && !x.Method && len(x.ArgT) == 1 &&
			x.ArgT[0] != nil && x.ArgT[0].Kind == KInt
	}
	return false
}

// freeTemps frees whichever sides of a join were temporaries.
func (l *lowerer) freeTemps(le Expr, a Reg, re Expr, b Reg) {
	if freshStr(le) {
		l.freeObject(a)
	}
	if freshStr(re) {
		l.freeObject(b)
	}
}
