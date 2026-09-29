package main

// Lists, built out of the raw memory ops rather than hand-written
// assembly.
//
// A list value is a pointer to a three-word header:
//
//	[ptr+0]   length
//	[ptr+8]   capacity
//	[ptr+16]  pointer to the elements
//
// The header is allocated separately from the elements so that growing
// the list replaces the element block without the header pointer
// changing. If the elements lived inline, every push that grew the list
// would invalidate every copy of the list value that anyone held, which
// is the kind of bug that shows up months later in one program.
//
// Every element is eight bytes, whether it is an int, a bool or a
// pointer to a string. That uniformity is what lets one implementation
// serve every element type without generics or monomorphisation.
//
// None of this is ever freed. A list that goes out of scope leaks its
// header and its elements. That is the state of a backend with no
// collector, and it is the single largest thing between here and the Go
// backend.

const (
	listLenOff  = 0
	listCapOff  = 8
	listDataOff = 16
	listHeader  = 24
	wordSize    = 8
)

// ---- the object header ----
//
// Every heap allocation carries one word in front of it saying what it
// is and how big it is:
//
//	[ptr-8]   size in bytes << 8 | tag
//	[ptr+0]   the payload, which is what every caller sees
//
// There is no collector yet. This exists so that there can be one.
//
// A collector has to answer one question about every word it finds: is
// this a pointer to trace, or an integer that happens to look like one?
// Every value here is an anonymous eight bytes, so nothing in the heap
// could answer that. The tag answers it for a whole block at once - the
// bytes of a string are never scanned, the elements of a []int are
// never scanned, and the elements of a []str always are.
//
// Putting the word before the payload rather than after the other
// fields is what kept this cheap: every offset already in this file and
// in strings.go still measures from the same place, so nothing above
// the allocator changed.
//
// The size is recorded because a collector that moves or frees an
// object needs to know how much of it there is, and the allocator is
// the only place that still knows.
const (
	// Two words in front of every object: the tag and size, then the
	// link that threads every allocation onto one list.
	//
	// The list is what makes the heap walkable. A collector has to be
	// able to visit everything, and there is no other way to find an
	// object here - malloc does not hand back an enumeration and this
	// compiler does not allocate out of its own pages.
	//
	// The link's low bit is the mark. malloc returns memory aligned to
	// at least 16 bytes, so bit 0 of a pointer to a header is always
	// zero and is free to borrow.
	objHeader  = 16
	objTagOff  = 0
	objNextOff = 8
	objMarkBit = 1

	tagShift = 8

	tagBytes = 0 // raw bytes, never scanned: the characters of a string
	tagWords = 1 // eight-byte slots holding no pointers: []int, []float
	tagPtrs  = 2 // eight-byte slots, every one a pointer: []str
	tagList  = 3 // a list header: len, cap, and a pointer to the elements
	tagMap   = 4 // a map header: len, cap, and pointers to keys and values
)

// elemTag reports how the element block of a list must be treated.
// One level deep is all the vty tracks, which is all this needs: a
// list of strings holds pointers, a list of anything else does not.
func elemTag(t vty) int64 {
	if t.elemType().holdsPointer() {
		return tagPtrs
	}
	return tagWords
}

// allocObj allocates a tagged object and returns a pointer to its
// payload. The header is written in the IR rather than inside the
// runtime alloc helper, so that x64.go stays a translator and the
// layout stays visible in `veyl ir`.
func (l *lowerer) allocObj(bytes Reg, tag int64) Reg {
	// tag is below 256 and the size is shifted past it, so no bit of one
	// can reach the other and an add is an or.
	word := l.arith(OpAdd, l.arith(OpShl, bytes, l.constant(tagShift)), l.constant(tag))
	return l.allocTagged(bytes, word)
}

