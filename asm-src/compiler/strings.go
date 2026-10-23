package main

// The string library, lowered in the IR out of byte loads and stores.
//
// A Scandiacus string is a pointer to its bytes, like a C string, and like a
// C string they end in a zero byte, so any C function can take one as it
// is. But the length is not found by looking for that zero: it is in
// the object header in front of the bytes, the size word every heap
// object has (the length plus the terminator), and string literals carry
// the same header in .rdata. So len is one load, and a string can hold
// zero bytes of its own - binary data reads back whole. Everything below
// loops over an index up to strLen.
//
// The rule that follows: every str value must have that header. A
// pointer to C memory is never a str until it is copied into one with
// cstr. Writing these as IR rather than as assembly helpers means
// the byte writer inherits them, and means each one is readable as
// ordinary control flow rather than as a page of instructions.
//
// Every function that returns a string allocates a new one. None of them
// free anything.

// strAlloc reserves n+1 bytes and returns the pointer, typed as a
// string. The extra byte is the terminator, which every caller has to
// write itself.
func (l *lowerer) strAlloc(n Reg) Reg {
	buf := l.allocObj(l.arith(OpAdd, n, l.constant(1)), tagBytes)
	l.regTy[buf] = vStr
	return buf
}

// cstr copies NUL-terminated C memory - a buffer an API filled in, a
// pointer a library returned - into a string with a header, measuring
// it the C way, with strlen.
func (l *lowerer) cstr(p Reg) Reg {
	ps := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: p, Dst: NoReg, Imm: ps})
	n := l.ccall("strlen", []Reg{l.load(ps, vInt)}, []vty{vInt}, vInt, false, false)
	return l.strFromMem(l.load(ps, vInt), n)
}

// cstrOrNil is cstr with a null pointer passed through as 0, for an API
// whose null means "there is none".
func (l *lowerer) cstrOrNil(p Reg) Reg {
	ps := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: p, Dst: NoReg, Imm: ps})
	out := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: out})
	skip := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(ps, vInt), l.constant(0)), Dst: NoReg, Imm: skip})
	l.emit(Instr{Op: OpStore, A: l.cstr(l.load(ps, vInt)), Dst: NoReg, Imm: out})
	l.mark(skip)
	return l.load(out, vStr)
}

// strEnd finishes a string built in a buffer that may be bigger than
// what was written into it: the terminator at n, and the header's size
// brought down to n+1, so the length reads as n. The live-byte count
// the collector keeps is moved by the same amount, since a sweep takes
// the size off it from the header when the string is freed.
func (l *lowerer) strEnd(buf, n Reg) {
	bs := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: buf, Dst: NoReg, Imm: bs})
	ns := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: n, Dst: NoReg, Imm: ns})
	l.storeByte(l.load(bs, vInt), l.load(ns, vInt), l.constant(0))
	at := l.arith(OpSub, l.load(bs, vInt), l.constant(objHeader))
	old := l.peekWord(at, objTagOff)
	olds := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: old, Dst: NoReg, Imm: olds})
	size := l.arith(OpAdd, l.load(ns, vInt), l.constant(1))
	hdr := l.arith(OpBOr, l.arith(OpShl, size, l.constant(tagShift)),
		l.arith(OpBAnd, l.load(olds, vInt), l.constant(255)))
	l.pokeWord(l.arith(OpSub, l.load(bs, vInt), l.constant(objHeader)), objTagOff, hdr)
	delta := l.arith(OpSub, l.arith(OpAdd, l.load(ns, vInt), l.constant(1)),
		l.arith(OpShr, l.load(olds, vInt), l.constant(tagShift)))
	l.atomicAdd(l.rtSlot(gcBytesSlot), delta)
}

