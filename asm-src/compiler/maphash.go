package main

// The hash index behind a map.
//
// The entries are two parallel arrays, keys and values, as they always
// were. What finds one is an open-addressed table of entry numbers:
// slot i holds 0 when empty, -1 when an entry was removed from it, and
// n+1 when it points at entry n. The table has twice as many slots as
// the arrays have room for, so it is never more than half full of live
// entries, and it is rebuilt before live entries and removed ones
// together fill three quarters of it - a probe always reaches an empty
// slot and stops.
//
// Order. Printing, iterating and keys() give entries sorted by key,
// which the language promises. A new key is appended rather than
// inserted in place, which is what makes insertion O(1) instead of a
// memmove of half the map, so the arrays fall out of order and a flag
// says so. Anything that walks the map in order sorts it first if the
// flag is up - a heap sort, then a rebuild of the table - and a map
// built in key order, or never walked, never pays for one.
//
// Each piece is one helper per key kind (int or str), or per map type
// where the collector tags of freshly allocated blocks depend on it.

// mapHelperKey names a helper per key kind.
func mapHelperKey(name string, t vty) string { return name + " " + vty{k: t.key}.String() }

// wordAt and setWordAt read and write word i of a block.
func (l *lowerer) wordAt(block, i Reg, t vty) Reg {
	addr := l.newReg()
	l.regTy[addr] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: addr, A: block, B: i})
	v := l.newReg()
	l.regTy[v] = t
	l.emit(Instr{Op: OpLoadMem, Dst: v, A: addr, B: NoReg, Imm: 0})
	return v
}

func (l *lowerer) setWordAt(block, i, v Reg) {
	addr := l.newReg()
	l.regTy[addr] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: addr, A: block, B: i})
	l.emit(Instr{Op: OpStoreMem, A: addr, B: v, Imm: 0})
}

func (l *lowerer) jump(label int64) {
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: label})
}

func (l *lowerer) jumpIf(cond Reg, label int64) {
	l.emit(Instr{Op: OpJumpIf, A: cond, Dst: NoReg, Imm: label})
}

func (l *lowerer) jumpNot(cond Reg, label int64) {
	l.emit(Instr{Op: OpJumpNot, A: cond, Dst: NoReg, Imm: label})
}

func (l *lowerer) setSlot(slot int64, v Reg) {
	l.emit(Instr{Op: OpStore, A: v, Dst: NoReg, Imm: slot})
}

func (l *lowerer) ret(v Reg) {
	l.emit(Instr{Op: OpRet, A: v, Dst: NoReg})
}

// keyEq is a == b on keys.
func (l *lowerer) keyEq(a, b Reg, kk vkind) Reg {
	if kk == kStr {
		return l.compare(OpEq, l.keyCmp(a, b, kk), l.constant(0))
	}
	return l.compare(OpEq, a, b)
}

// mapHash spreads a key over 64 bits. An int is multiplied by the
// golden ratio and folded, so keys that differ only in their high bits,
// or step by a power of two, still land in different slots; a string
// is FNV-1a over its bytes.
func (l *lowerer) mapHash(key Reg, t vty) Reg {
	kt := vty{k: t.key}
	sym := l.helperFunc(mapHelperKey("__map_hash", t), []vty{kt}, vInt, func(a []Reg) {
		if t.key != kStr {
			h := l.arith(OpMul, a[0], l.constant(-7046029254386353131))
			l.ret(l.arith(OpBXor, h, l.arith(OpShr, h, l.constant(32))))
			return
		}
		h := l.temp(vInt)
		i := l.temp(vInt)
		l.setSlot(h, l.constant(-3750763034362895579))
		l.setSlot(i, l.constant(0))
		top, done := l.newLabel(), l.newLabel()
		l.mark(top)
		b := l.newReg()
		l.regTy[b] = vInt
		l.emit(Instr{Op: OpLoadByte, Dst: b, A: a[0], B: l.load(i, vInt)})
		l.jumpIf(l.compare(OpEq, b, l.constant(0)), done)
		l.setSlot(h, l.arith(OpMul, l.arith(OpBXor, l.load(h, vInt), b), l.constant(1099511628211)))
		l.setSlot(i, l.arith(OpAdd, l.load(i, vInt), l.constant(1)))
		l.jump(top)
		l.mark(done)
		l.ret(l.load(h, vInt))
	})
	return l.callHelper(sym, []Reg{key}, []vty{kt}, vInt)
}

