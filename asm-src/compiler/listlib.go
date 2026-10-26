package main

// The list library, lowered in the IR.
//
// Every one of these is a loop over an index, so they are written here
// rather than as assembly helpers: the byte writer inherits them, and
// each reads as ordinary control flow rather than as a page of
// instructions. None of them frees anything.
//
// The ones that produce a new list produce a *new* list, matching the Go
// backend, where `sorted` and `reverse` copy rather than mutate in
// place. Getting that backwards does not crash, it makes `sort(xs)`
// silently reorder the original too, which the differential test only
// catches if a program happens to print xs afterwards.

// fatal writes a message to stderr and stops, the way the Go backend's
// list helpers do for an empty list.
//
// stdout is flushed first. The C runtime buffers it and stderr is
// unbuffered, so without the flush the message overtakes everything the
// program already printed, and the two backends' combined output differs
// in order while agreeing line for line.
func (l *lowerer) fatal(msg string) {
	l.ccall("fflush", []Reg{l.constant(0)}, []vty{vInt}, vInt, true, false)
	m := l.strLit("runtime error: " + msg + "\n")
	l.ccall("_write", []Reg{l.constant(2), m, l.strLen(m)},
		[]vty{vInt, vStr, vInt}, vInt, true, false)
	l.ccall("exit", []Reg{l.constant(1)}, []vty{vInt}, vVoid, false, false)
}

// requireNonEmpty stops the program when a list is empty, naming the
// builtin that could not go on.
func (l *lowerer) requireNonEmpty(list Reg, msg string) {
	ok := l.newLabel()
	any := l.compare(OpGt, l.field(list, listLenOff, vInt), l.constant(0))
	l.emit(Instr{Op: OpJumpIf, A: any, Dst: NoReg, Imm: ok})
	l.fatal(msg)
	l.mark(ok)
}

// equalValues compares two elements the way the containing list does.
//
// A list of structs or of containers has no equality here, the same as
// on the Go backend, where slices.Index needs a comparable element.
func (l *lowerer) equalValues(n Node, a, b Reg, t vty, what string) (Reg, bool) {
	switch t.k {
	case kStr:
		return l.strEq(a, b), true
	case kFloat:
		return l.compare(OpFEq, a, b), true
	case kInt, kBool:
		return l.compare(OpEq, a, b), true
	}
	l.errorAt(n, "%s needs a list of numbers, strings or bools, not %s", what, t)
	return NoReg, false
}

// orderable reports whether sort can compare this element type. It is
// separate from lessValues because asking the question must not emit
// any code.
func orderable(t vty) bool {
	switch t.k {
	case kInt, kFloat, kStr:
		return true
	}
	return false
}

// lessValues is equalValues for ordering, used by sort.
func (l *lowerer) lessValues(n Node, a, b Reg, t vty, what string) (Reg, bool) {
	switch t.k {
	case kStr:
		cmp := l.strCmp(a, b)
		return l.compare(OpLt, cmp, l.constant(0)), true
	case kFloat:
		return l.compare(OpFLt, a, b), true
	case kInt:
		return l.compare(OpLt, a, b), true
	}
	l.errorAt(n, "%s needs a list of numbers or strings, not %s", what, t)
	return NoReg, false
}

// eachElement walks a list, calling body with the index and the element.
// Almost everything below is this loop with a different body.
func (l *lowerer) eachElement(list Reg, t vty, body func(i, v Reg)) {
	iSlot := l.scalarTemp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	i := l.load(iSlot, vInt)
	more := l.compare(OpLt, i, l.field(list, listLenOff, vInt))
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})

	body(i, l.listGet(list, i, t.elemType()))

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(iSlot, vInt), l.constant(1)),
		Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
}