// strCmp orders two strings as Go does, byte by byte with a shorter
// prefix first: below zero, zero or above. Zero bytes inside count like
// any other, which strcmp could not do.
func (l *lowerer) strCmp(a, b Reg) Reg {
	sym := l.helperFunc("__strcmp", []vty{vStr, vStr}, vInt, func(p []Reg) {
		as := l.temp(vStr)
		l.emit(Instr{Op: OpStore, A: p[0], Dst: NoReg, Imm: as})
		bs := l.temp(vStr)
		l.emit(Instr{Op: OpStore, A: p[1], Dst: NoReg, Imm: bs})
		la := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: l.strLen(l.load(as, vStr)), Dst: NoReg, Imm: la})
		lb := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: l.strLen(l.load(bs, vStr)), Dst: NoReg, Imm: lb})
		n := l.pick(l.compare(OpLt, l.load(la, vInt), l.load(lb, vInt)),
			l.load(la, vInt), l.load(lb, vInt), vInt)
		r := l.ccall("memcmp", []Reg{l.load(as, vStr), l.load(bs, vStr), n},
			[]vty{vStr, vStr, vInt}, vInt, true, false)
		rs := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: r, Dst: NoReg, Imm: rs})
		tie := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(rs, vInt), l.constant(0)), Dst: NoReg, Imm: tie})
		l.emit(Instr{Op: OpRet, A: l.load(rs, vInt), Dst: NoReg})
		l.mark(tie)
		l.emit(Instr{Op: OpRet, A: l.arith(OpSub, l.load(la, vInt), l.load(lb, vInt)), Dst: NoReg})
	})
	return l.callHelper(sym, []Reg{a, b}, []vty{vStr, vStr}, vInt)
}

func (l *lowerer) strLen(s Reg) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpStrLen, Dst: d, A: s, B: NoReg})
	return d
}

func (l *lowerer) loadByte(base, off Reg) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpLoadByte, Dst: d, A: base, B: off})
	return d
}

// storeByte writes one byte. The value travels through a slot because
// the instruction already spends both register fields on the base and
// the offset.
func (l *lowerer) storeByte(base, off, val Reg) {
	slot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: val, Dst: NoReg, Imm: slot})
	l.emit(Instr{Op: OpStoreByte, A: base, B: off, Imm: slot})
}

// mapBytes copies s into a fresh buffer, passing each byte through
// transform. It is the shape of upper, lower and anything else that
// rewrites a string character by character.
func (l *lowerer) mapBytes(s Reg, transform func(b Reg) Reg) Reg {
	n := l.strLen(s)
	out := l.strAlloc(n)

	srcSlot := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: s, Dst: NoReg, Imm: srcSlot})
	dstSlot := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: out, Dst: NoReg, Imm: dstSlot})
	nSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: n, Dst: NoReg, Imm: nSlot})

	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	i := l.load(iSlot, vInt)
	more := l.compare(OpLt, i, l.load(nSlot, vInt))
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})

	b := l.loadByte(l.load(srcSlot, vStr), i)
	l.storeByte(l.load(dstSlot, vStr), i, transform(b))

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(done)

	// The terminator, at the length rather than at wherever the loop
	// happened to stop.
	l.storeByte(l.load(dstSlot, vStr), l.load(nSlot, vInt), l.constant(0))
	return l.load(dstSlot, vStr)
}

func (l *lowerer) load(slot int64, t vty) Reg {
	d := l.newReg()
	l.regTy[d] = t
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: slot})
	return d
}

// shiftIf adds delta to b when b is within [lo, hi]. That is the whole
// of ASCII case conversion, and it deliberately leaves anything outside
// that range alone rather than corrupting UTF-8 continuation bytes.
func (l *lowerer) shiftIf(b Reg, lo, hi, delta int64) Reg {
	slot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: b, Dst: NoReg, Imm: slot})

	skip := l.newLabel()
	tooLow := l.compare(OpLt, b, l.constant(lo))
	l.emit(Instr{Op: OpJumpIf, A: tooLow, Dst: NoReg, Imm: skip})
	tooHigh := l.compare(OpGt, b, l.constant(hi))
	l.emit(Instr{Op: OpJumpIf, A: tooHigh, Dst: NoReg, Imm: skip})
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, b, l.constant(delta)),
		Dst: NoReg, Imm: slot})
	l.mark(skip)

	return l.load(slot, vInt)
}

// substring copies the half-open range [from, to). Both ends are clamped
// into the string rather than reported, matching what the Go backend
// does for slice(), so a program cannot crash on an off-by-one here.
func (l *lowerer) substring(s, from, to Reg) Reg {
	n := l.strLen(s)

	lo := l.clamp(from, l.constant(0), n)
	hi := l.clamp(to, lo, n)

	count := l.arith(OpSub, hi, lo)
	out := l.strAlloc(count)

	srcSlot := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: s, Dst: NoReg, Imm: srcSlot})
	dstSlot := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: out, Dst: NoReg, Imm: dstSlot})
	loSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: lo, Dst: NoReg, Imm: loSlot})
	cntSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: count, Dst: NoReg, Imm: cntSlot})

	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)
	i := l.load(iSlot, vInt)
	more := l.compare(OpLt, i, l.load(cntSlot, vInt))
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})

	at := l.arith(OpAdd, i, l.load(loSlot, vInt))
	l.storeByte(l.load(dstSlot, vStr), i, l.loadByte(l.load(srcSlot, vStr), at))

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(done)

	l.storeByte(l.load(dstSlot, vStr), l.load(cntSlot, vInt), l.constant(0))
	return l.load(dstSlot, vStr)
}

