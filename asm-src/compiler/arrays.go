package main

import "strings"

// Fixed-size arrays: [N]T, N C scalars in a row. See frontend/array.go
// for the rules; this is how they run.
//
// An array value is the address of its first element, tracked like a
// pointer: kind kInt with the type's spelling, "[16]f32", in name. So
// it passes unchanged wherever an address goes - an extern's ptr or *T,
// mem.* - and reading an element is the same load a pointer index is.
// What makes it a value rather than a pointer is that every place that
// holds one has storage of its own, and binding, assigning, passing or
// returning copies the bytes into it:
//
//	a local                 its function's array area: part of the frame,
//	                        just above the outgoing arguments, at a fixed
//	                        offset per declaration (OpFrameAddr)
//	a parameter             the same, filled from the caller's on entry
//	a struct field          inside the struct, after the pointer fields
//	a global, or a local    a collector-owned block, since it outlives
//	  a closure captures      the frame
//	a returned value        a fresh block, copied into the caller's frame
//	                        as soon as the call returns
//	a list element          inline in the list's element block, one
//	                        after another, as a struct's fields are
//	a map value, or the     a block of its own that the container
//	  inside of a ?T or T!    points to, made when the array is stored
//
// A local array therefore costs no allocation, and neither does one in
// a struct. Storage is rounded up to whole words, so copying and zeroing
// work a word at a time.

// arrayFrameMax is the largest array kept in a frame. A bigger one goes
// in a block of its own, rather than risking a stack of 1 MB on Windows.
const arrayFrameMax = 64 << 10

// arrayWordsInline is the most words copied or zeroed by straight-line
// moves; beyond it, memmove or memset.
const arrayWordsInline = 16

func isArray(t vty) bool {
	return t.k == kInt && strings.HasPrefix(t.name, "[") && !t.res && !t.null
}

// arrayShape is an array type's element, as a one-field layout for
// viewRead and viewWrite, its length, the element's size, and the bytes
// its storage takes.
func arrayShape(t vty) (f structField, n, size, bytes int64) {
	at := ParseType(t.name)
	if at == nil || at.Kind != KArray {
		return f, 0, 0, 0
	}
	f = structField{name: "[]", kind: cKinds[at.Name], t: cScalarVty(at.Name)}
	n, size = int64(at.Len), int64(CTypeSize(at.Name))
	return f, n, size, (n*size + wordSize - 1) &^ (wordSize - 1)
}

// arrayStorage is room for one array of type t, not yet filled in: in
// the frame, or a collector-owned block when heap is set or it is too
// big for one.
func (l *lowerer) arrayStorage(t vty, heap bool) Reg {
	_, _, _, bytes := arrayShape(t)
	if heap || bytes > arrayFrameMax {
		return l.retype(l.bytesAlloc(l.constant(bytes)), t)
	}
	off := l.fn.ArrayBytes
	l.fn.ArrayBytes += bytes
	d := l.newReg()
	l.regTy[d] = t
	l.emit(Instr{Op: OpFrameAddr, Dst: d, A: NoReg, B: NoReg, Imm: off})
	return d
}

// arrayZero clears the storage at dst.
func (l *lowerer) arrayZero(dst Reg, t vty) {
	_, _, _, bytes := arrayShape(t)
	if bytes/wordSize > arrayWordsInline {
		l.copyZero(dst, l.constant(bytes))
		return
	}
	zero := l.constant(0)
	for off := int64(0); off < bytes; off += wordSize {
		l.pokeWord(dst, off, zero)
	}
}

// arrayCopy copies the array at src into the storage at dst. Two
// arrays never partly overlap, and a copy onto itself, a = a, reads
// each word before writing it back, so word by word is safe.
func (l *lowerer) arrayCopy(dst, src Reg, t vty) {
	_, _, _, bytes := arrayShape(t)
	if bytes/wordSize > arrayWordsInline {
		l.copyBytes(dst, src, l.constant(bytes))
		return
	}
	for off := int64(0); off < bytes; off += wordSize {
		l.pokeWord(dst, off, l.peekWord(src, off))
	}
}

// arrayFill fills the storage at dst from e: an array literal is
// written in place, anything else copied.
func (l *lowerer) arrayFill(dst Reg, e Expr, t vty) {
	lit, ok := e.(*ListLit)
	if !ok {
		l.arrayCopy(dst, l.expr(e), t)
		return
	}
	f, _, size, _ := arrayShape(t)
	vals := make([]Reg, len(lit.Elems))
	for i, el := range lit.Elems {
		vals[i] = l.rvalueAs(el, f.t)
	}
	l.arrayZero(dst, t)
	for i, v := range vals {
		l.viewWrite(lit, l.arith(OpAdd, dst, l.constant(int64(i)*size)), f, v)
	}
}

