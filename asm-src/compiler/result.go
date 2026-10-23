package main

// The error type `T!`, built in the IR.
//
// Scandiacus has no exceptions. Anything that can fail returns `T!`: either a
// T, or the reason there is not one. Every library function in the
// language reports failure this way, which is why this had to exist
// before any more of the library could be ported - without it a
// function that fails can only be given a signature that lies about it.
//
// Layout. A result is a two-word heap object with the usual header:
//
//	[ptr-8]  size << 8 | tag
//	[ptr+0]  err, a pointer to NUL-terminated bytes, 0 when it worked
//	[ptr+8]  the value, meaningless when err is set
//
// Two things follow from that shape, and both are the reason for it.
//
// The layout does not depend on T. So `?` can propagate a failure out
// of a str! and into an int! by handing back the very object it was
// given, rather than unpacking and rebuilding one. A failure crosses
// any number of frames as a single pointer.
//
// And a result is one word wherever it is held, so the IR stays
// three-address and the calling convention needs no second return
// register. The Go backend returns a two-field struct by value; doing
// the same here would mean classifying an aggregate return, which is
// the part of the Windows x64 ABI with the most rules and the least
// payoff.
//
// The cost is an allocation per fallible call. Nothing is ever freed
// yet, so that cost is real and unbounded, and it is the same trade
// every other allocation here already makes: be correct and uniform
// first, and give the collector one more shape to walk rather than a
// special case to know about.
const (
	resErrOff = 0
	resValOff = wordSize
	resWords  = 2

	// A result's error word is always a pointer and its value word is a
	// pointer only sometimes, so one tag cannot describe both. Two do.
	// A collector reading either knows the first word is a string to
	// trace and whether the second is anything at all.
	tagRes    = 5 // [err ptr, value holding no pointer]: int!, bool!
	tagResPtr = 6 // [err ptr, value that is a pointer]: str!, []int!
)

// resTag picks the tag for a result carrying t.
func resTag(t vty) int64 {
	if t.inner().holdsPointer() {
		return tagResPtr
	}
	return tagRes
}

// resBox allocates a result object and fills both words.
//
// err is a string pointer or a zero constant; val is the carried value
// or junk when there is none. Callers go through resOk or resFail
// rather than here, so that the two cases read differently at the call
// site even though they build the same object.
func (l *lowerer) resBox(err, val Reg, t vty) Reg {
	box := l.allocObj(l.constant(resWords*wordSize), resTag(t))
	l.emit(Instr{Op: OpStoreMem, A: box, B: err, Imm: resErrOff})
	l.emit(Instr{Op: OpStoreMem, A: box, B: val, Imm: resValOff})
	l.regTy[box] = vResultOf(t.inner())
	return box
}

// resOk wraps a plain value as the successful T!.
func (l *lowerer) resOk(val Reg, t vty) Reg {
	return l.resBox(l.constant(0), val, t)
}

// resFail builds the failing T! carrying msg.
//
// The value word is written as zero rather than left alone, because an
// allocator that hands back dirty memory would otherwise leave a
// pointer-shaped integer where a collector expects to find a value it
// can trust the tag about.
func (l *lowerer) resFail(msg Reg, t vty) Reg {
	return l.resBox(msg, l.constant(0), t)
}

// resErr loads the failure reason, zero when there is none.
func (l *lowerer) resErr(r Reg) Reg {
	d := l.newReg()
	l.regTy[d] = vStr
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: r, B: NoReg, Imm: resErrOff})
	return d
}

// resValue loads the carried value, typed as the T inside the T!.
func (l *lowerer) resValue(r Reg, t vty) Reg {
	d := l.newReg()
	l.regTy[d] = t.inner()
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: r, B: NoReg, Imm: resValOff})
	return d
}

// resIsOk is err == 0.
func (l *lowerer) resIsOk(r Reg) Reg {
	return l.compare(OpEq, l.resErr(r), l.constant(0))
}

// tryExpr lowers the postfix `?`: unwrap the result, or return it from
// the enclosing function unchanged.
//
// Returning the same object is what makes this cheap, and it is only
// sound because the layout is type-independent: the value word of a
// failed result is never read, so handing a failed str! back as the
// int! the caller declared loses nothing that exists.
//
// The checker has already established that there is an enclosing
// function and that it returns a result, so there is no context to
// verify here. `?` at the top level of a program is its error, not
// this one's.
func (l *lowerer) tryExpr(x *Try) Reg {
	r := l.expr(x.X)
	rt := l.regTy[r]
	if !rt.res {
		l.errorAt(x, "cannot use ? on %s, which is not a T!", rt)
		return l.junk()
	}

	fail := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.resIsOk(r), Dst: NoReg, Imm: fail,
		Comment: "? propagate on failure"})

	// The success path leaves the unwrapped value in a slot, because the
	// two paths have to agree on where it is and only one of them ever
	// arrives at the join.
	slot := l.temp(rt.inner())
	l.emit(Instr{Op: OpStore, A: l.resValue(r, rt), Dst: NoReg, Imm: slot})

	done := l.newLabel()
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(fail)
	l.emit(Instr{Op: OpRet, A: r, Dst: NoReg, Comment: "? failed"})

	l.mark(done)
	d := l.newReg()
	l.regTy[d] = rt.inner()
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: slot})
	return d
}

// mustExpr is `must(r)`: the value, or stop the program with the
// reason. The Go backend writes "runtime error: " and the message to
// stderr and exits 1, and this has to match it byte for byte.
func (l *lowerer) mustExpr(r Reg, t vty) Reg {
	l.mod.needs("must")

	ok := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.resIsOk(r), Dst: NoReg, Imm: ok})
	l.emit(Instr{Op: OpMustFail, A: l.resErr(r), Dst: NoReg, B: NoReg,
		Imm: l.where(), Comment: "must failed"})
	l.mark(ok)
	return l.resValue(r, t)
}

