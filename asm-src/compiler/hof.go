package main

// map, filter, reduce, sortBy, any, all and each.
//
// These are in the lowerer for the same reason shuffle is: they work on
// a list of anything, and Scandiacus has no generics, so they cannot be
// written in the prelude. Here the element type is a compile-time fact
// and the loop is the same loop whatever it turns out to be.
//
// Every one of them is a walk over the list calling a function value,
// which is what made closures the thing standing in front of all seven.

func (l *lowerer) hofBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "map", "filter", "each", "any", "all":
		if len(c.Args) != 2 {
			l.errorAt(c, "%s takes 2 arguments, got %d", name, len(c.Args))
			return l.junk(), true
		}
	case "reduce":
		if len(c.Args) != 3 {
			l.errorAt(c, "reduce takes 3 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
	case "sortBy":
		if len(c.Args) != 2 {
			l.errorAt(c, "sortBy takes 2 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
	default:
		return NoReg, false
	}

	xs := l.expr(c.Args[0])
	lt := l.regTy[xs]
	if lt.k != kList {
		l.errorAt(c.Args[0], "%s expects a list, got %s", name, lt)
		return l.junk(), true
	}
	elem := lt.elemType()

	fi := 1
	if name == "reduce" {
		fi = 2
	}
	f := l.expr(c.Args[fi])
	ft := l.regTy[f]
	if ft.k != kFunc || ft.fn == nil {
		l.errorAt(c.Args[fi], "%s expects a function here, got %s", name, ft)
		return l.junk(), true
	}

	switch name {
	case "map":
		return l.hofMap(xs, f, elem, ft.fn.ret), true
	case "filter":
		return l.hofFilter(xs, f, lt, elem), true
	case "each":
		l.hofEach(xs, f, elem)
		return l.junk(), true
	case "any":
		return l.hofAnyAll(xs, f, elem, true), true
	case "all":
		return l.hofAnyAll(xs, f, elem, false), true
	case "sortBy":
		return l.hofSortBy(xs, f, lt, elem), true
	case "reduce":
		init := l.rvalueAs(c.Args[1], ft.fn.ret)
		if ft.fn.ret.k == kFloat && l.regTy[init].k == kInt {
			init = l.toFloat(init)
		}
		return l.hofReduce(xs, init, f, elem, ft.fn.ret), true
	}
	return l.junk(), true
}

// listLoop runs body once per element, with the index and the element in
// hand. It returns the labels so a caller that wants to leave early -
// any and all both do - can jump out of it.
type listLoop struct {
	i    int64 // the slot holding the index
	top  int64
	done int64
}

func (l *lowerer) beginLoop(xs Reg) listLoop {
	lp := listLoop{i: l.temp(vInt), top: l.newLabel(), done: l.newLabel()}
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: lp.i})
	l.mark(lp.top)
	// The length is read each time round rather than once, because a
	// callback is arbitrary code and may have pushed.
	n := l.field(xs, listLenOff, vInt)
	cur := l.newReg()
	l.regTy[cur] = vInt
	l.emit(Instr{Op: OpLoad, Dst: cur, A: NoReg, B: NoReg, Imm: lp.i})
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, cur, n), Dst: NoReg, Imm: lp.done})
	return lp
}

func (l *lowerer) loopIndex(lp listLoop) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: lp.i})
	return d
}

func (l *lowerer) endLoop(lp listLoop) {
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.loopIndex(lp), l.constant(1)),
		Dst: NoReg, Imm: lp.i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: lp.top})
	l.mark(lp.done)
}

func (l *lowerer) hofMap(xs, f Reg, elem, ret vty) Reg {
	out := l.newList(vListOf(ret), 0)
	lp := l.beginLoop(xs)
	v := l.listGet(xs, l.loopIndex(lp), elem)
	l.listPush(out, l.callClosure(f, []Reg{v}, []vty{elem}, ret))
	l.endLoop(lp)
	return out
}

