package main

// JSON parsing: text to a tree, and the tree to a Veyl value.
//
// Two halves that do not know about each other. The parser is a runtime
// walk over the characters, producing nodes that carry their own kind -
// it has to be, because what is in the text is only known once it has
// been read. The conversion from a node to a typed value is generated
// from the annotation, the same way the encoder is generated from the
// type it is handed.
//
// That split is what json.get and its family are for: they need the
// tree and no type at all.
//
// A node is a mixed object, so it is tagged like a struct: the pointer
// fields first, then the count of them in the header. Getting that wrong
// costs nothing today and would cost a collector everything.

const (
	jsonNull   = 0
	jsonBool   = 1
	jsonNumber = 2
	jsonString = 3
	jsonArray  = 4
	jsonObject = 5

	// The four pointers first, so the header can describe them with a
	// single count, then the two plain words.
	jnStr    = 0  // string value, or the raw text of a number
	jnItems  = 8  // array elements, a list of nodes
	jnKeys   = 16 // object keys, a list of str
	jnVals   = 24 // object values, a list of nodes
	jnKind   = 32
	jnNumber = 40

	jsonNodeSize = 48
	jsonNodePtrs = 4
)

// A list of nodes is a list of pointers. It is typed as a list of str so
// that its element block is tagged as holding pointers - a node is not a
// string, but every element of that block is a pointer to the heap, and
// that is the only thing the tag records.
var vNodeList = vListOf(vStr)

func (l *lowerer) newJSONNode(kind int64) Reg {
	header := int64(jsonNodeSize)<<structSizeShift | int64(jsonNodePtrs)<<structNPtrShift | tagStruct
	node := l.allocTagged(l.constant(jsonNodeSize), l.constant(header))
	l.regTy[node] = vStr

	l.emit(Instr{Op: OpStoreMem, A: node, B: l.emptyStr(), Imm: jnStr})
	l.emit(Instr{Op: OpStoreMem, A: node, B: l.newList(vNodeList, initialCap), Imm: jnItems})
	l.emit(Instr{Op: OpStoreMem, A: node, B: l.newList(vListOf(vStr), initialCap), Imm: jnKeys})
	l.emit(Instr{Op: OpStoreMem, A: node, B: l.newList(vNodeList, initialCap), Imm: jnVals})
	l.emit(Instr{Op: OpStoreMem, A: node, B: l.constant(kind), Imm: jnKind})
	l.emit(Instr{Op: OpStoreMem, A: node, B: l.floatConst(0), Imm: jnNumber})
	return node
}

// nodeField reads one word out of a node.
func (l *lowerer) nodeField(node Reg, off int64, t vty) Reg {
	d := l.newReg()
	l.regTy[d] = t
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: node, B: NoReg, Imm: off})
	return d
}

// ---- the parser ----
//
// The text and the position travel in slots rather than as arguments,
// because this is lowered inline and every helper below has to see the
// same cursor. `ok` is cleared the moment anything is wrong and checked
// at the end; nothing tries to recover, since a half-parsed document is
// not useful to anybody.

// The cursor is a two-word heap object rather than a pair of stack
// slots, because the parser is a real function now and its callee has to
// move the same position the caller is watching.
const (
	curPos = 0
	curOk  = 8
)

type jsonCursor struct {
	text Reg
	n    Reg
	c    Reg // the heap object
}

func (l *lowerer) newCursor(text, cur Reg) *jsonCursor {
	return &jsonCursor{text: text, n: l.strLen(text), c: cur}
}

// freshCursor allocates one, positioned at the start and not yet failed.
func (l *lowerer) freshCursor() Reg {
	raw := l.allocObj(l.constant(2*wordSize), tagWords)
	l.emit(Instr{Op: OpStoreMem, A: raw, B: l.constant(0), Imm: curPos})
	l.emit(Instr{Op: OpStoreMem, A: raw, B: l.constant(1), Imm: curOk})
	return raw
}

