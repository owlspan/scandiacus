package main

// The bytes type.
//
// A bytes value is a pointer to a tagBytes block, with the length in the
// object header. No separate length word, and no terminator: a zero byte
// inside it is just a byte, which is the whole difference from a str.
//
// Only the primitives are here. hex, base64, the integer codecs and the
// hashes are all written in Veyl, in prelude_bytes.go, on top of
// __bytesMake, __byteAt and __bytePut.

func (l *lowerer) bytesBuiltin(c *Call, name string) (Reg, bool) {
	arity := func(n int) bool {
		if len(c.Args) != n {
			l.errorAt(c, "%s takes %d argument(s), got %d", name, n, len(c.Args))
			return false
		}
		return true
	}

	switch name {
	case "__bytesMake":
		if !arity(1) {
			return l.junk(), true
		}
		// Zeroed: malloc's memory is whatever was there, and the prelude
		// treats a fresh buffer as all zeros (the regex marks do).
		ns := l.temp(vInt)
		l.setSlot(ns, l.intArg(c, 0))
		bs := l.temp(vBytes)
		l.setSlot(bs, l.bytesAlloc(l.load(ns, vInt)))
		l.copyZero(l.load(bs, vInt), l.load(ns, vInt))
		return l.load(bs, vBytes), true

	case "__byteAt":
		if !arity(2) {
			return l.junk(), true
		}
		b := l.bytesArg(c, 0)
		d := l.newReg()
		l.regTy[d] = vInt
		l.emit(Instr{Op: OpLoadByte, Dst: d, A: b, B: l.intArg(c, 1)})
		return d, true

	case "__bytePut":
		if !arity(3) {
			return l.junk(), true
		}
		b := l.bytesArg(c, 0)
		l.storeByte(b, l.intArg(c, 1), l.intArg(c, 2))
		return l.junk(), true

	case "bytes.of":
		// A str is NUL-terminated, so its length is strlen. Anything
		// past a zero byte was never in the string to begin with.
		if !arity(1) {
			return l.junk(), true
		}
		s := l.expr(c.Args[0])
		if l.regTy[s].k != kStr {
			l.errorAt(c.Args[0], "bytes.of expects a str, got %s", l.regTy[s])
			return l.junk(), true
		}
		return l.bytesFromMem(s, l.strLen(s)), true

	case "bytes.str":
		if !arity(1) {
			return l.junk(), true
		}
		b := l.bytesArg(c, 0)
		return l.strFromMem(b, l.bytesLen(b)), true

	case "bytes.read":
		if !arity(1) {
			return l.junk(), true
		}
		return l.readBytes(c), true

	case "bytes.write":
		if !arity(2) {
			return l.junk(), true
		}
		return l.writeBytes(c), true

	case "bytes.concat":
		if len(c.Args) < 1 {
			l.errorAt(c, "bytes.concat takes at least 1 argument")
			return l.junk(), true
		}
		return l.bytesConcat(c), true
	}

	return NoReg, false
}

func (l *lowerer) bytesArg(c *Call, i int) Reg {
	v := l.expr(c.Args[i])
	if l.regTy[v].k != kBytes {
		l.errorAt(c.Args[i], "expected bytes, got %s", l.regTy[v])
		return l.constant(0)
	}
	return v
}

// bytesAlloc makes an uninitialised block of n bytes.
func (l *lowerer) bytesAlloc(n Reg) Reg {
	b := l.allocObj(n, tagBytes)
	l.regTy[b] = vBytes
	return b
}

// bytesLen reads the length out of the object header.
func (l *lowerer) bytesLen(b Reg) Reg {
	hdr := l.peekWord(l.arith(OpSub, b, l.constant(objHeader)), objTagOff)
	return l.arith(OpShr, hdr, l.constant(tagShift))
}

// bytesFromMem copies n bytes out of any pointer.
func (l *lowerer) bytesFromMem(src, n Reg) Reg {
	out := l.bytesAlloc(n)
	l.copyBytes(out, src, n)
	return out
}

// strFromMem is the same the other way, plus the terminator a str needs.
func (l *lowerer) strFromMem(src, n Reg) Reg {
	out := l.strAlloc(n)
	l.copyBytes(out, src, n)
	l.storeByte(out, n, l.constant(0))
	return out
}

