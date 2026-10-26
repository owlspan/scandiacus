package main

import "sort"

// Float literals read once before a loop.
//
// A float literal in an arithmetic or compare instruction is a memory
// operand, so `p.x += p.vx * 0.01` in a hot loop reads 0.01 from the
// constant pool on every trip. That is one more load port use per
// literal per trip in a loop whose own loads are already most of its
// work. This pass stores each distinct literal of a loop into a slot
// before the loop and has the loop read the slot, which promoteFloatSlots
// then keeps in an xmm register when the loop is busy enough to win one.
//
// Only loops with no call in them: a promoted float register is
// callee-saved, so every function that has one pays for saving and
// restoring it, and that is only a good trade where the loop runs many
// trips and there is nothing in it that would use the register anyway.
// The outermost such loop of a nest gets the literals, so an inner loop
// does not store them again on every trip of the loop around it.

func hoistFloatConsts(m *Module, fn *Func) {
	if len(fn.RegTypes) < fn.NRegs {
		return
	}
	spellNegations(m, fn)
	loops := findLoops(fn)
	sort.Slice(loops, func(i, j int) bool {
		a, b := loops[i], loops[j]
		if a.end-a.head != b.end-b.head {
			return a.end-a.head > b.end-b.head
		}
		return a.head < b.head
	})
	var done []loopSpan
	var plans []*licmPlan
next:
	for _, lp := range loops {
		for _, d := range done {
			if lp.head <= d.end && d.head <= lp.end {
				continue next
			}
		}
		if !leafLoop(fn, lp) {
			continue
		}
		if p := floatConstPlan(fn, lp); p != nil {
			plans = append(plans, p)
			done = append(done, lp)
		}
	}
	applyPlans(fn, plans)
}

// spellNegations writes a negation inside a call-free loop as the
// multiplication by -1.0 the emitter would have made of it anyway, so
// the -1.0 is a literal like the others and is read once before the
// loop with them. The result is the same bit for bit: x64.go already
// negates by that multiplication.
func spellNegations(m *Module, fn *Func) {
	var spots []int
	for _, lp := range findLoops(fn) {
		if !leafLoop(fn, lp) {
			continue
		}
		for p := lp.head; p <= lp.end; p++ {
			if fn.Code[p].Op == OpFNeg {
				spots = append(spots, p)
			}
		}
	}
	if len(spots) == 0 {
		return
	}
	sort.Ints(spots)
	imm := m.internFloat(-1)
	out := make([]Instr, 0, len(fn.Code)+len(spots))
	next := 0
	for p, in := range fn.Code {
		if next < len(spots) && spots[next] == p {
			next++
			for next < len(spots) && spots[next] == p {
				next++ // a position found through two nested loops
			}
			c := Reg(fn.NRegs)
			fn.NRegs++
			fn.RegTypes = append(fn.RegTypes, fn.RegTypes[in.Dst])
			out = append(out,
				Instr{Op: OpFConst, Dst: c, A: NoReg, B: NoReg, Imm: imm},
				Instr{Op: OpFMul, Dst: in.Dst, A: in.A, B: c, Comment: in.Comment})
			continue
		}
		out = append(out, in)
	}
	fn.Code = out
}

// leafLoop reports whether a loop calls nothing: its body is moves,
// arithmetic, loads, stores and jumps.
func leafLoop(fn *Func, lp loopSpan) bool {
	for p := lp.head; p <= lp.end; p++ {
		in := fn.Code[p]
		switch {
		case in.Op == OpStore, in.Op == OpStoreMem, in.Op == OpPoke,
			in.Op == OpStoreByte, in.Op == OpVStore,
			in.Op == OpVLoad, in.Op == OpVSplat, in.Op == OpVArith, in.Op == OpVZeroUpper,
			csePure(in.Op), in.Op == OpCheckIndex, in.Op == OpLoadMem,
			in.Op == OpLoad, in.Op == OpConst, in.Op == OpFConst, in.Op == OpStr,
			in.Op == OpLoadByte, in.Op == OpPeek, in.Op == OpSymAddr,
			in.Op == OpGlobalAddr, in.Op == OpLabel,
			in.Op == OpJump, in.Op == OpJumpIf, in.Op == OpJumpNot,
			in.Op == OpBoundsFail, in.Op == OpMustFail:
		default:
			return false
		}
	}
	return true
}

// maxHoistedFloats bounds the literals one loop takes slots for. Ten
// xmm registers hold promoted floats, and the loop's own float
// variables compete for them.
const maxHoistedFloats = 6

func floatConstPlan(fn *Func, lp loopSpan) *licmPlan {
	code := fn.Code
	type lit struct {
		imm int64
		ty  vty
	}
	var order []lit
	at := map[lit][]int{}
	for p := lp.head; p <= lp.end; p++ {
		in := code[p]
		if in.Op != OpFConst || in.Dst == NoReg || int(in.Dst) >= len(fn.RegTypes) {
			continue
		}
		k := lit{in.Imm, fn.RegTypes[in.Dst]}
		if _, seen := at[k]; !seen {
			order = append(order, k)
		}
		at[k] = append(at[k], p)
	}
	if len(order) == 0 {
		return nil
	}
	if len(order) > maxHoistedFloats {
		order = order[:maxHoistedFloats]
	}
	var pre []Instr
	plan := &licmPlan{at: lp.head, replace: map[int]Instr{}}
	for _, k := range order {
		first := code[at[k][0]]
		v := Reg(fn.NRegs)
		fn.NRegs++
		fn.RegTypes = append(fn.RegTypes, fn.RegTypes[first.Dst])
		slot := int64(fn.NSlots)
		fn.NSlots++
		c := first
		c.Dst, c.Comment = v, "hoisted"
		pre = append(pre, c,
			Instr{Op: OpStore, Dst: NoReg, A: v, B: NoReg, Imm: slot})
		for _, p := range at[k] {
			plan.replace[p] = Instr{Op: OpLoad, Dst: code[p].Dst, A: NoReg, B: NoReg,
				Imm: slot, Comment: code[p].Comment}
		}
	}
	plan.pre = pre
	return plan
}
