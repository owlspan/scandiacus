package main

// url.build and os.run.
//
// Two library functions that are one each away from a test program
// passing, and that have nothing else in common.

// moreBuiltin lowers this file's functions.
func (l *lowerer) moreBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "url.build":
		if len(c.Args) != 2 {
			l.errorAt(c, "url.build takes 2 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
		return l.urlBuild(c), true

	case "os.run":
		if len(c.Args) != 2 {
			l.errorAt(c, "os.run takes 2 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
		return l.osRun(c), true

	case "os.file.readOr":
		if len(c.Args) != 2 {
			l.errorAt(c, "os.file.readOr takes 2 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
		// The reading half is os.file.read; only what happens on
		// failure differs, and valueOr already says that.
		r := l.readFile(c)
		return l.valueOrExpr(r, l.expr(c.Args[1]), vResultOf(vStr)), true
	}

	if r, handled := l.pathBuiltin(c, name); handled {
		return r, true
	}
	return NoReg, false
}

// ---- url.build ----

// urlBuild appends a query string to a base URL.
//
// The parameters come out sorted, because a map here is stored sorted
// and because the Go backend sorts them explicitly - the same URL from
// the same map every time is what makes it cacheable and testable.
func (l *lowerer) urlBuild(c *Call) Reg {
	base := l.expr(c.Args[0])
	// Lowered with the wanted type carried in, because `{}` has no
	// element to infer from and this is the only context it has. The
	// checker already worked this out; the lowerer has to be told, since
	// it reads the literal itself rather than the type on it.
	strMap := vty{k: kMap, key: kStr, el: &vty{k: kStr}}
	params := l.rvalueAs(c.Args[1], strMap)
	t := l.regTy[params]
	if t.k != kMap || t.key != kStr || t.elemType().k != kStr {
		l.errorAt(c, "url.build needs a {str: str} of parameters, got %s", t)
		return l.junk()
	}

	out := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: base, Dst: NoReg, Imm: out})

	l.mapSort(params, t)
	done := l.newLabel()
	n := l.field(params, mapLenOff, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpGt, n, l.constant(0)),
		Dst: NoReg, Imm: done})

	// A base that already carries a query gets another parameter rather
	// than a second question mark.
	sep := l.pick(
		l.compare(OpGe, l.indexOfStr(base, l.strLit("?")), l.constant(0)),
		l.strLit("&"), l.strLit("?"), vStr)
	l.emit(Instr{Op: OpStore, A: l.concat(base, sep), Dst: NoReg, Imm: out})

	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	l.mark(top)
	i := l.load(iSlot, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, i, n), Dst: NoReg, Imm: done})

	amp := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, i, l.constant(0)),
		Dst: NoReg, Imm: amp})
	l.emit(Instr{Op: OpStore, A: l.concat(l.load(out, vStr), l.strLit("&")),
		Dst: NoReg, Imm: out})
	l.mark(amp)

	pair := l.concatAll(
		l.queryEscape(l.cellAt(params, mapKeysOff, i, vStr)),
		l.strLit("="),
		l.queryEscape(l.cellAt(params, mapValsOff, i, vStr)))
	l.emit(Instr{Op: OpStore, A: l.concat(l.load(out, vStr), pair),
		Dst: NoReg, Imm: out})

	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)),
		Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	return l.load(out, vStr)
}

// queryEscape is Go's url.QueryEscape: everything but the unreserved
// characters is percent-encoded, and a space becomes a plus.
//
// Written into a buffer sized for the worst case rather than built by
// concatenation, because a per-character concat would allocate a new
// string for every byte of the input.
func (l *lowerer) queryEscape(s Reg) Reg {
	n := l.strLen(s)
	buf := l.strAlloc(l.arith(OpMul, n, l.constant(3)))

	wSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: wSlot})

	l.eachByte(s, n, func(i Reg) {
		b := l.loadByte(s, i)
		w := l.load(wSlot, vInt)

		plain := l.newLabel()
		space := l.newLabel()
		next := l.newLabel()

		l.emit(Instr{Op: OpJumpIf, A: l.unreserved(b), Dst: NoReg, Imm: plain})
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, b, l.constant(32)),
			Dst: NoReg, Imm: space})

		// %XX, upper-case hex, which is what Go writes.
		l.storeByte(buf, w, l.constant('%'))
		l.storeByte(buf, l.arith(OpAdd, w, l.constant(1)),
			l.hexDigit(l.arith(OpShr, b, l.constant(4))))
		l.storeByte(buf, l.arith(OpAdd, w, l.constant(2)),
			l.hexDigit(l.arith(OpBAnd, b, l.constant(15))))
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, w, l.constant(3)),
			Dst: NoReg, Imm: wSlot})
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: next})

		l.mark(space)
		l.storeByte(buf, w, l.constant('+'))
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, w, l.constant(1)),
			Dst: NoReg, Imm: wSlot})
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: next})

		l.mark(plain)
		l.storeByte(buf, w, b)
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, w, l.constant(1)),
			Dst: NoReg, Imm: wSlot})

		l.mark(next)
	})

	l.storeByte(buf, l.load(wSlot, vInt), l.constant(0))
	return buf
}

