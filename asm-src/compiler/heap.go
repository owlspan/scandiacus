package main

// The page allocator: where small objects live.
//
// Every object used to be its own malloc block, threaded onto one list.
// That made each collection walk the list three times - to count, to
// build a hash table of every address so a word could be tested
// against it, and to sweep - and each step of the walk was a cache miss
// somewhere new in the heap. With a million objects alive a collection
// took a third of a second, and a program that kept a big map and went
// on allocating spent most of its time there.
//
// Now an object of up to smallMax bytes, header included, is a slot in
// a 64 KB page, and every slot of a page is one size. Twenty-nine size
// classes: every multiple of 16 up to 256, then every multiple of 128
// up to 2048. Each class has a free list, whose head is a runtime word,
// threaded through the link word of the free slots; allocating is
// taking the head. Pages come out of 1 MB arenas from malloc, which are
// never given back.
//
// The object header is unchanged - the tag word, then the link word
// with the collector's mark in bit 0 and strappend's owned mark in bit
// 1 - and a slot in use has bit 2 of its link set as well, where a free
// slot's link is the next free slot, 16-aligned, with that bit clear.
//
// What that buys the collector:
//
//   - Whether a word is an object is arithmetic, not a lookup in a table
//     built for the occasion: the page is the word with its low 16 bits
//     cleared, a small hash table of page addresses says whether it is
//     one, and the slot is the offset divided by the page's block size.
//     The word must be a slot's payload exactly, and the slot in use.
//   - The sweep reads pages front to back, in address order, rather
//     than following a list around the heap, and a dead object goes
//     onto its free list instead of back to malloc.
//
// Objects bigger than smallMax are malloc blocks on the object list as
// before, and so are the strings __vy_talloc makes in a program that
// has no __alloc; the collector indexes that list the old way, which is
// cheap when it holds few objects.

const (
	pageShift  = 16
	pageBytes  = 1 << pageShift
	pageHeader = 64 // the header words below, padded so slots are 16-aligned
	arenaPages = 16 // pages per arena: 1 MB, from one malloc
	smallMax   = 2048

	pgBlockOff = 0  // block size of every slot, header included
	pgClassOff = 8  // its size class
	pgSlotsOff = 16 // how many slots
	pgNextOff  = 24 // the next page

	objLiveBit = 4 // in a slot's link word: the slot holds an object

	heapClasses = 29
)

// The runtime words the allocator keeps, after the collector's own.
const (
	heapFreeSlot  = 15                         // the first of heapClasses free list heads
	heapPagesSlot = heapFreeSlot + heapClasses // every page, linked through pgNextOff
	heapNextSlot  = heapPagesSlot + 1          // the next page to hand out in the current arena
	heapEndSlot   = heapPagesSlot + 2          // the end of the current arena
	heapTableSlot = heapPagesSlot + 3          // the hash table of page addresses
	heapMaskSlot  = heapPagesSlot + 4          // its size minus one
	heapCountSlot = heapPagesSlot + 5          // how many pages it holds
	cpuSlot       = heapPagesSlot + 6          // 0 not asked yet, 1 SSE2 only, 2 AVX2; see vecloop.go
	ccbSlot       = heapPagesSlot + 7          // closures native code may call, then their adapters; see closurecb.go
	cpuFmaSlot    = ccbSlot + 2*ccbSlots       // 1 when FMA can run too, set along with cpuSlot
	heapReserved  = cpuFmaSlot + 1             // the first word after all of these
)

