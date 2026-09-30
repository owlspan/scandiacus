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

// ---- &local ----
//
// A variable whose address is taken (see frontend/addr.go) is kept in
// its slot in C form, at its own width: an i32 as four bytes, an f32 as
// a single, a bool as one byte. Every read and write goes through the
// slot's address at that width, so what native code writes through the
// pointer is exactly what the next read sees, with no stale upper half
// and no double where a float was expected. The slot is marked by
// OpSlotAddr, which already keeps the optimiser from caching it,
// promoting it into a register or sharing it with another variable.

// memKind is the width a variable of type t is kept at in memory.
func memKind(t vty) int64 {
	if k, ok := fixedExt[fixedOf(t)]; ok {
		return k
	}
	switch {
	case isF32(t):
		return memF32
	case t.k == kFloat:
		return memF64
	case t.k == kBool:
		return memU8
	}
	return memI64
}

// memSlot reports whether a slot of the current function is kept in
// memory, and at what width.
func (l *lowerer) memSlot(slot int64) (int64, bool) {
	k, ok := l.memSlots[l.fn][slot]
	return k, ok
}

// keepInMemory marks a just-declared slot as addressed.
func (l *lowerer) keepInMemory(n Node, name string, slot int64) {
	if l.boxed[slot] {
		l.errorAt(n, "%s is used by a closure, so its address cannot be taken", name)
		return
	}
	if l.memSlots == nil {
		l.memSlots = map[*Func]map[int64]int64{}
	}
	if l.memSlots[l.fn] == nil {
		l.memSlots[l.fn] = map[int64]int64{}
	}
	l.memSlots[l.fn][slot] = memKind(l.slotTy[slot])
}

// addrOfLocal lowers &x.
func (l *lowerer) addrOfLocal(x *Unary, id *Ident, want vty) Reg {
	slot, ok := l.lookup(id.Name)
	if !ok {
		l.errorAt(x, "cannot take the address of %s", id.Name)
		return l.junk()
	}
	if l.isView(l.slotTy[slot]) {
		// An extern struct variable holds the struct's address already.
		return l.retype(l.loadLocal(slot), want)
	}
	if _, kept := l.memSlot(slot); !kept {
		l.errorAt(x, "cannot take the address of %s here - a loop variable changes every "+
			"iteration; copy it into a let first", id.Name)
		return l.junk()
	}
	return l.retype(l.slotAddr(slot), want)
}