// allocTagged is allocObj for an object whose header word is worked out
// by the caller, which is what a struct and a JSON node need.
//
// Every allocation goes through here, and every one is threaded onto the
// object list before it is handed back. Missing that is not a leak, it
// is a live object the collector cannot see.
//
// It is one function called from every site rather than written out at
// each: the malloc, the header, the list link, the counters and the
// thread check come to about fifty instructions, and a program that
// allocates in eight hundred places was carrying eight hundred copies.
// The call costs nothing next to malloc itself.
func (l *lowerer) allocTagged(bytes, header Reg) Reg {
	sym := l.helperFunc("__alloc", []vty{vInt, vInt}, vInt, func(a []Reg) {
		raw := l.allocRaw(l.arith(OpAdd, a[0], l.constant(objHeader)))
		l.emit(Instr{Op: OpStoreMem, A: raw, B: a[1], Imm: objTagOff})
		obj := l.arith(OpAdd, raw, l.constant(objHeader))
		l.trackObject(raw, obj, a[0])
		l.emit(Instr{Op: OpRet, A: obj, Dst: NoReg})
	})
	return l.callHelper(sym, []Reg{bytes, header}, []vty{vInt, vInt}, vInt)
}

// initialCap is what an empty list grows to on its first push. Four is
// small enough not to waste much on the many short lists a program
// makes, and large enough that a handful of pushes do not each realloc.
const initialCap = 4

// newList allocates a header and an element block, and returns the
// header pointer.
func (l *lowerer) newList(t vty, capacity int64) Reg {
	if capacity < 1 {
		capacity = 1
	}

	hdr := l.allocObj(l.constant(listHeader), tagList)
	l.regTy[hdr] = t

	data := l.allocObj(l.constant(capacity*wordSize), elemTag(t))

	l.emit(Instr{Op: OpStoreMem, A: hdr, B: l.constant(0), Imm: listLenOff})
	l.emit(Instr{Op: OpStoreMem, A: hdr, B: l.constant(capacity), Imm: listCapOff})
	l.emit(Instr{Op: OpStoreMem, A: hdr, B: data, Imm: listDataOff})
	return hdr
}

func (l *lowerer) constant(n int64) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpConst, Dst: d, A: NoReg, B: NoReg, Imm: n})
	return d
}

// allocRaw is the untagged allocation. Only allocObj should call it:
// an untagged block is one a collector cannot describe.
func (l *lowerer) allocRaw(bytes Reg) Reg {
	l.mod.needs("alloc")
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpAlloc, Dst: d, A: bytes, B: NoReg})
	return d
}

// field reads one word out of the header.
func (l *lowerer) field(list Reg, off int64, t vty) Reg {
	d := l.newReg()
	l.regTy[d] = t
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: list, B: NoReg, Imm: off})
	return d
}

func (l *lowerer) arith(op Op, a, b Reg) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: op, Dst: d, A: a, B: b})
	return d
}

func (l *lowerer) compare(op Op, a, b Reg) Reg {
	d := l.newReg()
	l.regTy[d] = vBool
	l.emit(Instr{Op: op, Dst: d, A: a, B: b})
	return d
}