// unreserved is Go's set: letters, digits, and the four punctuation
// characters that never need escaping.
func (l *lowerer) unreserved(b Reg) Reg {
	inRange := func(lo, hi int64) Reg {
		return l.logicalAnd(
			l.compare(OpGe, b, l.constant(lo)),
			l.compare(OpLe, b, l.constant(hi)))
	}
	ok := inRange('a', 'z')
	ok = l.logicalOr(ok, inRange('A', 'Z'))
	ok = l.logicalOr(ok, inRange('0', '9'))
	for _, c := range []int64{'-', '_', '.', '~'} {
		ok = l.logicalOr(ok, l.compare(OpEq, b, l.constant(c)))
	}
	return ok
}

// logicalAnd is `a && b` on two booleans that are already computed.
func (l *lowerer) logicalAnd(a, b Reg) Reg {
	d := l.newReg()
	l.regTy[d] = vBool
	l.emit(Instr{Op: OpBAnd, Dst: d, A: a, B: b})
	return d
}

// hexDigit turns 0-15 into its upper-case hex character.
func (l *lowerer) hexDigit(v Reg) Reg {
	return l.pick(l.compare(OpLt, v, l.constant(10)),
		l.arith(OpAdd, v, l.constant('0')),
		l.arith(OpAdd, v, l.constant('A'-10)), vInt)
}

// ---- os.run ----

// osRun runs a program and collects what it printed.
//
// Through _popen, so the command goes to cmd.exe rather than being
// executed directly. That is a real difference from the Go backend,
// where exec.Command takes the arguments as a list and no shell is
// involved: an argument holding a space or a quote is quoted here by the
// caller or not at all. Doing it properly needs CreateProcess and a
// pipe, which is a session of its own.
//
// The output is read a line at a time. Concatenating per line rather
// than per byte keeps the number of allocations to the number of lines.
func (l *lowerer) osRun(c *Call) Reg {
	t := vResultOf(vStr)
	name := l.expr(c.Args[0])
	args := l.rvalueAs(c.Args[1], vListOf(vStr))
	if l.regTy[args].k != kList || l.regTy[args].elemType().k != kStr {
		l.errorAt(c, "os.run needs a []str of arguments, got %s", l.regTy[args])
		return l.junk()
	}

	// The command line: the program, then every argument, space
	// separated. Redirecting stderr into stdout matches CombinedOutput.
	cmd := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: name, Dst: NoReg, Imm: cmd})
	l.eachElement(args, l.regTy[args], func(_, v Reg) {
		l.emit(Instr{Op: OpStore,
			A:   l.concatAll(l.load(cmd, vStr), l.strLit(" "), v),
			Dst: NoReg, Imm: cmd})
	})
	full := l.concat(l.load(cmd, vStr), l.strLit(" 2>&1"))

	out := l.temp(t)
	fail := l.newLabel()
	done := l.newLabel()

	f := l.ccall("_popen", []Reg{full, l.strLit("r")},
		[]vty{vStr, vStr}, vInt, false, false)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, f, l.constant(0)),
		Dst: NoReg, Imm: fail})

	const lineMax = 4096
	text := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: l.emptyStr(), Dst: NoReg, Imm: text})
	line := l.allocObj(l.constant(lineMax), tagBytes)
	l.regTy[line] = vStr

	top := l.newLabel()
	read := l.newLabel()
	l.mark(top)
	got := l.ccall("fgets", []Reg{line, l.constant(lineMax), f},
		[]vty{vStr, vInt, vInt}, vInt, false, false)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpNe, got, l.constant(0)),
		Dst: NoReg, Imm: read})
	l.emit(Instr{Op: OpStore, A: l.concat(l.load(text, vStr), l.copyStr(line)),
		Dst: NoReg, Imm: text})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(read)
	code := l.ccall("_pclose", []Reg{f}, []vty{vInt}, vInt, true, false)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpNe, code, l.constant(0)),
		Dst: NoReg, Imm: fail})

	l.emit(Instr{Op: OpStore, A: l.resOk(l.load(text, vStr), t), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(fail)
	// The Go backend puts the program's own output in the reason when it
	// wrote any, which is usually the more useful half of why it failed.
	l.emit(Instr{Op: OpStore,
		A:   l.resFail(l.why("run", name, l.trimStr(l.load(text, vStr))), t),
		Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, t)
}

// ---- paths ----

// Paths are pure string work here, and never touch the disk. Only the
// four that a program actually reaches for are in: join, dir, base and
// ext.
//
// A separator is either slash, because Windows accepts both and Veyl
// programs are written with forward ones. join produces a backslash,
// which is what filepath.Join does on this platform.

// lastSeparator is the index of the last / or \ in a path, or -1.
func (l *lowerer) lastSeparator(s Reg) Reg {
	found := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(-1), Dst: NoReg, Imm: found})

	n := l.strLen(s)
	l.eachByte(s, n, func(i Reg) {
		b := l.loadByte(s, i)
		isSep := l.logicalOr(
			l.compare(OpEq, b, l.constant('/')),
			l.compare(OpEq, b, l.constant('\\')))
		skip := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: isSep, Dst: NoReg, Imm: skip})
		l.emit(Instr{Op: OpStore, A: i, Dst: NoReg, Imm: found})
		l.mark(skip)
	})
	return l.load(found, vInt)
}

