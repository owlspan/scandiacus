package main

// Mark and sweep.
//
// The cycle is: index every object so a word can be tested against it,
// find the roots, trace from them precisely, then free what nothing
// reached. Nothing here may allocate through the tracked allocator - the
// table and the worklist go straight to malloc, because a table of
// objects must not be an object.

// collectSym is the collector, emitted once as a real function.
//
// A function rather than inlined code for two reasons. It is large, and
// it is called wherever a program asks. More importantly the stack scan
// starts at the collector's own rsp, so everything its caller had live
// is above that and gets scanned; inlined, there would be no frame
// boundary to start from.
const collectSym = "gccollect"

func (l *lowerer) collect() {
	l.emitCollector()
	l.callHelper(collectSym, nil, nil, vVoid)
}

func (l *lowerer) emitCollector() {
	if l.helpers[collectSym] {
		return
	}
	l.helperFunc(collectSym, nil, vVoid, func([]Reg) {
		// Never while another thread runs: its stack is not scanned, so
		// what only it holds would look dead. See threads.go.
		busy := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, l.rtLoad(gcTasksSlot), l.constant(0)),
			Dst: NoReg, Imm: busy})
		l.collectBody()
		l.mark(busy)
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
}

// rawWord is a word of scratch memory that is not an object.
//
// The collector cannot use ptrSlot for this. ptrSlot allocates through
// the tracked allocator, so a word of scratch would be threaded onto the
// object list in the middle of a collection - after the index of what
// exists was built, and therefore invisible to it. This was a real bug:
// three of them per cycle, each freed by the sweep that had just been
// told nothing pointed at it.
func (l *lowerer) rawWord() Reg {
	p := l.ccall("calloc", []Reg{l.constant(1), l.constant(wordSize)},
		[]vty{vInt, vInt}, vInt, false, false)
	return p
}

// peekWord and pokeWord are raw memory access. The collector works on
// words, not on Veyl values, so nothing here is typed.
func (l *lowerer) peekWord(addr Reg, off int64) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: addr, B: NoReg, Imm: off})
	return d
}

func (l *lowerer) pokeWord(addr Reg, off int64, v Reg) {
	l.emit(Instr{Op: OpStoreMem, A: addr, B: v, Imm: off})
}

// unmarked strips the mark bit off a link word, leaving the pointer.
func (l *lowerer) unmarked(link Reg) Reg {
	return l.arith(OpBAnd, link, l.constant(^int64(objMarkBit)))
}