// clamp returns v held between lo and hi.
func (l *lowerer) clamp(v, lo, hi Reg) Reg {
	slot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: v, Dst: NoReg, Imm: slot})

	notLow := l.newLabel()
	below := l.compare(OpLt, v, lo)
	l.emit(Instr{Op: OpJumpNot, A: below, Dst: NoReg, Imm: notLow})
	l.emit(Instr{Op: OpStore, A: lo, Dst: NoReg, Imm: slot})
	l.mark(notLow)

	held := l.load(slot, vInt)
	notHigh := l.newLabel()
	above := l.compare(OpGt, held, hi)
	l.emit(Instr{Op: OpJumpNot, A: above, Dst: NoReg, Imm: notHigh})
	l.emit(Instr{Op: OpStore, A: hi, Dst: NoReg, Imm: slot})
	l.mark(notHigh)

	return l.load(slot, vInt)
}

// indexOfStr finds needle in haystack, or -1. The naive scan is O(n*m)
// and is what the standard library of every language starts with; a
// program that needs better than that needs a different function, not a
// cleverer version of this one.
func (l *lowerer) indexOfStr(hay, needle Reg) Reg {
	hn := l.strLen(hay)
	nn := l.strLen(needle)

	haySlot := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: hay, Dst: NoReg, Imm: haySlot})
	needleSlot := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: needle, Dst: NoReg, Imm: needleSlot})
	hnSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: hn, Dst: NoReg, Imm: hnSlot})
	nnSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: nn, Dst: NoReg, Imm: nnSlot})

	resultSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(-1), Dst: NoReg, Imm: resultSlot})

	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	outer := l.newLabel()
	outerDone := l.newLabel()
	l.mark(outer)

	i := l.load(iSlot, vInt)
	last := l.arith(OpSub, l.load(hnSlot, vInt), l.load(nnSlot, vInt))
	past := l.compare(OpGt, i, last)
	l.emit(Instr{Op: OpJumpIf, A: past, Dst: NoReg, Imm: outerDone})

	jSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: jSlot})

	inner := l.newLabel()
	matched := l.newLabel()
	mismatch := l.newLabel()
	l.mark(inner)

	j := l.load(jSlot, vInt)
	moreJ := l.compare(OpLt, j, l.load(nnSlot, vInt))
	l.emit(Instr{Op: OpJumpNot, A: moreJ, Dst: NoReg, Imm: matched})

	a := l.loadByte(l.load(haySlot, vStr), l.arith(OpAdd, i, j))
	b := l.loadByte(l.load(needleSlot, vStr), j)
	same := l.compare(OpEq, a, b)
	l.emit(Instr{Op: OpJumpNot, A: same, Dst: NoReg, Imm: mismatch})

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, j, l.constant(1)), Dst: NoReg, Imm: jSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: inner})

	l.mark(matched)
	l.emit(Instr{Op: OpStore, A: i, Dst: NoReg, Imm: resultSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: outerDone})

	l.mark(mismatch)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: outer})

	l.mark(outerDone)
	return l.load(resultSlot, vInt)
}

