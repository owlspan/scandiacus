package main

// Nullables, `?T`.
//
// One word. Zero is nil; anything else is a pointer to a one-word heap
// box holding the value. Same shape as a result, and for the same
// reason: the layout does not depend on what is inside, so `?int` and
// `?str` differ only in how the word inside the box is read back out,
// and a nullable stays one word wherever it is held. That keeps the IR
// three-address and needs nothing of the calling convention.
//
// An array is the one exception to the box: see nullBox.
//
// A str, a list, a map and a struct are already pointers, so a
// null-pointer representation would cost nothing for them. It is not
// worth it: `?int` has no spare value to mean nil, so those would need
// boxing anyway, and two representations means every use site has to ask
// which one it is looking at.
//
// The checker does the hard half. It proves `x != nil` and marks the
// uses inside that block with Ident.Narrowed, so this file never has to
// work out where a dereference is safe - it dereferences exactly the
// idents the checker marked, and nothing else.

// nilValue is the nil of a given nullable type: the zero word.
func (l *lowerer) nilValue(t vty) Reg {
	d := l.constant(0)
	l.regTy[d] = t
	return d
}

// nullBox wraps a value so it can be held as a ?T.
//
// An array has no word to put in a one-word box, and a box holding its
// address would only add a hop, so the box is the array: a block of its
// own with a copy of the bytes. It has to be a copy, since an array is a
// value and the one passed in goes on being written to; it is the same
// block that a map value or a result's value is.
func (l *lowerer) nullBox(v Reg, t vty) Reg {
	if isArray(t.notNull()) {
		box := l.arrayStorage(t.notNull(), true)
		l.arrayCopy(box, v, t.notNull())
		l.regTy[box] = t
		return box
	}
	box := l.allocObj(l.constant(wordSize), l.nullTag(t))
	l.emit(Instr{Op: OpStoreMem, A: box, B: v, Imm: 0})
	l.regTy[box] = t
	return box
}

// nullTag says whether the boxed word is a pointer, which is what the
// object header records for a collector that does not exist yet.
func (l *lowerer) nullTag(t vty) int64 {
	if t.notNull().holdsPointer() {
		return tagPtrs
	}
	return tagWords
}

// nullValue reads the value out of a non-nil ?T.
//
// Nothing checks the pointer here. It does not have to: the only callers
// are uses the checker marked as narrowed, which means it has already
// proved this cannot be nil. An unmarked use never reaches this, because
// the checker rejects reading a ?T without proving it first.
func (l *lowerer) nullValue(box Reg, t vty) Reg {
	if isArray(t.notNull()) {
		return l.retype(box, t.notNull()) // the box is the array
	}
	d := l.newReg()
	l.regTy[d] = t.notNull()
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: box, B: NoReg, Imm: 0})
	return d
}

// isNil is `x == nil`, which is the whole of a nullable's runtime.
func (l *lowerer) isNil(v Reg) Reg {
	return l.compare(OpEq, v, l.constant(0))
}

// widenNull boxes a plain T where a ?T was wanted. The checker marks
// every such spot with a Widen, so this is the only place that boxes.
func (l *lowerer) widenNull(x *Widen, t vty) Reg {
	return l.nullWiden(l.expr(x.X), t)
}

// nullWiden is widenNull on a value already lowered, so that widen can
// put a nullable inside a result without lowering the expression twice.
func (l *lowerer) nullWiden(v Reg, t vty) Reg {
	vt := l.regTy[v]
	if vt.null && vt.k == kVoid {
		// `nil` widened to a ?T is just the zero word - there is
		// nothing to box.
		return l.nilValue(t)
	}
	if vt.null {
		// already a ?T, so boxing again would make a ??T
		return v
	}
	if t.notNull().k == kFloat && vt.k == kInt {
		v = l.toFloat(v)
	}
	return l.nullBox(v, t)
}