// pathBase is what follows the last separator, or the whole path.
func (l *lowerer) pathBase(s Reg) Reg {
	at := l.lastSeparator(s)
	return l.substring(s, l.arith(OpAdd, at, l.constant(1)), l.strLen(s))
}

// pathDir is what precedes the last separator. A path with none is ".",
// which is what filepath.Dir answers.
func (l *lowerer) pathDir(s Reg) Reg {
	at := l.lastSeparator(s)
	return l.pick(l.compare(OpLt, at, l.constant(0)),
		l.strLit("."), l.substring(s, l.constant(0), at), vStr)
}

// pathExt is the extension, dot included, or "" when the base has no
// dot. The dot is looked for in the base rather than in the whole path,
// so a directory with a dot in it does not become the extension.
func (l *lowerer) pathExt(s Reg) Reg {
	base := l.pathBase(s)
	n := l.strLen(base)

	found := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(-1), Dst: NoReg, Imm: found})
	l.eachByte(base, n, func(i Reg) {
		skip := l.newLabel()
		l.emit(Instr{Op: OpJumpNot,
			A:   l.compare(OpEq, l.loadByte(base, i), l.constant('.')),
			Dst: NoReg, Imm: skip})
		l.emit(Instr{Op: OpStore, A: i, Dst: NoReg, Imm: found})
		l.mark(skip)
	})

	at := l.load(found, vInt)
	return l.pick(l.compare(OpLt, at, l.constant(0)),
		l.emptyStr(), l.substring(base, at, n), vStr)
}

// replaceAll is strings.ReplaceAll: every occurrence, left to right.
func (l *lowerer) replaceAll(s, old, with Reg) Reg {
	out := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: l.emptyStr(), Dst: NoReg, Imm: out})

	n := l.strLen(s)
	oldLen := l.strLen(old)

	// An empty needle would match forever. Go inserts the replacement
	// between every character in that case; this hands the string back
	// untouched, which is a documented difference rather than a hang.
	done := l.newLabel()
	notEmpty := l.newLabel()
	empty := l.compare(OpEq, oldLen, l.constant(0))
	l.emit(Instr{Op: OpJumpNot, A: empty, Dst: NoReg, Imm: notEmpty})
	l.emit(Instr{Op: OpStore, A: s, Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
	l.mark(notEmpty)

	iSlot := l.temp(vInt)
	startSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: startSlot})

	top := l.newLabel()
	next := l.newLabel()
	tail := l.newLabel()
	l.mark(top)

	i := l.load(iSlot, vInt)
	fits := l.compare(OpLe, l.arith(OpAdd, i, oldLen), n)
	l.emit(Instr{Op: OpJumpNot, A: fits, Dst: NoReg, Imm: tail})

	here := l.substring(s, i, l.arith(OpAdd, i, oldLen))
	l.emit(Instr{Op: OpJumpNot, A: l.strEq(here, old), Dst: NoReg, Imm: next})

	l.emit(Instr{Op: OpStore,
		A: l.concatAll(l.load(out, vStr),
			l.substring(s, l.load(startSlot, vInt), i), with),
		Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, oldLen), Dst: NoReg, Imm: startSlot})
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, oldLen), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(next)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)), Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(tail)
	l.emit(Instr{Op: OpStore,
		A:   l.concat(l.load(out, vStr), l.substring(s, l.load(startSlot, vInt), n)),
		Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, vStr)
}