// heapAllocSmall is the fast path of __alloc for a block of total bytes,
// header included, no bigger than smallMax: the head of its class's free
// list, with the header written and the counters bumped. It returns the
// payload.
func (l *lowerer) heapAllocSmall(total, payload, header Reg) Reg {
	// The class and its block size.
	t := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: total, Dst: NoReg, Imm: t})
	big := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe, l.load(t, vInt), l.constant(32)), Dst: NoReg, Imm: big})
	l.emit(Instr{Op: OpStore, A: l.constant(32), Dst: NoReg, Imm: t})
	l.mark(big)

	cls := l.temp(vInt)
	bs := l.temp(vInt)
	coarse := l.newLabel()
	classed := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGt, l.load(t, vInt), l.constant(256)), Dst: NoReg, Imm: coarse})
	c := l.arith(OpSub, l.arith(OpShr, l.arith(OpAdd, l.load(t, vInt), l.constant(15)), l.constant(4)), l.constant(2))
	l.emit(Instr{Op: OpStore, A: c, Dst: NoReg, Imm: cls})
	l.emit(Instr{Op: OpStore, A: l.arith(OpShl, l.arith(OpAdd, c, l.constant(2)), l.constant(4)), Dst: NoReg, Imm: bs})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: classed})
	l.mark(coarse)
	k := l.arith(OpShr, l.arith(OpAdd, l.load(t, vInt), l.constant(127-256)), l.constant(7))
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, k, l.constant(14)), Dst: NoReg, Imm: cls})
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.arith(OpShl, k, l.constant(7)), l.constant(256)), Dst: NoReg, Imm: bs})
	l.mark(classed)

	// With other threads running, under the allocation lock.
	locked := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.rtLoad(gcTasksSlot), Dst: NoReg, Imm: locked})
	alone := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(locked, vInt), l.constant(0)), Dst: NoReg, Imm: alone})
	l.spinLock()
	l.mark(alone)

	headAt := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.rtSlot(heapFreeSlot),
		l.arith(OpShl, l.load(cls, vInt), l.constant(3))), Dst: NoReg, Imm: headAt})
	have := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpNe, l.peekWord(l.load(headAt, vInt), 0), l.constant(0)), Dst: NoReg, Imm: have})
	l.heapRefill(l.load(cls, vInt), l.load(bs, vInt))
	l.mark(have)

	slot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.peekWord(l.load(headAt, vInt), 0), Dst: NoReg, Imm: slot})
	l.pokeWord(l.load(headAt, vInt), 0, l.peekWord(l.load(slot, vInt), objNextOff))
	l.pokeWord(l.load(slot, vInt), objTagOff, header)
	l.pokeWord(l.load(slot, vInt), objNextOff, l.constant(objLiveBit))

	l.rtBump(gcLiveSlot, l.constant(1))
	l.rtBump(gcBytesSlot, payload)
	l.rtBump(gcTotalSlot, payload)

	done := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(locked, vInt), l.constant(0)), Dst: NoReg, Imm: done})
	l.spinUnlock()
	l.mark(done)
	return l.arith(OpAdd, l.load(slot, vInt), l.constant(objHeader))
}

// heapRefill gives a class's empty free list a fresh page's slots.
func (l *lowerer) heapRefill(cls, bs Reg) {
	sym := l.helperFunc("__heap_refill", []vty{vInt, vInt}, vVoid, func(a []Reg) {
		clsS := l.temp(vInt)
		bsS := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: a[0], Dst: NoReg, Imm: clsS})
		l.emit(Instr{Op: OpStore, A: a[1], Dst: NoReg, Imm: bsS})

		// A page from the current arena, or a new arena.
		page := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: l.rtLoad(heapNextSlot), Dst: NoReg, Imm: page})
		got := l.newLabel()
		fresh := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(page, vInt), l.constant(0)), Dst: NoReg, Imm: fresh})
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpLe,
			l.arith(OpAdd, l.load(page, vInt), l.constant(pageBytes)), l.rtLoad(heapEndSlot)), Dst: NoReg, Imm: got})
		l.mark(fresh)
		chunk := l.ccall("malloc", []Reg{l.constant(arenaPages*pageBytes + pageBytes)},
			[]vty{vInt}, vInt, false, false)
		aligned := l.arith(OpBAnd, l.arith(OpAdd, chunk, l.constant(pageBytes-1)), l.constant(^int64(pageBytes-1)))
		l.emit(Instr{Op: OpStore, A: aligned, Dst: NoReg, Imm: page})
		l.rtStore(heapEndSlot, l.arith(OpAdd, l.load(page, vInt), l.constant(arenaPages*pageBytes)))
		l.mark(got)
		l.rtStore(heapNextSlot, l.arith(OpAdd, l.load(page, vInt), l.constant(pageBytes)))

		n := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: l.arith(OpDiv, l.constant(pageBytes-pageHeader), l.load(bsS, vInt)), Dst: NoReg, Imm: n})
		l.pokeWord(l.load(page, vInt), pgBlockOff, l.load(bsS, vInt))
		l.pokeWord(l.load(page, vInt), pgClassOff, l.load(clsS, vInt))
		l.pokeWord(l.load(page, vInt), pgSlotsOff, l.load(n, vInt))
		l.pokeWord(l.load(page, vInt), pgNextOff, l.rtLoad(heapPagesSlot))
		l.rtStore(heapPagesSlot, l.load(page, vInt))
		l.heapIndex(l.load(page, vInt))

		// Thread the slots, first to last, onto the class's list, which
		// is empty: the last slot's link is zero.
		s := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(page, vInt), l.constant(pageHeader)), Dst: NoReg, Imm: s})
		last := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(s, vInt),
			l.arith(OpMul, l.arith(OpSub, l.load(n, vInt), l.constant(1)), l.load(bsS, vInt))), Dst: NoReg, Imm: last})
		top := l.newLabel()
		end := l.newLabel()
		l.mark(top)
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe, l.load(s, vInt), l.load(last, vInt)), Dst: NoReg, Imm: end})
		next := l.arith(OpAdd, l.load(s, vInt), l.load(bsS, vInt))
		l.pokeWord(l.load(s, vInt), objTagOff, l.constant(0))
		l.pokeWord(l.load(s, vInt), objNextOff, next)
		l.emit(Instr{Op: OpStore, A: next, Dst: NoReg, Imm: s})
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
		l.mark(end)
		l.pokeWord(l.load(last, vInt), objTagOff, l.constant(0))
		l.pokeWord(l.load(last, vInt), objNextOff, l.constant(0))

		l.pokeWord(l.arith(OpAdd, l.rtSlot(heapFreeSlot), l.arith(OpShl, l.load(clsS, vInt), l.constant(3))), 0,
			l.arith(OpAdd, l.load(page, vInt), l.constant(pageHeader)))
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
	l.callHelper(sym, []Reg{cls, bs}, []vty{vInt, vInt}, vVoid)
}