// listPush appends one element, growing the element block when it is
// full. Growth doubles, so a list built by repeated pushes costs
// amortised constant time rather than quadratic copying.
func (l *lowerer) listPush(list, val Reg) {
	length := l.field(list, listLenOff, vInt)
	capacity := l.field(list, listCapOff, vInt)

	ready := l.newLabel()
	room := l.compare(OpLt, length, capacity)
	l.emit(Instr{Op: OpJumpIf, A: room, Dst: NoReg, Imm: ready, Comment: "push: room?"})

	// Growing. The new capacity goes through a slot because two branches
	// decide it, which is exactly what a virtual register cannot express
	// while every one of them has a single definition site.
	capSlot := l.temp(vInt)
	doubled := l.arith(OpMul, capacity, l.constant(2))
	l.emit(Instr{Op: OpStore, A: doubled, Dst: NoReg, Imm: capSlot})

	haveCap := l.newLabel()
	big := l.compare(OpGe, doubled, l.constant(initialCap))
	l.emit(Instr{Op: OpJumpIf, A: big, Dst: NoReg, Imm: haveCap})
	l.emit(Instr{Op: OpStore, A: l.constant(initialCap), Dst: NoReg, Imm: capSlot})
	l.mark(haveCap)

	newCap := l.newReg()
	l.regTy[newCap] = vInt
	l.emit(Instr{Op: OpLoad, Dst: newCap, A: NoReg, B: NoReg, Imm: capSlot})

	fresh := l.allocObj(l.arith(OpMul, newCap, l.constant(wordSize)), elemTag(l.regTy[list]))
	old := l.field(list, listDataOff, vInt)

	// Copy the old elements one word at a time. memcpy would be faster
	// and would mean another external symbol; this is a loop the byte
	// writer already knows how to emit.
	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})
	top := l.newLabel()
	copied := l.newLabel()
	l.mark(top)
	i := l.newReg()
	l.regTy[i] = vInt
	l.emit(Instr{Op: OpLoad, Dst: i, A: NoReg, B: NoReg, Imm: iSlot})
	more := l.compare(OpLt, i, length)
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: copied})

	from := l.newReg()
	l.regTy[from] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: from, A: old, B: i})
	v := l.newReg()
	l.regTy[v] = vInt
	l.emit(Instr{Op: OpLoadMem, Dst: v, A: from, B: NoReg, Imm: 0})
	to := l.newReg()
	l.regTy[to] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: to, A: fresh, B: i})
	l.emit(Instr{Op: OpStoreMem, A: to, B: v, Imm: 0})

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(copied)

	l.emit(Instr{Op: OpStoreMem, A: list, B: newCap, Imm: listCapOff})
	l.emit(Instr{Op: OpStoreMem, A: list, B: fresh, Imm: listDataOff})
	if l.gcOff {
		l.freeObject(old) // no collector will; see gcoff.go
	}

	l.mark(ready)

	// The length and the data pointer are re-read here rather than
	// reused from above: the growth path may have replaced both.
	at := l.field(list, listLenOff, vInt)
	data := l.field(list, listDataOff, vInt)
	addr := l.newReg()
	l.regTy[addr] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: addr, A: data, B: at})
	l.emit(Instr{Op: OpStoreMem, A: addr, B: val, Imm: 0})
	l.emit(Instr{Op: OpStoreMem, A: list, B: l.arith(OpAdd, at, l.constant(1)),
		Imm: listLenOff})
}

// elemAddr bounds-checks an index and returns the address of that
// element. Out of range exits with the same sentence the Go backend
// prints, because the differential test compares stderr too.
func (l *lowerer) elemAddr(list, idx Reg) Reg {
	length := l.field(list, listLenOff, vInt)

	ok := l.newLabel()
	bad := l.newLabel()

	negative := l.compare(OpLt, idx, l.constant(0))
	l.emit(Instr{Op: OpJumpIf, A: negative, Dst: NoReg, Imm: bad, Comment: "bounds"})
	tooBig := l.compare(OpGe, idx, length)
	l.emit(Instr{Op: OpJumpIf, A: tooBig, Dst: NoReg, Imm: bad})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: ok})

	l.mark(bad)
	l.mod.needs("bounds")
	l.emit(Instr{Op: OpBoundsFail, A: idx, B: length, Dst: NoReg, Imm: l.where()})

	l.mark(ok)
	data := l.field(list, listDataOff, vInt)
	addr := l.newReg()
	l.regTy[addr] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: addr, A: data, B: idx})
	return addr
}

func (l *lowerer) listGet(list, idx Reg, elem vty) Reg {
	addr := l.elemAddr(list, idx)
	d := l.newReg()
	l.regTy[d] = elem
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: addr, B: NoReg, Imm: 0})
	return d
}

func (l *lowerer) listSet(list, idx, val Reg) {
	addr := l.elemAddr(list, idx)
	l.emit(Instr{Op: OpStoreMem, A: addr, B: val, Imm: 0})
}

// printList writes a list the way the Go backend does: [1, 2, 3], with
// strings quoted. Built from the write ops rather than a runtime helper,
// so it costs no assembly of its own.
func (l *lowerer) writeList(n Node, list Reg, t vty) {
	l.mod.needs("write")
	l.writeLit("[")

	length := l.field(list, listLenOff, vInt)
	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	i := l.newReg()
	l.regTy[i] = vInt
	l.emit(Instr{Op: OpLoad, Dst: i, A: NoReg, B: NoReg, Imm: iSlot})
	more := l.compare(OpLt, i, length)
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})

	noComma := l.newLabel()
	firstOne := l.compare(OpEq, i, l.constant(0))
	l.emit(Instr{Op: OpJumpIf, A: firstOne, Dst: NoReg, Imm: noComma})
	l.writeLit(", ")
	l.mark(noComma)

	addr := l.newReg()
	l.regTy[addr] = vInt
	data := l.field(list, listDataOff, vInt)
	l.emit(Instr{Op: OpIndexAddr, Dst: addr, A: data, B: i})
	v := l.newReg()
	l.regTy[v] = t.elemType()
	l.emit(Instr{Op: OpLoadMem, Dst: v, A: addr, B: NoReg, Imm: 0})

	l.writeValue(n, v, t.elemType())

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(done)

	l.writeLit("]")
}

