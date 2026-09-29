package main

// Deep equality for containers and structs.
//
// `==` on a list, a map or a struct compares contents, which is what it
// means for everything else in the language and what the printed form
// suggests. The Go backend gets this from reflect.DeepEqual; here the
// comparison is generated from the static type, so a nested container
// recurses in the lowerer rather than at runtime. The depth is whatever
// the type says, so the recursion always terminates - a type cannot
// contain itself except through a struct, and that case is refused.

// deepEqual emits a comparison of two values of the same type and hands
// back the bool. It reports false when the type has no equality here.
func (l *lowerer) deepEqual(n Node, a, b Reg, t vty) (Reg, bool) {
	switch {
	case t.res:
		l.errorAt(n, "a %s cannot be compared - unwrap it first", t)
		return NoReg, false

	case t.null:
		return l.nullEqual(n, a, b, t)
	}

	switch t.k {
	case kInt, kBool:
		return l.compare(OpEq, a, b), true
	case kFloat:
		return l.compare(OpFEq, a, b), true
	case kStr:
		return l.strEq(a, b), true
	case kBytes:
		return l.bytesEqual(a, b), true
	case kList:
		return l.listEqual(n, a, b, t)
	case kMap:
		return l.mapEqual(n, a, b, t)
	case kStruct:
		if l.isView(t) {
			// Two views are equal when they look at the same address.
			return l.compare(OpEq, a, b), true
		}
		// A type that holds itself is compared by a function of its own,
		// which calls itself for what is inside: inlined, it never ends.
		if l.eqInline == t.name {
			l.eqInline = ""
		} else if l.recursiveStruct(t.name) {
			plain := vStructOf(t.name)
			ok := true
			sym := l.helperFunc("__eq_"+t.name, []vty{plain, plain}, vBool, func(args []Reg) {
				l.eqInline = t.name
				same, good := l.deepEqual(n, args[0], args[1], plain)
				if !good {
					ok = false
					same = l.boolConst(false)
				}
				l.emit(Instr{Op: OpRet, A: same, Dst: NoReg})
			})
			if !ok {
				return NoReg, false
			}
			return l.callHelper(sym, []Reg{a, b}, []vty{plain, plain}, vBool), true
		}
		if e := dataEnums[t.name]; e != nil {
			return l.variantEqual(n, a, b, t, e)
		}
		return l.structEqual(n, a, b, t)
	}
	l.errorAt(n, "%s cannot be compared on the assembly backend yet", t)
	return NoReg, false
}

// nullEqual: two nils are equal, a nil and a value are not, and two
// values compare as whatever is inside them.
func (l *lowerer) nullEqual(n Node, a, b Reg, t vty) (Reg, bool) {
	out := l.temp(vBool)
	done := l.newLabel()
	bothSet := l.newLabel()

	aNil := l.isNil(a)
	bNil := l.isNil(b)

	// Equal when both are nil.
	l.emit(Instr{Op: OpStore, A: l.boolConst(true), Dst: NoReg, Imm: out})
	neither := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: aNil, Dst: NoReg, Imm: neither})
	l.emit(Instr{Op: OpStore, A: bNil, Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	// a is set, so they differ if b is nil.
	l.mark(neither)
	l.emit(Instr{Op: OpStore, A: l.boolConst(false), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJumpNot, A: bNil, Dst: NoReg, Imm: bothSet})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(bothSet)
	inner, ok := l.deepEqual(n, l.nullValue(a, t), l.nullValue(b, t), t.notNull())
	if !ok {
		return NoReg, false
	}
	l.emit(Instr{Op: OpStore, A: inner, Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, vBool), true
}

// loadField reads one field out of a struct instance.
func (l *lowerer) loadField(obj Reg, f structField) Reg {
	d := l.newReg()
	l.regTy[d] = f.t
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: obj, B: NoReg, Imm: f.off,
		Comment: "." + f.name})
	return d
}

// boolConst is true or false as a register.
func (l *lowerer) boolConst(v bool) Reg {
	n := int64(0)
	if v {
		n = 1
	}
	d := l.constant(n)
	l.regTy[d] = vBool
	return d
}