// copyZero clears n bytes at dst; a count of zero or less clears none.
func (l *lowerer) copyZero(dst, n Reg) {
	skip := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpGt, n, l.constant(0)), Dst: NoReg, Imm: skip})
	l.ccall("memset", []Reg{dst, l.constant(0), n}, []vty{vInt, vInt, vInt}, vInt, false, false)
	l.mark(skip)
}

// copyBytes moves n bytes from src to dst, with memmove: one pass over
// memory rather than a loop of byte loads and stores, which is what
// every substring, file read and bytes copy spends its time in. A count
// of zero or less copies nothing.
func (l *lowerer) copyBytes(dst, src, n Reg) {
	skip := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpGt, n, l.constant(0)), Dst: NoReg, Imm: skip})
	l.ccall("memmove", []Reg{dst, src, n}, []vty{vInt, vInt, vInt}, vInt, false, false)
	l.mark(skip)
}

// bytesConcat is variadic, which the prelude cannot express, so it is
// here. Two passes: total the lengths, then copy.
func (l *lowerer) bytesConcat(c *Call) Reg {
	parts := make([]Reg, len(c.Args))
	for i := range c.Args {
		parts[i] = l.bytesArg(c, i)
	}

	total := l.constant(0)
	for _, p := range parts {
		total = l.arith(OpAdd, total, l.bytesLen(p))
	}

	out := l.bytesAlloc(total)
	at := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: at})
	for _, p := range parts {
		n := l.bytesLen(p)
		base := l.newReg()
		l.regTy[base] = vInt
		l.emit(Instr{Op: OpLoad, Dst: base, A: NoReg, B: NoReg, Imm: at})
		l.copyBytesAt(out, base, p, n)
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, base, n), Dst: NoReg, Imm: at})
	}
	return out
}

// copyBytesAt is copyBytes writing at an offset in dst.
func (l *lowerer) copyBytesAt(dst, off, src, n Reg) {
	l.copyBytes(l.arith(OpAdd, dst, off), src, n)
}

// bytesEqual compares contents, which is what == means on every other
// container here.
func (l *lowerer) bytesEqual(a, b Reg) Reg {
	out := l.temp(vBool)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: out})
	done := l.newLabel()

	na, nb := l.bytesLen(a), l.bytesLen(b)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, na, nb), Dst: NoReg, Imm: done})

	i := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: i})
	top, same := l.newLabel(), l.newLabel()
	l.mark(top)

	cur := l.newReg()
	l.regTy[cur] = vInt
	l.emit(Instr{Op: OpLoad, Dst: cur, A: NoReg, B: NoReg, Imm: i})
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, cur, na), Dst: NoReg, Imm: same})

	x := l.newReg()
	l.regTy[x] = vInt
	l.emit(Instr{Op: OpLoadByte, Dst: x, A: a, B: cur})
	y := l.newReg()
	l.regTy[y] = vInt
	l.emit(Instr{Op: OpLoadByte, Dst: y, A: b, B: cur})
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, x, y), Dst: NoReg, Imm: done})

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, cur, l.constant(1)), Dst: NoReg, Imm: i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(same)
	l.emit(Instr{Op: OpStore, A: l.constant(1), Dst: NoReg, Imm: out})
	l.mark(done)

	d := l.newReg()
	l.regTy[d] = vBool
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: out})
	return d
}

// checkBounds stops with the same message a list index does.
func (l *lowerer) checkBounds(idx, length Reg) {
	l.checkIndex(idx, length)
}

// checkIndex is the check itself; see OpCheckIndex.
func (l *lowerer) checkIndex(idx, length Reg) {
	l.mod.needs("bounds")
	l.emit(Instr{Op: OpCheckIndex, A: idx, B: length, Dst: NoReg, Imm: l.where(), Comment: "bounds"})
}

