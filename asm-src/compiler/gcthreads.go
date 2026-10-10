package main

// Collection while threads run.
//
// The collector finds roots by reading a stack word by word, and until
// this it read only its own, so nothing was collected while any other
// thread was alive. Now every thread that runs Veyl code is known, and a
// collection stops them all, reads every stack, and lets them go.
//
// Each thread has a record, four words of malloc'd memory reached
// through a thread-local slot:
//
//	next   the next record; the runtime word gcThreadsSlot is the first
//	state  0 running, 1 parked, 2 held by the collector, 3 finished
//	sp     where its stack was when it parked
//	high   the top of its stack
//
// A thread parks in two places. At a collection check, when one has been
// asked for: the collector sets the threshold the check compares against
// to -1, so the next check on every thread calls in, finds a stop under
// way and waits. And around any call that can block - a join, a lock, a
// sleep, a socket, a read - because a thread waiting there might be
// waiting for the collector's own thread, and would never reach a check.
//
// Both are places where every pointer the thread holds is in a stack
// slot, which is the property the single-threaded collector already
// rests on: the register allocator never keeps a pointer only in a
// register. So a parked thread's stack from its saved sp to its top
// holds all of its roots.
//
// The collector claims the stop under the allocation lock, takes each
// other thread from parked to held (a compare and swap, so a thread
// coming back from a blocking call cannot slip away mid-collection),
// marks from every held stack as well as its own, sweeps, and puts them
// all back to parked. Each then swaps itself back to running.
//
// A thread registers itself, under the same lock and only when no stop
// is under way, so a collection always knows every thread that could be
// running. thread.spawn waits for the new thread to register before it
// lets go of the closure, which until then only the spawner holds.

const (
	gcStopSlot    = 11 // nonzero while a collection has the world stopped
	gcThreadsSlot = 12 // the list of thread records
	gcTlsSlot     = 13 // thread-local index of the current record, plus one

	recNext  = 0
	recState = wordSize
	recSP    = 2 * wordSize
	recHigh  = 3 * wordSize
	recBytes = 4 * wordSize

	stRunning = 0
	stParked  = 1
	stHeld    = 2
	stDone    = 3
)

// yield gives the processor away for a moment, inside a wait loop.
func (l *lowerer) yield() {
	l.ccall("SwitchToThread", nil, nil, vInt, true, false)
}

// gcSelf is the current thread's record, made and registered the first
// time a thread asks.
func (l *lowerer) gcSelf() Reg {
	sym := l.helperFunc("__gc_self", nil, vInt, func([]Reg) {
		// The thread-local index, allocated once for the whole program.
		have := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, l.rtLoad(gcTlsSlot), l.constant(0)), Dst: NoReg, Imm: have})
		l.spinLock()
		again := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, l.rtLoad(gcTlsSlot), l.constant(0)), Dst: NoReg, Imm: again})
		idx := l.ccall("TlsAlloc", nil, nil, vInt, true, false)
		l.rtStore(gcTlsSlot, l.arith(OpAdd, idx, l.constant(1)))
		l.mark(again)
		l.spinUnlock()
		l.mark(have)

		index := l.arith(OpSub, l.rtLoad(gcTlsSlot), l.constant(1))
		idxSlot := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: index, Dst: NoReg, Imm: idxSlot})
		cur := l.ccall("TlsGetValue", []Reg{index}, []vty{vInt}, vInt, false, false)
		fresh := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, cur, l.constant(0)), Dst: NoReg, Imm: fresh})
		l.emit(Instr{Op: OpRet, A: cur, Dst: NoReg})

		l.mark(fresh)
		rec := l.ccall("calloc", []Reg{l.constant(1), l.constant(recBytes)}, []vty{vInt, vInt}, vInt, false, false)
		recSlot := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: rec, Dst: NoReg, Imm: recSlot})
		low := l.rawWord()
		lowSlot := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: low, Dst: NoReg, Imm: lowSlot})
		l.ccall("GetCurrentThreadStackLimits", []Reg{low, l.arith(OpAdd, l.load(recSlot, vInt), l.constant(recHigh))},
			[]vty{vInt, vInt}, vVoid, false, false)
		l.ccall("free", []Reg{l.load(lowSlot, vInt)}, []vty{vInt}, vVoid, false, false)

		// Onto the list, but never during a stop: the collector took its
		// view of the list when it began, and a thread it does not know
		// about must not be running Veyl code while it works.
		top := l.newLabel()
		in := l.newLabel()
		l.mark(top)
		l.spinLock()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.rtLoad(gcStopSlot), l.constant(0)), Dst: NoReg, Imm: in})
		l.spinUnlock()
		l.yield()
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
		l.mark(in)
		r := l.load(recSlot, vInt)
		l.pokeWord(r, recNext, l.rtLoad(gcThreadsSlot))
		l.rtStore(gcThreadsSlot, r)
		l.spinUnlock()

		l.ccall("TlsSetValue", []Reg{l.load(idxSlot, vInt), l.load(recSlot, vInt)}, []vty{vInt, vInt}, vInt, true, false)
		l.emit(Instr{Op: OpRet, A: l.load(recSlot, vInt), Dst: NoReg})
	})
	return l.callHelper(sym, nil, nil, vInt)
}