func (l *lowerer) hofFilter(xs, f Reg, lt, elem vty) Reg {
	out := l.newList(lt, 0)
	lp := l.beginLoop(xs)
	v := l.listGet(xs, l.loopIndex(lp), elem)
	keep := l.callClosure(f, []Reg{v}, []vty{elem}, vBool)
	skip := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: keep, Dst: NoReg, Imm: skip})
	l.listPush(out, v)
	l.mark(skip)
	l.endLoop(lp)
	return out
}

func (l *lowerer) hofEach(xs, f Reg, elem vty) {
	lp := l.beginLoop(xs)
	v := l.listGet(xs, l.loopIndex(lp), elem)
	l.callClosure(f, []Reg{v}, []vty{elem}, vVoid)
	l.endLoop(lp)
}

// hofAnyAll is one loop for both, because they are the same loop with
// the answer and the early exit swapped: any leaves true on the first
// yes, all leaves false on the first no.
func (l *lowerer) hofAnyAll(xs, f Reg, elem vty, isAny bool) Reg {
	out := l.temp(vBool)
	start := int64(1)
	if isAny {
		start = 0
	}
	l.emit(Instr{Op: OpStore, A: l.constant(start), Dst: NoReg, Imm: out})

	lp := l.beginLoop(xs)
	v := l.listGet(xs, l.loopIndex(lp), elem)
	got := l.callClosure(f, []Reg{v}, []vty{elem}, vBool)
	keep := l.newLabel()
	if isAny {
		l.emit(Instr{Op: OpJumpNot, A: got, Dst: NoReg, Imm: keep})
		l.emit(Instr{Op: OpStore, A: l.constant(1), Dst: NoReg, Imm: out})
	} else {
		l.emit(Instr{Op: OpJumpIf, A: got, Dst: NoReg, Imm: keep})
		l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: out})
	}
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: lp.done})
	l.mark(keep)
	l.endLoop(lp)

	d := l.newReg()
	l.regTy[d] = vBool
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: out})
	return d
}

func (l *lowerer) hofReduce(xs, init, f Reg, elem, acc vty) Reg {
	slot := l.temp(acc)
	l.emit(Instr{Op: OpStore, A: init, Dst: NoReg, Imm: slot})

	lp := l.beginLoop(xs)
	cur := l.newReg()
	l.regTy[cur] = acc
	l.emit(Instr{Op: OpLoad, Dst: cur, A: NoReg, B: NoReg, Imm: slot})
	v := l.listGet(xs, l.loopIndex(lp), elem)
	next := l.callClosure(f, []Reg{cur, v}, []vty{acc, elem}, acc)
	l.emit(Instr{Op: OpStore, A: next, Dst: NoReg, Imm: slot})
	l.endLoop(lp)

	d := l.newReg()
	l.regTy[d] = acc
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: slot})
	return d
}

// hofSortBy is an insertion sort over a copy.
//
// Insertion, not something faster, because the Go backend uses
// sort.SliceStable and a stable sort has exactly one answer: equal
// elements keep the order they were in. Any stable algorithm therefore
// agrees with any other, and an unstable one would disagree on the first
// list with a tie in it - which is a difference no test notices until it
// does. Sorting a list this way is quadratic, and the lists a program
// sorts by a callback are small enough that the honesty is worth more
// than the asymptotics.
func (l *lowerer) hofSortBy(xs, f Reg, lt, elem vty) Reg {
	out := l.listSlice(xs, lt, l.constant(0), l.field(xs, listLenOff, vInt))
	fs := l.temp(l.regTy[f])
	l.setSlot(fs, f)
	// less(a, b) - only a strict yes puts b's element first, which is
	// what keeps equal elements where they were.
	l.mergeSortList(out, lt, func(a, b Reg) Reg {
		return l.callClosure(l.load(fs, l.regTy[f]), []Reg{a, b}, []vty{elem, elem}, vBool)
	})
	return out
}
