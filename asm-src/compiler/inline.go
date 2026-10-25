package main

// Inlining small functions.
//
// A call costs a prologue, the callee-saved registers its body uses
// saved and put back, the arguments moved into place and the result
// moved out, and every value the caller had in a scratch register sent
// to the frame around it. For `p.len()`, `abs(x)` or a two-line helper
// in a loop that is most of the work. So a small enough function is
// copied into each place it is called, before the other passes run, and
// they then treat the copy as part of the caller: its parameters are
// the caller's values, its loads forward, its loop-invariant words
// hoist.
//
// The copy is the callee's IR with its registers, slots and labels
// renumbered past the caller's. A parameter becomes a load of a slot
// the argument was stored in - forwarding turns that into the argument
// itself - and a return becomes a store of the result and a jump to the
// end of the copy, where the call's own result is loaded. Bodies are
// taken as they were before any inlining, and an inlined copy is not
// looked at again, so a recursive function is copied once at most and
// nothing grows without end.
//
// Left alone: a function that calls itself, a closure (it reads its
// environment from a register the call sets up), anything that reads
// the stack pointer, the runtime's own helpers, bodies longer than
// inlineMax instructions, and anything that can crash other than by a
// bounds check: a read or write of raw memory, a native call, a
// division by something not known to be safe. A crash is reported
// with the function it happened in, from the stack, and a copied body
// has no frame of its own to be found in. A bounds check carries its
// own location, so it is no reason to keep a call.

const inlineMax = 40

// inlineCalls copies small functions into their callers, module-wide.
func inlineCalls(m *Module) {
	byName := map[string]*Func{}
	for _, f := range m.Funcs {
		byName[f.Name] = f
	}
	bodies := map[string][]Instr{}
	for _, f := range m.Funcs {
		if inlinable(m, f) {
			bodies[f.Name] = append([]Instr(nil), f.Code...)
		}
	}
	if len(bodies) == 0 {
		return
	}
	for _, f := range m.Funcs {
		if len(f.RegTypes) < f.NRegs {
			continue
		}
		inlineInto(f, byName, bodies)
	}
}

func inlinable(m *Module, f *Func) bool {
	if f.Env || f.Name == "main" || len(f.RegTypes) < f.NRegs || len(f.ParamTypes) != f.NParams {
		return false
	}
	// The runtime's helpers: the collector, the allocator, the thread
	// bookkeeping. Small, but called from places that rely on them being
	// calls - a frame of their own, a stack pointer of their own.
	if len(f.Name) > 2 && f.Name[:2] == "__" && !(len(f.Name) > 5 && f.Name[:5] == "__vy_") {
		return false
	}
	switch f.Name {
	case collectSym, "gcmaybe", crashSym:
		return false
	}
	consts := map[Reg]int64{}
	elem := map[Reg]bool{}
	global := map[Reg]bool{} // a global's own word, which is always there
	for _, in := range f.Code {
		switch in.Op {
		case OpConst:
			consts[in.Dst] = in.Imm
		case OpIndexAddr:
			elem[in.Dst] = true
		case OpGlobalAddr:
			global[in.Dst] = true
		}
	}
	n := 0
	for _, in := range f.Code {
		switch in.Op {
		case OpLabel:
			continue
		case OpStackPtr, OpPeek, OpPoke, OpCallAddr, OpLoadByte, OpStoreByte:
			return false
		case OpCall:
			if in.Sym == f.Name || in.Extern {
				return false
			}
		case OpDiv, OpMod, OpDivU, OpModU:
			if d, ok := consts[in.B]; !ok || d == 0 || d == -1 {
				return false
			}
		case OpLoadMem, OpStoreMem:
			if classOf(m, f, elem, in.A) == rawMem && !global[in.A] {
				return false
			}
		}
		n++
	}
	return n <= inlineMax
}

func inlineInto(f *Func, byName map[string]*Func, bodies map[string][]Instr) {
	var out []Instr
	changed := false
	for _, in := range f.Code {
		body, ok := bodies[in.Sym]
		if in.Op != OpCall || in.Extern || !ok || in.Sym == f.Name || len(f.Code) > 20000 {
			out = append(out, in)
			continue
		}
		callee := byName[in.Sym]
		if callee == nil || len(in.Args) != callee.NParams {
			out = append(out, in)
			continue
		}
		changed = true

		regOff := Reg(f.NRegs)
		f.NRegs += callee.NRegs
		f.RegTypes = append(f.RegTypes, callee.RegTypes[:callee.NRegs]...)
		slotOff := int64(f.NSlots)
		f.NSlots += callee.NSlots
		for sl, r := range callee.Ranges {
			if f.Ranges == nil {
				f.Ranges = map[int64]span{}
			}
			f.Ranges[sl+slotOff] = r
		}
		labOff := int64(f.NLabels)
		f.NLabels += callee.NLabels + 1
		end := labOff + int64(callee.NLabels)
		if callee.MaxCallArgs > f.MaxCallArgs {
			f.MaxCallArgs = callee.MaxCallArgs
		}

		argSlot := make([]int64, len(in.Args))
		for i, a := range in.Args {
			argSlot[i] = int64(f.NSlots)
			f.NSlots++
			out = append(out, Instr{Op: OpStore, Dst: NoReg, A: a, B: NoReg, Imm: argSlot[i],
				Comment: callee.Name + " inlined"})
		}
		retSlot := int64(-1)
		if in.Dst != NoReg {
			retSlot = int64(f.NSlots)
			f.NSlots++
		}

		re := func(r Reg) Reg {
			if r < 0 {
				return r
			}
			return r + regOff
		}
		for _, c := range body {
			c.Dst, c.A, c.B = re(c.Dst), re(c.A), re(c.B)
			if c.Args != nil {
				args := make([]Reg, len(c.Args))
				for i, a := range c.Args {
					args[i] = re(a)
				}
				c.Args = args
			}
			switch c.Op {
			case OpParam:
				out = append(out, Instr{Op: OpLoad, Dst: c.Dst, A: NoReg, B: NoReg, Imm: argSlot[c.Imm]})
				continue
			case OpRet:
				if c.A != NoReg && retSlot >= 0 {
					out = append(out, Instr{Op: OpStore, Dst: NoReg, A: c.A, B: NoReg, Imm: retSlot})
				}
				out = append(out, Instr{Op: OpJump, Dst: NoReg, A: NoReg, B: NoReg, Imm: end})
				continue
			case OpLabel, OpJump, OpJumpIf, OpJumpNot:
				c.Imm += labOff
			case OpLoad, OpStore, OpSlotAddr, OpStoreByte:
				c.Imm += slotOff
			}
			out = append(out, c)
		}
		out = append(out, Instr{Op: OpLabel, Dst: NoReg, A: NoReg, B: NoReg, Imm: end})
		if in.Dst != NoReg {
			out = append(out, Instr{Op: OpLoad, Dst: in.Dst, A: NoReg, B: NoReg, Imm: retSlot})
		}
	}
	if changed {
		f.Code = out
	}
}
