package main

import "strings"

// new and delete: C memory by hand. See frontend/cstruct.go for the
// rules.
//
// new is calloc, so the memory is zeroed and belongs to the same C
// runtime native code reached through msvcrt uses: either side can free
// it. The collector never sees it. A pointer into it is a word like any
// other, and the collector only follows words that point at its own
// objects, so holding one in a list or a struct is fine.

// ptrStruct is the extern struct a pointer type points at, or "".
func (l *lowerer) ptrStruct(t vty) string {
	if !isPtr(t) {
		return ""
	}
	rest := strings.TrimPrefix(t.name, "*")
	if lay, ok := l.structs[rest]; ok && lay.view {
		return rest
	}
	return ""
}

func (l *lowerer) newExpr(x *NewExpr) Reg {
	pt, ok := vtyOf(x.T)
	if !ok || !isPtr(pt) {
		l.errorAt(x, "cannot allocate %s", x.Type)
		return l.junk()
	}
	_, size, ok := l.pointee(x, pt)
	if !ok {
		return l.junk()
	}
	count := l.constant(1)
	if x.Count != nil {
		count = l.expr(x.Count)
	}
	p := l.ccall("calloc", []Reg{count, l.constant(size)}, []vty{vInt, vInt}, vInt, false, false)

	// Out of memory stops the program, as a C++ new that throws with
	// nobody to catch it would.
	ok2 := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpNe, p, l.constant(0)), Dst: NoReg, Imm: ok2})
	l.mod.needs("must")
	l.emit(Instr{Op: OpMustFail, A: l.strLit("new " + x.Type + ": out of memory"), Dst: NoReg,
		B: NoReg, Imm: l.where()})
	l.mark(ok2)

	d := l.newReg()
	l.regTy[d] = pt
	l.emit(Instr{Op: OpAdd, Dst: d, A: p, B: l.constant(0)})
	if x.Lit != nil {
		lay, ok := l.structs[x.Lit.Name]
		if !ok || !lay.view {
			l.errorAt(x, "new %s{...} needs an extern struct", x.Lit.Name)
			return d
		}
		for i, name := range x.Lit.Fields {
			if i >= len(x.Lit.Vals) {
				break
			}
			f, ok := lay.field(name)
			if !ok {
				l.errorAt(x, "%s has no field %q", lay.name, name)
				continue
			}
			l.viewWrite(x, d, f, l.rvalueAs(x.Lit.Vals[i], f.t))
		}
	}
	return d
}

// freePointer is delete(p) on memory from new: free, which does
// nothing for nil.
func (l *lowerer) freePointer(p Reg) {
	l.ccall("free", []Reg{p}, []vty{vInt}, vVoid, false, false)
}
