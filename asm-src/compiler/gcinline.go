package main

import "os"

// Collector support for data blocks that hold structs inline.
//
// A list of structs that keeps its elements in the data block is a run
// of equal elements whose first nptr words are pointers (tagStructs, see
// struct.go), so marking is a loop inside a loop.
//
// It is behind lowerer.gcInline. A program that never builds such a
// block does not get the code, so the collector and the binary stay the
// size they were.

// gcInlineWanted reports whether this build emits the support. Nothing
// but the test hook asks for it yet.
func gcInlineWanted() bool {
	return os.Getenv("SCANDIACUS_TESTHOOKS") != ""
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
	return obj
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
