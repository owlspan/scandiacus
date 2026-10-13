package main

// The os library, lowered in the IR on top of Win32.
//
// Everything here goes through ccall, so this file adds no opcode and no
// case in x64.go. That is what the foreign call op was for: a library
// function is a signature in library.go and a case below, not a new
// instruction.
//
// Win32 rather than the C runtime, for one reason: the failure text has
// to match. The Go backend's message for a missing file is Go printing a
// syscall.Errno, and that string comes from FormatMessage. fopen would
// have given an errno whose strerror text ("No such file or directory")
// is a different sentence, so the two backends would disagree about what
// a program prints the moment anything failed.
//
// Nothing here is freed, like everything else on this backend.

// Win32 constants. Named rather than written inline, because a wrong one
// is usually a call that quietly does the wrong thing rather than one
// that fails.
const (
	genericRead  = 0x80000000
	genericWrite = 0x40000000

	fileShareRead = 1

	createAlways = 2 // truncate, or create
	openExisting = 3 // fail if absent
	openAlways   = 4 // open, or create

	fileAttrNormal    = 0x80
	fileAttrDirectory = 0x10
	invalidFileAttrs  = -1 // 0xFFFFFFFF, sign-extended out of a C DWORD

	invalidHandle = -1

	fileEnd = 2 // SetFilePointer origin

	// GetFileAttributesExA's only info level, and the layout of the
	// WIN32_FILE_ATTRIBUTE_DATA it fills in: a DWORD of attributes,
	// three FILETIMEs, then the size as a pair of DWORDs.
	getFileExInfoStandard = 0
	attrDataSize          = 36
	attrDataSizeHigh      = 28
	attrDataSizeLow       = 32

	formatFromSystem   = 0x1000
	formatIgnoreInsert = 0x200

	// Long enough for any system message. FormatMessage truncates rather
	// than overflowing when it is given a size.
	msgBufSize = 512
)

// ptrSlot allocates one word of heap to serve as an out-parameter.
//
// Win32 hands several of its results back through a pointer, and the
// stack is not addressable from the IR - a virtual register has no
// address until the emitter puts it somewhere, and nothing here may
// depend on where that is. A word of heap has an address by
// construction.
func (l *lowerer) ptrSlot() Reg {
	p := l.allocObj(l.constant(wordSize), tagWords)
	l.emit(Instr{Op: OpStoreMem, A: p, B: l.constant(0), Imm: 0})
	return p
}

func (l *lowerer) loadPtr(p Reg) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: p, B: NoReg, Imm: 0})
	return d
}

// concat is `a ++ b` as an expression.
func (l *lowerer) concat(a, b Reg) Reg {
	l.mod.needs("concat")
	d := l.newReg()
	l.regTy[d] = vStr
	l.emit(Instr{Op: OpConcat, Dst: d, A: a, B: b})
	return d
}

func (l *lowerer) concatAll(parts ...Reg) Reg {
	out := parts[0]
	for _, p := range parts[1:] {
		out = l.concat(out, p)
	}
	return out
}

// logicalOr is `a || b` on two booleans that are already computed.
// Neither side can have an effect here, so there is nothing to
// short-circuit.
func (l *lowerer) logicalOr(a, b Reg) Reg {
	d := l.newReg()
	l.regTy[d] = vBool
	l.emit(Instr{Op: OpBOr, Dst: d, A: a, B: b})
	return d
}

