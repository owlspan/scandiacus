package main

// Closures as callbacks.
//
// A function declared with fn is handed to native code as the address of
// a stub that jumps to it; see callback.go. A closure cannot be: its
// code needs its environment in r10, and native code calls a bare
// address with nothing else. So a closure takes one of ccbSlots slots.
// Slot k holds the closure, where the collector sees it among the
// runtime's words and keeps it alive, and the address of the adapter
// for its signature, which widens the arguments the way a stub for a
// named function does. Native code is given stub k, one of a fixed row
// of identical 16-byte stubs written into the program:
//
//	mov r11, [slot k]           the closure
//	mov rax, [adapter k]
//	jmp rax
//
// and the adapter ends in __vy_ccbgo, which puts the environment in r10
// and jumps to the closure's code. Nothing is written into executable
// memory at run time, which antivirus scanners treat as suspicious.
//
// A closure passed straight to an extern function holds its slot for
// that call only: it is freed when the call returns, which is right for
// every function that calls back before returning (qsort, EnumWindows).
// mem.callback(closure) holds it until mem.freeCallback(address), for
// native code that keeps the pointer; the checker warns when one is
// never freed. A freed slot's stub returns 0 rather than jumping to a
// closure that may be gone.

import (
	"fmt"

	front "scandiacusfront"
)

// Kept here rather than in the table in library.go, whose alignment the
// longer name would shift on every line.
func init() {
	sigs["mem.freeCallback"] = front.Signature{Params: []*Type{Int}, Ret: Void}
}

// ccbSlots is how many closures can be callbacks at once.
const ccbSlots = 64

// ccbStubBytes is the size of each stub, so stub k is at the first plus
// k times this: two rip-relative loads of seven bytes and a jmp rax.
const ccbStubBytes = 16

// isClosureCallback reports whether a callback argument is something
// other than a function declared with fn and named directly.
func (l *lowerer) isClosureCallback(e Expr) bool {
	if id, ok := e.(*Ident); ok {
		if _, local := l.lookup(id.Name); !local {
			if _, isFn := l.sigs[id.Name]; isFn {
				return false
			}
		}
	}
	return true
}

// closureCallback puts the closure e in a free slot and returns the
// address of its stub, and a slot holding the slot's number for
// releaseCallback.
func (l *lowerer) closureCallback(e Expr, want vty, widen []widenArg, adapt bool) (Reg, int64) {
	clo := l.rvalue(e)
	ct := l.regTy[clo]
	if ct.k != kFunc || ct.fn == nil {
		l.errorAt(e, "a callback has to be a function, got %s", ct)
		return l.junk(), -1
	}
	sym := "__vy_ccb_" + thunkShape(widen)
	kinds := ""
	for _, p := range ct.fn.params {
		if p.k == kFloat || fixedOf(p) == "f32" {
			kinds += "f"
		} else {
			kinds += "i"
		}
	}
	if adapt {
		sym += "_sv" + kinds
	}
	known := false
	for _, t := range l.mod.Thunks {
		if t.Sym == sym {
			known = true
			break
		}
	}
	if !known {
		l.mod.Thunks = append(l.mod.Thunks, Thunk{Sym: sym, Target: "__vy_ccbgo", Widen: widen,
			Kinds: kinds, Adapt: adapt})
	}
	l.mod.needs("ccb")
	l.mod.needs("must")

	adapter := l.newReg()
	l.regTy[adapter] = vInt
	l.emit(Instr{Op: OpSymAddr, Dst: adapter, A: NoReg, B: NoReg, Sym: sym})
	take := l.helperFunc("__ccbtake", []vty{vInt, vInt}, vInt, func(a []Reg) {
		cs, as := l.temp(vInt), l.temp(vInt)
		l.setSlot(cs, a[0])
		l.setSlot(as, a[1])
		k := l.scalarTemp(vInt)
		l.setSlot(k, l.constant(0))
		top, full := l.newLabel(), l.newLabel()
		l.mark(top)
		l.jumpNot(l.compare(OpLt, l.load(k, vInt), l.constant(ccbSlots)), full)
		at := l.arith(OpAdd, l.rtSlot(ccbSlot), l.arith(OpShl, l.load(k, vInt), l.constant(3)))
		next := l.newLabel()
		l.jumpNot(l.atomicCAS(at, l.constant(0), l.load(cs, vInt)), next)
		l.pokeWord(l.arith(OpAdd, l.rtSlot(ccbSlot+ccbSlots), l.arith(OpShl, l.load(k, vInt), l.constant(3))),
			0, l.load(as, vInt))
		l.ret(l.load(k, vInt))
		l.mark(next)
		l.setSlot(k, l.arith(OpAdd, l.load(k, vInt), l.constant(1)))
		l.jump(top)
		l.mark(full)
		l.emit(Instr{Op: OpMustFail, A: l.strLit(fmt.Sprintf(
			"more than %d closures are callbacks at once - free some with mem.freeCallback", ccbSlots)),
			Dst: NoReg, B: NoReg, Imm: l.where()})
		l.ret(l.constant(0))
	})
	k := l.scalarTemp(vInt)
	l.setSlot(k, l.callHelper(take, []Reg{clo, adapter}, []vty{vInt, vInt}, vInt))
	stubs := l.newReg()
	l.regTy[stubs] = vInt
	l.emit(Instr{Op: OpSymAddr, Dst: stubs, A: NoReg, B: NoReg, Sym: "__vy_ccbstubs"})
	d := l.arith(OpAdd, stubs, l.arith(OpMul, l.load(k, vInt), l.constant(ccbStubBytes)))
	l.regTy[d] = want
	return d, k
}