// listBuiltin lowers the list library. It reports false for a name it
// does not handle, so the caller can go on looking.
func (l *lowerer) listBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "first", "last", "pop", "sum", "reverse", "sort", "slice",
		"join", "insert", "removeAt":
	case "contains", "indexOf":
		// These take a str as well. Both cases are settled here rather
		// than by falling through to the string library, because the
		// first argument has to be lowered to know which it is, and
		// lowering it in two places would evaluate it twice.
		if len(c.Args) != 2 {
			l.errorAt(c, "%s takes 2 arguments, got %d", name, len(c.Args))
			return l.junk(), true
		}
	default:
		return NoReg, false
	}

	list := l.expr(c.Args[0])
	t := l.regTy[list]

	if t.k == kStr && (name == "contains" || name == "indexOf") {
		at := l.indexOfStr(list, l.expr(c.Args[1]))
		if name == "indexOf" {
			return at, true
		}
		return l.compare(OpGe, at, l.constant(0)), true
	}

	if t.k != kList {
		l.errorAt(c, "%s expects a list, got %s", name, t)
		return l.junk(), true
	}
	elem := t.elemType()

	switch name {
	case "first":
		l.requireNonEmpty(list, "first() on an empty list")
		return l.freshCopy(l.listGet(list, l.constant(0), elem), elem), true

	case "last":
		l.requireNonEmpty(list, "last() on an empty list")
		return l.freshCopy(l.lastOf(list, elem), elem), true

	case "pop":
		// Shortening the list is a store to its length. The element
		// block keeps the value, which nothing can reach again and
		// nothing frees.
		l.requireNonEmpty(list, "pop from an empty list")
		v := l.freshCopy(l.lastOf(list, elem), elem)
		l.emit(Instr{Op: OpStoreMem, A: list,
			B:   l.arith(OpSub, l.field(list, listLenOff, vInt), l.constant(1)),
			Imm: listLenOff})
		return v, true

	case "sum":
		return l.listSum(c, list, t), true

	case "reverse":
		out := l.newList(t, initialCap)
		n := l.field(list, listLenOff, vInt)
		l.eachElement(list, t, func(i, _ Reg) {
			back := l.arith(OpSub, l.arith(OpSub, n, i), l.constant(1))
			l.listPush(out, l.listGet(list, back, elem))
		})
		return out, true

	case "sort":
		return l.listSort(c, list, t), true

	case "slice":
		if len(c.Args) != 3 {
			l.errorAt(c, "slice takes 3 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
		return l.listSlice(list, t, l.expr(c.Args[1]), l.expr(c.Args[2])), true

	case "join":
		if len(c.Args) != 2 {
			l.errorAt(c, "join takes 2 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
		return l.listJoin(c, list, t, l.expr(c.Args[1])), true

	case "insert":
		if len(c.Args) != 3 {
			l.errorAt(c, "insert takes 3 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
		l.listInsert(list, t, l.expr(c.Args[1]), l.rvalueAs(c.Args[2], elem))
		return l.void(), true

	case "removeAt":
		if len(c.Args) != 2 {
			l.errorAt(c, "removeAt takes 2 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
		return l.listRemoveAt(list, t, l.expr(c.Args[1])), true

	case "contains", "indexOf":
		return l.listSearch(c, list, t, name), true
	}
	return NoReg, false
}

// lastOf is xs[len(xs)-1].
func (l *lowerer) lastOf(list Reg, elem vty) Reg {
	back := l.arith(OpSub, l.field(list, listLenOff, vInt), l.constant(1))
	return l.listGet(list, back, elem)
}

func (l *lowerer) listSum(c *Call, list Reg, t vty) Reg {
	elem := t.elemType()
	if elem.k != kInt && elem.k != kFloat {
		l.errorAt(c, "sum needs a list of numbers, not %s", t)
		return l.junk()
	}

	acc := l.temp(elem)
	if elem.k == kFloat {
		l.emit(Instr{Op: OpStore, A: l.floatConst(0), Dst: NoReg, Imm: acc})
	} else {
		l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: acc})
	}

	add := OpAdd
	if elem.k == kFloat {
		add = OpFAdd
	}
	l.eachElement(list, t, func(_, v Reg) {
		sum := l.newReg()
		l.regTy[sum] = elem
		l.emit(Instr{Op: add, Dst: sum, A: l.load(acc, elem), B: v})
		l.emit(Instr{Op: OpStore, A: sum, Dst: NoReg, Imm: acc})
	})
	return l.load(acc, elem)
}

// listSort returns a sorted copy, by insertion. Quadratic, and right for
// the sizes a program written in this language sorts today; the seam for
// something better is this one function.
func (l *lowerer) listSort(c *Call, list Reg, t vty) Reg {
	elem := t.elemType()
	// Checked by kind rather than by trying lessValues on two dummy
	// registers: that probe emitted a real strcmp on two null pointers,
	// which segfaulted before the sort ran a single comparison.
	if !orderable(elem) {
		l.errorAt(c, "sort needs a list of numbers or strings, not %s", t)
		return l.junk()
	}

	out := l.listSlice(list, t, l.constant(0), l.field(list, listLenOff, vInt))
	l.mergeSortList(out, t, func(x, y Reg) Reg {
		less, _ := l.lessValues(c, x, y, elem, "sort")
		return less
	})
	return out
}

// listSlice is xs[start:end] with the indexes clamped rather than fatal,
// matching substr() and the Go backend's __slice.
func (l *lowerer) listSlice(list Reg, t vty, from, to Reg) Reg {
	out := l.newList(t, initialCap)
	n := l.field(list, listLenOff, vInt)

	start := l.clamp(from, l.constant(0), n)
	end := l.clamp(to, l.constant(0), n)

	iSlot := l.scalarTemp(vInt)
	l.emit(Instr{Op: OpStore, A: start, Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	i := l.load(iSlot, vInt)
	more := l.compare(OpLt, i, end)
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})
	l.listPush(out, l.listGet(list, i, t.elemType()))
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	return out
}

// listJoin renders every element and puts the separator between them.
//
// The Go backend formats each with %v, which prints a string unquoted -
// so this goes through toStr rather than through the element writer the
// containers use, which quotes.
func (l *lowerer) listJoin(c *Call, list Reg, t vty, sep Reg) Reg {
	if l.regTy[sep].k != kStr {
		l.errorAt(c, "join needs a str separator, got %s", l.regTy[sep])
		return l.junk()
	}

	// Anything but strings is rendered first, once per element, into a
	// list of strings, so the join below only ever sees text.
	strs := list
	if t.elemType().k != kStr || t.elemType().null {
		strs = l.newList(vListOf(vStr), initialCap)
		l.eachElement(list, t, func(i, v Reg) {
			l.listPush(strs, l.toStr(v, c))
		})
	}
	st := vListOf(vStr)

	// Two passes: add up the length, then copy each piece into place.
	// Joining by concatenation, as this used to, copied everything so
	// far again for every element - quadratic in the result.
	sepLen := l.strLen(sep)
	total := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: total})
	l.eachElement(strs, st, func(i, v Reg) {
		n := l.arith(OpAdd, l.load(total, vInt), l.strLen(v))
		first := l.compare(OpEq, i, l.constant(0))
		withSep := l.pick(first, n, l.arith(OpAdd, n, sepLen), vInt)
		l.emit(Instr{Op: OpStore, A: withSep, Dst: NoReg, Imm: total})
	})

	out := l.strAlloc(l.load(total, vInt))
	outSlot := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: out, Dst: NoReg, Imm: outSlot})
	at := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: at})
	l.eachElement(strs, st, func(i, v Reg) {
		skip := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, i, l.constant(0)), Dst: NoReg, Imm: skip})
		l.copyBytesAt(l.load(outSlot, vStr), l.load(at, vInt), sep, sepLen)
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(at, vInt), sepLen), Dst: NoReg, Imm: at})
		l.mark(skip)
		n := l.strLen(v)
		l.copyBytesAt(l.load(outSlot, vStr), l.load(at, vInt), v, n)
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(at, vInt), n), Dst: NoReg, Imm: at})
	})
	res := l.load(outSlot, vStr)
	l.strEnd(res, l.load(at, vInt))
	return res
}