// pathBuiltin lowers the path helpers, replace, and the three questions
// a program asks about the machine it is on.
func (l *lowerer) pathBuiltin(c *Call, name string) (Reg, bool) {
	one := func() (Reg, bool) {
		if len(c.Args) != 1 {
			l.errorAt(c, "%s takes 1 argument, got %d", name, len(c.Args))
			return l.junk(), false
		}
		return l.expr(c.Args[0]), true
	}

	switch name {
	case "os.path.base":
		s, ok := one()
		if !ok {
			return s, true
		}
		return l.pathBase(s), true

	case "os.path.dir":
		s, ok := one()
		if !ok {
			return s, true
		}
		return l.pathDir(s), true

	case "os.path.ext":
		s, ok := one()
		if !ok {
			return s, true
		}
		return l.pathExt(s), true

	case "os.path.join":
		if len(c.Args) == 0 {
			l.errorAt(c, "os.path.join takes at least 1 argument")
			return l.junk(), true
		}
		out := l.expr(c.Args[0])
		for _, a := range c.Args[1:] {
			out = l.concatAll(out, l.strLit("\\"), l.expr(a))
		}
		return out, true

	case "replace":
		if len(c.Args) != 3 {
			l.errorAt(c, "replace takes 3 arguments, got %d", len(c.Args))
			return l.junk(), true
		}
		return l.replaceAll(l.expr(c.Args[0]), l.expr(c.Args[1]), l.expr(c.Args[2])), true

	case "os.pid":
		return l.ccall("GetCurrentProcessId", nil, nil, vInt, true, false), true

	case "os.cpus":
		// ALL_PROCESSOR_GROUPS, so this counts every core the machine
		// has rather than only the group this thread is in.
		return l.ccall("GetActiveProcessorCount", []Reg{l.constant(0xFFFF)},
			[]vty{vInt}, vInt, true, false), true

	case "os.hostname":
		// MAX_COMPUTERNAME_LENGTH is 15, plus the terminator; the buffer
		// is generous because GetComputerNameA is told its size and
		// fails rather than overruns.
		const nameMax = 256
		buf := l.allocObj(l.constant(nameMax), tagBytes)
		l.regTy[buf] = vStr
		size := l.ptrSlot()
		l.emit(Instr{Op: OpStoreMem, A: size, B: l.constant(nameMax), Imm: 0})
		l.ccall("GetComputerNameA", []Reg{buf, size},
			[]vty{vStr, vInt}, vInt, true, false)
		return buf, true

	case "os.name":
		// This compiler emits a Windows PE and nothing else, so the
		// answer is known at compile time rather than asked for.
		return l.strLit("windows"), true

	case "os.arch":
		return l.strLit("amd64"), true

	case "os.dir.current":
		const pathMax = 32768
		buf := l.allocObj(l.constant(pathMax), tagBytes)
		l.regTy[buf] = vStr
		l.ccall("GetCurrentDirectoryA", []Reg{l.constant(pathMax), buf},
			[]vty{vInt, vStr}, vInt, true, false)
		return buf, true

	case "os.dir.change":
		if len(c.Args) != 1 {
			l.errorAt(c, "os.dir.change takes 1 argument, got %d", len(c.Args))
			return l.junk(), true
		}
		return l.dirChange(l.expr(c.Args[0])), true

	case "os.path.absolute":
		if len(c.Args) != 1 {
			l.errorAt(c, "os.path.absolute takes 1 argument, got %d", len(c.Args))
			return l.junk(), true
		}
		const pathMax = 32768
		buf := l.allocObj(l.constant(pathMax), tagBytes)
		l.regTy[buf] = vStr
		l.ccall("GetFullPathNameA",
			[]Reg{l.expr(c.Args[0]), l.constant(pathMax), buf, l.constant(0)},
			[]vty{vStr, vInt, vStr, vInt}, vInt, true, false)
		return buf, true
	}
	return NoReg, false
}

// dirChange is SetCurrentDirectory, reporting why rather than a bool.
func (l *lowerer) dirChange(path Reg) Reg {
	ret := vty{k: kVoid, res: true}
	sym := l.helperFunc("dirchange", []vty{vStr}, ret, func(a []Reg) {
		ok := l.ccall("SetCurrentDirectoryA", []Reg{a[0]},
			[]vty{vStr}, vInt, true, false)
		bad := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.asBool(ok), Dst: NoReg, Imm: bad})
		l.emit(Instr{Op: OpRet, A: l.resOk(l.constant(0), ret), Dst: NoReg})
		l.mark(bad)
		l.emit(Instr{Op: OpRet,
			A:   l.resFail(l.why("change directory to", a[0], l.sysMessage()), ret),
			Dst: NoReg},
		)
	})
	return l.callHelper(sym, []Reg{path}, []vty{vStr}, ret)
}