// heapHash is where a page's address starts its probe in the table.
func (l *lowerer) heapHash(page, mask Reg) Reg {
	return l.arith(OpBAnd, l.arith(OpMul, l.arith(OpShr, page, l.constant(pageShift)), l.constant(2654435761)), mask)
}

// heapIndex adds a page to the table of pages, doubling the table when
// it would be more than half full.
func (l *lowerer) heapIndex(page Reg) {
	sym := l.helperFunc("__heap_index", []vty{vInt}, vVoid, func(a []Reg) {
		pg := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: a[0], Dst: NoReg, Imm: pg})

		roomy := l.newLabel()
		grow := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.rtLoad(heapTableSlot), l.constant(0)), Dst: NoReg, Imm: grow})
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpLe,
			l.arith(OpMul, l.arith(OpAdd, l.rtLoad(heapCountSlot), l.constant(1)), l.constant(2)),
			l.arith(OpAdd, l.rtLoad(heapMaskSlot), l.constant(1))), Dst: NoReg, Imm: roomy})
		l.mark(grow)

		old := l.temp(vInt)
		oldMask := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: l.rtLoad(heapTableSlot), Dst: NoReg, Imm: old})
		l.emit(Instr{Op: OpStore, A: l.rtLoad(heapMaskSlot), Dst: NoReg, Imm: oldMask})
		size := l.pick(l.compare(OpEq, l.load(old, vInt), l.constant(0)), l.constant(64),
			l.arith(OpMul, l.arith(OpAdd, l.load(oldMask, vInt), l.constant(1)), l.constant(2)), vInt)
		sizeS := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: size, Dst: NoReg, Imm: sizeS})
		table := l.ccall("calloc", []Reg{l.load(sizeS, vInt), l.constant(wordSize)},
			[]vty{vInt, vInt}, vInt, false, false)
		l.rtStore(heapTableSlot, table)
		l.rtStore(heapMaskSlot, l.arith(OpSub, l.load(sizeS, vInt), l.constant(1)))

		moved := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(old, vInt), l.constant(0)), Dst: NoReg, Imm: moved})
		i := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: i})
		top := l.newLabel()
		l.mark(top)
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGt, l.load(i, vInt), l.load(oldMask, vInt)), Dst: NoReg, Imm: moved})
		e := l.peekWord(l.arith(OpAdd, l.load(old, vInt), l.arith(OpShl, l.load(i, vInt), l.constant(3))), 0)
		eS := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: e, Dst: NoReg, Imm: eS})
		skip := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(eS, vInt), l.constant(0)), Dst: NoReg, Imm: skip})
		l.heapPut(l.load(eS, vInt))
		l.mark(skip)
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(i, vInt), l.constant(1)), Dst: NoReg, Imm: i})
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
		l.mark(moved)
		freed := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(old, vInt), l.constant(0)), Dst: NoReg, Imm: freed})
		l.ccall("free", []Reg{l.load(old, vInt)}, []vty{vInt}, vVoid, false, false)
		l.mark(freed)

		l.mark(roomy)
		l.heapPut(l.load(pg, vInt))
		l.rtBump(heapCountSlot, l.constant(1))
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
	l.callHelper(sym, []Reg{page}, []vty{vInt}, vVoid)
}

