package main

import "os"

// Collector support for data blocks that hold structs inline.
//
// A list of structs that keeps its elements in the data block has two
// needs the collector did not have. The block is a run of equal
// elements whose first nptr words are pointers (tagStructs, see
// struct.go), so marking is a loop inside a loop. And code that works on
// an element in place holds a pointer into the middle of the block, which
// may be the only reference to it, so a word on a stack has to be able to
// keep an object alive without being its address.
//
// All of it is behind lowerer.gcInline. A program that never builds such a
// block does not get the code, so the collector and the binary stay the
// size they were.
//
// Only stack words may be interior pointers. A word in the heap is
// either an object's address exactly or not a pointer, and the precise
// half of the collector keeps relying on that.

// gcInlineWanted reports whether this build emits the support: inline
// struct lists need it, because the loop variable of `for p in ps` and
// a method's self point into an element block, and the test hook asks
// for it on its own.
func gcInlineWanted() bool {
	return inlineOn || os.Getenv("SCANDIACUS_TESTHOOKS") != ""
}

// gcBlock lowers mem.gcblock(nptr, stride, count): a zeroed data block of
// count elements of stride words, the first nptr words of each a
// pointer, allocated the way an inline list will allocate one. It returns
// the address as an int, which the conservative stack scan treats as any
// other word. The collector tests need a block with a descriptor before
// the lowering that builds real ones exists.
func (l *lowerer) gcBlock(c *Call) Reg {
	if !l.gcInline {
		l.errorAt(c, "mem.gcblock is a hook for the compiler's own tests; set SCANDIACUS_TESTHOOKS=1")
		return l.junk()
	}
	if len(c.Args) != 3 {
		l.errorAt(c, "mem.gcblock takes nptr, stride and count")
		return l.junk()
	}
	nptr, stride, count := l.intArg(c, 0), l.intArg(c, 1), l.intArg(c, 2)
	bytes := l.arith(OpMul, l.arith(OpMul, stride, count), l.constant(wordSize))
	header := l.arith(OpBOr,
		l.arith(OpBOr,
			l.arith(OpShl, bytes, l.constant(structsSizeShift)),
			l.arith(OpShl, stride, l.constant(structsStrideShift))),
		l.arith(OpBOr, l.arith(OpShl, nptr, l.constant(structsNPtrShift)), l.constant(tagStructs)))
	bytesS := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: bytes, Dst: NoReg, Imm: bytesS})
	obj := l.allocTagged(l.load(bytesS, vInt), header)
	// A slot reused from a free list holds whatever its last object did,
	// and the collector follows the pointer words of every element.
	l.ccall("memset", []Reg{obj, l.constant(0), l.load(bytesS, vInt)},
		[]vty{vInt, vInt, vInt}, vInt, false, false)
	// The hook is declared to return an int, and allocTagged now gives a
	// raw pointer; the stack scan is conservative, so the int copy still
	// keeps the block alive.
	addr := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: obj, Dst: NoReg, Imm: addr})
	return l.load(addr, vInt)
}