// listInsert opens a gap at i and writes v into it. An index past the
// end appends, and a negative one goes to the front, which is what the
// Go backend's __insert does.
func (l *lowerer) listInsert(list Reg, t vty, at, v Reg) {
	elem := t.elemType()
	n := l.field(list, listLenOff, vInt)
	i := l.clamp(at, l.constant(0), n)

	// Growing by one first means the shift has somewhere to go, and it
	// is the only place that knows how to reallocate.
	l.listPush(list, v)

	jSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, l.field(list, listLenOff, vInt),
		l.constant(1)), Dst: NoReg, Imm: jSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	j := l.load(jSlot, vInt)
	more := l.compare(OpGt, j, i)
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})
	l.listSet(list, j, l.listGet(list, l.arith(OpSub, j, l.constant(1)), elem))
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, j, l.constant(1)), Dst: NoReg, Imm: jSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	l.listSet(list, i, v)
}

// listRemoveAt takes one element out, closes the gap, and hands the
// element back.
func (l *lowerer) listRemoveAt(list Reg, t vty, at Reg) Reg {
	elem := t.elemType()
	n := l.field(list, listLenOff, vInt)

	// listGet bounds-checks, so an index off the end is the same failure
	// indexing gives rather than a silent write past the block.
	gone := l.freshCopy(l.listGet(list, at, elem), elem)

	iSlot := l.scalarTemp(vInt)
	l.emit(Instr{Op: OpStore, A: at, Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	i := l.load(iSlot, vInt)
	last := l.arith(OpSub, n, l.constant(1))
	more := l.compare(OpLt, i, last)
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})
	l.listSet(list, i, l.listGet(list, l.arith(OpAdd, i, l.constant(1)), elem))
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	l.emit(Instr{Op: OpStoreMem, A: list, B: l.arith(OpSub, n, l.constant(1)),
		Imm: listLenOff})
	return gone
}

