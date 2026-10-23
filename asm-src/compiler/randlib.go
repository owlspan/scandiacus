package main

// shuffle, sample and pick.
//
// These three are in the lowerer rather than in the prelude for one
// reason: they work on a list of anything, and the prelude has no way to
// say that. A Scandiacus function has to name its parameter's type, and Scandiacus
// has no generics - so a prelude version would be one function per
// element type, which is three functions turning into fifteen.
//
// Here it is free. A list is a header and a block of words whatever the
// elements are, so swapping two of them is swapping two words and the
// element type never comes up. The randomness still comes from the
// prelude, so the sequence is the same one every other rand function
// draws from.

// randSource is the prelude function each of these draws from. Go's
// Shuffle uses the multiply-and-shift int31n rather than the rejection
// Int31n, and they are different sequences from the same state, so the
// distinction matters: using the wrong one shuffles correctly and
// disagrees with the Go backend on every element.
const randSource = "__vy_rngInt31nFast"

func (l *lowerer) randBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "rand.shuffle":
		if len(c.Args) != 1 {
			l.errorAt(c, "rand.shuffle takes 1 argument, got %d", len(c.Args))
			return l.junk(), true
		}
		src := l.expr(c.Args[0])
		if l.regTy[src].k != kList {
			l.errorAt(c.Args[0], "rand.shuffle expects a list, got %s", l.regTy[src])
			return l.junk(), true
		}
		return l.shuffled(src), true

	case "rand.sample":
		if len(c.Args) != 2 {
			l.errorAt(c, "rand.sample takes 2 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
		src := l.expr(c.Args[0])
		if l.regTy[src].k != kList {
			l.errorAt(c.Args[0], "rand.sample expects a list, got %s", l.regTy[src])
			return l.junk(), true
		}
		n := l.expr(c.Args[1])
		if l.regTy[n].k != kInt {
			l.errorAt(c.Args[1], "rand.sample expects a count, got %s", l.regTy[n])
			return l.junk(), true
		}
		// Shuffle the whole list and take a prefix, which is what the Go
		// backend does - so the same seed gives the same sample, and it
		// is the same sample as the first n of the same shuffle.
		t := l.regTy[src]
		return l.listSlice(l.shuffled(src), t, l.constant(0), l.clampCount(src, n)), true

	case "rand.pick":
		if len(c.Args) != 1 {
			l.errorAt(c, "rand.pick takes 1 argument, got %d", len(c.Args))
			return l.junk(), true
		}
		src := l.expr(c.Args[0])
		t := l.regTy[src]
		if t.k != kList {
			l.errorAt(c.Args[0], "rand.pick expects a list, got %s", t)
			return l.junk(), true
		}
		n := l.field(src, listLenOff, vInt)
		return l.listGet(src, l.randIndex(n), t.elemType()), true
	}

	return NoReg, false
}

// randIndex is one draw in [0, n), through the prelude's generator.
func (l *lowerer) randIndex(n Reg) Reg {
	sig, ok := l.sigs[randSource]
	if !ok {
		return l.constant(0)
	}
	if 1 > l.fn.MaxCallArgs {
		l.fn.MaxCallArgs = 1
	}
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpCall, Dst: d, A: NoReg, B: NoReg, Args: []Reg{n},
		ArgTypes: sig.params, RetType: sig.ret, Sym: randSource,
		Comment: "rand index"})
	return d
}

// clampCount is sample's count, held to 0 .. len(xs).
func (l *lowerer) clampCount(src, n Reg) Reg {
	length := l.field(src, listLenOff, vInt)
	atLeast := l.pick(l.compare(OpLt, n, l.constant(0)), l.constant(0), n, vInt)
	return l.pick(l.compare(OpGt, atLeast, length), length, atLeast, vInt)
}

// shuffled is Fisher-Yates over a copy, walking down from the end, which
// is the direction Go's Shuffle walks. Walking up produces a different
// permutation from the same draws.
func (l *lowerer) shuffled(src Reg) Reg {
	t := l.regTy[src]
	out := l.listSlice(src, t, l.constant(0), l.field(src, listLenOff, vInt))
	n := l.field(out, listLenOff, vInt)

	i := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, n, l.constant(1)), Dst: NoReg, Imm: i})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	cur := l.newReg()
	l.regTy[cur] = vInt
	l.emit(Instr{Op: OpLoad, Dst: cur, A: NoReg, B: NoReg, Imm: i})
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpGt, cur, l.constant(0)), Dst: NoReg, Imm: done})

	j := l.randIndex(l.arith(OpAdd, cur, l.constant(1)))
	elem := t.elemType()
	a := l.listGet(out, cur, elem)
	b := l.listGet(out, j, elem)
	l.listSet(out, cur, b)
	l.listSet(out, j, a)

	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, cur, l.constant(1)), Dst: NoReg, Imm: i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	return out
}