func (l *lowerer) at(c *jsonCursor) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: c.c, B: NoReg, Imm: curPos})
	return d
}

func (l *lowerer) setAt(c *jsonCursor, v Reg) {
	l.emit(Instr{Op: OpStoreMem, A: c.c, B: v, Imm: curPos})
}

// peek is the character under the cursor, or 0 at the end of the text.
func (l *lowerer) peek(c *jsonCursor) Reg {
	out := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: out})
	past := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, l.at(c), c.n), Dst: NoReg, Imm: past})
	l.emit(Instr{Op: OpStore, A: l.loadByte(c.text, l.at(c)), Dst: NoReg, Imm: out})
	l.mark(past)
	return l.load(out, vInt)
}

func (l *lowerer) advance(c *jsonCursor) {
	l.setAt(c, l.arith(OpAdd, l.at(c), l.constant(1)))
}

func (l *lowerer) fail(c *jsonCursor) {
	l.emit(Instr{Op: OpStoreMem, A: c.c, B: l.constant(0), Imm: curOk})
}

// skipSpace steps over the whitespace JSON allows between tokens.
func (l *lowerer) skipSpace(c *jsonCursor) {
	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)
	b := l.peek(c)
	space := l.compare(OpEq, b, l.constant(' '))
	for _, w := range []int64{'\t', '\n', '\r'} {
		space = l.logicalOr(space, l.compare(OpEq, b, l.constant(w)))
	}
	l.emit(Instr{Op: OpJumpNot, A: space, Dst: NoReg, Imm: done})
	l.advance(c)
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(done)
}

// expect consumes one character, or marks the parse failed.
func (l *lowerer) expect(c *jsonCursor, ch int64) {
	good := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.peek(c), l.constant(ch)),
		Dst: NoReg, Imm: good})
	l.fail(c)
	l.mark(good)
	l.advance(c)
}

// jsonValueSym is the parser, emitted once as a real function.
//
// It has to be a function rather than inlined code, because it recurses
// on the *data*: how deep a document nests is not known until it is
// read. Inlining that to a fixed depth made the compiler allocate three
// gigabytes and give up. As a function the depth costs stack, which is
// what recursion is supposed to cost.
const jsonValueSym = "jsonvalue"

// parseValue calls the parser.
func (l *lowerer) parseValue(c *jsonCursor) Reg {
	l.emitJSONParser()
	d := l.callHelper(jsonValueSym, []Reg{c.text, c.c}, []vty{vStr, vInt}, vStr)
	l.regTy[d] = vStr
	return d
}

// emitJSONParser writes the parser function, once.
func (l *lowerer) emitJSONParser() {
	if l.helpers[jsonValueSym] {
		return
	}
	l.helperFunc(jsonValueSym, []vty{vStr, vInt}, vStr, func(args []Reg) {
		c := l.newCursor(args[0], args[1])
		l.emit(Instr{Op: OpRet, A: l.parseValueBody(c), Dst: NoReg})
	})
}