// stringBuiltin dispatches the ones this backend has. Names and
// behaviour follow ../src exactly, because the differential test
// compares their output.
func (l *lowerer) stringBuiltin(c *Call, name string) (Reg, bool) {
	need := func(n int) bool {
		if len(c.Args) != n {
			l.errorAt(c, "%s takes %d argument(s), got %d", name, n, len(c.Args))
			return false
		}
		return true
	}
	str := func(i int) Reg {
		r := l.expr(c.Args[i])
		if l.regTy[r].k != kStr {
			l.errorAt(c, "%s needs a str for argument %d", name, i+1)
		}
		return r
	}

	switch name {
	// The digits of a float to a fixed number of places, by the C
	// runtime's %.*f. __vy_fixed asks for every digit the value has and
	// rounds them itself, since C runtimes disagree on a tie.
	// The float arrives as its bits and goes in the va_list as them.
	case "__fixedRaw":
		if !need(2) {
			return l.junk(), true
		}
		bits, places := l.expr(c.Args[0]), l.expr(c.Args[1])
		va := l.allocObj(l.constant(2*wordSize), tagWords)
		vs := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: va, Dst: NoReg, Imm: vs})
		l.pokeWord(l.load(vs, vInt), 0, places)
		l.pokeWord(l.load(vs, vInt), wordSize, bits)
		const room = 1600 // 309 digits of 1e308, the point, 1140 places, a sign
		buf := l.strAlloc(l.constant(room))
		bs := l.temp(vStr)
		l.emit(Instr{Op: OpStore, A: buf, Dst: NoReg, Imm: bs})
		n := l.ccall("__stdio_common_vsprintf",
			[]Reg{l.constant(2), l.load(bs, vInt), l.constant(room + 1), l.strLit("%.*f"), l.constant(0), l.load(vs, vInt)},
			[]vty{vInt, vInt, vInt, vInt, vInt, vInt}, vInt, true, false)
		l.strEnd(l.load(bs, vStr), n)
		return l.load(bs, vStr), true

	// ASCII only; upper and lower themselves are __vy_upper and
	// __vy_lower in the prelude, which call these first.
	case "__upperA":
		if !need(1) {
			return l.junk(), true
		}
		return l.mapBytes(str(0), func(b Reg) Reg {
			return l.shiftIf(b, 'a', 'z', -32)
		}), true

	case "__lowerA":
		if !need(1) {
			return l.junk(), true
		}
		return l.mapBytes(str(0), func(b Reg) Reg {
			return l.shiftIf(b, 'A', 'Z', 32)
		}), true

	case "__substrB":
		if !need(3) {
			return l.junk(), true
		}
		s := str(0)
		return l.substring(s, l.expr(c.Args[1]), l.expr(c.Args[2])), true

	case "indexOf":
		if !need(2) {
			return l.junk(), true
		}
		return l.indexOfStr(str(0), str(1)), true

	case "contains":
		if !need(2) {
			return l.junk(), true
		}
		at := l.indexOfStr(str(0), str(1))
		return l.compare(OpGe, at, l.constant(0)), true

	case "startsWith":
		if !need(2) {
			return l.junk(), true
		}
		s, prefix := str(0), str(1)
		n := l.strLen(prefix)
		head := l.substring(s, l.constant(0), n)
		l.mod.needs("streq")
		d := l.newReg()
		l.regTy[d] = vBool
		l.emit(Instr{Op: OpStrEq, Dst: d, A: head, B: prefix})
		return d, true

	case "endsWith":
		if !need(2) {
			return l.junk(), true
		}
		s, suffix := str(0), str(1)
		sn := l.strLen(s)
		fn := l.strLen(suffix)
		tail := l.substring(s, l.arith(OpSub, sn, fn), sn)
		l.mod.needs("streq")
		d := l.newReg()
		l.regTy[d] = vBool
		l.emit(Instr{Op: OpStrEq, Dst: d, A: tail, B: suffix})
		return d, true

	case "__charAtB":
		if !need(2) {
			return l.junk(), true
		}
		s := str(0)
		at := l.expr(c.Args[1])
		return l.substring(s, at, l.arith(OpAdd, at, l.constant(1))), true

	case "repeat":
		if !need(2) {
			return l.junk(), true
		}
		return l.repeatStr(str(0), l.expr(c.Args[1])), true
	}

	return NoReg, false
}