// valueOrExpr is `valueOr(r, alt)`: the value, or alt when it failed.
func (l *lowerer) valueOrExpr(r, alt Reg, t vty) Reg {
	slot := l.temp(t.inner())
	l.emit(Instr{Op: OpStore, A: alt, Dst: NoReg, Imm: slot})

	done := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.resIsOk(r), Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpStore, A: l.resValue(r, t), Dst: NoReg, Imm: slot})
	l.mark(done)

	d := l.newReg()
	l.regTy[d] = t.inner()
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: slot})
	return d
}

// errorOfExpr is `errorOf(r)`: the reason, or "" when it worked. The Go
// backend's result carries a plain string field that is empty on
// success, so an empty string is what this has to produce for a zero
// error word.
func (l *lowerer) errorOfExpr(r Reg) Reg {
	slot := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: l.strLit(""), Dst: NoReg, Imm: slot})

	done := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.resIsOk(r), Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpStore, A: l.resErr(r), Dst: NoReg, Imm: slot})
	l.mark(done)

	d := l.newReg()
	l.regTy[d] = vStr
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: slot})
	return d
}

// strLit materialises a literal string constant.
func (l *lowerer) strLit(v string) Reg {
	d := l.newReg()
	l.regTy[d] = vStr
	l.emit(Instr{Op: OpStr, Dst: d, A: NoReg, B: NoReg, Imm: l.mod.intern(v)})
	return d
}

// resultBuiltin lowers the seven builtins that make up the error type.
// It reports false for anything else, so the caller can go on looking.
//
// Each one takes its inner type from the operand rather than from an
// annotation, which is what lets them stay monomorphic here while the
// Go backend needs a generic for the same job.
func (l *lowerer) resultBuiltin(c *Call, name string) (Reg, bool) {
	arity := func(n int) bool {
		if len(c.Args) != n {
			l.errorAt(c, "%s takes %d argument(s), got %d", name, n, len(c.Args))
			return false
		}
		return true
	}

	// operand lowers the single result argument and insists it is one.
	operand := func() (Reg, vty, bool) {
		r := l.expr(c.Args[0])
		t := l.regTy[r]
		if !t.res {
			l.errorAt(c, "%s needs a T!, got %s", name, t)
			return l.junk(), vVoid, false
		}
		return r, t, true
	}

	switch name {
	case "fail":
		if !arity(1) {
			return l.junk(), true
		}
		// The checker records the result type the context is asking for,
		// which is the only thing that can say what a bare fail("...")
		// is a failure of. Without one the box is still correct - the
		// value word of a failure is never read - so an unknown inner
		// type is not an error here.
		t := vResultOf(vVoid)
		if want, ok := vtyOf(c.Want); ok && want.res {
			t = want
		}
		return l.resFail(l.expr(c.Args[0]), t), true

	case "ok":
		if !arity(0) {
			return l.junk(), true
		}
		return l.resOk(l.constant(0), vResultOf(vVoid)), true

	case "isOk":
		if !arity(1) {
			return l.junk(), true
		}
		r, _, good := operand()
		if !good {
			return l.junk(), true
		}
		return l.resIsOk(r), true

	case "failed":
		if !arity(1) {
			return l.junk(), true
		}
		r, _, good := operand()
		if !good {
			return l.junk(), true
		}
		return l.compare(OpNe, l.resErr(r), l.constant(0)), true

	case "errorOf":
		if !arity(1) {
			return l.junk(), true
		}
		r, _, good := operand()
		if !good {
			return l.junk(), true
		}
		return l.errorOfExpr(r), true

	case "must":
		if !arity(1) {
			return l.junk(), true
		}
		r, t, good := operand()
		if !good {
			return l.junk(), true
		}
		return l.mustExpr(r, t), true

	case "valueOr":
		if !arity(2) {
			return l.junk(), true
		}
		r, t, good := operand()
		if !good {
			return l.junk(), true
		}
		// The fallback has to be the same type as what the result
		// carries, so that type is a hint: valueOr(r, []) knows what
		// the empty list holds even though the literal does not.
		alt := l.rvalueAs(c.Args[1], t.inner())
		if t.inner().k == kFloat && l.regTy[alt].k == kInt {
			alt = l.toFloat(alt)
		}
		return l.valueOrExpr(r, alt, t), true
	}

	return NoReg, false
}

// widen lowers the boxing node the checker inserts wherever a plain T
// is used where a wrapper was wanted.
//
// Having the checker mark the spot is what keeps this honest. The
// alternative is every site that can accept a T! - returns, arguments,
// assignments, list elements - deciding for itself whether to box, and
// the first one anybody forgets produces a raw value where a pointer
// was expected, which is a crash somewhere else entirely.
func (l *lowerer) widen(x *Widen) Reg {
	t, ok := vtyOf(x.T)
	if !ok {
		l.errorAt(x, "%s is not on the assembly backend yet", x.T)
		return l.junk()
	}
	if !t.res && !t.null {
		l.errorAt(x, "%s is not on the assembly backend yet", x.T)
		return l.junk()
	}

	v := l.expr(x.X)

	// res and null are separate flags, so a ?T! has both set and needs
	// both wrappers. The nullable is the inner one, so it goes on first
	// and the result closes over it. Testing null first and returning
	// meant `return nil` from a ?int! handed back a bare zero where a
	// result object was expected, and the first read of it segfaulted.
	if t.null {
		v = l.nullWiden(v, t.inner())
	}
	if t.res {
		if !t.null && t.inner().k == kFloat && l.regTy[v].k == kInt {
			v = l.toFloat(v)
		}
		v = l.resOk(v, t)
	}
	return v
}