// sysMessage is FormatMessage on the calling thread's last error, which
// is the same text Go prints for a syscall.Errno.
//
// The trailing "\r\n" FormatMessage appends is trimmed, because Go's
// errno strings do not carry one and the two have to agree character for
// character.
func (l *lowerer) sysMessage() Reg {
	code := l.ccall("GetLastError", nil, nil, vInt, true, false)

	buf := l.allocObj(l.constant(msgBufSize), tagBytes)
	l.regTy[buf] = vStr

	n := l.ccall("FormatMessageA",
		[]Reg{
			l.constant(formatFromSystem | formatIgnoreInsert),
			l.constant(0),
			code,
			l.constant(0),
			buf,
			l.constant(msgBufSize),
			l.constant(0),
		},
		[]vty{vInt, vInt, vInt, vInt, vStr, vInt, vInt},
		vInt, true, false)

	// Trim back over any trailing carriage returns and newlines. The
	// length comes from FormatMessage rather than from strlen, so a
	// message it could not produce at all (n of zero) trims nothing.
	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: n, Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	i := l.load(iSlot, vInt)
	more := l.compare(OpGt, i, l.constant(0))
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: done})

	last := l.arith(OpSub, i, l.constant(1))
	b := l.loadByte(buf, last)
	isCR := l.compare(OpEq, b, l.constant(13))
	isLF := l.compare(OpEq, b, l.constant(10))
	strip := l.logicalOr(isCR, isLF)
	l.emit(Instr{Op: OpJumpNot, A: strip, Dst: NoReg, Imm: done})

	l.emit(Instr{Op: OpStore, A: last, Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	return l.strFromMem(buf, l.load(iSlot, vInt))
}

// pathError is what Go's *PathError prints: the operation, the path,
// then the system's own sentence.
func (l *lowerer) pathError(op string, path Reg) Reg {
	return l.concatAll(l.strLit(op+" "), path, l.strLit(": "), l.sysMessage())
}

// why is Go's __why helper: `cannot <op> "<subject>": <reason>`. The
// quotes come from %q, which for a path holding no backslash or quote of
// its own is exactly a pair of double quotes around the text.
func (l *lowerer) why(op string, subject, reason Reg) Reg {
	return l.concatAll(l.strLit("cannot "+op+" \""), subject, l.strLit("\": "), reason)
}

// openFile is CreateFileA. The caller says what it means to do and what
// should happen when the file is not there.
func (l *lowerer) openFile(path Reg, access, disposition int64) Reg {
	return l.ccall("CreateFileA",
		[]Reg{
			path,
			l.constant(access),
			l.constant(fileShareRead),
			l.constant(0),
			l.constant(disposition),
			l.constant(fileAttrNormal),
			l.constant(0),
		},
		[]vty{vStr, vInt, vInt, vInt, vInt, vInt, vInt},
		vInt, false, false)
}

func (l *lowerer) closeHandle(h Reg) {
	l.ccall("CloseHandle", []Reg{h}, []vty{vInt}, vInt, true, false)
}

// osBuiltin lowers the os library. It reports false for a name it does
// not handle, so the caller can go on looking.
func (l *lowerer) osBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "os.file.read", "os.read.file":
		return l.readFile(c), true
	case "os.file.lines":
		return l.readLines(c), true
	case "os.file.write", "os.write.file":
		return l.writeFile(c, createAlways), true
	case "os.file.append", "os.append.file":
		return l.writeFile(c, openAlways), true
	case "os.file.exists":
		return l.fileExists(c), true
	case "os.file.size":
		return l.fileSize(c), true
	case "os.file.delete", "os.delete.file":
		return l.deleteFile(c), true
	case "os.file.rename":
		return l.renameFile(c), true
	case "os.dir.is":
		return l.isDir(c), true
	case "os.env.set":
		return l.setEnv(c), true
	}
	return NoReg, false
}