// traceStructs offers the pointer words of every element of an inline
// block. A capacity past the length is zero words, which tryMarkBody
// turns away at once.
func (l *lowerer) traceStructs(obj Reg, hdrSlot int64, table, mask, work, workLen Reg) {
	h := func() Reg { return l.load(hdrSlot, vInt) }
	nptr := l.arith(OpBAnd, l.arith(OpShr, h(), l.constant(structsNPtrShift)), l.constant(0xFF))
	stride := l.arith(OpMul,
		l.arith(OpBAnd, l.arith(OpShr, h(), l.constant(structsStrideShift)), l.constant(0xFFFF)),
		l.constant(wordSize))
	size := l.arith(OpBAnd, l.arith(OpShr, h(), l.constant(structsSizeShift)), l.constant(0xFFFFFFFF))

	nptrS, strideS, sizeS, objS := l.temp(vInt), l.temp(vInt), l.temp(vInt), l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: nptr, Dst: NoReg, Imm: nptrS})
	l.emit(Instr{Op: OpStore, A: stride, Dst: NoReg, Imm: strideS})
	l.emit(Instr{Op: OpStore, A: size, Dst: NoReg, Imm: sizeS})
	l.emit(Instr{Op: OpStore, A: obj, Dst: NoReg, Imm: objS})

	done := l.newLabel()
	// No pointers, or a stride that would never advance, leaves nothing
	// to walk.
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(nptrS, vInt), l.constant(0)), Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(strideS, vInt), l.constant(0)), Dst: NoReg, Imm: done})

	at := l.temp(vInt) // byte offset of the current element
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: at})
	elems := l.newLabel()
	l.mark(elems)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGt,
		l.arith(OpAdd, l.load(at, vInt), l.load(strideS, vInt)), l.load(sizeS, vInt)),
		Dst: NoReg, Imm: done})

	k := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: k})
	words := l.newLabel()
	next := l.newLabel()
	l.mark(words)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe, l.load(k, vInt), l.load(nptrS, vInt)), Dst: NoReg, Imm: next})
	child := l.peekWord(l.arith(OpAdd, l.load(objS, vInt),
		l.arith(OpAdd, l.load(at, vInt), l.arith(OpMul, l.load(k, vInt), l.constant(wordSize)))), 0)
	l.tryMarkBody(child, table, mask, work, workLen)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(k, vInt), l.constant(1)), Dst: NoReg, Imm: k})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: words})
	l.mark(next)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(at, vInt), l.load(strideS, vInt)), Dst: NoReg, Imm: at})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: elems})
	l.mark(done)
}

// structsSize is the payload size out of a tagStructs header word.
func (l *lowerer) structsSize(header Reg) Reg {
	return l.arith(OpBAnd, l.arith(OpShr, header, l.constant(structsSizeShift)), l.constant(0xFFFFFFFF))
}

// The interior scan keeps three words where the worklist length lives:
// the length, then the sorted payload addresses of the objects that are
// not in a page, and how many there are.
const (
	interiorArrOff = wordSize
	interiorLenOff = 2 * wordSize
)