// listSearch is contains and indexOf over a list: a linear scan that
// stops at the first match. indexOf answers -1 when there is none, the
// same as slices.Index.
func (l *lowerer) listSearch(c *Call, list Reg, t vty, name string) Reg {
	elem := t.elemType()
	want := l.rvalueAs(c.Args[1], elem)

	found := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(-1), Dst: NoReg, Imm: found})

	iSlot := l.scalarTemp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	next := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	i := l.load(iSlot, vInt)
	more := l.compare(OpLt, i, l.field(list, listLenOff, vInt))
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})

	same, ok := l.equalValues(c, l.listGet(list, i, elem), want, elem, name)
	if !ok {
		return l.junk()
	}
	l.emit(Instr{Op: OpJumpNot, A: same, Dst: NoReg, Imm: next})
	l.emit(Instr{Op: OpStore, A: i, Dst: NoReg, Imm: found})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(next)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	at := l.load(found, vInt)
	if name == "indexOf" {
		return at
	}
	return l.compare(OpGe, at, l.constant(0))
}

// splitStr, chars and lines all turn a string into a list of strings.
// They are here rather than in strings.go because the list is the part
// that needs building.

// splitStr is strings.Split: the pieces between each occurrence of sep,
// including empty ones at either end. An empty separator splits into
// single characters.
func (l *lowerer) splitStr(s, sep Reg) Reg {
	outSlot := l.temp(vListOf(vStr))
	out := l.newList(vListOf(vStr), initialCap)
	l.emit(Instr{Op: OpStore, A: out, Dst: NoReg, Imm: outSlot})

	n := l.strLen(s)
	sepLen := l.strLen(sep)

	// An empty separator is the chars() case. Go splits into runes
	// there; this splits into bytes, like every other string function on
	// this backend.
	byChar := l.newLabel()
	general := l.newLabel()
	joinPoint := l.newLabel()
	empty := l.compare(OpEq, sepLen, l.constant(0))
	l.emit(Instr{Op: OpJumpIf, A: empty, Dst: NoReg, Imm: byChar})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: general})

	l.mark(byChar)
	l.eachByte(s, n, func(i Reg) {
		l.listPush(out, l.substring(s, i, l.arith(OpAdd, i, l.constant(1))))
	})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: joinPoint})

	l.mark(general)
	got := l.splitBy(s, sep, false)
	l.emit(Instr{Op: OpStore, A: got, Dst: NoReg, Imm: outSlot})

	l.mark(joinPoint)
	return l.load(outSlot, vListOf(vStr))
}