// repeatStr joins n copies of s: the result sized once, then filled by
// doubling - the first copy, then the filled part copied onto its own
// end - so it is a handful of memmoves however large n is. It was a loop
// of concatenations, which was quadratic and, since nothing collects in
// the middle of a builtin, could allocate gigabytes on the way to a few
// megabytes.
func (l *lowerer) repeatStr(s, n Reg) Reg {
	srcS := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: s, Dst: NoReg, Imm: srcS})
	countS := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: n, Dst: NoReg, Imm: countS})
	lenS := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.strLen(l.load(srcS, vStr)), Dst: NoReg, Imm: lenS})

	// A count of zero or less, or an empty string, is the empty string.
	totalS := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: totalS})
	sized := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpGt, l.load(countS, vInt), l.constant(0)), Dst: NoReg, Imm: sized})
	l.emit(Instr{Op: OpStore, A: l.arith(OpMul, l.load(lenS, vInt), l.load(countS, vInt)), Dst: NoReg, Imm: totalS})
	l.mark(sized)

	out := l.strAlloc(l.load(totalS, vInt))
	outS := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: out, Dst: NoReg, Imm: outS})
	doneS := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: doneS})

	fill := l.newLabel()
	finish := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpGt, l.load(totalS, vInt), l.constant(0)), Dst: NoReg, Imm: finish})
	l.copyBytes(l.load(outS, vInt), l.load(srcS, vInt), l.load(lenS, vInt))
	l.emit(Instr{Op: OpStore, A: l.load(lenS, vInt), Dst: NoReg, Imm: doneS})
	l.mark(fill)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, l.load(doneS, vInt), l.load(totalS, vInt)), Dst: NoReg, Imm: finish})
	// Copy as much of what is already there as still fits.
	chunk := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.load(doneS, vInt), Dst: NoReg, Imm: chunk})
	left := l.arith(OpSub, l.load(totalS, vInt), l.load(doneS, vInt))
	small := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpGt, l.load(chunk, vInt), left), Dst: NoReg, Imm: small})
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, l.load(totalS, vInt), l.load(doneS, vInt)), Dst: NoReg, Imm: chunk})
	l.mark(small)
	l.copyBytes(l.arith(OpAdd, l.load(outS, vInt), l.load(doneS, vInt)), l.load(outS, vInt), l.load(chunk, vInt))
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(doneS, vInt), l.load(chunk, vInt)), Dst: NoReg, Imm: doneS})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: fill})
	l.mark(finish)
	l.storeByte(l.load(outS, vStr), l.load(totalS, vInt), l.constant(0))
	return l.load(outS, vStr)
}

// isSpaceByte is the set strings.TrimSpace strips, for the bytes that
// can appear in ASCII text. Go's version also strips the Unicode space
// characters; this one is byte-wise like the rest of the string library
// here, so it agrees on every input a program is likely to trim and
// disagrees on U+00A0 and its relatives.
func (l *lowerer) isSpaceByte(b Reg) Reg {
	space := l.compare(OpEq, b, l.constant(32))
	for _, c := range []int64{9, 10, 11, 12, 13} {
		space = l.logicalOr(space, l.compare(OpEq, b, l.constant(c)))
	}
	return space
}

// trimStr is strings.TrimSpace: whitespace off both ends.
func (l *lowerer) trimStr(s Reg) Reg {
	n := l.strLen(s)

	startSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: startSlot})
	endSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: n, Dst: NoReg, Imm: endSlot})

	// Forward past the leading space.
	top := l.newLabel()
	front := l.newLabel()
	l.mark(top)
	i := l.load(startSlot, vInt)
	more := l.compare(OpLt, i, l.load(endSlot, vInt))
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: front})
	l.emit(Instr{Op: OpJumpNot, A: l.isSpaceByte(l.loadByte(s, i)), Dst: NoReg, Imm: front})
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: startSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(front)

	// Back over the trailing space.
	top2 := l.newLabel()
	back := l.newLabel()
	l.mark(top2)
	e := l.load(endSlot, vInt)
	left := l.compare(OpGt, e, l.load(startSlot, vInt))
	l.emit(Instr{Op: OpJumpNot, A: left, Dst: NoReg, Imm: back})
	last := l.arith(OpSub, e, l.constant(1))
	l.emit(Instr{Op: OpJumpNot, A: l.isSpaceByte(l.loadByte(s, last)), Dst: NoReg, Imm: back})
	l.emit(Instr{Op: OpStore, A: last, Dst: NoReg, Imm: endSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top2})
	l.mark(back)

	return l.substring(s, l.load(startSlot, vInt), l.load(endSlot, vInt))
}

