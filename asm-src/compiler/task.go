package main

// Structured concurrency, on real Windows threads.
//
// task.map is map with the callback run on a thread per element. The
// results come back in the order the list was in, not the order the
// threads finished, which is the whole reason a job carries the slot it
// writes to rather than pushing onto a shared list.
//
// The thread entry is an ordinary function lowered by this compiler.
// That works because a Veyl function already takes its argument in rcx
// and returns in rax, which is exactly what Windows wants of a
// LPTHREAD_START_ROUTINE, so no hand-written trampoline is needed. It
// has to be a named function rather than a closure, since a lifted
// literal reads the environment register in its prologue and
// CreateThread has nowhere to put one.

import (
	"fmt"
	"strings"
)

// One job: the function to run, its argument, and where the answer goes.
const (
	jobCloOff = 0
	jobArgOff = wordSize
	jobResOff = 2 * wordSize
	jobWords  = 3
)

// waitForever is INFINITE, the timeout that never expires.
const waitForever = -1

func (l *lowerer) taskBuiltin(c *Call, name string) (Reg, bool) {
	want := map[string]int{
		"task.map": 2, "task.each": 2, "task.mapLimit": 3, "task.all": 1,
	}
	n, ok := want[name]
	if !ok {
		return NoReg, false
	}
	if len(c.Args) != n {
		l.errorAt(c, "%s takes %d argument(s), got %d", name, n, len(c.Args))
		return l.junk(), true
	}

	xs := l.expr(c.Args[0])
	lt := l.regTy[xs]
	if lt.k != kList {
		l.errorAt(c.Args[0], "%s expects a list, got %s", name, lt)
		return l.junk(), true
	}
	elem := lt.elemType()

	// task.all takes a list of functions and no separate callback: each
	// element is its own job, called with nothing.
	if name == "task.all" {
		if elem.k != kFunc {
			l.errorAt(c.Args[0], "task.all expects a list of functions, got %s", lt)
			return l.junk(), true
		}
		l.taskRun(xs, NoReg, vVoid, vVoid, NoReg, false)
		return l.junk(), true
	}

	fi := 1
	if name == "task.mapLimit" {
		fi = 2
	}
	f := l.expr(c.Args[fi])
	ft := l.regTy[f]
	if ft.k != kFunc || ft.fn == nil {
		l.errorAt(c.Args[fi], "%s expects a function here, got %s", name, ft)
		return l.junk(), true
	}

	limit := NoReg
	if name == "task.mapLimit" {
		limit = l.expr(c.Args[1])
	}

	if name == "task.each" {
		l.taskRun(xs, f, elem, vVoid, NoReg, false)
		return l.junk(), true
	}
	return l.taskRun(xs, f, elem, ft.fn.ret, limit, true), true
}

// taskRun is the whole of it: build a job per element, run them in
// batches, then read the answers back out in order.
//
// f at NoReg means task.all, where the function is the element itself
// and there is no argument to pass.
func (l *lowerer) taskRun(xs, f Reg, elem, ret vty, limit Reg, wantResults bool) Reg {
	perJob := int64(jobWords * wordSize)
	n := l.field(xs, listLenOff, vInt)

	// The jobs block is tagged all-pointers so a collection during the
	// run keeps the closures and any pointer arguments alive. Marking is
	// conservative, so an int argument sitting in one of these slots is
	// simply a word that does not match any object.
	jobs := l.allocObj(l.arith(OpMul, n, l.constant(perJob)), tagPtrs)
	handles := l.allocObj(l.arith(OpMul, n, l.constant(wordSize)), tagWords)

	fill := l.beginLoop(xs)
	{
		i := l.loopIndex(fill)
		base := l.arith(OpAdd, jobs, l.arith(OpMul, i, l.constant(perJob)))
		v := l.listGet(xs, i, elem)
		if f == NoReg {
			l.emit(Instr{Op: OpStoreMem, A: base, B: v, Imm: jobCloOff})
		} else {
			l.emit(Instr{Op: OpStoreMem, A: base, B: f, Imm: jobCloOff})
			l.emit(Instr{Op: OpStoreMem, A: base, B: v, Imm: jobArgOff})
		}
	}
	l.endLoop(fill)

	entry := l.taskEntry(elem, ret, f != NoReg)

	// batch is how many run at once. Without a limit that is all of
	// them, so the outer loop goes round once.
	batch := n
	if limit != NoReg {
		one := l.compare(OpLt, limit, l.constant(1))
		batch = l.pick(one, l.constant(1), limit, vInt)
	}

	start := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: start})

	outer := l.newLabel()
	outerDone := l.newLabel()
	l.mark(outer)
	from := l.load(start, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, from, n), Dst: NoReg, Imm: outerDone})

	to := l.arith(OpAdd, from, batch)
	to = l.pick(l.compare(OpGt, to, n), n, to, vInt)
	toSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: to, Dst: NoReg, Imm: toSlot})

	// One for the batch rather than one per thread: main waits for all
	// of them before it touches the count again. Atomic, since threads
	// from thread.spawn may be changing it at the same moment.
	if !l.gcOff && !lowerForDLL {
		l.gcSelf()
	}
	l.atomicAdd(l.rtSlot(gcTasksSlot), l.constant(1))
	l.taskSpawn(jobs, handles, from, l.load(toSlot, vInt), entry, perJob)
	l.taskWait(handles, from, l.load(toSlot, vInt))
	l.atomicAdd(l.rtSlot(gcTasksSlot), l.constant(-1))

	l.emit(Instr{Op: OpStore, A: l.load(toSlot, vInt), Dst: NoReg, Imm: start})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: outer})
	l.mark(outerDone)

	if !wantResults {
		return l.junk()
	}

	out := l.newList(vListOf(ret), 0)
	read := l.beginLoop(xs)
	{
		i := l.loopIndex(read)
		base := l.arith(OpAdd, jobs, l.arith(OpMul, i, l.constant(perJob)))
		l.listPush(out, l.field(base, jobResOff, ret))
	}
	l.endLoop(read)
	return out
}

