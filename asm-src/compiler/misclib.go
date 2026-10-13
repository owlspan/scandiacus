package main

// Two more private builtins for the prelude: a byte as a string, and the
// raw command line.
//
// Neither is in the language. __chr exists because percent-decoding has
// to turn a number back into a character and Veyl has no way to say
// that; __cmdline exists because a program built here has no C runtime
// startup, so there is no argv to read and the command line has to come
// from Windows directly.

func (l *lowerer) miscBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "__chr":
		if len(c.Args) != 1 {
			l.errorAt(c, "__chr takes 1 argument, got %d", len(c.Args))
			return l.junk(), true
		}
		v := l.expr(c.Args[0])
		if l.regTy[v].k != kInt {
			l.errorAt(c.Args[0], "__chr expects an int")
			return l.junk(), true
		}
		// Two bytes: the character and the terminator every string here
		// carries. A zero argument makes a one-byte string holding a
		// zero, since the length is in the header and not found by
		// looking for one.
		buf := l.strAlloc(l.constant(1))
		l.storeByte(buf, l.constant(0), v)
		l.storeByte(buf, l.constant(1), l.constant(0))
		return buf, true

	case "__strAt":
		if len(c.Args) != 2 {
			l.errorAt(c, "__strAt takes 2 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
		// The byte at an index of a str, as a number. charAt gives a
		// one-character string, which is no use for comparing codes.
		sv := l.expr(c.Args[0])
		if l.regTy[sv].k != kStr {
			l.errorAt(c.Args[0], "__strAt expects a str")
			return l.junk(), true
		}
		d := l.newReg()
		l.regTy[d] = vInt
		l.emit(Instr{Op: OpLoadByte, Dst: d, A: sv, B: l.expr(c.Args[1])})
		return d, true

	case "__isatty":
		if len(c.Args) != 0 {
			l.errorAt(c, "__isatty takes no arguments")
			return l.junk(), true
		}
		// Whether standard output is a console rather than a pipe or a
		// file. term uses it to decide whether colour would be read by a
		// terminal or written into somebody's log.
		r := l.ccall("_isatty", []Reg{l.constant(1)}, []vty{vInt}, vInt, true, false)
		return l.compare(OpNe, r, l.constant(0)), true

	case "__cmdline":
		if len(c.Args) != 0 {
			l.errorAt(c, "__cmdline takes no arguments")
			return l.junk(), true
		}
		// The pointer belongs to Windows; it is copied so it has the
		// header every string has.
		p := l.ccall("GetCommandLineA", nil, nil, vInt, false, false)
		return l.cstr(p), true
	}

	return NoReg, false
}