// releaseCallback frees the slot whose number is in the slot k: its
// stub returns 0 from now on, and the closure is the collector's again.
func (l *lowerer) releaseCallback(k Reg) {
	free := l.helperFunc("__ccbfree", []vty{vInt}, vInt, func(a []Reg) {
		ks := l.temp(vInt)
		l.setSlot(ks, a[0])
		dead := l.newReg()
		l.regTy[dead] = vInt
		l.emit(Instr{Op: OpSymAddr, Dst: dead, A: NoReg, B: NoReg, Sym: "__vy_ccbdead"})
		off := l.arith(OpShl, l.load(ks, vInt), l.constant(3))
		l.pokeWord(l.arith(OpAdd, l.rtSlot(ccbSlot+ccbSlots), off), 0, dead)
		l.pokeWord(l.arith(OpAdd, l.rtSlot(ccbSlot), l.arith(OpShl, l.load(ks, vInt), l.constant(3))),
			0, l.constant(0))
		l.ret(l.constant(0))
	})
	l.callHelper(free, []Reg{k}, []vty{vInt}, vInt)
}

// freeCallback is mem.freeCallback(address): the slot of a stub
// address, released. An address that is not one of the stubs, or one
// already freed, is left alone.
func (l *lowerer) freeCallback(addr Reg) {
	l.mod.needs("ccb")
	stubs := l.newReg()
	l.regTy[stubs] = vInt
	l.emit(Instr{Op: OpSymAddr, Dst: stubs, A: NoReg, B: NoReg, Sym: "__vy_ccbstubs"})
	off := l.scalarTemp(vInt)
	l.setSlot(off, l.arith(OpSub, addr, stubs))
	skip := l.newLabel()
	// Unsigned, so an address below the stubs is out of range too.
	l.jumpNot(l.compare(OpLtU, l.load(off, vInt), l.constant(ccbSlots*ccbStubBytes)), skip)
	l.releaseCallback(l.arith(OpShr, l.load(off, vInt), l.constant(4)))
	l.mark(skip)
}

// ccbRoutines is the row of stubs and the two routines they end in.
func ccbRoutines() string {
	s := "\n__vy_ccbstubs:\n"
	for k := 0; k < ccbSlots; k++ {
		s += fmt.Sprintf("    mov r11, qword ptr __globals[rip+%d]\n", (ccbSlot+k)*wordSize)
		s += fmt.Sprintf("    mov rax, qword ptr __globals[rip+%d]\n", (ccbSlot+ccbSlots+k)*wordSize)
		s += "    jmp rax\n"
	}
	s += fmt.Sprintf(`__vy_ccbgo:
    test r11, r11
    je __vy_ccbdead
    mov r10, qword ptr [r11+%d]
    mov r11, qword ptr [r11+%d]
    jmp r11
__vy_ccbdead:
    xor eax, eax
    ret
`, cloEnvOff, cloCodeOff)
	return s
}
