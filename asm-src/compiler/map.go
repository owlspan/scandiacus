package main

// Maps, built out of the raw memory ops like lists, and for the same
// reason: nothing here is hand-written assembly, so the byte writer
// inherits all of it without a second implementation.
//
// A map value is a pointer to a four-word header:
//
//	[ptr+0]   length
//	[ptr+8]   capacity
//	[ptr+16]  pointer to the keys
//	[ptr+24]  pointer to the values
//
// Keys and values live in two parallel blocks rather than interleaved
// pairs, so each block can carry its own tag - a {str: int} has
// pointers in the keys and plain integers in the values, and a
// collector needs to be told which is which.
//
// # Why sorted rather than hashed
//
// The entries are kept sorted by key, and a lookup is a binary search:
// O(log n), where a hash table would be O(1). An insert or a removal
// moves the entries after it with one memmove, which is O(n) but a
// single pass over memory rather than a loop of loads and stores.
//
// The reason is the Go backend. It sorts keys when it prints or
// iterates a map, so that output is stable, and the differential test
// holds this backend to producing the same bytes. A hash table would
// have to sort on every iteration to match. Keeping the array sorted at
// all times makes printing and iterating fall out for free, and moves
// the cost to insertion, which is where a small map spends less of its
// life.
//
// It is also the honest order of work: correct output first, then
// speed. The seam is the six functions below - newMap, mapFind, mapGet,
// mapSet, mapLen and printMap. Swapping the internals for a real hash
// table changes them and nothing that calls them, exactly as lists
// being flat arrays never leaked into their callers.
//
// None of this is ever freed. Same as lists.

const (
	mapLenOff  = 0
	mapCapOff  = 8
	mapKeysOff = 16
	mapValsOff = 24
	mapHeader  = 32
)

// keyTag and valTag say whether each block holds pointers, which is
// what the object header records for a future collector.
func keyTag(t vty) int64 {
	if t.key == kStr {
		return tagPtrs
	}
	return tagWords
}

func valTag(t vty) int64 {
	if t.elemType().holdsPointer() {
		return tagPtrs
	}
	return tagWords
}

// newMap allocates a header and two element blocks.
func (l *lowerer) newMap(t vty, capacity int64) Reg {
	if capacity < 1 {
		capacity = 1
	}

	hdr := l.allocObj(l.constant(mapHeader), tagMap)
	keys := l.allocObj(l.constant(capacity*wordSize), keyTag(t))
	vals := l.allocObj(l.constant(capacity*wordSize), valTag(t))

	l.emit(Instr{Op: OpStoreMem, A: hdr, B: l.constant(0), Imm: mapLenOff})
	l.emit(Instr{Op: OpStoreMem, A: hdr, B: l.constant(capacity), Imm: mapCapOff})
	l.emit(Instr{Op: OpStoreMem, A: hdr, B: keys, Imm: mapKeysOff})
	l.emit(Instr{Op: OpStoreMem, A: hdr, B: vals, Imm: mapValsOff})

	l.regTy[hdr] = t
	return hdr
}

// keyCmp emits a three-way comparison of two keys, negative, zero or
// positive like strcmp. Integer keys subtract; string keys go through
// strcmp itself, which is already linked for string equality.
func (l *lowerer) keyCmp(a, b Reg, kk vkind) Reg {
	if kk == kStr {
		// strcmp returns a C int, so only eax is meaningful.
		return l.ccall("strcmp", []Reg{a, b}, []vty{vStr, vStr}, vInt, true, false)
	}
	return l.arith(OpSub, a, b)
}

// keyLess is a < b on keys. Integer keys compare directly: subtracting
// them, as a three-way comparison would, overflows for two keys far
// enough apart and puts them in the wrong order.
func (l *lowerer) keyLess(a, b Reg, kk vkind) Reg {
	if kk == kStr {
		return l.compare(OpLt, l.keyCmp(a, b, kk), l.constant(0))
	}
	return l.compare(OpLt, a, b)
}