// tryMarkInterior is tryMark for a word from a stack, which may point
// anywhere inside an object. Exact addresses are looked up as before;
// otherwise a page's slot is found by division and a large object by
// binary search of the sorted array.
func (l *lowerer) tryMarkInterior(candidate, table, mask, work, workLen Reg) {
	ts := []vty{vInt, vInt, vInt, vInt, vInt}
	sym := l.helperFunc("__gc_tryi", ts, vVoid, func(a []Reg) {
		l.tryMarkInteriorBody(a[0], a[1], a[2], a[3], a[4])
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
	l.callHelper(sym, []Reg{candidate, table, mask, work, workLen}, ts, vVoid)
}

func (l *lowerer) tryMarkInteriorBody(candidate, table, mask, work, workLen Reg) {
	done := l.newLabel()
	cand := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: candidate, Dst: NoReg, Imm: cand})
	rawSlot := l.temp(vInt)
	payload := l.temp(vInt)

	// Small numbers and the null word are most of a stack.
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpLt, l.load(cand, vInt), l.constant(pageBytes)), Dst: NoReg, Imm: done})

	page := l.heapPage(l.load(cand, vInt))
	pageS := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: page, Dst: NoReg, Imm: pageS})
	large := l.newLabel()
	found := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(pageS, vInt), l.constant(0)), Dst: NoReg, Imm: large})

	// In a page. The word is inside slot q if it is past that slot's two
	// header words; one inside the header is not a pointer to the object.
	first := l.arith(OpAdd, l.load(pageS, vInt), l.constant(pageHeader))
	off := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, l.load(cand, vInt), first), Dst: NoReg, Imm: off})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpLt, l.load(off, vInt), l.constant(0)), Dst: NoReg, Imm: done})
	bs := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.peekWord(l.load(pageS, vInt), pgBlockOff), Dst: NoReg, Imm: bs})
	q := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpDiv, l.load(off, vInt), l.load(bs, vInt)), Dst: NoReg, Imm: q})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe, l.load(q, vInt),
		l.peekWord(l.load(pageS, vInt), pgSlotsOff)), Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd,
		l.arith(OpAdd, l.load(pageS, vInt), l.constant(pageHeader)),
		l.arith(OpMul, l.load(q, vInt), l.load(bs, vInt))), Dst: NoReg, Imm: rawSlot})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpLt, l.load(cand, vInt),
		l.arith(OpAdd, l.load(rawSlot, vInt), l.constant(objHeader))), Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq,
		l.arith(OpBAnd, l.peekWord(l.load(rawSlot, vInt), objNextOff), l.constant(objLiveBit)),
		l.constant(0)), Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(rawSlot, vInt), l.constant(objHeader)),
		Dst: NoReg, Imm: payload})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: found})

	// Not in a page: an exact address on the object list, else inside the
	// last big object that starts below the word.
	l.mark(large)
	search := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.tableHas(table, mask, l.load(cand, vInt)), Dst: NoReg, Imm: search})
	l.emit(Instr{Op: OpStore, A: l.load(cand, vInt), Dst: NoReg, Imm: payload})
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, l.load(cand, vInt), l.constant(objHeader)), Dst: NoReg, Imm: rawSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: found})

	l.mark(search)
	arr := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.peekWord(workLen, interiorArrOff), Dst: NoReg, Imm: arr})
	lo, hi := l.temp(vInt), l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: lo})
	l.emit(Instr{Op: OpStore, A: l.peekWord(workLen, interiorLenOff), Dst: NoReg, Imm: hi})
	// lo ends as the count of addresses at or below the word.
	top := l.newLabel()
	bound := l.newLabel()
	l.mark(top)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe, l.load(lo, vInt), l.load(hi, vInt)), Dst: NoReg, Imm: bound})
	mid := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpShr, l.arith(OpAdd, l.load(lo, vInt), l.load(hi, vInt)), l.constant(1)),
		Dst: NoReg, Imm: mid})
	atMid := l.peekWord(l.arith(OpAdd, l.load(arr, vInt), l.arith(OpMul, l.load(mid, vInt), l.constant(wordSize))), 0)
	right := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpLe, atMid, l.load(cand, vInt)), Dst: NoReg, Imm: right})
	l.emit(Instr{Op: OpStore, A: l.load(mid, vInt), Dst: NoReg, Imm: hi})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(right)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(mid, vInt), l.constant(1)), Dst: NoReg, Imm: lo})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(bound)

	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(lo, vInt), l.constant(0)), Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpStore, A: l.peekWord(l.arith(OpAdd, l.load(arr, vInt),
		l.arith(OpMul, l.arith(OpSub, l.load(lo, vInt), l.constant(1)), l.constant(wordSize))), 0),
		Dst: NoReg, Imm: payload})
	size := l.jsonObjectSize(l.peekWord(l.arith(OpSub, l.load(payload, vInt), l.constant(objHeader)), objTagOff))
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe, l.load(cand, vInt),
		l.arith(OpAdd, l.load(payload, vInt), size)), Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, l.load(payload, vInt), l.constant(objHeader)), Dst: NoReg, Imm: rawSlot})

	l.mark(found)
	l.markAndPush(rawSlot, func() Reg { return l.load(payload, vInt) }, work, workLen, done)
	l.mark(done)
}

