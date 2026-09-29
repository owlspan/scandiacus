package main

import "strings"

// json.get and its family: reading one value out of a document by path,
// with no type declared at the far end.
//
// This is the half of JSON that has nothing to do with the type system.
// It parses the text into the same tree json.decode uses, walks a
// dotted path, and renders whatever it lands on. A path that goes
// nowhere is quiet - the empty string, or zero - rather than an error,
// because these exist for poking at a document whose shape you are not
// sure of.
//
// A segment is a key, or an index when the node is an array: `tags.0`
// and `members.1.name` both work, and neither needs a schema.

// pathBuiltins lowers the query family.
func (l *lowerer) jsonPathBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "json.get", "json.int", "json.num", "json.bool", "json.count", "json.has":
		if len(c.Args) != 2 {
			l.errorAt(c, "%s takes 2 arguments, got %d", name, len(c.Args))
			return l.junk(), true
		}
	case "json.keys", "json.valid":
		if len(c.Args) != 1 {
			l.errorAt(c, "%s takes 1 argument, got %d", name, len(c.Args))
			return l.junk(), true
		}
	default:
		return NoReg, false
	}

	// The work is one helper per function, called with the arguments:
	// a parse and a walk come to a couple of thousand instructions, and
	// writing them out at every call made a program that asks a document
	// for fifteen values carry fifteen parsers.
	params := []vty{vStr}
	args := []Reg{l.expr(c.Args[0])}
	if len(c.Args) == 2 {
		params = append(params, vStr)
		args = append(args, l.expr(c.Args[1]))
	}
	ret := jsonPathRet[name]
	sym := l.helperFunc("__"+strings.ReplaceAll(name, ".", "_"), params, ret, func(a []Reg) {
		l.emit(Instr{Op: OpRet, A: l.jsonPathBody(name, a), Dst: NoReg})
	})
	return l.callHelper(sym, args, params, ret), true
}

// jsonPathRet is what each of the family gives back.
var jsonPathRet = map[string]vty{
	"json.get": vStr, "json.has": vBool, "json.int": vInt, "json.num": vFloat,
	"json.bool": vBool, "json.count": vInt, "json.keys": vListOf(vStr), "json.valid": vBool,
}

// jsonPathBody is one of the family, given its arguments.
func (l *lowerer) jsonPathBody(name string, a []Reg) Reg {
	switch name {
	case "json.valid":
		_, ok := l.parseDocument(a[0])
		return ok
	case "json.keys":
		root, ok := l.parseDocument(a[0])
		return l.jsonKeys(root, ok)
	}

	// The parse and the walk are shared by the whole family: one
	// function from text and path to the node found. A document that did
	// not parse behaves like one where the path is not there, so every
	// caller below sees the same empty answer.
	nodeSym := l.helperFunc("__json_node", []vty{vStr, vStr}, vStr, func(b []Reg) {
		root, ok := l.parseDocument(b[0])
		l.emit(Instr{Op: OpRet, A: l.pick(ok, l.walkPath(root, b[1]),
			l.newJSONNode(jsonNull), vStr), Dst: NoReg})
	})
	node := l.callHelper(nodeSym, []Reg{a[0], a[1]}, []vty{vStr, vStr}, vStr)

	switch name {
	case "json.get":
		return l.jsonScalarText(node)
	case "json.has":
		return l.compare(OpNe, l.nodeKind(node), l.constant(jsonNull))
	case "json.int":
		return l.jsonWhenKind(node, jsonNumber, l.jsonNumberAsInt(node),
			l.constant(0), vInt)
	case "json.num":
		return l.jsonWhenKind(node, jsonNumber, l.nodeField(node, jnNumber, vFloat),
			l.floatConst(0), vFloat)
	case "json.bool":
		return l.jsonWhenKind(node, jsonBool,
			l.compare(OpNe, l.jsonNumberAsInt(node), l.constant(0)),
			l.boolConst(false), vBool)
	case "json.count":
		return l.jsonCount(node)
	}
	return l.junk()
}