func (l *lowerer) parseValueBody(c *jsonCursor) Reg {
	out := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: l.newJSONNode(jsonNull), Dst: NoReg, Imm: out})

	l.skipSpace(c)
	b := l.peek(c)
	done := l.newLabel()

	// Each shape is told apart by its first character, which is what
	// makes JSON parseable without looking ahead.
	tryString := l.newLabel()
	tryArray := l.newLabel()
	tryObject := l.newLabel()
	tryTrue := l.newLabel()
	tryFalse := l.newLabel()
	tryNull := l.newLabel()
	tryNumber := l.newLabel()

	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, b, l.constant('"')), Dst: NoReg, Imm: tryString})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, b, l.constant('[')), Dst: NoReg, Imm: tryArray})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, b, l.constant('{')), Dst: NoReg, Imm: tryObject})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, b, l.constant('t')), Dst: NoReg, Imm: tryTrue})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, b, l.constant('f')), Dst: NoReg, Imm: tryFalse})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, b, l.constant('n')), Dst: NoReg, Imm: tryNull})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: tryNumber})

	l.mark(tryString)
	node := l.newJSONNode(jsonString)
	l.emit(Instr{Op: OpStoreMem, A: node, B: l.parseString(c), Imm: jnStr})
	l.emit(Instr{Op: OpStore, A: node, Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(tryArray)
	l.emit(Instr{Op: OpStore, A: l.parseArray(c), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(tryObject)
	l.emit(Instr{Op: OpStore, A: l.parseObject(c), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(tryTrue)
	l.emit(Instr{Op: OpStore, A: l.parseKeyword(c, "true", jsonBool, 1), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(tryFalse)
	l.emit(Instr{Op: OpStore, A: l.parseKeyword(c, "false", jsonBool, 0), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(tryNull)
	l.emit(Instr{Op: OpStore, A: l.parseKeyword(c, "null", jsonNull, 0), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(tryNumber)
	l.emit(Instr{Op: OpStore, A: l.parseNumber(c), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, vStr)
}

// parseKeyword matches one of the three bare words and builds its node.
func (l *lowerer) parseKeyword(c *jsonCursor, word string, kind, truth int64) Reg {
	node := l.newJSONNode(kind)
	for _, ch := range []byte(word) {
		l.expect(c, int64(ch))
	}
	if kind == jsonBool {
		l.emit(Instr{Op: OpStoreMem, A: node, B: l.constant(truth), Imm: jnNumber})
	}
	return node
}

// parseString reads a quoted string, undoing the escapes.
func (l *lowerer) parseString(c *jsonCursor) Reg {
	l.expect(c, '"')

	// The result is never longer than what is left of the input, and an
	// escape only ever shrinks.
	buf := l.strAlloc(c.n)
	wSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: wSlot})

	put := func(b Reg) {
		w := l.load(wSlot, vInt)
		l.storeByte(buf, w, b)
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, w, l.constant(1)),
			Dst: NoReg, Imm: wSlot})
	}

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	b := l.peek(c)
	// Running off the end is a failure, not a string.
	atEnd := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe, l.at(c), c.n), Dst: NoReg, Imm: atEnd})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, b, l.constant('"')), Dst: NoReg, Imm: done})

	esc := l.newLabel()
	plain := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, b, l.constant('\\')), Dst: NoReg, Imm: esc})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: plain})

	l.mark(esc)
	l.advance(c)
	e := l.peek(c)
	l.advance(c)
	next := l.newLabel()
	for _, pair := range []struct{ from, to int64 }{
		{'n', '\n'}, {'t', '\t'}, {'r', '\r'}, {'b', 8}, {'f', 12},
	} {
		skip := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, e, l.constant(pair.from)),
			Dst: NoReg, Imm: skip})
		put(l.constant(pair.to))
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: next})
		l.mark(skip)
	}
	// \uXXXX: the code point, as UTF-8. A high surrogate followed by an
	// escaped low one is the pair's code point; a surrogate on its own
	// is U+FFFD, as Go's decoder has it. This used to keep only the low
	// byte of the four digits, so \u00e9 came out as a lone byte and
	// \u4e2d as "-".
	notU := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, e, l.constant('u')), Dst: NoReg, Imm: notU})
	cp := l.temp(vInt)
	l.setSlot(cp, l.hex4(c, 0))
	for i := 0; i < 4; i++ {
		l.advance(c)
	}
	ld := func() Reg { return l.load(cp, vInt) }
	between := func(v Reg, lo, hi int64) Reg {
		return l.logicalAnd(l.compare(OpGe, v, l.constant(lo)), l.compare(OpLe, v, l.constant(hi)))
	}
	noPair := l.newLabel()
	l.jumpNot(between(ld(), 0xD800, 0xDBFF), noPair)
	l.jumpNot(l.compare(OpEq, l.peekAt(c, 0), l.constant('\\')), noPair)
	l.jumpNot(l.compare(OpEq, l.peekAt(c, 1), l.constant('u')), noPair)
	low := l.temp(vInt)
	l.setSlot(low, l.hex4(c, 2))
	l.jumpNot(between(l.load(low, vInt), 0xDC00, 0xDFFF), noPair)
	l.setSlot(cp, l.arith(OpAdd, l.constant(0x10000), l.arith(OpAdd,
		l.arith(OpShl, l.arith(OpSub, ld(), l.constant(0xD800)), l.constant(10)),
		l.arith(OpSub, l.load(low, vInt), l.constant(0xDC00)))))
	for i := 0; i < 6; i++ {
		l.advance(c)
	}
	l.mark(noPair)
	lone := l.newLabel()
	l.jumpNot(between(ld(), 0xD800, 0xDFFF), lone)
	l.setSlot(cp, l.constant(0xFFFD))
	l.mark(lone)
	l.putUTF8(ld, put)
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: next})
	l.mark(notU)
	// Anything else after a backslash stands for itself, which covers
	// \" and \\ and \/.
	put(e)
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: next})

	l.mark(plain)
	put(b)
	l.advance(c)

	l.mark(next)
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(atEnd)
	l.fail(c)

	l.mark(done)
	l.advance(c) // the closing quote
	l.strEnd(buf, l.load(wSlot, vInt))
	return buf
}