// gcResume takes a parked thread back to running, waiting while the
// collector holds it.
func (l *lowerer) gcResume(rec Reg) {
	sym := l.helperFunc("__gc_resume", []vty{vInt}, vVoid, func(a []Reg) {
		top := l.newLabel()
		l.mark(top)
		got := l.atomicCAS(l.arith(OpAdd, a[0], l.constant(recState)), l.constant(stParked), l.constant(stRunning))
		done := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: got, Dst: NoReg, Imm: done})
		l.yield()
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
		l.mark(done)
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
	l.callHelper(sym, []Reg{rec}, []vty{vInt}, vVoid)
}

// gcPark is a thread stopping for a collection another thread runs: it
// says where its stack is, waits for the collection to finish, and goes
// back to running.
func (l *lowerer) gcPark() {
	sym := l.helperFunc("__gc_park", nil, vVoid, func([]Reg) {
		rec := l.gcSelf()
		recSlot := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: rec, Dst: NoReg, Imm: recSlot})
		sp := l.newReg()
		l.regTy[sp] = vInt
		l.emit(Instr{Op: OpStackPtr, Dst: sp, A: NoReg, B: NoReg})
		l.pokeWord(rec, recSP, sp)
		l.atomicSwap(l.arith(OpAdd, rec, l.constant(recState)), l.constant(stParked))

		top := l.newLabel()
		done := l.newLabel()
		l.mark(top)
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.rtLoad(gcStopSlot), l.constant(0)), Dst: NoReg, Imm: done})
		l.yield()
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
		l.mark(done)
		l.gcResume(l.load(recSlot, vInt))
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
	l.callHelper(sym, nil, nil, vVoid)
}

// gcEnter is the way into a call that can block, and gcLeave the way
// out. With no other thread running neither does anything but compare
// a word; with some, the thread is parked for the length of the call,
// so a collection can go ahead without waiting for it.
func (l *lowerer) gcEnter() Reg {
	if l.gcOff || lowerForDLL {
		return NoReg
	}
	sym := l.helperFunc("__gc_enter", nil, vInt, func([]Reg) {
		alone := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.rtLoad(gcTasksSlot), l.constant(0)), Dst: NoReg, Imm: alone})
		rec := l.gcSelf()
		recSlot := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: rec, Dst: NoReg, Imm: recSlot})
		sp := l.newReg()
		l.regTy[sp] = vInt
		l.emit(Instr{Op: OpStackPtr, Dst: sp, A: NoReg, B: NoReg})
		l.pokeWord(rec, recSP, sp)
		l.atomicSwap(l.arith(OpAdd, rec, l.constant(recState)), l.constant(stParked))
		l.emit(Instr{Op: OpRet, A: l.load(recSlot, vInt), Dst: NoReg})
		l.mark(alone)
		l.emit(Instr{Op: OpRet, A: l.constant(0), Dst: NoReg})
	})
	rec := l.callHelper(sym, nil, nil, vInt)
	slot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: rec, Dst: NoReg, Imm: slot})
	return l.load(slot, vInt)
}

func (l *lowerer) gcLeave(rec Reg) {
	if rec == NoReg {
		return
	}
	skip := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, rec, l.constant(0)), Dst: NoReg, Imm: skip})
	l.gcResume(rec)
	l.mark(skip)
}

// blockingCall is ccall for a C function that can wait: on another
// thread, on the network, on the user, on the clock.
func (l *lowerer) blockingCall(sym string, args []Reg, argTypes []vty, ret vty, ret32 bool) Reg {
	rec := l.gcEnter()
	if rec == NoReg {
		return l.ccall(sym, args, argTypes, ret, ret32, false)
	}
	keep := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: rec, Dst: NoReg, Imm: keep})
	r := l.ccall(sym, args, argTypes, ret, ret32, false)
	if r == NoReg {
		l.gcLeave(l.load(keep, vInt))
		return r
	}
	saved := l.temp(ret)
	l.emit(Instr{Op: OpStore, A: r, Dst: NoReg, Imm: saved})
	l.gcLeave(l.load(keep, vInt))
	return l.load(saved, ret)
}

// gcThreadDone marks the current thread finished, as its last act.
func (l *lowerer) gcThreadDone() {
	rec := l.gcSelf()
	l.atomicSwap(l.arith(OpAdd, rec, l.constant(recState)), l.constant(stDone))
}