// keyAt loads key i.
func (l *lowerer) keyAt(keys, i Reg, t vty) Reg {
	addr := l.newReg()
	l.regTy[addr] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: addr, A: keys, B: i})
	held := l.newReg()
	l.regTy[held] = l.mapKeyReg(t)
	l.emit(Instr{Op: OpLoadMem, Dst: held, A: addr, B: NoReg, Imm: 0})
	return held
}

// mapScan finds the first entry whose key is not less than the wanted
// one, by binary search over the sorted keys. It writes that index into
// idxSlot and whether it matched into hitSlot.
//
// One search answers both questions a map ever asks: where a key is,
// and where it would go. mapGet needs the first, mapSet needs both, and
// doing it once means the sorted order is maintained in one place.
//
// The search is one function per key kind, called from every get, set
// and remove. It answers both halves in one word, the index shifted up
// one and the hit in bit 0, so the call needs no second result.
func (l *lowerer) mapScan(m, key Reg, t vty, idxSlot, hitSlot int64) {
	kt := vty{k: t.key}
	sym := l.helperFunc("__map_scan "+kt.String(), []vty{vInt, kt}, vInt, func(a []Reg) {
		idx := l.temp(vInt)
		hit := l.temp(vInt)
		l.mapScanBody(a[0], a[1], t, idx, hit)
		packed := l.arith(OpBOr, l.arith(OpShl, l.load(idx, vInt), l.constant(1)), l.load(hit, vInt))
		l.emit(Instr{Op: OpRet, A: packed, Dst: NoReg})
	})
	packed := l.callHelper(sym, []Reg{m, key}, []vty{vInt, kt}, vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpShr, packed, l.constant(1)), Dst: NoReg, Imm: idxSlot})
	l.emit(Instr{Op: OpStore, A: l.arith(OpBAnd, packed, l.constant(1)), Dst: NoReg, Imm: hitSlot})
}

func (l *lowerer) mapScanBody(m, key Reg, t vty, idxSlot, hitSlot int64) {
	length := l.field(m, mapLenOff, vInt)
	keys := l.field(m, mapKeysOff, vInt)

	hiSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: idxSlot})
	l.emit(Instr{Op: OpStore, A: length, Dst: NoReg, Imm: hiSlot})
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: hitSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	lo := l.load(idxSlot, vInt)
	hi := l.load(hiSlot, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, lo, hi), Dst: NoReg, Imm: done})

	// Both ends are small and non-negative, so the sum cannot overflow
	// and the shift halves it.
	mid := l.arith(OpShr, l.arith(OpAdd, lo, hi), l.constant(1))
	below := l.keyLess(l.keyAt(keys, mid, t), key, t.key)
	goHigh := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: below, Dst: NoReg, Imm: goHigh})
	l.emit(Instr{Op: OpStore, A: mid, Dst: NoReg, Imm: hiSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(goHigh)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, mid, l.constant(1)), Dst: NoReg, Imm: idxSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)

	// The search stops on the first entry >= the wanted key, or past
	// the end. Only the first can be a hit, and only if it is equal.
	after := l.newLabel()
	j := l.load(idxSlot, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, j, length), Dst: NoReg, Imm: after})
	eq := l.compare(OpEq, l.keyCmp(l.keyAt(keys, j, t), key, t.key), l.constant(0))
	if t.key != kStr {
		eq = l.compare(OpEq, l.keyAt(keys, j, t), key)
	}
	l.emit(Instr{Op: OpJumpNot, A: eq, Dst: NoReg, Imm: after})
	l.emit(Instr{Op: OpStore, A: l.constant(1), Dst: NoReg, Imm: hitSlot})

	l.mark(after)
}

// mapKeyReg is the vty a loaded key should carry.
func (l *lowerer) mapKeyReg(t vty) vty { return vty{k: t.key} }