// sortInterior sorts the payload addresses of the objects that
// are not in a page, gathered by the walk that fills the hash table, and
// leaves the array and its length where tryMarkInterior looks.
func (l *lowerer) sortInterior(workLen, arr, n Reg) {
	sym := l.helperFunc("__gc_sort", []vty{vInt, vInt}, vVoid, func(a []Reg) {
		l.heapSort(a[0], a[1])
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
	l.callHelper(sym, []Reg{arr, n}, []vty{vInt, vInt}, vVoid)
	l.pokeWord(workLen, interiorArrOff, arr)
	l.pokeWord(workLen, interiorLenOff, n)
}

// heapSort sorts n words in place. It is a heap sort because it needs no
// scratch memory and no recursion, and the collector may use neither.
func (l *lowerer) heapSort(arr, n Reg) {
	sift := l.helperFunc("__gc_sift", []vty{vInt, vInt, vInt}, vVoid, func(a []Reg) {
		arrS, endS, root, child := l.temp(vInt), l.temp(vInt), l.temp(vInt), l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: a[0], Dst: NoReg, Imm: arrS})
		l.emit(Instr{Op: OpStore, A: a[1], Dst: NoReg, Imm: root})
		l.emit(Instr{Op: OpStore, A: a[2], Dst: NoReg, Imm: endS})
		at := func(i Reg) Reg {
			return l.arith(OpAdd, l.load(arrS, vInt), l.arith(OpMul, i, l.constant(wordSize)))
		}
		top := l.newLabel()
		out := l.newLabel()
		l.mark(top)
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.arith(OpMul, l.load(root, vInt), l.constant(2)), l.constant(1)),
			Dst: NoReg, Imm: child})
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe, l.load(child, vInt), l.load(endS, vInt)), Dst: NoReg, Imm: out})
		single := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe, l.arith(OpAdd, l.load(child, vInt), l.constant(1)),
			l.load(endS, vInt)), Dst: NoReg, Imm: single})
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe,
			l.peekWord(at(l.load(child, vInt)), 0),
			l.peekWord(at(l.arith(OpAdd, l.load(child, vInt), l.constant(1))), 0)), Dst: NoReg, Imm: single})
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(child, vInt), l.constant(1)), Dst: NoReg, Imm: child})
		l.mark(single)
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe,
			l.peekWord(at(l.load(root, vInt)), 0),
			l.peekWord(at(l.load(child, vInt)), 0)), Dst: NoReg, Imm: out})
		l.swapMemWords(at(l.load(root, vInt)), at(l.load(child, vInt)))
		l.emit(Instr{Op: OpStore, A: l.load(child, vInt), Dst: NoReg, Imm: root})
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
		l.mark(out)
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
	ts := []vty{vInt, vInt, vInt}

	arrS, nS, i := l.temp(vInt), l.temp(vInt), l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: arr, Dst: NoReg, Imm: arrS})
	l.emit(Instr{Op: OpStore, A: n, Dst: NoReg, Imm: nS})
	done := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpLt, l.load(nS, vInt), l.constant(2)), Dst: NoReg, Imm: done})

	// Heapify: sift down every parent, last first.
	l.emit(Instr{Op: OpStore, A: l.arith(OpShr, l.arith(OpSub, l.load(nS, vInt), l.constant(2)), l.constant(1)),
		Dst: NoReg, Imm: i})
	build := l.newLabel()
	built := l.newLabel()
	l.mark(build)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpLt, l.load(i, vInt), l.constant(0)), Dst: NoReg, Imm: built})
	l.callHelper(sift, []Reg{l.load(arrS, vInt), l.load(i, vInt), l.load(nS, vInt)}, ts, vVoid)
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, l.load(i, vInt), l.constant(1)), Dst: NoReg, Imm: i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: build})
	l.mark(built)

	// Move the largest to the end, shrink the heap by one, repeat.
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, l.load(nS, vInt), l.constant(1)), Dst: NoReg, Imm: i})
	drain := l.newLabel()
	l.mark(drain)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpLe, l.load(i, vInt), l.constant(0)), Dst: NoReg, Imm: done})
	l.swapMemWords(l.load(arrS, vInt),
		l.arith(OpAdd, l.load(arrS, vInt), l.arith(OpMul, l.load(i, vInt), l.constant(wordSize))))
	l.callHelper(sift, []Reg{l.load(arrS, vInt), l.constant(0), l.load(i, vInt)}, ts, vVoid)
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, l.load(i, vInt), l.constant(1)), Dst: NoReg, Imm: i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: drain})
	l.mark(done)
}

// swapMemWords exchanges the words at two addresses.
func (l *lowerer) swapMemWords(a, b Reg) {
	as, bs, t := l.temp(vInt), l.temp(vInt), l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: a, Dst: NoReg, Imm: as})
	l.emit(Instr{Op: OpStore, A: b, Dst: NoReg, Imm: bs})
	l.emit(Instr{Op: OpStore, A: l.peekWord(l.load(as, vInt), 0), Dst: NoReg, Imm: t})
	l.pokeWord(l.load(as, vInt), 0, l.peekWord(l.load(bs, vInt), 0))
	l.pokeWord(l.load(bs, vInt), 0, l.load(t, vInt))
}
