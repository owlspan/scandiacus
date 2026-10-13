package main

// Threads.
//
//	let t = thread.spawn(fn() { work() })
//	thread.join(t)
//
// thread.spawn runs a function - a closure, with whatever it captured -
// on a new Windows thread and gives back its handle; thread.join waits
// for it to finish. Beside them: a mutex (a critical section), a
// condition variable, and atomic words, each operation one locked
// instruction. Channel<T>, in prelude_channel.go, is built on those in
// Veyl.
//
// Collecting while they run is gcthreads.go: every thread registers a
// record, and a collection stops them all and reads every stack. The
// count of threads running besides main, the runtime word task.map also
// uses, is what lets a program without threads skip all of that: while
// it is zero, allocation takes no lock and a blocking call parks nobody.
// A thread is counted from just before CreateThread to just after its
// function returns.

const (
	// A thread's job: the closure to run, and a word the thread sets
	// once it has taken the closure. An object, held by the spawner
	// until then; see thread.spawn.
	threadJobBytes = 2 * wordSize

	critSectionBytes   = 40 // CRITICAL_SECTION on x64
	condVarBytes       = 8  // CONDITION_VARIABLE: one pointer, zero to start
	allProcessorGroups = 0xFFFF
)

func (l *lowerer) atomicAdd(addr, delta Reg) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpAtomicAdd, Dst: d, A: addr, B: delta})
	return d
}

func (l *lowerer) atomicSwap(addr, v Reg) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpAtomicSwap, Dst: d, A: addr, B: v})
	return d
}

func (l *lowerer) atomicCAS(addr, old, new Reg) Reg {
	d := l.newReg()
	l.regTy[d] = vBool
	l.emit(Instr{Op: OpAtomicCAS, Dst: d, A: NoReg, B: NoReg, Args: []Reg{addr, old, new}})
	return d
}

// spinLock takes the allocation lock: swap a one in until a zero comes
// out.
func (l *lowerer) spinLock() {
	top := l.newLabel()
	l.mark(top)
	got := l.atomicCAS(l.rtSlot(gcLockSlot), l.constant(0), l.constant(1))
	l.emit(Instr{Op: OpJumpNot, A: got, Dst: NoReg, Imm: top})
}

func (l *lowerer) spinUnlock() {
	l.atomicSwap(l.rtSlot(gcLockSlot), l.constant(0))
}

