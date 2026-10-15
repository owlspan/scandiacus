package main

// Appending to a string variable in place.
//
// `s += t` used to be `s = s + t`: a new string the size of both, and a
// copy of all of s, every time. A loop that builds a string that way is
// quadratic, and that is how nearly everyone builds one.
//
// Now the string a `+=` makes is allocated with room to spare - a power
// of two, at least 32 bytes - and marked as owned by the variable it is
// stored in. The next `+=` on that variable writes into the spare room
// and moves the length in the header up, with no copy, so a loop of
// appends costs what the bytes cost, like a Go slice or a Java
// StringBuilder.
//
// Writing into a string is only allowed while nothing else can see it,
// since a string is a value: `let t = s` before an append must leave t
// as it was. The rule that keeps it so is that every read of the
// variable, other than the `+=` itself and `len`, takes the mark off
// first. So the moment the value could be anywhere else - assigned,
// passed, printed, pushed - the object is an ordinary string again, and
// the next `+=` copies it into a fresh owned one. Only plain locals
// qualify: a variable a closure captures, or one whose address is
// taken, is read in ways this file does not see, so it keeps the old
// behaviour.
//
// The mark is bit 1 of the object's list link, next to the collector's
// mark in bit 0; malloc's 16-byte alignment leaves both free. The
// capacity is not stored anywhere: an owned string's block is always
// the power of two at or above its size, so it is worked out again from
// the length. A collection keeps the bit. A string literal in .rdata has
// a link of zero, so it is never owned and never written.

const objOwnedBit = 2

// appendTargets is every name in a function body that is the target of
// a `+=` or of `x = x + ...`. Reads of those names take the owned mark
// off; reads of any other name cost nothing extra. Names are not scoped
// here, so a shadowed name is over-counted, which only costs a check.
func appendTargets(stmts []Stmt) map[string]bool {
	out := map[string]bool{}
	var stmt func(s Stmt)
	block := func(b *Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			stmt(s)
		}
	}
	stmt = func(s Stmt) {
		switch x := s.(type) {
		case *Block:
			block(x)
		case *AssignStmt:
			if id, ok := x.Target.(*Ident); ok {
				if x.Op == PLUSEQ || selfAppend(x, id) != nil {
					out[id.Name] = true
				}
			}
		case *IfStmt:
			block(x.Then)
			if x.Else != nil {
				stmt(x.Else)
			}
		case *WhileStmt:
			block(x.Body)
		case *ForStmt:
			block(x.Body)
			if x.Lowered != nil {
				stmt(x.Lowered)
			}
		case *MatchStmt:
			for _, c := range x.Cases {
				if c.Body != nil {
					stmt(c.Body)
				}
			}
			if x.Else != nil {
				stmt(x.Else)
			}
			if x.Lowered != nil {
				stmt(x.Lowered)
			}
		case *DeferStmt:
			if x.Body != nil {
				stmt(x.Body)
			}
		}
	}
	for _, s := range stmts {
		stmt(s)
	}
	return out
}

// selfAppend reports the appended part when an assignment is
// `x = x + e`, or nil.
func selfAppend(st *AssignStmt, id *Ident) Expr {
	if st.Op != ASSIGN {
		return nil
	}
	b, ok := st.Value.(*Binary)
	if !ok || b.Op != PLUS {
		return nil
	}
	if l, ok := b.L.(*Ident); ok && l.Name == id.Name {
		return b.R
	}
	return nil
}

// ownable reports whether a slot can hold an owned string: a plain,
// non-null str local, not boxed for a closure and not kept in memory
// for &.
func (l *lowerer) ownable(name string, slot int64) bool {
	if !l.appendNames[name] || l.boxed[slot] {
		return false
	}
	if _, kept := l.memSlot(slot); kept {
		return false
	}
	t := l.slotTy[slot]
	return t.k == kStr && !t.null
}

// disown takes the owned mark off the string in d, if it has one. The
// test comes first so that a literal, whose header is in read-only
// memory, is never written.
func (l *lowerer) disown(d Reg) {
	ds := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: d, Dst: NoReg, Imm: ds})
	skip := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(ds, vInt), l.constant(0)), Dst: NoReg, Imm: skip})
	link := l.peekWord(l.arith(OpSub, l.load(ds, vInt), l.constant(objHeader)), objNextOff)
	ls := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: link, Dst: NoReg, Imm: ls})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq,
		l.arith(OpBAnd, l.load(ls, vInt), l.constant(objOwnedBit)), l.constant(0)), Dst: NoReg, Imm: skip})
	l.pokeWord(l.arith(OpSub, l.load(ds, vInt), l.constant(objHeader)), objNextOff,
		l.arith(OpBAnd, l.load(ls, vInt), l.constant(^int64(objOwnedBit))))
	l.mark(skip)
}

// appendInPlace lowers `name += v` for an ownable slot.
func (l *lowerer) appendInPlace(slot int64, v Reg) {
	l.storeLocal(slot, l.appendCall(l.loadLocal(slot), v))
}

