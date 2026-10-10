package main

// The primitives under the text builtins in prelude_text.go.
//
// input, pause, toFloat, isFloat, count, padLeft, padRight and the
// character-indexed substr, charAt and chars are all written in Veyl.
// What they need from here is the few things Veyl cannot say: one byte
// of a str without copying it, one byte from standard input, and the C
// runtime's decimal-to-float conversion.

// Standard input, as GetStdHandle names it: (DWORD)-10.
const stdInputHandle = -10

func (l *lowerer) textBuiltin(c *Call, name string) (Reg, bool) {
	arity := func(n int) bool {
		if len(c.Args) != n {
			l.errorAt(c, "%s takes %d argument(s), got %d", name, n, len(c.Args))
			return false
		}
		return true
	}

	switch name {
	case "__strByte":
		// The byte at a byte offset. The caller keeps the offset inside
		// the string; the terminator reads as 0.
		if !arity(2) {
			return l.junk(), true
		}
		s := l.expr(c.Args[0])
		d := l.newReg()
		l.regTy[d] = vInt
		l.emit(Instr{Op: OpLoadByte, Dst: d, A: s, B: l.intArg(c, 1)})
		return d, true

	case "__readByte":
		// One byte of standard input, or -1 at the end of it.
		//
		// ReadFile rather than the C runtime, so there is no second
		// buffer holding bytes a later read would have to know about:
		// every call takes exactly one byte from the handle. A console
		// hands the line over as the user finishes it and keeps the rest
		// for the next call, so reading one byte at a time is not one
		// keystroke at a time.
		//
		// The argument is scratch space of at least sixteen bytes: the
		// byte lands at 0 and the count ReadFile reports at 8.
		if !arity(1) {
			return l.junk(), true
		}
		buf := l.bytesArg(c, 0)

		// Anything the program printed has to be out before it waits,
		// or a prompt sits in printf's buffer while the user stares at
		// an empty console.
		l.ccall("fflush", []Reg{l.intConst(0)}, []vty{vInt}, vty{k: kVoid}, false, false)

		l.emit(Instr{Op: OpStoreMem, A: buf, B: l.constant(0), Imm: 8})
		h := l.ccall("GetStdHandle", []Reg{l.constant(stdInputHandle)}, []vty{vInt}, vInt, false, false)
		ok := l.blockingCall("ReadFile",
			[]Reg{h, buf, l.constant(1), l.arith(OpAdd, buf, l.constant(8)), l.constant(0)},
			[]vty{vInt, vBytes, vInt, vInt, vInt}, vInt, true)
		got := l.newReg()
		l.regTy[got] = vInt
		l.emit(Instr{Op: OpLoadMem, Dst: got, A: buf, B: NoReg, Imm: 8})

		b := l.newReg()
		l.regTy[b] = vInt
		l.emit(Instr{Op: OpLoadByte, Dst: b, A: buf, B: l.constant(0)})
		read := l.logicalAnd(l.compare(OpNe, ok, l.constant(0)), l.compare(OpNe, got, l.constant(0)))
		return l.pick(read, b, l.constant(-1), vInt), true

	case "__strtod":
		// The C runtime's conversion, on text the caller has already
		// checked is a decimal number and nothing else.
		if !arity(1) {
			return l.junk(), true
		}
		s := l.expr(c.Args[0])
		return l.ccall("strtod", []Reg{s, l.constant(0)}, []vty{vStr, vInt}, vFloat, false, false), true
	}
	return NoReg, false
}
