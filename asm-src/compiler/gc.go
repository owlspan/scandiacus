package main

import "os"

// The garbage collector.
//
// Mark and sweep, with conservative roots and precise tracing. That
// split is what this backend's shape makes possible, and it is worth
// saying why each half is the way it is.
//
// **Roots are conservative** because there is no stack map. A frame here
// is a block of slots and nothing records which of them hold pointers at
// a given instruction. So the stack is read as a flat array of words and
// any word that is the address of a known object is treated as a root.
// The cost is that an integer that happens to equal an object's address
// keeps it alive; the benefit is that no other part of the compiler has
// to be told about collection.
//
// What makes that sound here rather than hopeful: **every pointer lives
// in a stack slot.** The register allocator in regalloc.go only ever
// takes values that are not pointers, so there is no such thing as a
// pointer that exists only in a machine register and would be missed.
// That restriction is load-bearing: TestRegAllocKeepsPointersSlotted is
// there so it cannot be relaxed by accident.
//
// **Tracing is precise** because every object carries a header saying
// what it is: raw bytes, pointer-free words, all-pointer words, a list
// header, a map header, or a struct with a count of leading pointers.
// That header went in long before there was a collector, for exactly
// this moment.
//
// Nothing collects automatically. `mem.collect()` is the only thing that
// runs it. Automatic collection would need to be sure that no allocation
// site has a live pointer sitting in a register between the allocation
// and the store that parks it - which is true of every site written so
// far, but is a property nobody is currently checking, and a collector
// that frees one live object produces a bug hours away from its cause.

const (
	// Words of static storage the runtime keeps for itself, ahead of the
	// program's own globals.
	gcHeadSlot   = 0  // the object list
	gcLiveSlot   = 1  // objects allocated and not yet freed
	gcBytesSlot  = 2  // bytes in those objects, payload only
	gcTotalSlot  = 3  // bytes ever allocated
	gcCyclesSlot = 4  // how many times collect has run
	gcNGlobSlot  = 5  // how many words the globals block has, for the scan
	gcTasksSlot  = 6  // threads running besides main, during which nothing collects
	gcNextSlot   = 7  // live bytes at which the next automatic collection runs
	gcLockSlot   = 8  // the allocation lock, taken while other threads run
	gcWhereSlot  = 9  // where a runtime error happened, as a string; see where.go
	gcStackSlot  = 10 // the in/out word SetThreadStackGuarantee takes; see crash.go
	gcReserved   = 11
)

// rtSlot is the address of one of the runtime's own global words.
func (l *lowerer) rtSlot(i int64) Reg { return l.globalAddr(i) }

func (l *lowerer) rtLoad(i int64) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: l.rtSlot(i), B: NoReg, Imm: 0})
	return d
}

func (l *lowerer) rtStore(i int64, v Reg) {
	l.emit(Instr{Op: OpStoreMem, A: l.rtSlot(i), B: v, Imm: 0})
}

func (l *lowerer) rtBump(i int64, by Reg) {
	l.rtStore(i, l.arith(OpAdd, l.rtLoad(i), by))
}

// trackObject threads a fresh allocation onto the object list.
//
// The list is intrusive - the link lives in the object's own header - so
// tracking costs one store and no allocation of its own. An allocator
// that had to allocate to record an allocation would not terminate.
func (l *lowerer) trackObject(raw, obj, bytes Reg) {
	// With other threads running, under the allocation lock; see
	// threads.go. Alone, as it always was.
	alone := l.newLabel()
	done := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, l.rtLoad(gcTasksSlot), l.constant(0)), Dst: NoReg, Imm: alone})
	l.trackLocked(raw, bytes)
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
	l.mark(alone)
	l.trackUnlocked(raw, bytes)
	l.mark(done)
}

func (l *lowerer) trackUnlocked(raw, bytes Reg) {
	l.emit(Instr{Op: OpStoreMem, A: raw, B: l.rtLoad(gcHeadSlot), Imm: objNextOff})
	l.rtStore(gcHeadSlot, raw)

	l.rtBump(gcLiveSlot, l.constant(1))
	l.rtBump(gcBytesSlot, bytes)
	l.rtBump(gcTotalSlot, bytes)
}

// gcFirst is the live heap at which the first automatic collection runs.
// Small programs never reach it, and so never pay for one.
//
// VEYL_GC=eager makes it zero, and keeps it there, so the automatic path
// collects at nearly every statement - the test suite runs under it to
// hold that path to the same standard as stress mode.
var gcFirst = func() int64 {
	if os.Getenv("VEYL_GC") == "eager" {
		return 0
	}
	return 4 << 20
}()