// mapGet returns the value for a key, or the zero value when the key is
// absent. That is the Go backend's behaviour and so it is the
// definition: a missing int key reads 0, a missing str key reads "".
//
// One function per map type, called from every read.
func (l *lowerer) mapGet(n Node, m, key Reg, t vty) Reg {
	kt := vty{k: t.key}
	sym := l.helperFunc("__map_get "+t.String(), []vty{t, kt}, t.elemType(), func(a []Reg) {
		l.emit(Instr{Op: OpRet, A: l.mapGetBody(n, a[0], a[1], t), Dst: NoReg})
	})
	return l.callHelper(sym, []Reg{m, key}, []vty{t, kt}, t.elemType())
}

func (l *lowerer) mapGetBody(n Node, m, key Reg, t vty) Reg {
	idxSlot := l.temp(vInt)
	hitSlot := l.temp(vInt)
	l.mapScan(m, key, t, idxSlot, hitSlot)

	// The zero for an absent key is the same zero a struct field gets,
	// which for a container is a real empty one rather than a null
	// pointer. Once a map can hold a list, reading a missing key and
	// then asking its length has to answer 0, not fault.
	out := l.temp(t.elemType())
	l.emit(Instr{Op: OpStore, A: l.zeroOf(n, t.elemType(), 0), Dst: NoReg, Imm: out})

	done := l.newLabel()
	hit := l.newReg()
	l.regTy[hit] = vInt
	l.emit(Instr{Op: OpLoad, Dst: hit, A: NoReg, B: NoReg, Imm: hitSlot})
	found := l.compare(OpNe, hit, l.constant(0))
	l.emit(Instr{Op: OpJumpNot, A: found, Dst: NoReg, Imm: done})

	vals := l.field(m, mapValsOff, vInt)
	i := l.newReg()
	l.regTy[i] = vInt
	l.emit(Instr{Op: OpLoad, Dst: i, A: NoReg, B: NoReg, Imm: idxSlot})
	addr := l.newReg()
	l.regTy[addr] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: addr, A: vals, B: i})
	v := l.newReg()
	l.regTy[v] = t.elemType()
	l.emit(Instr{Op: OpLoadMem, Dst: v, A: addr, B: NoReg, Imm: 0})
	l.emit(Instr{Op: OpStore, A: v, Dst: NoReg, Imm: out})

	l.mark(done)

	d := l.newReg()
	l.regTy[d] = t.elemType()
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: out})
	return d
}

// mapSet inserts or overwrites, keeping the entries sorted by key.
//
// One function per map type, called from every write.
func (l *lowerer) mapSet(m, key, val Reg, t vty) {
	kt := vty{k: t.key}
	ps := []vty{t, kt, t.elemType()}
	sym := l.helperFunc("__map_set "+t.String(), ps, vVoid, func(a []Reg) {
		l.mapSetBody(a[0], a[1], a[2], t)
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
	l.callHelper(sym, []Reg{m, key, val}, ps, vVoid)
}

func (l *lowerer) mapSetBody(m, key, val Reg, t vty) {
	idxSlot := l.temp(vInt)
	hitSlot := l.temp(vInt)
	l.mapScan(m, key, t, idxSlot, hitSlot)

	store := l.newLabel()
	hit := l.newReg()
	l.regTy[hit] = vInt
	l.emit(Instr{Op: OpLoad, Dst: hit, A: NoReg, B: NoReg, Imm: hitSlot})
	found := l.compare(OpNe, hit, l.constant(0))
	l.emit(Instr{Op: OpJumpIf, A: found, Dst: NoReg, Imm: store, Comment: "overwrite"})

	// A new key. Grow if the blocks are full, then shift everything from
	// the insertion point one place right to open a gap.
	l.mapGrow(m, t)
	l.mapShiftRight(m, idxSlot)

	length := l.field(m, mapLenOff, vInt)
	l.emit(Instr{Op: OpStoreMem, A: m, B: l.arith(OpAdd, length, l.constant(1)), Imm: mapLenOff})

	keys := l.field(m, mapKeysOff, vInt)
	i0 := l.newReg()
	l.regTy[i0] = vInt
	l.emit(Instr{Op: OpLoad, Dst: i0, A: NoReg, B: NoReg, Imm: idxSlot})
	kaddr := l.newReg()
	l.regTy[kaddr] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: kaddr, A: keys, B: i0})
	l.emit(Instr{Op: OpStoreMem, A: kaddr, B: key, Imm: 0})

	l.mark(store)

	vals := l.field(m, mapValsOff, vInt)
	i := l.newReg()
	l.regTy[i] = vInt
	l.emit(Instr{Op: OpLoad, Dst: i, A: NoReg, B: NoReg, Imm: idxSlot})
	vaddr := l.newReg()
	l.regTy[vaddr] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: vaddr, A: vals, B: i})
	l.emit(Instr{Op: OpStoreMem, A: vaddr, B: val, Imm: 0})
}