// heapPut puts a page address into the first empty cell of its probe.
func (l *lowerer) heapPut(page Reg) {
	pg := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: page, Dst: NoReg, Imm: pg})
	i := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.heapHash(l.load(pg, vInt), l.rtLoad(heapMaskSlot)), Dst: NoReg, Imm: i})
	top := l.newLabel()
	empty := l.newLabel()
	l.mark(top)
	cell := l.arith(OpAdd, l.rtLoad(heapTableSlot), l.arith(OpShl, l.load(i, vInt), l.constant(3)))
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.peekWord(cell, 0), l.constant(0)), Dst: NoReg, Imm: empty})
	l.emit(Instr{Op: OpStore, A: l.arith(OpBAnd, l.arith(OpAdd, l.load(i, vInt), l.constant(1)), l.rtLoad(heapMaskSlot)),
		Dst: NoReg, Imm: i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(empty)
	l.pokeWord(l.arith(OpAdd, l.rtLoad(heapTableSlot), l.arith(OpShl, l.load(i, vInt), l.constant(3))), 0, l.load(pg, vInt))
}

// heapPage is the page a word points into, or 0 when it points into
// none of them.
func (l *lowerer) heapPage(w Reg) Reg {
	out := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: out})
	done := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.rtLoad(heapTableSlot), l.constant(0)), Dst: NoReg, Imm: done})
	pg := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpBAnd, w, l.constant(^int64(pageBytes-1))), Dst: NoReg, Imm: pg})
	i := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.heapHash(l.load(pg, vInt), l.rtLoad(heapMaskSlot)), Dst: NoReg, Imm: i})
	top := l.newLabel()
	hit := l.newLabel()
	l.mark(top)
	held := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.peekWord(l.arith(OpAdd, l.rtLoad(heapTableSlot),
		l.arith(OpShl, l.load(i, vInt), l.constant(3))), 0), Dst: NoReg, Imm: held})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(held, vInt), l.constant(0)), Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(held, vInt), l.load(pg, vInt)), Dst: NoReg, Imm: hit})
	l.emit(Instr{Op: OpStore, A: l.arith(OpBAnd, l.arith(OpAdd, l.load(i, vInt), l.constant(1)), l.rtLoad(heapMaskSlot)),
		Dst: NoReg, Imm: i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(hit)
	l.emit(Instr{Op: OpStore, A: l.load(pg, vInt), Dst: NoReg, Imm: out})
	l.mark(done)
	return l.load(out, vInt)
}

// heapSlotOf is the header of the object in use whose payload a word in
// a page is exactly, or 0.
func (l *lowerer) heapSlotOf(w, page Reg) Reg {
	ws := l.temp(vInt)
	ps := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: w, Dst: NoReg, Imm: ws})
	l.emit(Instr{Op: OpStore, A: page, Dst: NoReg, Imm: ps})
	out := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: out})
	done := l.newLabel()
	off := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, l.load(ws, vInt),
		l.arith(OpAdd, l.load(ps, vInt), l.constant(pageHeader+objHeader))), Dst: NoReg, Imm: off})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpLt, l.load(off, vInt), l.constant(0)), Dst: NoReg, Imm: done})
	bs := l.peekWord(l.load(ps, vInt), pgBlockOff)
	bsS := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: bs, Dst: NoReg, Imm: bsS})
	q := l.arith(OpDiv, l.load(off, vInt), l.load(bsS, vInt))
	qS := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: q, Dst: NoReg, Imm: qS})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpNe, l.arith(OpMul, l.load(qS, vInt), l.load(bsS, vInt)), l.load(off, vInt)),
		Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe, l.load(qS, vInt), l.peekWord(l.load(ps, vInt), pgSlotsOff)),
		Dst: NoReg, Imm: done})
	raw := l.arith(OpSub, l.load(ws, vInt), l.constant(objHeader))
	rawS := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: raw, Dst: NoReg, Imm: rawS})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq,
		l.arith(OpBAnd, l.peekWord(l.load(rawS, vInt), objNextOff), l.constant(objLiveBit)), l.constant(0)),
		Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpStore, A: l.load(rawS, vInt), Dst: NoReg, Imm: out})
	l.mark(done)
	return l.load(out, vInt)
}