func (l *lowerer) collectBody() {
	// ---- how many objects there are ----
	count := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: count})
	l.walkObjects(func(raw Reg) {
		l.emit(Instr{Op: OpStore,
			A:   l.arith(OpAdd, l.load(count, vInt), l.constant(1)),
			Dst: NoReg, Imm: count})
	})

	nothing := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpGt, l.load(count, vInt), l.constant(0)),
		Dst: NoReg, Imm: nothing})

	// ---- an index of every payload address ----
	//
	// Open addressing, power-of-two capacity, at least twice the object
	// count so the probes stay short.
	capSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(16), Dst: NoReg, Imm: capSlot})
	grow := l.newLabel()
	sized := l.newLabel()
	l.mark(grow)
	wanted := l.arith(OpMul, l.load(count, vInt), l.constant(2))
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, l.load(capSlot, vInt), wanted),
		Dst: NoReg, Imm: sized})
	l.emit(Instr{Op: OpStore, A: l.arith(OpMul, l.load(capSlot, vInt), l.constant(2)),
		Dst: NoReg, Imm: capSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: grow})
	l.mark(sized)

	tableCap := l.load(capSlot, vInt)
	table := l.ccall("calloc", []Reg{tableCap, l.constant(wordSize)},
		[]vty{vInt, vInt}, vInt, false, false)
	mask := l.arith(OpSub, tableCap, l.constant(1))

	// The worklist can never hold more than every object at once. Its
	// length lives in a word of heap rather than a stack slot, because
	// the marking helpers need its address.
	work := l.ccall("malloc",
		[]Reg{l.arith(OpMul, l.load(count, vInt), l.constant(wordSize))},
		[]vty{vInt}, vInt, false, false)
	workLen := l.rawWord()

	// ---- fill the index, and clear the marks from last time ----
	l.walkObjects(func(raw Reg) {
		l.tableInsert(table, mask, l.arith(OpAdd, raw, l.constant(objHeader)))
		l.pokeWord(raw, objNextOff, l.unmarked(l.peekWord(raw, objNextOff)))
	})

	// ---- roots ----
	//
	// The stack first: from this function's own rsp to the top of the
	// thread's stack, every word. Everything above rsp belongs to a
	// frame that is still live, and every live pointer in this backend
	// is in a slot in one of those frames - the register allocator only
	// takes values that are not pointers, so no pointer lives only in a
	// register. Native frames in between, a callback's caller, are
	// scanned too; a word there that is not a known object is ignored.
	lowOut := l.rawWord()
	highOut := l.rawWord()
	l.ccall("GetCurrentThreadStackLimits", []Reg{lowOut, highOut},
		[]vty{vInt, vInt}, vVoid, false, false)

	sp := l.newReg()
	l.regTy[sp] = vInt
	l.emit(Instr{Op: OpStackPtr, Dst: sp, A: NoReg, B: NoReg})
	l.scanRange(sp, l.loadPtr(highOut), table, mask, work, workLen)

	// Then the globals, the other place a live pointer can be without
	// being on the stack.
	base := l.rtSlot(0)
	l.scanRange(base,
		l.arith(OpAdd, base, l.arith(OpMul, l.rtLoad(gcNGlobSlot), l.constant(wordSize))),
		table, mask, work, workLen)

	l.traceAll(table, mask, work, workLen)
	l.sweep()

	l.ccall("free", []Reg{table}, []vty{vInt}, vVoid, false, false)
	l.ccall("free", []Reg{work}, []vty{vInt}, vVoid, false, false)
	l.ccall("free", []Reg{workLen}, []vty{vInt}, vVoid, false, false)
	l.ccall("free", []Reg{lowOut}, []vty{vInt}, vVoid, false, false)
	l.ccall("free", []Reg{highOut}, []vty{vInt}, vVoid, false, false)

	l.mark(nothing)
	l.rtBump(gcCyclesSlot, l.constant(1))
}

// walkObjects runs body for every object on the list, passing the raw
// pointer - the header, not the payload.
func (l *lowerer) walkObjects(body func(raw Reg)) {
	p := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.rtLoad(gcHeadSlot), Dst: NoReg, Imm: p})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)
	cur := l.load(p, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpNe, cur, l.constant(0)),
		Dst: NoReg, Imm: done})

	// The link is read before the body runs, so a body that unlinks the
	// object still knows where to go next.
	next := l.unmarked(l.peekWord(cur, objNextOff))
	nextSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: next, Dst: NoReg, Imm: nextSlot})

	body(cur)

	l.emit(Instr{Op: OpStore, A: l.load(nextSlot, vInt), Dst: NoReg, Imm: p})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
}

// hashAddr spreads an address across the table. The low four bits go
// first because malloc aligns, so they carry no information.
func (l *lowerer) hashAddr(addr, mask Reg) Reg {
	h := l.arith(OpShr, addr, l.constant(4))
	h = l.arith(OpMul, h, l.constant(2654435761))
	return l.arith(OpBAnd, h, mask)
}