// splitBy is the general case of split, one function rather than code at
// each call. memchr finds the next place the separator's first byte
// occurs - it looks at many bytes at a time, where comparing the
// separator at every position looked at one - and memcmp checks the
// rest of it there.
//
// With dropCR, a carriage return at the end of a piece is left out of
// it: lines, which would otherwise copy each such piece a second time to
// remove it.
func (l *lowerer) splitBy(s, sep Reg, dropCR bool) Reg {
	name := "__split"
	if dropCR {
		name = "__splitcr"
	}
	sym := l.helperFunc(name, []vty{vStr, vStr}, vListOf(vStr), func(a []Reg) {
		ss, sp := l.temp(vStr), l.temp(vStr)
		l.setSlot(ss, a[0])
		l.setSlot(sp, a[1])
		out := l.temp(vListOf(vStr))
		l.setSlot(out, l.newList(vListOf(vStr), initialCap))
		n, m := l.temp(vInt), l.temp(vInt)
		l.setSlot(n, l.strLen(l.load(ss, vStr)))
		l.setSlot(m, l.strLen(l.load(sp, vStr)))
		first := l.temp(vInt)
		l.setSlot(first, l.loadByte(l.load(sp, vStr), l.constant(0)))
		start, i := l.temp(vInt), l.temp(vInt)
		l.setSlot(start, l.constant(0))
		l.setSlot(i, l.constant(0))

		top, tail, miss := l.newLabel(), l.newLabel(), l.newLabel()
		l.mark(top)
		// The last place the separator could still start, plus one.
		room := l.arith(OpSub, l.arith(OpAdd, l.load(n, vInt), l.constant(1)),
			l.arith(OpAdd, l.load(i, vInt), l.load(m, vInt)))
		roomS := l.temp(vInt)
		l.setSlot(roomS, room)
		l.jumpNot(l.compare(OpGt, l.load(roomS, vInt), l.constant(0)), tail)
		hit := l.ccall("memchr", []Reg{l.arith(OpAdd, l.load(ss, vStr), l.load(i, vInt)),
			l.load(first, vInt), l.load(roomS, vInt)}, []vty{vInt, vInt, vInt}, vInt, false, false)
		hitS := l.temp(vInt)
		l.setSlot(hitS, hit)
		l.jumpIf(l.compare(OpEq, l.load(hitS, vInt), l.constant(0)), tail)
		l.setSlot(i, l.arith(OpSub, l.load(hitS, vInt), l.load(ss, vStr)))
		whole := l.newLabel()
		l.jumpIf(l.compare(OpEq, l.load(m, vInt), l.constant(1)), whole)
		same := l.ccall("memcmp", []Reg{l.arith(OpAdd, l.load(hitS, vInt), l.constant(1)),
			l.arith(OpAdd, l.load(sp, vStr), l.constant(1)), l.arith(OpSub, l.load(m, vInt), l.constant(1))},
			[]vty{vInt, vInt, vInt}, vInt, true, false)
		l.jumpNot(l.compare(OpEq, same, l.constant(0)), miss)
		push := func(end Reg) {
			e := l.temp(vInt)
			l.setSlot(e, end)
			if dropCR {
				keep := l.newLabel()
				l.jumpNot(l.compare(OpGt, l.load(e, vInt), l.load(start, vInt)), keep)
				last := l.arith(OpSub, l.load(e, vInt), l.constant(1))
				l.jumpNot(l.compare(OpEq, l.loadByte(l.load(ss, vStr), last), l.constant(13)), keep)
				l.setSlot(e, l.arith(OpSub, l.load(e, vInt), l.constant(1)))
				l.mark(keep)
			}
			l.listPush(l.load(out, vListOf(vStr)), l.substring(l.load(ss, vStr), l.load(start, vInt), l.load(e, vInt)))
		}
		l.mark(whole)
		push(l.load(i, vInt))
		l.setSlot(start, l.arith(OpAdd, l.load(i, vInt), l.load(m, vInt)))
		l.setSlot(i, l.load(start, vInt))
		l.jump(top)
		l.mark(miss)
		l.setSlot(i, l.arith(OpAdd, l.load(i, vInt), l.constant(1)))
		l.jump(top)

		// Whatever follows the last separator is the final piece, and it
		// is pushed even when it is empty: "a,b," splits into three, the
		// last one empty, which is what strings.Split gives.
		l.mark(tail)
		push(l.load(n, vInt))
		l.ret(l.load(out, vListOf(vStr)))
	})
	return l.callHelper(sym, []Reg{s, sep}, []vty{vStr, vStr}, vListOf(vStr))
}

// eachByte walks the bytes of a string by index.
func (l *lowerer) eachByte(s, n Reg, body func(i Reg)) {
	iSlot := l.scalarTemp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	i := l.load(iSlot, vInt)
	more := l.compare(OpLt, i, n)
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})

	body(i)

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(iSlot, vInt), l.constant(1)),
		Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
}

// strListBuiltin lowers split, chars and lines.
func (l *lowerer) strListBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "split":
		if len(c.Args) != 2 {
			l.errorAt(c, "split takes 2 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
		return l.splitStr(l.expr(c.Args[0]), l.expr(c.Args[1])), true

	case "lines":
		if len(c.Args) != 1 {
			l.errorAt(c, "lines takes 1 argument, got %d", len(c.Args))
			return l.junk(), true
		}
		// The Go backend rewrites CRLF to LF and splits on LF, keeping
		// a trailing empty piece. Leaving the carriage return out of the
		// end of each piece is the same result without a second copy of
		// the text.
		pieces := l.splitBy(l.expr(c.Args[0]), l.strLit("\n"), true)
		return pieces, true
	}
	return NoReg, false
}