// taskSpawn starts a thread for every job in [from, to).
func (l *lowerer) taskSpawn(jobs, handles, from, to Reg, entry string, perJob int64) {
	addr := l.newReg()
	l.regTy[addr] = vInt
	l.emit(Instr{Op: OpSymAddr, Dst: addr, A: NoReg, B: NoReg,
		Sym: fnSym(entry), Comment: entry})

	l.taskLoop(from, to, func(i Reg) {
		base := l.arith(OpAdd, jobs, l.arith(OpMul, i, l.constant(perJob)))
		zero := l.constant(0)
		h := l.ccall("CreateThread",
			[]Reg{zero, zero, addr, base, zero, zero},
			[]vty{vInt, vInt, vInt, vInt, vInt, vInt}, vInt, false, false)
		slot := l.arith(OpAdd, handles, l.arith(OpMul, i, l.constant(wordSize)))
		l.emit(Instr{Op: OpStoreMem, A: slot, B: h, Imm: 0})
	})
}

// taskWait joins every thread in [from, to).
//
// One at a time rather than WaitForMultipleObjects, which caps at 64
// handles. Waiting in order costs nothing: they are all already
// running, so the total is still however long the slowest one takes.
func (l *lowerer) taskWait(handles, from, to Reg) {
	l.taskLoop(from, to, func(i Reg) {
		slot := l.arith(OpAdd, handles, l.arith(OpMul, i, l.constant(wordSize)))
		h := l.field(slot, 0, vInt)
		hSlot := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: h, Dst: NoReg, Imm: hSlot})
		l.blockingCall("WaitForSingleObject", []Reg{h, l.constant(waitForever)},
			[]vty{vInt, vInt}, vInt, true)
		l.ccall("CloseHandle", []Reg{l.load(hSlot, vInt)}, []vty{vInt}, vInt, true, false)
	})
}

// taskLoop counts from one register to another, body per step.
func (l *lowerer) taskLoop(from, to Reg, body func(i Reg)) {
	iSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: from, Dst: NoReg, Imm: iSlot})
	toSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: to, Dst: NoReg, Imm: toSlot})

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)
	i := l.load(iSlot, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, i, l.load(toSlot, vInt)),
		Dst: NoReg, Imm: done})
	body(i)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(iSlot, vInt), l.constant(1)),
		Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(done)
}

// taskEntry is the function CreateThread starts, one per argument and
// return shape the program actually uses.
//
// It takes the job pointer in the one place Windows puts it and returns
// zero, so it is a LPTHREAD_START_ROUTINE without anything being said
// about it anywhere.
func (l *lowerer) taskEntry(elem, ret vty, hasArg bool) string {
	l.mod.Threaded = true
	name := "taskentry_" + taskShape(elem, ret, hasArg)
	return l.helperFunc(name, []vty{vInt}, vInt, func(args []Reg) {
		jobSlot := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: args[0], Dst: NoReg, Imm: jobSlot})
		if !l.gcOff && !lowerForDLL {
			l.gcSelf()
		}
		job := l.load(jobSlot, vInt)
		clo := l.field(job, jobCloOff, vInt)

		var callArgs []Reg
		var callTypes []vty
		if hasArg {
			callArgs = []Reg{l.field(job, jobArgOff, elem)}
			callTypes = []vty{elem}
		}
		r := l.callClosure(clo, callArgs, callTypes, ret)
		if r != NoReg {
			l.emit(Instr{Op: OpStoreMem, A: l.load(jobSlot, vInt), B: r, Imm: jobResOff})
		}
		if !l.gcOff && !lowerForDLL {
			l.gcThreadDone()
		}
		l.emit(Instr{Op: OpRet, A: l.constant(0), Dst: NoReg})
	})
}

// taskShape names a call shape in something that can be a symbol.
func taskShape(elem, ret vty, hasArg bool) string {
	s := "void"
	if hasArg {
		s = elem.String()
	}
	return sanitizeSym(s) + "_" + sanitizeSym(ret.String())
}

// fnSym is the symbol a Veyl function is emitted under. A generic
// instance's name, max<int> or Box<str>.get, holds characters no
// assembler takes in a symbol, so a name with any is spelled out in hex;
// every other name is kept as it is.
func fnSym(name string) string {
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.') {
			return "__vy_" + sanitizeSym(name)
		}
	}
	return "__vy_" + name
}

func sanitizeSym(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			// keep it one to one, so two different types cannot collide
			fmt.Fprintf(&b, "x%02x", int(r)&0xff)
		}
	}
	return b.String()
}