// arrayTemp is a fresh array holding what e evaluates to.
func (l *lowerer) arrayTemp(e Expr, t vty) Reg {
	dst := l.arrayStorage(t, false)
	l.arrayFill(dst, e, t)
	return dst
}

// letArray lowers `let a: [N]T = v`: storage of its own, filled.
func (l *lowerer) letArray(st *LetStmt, t vty) {
	boxed := l.boxNames[st.Name]
	dst := l.arrayStorage(t, boxed)
	l.arrayFill(dst, st.Value, t)
	slot := l.declareMaybeBoxed(st.Name, t, boxed)
	l.storeLocal(slot, dst)
}

// arrayParam gives an array parameter storage of its own, filled from
// the caller's: the callee may write to it, and the caller's must not
// see that.
func (l *lowerer) arrayParam(slot int64, t vty) {
	dst := l.arrayStorage(t, l.boxed[slot])
	l.arrayCopy(dst, l.loadLocal(slot), t)
	l.storeLocal(slot, dst)
}

// arrayReturn is what a function returning an array hands back: a copy
// in a block of its own, since its frame is about to go.
func (l *lowerer) arrayReturn(v Reg, t vty) Reg {
	dst := l.arrayStorage(t, true)
	l.arrayCopy(dst, v, t)
	return dst
}

// arrayElemAddr is the address of a[i], checked against the length.
func (l *lowerer) arrayElemAddr(a, i Reg) (Reg, structField) {
	f, n, size, _ := arrayShape(l.regTy[a])
	l.checkIndex(i, l.constant(n))
	return l.ptrElemAddr(a, i, size), f
}

// arrayIndexRead lowers a[i].
func (l *lowerer) arrayIndexRead(x *Index, a Reg) Reg {
	at, f := l.arrayElemAddr(a, l.expr(x.Idx))
	return l.viewRead(at, f)
}

// arrayIndexAssign lowers a[i] = v and a[i] op= v.
func (l *lowerer) arrayIndexAssign(st *AssignStmt, a Reg, idx Expr) {
	at, f := l.arrayElemAddr(a, l.expr(idx))
	v := l.rvalueAs(st.Value, f.t)
	if st.Op != ASSIGN {
		applied := false
		v, applied = l.compound(st, st.Op, f.t, l.viewRead(at, f), v)
		if !applied {
			return
		}
	}
	l.viewWrite(st, at, f, v)
}

// arrayEqual compares two arrays element by element, so a float
// element compares as == does on floats: 0.0 equals -0.0 and NaN equals
// nothing.
func (l *lowerer) arrayEqual(n Node, a, b Reg, t vty) Reg {
	f, count, size, _ := arrayShape(t)
	as, bs := l.temp(vInt), l.temp(vInt)
	l.setSlot(as, a)
	l.setSlot(bs, b)
	out := l.temp(vBool)
	l.setSlot(out, l.boolConst(false))
	i := l.scalarTemp(vInt)
	l.setSlot(i, l.constant(0))
	top, same, differ := l.newLabel(), l.newLabel(), l.newLabel()
	l.mark(top)
	l.jumpNot(l.compare(OpLt, l.load(i, vInt), l.constant(count)), same)
	x := l.viewRead(l.ptrElemAddr(l.load(as, vInt), l.load(i, vInt), size), f)
	y := l.viewRead(l.ptrElemAddr(l.load(bs, vInt), l.load(i, vInt), size), f)
	eq, _ := l.deepEqual(n, x, y, f.t)
	l.jumpNot(eq, differ)
	l.setSlot(i, l.arith(OpAdd, l.load(i, vInt), l.constant(1)))
	l.jump(top)
	l.mark(same)
	l.setSlot(out, l.boolConst(true))
	l.mark(differ)
	return l.load(out, vBool)
}

// writeArray renders an array the way a list prints: [1, 2, 3].
func (l *lowerer) writeArray(n Node, a Reg, t vty) {
	f, count, size, _ := arrayShape(t)
	base := l.temp(vInt)
	l.setSlot(base, a)
	i := l.scalarTemp(vInt)
	l.setSlot(i, l.constant(0))
	l.writeLit("[")
	top, done, first := l.newLabel(), l.newLabel(), l.newLabel()
	l.mark(top)
	l.jumpNot(l.compare(OpLt, l.load(i, vInt), l.constant(count)), done)
	l.jumpNot(l.compare(OpNe, l.load(i, vInt), l.constant(0)), first)
	l.writeLit(", ")
	l.mark(first)
	at := l.ptrElemAddr(l.load(base, vInt), l.load(i, vInt), size)
	l.writeValue(n, l.viewRead(at, f), f.t)
	l.setSlot(i, l.arith(OpAdd, l.load(i, vInt), l.constant(1)))
	l.jump(top)
	l.mark(done)
	l.writeLit("]")
}

