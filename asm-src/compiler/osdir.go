package main

// Directories: listing, making and removing.
//
// Split from os.go because all three need the same thing - walking a
// WIN32_FIND_DATAA - and because removing a directory tree is the only
// os call here that is a loop rather than a single syscall.

const (
	// WIN32_FIND_DATAA. Only two fields are read: the attributes, to
	// tell a subdirectory from a file, and the name.
	findDataSize  = 320
	findDataAttrs = 0
	findDataName  = 44

	errorAlreadyExists = 183
	errorFileNotFound  = 2
)

func (l *lowerer) dirBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "os.dir.list", "os.list.dir":
		return l.dirList(c), true
	case "os.dir.make", "os.make.dir":
		return l.dirMake(c), true
	case "os.dir.delete":
		return l.dirDelete(c), true
	}
	return NoReg, false
}

// strEq is string equality as an expression.
func (l *lowerer) strEq(a, b Reg) Reg {
	l.mod.needs("streq")
	d := l.newReg()
	l.regTy[d] = vBool
	l.emit(Instr{Op: OpStrEq, Dst: d, A: a, B: b})
	return d
}

// copyStr takes a private copy of a string.
//
// FindNextFile writes the next name over the same buffer, so a name kept
// as a pointer into that buffer becomes whatever the following entry is
// called. This was a bug before it was a comment: the listing came back
// as the last filename repeated.
// copyStr copies a name a C API wrote, which has no header.
func (l *lowerer) copyStr(s Reg) Reg {
	return l.cstr(s)
}

// findName is the address of the cFileName field of a find record: C
// memory, not a string, until copyStr makes one of it.
func (l *lowerer) findName(data Reg) Reg {
	return l.arith(OpAdd, data, l.constant(findDataName))
}

// findIsDir reports whether the current find record is a directory.
func (l *lowerer) findIsDir(data Reg) Reg {
	attrs := l.loadU32(data, findDataAttrs)
	bit := l.arith(OpBAnd, attrs, l.constant(fileAttrDirectory))
	return l.compare(OpNe, bit, l.constant(0))
}

// listNames walks a directory and returns the entry names, sorted, with
// "." and ".." left out.
//
// Sorted because os.ReadDir sorts, and a listing that came back in the
// file system's own order would agree with the Go backend on most
// directories and disagree on some - the worst kind of difference to
// find. The sort is an insertion into an already-ordered list, which is
// quadratic and is fine for the size a directory listing is.
//
// The handle is returned to the caller's `bad` label when the directory
// cannot be opened at all.
func (l *lowerer) listNames(path Reg, onFail int64) Reg {
	names := l.newList(vListOf(vStr), initialCap)

	pattern := l.concatAll(path, l.strLit("\\*"))
	data := l.ccall("calloc", []Reg{l.constant(1), l.constant(findDataSize)},
		[]vty{vInt, vInt}, vInt, false, false)

	h := l.ccall("FindFirstFileA", []Reg{pattern, data},
		[]vty{vStr, vInt}, vInt, false, false)
	bad := l.compare(OpEq, h, l.constant(invalidHandle))
	l.emit(Instr{Op: OpJumpIf, A: bad, Dst: NoReg, Imm: onFail})

	top := l.newLabel()
	next := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	// Copied first: the name is C memory inside the find record, and a
	// string compare reads the header a copy has.
	name := l.copyStr(l.findName(data))
	nameSlot := l.temp(vStr)
	l.emit(Instr{Op: OpStore, A: name, Dst: NoReg, Imm: nameSlot})
	isDot := l.strEq(l.load(nameSlot, vStr), l.strLit("."))
	isDotDot := l.strEq(l.load(nameSlot, vStr), l.strLit(".."))
	skip := l.logicalOr(isDot, isDotDot)
	l.emit(Instr{Op: OpJumpIf, A: skip, Dst: NoReg, Imm: next})

	l.insertSorted(names, l.load(nameSlot, vStr))

	l.mark(next)
	more := l.ccall("FindNextFileA", []Reg{h, data}, []vty{vInt, vInt},
		vInt, true, false)
	keep := l.compare(OpNe, more, l.constant(0))
	l.emit(Instr{Op: OpJumpIf, A: keep, Dst: NoReg, Imm: top})

	l.mark(done)
	l.ccall("FindClose", []Reg{h}, []vty{vInt}, vInt, true, false)
	return names
}

