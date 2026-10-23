package main

// The math library.
//
// Almost none of it is here. sin, cos, tan, asin, acos, atan, atan2,
// exp, log, log2, log10, pow, cbrt and hypot are all written in Scandiacus, in
// prelude_math.go, and this file only routes a call to them - see
// prelude.go for why calling msvcrt instead would be a different
// function rather than a different implementation.
//
// What is left here is the handful that are one comparison or one call
// and would be sillier as Scandiacus source than as five lines of IR.

func (l *lowerer) mathBuiltin(c *Call, name string) (Reg, bool) {
	arity := func(n int) bool {
		if len(c.Args) != n {
			l.errorAt(c, "%s takes %d argument(s), got %d", name, n, len(c.Args))
			return false
		}
		return true
	}

	// Everything transcendental is a call into the prelude, which is
	// Scandiacus source compiled by this compiler - see prelude.go. Routing it
	// here rather than renaming the callee earlier keeps the error a
	// user sees naming `sin` rather than `__vy_sin`.
	if fn, ok := preludeOf[name]; ok {
		sig, declared := l.sigs[fn]
		if !declared {
			l.errorAt(c, "the prelude did not supply %s", fn)
			return l.junk(), true
		}
		if defaults := preludeDefaults[name]; len(defaults) > 0 {
			missing := len(sig.params) - len(c.Args)
			if missing > 0 && missing <= len(defaults) {
				// A copy of the call, so the checker's own record of
				// the original is left as it was written.
				cc := *c
				cc.Args = append(append([]Expr{}, c.Args...), defaults[len(defaults)-missing:]...)
				c = &cc
			}
		}
		if !arity(len(sig.params)) {
			return l.junk(), true
		}
		// Lowered against the prelude function's own parameter types,
		// the same way an ordinary user call is. Promoting everything to
		// float is right for sin and wrong for time.format, and getting
		// that wrong hands a string to a function expecting one and a
		// float to a function expecting an int, which does not fail - it
		// runs and reads the wrong memory.
		args := make([]Reg, len(c.Args))
		for i := range c.Args {
			v := l.rvalueAs(c.Args[i], sig.params[i])
			if sig.params[i].k == kFloat && l.regTy[v].k == kInt {
				v = l.toFloat(v)
			}
			// A list of ints where a list of floats is wanted. stats is
			// the only caller, and the Go backend widens the same way
			// before it calls - so stats.mean of an int list is the mean
			// of the floats, not integer arithmetic.
			if sig.params[i].k == kList && sig.params[i].elemKind() == kFloat &&
				l.regTy[v].k == kList && l.regTy[v].elemKind() == kInt {
				v = l.widenIntList(v)
			}
			args[i] = v
		}
		if len(args) > l.fn.MaxCallArgs {
			l.fn.MaxCallArgs = len(args)
		}
		d := l.newReg()
		l.regTy[d] = sig.ret
		l.emit(Instr{Op: OpCall, Dst: d, A: NoReg, B: NoReg, Args: args,
			ArgTypes: sig.params, RetType: sig.ret, Sym: fn, Comment: name + "()"})
		return d, true
	}

	switch name {
	case "__abort", "__abortStr":
		if !arity(1) {
			return l.junk(), true
		}
		l.mod.needs("must")
		reason := l.expr(c.Args[0])
		if l.regTy[reason].k != kStr {
			l.errorAt(c.Args[0], "__abort expects a str")
			return l.junk(), true
		}
		l.emit(Instr{Op: OpMustFail, A: reason, Dst: NoReg, B: NoReg,
			Imm: l.where(), Comment: "__abort()"})
		// Nothing runs after it, but the lowerer still needs a register
		// to hand back to whatever asked for the value.
		if name == "__abortStr" {
			return l.emptyStr(), true
		}
		return l.floatConst(0), true

	case "__bits", "__frombits":
		if !arity(1) {
			return l.junk(), true
		}
		// A bitcast through a stack slot: storing a float and loading
		// the same slot as an int is the reinterpretation, and the only
		// thing that changes is what the lowerer believes about the
		// register it hands back. Load forwarding knows not to fold the
		// load away - see sameClass in opt.go - because the two sides
		// may live in different register files.
		want, from := vInt, kFloat
		if name == "__frombits" {
			want, from = vFloat, kInt
		}
		v := l.expr(c.Args[0])
		if l.regTy[v].k != from {
			l.errorAt(c.Args[0], "%s expects a %s", name, vty{k: from})
			return l.junk(), true
		}
		slot := l.temp(l.regTy[v])
		l.emit(Instr{Op: OpStore, A: v, Dst: NoReg, Imm: slot})
		d := l.newReg()
		l.regTy[d] = want
		l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: slot})
		return d, true

	case "sign":
		if !arity(1) {
			return l.junk(), true
		}
		x := l.numeric(c.Args[0])
		zero := l.floatConst(0)
		pos := l.pick(l.compare(OpFGt, x, zero), l.intConst(1), l.intConst(0), vInt)
		return l.pick(l.compare(OpFLt, x, zero), l.intConst(-1), pos, vInt), true

	case "clamp":
		if !arity(3) {
			return l.junk(), true
		}
		x := l.numeric(c.Args[0])
		lo, hi := l.numeric(c.Args[1]), l.numeric(c.Args[2])
		// Written in the Go backend's order: below the floor first, then
		// above the ceiling, so clamp(x, 5, 1) gives 1 on both.
		x = l.pick(l.compare(OpFLt, x, lo), lo, x, vFloat)
		return l.pick(l.compare(OpFGt, x, hi), hi, x, vFloat), true

	case "isNan":
		if !arity(1) {
			return l.junk(), true
		}
		// A NaN is the only value that is not equal to itself, and the
		// float comparisons in x64.go are NaN-correct.
		x := l.numeric(c.Args[0])
		eq := l.compare(OpFEq, x, x)
		return l.pick(eq, l.intConst(0), l.intConst(1), vBool), true

	case "exit":
		if !arity(1) {
			return l.junk(), true
		}
		code := l.expr(c.Args[0])
		if l.regTy[code].k != kInt {
			l.errorAt(c.Args[0], "exit expects an int, got %s", l.regTy[code])
			return l.junk(), true
		}
		// Flushed first: exit through msvcrt does run the stream
		// teardown, but print here writes through printf and a runtime
		// abort writes through _write, and only an explicit flush keeps
		// those two in the order the program wrote them.
		l.ccall("fflush", []Reg{l.intConst(0)}, []vty{vInt}, vty{k: kVoid}, false, false)
		l.ccall("exit", []Reg{code}, []vty{vInt}, vty{k: kVoid}, false, false)
		return l.junk(), true

	case "sleep":
		if !arity(1) {
			return l.junk(), true
		}
		// Milliseconds, which is what the Go backend's time.Sleep gets.
		// This used to take seconds and multiply, so sleep(1) waited a
		// second here and a millisecond there.
		ms := l.expr(c.Args[0])
		l.blockingCall("Sleep", []Reg{ms}, []vty{vInt}, vty{k: kVoid}, false)
		return l.junk(), true
	}

	return NoReg, false
}