// stopWorld begins a collection with other threads running: claim the
// stop, make every check call in, and take each other thread from parked
// to held. stopped is set to one when this thread owns the stop. A
// thread that finds another already collecting parks for it, and its
// own collection returns without running.
func (l *lowerer) stopWorld(stopped int64) {
	me := l.gcSelf()
	meSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: me, Dst: NoReg, Imm: meSlot})

	l.spinLock()
	mine := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.rtLoad(gcStopSlot), l.constant(0)), Dst: NoReg, Imm: mine})
	l.spinUnlock()
	l.gcPark()
	l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})

	l.mark(mine)
	l.rtStore(gcStopSlot, l.constant(1))
	l.spinUnlock()
	l.emit(Instr{Op: OpStore, A: l.constant(1), Dst: NoReg, Imm: stopped})
	l.rtStore(gcNextSlot, l.constant(-1))

	l.eachRecord(func(r Reg) {
		rSlot := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: r, Dst: NoReg, Imm: rSlot})
		next := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, r, l.load(meSlot, vInt)), Dst: NoReg, Imm: next})
		top := l.newLabel()
		l.mark(top)
		st := l.peekWord(l.load(rSlot, vInt), recState)
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, st, l.constant(stDone)), Dst: NoReg, Imm: next})
		got := l.atomicCAS(l.arith(OpAdd, l.load(rSlot, vInt), l.constant(recState)), l.constant(stParked), l.constant(stHeld))
		l.emit(Instr{Op: OpJumpIf, A: got, Dst: NoReg, Imm: next})
		l.yield()
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
		l.mark(next)
	})
}

// startWorld ends it: every held thread back to parked, from where each
// swaps itself to running, and the records of finished threads freed -
// nothing can join the list while the stop is on.
func (l *lowerer) startWorld(stopped int64) {
	skip := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(stopped, vInt), l.constant(0)), Dst: NoReg, Imm: skip})
	l.eachRecord(func(r Reg) {
		not := l.newLabel()
		rSlot := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: r, Dst: NoReg, Imm: rSlot})
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, l.peekWord(r, recState), l.constant(stHeld)), Dst: NoReg, Imm: not})
		l.atomicSwap(l.arith(OpAdd, l.load(rSlot, vInt), l.constant(recState)), l.constant(stParked))
		l.mark(not)
	})
	l.unlinkFinished()
	l.setThreshold()
	l.atomicSwap(l.rtSlot(gcStopSlot), l.constant(0))
	l.mark(skip)
}

// eachRecord runs body for every thread record.
func (l *lowerer) eachRecord(body func(r Reg)) {
	p := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.rtLoad(gcThreadsSlot), Dst: NoReg, Imm: p})
	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)
	cur := l.load(p, vInt)
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, cur, l.constant(0)), Dst: NoReg, Imm: done})
	next := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.peekWord(cur, recNext), Dst: NoReg, Imm: next})
	body(cur)
	l.emit(Instr{Op: OpStore, A: l.load(next, vInt), Dst: NoReg, Imm: p})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(done)
}

// unlinkFinished frees the records of threads that have ended.
func (l *lowerer) unlinkFinished() {
	// at is the address of the link that points at the current record:
	// the list head first, then each record's next word.
	at := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.rtSlot(gcThreadsSlot), Dst: NoReg, Imm: at})
	top := l.newLabel()
	done := l.newLabel()
	keep := l.newLabel()
	l.mark(top)
	cur := l.peekWord(l.load(at, vInt), 0)
	curSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: cur, Dst: NoReg, Imm: curSlot})
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, cur, l.constant(0)), Dst: NoReg, Imm: done})
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, l.peekWord(cur, recState), l.constant(stDone)), Dst: NoReg, Imm: keep})
	l.pokeWord(l.load(at, vInt), 0, l.peekWord(l.load(curSlot, vInt), recNext))
	l.ccall("free", []Reg{l.load(curSlot, vInt)}, []vty{vInt}, vVoid, false, false)
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(keep)
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, l.load(curSlot, vInt), l.constant(recNext)), Dst: NoReg, Imm: at})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(done)
}

// scanThreadStacks marks from every held thread's stack, the roots a
// collection with the world stopped has besides its own.
func (l *lowerer) scanThreadStacks(table, mask, work, workLen Reg) {
	l.eachRecord(func(r Reg) {
		skip := l.newLabel()
		rSlot := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: r, Dst: NoReg, Imm: rSlot})
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, l.peekWord(r, recState), l.constant(stHeld)), Dst: NoReg, Imm: skip})
		rr := l.load(rSlot, vInt)
		l.scanRange(l.peekWord(rr, recSP), l.peekWord(l.load(rSlot, vInt), recHigh), table, mask, work, workLen)
		l.mark(skip)
	})
}
