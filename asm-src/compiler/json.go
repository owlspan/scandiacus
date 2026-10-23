package main

import "fmt"

// JSON encoding.
//
// The same shape as `str()` of a container: one renderer, generated from
// the static type, writing into the buffer that strOf sets up. What
// differs is the spelling - JSON quotes its keys, writes true and false
// the same way, and escapes a string to its own rules rather than
// Scandiacus's.
//
// The Go backend hands this to encoding/json, so the target is whatever
// json.Marshal produces: no spaces in the compact form, two spaces per
// level and `": "` after a key in the indented one, and struct fields in
// declaration order under the names they were written with.

// jsonBuiltin lowers json.encode and json.pretty.
func (l *lowerer) jsonBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "json.encode", "json.pretty":
		if len(c.Args) != 1 {
			l.errorAt(c, "%s takes 1 argument, got %d", name, len(c.Args))
			return l.junk(), true
		}
		v := l.expr(c.Args[0])
		t := l.regTy[v]
		pretty := name == "json.pretty"
		// One encoder per type, however many places encode one.
		fn := "__json_enc " + t.String()
		if pretty {
			fn = "__json_pretty " + t.String()
		}
		sym := l.helperFunc(fn, []vty{t}, vStr, func(a []Reg) {
			l.emit(Instr{Op: OpRet, A: l.jsonOf(c, a[0], t, pretty), Dst: NoReg})
		})
		return l.callHelper(sym, []Reg{v}, []vty{t}, vStr), true
	}
	return NoReg, false
}

// jsonOf renders a value as JSON, into a fresh buffer.
//
// This is strOf with a different writer: the buffer switch is the same
// one, so nothing about how a string gets built is duplicated here.
func (l *lowerer) jsonOf(n Node, v Reg, t vty, indent bool) Reg {
	slot := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: l.emptyStr(), Dst: NoReg, Imm: slot})

	saved := l.buf
	l.buf = slot
	l.jsonWrite(n, v, t, indent, 0)
	l.buf = saved

	return l.bufResult(slot)
}

// newline writes a line break and the indentation for a nesting depth,
// but only in the indented form. In the compact one it writes nothing,
// which is what keeps the two paths a single piece of code.
func (l *lowerer) newline(indent bool, depth int) {
	if !indent {
		return
	}
	pad := "\n"
	for i := 0; i < depth; i++ {
		pad += "  "
	}
	l.writeLit(pad)
}

// jsonWrite appends one value. Everything below is this function
// recursing on the type, so the depth is whatever the type says.
func (l *lowerer) jsonWrite(n Node, v Reg, t vty, indent bool, depth int) {
	if t.null {
		// A nil is JSON's null; a value is whatever is inside it.
		isNil := l.newLabel()
		done := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.isNil(v), Dst: NoReg, Imm: isNil})
		l.jsonWrite(n, l.nullValue(v, t), t.notNull(), indent, depth)
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
		l.mark(isNil)
		l.writeLit("null")
		l.mark(done)
		return
	}

	switch t.k {
	case kStr:
		l.jsonString(v)
	case kBool:
		l.mod.needs("booltostr")
		s := l.newReg()
		l.regTy[s] = vStr
		l.emit(Instr{Op: OpBoolToStr, Dst: s, A: v, B: NoReg})
		l.emitStr(s)
	case kFloat:
		l.emitFloat(v)
	case kInt:
		l.emitInt(v)
	case kList, kMap, kStruct:
		l.jsonComposite(n, v, t, indent, depth)
	default:
		l.errorAt(n, "%s cannot be encoded as JSON on the assembly backend yet", t)
	}
}

// jsonComposite writes a list, map or struct through the function that
// encodes its type, so a Team holding Persons calls the Person encoder
// rather than carrying a copy of it. The indented form depends on how
// deep the value sits, so that one is a function per type and depth.
func (l *lowerer) jsonComposite(n Node, v Reg, t vty, indent bool, depth int) {
	key := "__json_w " + t.String()
	if indent {
		key = fmt.Sprintf("__json_w%d %s", depth, t.String())
	}
	if l.inlineOnce == key {
		l.inlineOnce = ""
		switch t.k {
		case kList:
			l.jsonList(n, v, t, indent, depth)
		case kMap:
			l.jsonMap(n, v, t, indent, depth)
		default:
			l.jsonStruct(n, v, t, indent, depth)
		}
		return
	}
	sym := l.helperFunc(key, []vty{t}, vStr, func(a []Reg) {
		slot := l.temp(vStr)
		l.emit(Instr{Op: OpStore, A: l.emptyStr(), Dst: NoReg, Imm: slot})
		l.buf = slot
		l.inlineOnce = key
		l.jsonComposite(n, a[0], t, indent, depth)
		l.emit(Instr{Op: OpRet, A: l.bufResult(slot), Dst: NoReg})
	})
	l.emitStr(l.callHelper(sym, []Reg{v}, []vty{t}, vStr))
}