// mapFind probes for a key and returns a table slot: the one pointing
// at the key's entry when it is there, and otherwise where it would go,
// which is the first removed slot passed on the way or the empty one
// that ended the probe.
func (l *lowerer) mapFind(m, key Reg, t vty) Reg {
	kt := vty{k: t.key}
	sym := l.helperFunc(mapHelperKey("__map_find", t), []vty{vInt, kt}, vInt, func(a []Reg) {
		m, key := a[0], a[1]
		mask := l.arith(OpSub, l.arith(OpMul, l.field(m, mapCapOff, vInt), l.constant(2)), l.constant(1))
		idx := l.field(m, mapIdxOff, vInt)
		keys := l.field(m, mapKeysOff, vInt)
		i := l.temp(vInt)
		tomb := l.temp(vInt)
		l.setSlot(i, l.arith(OpBAnd, l.mapHash(key, t), mask))
		l.setSlot(tomb, l.constant(-1))

		top, next, empty, removed := l.newLabel(), l.newLabel(), l.newLabel(), l.newLabel()
		l.mark(top)
		cur := l.load(i, vInt)
		e := l.wordAt(idx, cur, vInt)
		l.jumpIf(l.compare(OpEq, e, l.constant(0)), empty)
		l.jumpIf(l.compare(OpLt, e, l.constant(0)), removed)
		found := l.newLabel()
		l.jumpIf(l.keyEq(l.wordAt(keys, l.arith(OpSub, e, l.constant(1)), kt), key, t.key), found)
		l.jump(next)
		l.mark(found)
		l.ret(l.load(i, vInt))

		l.mark(removed)
		l.jumpIf(l.compare(OpGe, l.load(tomb, vInt), l.constant(0)), next)
		l.setSlot(tomb, l.load(i, vInt))

		l.mark(next)
		l.setSlot(i, l.arith(OpBAnd, l.arith(OpAdd, l.load(i, vInt), l.constant(1)), mask))
		l.jump(top)

		l.mark(empty)
		first := l.load(tomb, vInt)
		l.ret(l.pick(l.compare(OpGe, first, l.constant(0)), first, l.load(i, vInt), vInt))
	})
	return l.callHelper(sym, []Reg{m, key}, []vty{vInt, kt}, vInt)
}

// mapReindex clears the table and points it at every entry again, which
// is how removed slots are cleaned out and how the table follows the
// entries after they are sorted or moved into bigger arrays.
func (l *lowerer) mapReindex(m Reg, t vty) {
	sym := l.helperFunc(mapHelperKey("__map_reindex", t), []vty{vInt}, vVoid, func(a []Reg) {
		m := a[0]
		kt := vty{k: t.key}
		slots := l.arith(OpMul, l.field(m, mapCapOff, vInt), l.constant(2))
		l.ccall("memset", []Reg{l.field(m, mapIdxOff, vInt), l.constant(0),
			l.arith(OpMul, slots, l.constant(wordSize))},
			[]vty{vInt, vInt, vInt}, vInt, false, false)
		l.emit(Instr{Op: OpStoreMem, A: m, B: l.constant(0), Imm: mapTombOff})

		j := l.temp(vInt)
		l.setSlot(j, l.constant(0))
		top, done := l.newLabel(), l.newLabel()
		l.mark(top)
		cur := l.load(j, vInt)
		l.jumpNot(l.compare(OpLt, cur, l.field(m, mapLenOff, vInt)), done)
		s := l.mapFind(m, l.wordAt(l.field(m, mapKeysOff, vInt), cur, kt), t)
		l.setWordAt(l.field(m, mapIdxOff, vInt), s, l.arith(OpAdd, l.load(j, vInt), l.constant(1)))
		l.setSlot(j, l.arith(OpAdd, l.load(j, vInt), l.constant(1)))
		l.jump(top)
		l.mark(done)
		l.ret(NoReg)
	})
	l.callHelper(sym, []Reg{m}, []vty{vInt}, vVoid)
}