// tableInsert puts one address into the set. An empty cell is zero,
// which is safe because no object ever lives at address zero.
func (l *lowerer) tableInsert(table, mask, addr Reg) {
	i := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.hashAddr(addr, mask), Dst: NoReg, Imm: i})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)
	cell := l.arith(OpAdd, table, l.arith(OpMul, l.load(i, vInt), l.constant(wordSize)))
	cellSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: cell, Dst: NoReg, Imm: cellSlot})
	l.emit(Instr{Op: OpJumpIf,
		A:   l.compare(OpEq, l.peekWord(l.load(cellSlot, vInt), 0), l.constant(0)),
		Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpStore,
		A:   l.arith(OpBAnd, l.arith(OpAdd, l.load(i, vInt), l.constant(1)), mask),
		Dst: NoReg, Imm: i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	l.pokeWord(l.load(cellSlot, vInt), 0, addr)
}

// tableHas reports whether an address is a known payload.
func (l *lowerer) tableHas(table, mask, addr Reg) Reg {
	found := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: found})

	// Zero is never an object, and testing it first spares a table probe
	// for every null word on the stack, of which there are many.
	done := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpNe, addr, l.constant(0)),
		Dst: NoReg, Imm: done})

	i := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.hashAddr(addr, mask), Dst: NoReg, Imm: i})

	top := l.newLabel()
	hit := l.newLabel()
	l.mark(top)
	cell := l.arith(OpAdd, table, l.arith(OpMul, l.load(i, vInt), l.constant(wordSize)))
	held := l.peekWord(cell, 0)
	heldSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: held, Dst: NoReg, Imm: heldSlot})

	// An empty cell means the address is not in the table: insertion
	// would have put it before this point.
	l.emit(Instr{Op: OpJumpIf,
		A:   l.compare(OpEq, l.load(heldSlot, vInt), l.constant(0)),
		Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpJumpIf,
		A:   l.compare(OpEq, l.load(heldSlot, vInt), addr),
		Dst: NoReg, Imm: hit})
	l.emit(Instr{Op: OpStore,
		A:   l.arith(OpBAnd, l.arith(OpAdd, l.load(i, vInt), l.constant(1)), mask),
		Dst: NoReg, Imm: i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(hit)
	l.emit(Instr{Op: OpStore, A: l.constant(1), Dst: NoReg, Imm: found})

	l.mark(done)
	return l.compare(OpNe, l.load(found, vInt), l.constant(0))
}

// tryMark marks a candidate if it is an object that is not marked yet,
// and puts it on the worklist when it does.
//
// It is offered a word from a dozen places - every root range and every
// kind of child - so it is one function they all call rather than a
// hash probe written out at each.
func (l *lowerer) tryMark(candidate, table, mask, work, workLen Reg) {
	ts := []vty{vInt, vInt, vInt, vInt, vInt}
	sym := l.helperFunc("__gc_try", ts, vVoid, func(a []Reg) {
		l.tryMarkBody(a[0], a[1], a[2], a[3], a[4])
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
	l.callHelper(sym, []Reg{candidate, table, mask, work, workLen}, ts, vVoid)
}

func (l *lowerer) tryMarkBody(candidate, table, mask, work, workLen Reg) {
	done := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.tableHas(table, mask, candidate),
		Dst: NoReg, Imm: done})

	raw := l.arith(OpSub, candidate, l.constant(objHeader))
	rawSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: raw, Dst: NoReg, Imm: rawSlot})

	link := l.peekWord(l.load(rawSlot, vInt), objNextOff)
	linkSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: link, Dst: NoReg, Imm: linkSlot})

	already := l.compare(OpNe,
		l.arith(OpBAnd, l.load(linkSlot, vInt), l.constant(objMarkBit)), l.constant(0))
	l.emit(Instr{Op: OpJumpIf, A: already, Dst: NoReg, Imm: done})

	l.pokeWord(l.load(rawSlot, vInt), objNextOff,
		l.arith(OpBOr, l.load(linkSlot, vInt), l.constant(objMarkBit)))

	n := l.peekWord(workLen, 0)
	l.pokeWord(l.arith(OpAdd, work, l.arith(OpMul, n, l.constant(wordSize))), 0, candidate)
	l.pokeWord(workLen, 0, l.arith(OpAdd, n, l.constant(1)))

	l.mark(done)
}

// scanRange offers every word between two addresses as a possible root.
// This is the conservative half: a word that happens to equal an
// object's address keeps it alive whether it meant to or not.
func (l *lowerer) scanRange(from, to, table, mask, work, workLen Reg) {
	toSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: to, Dst: NoReg, Imm: toSlot})

	at := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: from, Dst: NoReg, Imm: at})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)
	cur := l.load(at, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, cur, l.load(toSlot, vInt)),
		Dst: NoReg, Imm: done})
	// Inline in the two loops that offer every word - here and the
	// fields of an object - since that is where marking spends its time.
	l.tryMarkBody(l.peekWord(cur, 0), table, mask, work, workLen)
	l.emit(Instr{Op: OpStore,
		A:   l.arith(OpAdd, l.load(at, vInt), l.constant(wordSize)),
		Dst: NoReg, Imm: at})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
}

