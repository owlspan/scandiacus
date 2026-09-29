package main

// json.decode: a parsed tree into a value of a named type.
//
// The shape comes from the annotation, so the conversion is generated
// per type rather than being one runtime function walking a description
// of itself. That is the opposite of the Go backend, where
// encoding/json reads the shape out of the value with reflection - and
// it is the same trade the encoder makes.
//
// Anything the tree does not have takes its zero value, which is what
// Unmarshal does: a missing field, a null, or a value of the wrong kind
// all leave the target as it was.

// decodeBuiltin lowers json.decode and json.decodeOr.
func (l *lowerer) decodeBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "json.decode":
		want, ok := vtyOf(c.T)
		if !ok || !want.res {
			l.errorAt(c, "json.decode needs to know what to decode into, and it can "+
				"fail - annotate the variable, as in: let p: Point! = json.decode(text)")
			return l.junk(), true
		}
		return l.jsonDecode(c, l.expr(c.Args[0]), want), true

	case "json.decodeOr":
		if len(c.Args) != 2 {
			l.errorAt(c, "json.decodeOr takes 2 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
		text := l.expr(c.Args[0])
		fallback := l.rvalue(c.Args[1])
		inner := l.regTy[fallback]
		r := l.jsonDecode(c, text, vResultOf(inner))
		return l.valueOrExpr(r, fallback, vResultOf(inner)), true
	}
	return NoReg, false
}

// jsonDecode parses the text and converts the tree, or explains why not.
// The conversion is one function per type, however many places decode
// one.
func (l *lowerer) jsonDecode(n Node, text Reg, want vty) Reg {
	sym := l.helperFunc("__json_dec "+want.String(), []vty{vStr}, want, func(a []Reg) {
		l.emit(Instr{Op: OpRet, A: l.jsonDecodeBody(n, a[0], want), Dst: NoReg})
	})
	return l.callHelper(sym, []Reg{text}, []vty{vStr}, want)
}