// jsonString writes a quoted, escaped string.
//
// Go escapes the two characters JSON requires, the control characters
// below 0x20, and - for HTML safety, which Marshal does by default -
// `<`, `>` and `&`. Leaving those three unescaped would produce valid
// JSON that the two backends spell differently.
func (l *lowerer) jsonString(s Reg) {
	l.writeLit("\"")
	sym := l.helperFunc("__json_escape", []vty{vStr}, vStr, func(a []Reg) {
		l.emit(Instr{Op: OpRet, A: l.jsonEscape(a[0]), Dst: NoReg})
	})
	l.emitStr(l.callHelper(sym, []Reg{s}, []vty{vStr}, vStr))
	l.writeLit("\"")
}

// jsonEscape builds the escaped body of a string, without the quotes.
func (l *lowerer) jsonEscape(s Reg) Reg {
	n := l.strLen(s)
	// Six bytes is the worst case, \u00XX for a control character.
	buf := l.strAlloc(l.arith(OpMul, n, l.constant(6)))

	wSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: wSlot})

	// put writes one byte and advances.
	put := func(b Reg) {
		w := l.load(wSlot, vInt)
		l.storeByte(buf, w, b)
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, w, l.constant(1)),
			Dst: NoReg, Imm: wSlot})
	}
	// escape writes a two-character escape.
	escape := func(c int64) {
		put(l.constant('\\'))
		put(l.constant(c))
	}

	// The bytes are walked by hand rather than with eachByte, because a
	// character of more than one byte is copied, or replaced, whole.
	sS := l.temp(vStr)
	l.setSlot(sS, s)
	nS := l.temp(vInt)
	l.setSlot(nS, n)
	iS := l.temp(vInt)
	l.setSlot(iS, l.constant(0))
	top, end := l.newLabel(), l.newLabel()
	l.mark(top)
	l.jumpNot(l.compare(OpLt, l.load(iS, vInt), l.load(nS, vInt)), end)
	i := l.load(iS, vInt)
	b := l.loadByte(l.load(sS, vStr), i)
	bS := l.temp(vInt)
	l.setSlot(bS, b)
	b = l.load(bS, vInt)
	done := l.newLabel()
	step := l.temp(vInt)
	l.setSlot(step, l.constant(1))

	// Past ASCII: a valid character is copied as it is, except U+2028
	// and U+2029, which JavaScript reads as line breaks and Go escapes;
	// a byte that does not start one is written \ufffd, as Go does, so
	// the output is always valid UTF-8 and so valid JSON.
	ascii := l.newLabel()
	l.jumpNot(l.compare(OpGe, b, l.constant(0x80)), ascii)
	w := l.utf8Width(l.load(sS, vStr), l.load(iS, vInt), l.load(nS, vInt))
	wS := l.temp(vInt)
	l.setSlot(wS, w)
	valid := l.newLabel()
	l.jumpNot(l.compare(OpEq, l.load(wS, vInt), l.constant(0)), valid)
	for _, c := range []byte(`\ufffd`) {
		put(l.constant(int64(c)))
	}
	l.jump(done)
	l.mark(valid)
	l.setSlot(step, l.load(wS, vInt))
	copyIt := l.newLabel()
	l.jumpNot(l.compare(OpEq, l.load(wS, vInt), l.constant(3)), copyIt)
	l.jumpNot(l.compare(OpEq, l.load(bS, vInt), l.constant(0xE2)), copyIt)
	l.jumpNot(l.compare(OpEq, l.loadByte(l.load(sS, vStr), l.arith(OpAdd, l.load(iS, vInt), l.constant(1))), l.constant(0x80)), copyIt)
	third := l.loadByte(l.load(sS, vStr), l.arith(OpAdd, l.load(iS, vInt), l.constant(2)))
	tS := l.temp(vInt)
	l.setSlot(tS, third)
	sep := l.newLabel()
	l.jumpIf(l.compare(OpEq, l.load(tS, vInt), l.constant(0xA8)), sep)
	l.jumpNot(l.compare(OpEq, l.load(tS, vInt), l.constant(0xA9)), copyIt)
	l.mark(sep)
	for _, c := range []byte(`\u202`) {
		put(l.constant(int64(c)))
	}
	put(l.pick(l.compare(OpEq, l.load(tS, vInt), l.constant(0xA8)), l.constant('8'), l.constant('9'), vInt))
	l.jump(done)
	l.mark(copyIt)
	k := l.temp(vInt)
	l.setSlot(k, l.constant(0))
	ctop := l.newLabel()
	l.mark(ctop)
	l.jumpNot(l.compare(OpLt, l.load(k, vInt), l.load(wS, vInt)), done)
	put(l.loadByte(l.load(sS, vStr), l.arith(OpAdd, l.load(iS, vInt), l.load(k, vInt))))
	l.setSlot(k, l.arith(OpAdd, l.load(k, vInt), l.constant(1)))
	l.jump(ctop)

	l.mark(ascii)
	b = l.load(bS, vInt)
	// The short escapes Go writes; backspace and form feed have had
	// theirs since Go 1.22.
	for _, pair := range []struct {
		from int64
		to   int64
	}{
		{'"', '"'}, {'\\', '\\'}, {'\n', 'n'}, {'\r', 'r'}, {'\t', 't'}, {8, 'b'}, {12, 'f'},
	} {
		next := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, b, l.constant(pair.from)),
			Dst: NoReg, Imm: next})
		escape(pair.to)
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
		l.mark(next)
	}

	// The HTML-unsafe three and every other control character go
	// out as \u00XX.
	unicode := l.newLabel()
	plain := l.newLabel()
	needsU := l.compare(OpLt, b, l.constant(0x20))
	for _, c := range []int64{'<', '>', '&'} {
		needsU = l.logicalOr(needsU, l.compare(OpEq, b, l.constant(c)))
	}
	l.emit(Instr{Op: OpJumpIf, A: needsU, Dst: NoReg, Imm: unicode})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: plain})

	l.mark(unicode)
	put(l.constant('\\'))
	put(l.constant('u'))
	put(l.constant('0'))
	put(l.constant('0'))
	put(l.hexDigitLower(l.arith(OpShr, b, l.constant(4))))
	put(l.hexDigitLower(l.arith(OpBAnd, b, l.constant(15))))
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(plain)
	put(b)

	l.mark(done)
	l.setSlot(iS, l.arith(OpAdd, l.load(iS, vInt), l.load(step, vInt)))
	l.jump(top)
	l.mark(end)

	l.strEnd(buf, l.load(wSlot, vInt))
	return buf
}