// traceAll drains the worklist, following each object's children.
//
// This is the precise half. The header says what the object is, so
// nothing has to guess which of its words are pointers - which is what
// the tag was put there for, long before there was a collector.
func (l *lowerer) traceAll(table, mask, work, workLen Reg) {
	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	n := l.peekWord(workLen, 0)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpGt, n, l.constant(0)),
		Dst: NoReg, Imm: done})

	last := l.arith(OpSub, n, l.constant(1))
	l.pokeWord(workLen, 0, last)
	obj := l.peekWord(l.arith(OpAdd, work, l.arith(OpMul, last, l.constant(wordSize))), 0)
	objSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: obj, Dst: NoReg, Imm: objSlot})

	l.traceOne(l.load(objSlot, vInt), table, mask, work, workLen)
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
}

// traceOne offers one object's children to the marker.
func (l *lowerer) traceOne(obj, table, mask, work, workLen Reg) {
	header := l.peekWord(l.arith(OpSub, obj, l.constant(objHeader)), objTagOff)
	hdrSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: header, Dst: NoReg, Imm: hdrSlot})
	tag := l.arith(OpBAnd, l.load(hdrSlot, vInt), l.constant(0xFF))
	tagSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: tag, Dst: NoReg, Imm: tagSlot})

	done := l.newLabel()

	// A block of pointers, and a struct's leading pointers, are the same
	// loop over a count that the header supplies differently.
	words := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: words})

	isPtrs := l.compare(OpEq, l.load(tagSlot, vInt), l.constant(tagPtrs))
	notPtrs := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: isPtrs, Dst: NoReg, Imm: notPtrs})
	l.emit(Instr{Op: OpStore,
		A: l.arith(OpDiv, l.arith(OpShr, l.load(hdrSlot, vInt), l.constant(tagShift)),
			l.constant(wordSize)),
		Dst: NoReg, Imm: words})
	l.mark(notPtrs)

	isStruct := l.compare(OpEq, l.load(tagSlot, vInt), l.constant(tagStruct))
	notStruct := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: isStruct, Dst: NoReg, Imm: notStruct})
	l.emit(Instr{Op: OpStore,
		A: l.arith(OpBAnd,
			l.arith(OpShr, l.load(hdrSlot, vInt), l.constant(structNPtrShift)),
			l.constant(0xFF)),
		Dst: NoReg, Imm: words})
	l.mark(notStruct)

	i := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: i})
	top := l.newLabel()
	walked := l.newLabel()
	l.mark(top)
	l.emit(Instr{Op: OpJumpNot,
		A:   l.compare(OpLt, l.load(i, vInt), l.load(words, vInt)),
		Dst: NoReg, Imm: walked})
	child := l.peekWord(
		l.arith(OpAdd, obj, l.arith(OpMul, l.load(i, vInt), l.constant(wordSize))), 0)
	l.tryMarkBody(child, table, mask, work, workLen)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(i, vInt), l.constant(1)),
		Dst: NoReg, Imm: i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(walked)

	// A list header points at its element block; a map header at its two
	// blocks. Their other words are a length and a capacity, which are
	// numbers and must not be offered as pointers.
	notList := l.newLabel()
	l.emit(Instr{Op: OpJumpNot,
		A:   l.compare(OpEq, l.load(tagSlot, vInt), l.constant(tagList)),
		Dst: NoReg, Imm: notList})
	l.tryMark(l.peekWord(obj, listDataOff), table, mask, work, workLen)
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
	l.mark(notList)

	notMap := l.newLabel()
	l.emit(Instr{Op: OpJumpNot,
		A:   l.compare(OpEq, l.load(tagSlot, vInt), l.constant(tagMap)),
		Dst: NoReg, Imm: notMap})
	l.tryMark(l.peekWord(obj, mapKeysOff), table, mask, work, workLen)
	l.tryMark(l.peekWord(obj, mapValsOff), table, mask, work, workLen)
	l.tryMark(l.peekWord(obj, mapIdxOff), table, mask, work, workLen)
	l.mark(notMap)

	// A result box always traces its first word, the error string. Its
	// second word is traced too when the header says the value inside is
	// a pointer shape. Before this case existed the only thing keeping a
	// failure's message alive was stale copies of its address left on
	// the stack below the caller's frame, so any collection that ran
	// after deeper calls overwrote those words freed a string the
	// program still meant to read.
	isRes := l.compare(OpEq, l.load(tagSlot, vInt), l.constant(tagRes))
	notRes := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: isRes, Dst: NoReg, Imm: notRes})
	l.tryMark(l.peekWord(obj, resErrOff), table, mask, work, workLen)
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
	l.mark(notRes)

	isResPtr := l.compare(OpEq, l.load(tagSlot, vInt), l.constant(tagResPtr))
	notResPtr := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: isResPtr, Dst: NoReg, Imm: notResPtr})
	l.tryMark(l.peekWord(obj, resErrOff), table, mask, work, workLen)
	l.tryMark(l.peekWord(obj, resValOff), table, mask, work, workLen)
	l.mark(notResPtr)

	l.mark(done)
}