// bindElemTrip binds a loop variable to the element at v, which is an
// array when it lives in a list's or a map's storage. The variable gets
// a copy, as Go's range does: the body may push onto the list, which
// moves the block, and a write to the variable is not a write to the
// container. A variable a closure captured is given a block of its own
// by bindTrip.
func (l *lowerer) bindElemTrip(slot int64, v Reg, t vty, name string) {
	if isArray(t) && !l.boxed[slot] {
		own := l.arrayStorage(t, false)
		l.arrayCopy(own, v, t)
		v = own
	}
	l.bindTrip(slot, v, name)
}

// forArray lowers `for x in a` and `for i, x in a`. The array is read
// once, like a list; its length is a constant.
func (l *lowerer) forArray(st *ForStmt, a Reg, t vty) {
	f, count, size, _ := arrayShape(t)
	l.pushScope()
	base := l.temp(vInt)
	l.setSlot(base, a)
	iSlot := l.scalarTemp(vInt)
	l.setSlot(iSlot, l.constant(0))
	idxSlot := int64(-1)
	varName := st.Var
	if st.Var2 != "" {
		idxSlot = l.declareLoopVar(st.Var, vInt)
		varName = st.Var2
	}
	varSlot := l.declareLoopVar(varName, f.t)

	top, cont, done := l.newLabel(), l.newLabel(), l.newLabel()
	l.mark(top)
	l.jumpNot(l.compare(OpLt, l.load(iSlot, vInt), l.constant(count)), done)
	i := l.load(iSlot, vInt)
	if idxSlot >= 0 {
		l.bindTrip(idxSlot, i, st.Var)
	}
	l.bindTrip(varSlot, l.viewRead(l.ptrElemAddr(l.load(base, vInt), i, size), f), varName)

	l.loops = append(l.loops, loopTarget{brk: done, cont: cont, defers: len(l.defers)})
	l.stmt(st.Body)
	l.loops = l.loops[:len(l.loops)-1]

	l.mark(cont)
	l.setSlot(iSlot, l.arith(OpAdd, l.load(iSlot, vInt), l.constant(1)))
	l.jump(top)
	l.mark(done)
	l.popScope()
}

// storeField writes a struct field: one word, or for an array field
// its bytes, copied in.
func (l *lowerer) storeField(obj Reg, f structField, v Reg) {
	if isArray(f.t) {
		l.arrayCopy(l.arith(OpAdd, obj, l.constant(f.off)), v, f.t)
		return
	}
	l.emit(Instr{Op: OpStoreMem, A: obj, B: v, Imm: f.off, Comment: "." + f.name})
}

// zeroArray is a zeroed array in the frame, for a struct field a
// literal leaves out.
func (l *lowerer) zeroArray(t vty) Reg {
	dst := l.arrayStorage(t, false)
	l.arrayZero(dst, t)
	return dst
}

// anyArray reports whether a signature takes or returns an array.
func anyArray(params []vty, ret vty) bool {
	for _, p := range params {
		if isArray(p) {
			return true
		}
	}
	return isArray(ret)
}

// jsonArray encodes an array as a JSON array of its elements, on one
// line: they are numbers or booleans, which indenting would only
// stretch out.
func (l *lowerer) jsonArray(n Node, a Reg, t vty, indent bool, depth int) {
	f, count, size, _ := arrayShape(t)
	base := l.temp(vInt)
	l.setSlot(base, a)
	i := l.scalarTemp(vInt)
	l.setSlot(i, l.constant(0))
	l.writeLit("[")
	top, done, first := l.newLabel(), l.newLabel(), l.newLabel()
	l.mark(top)
	l.jumpNot(l.compare(OpLt, l.load(i, vInt), l.constant(count)), done)
	l.jumpNot(l.compare(OpNe, l.load(i, vInt), l.constant(0)), first)
	l.writeLit(",")
	l.mark(first)
	at := l.ptrElemAddr(l.load(base, vInt), l.load(i, vInt), size)
	l.jsonWrite(n, l.viewRead(at, f), f.t, indent, depth)
	l.setSlot(i, l.arith(OpAdd, l.load(i, vInt), l.constant(1)))
	l.jump(top)
	l.mark(done)
	l.writeLit("]")
}