// readFile reads a whole file into a string.
//
// A Veyl string on this backend is NUL-terminated bytes, so a file
// holding a zero byte reads back short where the Go backend, which
// carries a length, reads it whole. That is a real difference and it is
// the reason `bytes` will have to be its own type here rather than a str
// in disguise.
func (l *lowerer) readFile(c *Call) Reg {
	t := vResultOf(vStr)
	path := l.expr(c.Args[0])

	out := l.temp(t)
	fail := l.newLabel()
	done := l.newLabel()

	h := l.openFile(path, genericRead, openExisting)
	bad := l.compare(OpEq, h, l.constant(invalidHandle))
	l.emit(Instr{Op: OpJumpIf, A: bad, Dst: NoReg, Imm: fail})

	sizeOut := l.ptrSlot()
	l.ccall("GetFileSizeEx", []Reg{h, sizeOut}, []vty{vInt, vInt}, vInt, true, false)
	size := l.loadPtr(sizeOut)

	buf := l.strAlloc(size)
	readOut := l.ptrSlot()
	l.ccall("ReadFile",
		[]Reg{h, buf, size, readOut, l.constant(0)},
		[]vty{vInt, vStr, vInt, vInt, vInt}, vInt, true, false)
	l.strEnd(buf, l.loadPtr(readOut))
	l.closeHandle(h)

	l.emit(Instr{Op: OpStore, A: l.resOk(buf, t), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(fail)
	reason := l.why("read", path, l.pathError("open", path))
	l.emit(Instr{Op: OpStore, A: l.resFail(reason, t), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, t)
}

// readLines is readFile split on line endings.
//
// It follows the Go backend exactly: CRLF becomes LF first, one trailing
// newline is dropped so a file ending in one does not yield a final
// empty line, and an empty file yields an empty list rather than a list
// holding "".
func (l *lowerer) readLines(c *Call) Reg {
	t := vResultOf(vListOf(vStr))
	text := l.readFile(c)
	textT := vResultOf(vStr)

	out := l.temp(t)
	done := l.newLabel()
	good := l.newLabel()

	ok := l.resIsOk(text)
	l.emit(Instr{Op: OpJumpIf, A: ok, Dst: NoReg, Imm: good})
	// The failure travels as it is. Its reason already names the read.
	l.emit(Instr{Op: OpStore, A: l.resFail(l.resErr(text), t), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(good)
	lines := l.splitLines(l.resValue(text, textT))
	l.emit(Instr{Op: OpStore, A: l.resOk(lines, t), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, t)
}

// splitLines is the loop behind readLines, over the bytes of a string.
//
// A carriage return immediately before a newline is skipped rather than
// replaced, which is the same result as rewriting CRLF to LF first and
// does not need a second copy of the text.
func (l *lowerer) splitLines(text Reg) Reg {
	list := l.newList(vListOf(vStr), initialCap)

	n := l.strLen(text)
	// Drop one trailing newline, and the carriage return in front of it.
	nSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: n, Dst: NoReg, Imm: nSlot})
	notNL := l.newLabel()
	last := l.arith(OpSub, n, l.constant(1))
	hasAny := l.compare(OpGt, n, l.constant(0))
	l.emit(Instr{Op: OpJumpNot, A: hasAny, Dst: NoReg, Imm: notNL})
	endsNL := l.compare(OpEq, l.loadByte(text, last), l.constant(10))
	l.emit(Instr{Op: OpJumpNot, A: endsNL, Dst: NoReg, Imm: notNL})
	l.emit(Instr{Op: OpStore, A: last, Dst: NoReg, Imm: nSlot})
	l.mark(notNL)

	// An empty text is an empty list, not a list holding one empty line.
	empty := l.newLabel()
	length := l.load(nSlot, vInt)
	any := l.compare(OpGt, length, l.constant(0))
	l.emit(Instr{Op: OpJumpNot, A: any, Dst: NoReg, Imm: empty})

	startSlot := l.temp(vInt)
	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: startSlot})
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	cont := l.newLabel()
	l.mark(top)

	i := l.load(iSlot, vInt)
	more := l.compare(OpLt, i, l.load(nSlot, vInt))
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: empty})

	isNL := l.compare(OpEq, l.loadByte(text, i), l.constant(10))
	l.emit(Instr{Op: OpJumpNot, A: isNL, Dst: NoReg, Imm: cont})

	l.listPush(list, l.lineAt(text, l.load(startSlot, vInt), i))
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, i, l.constant(1)),
		Dst: NoReg, Imm: startSlot})

	l.mark(cont)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(iSlot, vInt), l.constant(1)),
		Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(empty)
	// Whatever is left after the last newline is the final line, unless
	// the text ended on one.
	tail := l.newLabel()
	start := l.load(startSlot, vInt)
	end := l.load(nSlot, vInt)
	rest := l.compare(OpLt, start, end)
	l.emit(Instr{Op: OpJumpNot, A: rest, Dst: NoReg, Imm: tail})
	l.listPush(list, l.lineAt(text, start, end))
	l.mark(tail)

	return list
}