// printList is the same list on a line of its own. The two are
// separate because a list nested inside a printed struct must not
// end the line.
func (l *lowerer) printList(n Node, v Reg, t vty) {
	l.writeValue(n, v, t)
	l.writeLit("\n")
}

// writeValue writes one element the way it appears inside a printed
// list, map or struct: strings quoted, bools as words, everything else
// plain. Shared so that the containers cannot drift apart in how they
// render what they hold.
//
// It takes the whole vty rather than the kind, because a struct element
// needs its name to render and a kind cannot carry one.
func (l *lowerer) writeValue(n Node, v Reg, t vty) {
	if t.null {
		l.writeNull(n, v, t)
		return
	}
	if l.renderOutlined(n, v, t) {
		return
	}
	switch t.k {
	case kStruct:
		l.writeStruct(n, v, t)
	case kList:
		l.writeList(n, v, t)
	case kMap:
		l.writeMap(n, v, t)
	case kBytes:
		l.renderBytes(v)
	case kStr:
		l.writeLit("\"")
		l.emitStr(l.quoteEscape(v))
		l.writeLit("\"")
	case kBool:
		l.mod.needs("booltostr")
		s := l.newReg()
		l.regTy[s] = vStr
		l.emit(Instr{Op: OpBoolToStr, Dst: s, A: v, B: NoReg})
		l.emitStr(s)
	case kFloat:
		l.mod.needs("floattostr")
		l.emitFloat(v)
	default:
		if isEnum(t) {
			l.emitStr(l.enumName(v))
			return
		}
		l.emitInt(v)
	}
}

// renderOutlined writes a list, map or struct by calling the function
// that renders its type, made the first time one is needed. Written out
// in place, printing a {str: []int} is a pair of nested loops, and a
// program printing forty of them carried forty copies. It also covers a
// type that holds itself - a tree - which inlined would never end.
//
// There are two per type. __print writes straight to stdout, as the
// inline code did, so printing a long list stays linear. __show builds
// a string, for str() and for a value inside something being built.
func (l *lowerer) renderOutlined(n Node, v Reg, t vty) bool {
	switch t.k {
	case kList, kMap, kStruct:
	default:
		return false
	}
	if t.res || l.isView(t) {
		return false
	}
	key := "__show " + t.String()
	if l.buf < 0 {
		key = "__print " + t.String()
	}
	if l.inlineOnce == key {
		l.inlineOnce = "" // this is the function's own body
		return false
	}
	toStdout := l.buf < 0
	ret := vStr
	if toStdout {
		ret = vVoid
	}
	sym := l.helperFunc(key, []vty{t}, ret, func(a []Reg) {
		l.inlineOnce = key
		if toStdout {
			l.writeValue(n, a[0], t)
			l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
			return
		}
		slot := l.temp(vStr)
		l.emit(Instr{Op: OpStore, A: l.emptyStr(), Dst: NoReg, Imm: slot})
		l.buf = slot
		l.writeValue(n, a[0], t)
		l.emit(Instr{Op: OpRet, A: l.load(slot, vStr), Dst: NoReg})
	})
	out := l.callHelper(sym, []Reg{v}, []vty{t}, ret)
	if !toStdout {
		l.emitStr(out)
	}
	return true
}