// maybeCollect is the check every statement starts with: collect when
// the live heap has grown past the threshold. The common case is one
// compare and a branch not taken; the rest is in gcmaybe.
//
// A statement boundary is a safe place to collect. Every pointer the
// program holds is in a stack slot or a global there - the register
// allocator never takes one - and nothing is half-built that is not
// already reachable from one of those, which is also what stress mode
// relies on.
func (l *lowerer) maybeCollect() {
	name := l.helperFunc("gcmaybe", nil, vVoid, func([]Reg) {
		// Not while tasks run: the collector reads only the stack it
		// runs on, and a pointer on another thread's would be missed.
		// The threshold moves on either way, so this is not asked again
		// on every statement until the tasks finish.
		busy := l.newLabel()
		idle := l.compare(OpEq, l.rtLoad(gcTasksSlot), l.constant(0))
		l.emit(Instr{Op: OpJumpNot, A: idle, Dst: NoReg, Imm: busy})
		l.collect()
		l.mark(busy)
		twice := l.arith(OpMul, l.rtLoad(gcBytesSlot), l.constant(2))
		if gcFirst == 0 {
			twice = l.constant(0)
		}
		floor := l.constant(gcFirst)
		l.rtStore(gcNextSlot, l.pick(l.compare(OpGt, twice, floor), twice, floor, vInt))
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})
	})
	// One instruction here, expanded by the emitter into the compare and
	// the call, so the optimiser can see it whole and drop the ones no
	// allocation can have made necessary: see dropPolls in opt.go.
	l.emit(Instr{Op: OpGCPoll, Dst: NoReg, A: NoReg, B: NoReg, Sym: name})
}

// memBuiltin lowers the mem library.
func (l *lowerer) memBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "mem.used":
		return l.rtLoad(gcBytesSlot), true
	case "mem.total":
		return l.rtLoad(gcTotalSlot), true
	case "mem.objects":
		return l.rtLoad(gcLiveSlot), true
	case "mem.collections":
		return l.rtLoad(gcCyclesSlot), true
	case "mem.system":
		// Go reports what the runtime reserved from the operating
		// system, which is always more than what is in use. There is no
		// such number here - allocation goes straight to malloc - so
		// this reports what is live, which is the honest answer to "how
		// much memory is this program holding".
		return l.rtLoad(gcBytesSlot), true
	case "mem.goroutines":
		// The main thread, and one more for each task batch in flight.
		return l.arith(OpAdd, l.rtLoad(gcTasksSlot), l.constant(1)), true
	case "delete":
		return l.deleteValue(c), true
	case "mem.collect":
		if l.gcOff {
			l.errorAt(c, "this program says gc off, so nothing is collected - free a value with delete")
			return l.void(), true
		}
		l.collect()
		return l.void(), true
	}
	return NoReg, false
}

// stripCollector removes the collector from a program that can never
// need it. Every statement starts with a poll, and the poll pulls in
// gcmaybe, the collector and the marker - a few kilobytes that a
// program which never allocates carries for nothing, and hello, world
// was mostly that.
//
// A program needs collecting when something outside the crash handler
// makes an object the collector owns: __alloc, or one of the string
// helpers that allocate through __vy_talloc. The crash handler builds
// its message with concat too, but only as the program ends, when
// there is nothing left worth collecting. mem.collect, and stress mode,
// call the collector directly and keep it.
func (l *lowerer) stripCollector() {
	skip := map[string]bool{crashSym: true, "gcmaybe": true, collectSym: true, "__gc_try": true}
	for _, f := range l.mod.Funcs {
		if f.Name == "__alloc" {
			return
		}
		if skip[f.Name] {
			continue
		}
		for _, in := range f.Code {
			switch in.Op {
			case OpConcat, OpIntToStr, OpFloatToStr:
				return
			case OpCall:
				if in.Sym == collectSym {
					return
				}
			}
		}
	}
	gone := map[string]bool{"gcmaybe": true, collectSym: true, "__gc_try": true}
	kept := l.mod.Funcs[:0]
	for _, f := range l.mod.Funcs {
		if gone[f.Name] {
			continue
		}
		code := f.Code[:0]
		for _, in := range f.Code {
			if in.Op != OpGCPoll {
				code = append(code, in)
			}
		}
		f.Code = code
		kept = append(kept, f)
	}
	l.mod.Funcs = kept
}