// newIndex allocates a zeroed table for a map of the given capacity.
func (l *lowerer) newIndex(capacity Reg) Reg {
	bytes := l.arith(OpMul, capacity, l.constant(2*wordSize))
	idx := l.allocObj(bytes, tagWords)
	l.ccall("memset", []Reg{idx, l.constant(0), bytes}, []vty{vInt, vInt, vInt}, vInt, false, false)
	return idx
}

// mapGrow doubles the arrays and the table. The collector tags of the
// new arrays depend on what the map holds, so this one is per map type.
func (l *lowerer) mapGrow(m Reg, t vty) {
	sym := l.helperFunc("__map_grow "+t.String(), []vty{vInt}, vVoid, func(a []Reg) {
		m := a[0]
		length := l.field(m, mapLenOff, vInt)
		newCap := l.arith(OpMul, l.field(m, mapCapOff, vInt), l.constant(2))
		bytes := l.arith(OpMul, newCap, l.constant(wordSize))
		used := l.arith(OpMul, length, l.constant(wordSize))
		freshK := l.allocObj(bytes, keyTag(t))
		freshV := l.allocObj(bytes, valTag(t))
		l.ccall("memmove", []Reg{freshK, l.field(m, mapKeysOff, vInt), used},
			[]vty{vInt, vInt, vInt}, vInt, false, false)
		l.ccall("memmove", []Reg{freshV, l.field(m, mapValsOff, vInt), used},
			[]vty{vInt, vInt, vInt}, vInt, false, false)
		idx := l.newIndex(newCap)
		l.emit(Instr{Op: OpStoreMem, A: m, B: freshK, Imm: mapKeysOff})
		l.emit(Instr{Op: OpStoreMem, A: m, B: freshV, Imm: mapValsOff})
		l.emit(Instr{Op: OpStoreMem, A: m, B: idx, Imm: mapIdxOff})
		l.emit(Instr{Op: OpStoreMem, A: m, B: newCap, Imm: mapCapOff})
		l.mapReindex(m, t)
		l.ret(NoReg)
	})
	l.callHelper(sym, []Reg{m}, []vty{vInt}, vVoid)
}

// mapSetBody inserts or overwrites.
func (l *lowerer) mapSetBody(m, key, val Reg, t vty) {
	kt := vty{k: t.key}
	slot := l.temp(vInt)
	l.setSlot(slot, l.mapFind(m, key, t))
	e := l.wordAt(l.field(m, mapIdxOff, vInt), l.load(slot, vInt), vInt)

	fresh := l.newLabel()
	l.jumpNot(l.compare(OpGt, e, l.constant(0)), fresh)
	l.setWordAt(l.field(m, mapValsOff, vInt), l.arith(OpSub, e, l.constant(1)), val)
	l.ret(NoReg)

	// A new key. Make room first - a full array grows, a table choked
	// with removed slots is rebuilt - and find the slot again if either
	// happened, since both move everything.
	l.mark(fresh)
	placed := l.newLabel()
	noGrow := l.newLabel()
	length := l.field(m, mapLenOff, vInt)
	capacity := l.field(m, mapCapOff, vInt)
	l.jumpNot(l.compare(OpGe, length, capacity), noGrow)
	l.mapGrow(m, t)
	l.setSlot(slot, l.mapFind(m, key, t))
	l.jump(placed)
	l.mark(noGrow)
	crowded := l.arith(OpMul, l.arith(OpAdd, l.arith(OpAdd, length, l.field(m, mapTombOff, vInt)),
		l.constant(1)), l.constant(4))
	l.jumpNot(l.compare(OpGt, crowded, l.arith(OpMul, capacity, l.constant(6))), placed)
	l.mapReindex(m, t)
	l.setSlot(slot, l.mapFind(m, key, t))
	l.mark(placed)

	s := l.load(slot, vInt)
	idx := l.field(m, mapIdxOff, vInt)
	wasRemoved := l.newLabel()
	l.jumpNot(l.compare(OpLt, l.wordAt(idx, s, vInt), l.constant(0)), wasRemoved)
	l.emit(Instr{Op: OpStoreMem, A: m, Imm: mapTombOff,
		B: l.arith(OpSub, l.field(m, mapTombOff, vInt), l.constant(1))})
	l.mark(wasRemoved)

	n := l.field(m, mapLenOff, vInt)
	keys := l.field(m, mapKeysOff, vInt)
	l.setWordAt(keys, n, key)
	l.setWordAt(l.field(m, mapValsOff, vInt), n, val)
	l.setWordAt(idx, s, l.arith(OpAdd, n, l.constant(1)))
	l.emit(Instr{Op: OpStoreMem, A: m, B: l.arith(OpAdd, n, l.constant(1)), Imm: mapLenOff})

	// Appending keeps the order only when the new key is the largest.
	done := l.newLabel()
	l.jumpIf(l.compare(OpEq, n, l.constant(0)), done)
	prev := l.wordAt(keys, l.arith(OpSub, n, l.constant(1)), kt)
	l.jumpNot(l.keyLess(key, prev, t.key), done)
	l.emit(Instr{Op: OpStoreMem, A: m, B: l.constant(1), Imm: mapDirtyOff})
	l.mark(done)
}