// writeNull renders a nullable the way print does: the value, or the
// word nil. The Go backend prints a nil as "nil" rather than Go's
// "<nil>", so this has to as well.
func (l *lowerer) writeNull(n Node, v Reg, t vty) {
	isNilLabel := l.newLabel()
	done := l.newLabel()

	l.emit(Instr{Op: OpJumpIf, A: l.isNil(v), Dst: NoReg, Imm: isNilLabel})
	l.writeValue(n, l.nullValue(v, t), t.notNull())
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(isNilLabel)
	l.writeLit("nil")

	l.mark(done)
}

// strOfNull is writeNull's counterpart for interpolation and str(),
// where a nullable renders unquoted - "printed 7 nil", not
// "printed 7 \"nil\"".
func (l *lowerer) strOfNull(n Node, v Reg, t vty) Reg {
	out := l.temp(vStr)

	isNilLabel := l.newLabel()
	done := l.newLabel()

	l.emit(Instr{Op: OpJumpIf, A: l.isNil(v), Dst: NoReg, Imm: isNilLabel})
	l.emit(Instr{Op: OpStore, A: l.toStr(l.nullValue(v, t), n), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(isNilLabel)
	l.emit(Instr{Op: OpStore, A: l.strLit("nil"), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, vStr)
}

// findExpr is `find(m, k)`: the value for a key as a ?V, so that absent
// and zero stay distinguishable where a bare index cannot tell them
// apart.
func (l *lowerer) findExpr(c *Call) Reg {
	m := l.expr(c.Args[0])
	t := l.regTy[m]
	if t.k != kMap {
		l.errorAt(c, "find expects a map, got %s", t)
		return l.junk()
	}
	key := l.expr(c.Args[1])
	if l.regTy[key].k != t.key {
		l.errorAt(c, "this map is keyed by %s, but the key is %s", t.keyType(), l.regTy[key])
		return l.junk()
	}

	want := vNullOf(t.elemType())
	out := l.temp(want)

	idxSlot := l.temp(vInt)
	hitSlot := l.temp(vInt)
	l.mapScan(m, key, t, idxSlot, hitSlot)

	miss := l.newLabel()
	done := l.newLabel()
	hit := l.compare(OpNe, l.load(hitSlot, vInt), l.constant(0))
	l.emit(Instr{Op: OpJumpNot, A: hit, Dst: NoReg, Imm: miss})

	l.emit(Instr{Op: OpStore,
		A:   l.nullBox(l.cellAt(m, mapValsOff, l.load(idxSlot, vInt), t.elemType()), want),
		Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(miss)
	l.emit(Instr{Op: OpStore, A: l.nilValue(want), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, want)
}

// separateNullable runs before `n[i] = v` where n is a ?[N]T the checker
// narrowed. The write goes into the block n holds, and a ?T is passed
// around as that one word, so every other holder of the same block would
// see it change; an array is a value. n is first given a block of its
// own, which costs one copy, and only on a write through a nullable.
func (l *lowerer) separateNullable(idx *Index) {
	id, ok := idx.X.(*Ident)
	if !ok || !id.Narrowed {
		return
	}
	t, ok := l.varType(id.Name)
	if !ok || !t.null || t.res || !isArray(t.notNull()) {
		return
	}
	at := ParseType(t.String())
	if at == nil {
		return
	}
	fresh := &AssignStmt{Span: id.Span, Target: &Ident{Span: id.Span, Name: id.Name}, Op: ASSIGN,
		Value: &Widen{Span: id.Span, X: id, T: at}}
	l.assign(fresh)
}

// varType is the type a name was declared with, whether it is a local,
// a global or a variable the enclosing function captured.
func (l *lowerer) varType(name string) (vty, bool) {
	if slot, ok := l.lookup(name); ok {
		return l.slotTy[slot], true
	}
	if t, ok := l.captureTy[name]; ok {
		return t, true
	}
	if g, ok := l.globals[name]; ok {
		return l.globalTy[g], true
	}
	return vVoid, false
}