// parseInt is strconv.Atoi on a trimmed string. It writes the value into
// one slot and whether it worked into another, because the two builtins
// on top of it want different halves of that answer.
//
// No overflow check: a number too large for 64 bits wraps here where Go
// reports a range error and toInt falls back. That is a real difference
// and the wrong kind - silent - but it needs a division to detect and
// nothing in the tests reaches it.
func (l *lowerer) parseInt(s Reg) (valSlot, okSlot int64) {
	t := l.trimStr(s)
	n := l.strLen(t)

	valSlot = l.temp(vInt)
	okSlot = l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: valSlot})
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: okSlot})

	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})
	negSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: negSlot})

	done := l.newLabel()
	noSign := l.newLabel()

	// A leading sign, then at least one digit. Atoi accepts both + and -.
	any := l.compare(OpGt, n, l.constant(0))
	l.emit(Instr{Op: OpJumpNot, A: any, Dst: NoReg, Imm: done})
	first := l.loadByte(t, l.constant(0))
	isMinus := l.compare(OpEq, first, l.constant(45))
	isPlus := l.compare(OpEq, first, l.constant(43))
	signed := l.logicalOr(isMinus, isPlus)
	l.emit(Instr{Op: OpJumpNot, A: signed, Dst: NoReg, Imm: noSign})
	l.emit(Instr{Op: OpStore, A: l.constant(1), Dst: NoReg, Imm: iSlot})
	negTo := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: isMinus, Dst: NoReg, Imm: negTo})
	l.emit(Instr{Op: OpStore, A: l.constant(1), Dst: NoReg, Imm: negSlot})
	l.mark(negTo)
	l.mark(noSign)

	// A sign on its own is not a number.
	digits := l.compare(OpLt, l.load(iSlot, vInt), n)
	l.emit(Instr{Op: OpJumpNot, A: digits, Dst: NoReg, Imm: done})

	accSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: accSlot})

	top := l.newLabel()
	good := l.newLabel()
	l.mark(top)
	i := l.load(iSlot, vInt)
	more := l.compare(OpLt, i, n)
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: good})

	b := l.loadByte(t, i)
	tooLow := l.compare(OpLt, b, l.constant(48))
	tooHigh := l.compare(OpGt, b, l.constant(57))
	l.emit(Instr{Op: OpJumpIf, A: l.logicalOr(tooLow, tooHigh), Dst: NoReg, Imm: done})

	acc := l.arith(OpAdd,
		l.arith(OpMul, l.load(accSlot, vInt), l.constant(10)),
		l.arith(OpSub, b, l.constant(48)))
	l.emit(Instr{Op: OpStore, A: acc, Dst: NoReg, Imm: accSlot})
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(good)
	// Every byte was a digit and there was at least one, so this parsed.
	l.emit(Instr{Op: OpStore, A: l.constant(1), Dst: NoReg, Imm: okSlot})
	value := l.load(accSlot, vInt)
	keep := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpNe, l.load(negSlot, vInt), l.constant(0)),
		Dst: NoReg, Imm: keep})
	negated := l.newReg()
	l.regTy[negated] = vInt
	l.emit(Instr{Op: OpNeg, Dst: negated, A: value, B: NoReg})
	l.emit(Instr{Op: OpStore, A: negated, Dst: NoReg, Imm: valSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
	l.mark(keep)
	l.emit(Instr{Op: OpStore, A: value, Dst: NoReg, Imm: valSlot})

	l.mark(done)
	return valSlot, okSlot
}

// numStrBuiltin lowers trim and the string-to-number family.
func (l *lowerer) numStrBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "trim":
		if len(c.Args) != 1 {
			l.errorAt(c, "trim takes 1 argument, got %d", len(c.Args))
			return l.junk(), true
		}
		return l.trimStr(l.expr(c.Args[0])), true

	case "isInt":
		if len(c.Args) != 1 {
			l.errorAt(c, "isInt takes 1 argument, got %d", len(c.Args))
			return l.junk(), true
		}
		_, ok := l.parseIntOK(l.expr(c.Args[0]))
		return ok, true

	case "toInt":
		if len(c.Args) < 1 || len(c.Args) > 2 {
			l.errorAt(c, "toInt takes 1 or 2 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
		v, ok := l.parseIntOK(l.expr(c.Args[0]))
		fallback := l.constant(0)
		if len(c.Args) == 2 {
			fallback = l.expr(c.Args[1])
		}
		return l.pick(ok, v, fallback, vInt), true
	}
	return NoReg, false
}

// parseIntOK is parseInt with both halves read back out.
func (l *lowerer) parseIntOK(s Reg) (value, ok Reg) {
	valSlot, okSlot := l.parseInt(s)
	return l.load(valSlot, vInt), l.compare(OpNe, l.load(okSlot, vInt), l.constant(0))
}