// insertSorted puts one string into a list that is already in order.
func (l *lowerer) insertSorted(list, s Reg) {
	// Grow by one at the end, then shift the tail right until the new
	// value is in its place. Pushing first means the list has the room
	// before anything moves.
	l.listPush(list, s)

	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, l.field(list, listLenOff, vInt),
		l.constant(1)), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	i := l.load(iSlot, vInt)
	notFirst := l.compare(OpGt, i, l.constant(0))
	l.emit(Instr{Op: OpJumpNot, A: notFirst, Dst: NoReg, Imm: done})

	prev := l.arith(OpSub, i, l.constant(1))
	before := l.listGet(list, prev, vStr)
	here := l.listGet(list, i, vStr)
	cmp := l.ccall("strcmp", []Reg{before, here}, []vty{vStr, vStr}, vInt, true, false)
	inOrder := l.compare(OpLe, cmp, l.constant(0))
	l.emit(Instr{Op: OpJumpIf, A: inOrder, Dst: NoReg, Imm: done})

	l.listSet(list, prev, here)
	l.listSet(list, i, before)
	l.emit(Instr{Op: OpStore, A: prev, Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
}

func (l *lowerer) dirList(c *Call) Reg {
	t := vResultOf(vListOf(vStr))
	path := l.expr(c.Args[0])

	out := l.temp(t)
	fail := l.newLabel()
	done := l.newLabel()

	names := l.listNames(path, fail)
	l.emit(Instr{Op: OpStore, A: l.resOk(names, t), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(fail)
	reason := l.why("list", path, l.pathError("open", path))
	l.emit(Instr{Op: OpStore, A: l.resFail(reason, t), Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, t)
}

// dirMake is os.MkdirAll: every missing parent is created too, and a
// directory that is already there is not an error.
func (l *lowerer) dirMake(c *Call) Reg {
	t := vResultOf(vVoid)
	path := l.expr(c.Args[0])

	out := l.temp(t)
	fail := l.newLabel()
	done := l.newLabel()

	// Walk the path and create each prefix as a separator is passed.
	// Failures on a prefix are ignored: the only one that matters is the
	// whole path, and a prefix that is a drive letter or an existing
	// directory fails for reasons that are not this call's business.
	n := l.strLen(path)
	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(1), Dst: NoReg, Imm: iSlot})

	top := l.newLabel()
	next := l.newLabel()
	walked := l.newLabel()
	l.mark(top)

	i := l.load(iSlot, vInt)
	more := l.compare(OpLt, i, n)
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: walked})

	b := l.loadByte(path, i)
	isSlash := l.logicalOr(
		l.compare(OpEq, b, l.constant('/')),
		l.compare(OpEq, b, l.constant('\\')))
	l.emit(Instr{Op: OpJumpNot, A: isSlash, Dst: NoReg, Imm: next})
	l.ccall("CreateDirectoryA",
		[]Reg{l.substring(path, l.constant(0), i), l.constant(0)},
		[]vty{vStr, vInt}, vInt, true, false)

	l.mark(next)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(iSlot, vInt), l.constant(1)),
		Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(walked)
	rc := l.ccall("CreateDirectoryA", []Reg{path, l.constant(0)},
		[]vty{vStr, vInt}, vInt, true, false)
	made := l.compare(OpNe, rc, l.constant(0))
	good := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: made, Dst: NoReg, Imm: good})

	// Already there is success, which is what MkdirAll means. Anything
	// else is the failure this call reports.
	err := l.ccall("GetLastError", nil, nil, vInt, true, false)
	existed := l.compare(OpEq, err, l.constant(errorAlreadyExists))
	l.emit(Instr{Op: OpJumpNot, A: existed, Dst: NoReg, Imm: fail})

	l.mark(good)
	l.emit(Instr{Op: OpStore, A: l.resOk(l.constant(0), t), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(fail)
	l.emit(Instr{Op: OpStore, A: l.resFail(l.pathError("mkdir", path), t),
		Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, t)
}

// dirDelete is os.RemoveAll: the whole tree, and a path that is not
// there at all is success.
//
// Written as two passes over a worklist rather than as a recursion,
// because this is lowered inline and a recursive lowering would inline
// itself forever. The first pass walks breadth-first, deleting files as
// it meets them and collecting directories; the second removes the
// directories back to front, which is the post-order the file system
// requires.
func (l *lowerer) dirDelete(c *Call) Reg {
	t := vResultOf(vVoid)
	path := l.expr(c.Args[0])

	out := l.temp(t)
	fail := l.newLabel()
	done := l.newLabel()
	ok := l.newLabel()

	a := l.attrs(path)
	absent := l.compare(OpEq, a, l.constant(invalidFileAttrs))
	l.emit(Instr{Op: OpJumpIf, A: absent, Dst: NoReg, Imm: ok})

	// A plain file is removed as one.
	isDir := l.compare(OpNe, l.arith(OpBAnd, a, l.constant(fileAttrDirectory)),
		l.constant(0))
	tree := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: isDir, Dst: NoReg, Imm: tree})
	rcFile := l.ccall("DeleteFileA", []Reg{path}, []vty{vStr}, vInt, true, false)
	goneFile := l.compare(OpNe, rcFile, l.constant(0))
	l.emit(Instr{Op: OpJumpIf, A: goneFile, Dst: NoReg, Imm: ok})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: fail})

	l.mark(tree)
	dirs := l.newList(vListOf(vStr), initialCap)
	l.listPush(dirs, path)

	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: iSlot})

	outer := l.newLabel()
	collected := l.newLabel()
	l.mark(outer)

	i := l.load(iSlot, vInt)
	more := l.compare(OpLt, i, l.field(dirs, listLenOff, vInt))
	l.emit(Instr{Op: OpJumpNot, A: more, Dst: NoReg, Imm: collected})

	dir := l.listGet(dirs, i, vStr)
	// A directory that cannot be listed is one that cannot be emptied,
	// so the whole removal fails rather than reporting a success that
	// left files behind.
	names := l.listNames(dir, fail)

	jSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: jSlot})
	inner := l.newLabel()
	innerDone := l.newLabel()
	l.mark(inner)

	j := l.load(jSlot, vInt)
	moreNames := l.compare(OpLt, j, l.field(names, listLenOff, vInt))
	l.emit(Instr{Op: OpJumpNot, A: moreNames, Dst: NoReg, Imm: innerDone})

	full := l.concatAll(dir, l.strLit("\\"), l.listGet(names, j, vStr))
	childAttrs := l.attrs(full)
	childIsDir := l.compare(OpNe,
		l.arith(OpBAnd, childAttrs, l.constant(fileAttrDirectory)), l.constant(0))
	pushDir := l.newLabel()
	nextName := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: childIsDir, Dst: NoReg, Imm: pushDir})
	l.ccall("DeleteFileA", []Reg{full}, []vty{vStr}, vInt, true, false)
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: nextName})
	l.mark(pushDir)
	l.listPush(dirs, full)

	l.mark(nextName)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(jSlot, vInt), l.constant(1)),
		Dst: NoReg, Imm: jSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: inner})

	l.mark(innerDone)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(iSlot, vInt), l.constant(1)),
		Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: outer})

	l.mark(collected)
	// Back to front, so a directory is empty by the time it is removed.
	kSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, l.field(dirs, listLenOff, vInt),
		l.constant(1)), Dst: NoReg, Imm: kSlot})

	back := l.newLabel()
	removed := l.newLabel()
	l.mark(back)

	k := l.load(kSlot, vInt)
	left := l.compare(OpGe, k, l.constant(0))
	l.emit(Instr{Op: OpJumpNot, A: left, Dst: NoReg, Imm: removed})
	rc := l.ccall("RemoveDirectoryA", []Reg{l.listGet(dirs, k, vStr)},
		[]vty{vStr}, vInt, true, false)
	gone := l.compare(OpNe, rc, l.constant(0))
	l.emit(Instr{Op: OpJumpNot, A: gone, Dst: NoReg, Imm: fail})
	l.emit(Instr{Op: OpStore, A: l.arith(OpSub, k, l.constant(1)),
		Dst: NoReg, Imm: kSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: back})

	l.mark(removed)
	l.mark(ok)
	l.emit(Instr{Op: OpStore, A: l.resOk(l.constant(0), t), Dst: NoReg, Imm: out})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	l.mark(fail)
	l.emit(Instr{Op: OpStore, A: l.resFail(l.pathError("remove", path), t),
		Dst: NoReg, Imm: out})

	l.mark(done)
	return l.load(out, t)
}