// heapSweep puts every unmarked object in a page back on its free list,
// takes the mark off the rest, and takes the dead off the counters.
func (l *lowerer) heapSweep() {
	deadN := l.temp(vInt)
	deadB := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: deadN})
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: deadB})

	page := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.rtLoad(heapPagesSlot), Dst: NoReg, Imm: page})
	pages := l.newLabel()
	swept := l.newLabel()
	l.mark(pages)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(page, vInt), l.constant(0)), Dst: NoReg, Imm: swept})

	bs := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.peekWord(l.load(page, vInt), pgBlockOff), Dst: NoReg, Imm: bs})
	headAt := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.rtSlot(heapFreeSlot),
		l.arith(OpShl, l.peekWord(l.load(page, vInt), pgClassOff), l.constant(3))), Dst: NoReg, Imm: headAt})
	s := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(page, vInt), l.constant(pageHeader)), Dst: NoReg, Imm: s})
	end := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(s, vInt),
		l.arith(OpMul, l.peekWord(l.load(page, vInt), pgSlotsOff), l.load(bs, vInt))), Dst: NoReg, Imm: end})

	slots := l.newLabel()
	paged := l.newLabel()
	next := l.newLabel()
	l.mark(slots)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe, l.load(s, vInt), l.load(end, vInt)), Dst: NoReg, Imm: paged})
	link := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.peekWord(l.load(s, vInt), objNextOff), Dst: NoReg, Imm: link})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq,
		l.arith(OpBAnd, l.load(link, vInt), l.constant(objLiveBit)), l.constant(0)), Dst: NoReg, Imm: next})
	dead := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq,
		l.arith(OpBAnd, l.load(link, vInt), l.constant(objMarkBit)), l.constant(0)), Dst: NoReg, Imm: dead})
	l.pokeWord(l.load(s, vInt), objNextOff, l.arith(OpBAnd, l.load(link, vInt), l.constant(^int64(objMarkBit))))
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: next})
	l.mark(dead)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(deadN, vInt), l.constant(1)), Dst: NoReg, Imm: deadN})
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(deadB, vInt),
		l.jsonObjectSize(l.peekWord(l.load(s, vInt), objTagOff))), Dst: NoReg, Imm: deadB})
	l.pokeWord(l.load(s, vInt), objTagOff, l.constant(0))
	l.pokeWord(l.load(s, vInt), objNextOff, l.peekWord(l.load(headAt, vInt), 0))
	l.pokeWord(l.load(headAt, vInt), 0, l.load(s, vInt))
	l.mark(next)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(s, vInt), l.load(bs, vInt)), Dst: NoReg, Imm: s})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: slots})
	l.mark(paged)

	l.emit(Instr{Op: OpStore, A: l.peekWord(l.load(page, vInt), pgNextOff), Dst: NoReg, Imm: page})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: pages})
	l.mark(swept)

	l.rtStore(gcLiveSlot, l.arith(OpSub, l.rtLoad(gcLiveSlot), l.load(deadN, vInt)))
	l.rtStore(gcBytesSlot, l.arith(OpSub, l.rtLoad(gcBytesSlot), l.load(deadB, vInt)))
}

// heapFree puts one object of a page back on its free list, for delete
// in a program that manages its own memory. raw is its header.
func (l *lowerer) heapFree(raw, page Reg) {
	rs := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: raw, Dst: NoReg, Imm: rs})
	headAt := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.rtSlot(heapFreeSlot),
		l.arith(OpShl, l.peekWord(page, pgClassOff), l.constant(3))), Dst: NoReg, Imm: headAt})
	locked := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.rtLoad(gcTasksSlot), Dst: NoReg, Imm: locked})
	alone := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(locked, vInt), l.constant(0)), Dst: NoReg, Imm: alone})
	l.spinLock()
	l.mark(alone)
	l.pokeWord(l.load(rs, vInt), objTagOff, l.constant(0))
	l.pokeWord(l.load(rs, vInt), objNextOff, l.peekWord(l.load(headAt, vInt), 0))
	l.pokeWord(l.load(headAt, vInt), 0, l.load(rs, vInt))
	done := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(locked, vInt), l.constant(0)), Dst: NoReg, Imm: done})
	l.spinUnlock()
	l.mark(done)
}