// mapRemove deletes a key. The last entry moves into the hole, so this
// is O(1), and the map is marked out of order unless it was the last.
func (l *lowerer) mapRemove(m, key Reg, t vty) {
	kt := vty{k: t.key}
	sym := l.helperFunc(mapHelperKey("__map_remove", t), []vty{vInt, kt}, vVoid, func(a []Reg) {
		m, key := a[0], a[1]
		s := l.mapFind(m, key, t)
		idx := l.field(m, mapIdxOff, vInt)
		e := l.wordAt(idx, s, vInt)
		done := l.newLabel()
		l.jumpNot(l.compare(OpGt, e, l.constant(0)), done)

		l.setWordAt(idx, s, l.constant(-1))
		l.emit(Instr{Op: OpStoreMem, A: m, Imm: mapTombOff,
			B: l.arith(OpAdd, l.field(m, mapTombOff, vInt), l.constant(1))})
		j := l.arith(OpSub, e, l.constant(1))
		last := l.arith(OpSub, l.field(m, mapLenOff, vInt), l.constant(1))
		l.emit(Instr{Op: OpStoreMem, A: m, B: last, Imm: mapLenOff})
		l.jumpIf(l.compare(OpEq, j, last), done)

		keys := l.field(m, mapKeysOff, vInt)
		vals := l.field(m, mapValsOff, vInt)
		moved := l.wordAt(keys, last, kt)
		l.setWordAt(keys, j, moved)
		l.setWordAt(vals, j, l.wordAt(vals, last, vInt))
		l.setWordAt(idx, l.mapFind(m, moved, t), l.arith(OpAdd, j, l.constant(1)))
		l.emit(Instr{Op: OpStoreMem, A: m, B: l.constant(1), Imm: mapDirtyOff})
		l.mark(done)
		l.ret(NoReg)
	})
	l.callHelper(sym, []Reg{m, key}, []vty{vInt, kt}, vVoid)
}

// mapClear empties a map, keeping its arrays.
func (l *lowerer) mapClear(m Reg, t vty) {
	l.emit(Instr{Op: OpStoreMem, A: m, B: l.constant(0), Imm: mapLenOff})
	l.emit(Instr{Op: OpStoreMem, A: m, B: l.constant(0), Imm: mapDirtyOff})
	l.mapReindex(m, t)
}