// trackLocked is trackObject for when other threads are running.
func (l *lowerer) trackLocked(raw, bytes Reg) {
	sym := l.helperFunc("__track_locked", []vty{vInt, vInt}, vVoid, func(a []Reg) {
		l.spinLock()
		l.trackUnlocked(a[0], a[1])
		l.spinUnlock()
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
	l.callHelper(sym, []Reg{raw, bytes}, []vty{vInt, vInt}, vVoid)
}

func (l *lowerer) threadBuiltin(c *Call, name string) (Reg, bool) {
	arity := func(n int) bool {
		if len(c.Args) != n {
			l.errorAt(c, "%s takes %d argument(s), got %d", name, n, len(c.Args))
			return false
		}
		return true
	}
	zero := func() Reg { return l.constant(0) }

	switch name {
	case "thread.spawn":
		if !arity(1) {
			return l.junk(), true
		}
		f := l.expr(c.Args[0])
		if t := l.regTy[f]; t.k != kFunc || t.fn == nil || len(t.fn.params) != 0 {
			l.errorAt(c.Args[0], "thread.spawn runs a function that takes nothing, got %s", t)
			return l.junk(), true
		}
		// The job is an object, so while the spawner holds it the
		// closure inside cannot be collected. The spawner holds it until
		// the new thread has registered and taken the closure onto its
		// own stack, where a collection will find it.
		job := l.allocObj(l.constant(threadJobBytes), tagPtrs)
		l.emit(Instr{Op: OpStoreMem, A: job, B: f, Imm: 0})
		l.emit(Instr{Op: OpStoreMem, A: job, B: l.constant(0), Imm: wordSize})
		jobSlot := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: job, Dst: NoReg, Imm: jobSlot})
		entry := l.threadEntry()
		addr := l.newReg()
		l.regTy[addr] = vInt
		l.emit(Instr{Op: OpSymAddr, Dst: addr, A: NoReg, B: NoReg, Sym: fnSym(entry), Comment: entry})
		if !l.gcOff {
			l.gcSelf() // known before any thread exists that could stop it
		}
		l.atomicAdd(l.rtSlot(gcTasksSlot), l.constant(1))
		h := l.ccall("CreateThread", []Reg{zero(), zero(), addr, l.load(jobSlot, vInt), zero(), zero()},
			[]vty{vInt, vInt, vInt, vInt, vInt, vInt}, vInt, false, false)
		hSlot := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: h, Dst: NoReg, Imm: hSlot})

		rec := l.gcEnter()
		top, started := l.newLabel(), l.newLabel()
		l.mark(top)
		flag := l.loadWidth(l.arith(OpAdd, l.load(jobSlot, vInt), l.constant(wordSize)), memI64)
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpNe, flag, l.constant(0)), Dst: NoReg, Imm: started})
		l.yield()
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
		l.mark(started)
		l.gcLeave(rec)
		return l.load(hSlot, vInt), true

	case "thread.join":
		if !arity(1) {
			return l.junk(), true
		}
		h := l.intArg(c, 0)
		hSlot := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: h, Dst: NoReg, Imm: hSlot})
		l.blockingCall("WaitForSingleObject", []Reg{h, l.constant(waitForever)}, []vty{vInt, vInt}, vInt, true)
		l.ccall("CloseHandle", []Reg{l.load(hSlot, vInt)}, []vty{vInt}, vInt, true, false)
		return l.void(), true

	case "thread.id":
		if !arity(0) {
			return l.junk(), true
		}
		return l.ccall("GetCurrentThreadId", nil, nil, vInt, true, false), true

	case "thread.cores":
		if !arity(0) {
			return l.junk(), true
		}
		return l.ccall("GetActiveProcessorCount", []Reg{l.constant(allProcessorGroups)},
			[]vty{vInt}, vInt, true, false), true

	case "thread.mutex":
		if !arity(0) {
			return l.junk(), true
		}
		m := l.ccall("calloc", []Reg{l.constant(1), l.constant(critSectionBytes)},
			[]vty{vInt, vInt}, vInt, false, false)
		l.ccall("InitializeCriticalSection", []Reg{m}, []vty{vInt}, vVoid, false, false)
		return m, true

	case "thread.lock", "thread.unlock":
		if !arity(1) {
			return l.junk(), true
		}
		if name == "thread.unlock" {
			l.ccall("LeaveCriticalSection", []Reg{l.intArg(c, 0)}, []vty{vInt}, vVoid, false, false)
		} else {
			l.blockingCall("EnterCriticalSection", []Reg{l.intArg(c, 0)}, []vty{vInt}, vVoid, false)
		}
		return l.void(), true

	case "thread.cond":
		if !arity(0) {
			return l.junk(), true
		}
		return l.ccall("calloc", []Reg{l.constant(1), l.constant(condVarBytes)},
			[]vty{vInt, vInt}, vInt, false, false), true

	case "thread.wait":
		// Releases the mutex, sleeps until notified, and takes it back.
		if !arity(2) {
			return l.junk(), true
		}
		l.blockingCall("SleepConditionVariableCS", []Reg{l.intArg(c, 0), l.intArg(c, 1), l.constant(waitForever)},
			[]vty{vInt, vInt, vInt}, vInt, true)
		return l.void(), true

	case "thread.notify", "thread.notifyAll":
		if !arity(1) {
			return l.junk(), true
		}
		fn := "WakeConditionVariable"
		if name == "thread.notifyAll" {
			fn = "WakeAllConditionVariable"
		}
		l.ccall(fn, []Reg{l.intArg(c, 0)}, []vty{vInt}, vVoid, false, false)
		return l.void(), true

	case "atomic.new":
		if !arity(1) {
			return l.junk(), true
		}
		v := l.intArg(c, 0)
		p := l.ccall("calloc", []Reg{l.constant(1), l.constant(wordSize)}, []vty{vInt, vInt}, vInt, false, false)
		l.emit(Instr{Op: OpStoreMem, A: p, B: v, Imm: 0})
		return p, true

	case "atomic.add":
		// The new value, as InterlockedAdd gives.
		if !arity(2) {
			return l.junk(), true
		}
		p, n := l.intArg(c, 0), l.intArg(c, 1)
		return l.arith(OpAdd, l.atomicAdd(p, n), n), true

	case "atomic.get":
		if !arity(1) {
			return l.junk(), true
		}
		return l.loadWidth(l.intArg(c, 0), memI64), true

	case "atomic.set":
		if !arity(2) {
			return l.junk(), true
		}
		l.atomicSwap(l.intArg(c, 0), l.intArg(c, 1))
		return l.void(), true

	case "atomic.swap":
		if !arity(2) {
			return l.junk(), true
		}
		return l.atomicSwap(l.intArg(c, 0), l.intArg(c, 1)), true

	case "atomic.cas":
		if !arity(3) {
			return l.junk(), true
		}
		return l.atomicCAS(l.intArg(c, 0), l.intArg(c, 1), l.intArg(c, 2)), true
	}
	return NoReg, false
}

// threadEntry is what CreateThread starts: take the closure out of the
// job, free the job, run the closure, and stop being counted.
func (l *lowerer) threadEntry() string {
	l.mod.Threaded = true
	return l.helperFunc("threadentry", []vty{vInt}, vInt, func(args []Reg) {
		job := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: args[0], Dst: NoReg, Imm: job})
		if !l.gcOff {
			l.gcSelf()
		}
		clo := l.field(l.load(job, vInt), 0, vFuncOf(nil, vVoid))
		cloSlot := l.temp(vFuncOf(nil, vVoid))
		l.emit(Instr{Op: OpStore, A: clo, Dst: NoReg, Imm: cloSlot})
		// The closure is on this stack now; the spawner can let go.
		l.atomicSwap(l.arith(OpAdd, l.load(job, vInt), l.constant(wordSize)), l.constant(1))
		l.callClosure(l.load(cloSlot, vFuncOf(nil, vVoid)), nil, nil, vVoid)
		if !l.gcOff {
			l.gcThreadDone()
		}
		l.atomicAdd(l.rtSlot(gcTasksSlot), l.constant(-1))
		l.emit(Instr{Op: OpRet, A: l.constant(0), Dst: NoReg})
	})
}
