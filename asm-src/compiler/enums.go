package main

// Enums: a fixed set of named variants.
//
//	enum State { Idle, Running, Done }
//	let s = State.Running
//
// A value is its variant's position, an int, so comparing two is one
// integer compare and a match on one is a chain of them. What makes it
// an enum rather than an int is the checker - no arithmetic, no mixing
// with ints or with another enum - and the name its vty carries here,
// which is how printing knows to write "Running" rather than 1.

// enumVariants holds the program's enums while it is being lowered.
// vtyOf is a plain function of a checked type, and a type annotation
// such as `s: State` reaches it as a name the parser could not tell
// from a struct's, so this is where it looks.
var enumVariants = map[string][]string{}

func vEnumOf(name string) vty { return vty{k: kInt, name: name} }

func isEnum(t vty) bool {
	return t.k == kInt && t.name != "" && t.name[0] != '*' && !t.res && !t.null
}

// enumNamed is the enum an expression names as a type, as in the State
// of State.Idle, or "" when it is a variable.
func (l *lowerer) enumNamed(e Expr) string {
	id, ok := e.(*Ident)
	if !ok {
		return ""
	}
	if _, local := l.lookup(id.Name); local {
		return ""
	}
	if _, ok := enumVariants[id.Name]; ok {
		return id.Name
	}
	return ""
}

// enumValue lowers State.Running.
func (l *lowerer) enumValue(x *Field, name string) Reg {
	for i, v := range enumVariants[name] {
		if v == x.Name {
			d := l.constant(int64(i))
			l.regTy[d] = vEnumOf(name)
			return d
		}
	}
	l.errorAt(x, "%s has no variant %q", name, x.Name)
	return l.junk()
}

// enumName is the name of the variant a value holds, as a string.
func (l *lowerer) enumName(v Reg) Reg {
	variants := enumVariants[l.regTy[v].name]
	out := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: l.strLit("?"), Dst: NoReg, Imm: out})
	done := l.newLabel()
	for i, name := range variants {
		next := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, v, l.constant(int64(i))), Dst: NoReg, Imm: next})
		l.emit(Instr{Op: OpStore, A: l.strLit(name), Dst: NoReg, Imm: out})
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
		l.mark(next)
	}
	l.mark(done)
	return l.load(out, vStr)
}

// dataEnums are the enums whose variants carry values, by name. The
// checker keeps each in a struct - see frontend/dataenums.go - and has
// already rewritten building one and matching on one, so all that is
// left here is printing it.
var dataEnums = map[string]*EnumDecl{}

// writeVariant prints a data enum value as the variant it is, with what
// it holds: Circle(2.0), Empty.
func (l *lowerer) writeVariant(n Node, v Reg, lay *structLayout, e *EnumDecl) {
	fieldOf := func(name string) (structField, bool) {
		for _, f := range lay.fields {
			if f.name == name {
				return f, true
			}
		}
		return structField{}, false
	}
	tf, _ := fieldOf(TagField)
	tag := l.loadField(v, tf)
	done := l.newLabel()
	for i, name := range e.Variants {
		next := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, tag, l.constant(int64(i))), Dst: NoReg, Imm: next})
		if e.Interface {
			// An interface prints as the struct it holds.
			f, _ := fieldOf(PayloadField(name, e.Payloads[i][0].Name))
			l.writeValue(n, l.loadField(v, f), f.t)
			l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
			l.mark(next)
			continue
		}
		l.writeLit(name)
		if e.Payloads[i] != nil {
			l.writeLit("(")
			for k, p := range e.Payloads[i] {
				if k > 0 {
					l.writeLit(", ")
				}
				f, _ := fieldOf(PayloadField(name, p.Name))
				l.writeValue(n, l.loadField(v, f), f.t)
			}
			l.writeLit(")")
		}
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
		l.mark(next)
	}
	l.mark(done)
}

// variantEqual compares two data enum values: the same variant, holding
// equal values. Another variant's fields hold nothing and are not read.
func (l *lowerer) variantEqual(n Node, a, b Reg, t vty, e *EnumDecl) (Reg, bool) {
	lay, ok := l.layoutOf(n, t)
	if !ok {
		return NoReg, false
	}
	fieldOf := func(name string) structField {
		f, _ := lay.field(name)
		return f
	}
	out := l.temp(vBool)
	l.emit(Instr{Op: OpStore, A: l.boolConst(false), Dst: NoReg, Imm: out})
	done := l.newLabel()

	tf := fieldOf(TagField)
	tagA := l.loadField(a, tf)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, tagA, l.loadField(b, tf)), Dst: NoReg, Imm: done})
	for i, name := range e.Variants {
		next := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, tagA, l.constant(int64(i))), Dst: NoReg, Imm: next})
		for _, p := range e.Payloads[i] {
			f := fieldOf(PayloadField(name, p.Name))
			same, good := l.deepEqual(n, l.loadField(a, f), l.loadField(b, f), f.t)
			if !good {
				return NoReg, false
			}
			l.emit(Instr{Op: OpJumpNot, A: same, Dst: NoReg, Imm: done})
		}
		l.emit(Instr{Op: OpStore, A: l.boolConst(true), Dst: NoReg, Imm: out})
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
		l.mark(next)
	}
	l.mark(done)
	return l.load(out, vBool), true
}

// zeroVariant is the zero value of a data enum, for a struct field or a
// list slot nobody gave one: its first variant that holds nothing, or
// failing that its first, holding zeros.
func (l *lowerer) zeroVariant(n Node, lay *structLayout, e *EnumDecl, depth int) Reg {
	if e.Interface || len(e.Variants) == 0 {
		// No struct at all: tag -1, which no method and no match takes.
		obj := l.allocStruct(lay)
		for _, f := range lay.fields {
			v := l.constant(-1)
			if f.name != TagField {
				v = l.constant(0)
			}
			l.regTy[v] = f.t
			l.emit(Instr{Op: OpStoreMem, A: obj, B: v, Imm: f.off})
		}
		return obj
	}
	pick := 0
	for i, p := range e.Payloads {
		if p == nil {
			pick = i
			break
		}
	}
	given := map[string]bool{TagField: true}
	for _, p := range e.Payloads[pick] {
		given[PayloadField(e.Variants[pick], p.Name)] = true
	}
	obj := l.allocStruct(lay)
	for _, f := range lay.fields {
		var v Reg
		switch {
		case f.name == TagField:
			v = l.constant(int64(pick))
		case given[f.name]:
			v = l.zeroOf(n, f.t, depth)
		default:
			v = l.constant(0)
			l.regTy[v] = f.t
		}
		l.emit(Instr{Op: OpStoreMem, A: obj, B: v, Imm: f.off})
	}
	return obj
}