// sweep frees every object nothing reached, and clears the marks on the
// ones that survived.
func (l *lowerer) sweep() {
	// prev is the address of the link that points at the current object,
	// which starts as the head slot itself. Keeping it as an address
	// rather than as an object means unlinking the first object needs no
	// special case.
	prev := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.rtSlot(gcHeadSlot), Dst: NoReg, Imm: prev})

	p := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.rtLoad(gcHeadSlot), Dst: NoReg, Imm: p})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)
	cur := l.load(p, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpNe, cur, l.constant(0)),
		Dst: NoReg, Imm: done})

	link := l.peekWord(cur, objNextOff)
	linkSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: link, Dst: NoReg, Imm: linkSlot})
	next := l.unmarked(l.load(linkSlot, vInt))
	nextSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: next, Dst: NoReg, Imm: nextSlot})

	live := l.compare(OpNe,
		l.arith(OpBAnd, l.load(linkSlot, vInt), l.constant(objMarkBit)), l.constant(0))
	dead := l.newLabel()
	step := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: live, Dst: NoReg, Imm: dead})

	// Survived: clear the mark and move prev along.
	l.pokeWord(cur, objNextOff, l.load(nextSlot, vInt))
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, cur, l.constant(objNextOff)),
		Dst: NoReg, Imm: prev})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: step})

	l.mark(dead)
	l.pokeWord(l.load(prev, vInt), 0, l.load(nextSlot, vInt))
	size := l.jsonObjectSize(l.peekWord(cur, objTagOff))
	l.rtStore(gcLiveSlot, l.arith(OpSub, l.rtLoad(gcLiveSlot), l.constant(1)))
	l.rtStore(gcBytesSlot, l.arith(OpSub, l.rtLoad(gcBytesSlot), size))
	l.ccall("free", []Reg{cur}, []vty{vInt}, vVoid, false, false)

	l.mark(step)
	l.emit(Instr{Op: OpStore, A: l.load(nextSlot, vInt), Dst: NoReg, Imm: p})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
}

// jsonObjectSize reads the payload size out of a header word. A struct
// keeps its size further up, because the eight bits below it hold the
// pointer count.
func (l *lowerer) jsonObjectSize(header Reg) Reg {
	hdrSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: header, Dst: NoReg, Imm: hdrSlot})

	plain := l.arith(OpShr, l.load(hdrSlot, vInt), l.constant(tagShift))
	asStruct := l.arith(OpShr, l.load(hdrSlot, vInt), l.constant(structSizeShift))
	isStruct := l.compare(OpEq,
		l.arith(OpBAnd, l.load(hdrSlot, vInt), l.constant(0xFF)), l.constant(tagStruct))
	return l.pick(isStruct, asStruct, plain, vInt)
}