// mapSort puts the entries back in key order if anything disturbed it.
// Every walk of a map in order calls this first.
func (l *lowerer) mapSort(m Reg, t vty) {
	sym := l.helperFunc(mapHelperKey("__map_sort", t), []vty{vInt}, vVoid, func(a []Reg) {
		m := a[0]
		done := l.newLabel()
		l.jumpIf(l.compare(OpEq, l.field(m, mapDirtyOff, vInt), l.constant(0)), done)

		n := l.field(m, mapLenOff, vInt)
		keys := l.field(m, mapKeysOff, vInt)
		vals := l.field(m, mapValsOff, vInt)

		// Heap sort: in place, O(n log n) whatever the input, and no
		// second buffer. Keys are unique, so stability does not arise.
		start := l.temp(vInt)
		l.setSlot(start, l.arith(OpSub, l.arith(OpShr, n, l.constant(1)), l.constant(1)))
		build, built := l.newLabel(), l.newLabel()
		l.mark(build)
		st := l.load(start, vInt)
		l.jumpIf(l.compare(OpLt, st, l.constant(0)), built)
		l.mapSift(keys, vals, st, n, t)
		l.setSlot(start, l.arith(OpSub, l.load(start, vInt), l.constant(1)))
		l.jump(build)
		l.mark(built)

		end := l.temp(vInt)
		l.setSlot(end, l.arith(OpSub, n, l.constant(1)))
		down, sorted := l.newLabel(), l.newLabel()
		l.mark(down)
		en := l.load(end, vInt)
		l.jumpNot(l.compare(OpGt, en, l.constant(0)), sorted)
		l.swapWords(keys, l.constant(0), en)
		l.swapWords(vals, l.constant(0), en)
		l.mapSift(keys, vals, l.constant(0), en, t)
		l.setSlot(end, l.arith(OpSub, l.load(end, vInt), l.constant(1)))
		l.jump(down)
		l.mark(sorted)

		l.emit(Instr{Op: OpStoreMem, A: m, B: l.constant(0), Imm: mapDirtyOff})
		l.mapReindex(m, t)
		l.mark(done)
		l.ret(NoReg)
	})
	l.callHelper(sym, []Reg{m}, []vty{vInt}, vVoid)
}

// mapSift moves entry `start` down the heap in [start, end) until
// neither child has a larger key.
func (l *lowerer) mapSift(keys, vals, start, end Reg, t vty) {
	ps := []vty{vInt, vInt, vInt, vInt}
	sym := l.helperFunc(mapHelperKey("__map_sift", t), ps, vVoid, func(a []Reg) {
		keys, vals, end := a[0], a[1], a[3]
		kt := vty{k: t.key}
		root := l.temp(vInt)
		l.setSlot(root, a[2])
		top, done := l.newLabel(), l.newLabel()
		l.mark(top)
		r := l.load(root, vInt)
		child := l.arith(OpAdd, l.arith(OpMul, r, l.constant(2)), l.constant(1))
		l.jumpNot(l.compare(OpLt, child, end), done)

		big := l.temp(vInt)
		l.setSlot(big, r)
		left := l.newLabel()
		l.jumpNot(l.keyLess(l.wordAt(keys, r, kt), l.wordAt(keys, child, kt), t.key), left)
		l.setSlot(big, child)
		l.mark(left)
		right := l.arith(OpAdd, child, l.constant(1))
		noRight := l.newLabel()
		l.jumpNot(l.compare(OpLt, right, end), noRight)
		l.jumpNot(l.keyLess(l.wordAt(keys, l.load(big, vInt), kt), l.wordAt(keys, right, kt), t.key), noRight)
		l.setSlot(big, right)
		l.mark(noRight)

		b := l.load(big, vInt)
		l.jumpIf(l.compare(OpEq, b, r), done)
		l.swapWords(keys, r, b)
		l.swapWords(vals, r, b)
		l.setSlot(root, b)
		l.jump(top)
		l.mark(done)
		l.ret(NoReg)
	})
	l.callHelper(sym, []Reg{keys, vals, start, end}, ps, vVoid)
}

// swapWords exchanges words i and j of a block.
func (l *lowerer) swapWords(block, i, j Reg) {
	x := l.wordAt(block, i, vInt)
	y := l.wordAt(block, j, vInt)
	l.setWordAt(block, i, y)
	l.setWordAt(block, j, x)
}
