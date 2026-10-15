package main

// sort and sortBy: a bottom-up merge sort.
//
// Both were insertion sorts, which is quadratic: a million numbers took
// minutes. This is O(n log n) and stable - an element moves ahead of an
// equal one only if less says so strictly - which is what both have
// always promised.
//
// It merges runs of width 1, 2, 4, ... back and forth between the list
// being sorted and a second list of the same length, and copies back at
// the end if the last pass left the result in the second one. Both are
// ordinary lists held in slots of the list's own type, and the element
// block is read out of the header again after every comparison, because
// sortBy's comparison is the program's own function: it can allocate,
// and so collect, in the middle of a merge.

// mergeSortList sorts the elements of list in place by less.
func (l *lowerer) mergeSortList(list Reg, lt vty, less func(a, b Reg) Reg) {
	elem := lt.elemType()
	a := l.temp(lt)
	l.setSlot(a, list)
	n := l.temp(vInt)
	l.setSlot(n, l.field(list, listLenOff, vInt))

	// The second list: as long as the first, its length set so that
	// what is merged into it is part of it to the collector.
	b := l.temp(lt)
	l.setSlot(b, l.newListLen(lt, l.load(n, vInt)))
	swapped := l.temp(vInt)
	l.setSlot(swapped, l.constant(0))

	data := func(slot int64) Reg { return l.field(l.load(slot, lt), listDataOff, vInt) }
	ld := func(slot int64) Reg { return l.load(slot, vInt) }
	minN := func(v Reg) int64 {
		s := l.temp(vInt)
		l.setSlot(s, v)
		ok := l.newLabel()
		l.jumpNot(l.compare(OpGt, ld(s), ld(n)), ok)
		l.setSlot(s, ld(n))
		l.mark(ok)
		return s
	}

	width := l.temp(vInt)
	l.setSlot(width, l.constant(1))
	passTop, passDone := l.newLabel(), l.newLabel()
	l.mark(passTop)
	l.jumpNot(l.compare(OpLt, ld(width), ld(n)), passDone)

	i := l.temp(vInt)
	l.setSlot(i, l.constant(0))
	runTop, runDone := l.newLabel(), l.newLabel()
	l.mark(runTop)
	l.jumpNot(l.compare(OpLt, ld(i), ld(n)), runDone)

	mid := minN(l.arith(OpAdd, ld(i), ld(width)))
	hi := minN(l.arith(OpAdd, ld(i), l.arith(OpShl, ld(width), l.constant(1))))
	p, q, k := l.temp(vInt), l.temp(vInt), l.temp(vInt)
	l.setSlot(p, ld(i))
	l.setSlot(q, ld(mid))
	l.setSlot(k, ld(i))

	mergeTop, mergeDone := l.newLabel(), l.newLabel()
	takeLeft, takeRight, next := l.newLabel(), l.newLabel(), l.newLabel()
	l.mark(mergeTop)
	l.jumpNot(l.compare(OpLt, ld(k), ld(hi)), mergeDone)
	l.jumpNot(l.compare(OpLt, ld(p), ld(mid)), takeRight)
	l.jumpNot(l.compare(OpLt, ld(q), ld(hi)), takeLeft)
	right := l.wordAt(data(a), ld(q), elem)
	left := l.wordAt(data(a), ld(p), elem)
	l.jumpIf(less(right, left), takeRight)
	l.jump(takeLeft)

	l.mark(takeLeft)
	l.setWordAt(data(b), ld(k), l.wordAt(data(a), ld(p), elem))
	l.setSlot(p, l.arith(OpAdd, ld(p), l.constant(1)))
	l.jump(next)
	l.mark(takeRight)
	l.setWordAt(data(b), ld(k), l.wordAt(data(a), ld(q), elem))
	l.setSlot(q, l.arith(OpAdd, ld(q), l.constant(1)))
	l.mark(next)
	l.setSlot(k, l.arith(OpAdd, ld(k), l.constant(1)))
	l.jump(mergeTop)
	l.mark(mergeDone)

	l.setSlot(i, ld(hi))
	l.jump(runTop)
	l.mark(runDone)

	// The merged runs are in b now: b is the one to read next time.
	t := l.load(a, lt)
	l.setSlot(a, l.load(b, lt))
	l.setSlot(b, t)
	l.setSlot(swapped, l.arith(OpBXor, ld(swapped), l.constant(1)))
	l.setSlot(width, l.arith(OpShl, ld(width), l.constant(1)))
	l.jump(passTop)
	l.mark(passDone)

	// An odd number of passes left the result in the other list.
	done := l.newLabel()
	l.jumpIf(l.compare(OpEq, ld(swapped), l.constant(0)), done)
	l.copyBytes(l.field(list, listDataOff, vInt), data(a), l.arith(OpMul, ld(n), l.constant(wordSize)))
	l.mark(done)
}

// newListLen is a list of n zero elements.
func (l *lowerer) newListLen(t vty, n Reg) Reg {
	ns := l.temp(vInt)
	l.setSlot(ns, n)
	capacity := l.temp(vInt)
	l.setSlot(capacity, l.load(ns, vInt))
	one := l.newLabel()
	l.jumpIf(l.compare(OpGt, l.load(capacity, vInt), l.constant(0)), one)
	l.setSlot(capacity, l.constant(1))
	l.mark(one)
	hdr := l.allocObj(l.constant(listHeader), tagList)
	l.regTy[hdr] = t
	hs := l.temp(t)
	l.setSlot(hs, hdr)
	bytes := l.arith(OpMul, l.load(capacity, vInt), l.constant(wordSize))
	blk := l.allocObj(bytes, elemTag(t))
	l.ccall("memset", []Reg{blk, l.constant(0), l.arith(OpMul, l.load(capacity, vInt), l.constant(wordSize))},
		[]vty{vInt, vInt, vInt}, vInt, false, false)
	l.emit(Instr{Op: OpStoreMem, A: l.load(hs, t), B: blk, Imm: listDataOff})
	l.emit(Instr{Op: OpStoreMem, A: l.load(hs, t), B: l.load(ns, vInt), Imm: listLenOff})
	l.emit(Instr{Op: OpStoreMem, A: l.load(hs, t), B: l.load(capacity, vInt), Imm: listCapOff})
	return l.load(hs, t)
}