// quoteEscape builds the body of a Go strconv.Quote, without the quotes.
//
// A str inside a container is printed quoted, and the Go backend gets
// those quotes from strconv.Quote, so a list holding a newline has to
// print \n on both sides rather than break the line on one of them.
//
// Bytes at 0x80 and up go through untouched. That is what Quote does
// for printable UTF-8, which is all any real string here holds; it
// differs from Quote for invalid UTF-8 and for non-printable runes,
// neither of which this backend can see without a rune decoder.
func (l *lowerer) quoteEscape(s Reg) Reg {
	n := l.strLen(s)
	// Worst case four bytes, \xNN for a control character.
	buf := l.strAlloc(l.arith(OpMul, n, l.constant(4)))

	wSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: wSlot})

	put := func(b Reg) {
		w := l.load(wSlot, vInt)
		l.storeByte(buf, w, b)
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, w, l.constant(1)),
			Dst: NoReg, Imm: wSlot})
	}
	escape := func(c int64) {
		put(l.constant('\\'))
		put(l.constant(c))
	}

	l.eachByte(s, n, func(i Reg) {
		b := l.loadByte(s, i)
		done := l.newLabel()

		// The named escapes, the set and the spelling Quote uses.
		for _, pair := range []struct {
			from int64
			to   int64
		}{
			{'"', '"'}, {'\\', '\\'}, {'\a', 'a'}, {'\b', 'b'}, {'\f', 'f'},
			{'\n', 'n'}, {'\r', 'r'}, {'\t', 't'}, {'\v', 'v'},
		} {
			next := l.newLabel()
			l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, b, l.constant(pair.from)),
				Dst: NoReg, Imm: next})
			escape(pair.to)
			l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
			l.mark(next)
		}

		// Anything else below a space, and DEL, goes out as \xNN.
		hex := l.newLabel()
		plain := l.newLabel()
		needsHex := l.logicalOr(
			l.compare(OpLt, b, l.constant(0x20)),
			l.compare(OpEq, b, l.constant(0x7f)))
		l.emit(Instr{Op: OpJumpIf, A: needsHex, Dst: NoReg, Imm: hex})
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: plain})

		l.mark(hex)
		put(l.constant('\\'))
		put(l.constant('x'))
		put(l.hexDigitLower(l.arith(OpShr, b, l.constant(4))))
		put(l.hexDigitLower(l.arith(OpBAnd, b, l.constant(15))))
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

		l.mark(plain)
		put(b)

		l.mark(done)
	})

	l.storeByte(buf, l.load(wSlot, vInt), l.constant(0))
	return buf
}

// emptyStr is the interned "", used as the zero value for a str.
func (l *lowerer) emptyStr() Reg {
	r := l.newReg()
	l.regTy[r] = vStr
	l.emit(Instr{Op: OpStr, Dst: r, A: NoReg, B: NoReg, Imm: l.mod.intern("")})
	return r
}

func (l *lowerer) writeLit(s string) {
	r := l.newReg()
	l.regTy[r] = vStr
	l.emit(Instr{Op: OpStr, Dst: r, A: NoReg, B: NoReg, Imm: l.mod.intern(s)})
	l.emitStr(r)
}

// emitStr, emitInt and emitFloat write one piece to wherever writes are
// currently going: stdout, or the buffer str() set up.
//
// Everything that renders a container goes through these three, which
// is what makes str(xs) and print(xs) produce the same characters by
// construction rather than by two pieces of code being kept in step.
func (l *lowerer) emitStr(v Reg) {
	if l.buf < 0 {
		l.emit(Instr{Op: OpWriteStr, A: v, Dst: NoReg})
		return
	}
	// Append by concatenation, reloading the buffer each time, because
	// the writes happen inside loops whose bodies run an unknown number
	// of times and a register cannot carry a value across that.
	l.mod.needs("concat")
	cur := l.newReg()
	l.regTy[cur] = vStr
	l.emit(Instr{Op: OpLoad, Dst: cur, A: NoReg, B: NoReg, Imm: l.buf})
	d := l.newReg()
	l.regTy[d] = vStr
	l.emit(Instr{Op: OpConcat, Dst: d, A: cur, B: v})
	l.emit(Instr{Op: OpStore, A: d, Dst: NoReg, Imm: l.buf})
}

func (l *lowerer) emitInt(v Reg) {
	if l.buf < 0 {
		l.emit(Instr{Op: OpWriteInt, A: v, Dst: NoReg})
		return
	}
	l.mod.needs("inttostr")
	d := l.newReg()
	l.regTy[d] = vStr
	l.emit(Instr{Op: OpIntToStr, Dst: d, A: v, B: NoReg})
	l.emitStr(d)
}