// mapGrow doubles both blocks when they are full. Both are the same
// length always, so one capacity governs the pair.
func (l *lowerer) mapGrow(m Reg, t vty) {
	length := l.field(m, mapLenOff, vInt)
	capacity := l.field(m, mapCapOff, vInt)

	ready := l.newLabel()
	room := l.compare(OpLt, length, capacity)
	l.emit(Instr{Op: OpJumpIf, A: room, Dst: NoReg, Imm: ready, Comment: "map: room?"})

	newCap := l.arith(OpMul, capacity, l.constant(2))
	bytes := l.arith(OpMul, newCap, l.constant(wordSize))

	freshK := l.allocObj(bytes, keyTag(t))
	freshV := l.allocObj(bytes, valTag(t))
	l.copyWords(l.field(m, mapKeysOff, vInt), freshK, length)
	l.copyWords(l.field(m, mapValsOff, vInt), freshV, length)

	l.emit(Instr{Op: OpStoreMem, A: m, B: freshK, Imm: mapKeysOff})
	l.emit(Instr{Op: OpStoreMem, A: m, B: freshV, Imm: mapValsOff})
	l.emit(Instr{Op: OpStoreMem, A: m, B: newCap, Imm: mapCapOff})

	l.mark(ready)
}

// copyWords copies n words from one block to another, front to back.
func (l *lowerer) copyWords(from, to, n Reg) {
	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	i := l.newReg()
	l.regTy[i] = vInt
	l.emit(Instr{Op: OpLoad, Dst: i, A: NoReg, B: NoReg, Imm: iSlot})
	more := l.compare(OpLt, i, n)
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})

	src := l.newReg()
	l.regTy[src] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: src, A: from, B: i})
	v := l.newReg()
	l.regTy[v] = vInt
	l.emit(Instr{Op: OpLoadMem, Dst: v, A: src, B: NoReg, Imm: 0})
	dst := l.newReg()
	l.regTy[dst] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: dst, A: to, B: i})
	l.emit(Instr{Op: OpStoreMem, A: dst, B: v, Imm: 0})

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(done)
}

// mapShiftRight opens a one-slot gap at idxSlot by moving every entry
// from there to the end one place right, with memmove, which copes
// with the two ranges overlapping.
func (l *lowerer) mapShiftRight(m Reg, idxSlot int64) {
	length := l.field(m, mapLenOff, vInt)
	at := l.load(idxSlot, vInt)
	count := l.arith(OpMul, l.arith(OpSub, length, at), l.constant(wordSize))
	for _, off := range []int64{mapKeysOff, mapValsOff} {
		block := l.field(m, off, vInt)
		from := l.arith(OpAdd, block, l.arith(OpMul, at, l.constant(wordSize)))
		l.ccall("memmove", []Reg{l.arith(OpAdd, from, l.constant(wordSize)), from, count},
			[]vty{vInt, vInt, vInt}, vInt, false, false)
	}
}