// utf8Width is how many bytes the character starting at i of s takes,
// or 0 when the byte there does not start a valid one: Go's rules, so
// no overlong forms, no surrogates, nothing past U+10FFFF, and nothing
// cut off by the end at n.
func (l *lowerer) utf8Width(s, i, n Reg) Reg {
	sym := l.helperFunc("__vy_utf8w", []vty{vStr, vInt, vInt}, vInt, func(a []Reg) {
		sS, iS, nS := l.temp(vStr), l.temp(vInt), l.temp(vInt)
		l.setSlot(sS, a[0])
		l.setSlot(iS, a[1])
		l.setSlot(nS, a[2])
		at := func(k int64) Reg {
			return l.loadByte(l.load(sS, vStr), l.arith(OpAdd, l.load(iS, vInt), l.constant(k)))
		}
		has := func(k int64) Reg {
			return l.compare(OpLt, l.arith(OpAdd, l.load(iS, vInt), l.constant(k)), l.load(nS, vInt))
		}
		in := func(v Reg, lo, hi int64) Reg {
			return l.logicalAnd(l.compare(OpGe, v, l.constant(lo)), l.compare(OpLe, v, l.constant(hi)))
		}
		bad := l.newLabel()
		b0 := l.temp(vInt)
		l.setSlot(b0, at(0))
		lo, hi := l.temp(vInt), l.temp(vInt)
		l.setSlot(lo, l.constant(0x80))
		l.setSlot(hi, l.constant(0xBF))
		// Two bytes: C2 to DF.
		notTwo := l.newLabel()
		l.jumpNot(in(l.load(b0, vInt), 0xC2, 0xDF), notTwo)
		l.jumpNot(has(1), bad)
		l.jumpNot(in(at(1), 0x80, 0xBF), bad)
		l.ret(l.constant(2))
		l.mark(notTwo)
		// Three bytes: E0 to EF, with E0 and ED narrowing the second.
		notThree := l.newLabel()
		l.jumpNot(in(l.load(b0, vInt), 0xE0, 0xEF), notThree)
		e0, ed := l.newLabel(), l.newLabel()
		l.jumpNot(l.compare(OpEq, l.load(b0, vInt), l.constant(0xE0)), e0)
		l.setSlot(lo, l.constant(0xA0))
		l.mark(e0)
		l.jumpNot(l.compare(OpEq, l.load(b0, vInt), l.constant(0xED)), ed)
		l.setSlot(hi, l.constant(0x9F))
		l.mark(ed)
		l.jumpNot(has(2), bad)
		l.jumpNot(l.logicalAnd(l.compare(OpGe, at(1), l.load(lo, vInt)), l.compare(OpLe, at(1), l.load(hi, vInt))), bad)
		l.jumpNot(in(at(2), 0x80, 0xBF), bad)
		l.ret(l.constant(3))
		l.mark(notThree)
		// Four bytes: F0 to F4, with F0 and F4 narrowing the second.
		l.jumpNot(in(l.load(b0, vInt), 0xF0, 0xF4), bad)
		f0, f4 := l.newLabel(), l.newLabel()
		l.jumpNot(l.compare(OpEq, l.load(b0, vInt), l.constant(0xF0)), f0)
		l.setSlot(lo, l.constant(0x90))
		l.mark(f0)
		l.jumpNot(l.compare(OpEq, l.load(b0, vInt), l.constant(0xF4)), f4)
		l.setSlot(hi, l.constant(0x8F))
		l.mark(f4)
		l.jumpNot(has(3), bad)
		l.jumpNot(l.logicalAnd(l.compare(OpGe, at(1), l.load(lo, vInt)), l.compare(OpLe, at(1), l.load(hi, vInt))), bad)
		l.jumpNot(in(at(2), 0x80, 0xBF), bad)
		l.jumpNot(in(at(3), 0x80, 0xBF), bad)
		l.ret(l.constant(4))
		l.mark(bad)
		l.ret(l.constant(0))
	})
	return l.callHelper(sym, []Reg{s, i, n}, []vty{vStr, vInt, vInt}, vInt)
}