// widenIntList copies a list of ints into a fresh list of floats.
//
// A copy, not a reinterpretation: the two have the same shape in memory
// and completely different contents, since an int slot holds the number
// and a float slot holds its IEEE bits. Reading one as the other is the
// kind of mistake that produces enormous nonsense rather than an error.
func (l *lowerer) widenIntList(src Reg) Reg {
	out := l.newList(vty{k: kList, el: &vty{k: kFloat}}, 0)
	n := l.field(src, listLenOff, vInt)

	i := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: i})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	cur := l.newReg()
	l.regTy[cur] = vInt
	l.emit(Instr{Op: OpLoad, Dst: cur, A: NoReg, B: NoReg, Imm: i})
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, cur, n), Dst: NoReg, Imm: done})

	l.listPush(out, l.toFloat(l.listGet(src, cur, vInt)))
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, cur, l.constant(1)), Dst: NoReg, Imm: i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	return out
}

// intConst is an integer literal as a register.
func (l *lowerer) intConst(n int64) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpConst, Dst: d, A: NoReg, B: NoReg, Imm: n})
	return d
}

// slotAddr is the address of a frame slot, for a C function that writes
// through a pointer.
func (l *lowerer) slotAddr(slot int64) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpSlotAddr, Dst: d, A: NoReg, B: NoReg, Imm: slot})
	return d
}