// hex4 is the value of the four hex digits off places ahead.
func (l *lowerer) hex4(c *jsonCursor, off int64) Reg {
	v := l.constant(0)
	for i := int64(0); i < 4; i++ {
		v = l.arith(OpAdd, l.arith(OpShl, v, l.constant(4)), l.hexValue(l.peekAt(c, off+i)))
	}
	return v
}

// putUTF8 writes code point cp() as one to four bytes.
func (l *lowerer) putUTF8(cp func() Reg, put func(Reg)) {
	done := l.newLabel()
	two, three, four := l.newLabel(), l.newLabel(), l.newLabel()
	cont := func(shift int64) Reg {
		return l.arith(OpBOr, l.constant(0x80), l.arith(OpBAnd, l.arith(OpShr, cp(), l.constant(shift)), l.constant(63)))
	}
	l.jumpNot(l.compare(OpLt, cp(), l.constant(0x80)), two)
	put(cp())
	l.jump(done)
	l.mark(two)
	l.jumpNot(l.compare(OpLt, cp(), l.constant(0x800)), three)
	put(l.arith(OpBOr, l.constant(0xC0), l.arith(OpShr, cp(), l.constant(6))))
	put(cont(0))
	l.jump(done)
	l.mark(three)
	l.jumpNot(l.compare(OpLt, cp(), l.constant(0x10000)), four)
	put(l.arith(OpBOr, l.constant(0xE0), l.arith(OpShr, cp(), l.constant(12))))
	put(cont(6))
	put(cont(0))
	l.jump(done)
	l.mark(four)
	put(l.arith(OpBOr, l.constant(0xF0), l.arith(OpShr, cp(), l.constant(18))))
	put(cont(12))
	put(cont(6))
	put(cont(0))
	l.mark(done)
}

// peekAt is the character `off` places ahead of the cursor.
func (l *lowerer) peekAt(c *jsonCursor, off int64) Reg {
	out := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: out})
	past := l.newLabel()
	at := l.arith(OpAdd, l.at(c), l.constant(off))
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, at, c.n), Dst: NoReg, Imm: past})
	l.emit(Instr{Op: OpStore, A: l.loadByte(c.text, at), Dst: NoReg, Imm: out})
	l.mark(past)
	return l.load(out, vInt)
}

// hexValue turns one hex character into its value.
func (l *lowerer) hexValue(b Reg) Reg {
	digit := l.arith(OpSub, b, l.constant('0'))
	lower := l.arith(OpSub, b, l.constant('a'-10))
	upper := l.arith(OpSub, b, l.constant('A'-10))
	v := l.pick(l.compare(OpGe, b, l.constant('a')), lower, upper, vInt)
	return l.pick(l.compare(OpLe, b, l.constant('9')), digit, v, vInt)
}