// printMap writes a map the way the Go backend does:
//
//	{"a": 1, "b": 2}
//
// with string keys quoted and entries in sorted order, which they are
// already in. An empty map is {}.
func (l *lowerer) writeMap(n Node, m Reg, t vty) {
	length := l.field(m, mapLenOff, vInt)
	keys := l.field(m, mapKeysOff, vInt)
	vals := l.field(m, mapValsOff, vInt)

	l.mod.needs("write")
	l.writeLit("{")

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

	// Separator before every entry but the first.
	noComma := l.newLabel()
	first := l.compare(OpEq, i, l.constant(0))
	l.emit(Instr{Op: OpJumpIf, A: first, Dst: NoReg, Imm: noComma})
	l.writeLit(", ")
	l.mark(noComma)

	kaddr := l.newReg()
	l.regTy[kaddr] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: kaddr, A: keys, B: i})
	k := l.newReg()
	l.regTy[k] = l.mapKeyReg(t)
	l.emit(Instr{Op: OpLoadMem, Dst: k, A: kaddr, B: NoReg, Imm: 0})

	if t.key == kStr {
		l.writeLit("\"")
		l.emitStr(k)
		l.writeLit("\"")
	} else {
		l.emitInt(k)
	}
	l.writeLit(": ")

	vaddr := l.newReg()
	l.regTy[vaddr] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: vaddr, A: vals, B: i})
	v := l.newReg()
	l.regTy[v] = t.elemType()
	l.emit(Instr{Op: OpLoadMem, Dst: v, A: vaddr, B: NoReg, Imm: 0})
	l.writeValue(n, v, t.elemType())

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	l.writeLit("}")
}

// printMap is the same map on a line of its own, separate from
// writeMap for the same reason printList is.
func (l *lowerer) printMap(n Node, v Reg, t vty) {
	l.writeValue(n, v, t)
	l.writeLit("\n")
}

// keys and values copy a map's blocks out as lists.
//
// The Go backend sorts both, for the same reason map iteration there is
// sorted: a program whose output changes between runs is a bad first
// experience. Here they come out sorted with nothing to do, because
// sorted is how the map is stored - the one place this representation
// pays off against a hash table.
func (l *lowerer) mapBlockToList(m Reg, t vty, off int64, elem vty) Reg {
	list := l.newList(vListOf(elem), initialCap)
	block := l.field(m, off, vInt)

	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	i := l.load(iSlot, vInt)
	more := l.compare(OpLt, i, l.field(m, mapLenOff, vInt))
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})

	addr := l.newReg()
	l.regTy[addr] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: addr, A: block, B: i})
	v := l.newReg()
	l.regTy[v] = elem
	l.emit(Instr{Op: OpLoadMem, Dst: v, A: addr, B: NoReg, Imm: 0})
	l.listPush(list, v)

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	return list
}

// mapRemove deletes a key, closing the gap so the entries stay sorted
// and contiguous. A key that is not there is not an error, which is what
// Go's delete does.
func (l *lowerer) mapRemove(m, key Reg, t vty) {
	idxSlot := l.temp(vInt)
	hitSlot := l.temp(vInt)
	l.mapScan(m, key, t, idxSlot, hitSlot)

	done := l.newLabel()
	hit := l.load(hitSlot, vInt)
	found := l.compare(OpNe, hit, l.constant(0))
	l.emit(Instr{Op: OpJumpNot, A: found, Dst: NoReg, Imm: done})

	length := l.field(m, mapLenOff, vInt)

	// Close the hole: everything after it moves one place left.
	at := l.load(idxSlot, vInt)
	count := l.arith(OpMul, l.arith(OpSub, l.arith(OpSub, length, at), l.constant(1)),
		l.constant(wordSize))
	for _, off := range []int64{mapKeysOff, mapValsOff} {
		block := l.field(m, off, vInt)
		to := l.arith(OpAdd, block, l.arith(OpMul, at, l.constant(wordSize)))
		l.ccall("memmove", []Reg{to, l.arith(OpAdd, to, l.constant(wordSize)), count},
			[]vty{vInt, vInt, vInt}, vInt, false, false)
	}
	l.emit(Instr{Op: OpStoreMem, A: m, B: l.arith(OpSub, length, l.constant(1)),
		Imm: mapLenOff})

	l.mark(done)
}

