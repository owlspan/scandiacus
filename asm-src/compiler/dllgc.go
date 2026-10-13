package main

// Collection inside a DLL.
//
// A DLL's code runs on the host's threads, which the DLL did not start
// and only learns about when one calls an export. So every export is
// entered through a wrapper that makes the calling thread known to the
// collector the way a thread.spawn thread is (gcthreads.go): it gets a
// record, is running while it is inside, and parks on the way out with
// an empty stack - nothing of the DLL's is on that stack once control
// is back with the host. A collection on any thread then stops the
// others exactly as it does in a program.
//
// Three more things make it safe:
//
//   - The DLL counts itself as threaded from the moment it loads, so a
//     collection always takes the stop-the-world path and never assumes
//     it is alone.
//   - What an export returns - a str, an extern struct - the host may
//     still be reading after the call. It is kept alive in the thread's
//     record until that thread calls in again.
//   - A thread that ends gets a DLL_THREAD_DETACH; its record is marked
//     finished there, so no collection reads a stack that is gone.

// dllCallName is the wrapper an export's entry stub calls.
func dllCallName(name string) string { return "__dllcall_" + sanitizeSym(name) }

const (
	dllParkName = "__dll_park"
	dllGoneName = "__dll_gone"
)

// dllCollector builds the export wrappers and the two helpers the entry
// point calls. Only a DLL that collects needs them.
func (l *lowerer) dllCollector(p *Program) {
	if !lowerForDLL {
		return
	}
	collect := !l.gcOff
	for _, fd := range p.Funcs {
		if !fd.Export || fd.Recv != "" {
			continue
		}
		s, ok := l.sigs[fd.Name]
		if !ok {
			continue
		}
		name := fd.Name
		l.helperFunc(dllCallName(name), s.params, s.ret, func(a []Reg) {
			// The arguments are held in slots across the calls below.
			slots := make([]int64, len(a))
			for i, r := range a {
				slots[i] = l.temp(s.params[i])
				l.emit(Instr{Op: OpStore, A: r, Dst: NoReg, Imm: slots[i]})
			}
			recSlot := l.temp(vInt)
			if collect {
				rec := l.gcSelf()
				l.emit(Instr{Op: OpStore, A: rec, Dst: NoReg, Imm: recSlot})
				// A record made just now is running already; one that
				// parked on its way out of an earlier call comes back,
				// waiting while a collection holds it.
				in := l.newLabel()
				l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq,
					l.peekWord(l.load(recSlot, vInt), recState), l.constant(stRunning)), Dst: NoReg, Imm: in})
				l.gcResume(l.load(recSlot, vInt))
				l.mark(in)
			}
			args := make([]Reg, len(a))
			for i := range a {
				args[i] = l.load(slots[i], s.params[i])
			}
			r := l.callHelper(name, args, s.params, s.ret)
			var res int64 = -1
			if r != NoReg {
				res = l.temp(s.ret)
				l.emit(Instr{Op: OpStore, A: r, Dst: NoReg, Imm: res})
			}
			if collect {
				if r != NoReg && s.ret.k != kFloat {
					l.pokeWord(l.load(recSlot, vInt), recKeep, l.load(res, s.ret))
				}
				l.dllParkRecord(l.load(recSlot, vInt))
			}
			if r == NoReg {
				l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
			} else {
				l.emit(Instr{Op: OpRet, A: l.load(res, s.ret), Dst: NoReg})
			}
		})
	}

	// After the top-level code, on the loading thread.
	l.helperFunc(dllParkName, nil, vVoid, func([]Reg) {
		if collect {
			l.dllParkRecord(l.gcSelf())
		}
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})

	// DLL_THREAD_DETACH: the thread is ending. Its record, if it has one,
	// is finished; the collector frees it at the next stop.
	l.helperFunc(dllGoneName, nil, vVoid, func([]Reg) {
		if collect {
			done := l.newLabel()
			l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.rtLoad(gcTlsSlot), l.constant(0)), Dst: NoReg, Imm: done})
			rec := l.ccall("TlsGetValue", []Reg{l.arith(OpSub, l.rtLoad(gcTlsSlot), l.constant(1))},
				[]vty{vInt}, vInt, false, false)
			recSlot := l.temp(vInt)
			l.emit(Instr{Op: OpStore, A: rec, Dst: NoReg, Imm: recSlot})
			l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.load(recSlot, vInt), l.constant(0)), Dst: NoReg, Imm: done})
			l.atomicSwap(l.arith(OpAdd, l.load(recSlot, vInt), l.constant(recState)), l.constant(stDone))
			l.mark(done)
		}
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
}

// dllParkRecord parks a thread that is leaving the DLL's code: its
// stack from here up is the host's, so the range the collector reads
// is empty, and the state says a collection may go ahead without it.
func (l *lowerer) dllParkRecord(rec Reg) {
	recSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: rec, Dst: NoReg, Imm: recSlot})
	l.pokeWord(l.load(recSlot, vInt), recSP, l.peekWord(l.load(recSlot, vInt), recHigh))
	l.atomicSwap(l.arith(OpAdd, l.load(recSlot, vInt), l.constant(recState)), l.constant(stParked))
}