func (l *lowerer) jsonDecodeBody(n Node, text Reg, want vty) Reg {
	inner := want.inner()
	out := l.temp(want)

	node, ok := l.parseDocument(text)

	bad := l.newLabel()
	done := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: ok, Dst: NoReg, Imm: bad})

	value, good := l.fromJSON(n, node, inner)
	if !good {
		return l.junk()
	}
	l.emit(Instr{Op: OpStore, A: l.resOk(value, want), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(bad)
	l.emit(Instr{Op: OpStore, A: l.resFail(l.jsonReason(text), want), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, want)
}

// jsonReason is the failure text. The Go backend quotes the offending
// document with %q and truncates it, because a 40 kB response in an
// error message helps nobody.
func (l *lowerer) jsonReason(text Reg) Reg {
	short := l.pick(l.compare(OpGt, l.strLen(text), l.constant(60)),
		l.concat(l.substring(text, l.constant(0), l.constant(60)), l.strLit("...")),
		text, vStr)
	return l.concatAll(l.strLit("cannot decode \""), short,
		l.strLit("\": invalid JSON"))
}

// nodeKind is the tag a parsed node carries.
func (l *lowerer) nodeKind(node Reg) Reg {
	return l.nodeField(node, jnKind, vInt)
}

// fromJSON converts one node into a value of the wanted type. A node of
// the wrong kind yields the zero value rather than an error, matching
// what Unmarshal does with a mismatched field.
func (l *lowerer) fromJSON(n Node, node Reg, t vty) (Reg, bool) {
	if t.res || t.null {
		l.errorAt(n, "json.decode cannot produce a %s on the assembly backend yet", t)
		return NoReg, false
	}

	switch t.k {
	case kStr:
		return l.jsonWhenKind(node, jsonString, l.nodeField(node, jnStr, vStr),
			l.emptyStr(), vStr), true

	case kBool:
		got := l.compare(OpNe,
			l.jsonNumberAsInt(node), l.constant(0))
		return l.jsonWhenKind(node, jsonBool, got, l.boolConst(false), vBool), true

	case kInt:
		return l.jsonWhenKind(node, jsonNumber, l.jsonNumberAsInt(node),
			l.constant(0), vInt), true

	case kFloat:
		return l.jsonWhenKind(node, jsonNumber, l.nodeField(node, jnNumber, vFloat),
			l.floatConst(0), vFloat), true

	case kList:
		return l.jsonToList(n, node, t)

	case kMap:
		return l.jsonToMap(n, node, t)

	case kStruct:
		return l.jsonToStruct(n, node, t)
	}

	l.errorAt(n, "json.decode cannot produce a %s on the assembly backend yet", t)
	return NoReg, false
}

// jsonNumberAsInt reads a node's number as an integer. A bool stores its
// truth in the same word, so this reads both.
func (l *lowerer) jsonNumberAsInt(node Reg) Reg {
	isBool := l.compare(OpEq, l.nodeKind(node), l.constant(jsonBool))

	raw := l.newReg()
	l.regTy[raw] = vInt
	l.emit(Instr{Op: OpLoadMem, Dst: raw, A: node, B: NoReg, Imm: jnNumber})

	asInt := l.newReg()
	l.regTy[asInt] = vInt
	l.emit(Instr{Op: OpFloatToInt, Dst: asInt,
		A: l.nodeField(node, jnNumber, vFloat), B: NoReg})

	return l.pick(isBool, raw, asInt, vInt)
}

// jsonWhenKind is `node is of this kind ? got : zero`.
func (l *lowerer) jsonWhenKind(node Reg, kind int64, got, zero Reg, t vty) Reg {
	return l.pick(l.compare(OpEq, l.nodeKind(node), l.constant(kind)), got, zero, t)
}

func (l *lowerer) jsonToList(n Node, node Reg, t vty) (Reg, bool) {
	elem := t.elemType()
	out := l.newList(t, initialCap)

	skip := l.newLabel()
	l.emit(Instr{Op: OpJumpNot,
		A:   l.compare(OpEq, l.nodeKind(node), l.constant(jsonArray)),
		Dst: NoReg, Imm: skip})

	items := l.nodeField(node, jnItems, vNodeList)
	failed := false
	l.eachElement(items, vNodeList, func(_, item Reg) {
		v, ok := l.fromJSON(n, item, elem)
		if !ok {
			failed = true
			return
		}
		l.listPush(out, v)
	})
	if failed {
		return NoReg, false
	}

	l.mark(skip)
	return out, true
}

func (l *lowerer) jsonToMap(n Node, node Reg, t vty) (Reg, bool) {
	if t.key != kStr {
		l.errorAt(n, "a JSON object has string keys, so it cannot decode into %s", t)
		return NoReg, false
	}
	out := l.newMap(t, initialCap)

	skip := l.newLabel()
	l.emit(Instr{Op: OpJumpNot,
		A:   l.compare(OpEq, l.nodeKind(node), l.constant(jsonObject)),
		Dst: NoReg, Imm: skip})

	keys := l.nodeField(node, jnKeys, vListOf(vStr))
	vals := l.nodeField(node, jnVals, vNodeList)

	failed := false
	l.eachElement(keys, vListOf(vStr), func(i, key Reg) {
		v, ok := l.fromJSON(n, l.listGet(vals, i, vStr), t.elemType())
		if !ok {
			failed = true
			return
		}
		l.mapSet(out, key, v, t)
	})
	if failed {
		return NoReg, false
	}

	l.mark(skip)
	return out, true
}

func (l *lowerer) jsonToStruct(n Node, node Reg, t vty) (Reg, bool) {
	lay, ok := l.layoutOf(n, t)
	if !ok {
		return NoReg, false
	}

	obj := l.allocStruct(lay)
	for _, f := range lay.fields {
		// Every field is written, whether the document mentioned it or
		// not: a struct that leaves one uninitialised is a struct with a
		// wild pointer in it.
		found := l.jsonMember(node, f.name)
		v, good := l.fromJSON(n, found, f.t)
		if !good {
			return NoReg, false
		}
		l.emit(Instr{Op: OpStoreMem, A: obj, B: v, Imm: f.off, Comment: "." + f.name})
	}
	return obj, true
}

// jsonMember looks a key up in an object node, answering a null node
// when it is not there - so the caller never has to check, and a missing
// field takes the zero value its type gives.
func (l *lowerer) jsonMember(node Reg, name string) Reg {
	out := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: l.newJSONNode(jsonNull), Dst: NoReg, Imm: out})

	done := l.newLabel()
	l.emit(Instr{Op: OpJumpNot,
		A:   l.compare(OpEq, l.nodeKind(node), l.constant(jsonObject)),
		Dst: NoReg, Imm: done})

	keys := l.nodeField(node, jnKeys, vListOf(vStr))
	vals := l.nodeField(node, jnVals, vNodeList)
	want := l.strLit(name)

	l.eachElement(keys, vListOf(vStr), func(i, key Reg) {
		skip := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.strEq(key, want), Dst: NoReg, Imm: skip})
		l.emit(Instr{Op: OpStore, A: l.listGet(vals, i, vStr), Dst: NoReg, Imm: out})
		l.mark(skip)
	})

	l.mark(done)
	return l.load(out, vStr)
}