// parseNumber reads the characters a JSON number can be made of and
// hands them to strtod, which is the same conversion the printer's
// inverse uses.
func (l *lowerer) parseNumber(c *jsonCursor) Reg {
	node := l.newJSONNode(jsonNumber)
	start := l.at(c)

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)
	b := l.peek(c)
	isDigit := l.logicalAnd(
		l.compare(OpGe, b, l.constant('0')), l.compare(OpLe, b, l.constant('9')))
	part := isDigit
	for _, ch := range []int64{'-', '+', '.', 'e', 'E'} {
		part = l.logicalOr(part, l.compare(OpEq, b, l.constant(ch)))
	}
	l.emit(Instr{Op: OpJumpNot, A: part, Dst: NoReg, Imm: done})
	l.advance(c)
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(done)

	text := l.substring(c.text, start, l.at(c))
	// An empty run means the character was not the start of anything.
	good := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGt, l.strLen(text), l.constant(0)),
		Dst: NoReg, Imm: good})
	l.fail(c)
	l.mark(good)

	l.emit(Instr{Op: OpStoreMem, A: node, B: text, Imm: jnStr})
	l.emit(Instr{Op: OpStoreMem, A: node,
		B:   l.ccall("strtod", []Reg{text, l.constant(0)}, []vty{vStr, vInt}, vFloat, false, false),
		Imm: jnNumber})
	return node
}

func (l *lowerer) parseArray(c *jsonCursor) Reg {
	node := l.newJSONNode(jsonArray)
	items := l.nodeField(node, jnItems, vNodeList)

	l.expect(c, '[')
	l.skipSpace(c)

	done := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.peek(c), l.constant(']')),
		Dst: NoReg, Imm: done})

	top := l.newLabel()
	l.mark(top)
	l.listPush(items, l.recurse(c))
	l.skipSpace(c)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, l.peek(c), l.constant(',')),
		Dst: NoReg, Imm: done})
	l.advance(c)
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	l.expect(c, ']')
	return node
}

func (l *lowerer) parseObject(c *jsonCursor) Reg {
	node := l.newJSONNode(jsonObject)
	keys := l.nodeField(node, jnKeys, vListOf(vStr))
	vals := l.nodeField(node, jnVals, vNodeList)

	l.expect(c, '{')
	l.skipSpace(c)

	done := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.peek(c), l.constant('}')),
		Dst: NoReg, Imm: done})

	top := l.newLabel()
	l.mark(top)
	l.skipSpace(c)
	l.listPush(keys, l.parseString(c))
	l.skipSpace(c)
	l.expect(c, ':')
	l.listPush(vals, l.recurse(c))
	l.skipSpace(c)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, l.peek(c), l.constant(',')),
		Dst: NoReg, Imm: done})
	l.advance(c)
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	l.expect(c, '}')
	return node
}

// recurse is the parser calling itself, from inside its own body. It
// cannot go through parseValue, which would try to emit the function it
// is already in the middle of emitting.
func (l *lowerer) recurse(c *jsonCursor) Reg {
	d := l.callHelper(jsonValueSym, []Reg{c.text, c.c}, []vty{vStr, vInt}, vStr)
	l.regTy[d] = vStr
	return d
}

// parseDocument parses a whole document and reports whether it was one:
// a value, then nothing but whitespace.
func (l *lowerer) parseDocument(text Reg) (node Reg, ok Reg) {
	cur := l.freshCursor()
	c := l.newCursor(text, cur)
	root := l.parseValue(c)
	l.skipSpace(c)

	clean := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe, l.at(c), c.n), Dst: NoReg, Imm: clean})
	l.fail(c)
	l.mark(clean)

	good := l.newReg()
	l.regTy[good] = vInt
	l.emit(Instr{Op: OpLoadMem, Dst: good, A: cur, B: NoReg, Imm: curOk})
	return root, l.compare(OpNe, good, l.constant(0))
}