// bytesHexStr renders a bytes as its hex digits, which is how print and
// str() show one. Built here rather than calling the prelude, so that
// printing a bytes does not drag the prelude in.
func (l *lowerer) bytesHexStr(b Reg) Reg {
	n := l.bytesLen(b)
	out := l.strAlloc(l.arith(OpMul, n, l.constant(2)))

	digits := l.newReg()
	l.regTy[digits] = vStr
	l.emit(Instr{Op: OpStr, Dst: digits, A: NoReg, B: NoReg,
		Imm: l.mod.intern("0123456789abcdef")})

	i := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: i})
	top, done := l.newLabel(), l.newLabel()
	l.mark(top)

	cur := l.newReg()
	l.regTy[cur] = vInt
	l.emit(Instr{Op: OpLoad, Dst: cur, A: NoReg, B: NoReg, Imm: i})
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, cur, n), Dst: NoReg, Imm: done})

	v := l.newReg()
	l.regTy[v] = vInt
	l.emit(Instr{Op: OpLoadByte, Dst: v, A: b, B: cur})

	hiNib := l.arith(OpBAnd, l.arith(OpShr, v, l.constant(4)), l.constant(15))
	loNib := l.arith(OpBAnd, v, l.constant(15))
	hiCh := l.newReg()
	l.regTy[hiCh] = vInt
	l.emit(Instr{Op: OpLoadByte, Dst: hiCh, A: digits, B: hiNib})
	loCh := l.newReg()
	l.regTy[loCh] = vInt
	l.emit(Instr{Op: OpLoadByte, Dst: loCh, A: digits, B: loNib})

	at := l.arith(OpMul, cur, l.constant(2))
	l.storeByte(out, at, hiCh)
	l.storeByte(out, l.arith(OpAdd, at, l.constant(1)), loCh)

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, cur, l.constant(1)), Dst: NoReg, Imm: i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(done)

	l.storeByte(out, l.arith(OpMul, n, l.constant(2)), l.constant(0))
	return out
}

// renderBytes is the rendering the Go backend produces: bytes(hex).
func (l *lowerer) renderBytes(b Reg) {
	l.writeLit("bytes(")
	l.emitStr(l.bytesHexStr(b))
	l.writeLit(")")
}

// readBytes is readFile without the terminator, and without stopping at
// a zero byte. That difference is the reason bytes exists.
func (l *lowerer) readBytes(c *Call) Reg {
	t := vResultOf(vBytes)
	path := l.expr(c.Args[0])

	out := l.temp(t)
	fail, done := l.newLabel(), l.newLabel()

	h := l.openFile(path, genericRead, openExisting)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, h, l.constant(invalidHandle)),
		Dst: NoReg, Imm: fail})

	sizeOut := l.ptrSlot()
	l.ccall("GetFileSizeEx", []Reg{h, sizeOut}, []vty{vInt, vInt}, vInt, true, false)
	size := l.loadPtr(sizeOut)

	buf := l.bytesAlloc(size)
	readOut := l.ptrSlot()
	l.ccall("ReadFile",
		[]Reg{h, buf, size, readOut, l.constant(0)},
		[]vty{vInt, vBytes, vInt, vInt, vInt}, vInt, true, false)
	l.closeHandle(h)

	l.emit(Instr{Op: OpStore, A: l.resOk(buf, t), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(fail)
	reason := l.why("read", path, l.pathError("open", path))
	l.emit(Instr{Op: OpStore, A: l.resFail(reason, t), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, t)
}

// writeBytes writes the whole block, zero bytes included.
func (l *lowerer) writeBytes(c *Call) Reg {
	t := vResultOf(vVoid)
	path := l.expr(c.Args[0])
	data := l.bytesArg(c, 1)

	out := l.temp(t)
	fail, done := l.newLabel(), l.newLabel()

	h := l.openFile(path, genericWrite, createAlways)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, h, l.constant(invalidHandle)),
		Dst: NoReg, Imm: fail})

	writtenOut := l.ptrSlot()
	l.ccall("WriteFile",
		[]Reg{h, data, l.bytesLen(data), writtenOut, l.constant(0)},
		[]vty{vInt, vBytes, vInt, vInt, vInt}, vInt, true, false)
	l.closeHandle(h)

	l.emit(Instr{Op: OpStore, A: l.resOk(l.constant(0), t), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(fail)
	l.emit(Instr{Op: OpStore, A: l.resFail(l.pathError("open", path), t),
		Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, t)
}