// jsonCount is how many elements an array has, or how many members an
// object has, and zero for anything else.
func (l *lowerer) jsonCount(node Reg) Reg {
	out := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: out})

	done := l.newLabel()
	notArray := l.newLabel()
	l.emit(Instr{Op: OpJumpNot,
		A:   l.compare(OpEq, l.nodeKind(node), l.constant(jsonArray)),
		Dst: NoReg, Imm: notArray})
	l.emit(Instr{Op: OpStore,
		A:   l.field(l.nodeField(node, jnItems, vNodeList), listLenOff, vInt),
		Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(notArray)
	l.emit(Instr{Op: OpJumpNot,
		A:   l.compare(OpEq, l.nodeKind(node), l.constant(jsonObject)),
		Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpStore,
		A:   l.field(l.nodeField(node, jnKeys, vListOf(vStr)), listLenOff, vInt),
		Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, vInt)
}

// jsonKeys is the member names of the top-level object, sorted, which is
// what the Go backend produces because it sorts them explicitly.
func (l *lowerer) jsonKeys(root, ok Reg) Reg {
	out := l.newList(vListOf(vStr), initialCap)

	done := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: ok, Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpJumpNot,
		A:   l.compare(OpEq, l.nodeKind(root), l.constant(jsonObject)),
		Dst: NoReg, Imm: done})

	keys := l.nodeField(root, jnKeys, vListOf(vStr))
	l.eachElement(keys, vListOf(vStr), func(_, k Reg) {
		l.insertSorted(out, k)
	})

	l.mark(done)
	return out
}

// jsonScalarText is what json.get answers: the text of a scalar, and a
// nested object or array re-encoded as JSON.
//
// Re-encoding rather than returning the original slice of the document
// is what the Go backend does, and it means the answer is normalised -
// whatever spacing the input had, this comes back compact.
func (l *lowerer) jsonScalarText(node Reg) Reg {
	out := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: l.emptyStr(), Dst: NoReg, Imm: out})

	done := l.newLabel()
	kind := l.nodeKind(node)

	// A string is its own text, unquoted.
	notStr := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, kind, l.constant(jsonString)),
		Dst: NoReg, Imm: notStr})
	l.emit(Instr{Op: OpStore, A: l.nodeField(node, jnStr, vStr), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
	l.mark(notStr)

	// A number keeps the spelling it had in the document, which is what
	// reading it back out of the text would give.
	notNum := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, kind, l.constant(jsonNumber)),
		Dst: NoReg, Imm: notNum})
	l.emit(Instr{Op: OpStore, A: l.nodeField(node, jnStr, vStr), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
	l.mark(notNum)

	notBool := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, kind, l.constant(jsonBool)),
		Dst: NoReg, Imm: notBool})
	l.mod.needs("booltostr")
	b := l.newReg()
	l.regTy[b] = vStr
	l.emit(Instr{Op: OpBoolToStr, Dst: b,
		A: l.compare(OpNe, l.jsonNumberAsInt(node), l.constant(0)), B: NoReg})
	l.emit(Instr{Op: OpStore, A: b, Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
	l.mark(notBool)

	// An array or an object is re-encoded. Null falls through with the
	// empty string, which is what a missing path answers too.
	container := l.logicalOr(
		l.compare(OpEq, kind, l.constant(jsonArray)),
		l.compare(OpEq, kind, l.constant(jsonObject)))
	l.emit(Instr{Op: OpJumpNot, A: container, Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpStore, A: l.reEncode(node), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, vStr)
}

// reEncodeSym renders a parsed node back to JSON. Like the parser it is
// a real function, and for the same reason: it recurses on the document
// rather than on a type.
const reEncodeSym = "jsonrender"

func (l *lowerer) reEncode(node Reg) Reg {
	l.emitJSONRenderer()
	d := l.callHelper(reEncodeSym, []Reg{node}, []vty{vStr}, vStr)
	l.regTy[d] = vStr
	return d
}

func (l *lowerer) emitJSONRenderer() {
	if l.helpers[reEncodeSym] {
		return
	}
	l.helperFunc(reEncodeSym, []vty{vStr}, vStr, func(args []Reg) {
		l.emit(Instr{Op: OpRet, A: l.renderNodeBody(args[0]), Dst: NoReg})
	})
}

// renderNodeBody is the body of that function: one node, compact.
func (l *lowerer) renderNodeBody(node Reg) Reg {
	out := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: l.strLit("null"), Dst: NoReg, Imm: out})

	kind := l.nodeKind(node)
	done := l.newLabel()

	branch := func(want int64, produce func() Reg) {
		next := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, kind, l.constant(want)),
			Dst: NoReg, Imm: next})
		l.emit(Instr{Op: OpStore, A: produce(), Dst: NoReg, Imm: out})
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
		l.mark(next)
	}

	branch(jsonString, func() Reg {
		return l.concatAll(l.strLit("\""),
			l.jsonEscape(l.nodeField(node, jnStr, vStr)), l.strLit("\""))
	})
	branch(jsonNumber, func() Reg { return l.nodeField(node, jnStr, vStr) })
	branch(jsonBool, func() Reg {
		l.mod.needs("booltostr")
		b := l.newReg()
		l.regTy[b] = vStr
		l.emit(Instr{Op: OpBoolToStr, Dst: b,
			A: l.compare(OpNe, l.jsonNumberAsInt(node), l.constant(0)), B: NoReg})
		return b
	})

	branch(jsonArray, func() Reg {
		acc := l.temp(vStr)
		l.emit(Instr{Op: OpStore, A: l.strLit("["), Dst: NoReg, Imm: acc})
		items := l.nodeField(node, jnItems, vNodeList)
		l.eachElement(items, vNodeList, func(i, item Reg) {
			first := l.newLabel()
			l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, i, l.constant(0)),
				Dst: NoReg, Imm: first})
			l.emit(Instr{Op: OpStore, A: l.concat(l.load(acc, vStr), l.strLit(",")),
				Dst: NoReg, Imm: acc})
			l.mark(first)
			l.emit(Instr{Op: OpStore,
				A:   l.concat(l.load(acc, vStr), l.renderChild(item)),
				Dst: NoReg, Imm: acc})
		})
		return l.concat(l.load(acc, vStr), l.strLit("]"))
	})

	branch(jsonObject, func() Reg {
		acc := l.temp(vStr)
		l.emit(Instr{Op: OpStore, A: l.strLit("{"), Dst: NoReg, Imm: acc})
		keys := l.nodeField(node, jnKeys, vListOf(vStr))
		vals := l.nodeField(node, jnVals, vNodeList)
		l.eachElement(keys, vListOf(vStr), func(i, k Reg) {
			first := l.newLabel()
			l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, i, l.constant(0)),
				Dst: NoReg, Imm: first})
			l.emit(Instr{Op: OpStore, A: l.concat(l.load(acc, vStr), l.strLit(",")),
				Dst: NoReg, Imm: acc})
			l.mark(first)
			l.emit(Instr{Op: OpStore,
				A: l.concatAll(l.load(acc, vStr),
					l.strLit("\""), l.jsonEscape(k), l.strLit("\":"),
					l.renderChild(l.listGet(vals, i, vStr))),
				Dst: NoReg, Imm: acc})
		})
		return l.concat(l.load(acc, vStr), l.strLit("}"))
	})

	l.mark(done)
	return l.load(out, vStr)
}

