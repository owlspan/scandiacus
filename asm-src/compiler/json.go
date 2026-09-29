package main

import "fmt"

// JSON encoding.
//
// The same shape as `str()` of a container: one renderer, generated from
// the static type, writing into the buffer that strOf sets up. What
// differs is the spelling - JSON quotes its keys, writes true and false
// the same way, and escapes a string to its own rules rather than
// Veyl's.
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

	return l.load(slot, vStr)
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
		l.emit(Instr{Op: OpRet, A: l.load(slot, vStr), Dst: NoReg})
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

	l.eachByte(s, n, func(i Reg) {
		b := l.loadByte(s, i)
		done := l.newLabel()

		// The short escapes, in the order Go writes them.
		for _, pair := range []struct {
			from int64
			to   int64
		}{
			{'"', '"'}, {'\\', '\\'}, {'\n', 'n'}, {'\r', 'r'}, {'\t', 't'},
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
	})

	l.storeByte(buf, l.load(wSlot, vInt), l.constant(0))
	return buf
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
		// The field name as written in Veyl, which is what the Go
		// backend's struct tags say too.
		l.jsonString(l.strLit(f.name))
		l.jsonColon(indent)
		l.jsonWrite(n, l.loadField(v, f), f.t, indent, depth+1)
	}
	l.newline(indent, depth)
	l.writeLit("}")
}