// appendCall is cur + v through the in-place append: cur is the one
// reference to its string, so an owned one grows where it is.
func (l *lowerer) appendCall(cur, v Reg) Reg {
	sym := l.helperFunc("__vy_append", []vty{vStr, vStr}, vStr, func(a []Reg) {
		l.appendBody(a[0], a[1])
	})
	return l.callHelper(sym, []Reg{cur, v}, []vty{vStr, vStr}, vStr)
}

// bufResult is the text a str() or json buffer built, let go of: the
// buffer was appended to in place, and the value is about to be
// anybody's.
func (l *lowerer) bufResult(slot int64) Reg {
	d := l.load(slot, vStr)
	l.disown(d)
	return l.load(slot, vStr)
}

// strCapFor is the block an owned string of size bytes (terminator
// included) lives in.
func (l *lowerer) strCapFor(size Reg) Reg {
	ss := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: size, Dst: NoReg, Imm: ss})
	c := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(32), Dst: NoReg, Imm: c})
	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, l.load(c, vInt), l.load(ss, vInt)), Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpStore, A: l.arith(OpShl, l.load(c, vInt), l.constant(1)), Dst: NoReg, Imm: c})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(done)
	return l.load(c, vInt)
}

func (l *lowerer) appendBody(s, t Reg) {
	sS := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: s, Dst: NoReg, Imm: sS})
	tS := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: t, Dst: NoReg, Imm: tS})
	nS := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.strLen(l.load(sS, vStr)), Dst: NoReg, Imm: nS})
	mS := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.strLen(l.load(tS, vStr)), Dst: NoReg, Imm: mS})
	totS := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.arith(OpAdd, l.load(nS, vInt), l.load(mS, vInt)),
		l.constant(1)), Dst: NoReg, Imm: totS})

	// Nothing to add: the string as it is, owned or not.
	notEmpty := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpNe, l.load(mS, vInt), l.constant(0)), Dst: NoReg, Imm: notEmpty})
	l.emit(Instr{Op: OpRet, A: l.load(sS, vStr), Dst: NoReg})
	l.mark(notEmpty)

	raw := func() Reg { return l.arith(OpSub, l.load(sS, vInt), l.constant(objHeader)) }
	fresh := l.newLabel()
	link := l.peekWord(raw(), objNextOff)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq,
		l.arith(OpBAnd, link, l.constant(objOwnedBit)), l.constant(0)), Dst: NoReg, Imm: fresh})
	capNow := l.strCapFor(l.arith(OpAdd, l.load(nS, vInt), l.constant(1)))
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGt, l.load(totS, vInt), capNow), Dst: NoReg, Imm: fresh})

	// Owned, with room: write after the end and lengthen the header.
	l.copyBytes(l.arith(OpAdd, l.load(sS, vInt), l.load(nS, vInt)), l.load(tS, vInt), l.load(mS, vInt))
	l.storeByte(l.load(sS, vStr), l.arith(OpSub, l.load(totS, vInt), l.constant(1)), l.constant(0))
	hdr := l.peekWord(raw(), objTagOff)
	l.pokeWord(raw(), objTagOff, l.arith(OpBOr,
		l.arith(OpShl, l.load(totS, vInt), l.constant(tagShift)),
		l.arith(OpBAnd, hdr, l.constant(255))))
	l.atomicAdd(l.rtSlot(gcBytesSlot), l.load(mS, vInt))
	l.emit(Instr{Op: OpRet, A: l.load(sS, vStr), Dst: NoReg})

	// Otherwise a new owned block, with both copied in. Its header is
	// brought down to the size actually used, as strEnd does, so the
	// collector's count stays the sum of the headers.
	l.mark(fresh)
	capS := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.strCapFor(l.load(totS, vInt)), Dst: NoReg, Imm: capS})
	out := l.allocObj(l.load(capS, vInt), tagBytes)
	oS := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: out, Dst: NoReg, Imm: oS})
	l.copyBytes(l.load(oS, vInt), l.load(sS, vInt), l.load(nS, vInt))
	l.copyBytes(l.arith(OpAdd, l.load(oS, vInt), l.load(nS, vInt)), l.load(tS, vInt), l.load(mS, vInt))
	l.storeByte(l.load(oS, vStr), l.arith(OpSub, l.load(totS, vInt), l.constant(1)), l.constant(0))
	oraw := func() Reg { return l.arith(OpSub, l.load(oS, vInt), l.constant(objHeader)) }
	l.pokeWord(oraw(), objTagOff, l.arith(OpBOr,
		l.arith(OpShl, l.load(totS, vInt), l.constant(tagShift)), l.constant(tagBytes)))
	l.atomicAdd(l.rtSlot(gcBytesSlot), l.arith(OpSub, l.load(totS, vInt), l.load(capS, vInt)))
	l.pokeWord(oraw(), objNextOff, l.arith(OpBOr, l.peekWord(oraw(), objNextOff), l.constant(objOwnedBit)))
	l.emit(Instr{Op: OpRet, A: l.load(oS, vStr), Dst: NoReg})
}