// mapBuiltin lowers keys, values, remove and clear.
func (l *lowerer) mapBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "keys", "values", "remove", "clear":
	default:
		return NoReg, false
	}

	m := l.expr(c.Args[0])
	t := l.regTy[m]

	// clear is the one of these that also takes a list. Emptying either
	// is a length of zero: the elements are unreachable after it, and
	// nothing is freed here anyway.
	if name == "clear" {
		switch t.k {
		case kMap:
			l.emit(Instr{Op: OpStoreMem, A: m, B: l.constant(0), Imm: mapLenOff})
			return l.void(), true
		case kList:
			l.emit(Instr{Op: OpStoreMem, A: m, B: l.constant(0), Imm: listLenOff})
			return l.void(), true
		}
		l.errorAt(c, "clear expects a list or a map, got %s", t)
		return l.junk(), true
	}

	if t.k != kMap {
		l.errorAt(c, "%s expects a map, got %s", name, t)
		return l.junk(), true
	}

	switch name {
	case "keys":
		return l.mapBlockToList(m, t, mapKeysOff, t.keyType()), true
	case "values":
		return l.mapBlockToList(m, t, mapValsOff, t.elemType()), true
	}

	key := l.expr(c.Args[1])
	if l.regTy[key].k != t.key {
		l.errorAt(c, "this map is keyed by %s, but the key is %s", t.keyType(), l.regTy[key])
		return l.junk(), true
	}
	l.mapRemove(m, key, t)
	return l.void(), true
}

// forMap lowers `for k, v in m`, and `for k in m` when only one name is
// given.
//
// The entries come out in key order because that is how they are
// stored - the Go backend has to sort on every iteration to promise the
// same thing.
//
// The length is read once, before the first iteration, matching the Go
// backend's range over a sorted key slice: inserting inside the loop
// does not make it run longer. Unlike a list, the blocks themselves can
// move when the map grows, so each iteration re-reads them through the
// header.
func (l *lowerer) forMap(st *ForStmt, m Reg, t vty) {
	l.pushScope()

	mapSlot := l.temp(t)
	l.emit(Instr{Op: OpStore, A: m, Dst: NoReg, Imm: mapSlot, Comment: "for ... in"})

	held := l.load(mapSlot, t)
	lenSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.field(held, mapLenOff, vInt), Dst: NoReg, Imm: lenSlot})

	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	keySlot := l.declare(st.Var, t.keyType())
	valSlot := int64(-1)
	if st.Var2 != "" {
		valSlot = l.declare(st.Var2, t.elemType())
	}

	top := l.newLabel()
	cont := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	i := l.load(iSlot, vInt)
	more := l.compare(OpLt, i, l.load(lenSlot, vInt))
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})

	cur := l.load(mapSlot, t)
	l.emit(Instr{Op: OpStore, A: l.cellAt(cur, mapKeysOff, i, t.keyType()),
		Dst: NoReg, Imm: keySlot, Comment: st.Var})
	if valSlot >= 0 {
		l.emit(Instr{Op: OpStore, A: l.cellAt(cur, mapValsOff, i, t.elemType()),
			Dst: NoReg, Imm: valSlot, Comment: st.Var2})
	}

	l.loops = append(l.loops, loopTarget{brk: done, cont: cont, defers: len(l.defers)})
	l.stmt(st.Body)
	l.loops = l.loops[:len(l.loops)-1]

	l.mark(cont)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(iSlot, vInt), l.constant(1)),
		Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	l.popScope()
}

// cellAt reads one entry out of a map's key or value block.
func (l *lowerer) cellAt(m Reg, off int64, i Reg, elem vty) Reg {
	block := l.field(m, off, vInt)
	addr := l.newReg()
	l.regTy[addr] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: addr, A: block, B: i})
	d := l.newReg()
	l.regTy[d] = elem
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: addr, B: NoReg, Imm: 0})
	return d
}