// listEqual: same length, then every element, stopping at the first that
// differs.
func (l *lowerer) listEqual(n Node, a, b Reg, t vty) (Reg, bool) {
	out := l.temp(vBool)
	l.emit(Instr{Op: OpStore, A: l.boolConst(false), Dst: NoReg, Imm: out})

	done := l.newLabel()
	lenA := l.field(a, listLenOff, vInt)
	lenB := l.field(b, listLenOff, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, lenA, lenB), Dst: NoReg, Imm: done})

	// Same length, so equal unless an element says otherwise.
	l.emit(Instr{Op: OpStore, A: l.boolConst(true), Dst: NoReg, Imm: out})

	elem := t.elemType()
	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	l.mark(top)
	i := l.load(iSlot, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, i, lenA), Dst: NoReg, Imm: done})

	same, ok := l.deepEqual(n, l.listGet(a, i, elem), l.listGet(b, i, elem), elem)
	if !ok {
		return NoReg, false
	}
	differs := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: same, Dst: NoReg, Imm: differs})
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(differs)
	l.emit(Instr{Op: OpStore, A: l.boolConst(false), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, vBool), true
}

// mapEqual: same length, then the same keys in the same places with the
// same values.
//
// Comparing position by position is only valid because the entries are
// kept sorted. A hash table here would have to look each key up in the
// other map instead - one more reason the sorted representation earns
// its O(n) lookup.
func (l *lowerer) mapEqual(n Node, a, b Reg, t vty) (Reg, bool) {
	out := l.temp(vBool)
	l.emit(Instr{Op: OpStore, A: l.boolConst(false), Dst: NoReg, Imm: out})

	// Entry by entry, so both have to be in key order first.
	l.mapSort(a, t)
	l.mapSort(b, t)
	done := l.newLabel()
	lenA := l.field(a, mapLenOff, vInt)
	lenB := l.field(b, mapLenOff, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, lenA, lenB), Dst: NoReg, Imm: done})

	l.emit(Instr{Op: OpStore, A: l.boolConst(true), Dst: NoReg, Imm: out})

	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	differs := l.newLabel()
	l.mark(top)
	i := l.load(iSlot, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, i, lenA), Dst: NoReg, Imm: done})

	sameKey, ok := l.deepEqual(n,
		l.cellAt(a, mapKeysOff, i, t.keyType()),
		l.cellAt(b, mapKeysOff, i, t.keyType()), t.keyType())
	if !ok {
		return NoReg, false
	}
	l.emit(Instr{Op: OpJumpNot, A: sameKey, Dst: NoReg, Imm: differs})

	sameVal, ok := l.deepEqual(n,
		l.cellAt(a, mapValsOff, i, t.elemType()),
		l.cellAt(b, mapValsOff, i, t.elemType()), t.elemType())
	if !ok {
		return NoReg, false
	}
	l.emit(Instr{Op: OpJumpNot, A: sameVal, Dst: NoReg, Imm: differs})

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(differs)
	l.emit(Instr{Op: OpStore, A: l.boolConst(false), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, vBool), true
}

// structEqual: every field, in layout order, stopping at the first that
// differs. Unrolled rather than looped, because the fields are known and
// each one has its own type.
func (l *lowerer) structEqual(n Node, a, b Reg, t vty) (Reg, bool) {
	lay, ok := l.layoutOf(n, t)
	if !ok {
		return NoReg, false
	}

	out := l.temp(vBool)
	l.emit(Instr{Op: OpStore, A: l.boolConst(false), Dst: NoReg, Imm: out})
	done := l.newLabel()

	for _, f := range lay.fields {
		same, good := l.deepEqual(n,
			l.loadField(a, f), l.loadField(b, f), f.t)
		if !good {
			return NoReg, false
		}
		l.emit(Instr{Op: OpJumpNot, A: same, Dst: NoReg, Imm: done})
	}
	l.emit(Instr{Op: OpStore, A: l.boolConst(true), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, vBool), true
}