func (l *lowerer) emitFloat(v Reg) {
	if l.buf < 0 {
		l.emit(Instr{Op: OpWriteFloat, A: v, Dst: NoReg})
		return
	}
	l.mod.needs("floattostr")
	d := l.newReg()
	l.regTy[d] = vStr
	l.emit(Instr{Op: OpFloatToStr, Dst: d, A: v, B: NoReg})
	l.emitStr(d)
}

// strOf renders a list, map or struct into a string.
//
// It is the printing code with its output redirected, so the two cannot
// disagree. The cost is one allocation per piece appended, which for a
// long list is quadratic and, with nothing freed, permanent. A rope or
// a growable buffer is the fix when that matters; correctness first.
func (l *lowerer) strOf(n Node, v Reg, t vty) Reg {
	slot := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: l.emptyStr(), Dst: NoReg, Imm: slot})

	saved := l.buf
	l.buf = slot
	l.writeValue(n, v, t)
	l.buf = saved

	d := l.newReg()
	l.regTy[d] = vStr
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: slot})
	return d
}

// forList lowers `for x in xs`.
//
// The length is read once, before the first iteration, matching Go's
// range: pushing onto the list inside the loop does not make it run
// longer. The list itself is held in a slot so that the loop reads the
// same list every time even if the variable it came from is reassigned.
func (l *lowerer) forList(st *ForStmt) {
	coll := l.expr(st.Coll)
	t := l.regTy[coll]
	if t.k == kMap {
		l.forMap(st, coll, t)
		return
	}
	if t.k != kList {
		l.errorAt(st, "only a list or a map can be iterated on this backend so far")
		return
	}
	l.pushScope()

	listSlot := l.temp(t)
	l.emit(Instr{Op: OpStore, A: coll, Dst: NoReg, Imm: listSlot, Comment: "for ... in"})

	held := l.newReg()
	l.regTy[held] = t
	l.emit(Instr{Op: OpLoad, Dst: held, A: NoReg, B: NoReg, Imm: listSlot})
	length := l.field(held, listLenOff, vInt)
	lenSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: length, Dst: NoReg, Imm: lenSlot})

	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	// `for i, v in xs` puts the index in the first name and the element
	// in the second; `for v in xs` binds only the element. The two forms
	// differ in which name gets which, not in how the loop runs.
	idxSlot := int64(-1)
	varName := st.Var
	if st.Var2 != "" {
		idxSlot = l.declare(st.Var, vInt)
		varName = st.Var2
	}
	varSlot := l.declare(varName, t.elemType())

	top := l.newLabel()
	cont := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	i := l.newReg()
	l.regTy[i] = vInt
	l.emit(Instr{Op: OpLoad, Dst: i, A: NoReg, B: NoReg, Imm: iSlot})
	n := l.newReg()
	l.regTy[n] = vInt
	l.emit(Instr{Op: OpLoad, Dst: n, A: NoReg, B: NoReg, Imm: lenSlot})
	more := l.compare(OpLt, i, n)
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})

	list := l.newReg()
	l.regTy[list] = t
	l.emit(Instr{Op: OpLoad, Dst: list, A: NoReg, B: NoReg, Imm: listSlot})
	if idxSlot >= 0 {
		l.emit(Instr{Op: OpStore, A: i, Dst: NoReg, Imm: idxSlot, Comment: st.Var})
	}
	l.emit(Instr{Op: OpStore, A: l.listGet(list, i, t.elemType()), Dst: NoReg,
		Imm: varSlot, Comment: varName})

	l.loops = append(l.loops, loopTarget{brk: done, cont: cont, defers: len(l.defers)})
	l.stmt(st.Body)
	l.loops = l.loops[:len(l.loops)-1]

	// continue goes to the increment, not the test, or the index never
	// advances and the loop never ends.
	l.mark(cont)
	cur := l.newReg()
	l.regTy[cur] = vInt
	l.emit(Instr{Op: OpLoad, Dst: cur, A: NoReg, B: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, cur, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	l.popScope()
}