// lineAt is text[from:to] with a carriage return immediately before to
// left out, which is how a CRLF file reads the same as an LF one.
func (l *lowerer) lineAt(text, from, to Reg) Reg {
	endSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: to, Dst: NoReg, Imm: endSlot})

	keep := l.newLabel()
	any := l.compare(OpGt, to, from)
	l.emit(Instr{Op: OpJumpNot, A: any, Dst: NoReg, Imm: keep})
	before := l.arith(OpSub, to, l.constant(1))
	isCR := l.compare(OpEq, l.loadByte(text, before), l.constant(13))
	l.emit(Instr{Op: OpJumpNot, A: isCR, Dst: NoReg, Imm: keep})
	l.emit(Instr{Op: OpStore, A: before, Dst: NoReg, Imm: endSlot})
	l.mark(keep)

	return l.substring(text, from, l.load(endSlot, vInt))
}

// writeFile writes a string to a file, truncating or appending depending
// on the disposition it is handed.
func (l *lowerer) writeFile(c *Call, disposition int64) Reg {
	t := vResultOf(vVoid)
	path := l.expr(c.Args[0])
	text := l.expr(c.Args[1])

	out := l.temp(t)
	fail := l.newLabel()
	done := l.newLabel()

	h := l.openFile(path, genericWrite, disposition)
	bad := l.compare(OpEq, h, l.constant(invalidHandle))
	l.emit(Instr{Op: OpJumpIf, A: bad, Dst: NoReg, Imm: fail})

	if disposition == openAlways {
		// Appending. OPEN_ALWAYS leaves the position at the start, so
		// without this the existing contents are overwritten rather than
		// kept - which looks like a working append on an empty file and
		// only goes wrong on the second call.
		l.ccall("SetFilePointer",
			[]Reg{h, l.constant(0), l.constant(0), l.constant(fileEnd)},
			[]vty{vInt, vInt, vInt, vInt}, vInt, true, false)
	}

	writtenOut := l.ptrSlot()
	l.ccall("WriteFile",
		[]Reg{h, text, l.strLen(text), writtenOut, l.constant(0)},
		[]vty{vInt, vStr, vInt, vInt, vInt}, vInt, true, false)
	l.closeHandle(h)

	l.emit(Instr{Op: OpStore, A: l.resOk(l.constant(0), t), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(fail)
	l.emit(Instr{Op: OpStore, A: l.resFail(l.pathError("open", path), t),
		Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, t)
}

// attrs is GetFileAttributesA, which answers both "is it there" and "is
// it a directory" without opening anything.
func (l *lowerer) attrs(path Reg) Reg {
	return l.ccall("GetFileAttributesA", []Reg{path}, []vty{vStr}, vInt, true, false)
}

func (l *lowerer) fileExists(c *Call) Reg {
	return l.compare(OpNe, l.attrs(l.expr(c.Args[0])), l.constant(invalidFileAttrs))
}

func (l *lowerer) isDir(c *Call) Reg {
	a := l.attrs(l.expr(c.Args[0]))
	present := l.compare(OpNe, a, l.constant(invalidFileAttrs))
	bit := l.arith(OpBAnd, a, l.constant(fileAttrDirectory))
	directory := l.compare(OpNe, bit, l.constant(0))
	d := l.newReg()
	l.regTy[d] = vBool
	l.emit(Instr{Op: OpBAnd, Dst: d, A: present, B: directory})
	return d
}

// loadU32 reads a 32-bit little-endian field, a byte at a time.
//
// The IR can load a byte or a whole word and nothing between, and a
// word-sized load of a DWORD field would take the four bytes after it
// as well. Four byte loads is not fast, and it is exactly right.
func (l *lowerer) loadU32(base Reg, off int64) Reg {
	out := l.constant(0)
	for i := int64(3); i >= 0; i-- {
		b := l.loadByte(base, l.constant(off+i))
		out = l.arith(OpBOr, l.arith(OpShl, out, l.constant(8)), b)
	}
	return out
}

// fileSize measures a file without opening it, which is what os.Stat
// does. That is not an implementation detail: the failure message names
// the call that failed, so measuring with CreateFile here would print a
// different sentence than the Go backend does for the same program.
func (l *lowerer) fileSize(c *Call) Reg {
	t := vResultOf(vInt)
	path := l.expr(c.Args[0])

	out := l.temp(t)
	fail := l.newLabel()
	done := l.newLabel()

	// calloc rather than malloc: the high half of the size is only
	// written on a file large enough to need it, and reading whatever
	// malloc left there would make a small file enormous.
	data := l.ccall("calloc",
		[]Reg{l.constant(1), l.constant(attrDataSize)},
		[]vty{vInt, vInt}, vInt, false, false)

	rc := l.ccall("GetFileAttributesExA",
		[]Reg{path, l.constant(getFileExInfoStandard), data},
		[]vty{vStr, vInt, vInt}, vInt, true, false)
	bad := l.compare(OpEq, rc, l.constant(0))
	l.emit(Instr{Op: OpJumpIf, A: bad, Dst: NoReg, Imm: fail})

	high := l.loadU32(data, attrDataSizeHigh)
	low := l.loadU32(data, attrDataSizeLow)
	size := l.arith(OpBOr, l.arith(OpShl, high, l.constant(32)), low)

	l.emit(Instr{Op: OpStore, A: l.resOk(size, t), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(fail)
	reason := l.why("measure", path, l.pathError("GetFileAttributesEx", path))
	l.emit(Instr{Op: OpStore, A: l.resFail(reason, t), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, t)
}

// win32Action is the shape of every os call that either works or
// explains itself, and produces nothing when it works.
func (l *lowerer) win32Action(op, sym string, args []Reg, argTypes []vty, subject Reg) Reg {
	t := vResultOf(vVoid)
	out := l.temp(t)
	fail := l.newLabel()
	done := l.newLabel()

	rc := l.ccall(sym, args, argTypes, vInt, true, false)
	bad := l.compare(OpEq, rc, l.constant(0))
	l.emit(Instr{Op: OpJumpIf, A: bad, Dst: NoReg, Imm: fail})

	l.emit(Instr{Op: OpStore, A: l.resOk(l.constant(0), t), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(fail)
	l.emit(Instr{Op: OpStore, A: l.resFail(l.pathError(op, subject), t),
		Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, t)
}

func (l *lowerer) deleteFile(c *Call) Reg {
	path := l.expr(c.Args[0])
	return l.win32Action("remove", "DeleteFileA", []Reg{path}, []vty{vStr}, path)
}

func (l *lowerer) renameFile(c *Call) Reg {
	from := l.expr(c.Args[0])
	to := l.expr(c.Args[1])
	// MOVEFILE_REPLACE_EXISTING | MOVEFILE_COPY_ALLOWED, which is what
	// os.Rename asks for.
	return l.win32Action("rename", "MoveFileExA",
		[]Reg{from, to, l.constant(3)}, []vty{vStr, vStr, vInt}, from)
}

// setEnv goes through the C runtime rather than Win32, which is the one
// place in this file where that is the right answer.
//
// A process on Windows has two environments: the block Win32 keeps and
// the copy the C runtime made when it started. SetEnvironmentVariableA
// updates only the first, so a later getenv - which reads the second -
// would not see the value that was just set. _putenv updates both. Go's
// os.Setenv and os.Getenv are consistent with each other, so this has to
// be too, and the failure is silent: the set reports success and the get
// returns nothing.
func (l *lowerer) setEnv(c *Call) Reg {
	name := l.expr(c.Args[0])
	value := l.expr(c.Args[1])
	entry := l.concatAll(name, l.strLit("="), value)

	t := vResultOf(vVoid)
	out := l.temp(t)
	fail := l.newLabel()
	done := l.newLabel()

	// _putenv answers 0 for success, unlike every Win32 call here.
	rc := l.ccall("_putenv", []Reg{entry}, []vty{vStr}, vInt, true, false)
	bad := l.compare(OpNe, rc, l.constant(0))
	l.emit(Instr{Op: OpJumpIf, A: bad, Dst: NoReg, Imm: fail})

	l.emit(Instr{Op: OpStore, A: l.resOk(l.constant(0), t), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(fail)
	l.emit(Instr{Op: OpStore, A: l.resFail(l.pathError("setenv", name), t),
		Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, t)
}