// renderChild is the renderer calling itself, from inside its own body.
func (l *lowerer) renderChild(node Reg) Reg {
	d := l.callHelper(reEncodeSym, []Reg{node}, []vty{vStr}, vStr)
	l.regTy[d] = vStr
	return d
}

// walkPath follows a dotted path from a node, answering a null node the
// moment a segment does not exist.
func (l *lowerer) walkPath(root, path Reg) Reg {
	here := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: root, Dst: NoReg, Imm: here})

	// An empty path is the document itself.
	done := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpGt, l.strLen(path), l.constant(0)),
		Dst: NoReg, Imm: done})

	segments := l.splitStr(path, l.strLit("."))
	l.eachElement(segments, vListOf(vStr), func(_, seg Reg) {
		l.emit(Instr{Op: OpStore, A: l.stepPath(l.load(here, vStr), seg),
			Dst: NoReg, Imm: here})
	})

	l.mark(done)
	return l.load(here, vStr)
}

// stepPath is one segment: a member of an object, or an element of an
// array when the segment reads as a number.
func (l *lowerer) stepPath(node, seg Reg) Reg {
	out := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: l.newJSONNode(jsonNull), Dst: NoReg, Imm: out})

	done := l.newLabel()
	kind := l.nodeKind(node)

	// An object: look the segment up by name.
	notObject := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, kind, l.constant(jsonObject)),
		Dst: NoReg, Imm: notObject})
	keys := l.nodeField(node, jnKeys, vListOf(vStr))
	vals := l.nodeField(node, jnVals, vNodeList)
	l.eachElement(keys, vListOf(vStr), func(i, k Reg) {
		skip := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.strEq(k, seg), Dst: NoReg, Imm: skip})
		l.emit(Instr{Op: OpStore, A: l.listGet(vals, i, vStr), Dst: NoReg, Imm: out})
		l.mark(skip)
	})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	// An array: the segment has to be a number, and in range.
	l.mark(notObject)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, kind, l.constant(jsonArray)),
		Dst: NoReg, Imm: done})
	idx, isNum := l.parseIntOK(seg)
	l.emit(Instr{Op: OpJumpNot, A: isNum, Dst: NoReg, Imm: done})
	items := l.nodeField(node, jnItems, vNodeList)
	inRange := l.logicalAnd(
		l.compare(OpGe, idx, l.constant(0)),
		l.compare(OpLt, idx, l.field(items, listLenOff, vInt)))
	l.emit(Instr{Op: OpJumpNot, A: inRange, Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpStore, A: l.listGet(items, idx, vStr), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, vStr)
}