// hexDigitLower is hexDigit in the case Go's \u escapes use.
func (l *lowerer) hexDigitLower(v Reg) Reg {
	return l.pick(l.compare(OpLt, v, l.constant(10)),
		l.arith(OpAdd, v, l.constant('0')),
		l.arith(OpAdd, v, l.constant('a'-10)), vInt)
}

func (l *lowerer) jsonList(n Node, v Reg, t vty, indent bool, depth int) {
	elem := t.elemType()

	// An empty list is "[]" on one line even in the indented form,
	// which is what Marshal does.
	empty := l.newLabel()
	done := l.newLabel()
	l.emit(Instr{Op: OpJumpIf,
		A:   l.compare(OpEq, l.field(v, listLenOff, vInt), l.constant(0)),
		Dst: NoReg, Imm: empty})

	l.writeLit("[")
	l.eachElement(v, t, func(i, x Reg) {
		first := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, i, l.constant(0)),
			Dst: NoReg, Imm: first})
		l.writeLit(",")
		l.mark(first)
		l.newline(indent, depth+1)
		l.jsonWrite(n, x, elem, indent, depth+1)
	})
	l.newline(indent, depth)
	l.writeLit("]")
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(empty)
	l.writeLit("[]")

	l.mark(done)
}

func (l *lowerer) jsonMap(n Node, v Reg, t vty, indent bool, depth int) {
	if t.key != kStr {
		l.errorAt(n, "JSON object keys are strings, so a %s cannot be encoded", t)
		return
	}

	l.mapSort(v, t)
	empty := l.newLabel()
	done := l.newLabel()
	length := l.field(v, mapLenOff, vInt)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, length, l.constant(0)),
		Dst: NoReg, Imm: empty})

	l.writeLit("{")

	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})
	top := l.newLabel()
	walked := l.newLabel()
	l.mark(top)
	i := l.load(iSlot, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, i, length), Dst: NoReg, Imm: walked})

	first := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, i, l.constant(0)),
		Dst: NoReg, Imm: first})
	l.writeLit(",")
	l.mark(first)

	l.newline(indent, depth+1)
	l.jsonString(l.cellAt(v, mapKeysOff, i, vStr))
	l.jsonColon(indent)
	l.jsonWrite(n, l.cellAt(v, mapValsOff, i, t.elemType()), t.elemType(), indent, depth+1)

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(walked)
	l.newline(indent, depth)
	l.writeLit("}")
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(empty)
	l.writeLit("{}")

	l.mark(done)
}

// jsonColon is what separates a key from its value: bare in the compact
// form, followed by a space in the indented one.
func (l *lowerer) jsonColon(indent bool) {
	if indent {
		l.writeLit(": ")
		return
	}
	l.writeLit(":")
}

func (l *lowerer) jsonStruct(n Node, v Reg, t vty, indent bool, depth int) {
	lay, ok := l.layoutOf(n, t)
	if !ok {
		return
	}
	if len(lay.fields) == 0 {
		l.writeLit("{}")
		return
	}

	l.writeLit("{")
	for i, f := range lay.fields {
		if i > 0 {
			l.writeLit(",")
		}
		l.newline(indent, depth+1)
		// The field name as written in Scandiacus, which is what the Go
		// backend's struct tags say too.
		l.jsonString(l.strLit(f.name))
		l.jsonColon(indent)
		l.jsonWrite(n, l.loadField(v, f), f.t, indent, depth+1)
	}
	l.newline(indent, depth)
	l.writeLit("}")
}
