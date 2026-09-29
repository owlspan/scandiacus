package main

// The register allocator.
//
// A handful of virtual registers get machine registers instead of frame
// slots, so that arithmetic between two barriers runs in registers
// rather than through memory. Which handful is decided by three rules,
// each of which exists because breaking it breaks something quiet:
//
//   - Only non-pointers. The collector finds roots by scanning stack
//     slots. A pointer parked in a register during an allocation is a
//     word the scan never sees, and the object behind it can be freed
//     while the program still holds it. Types come from the lowerer;
//     anything whose type says it holds a pointer keeps its slot.
//
//   - No interval crosses a barrier: a call, direct or through a
//     closure, or one of the helpers that allocates or can abort. The
//     calling convention lets the callee take every volatile register,
//     and every barrier can allocate, which is rule one all over again.
//     A value whose whole life fits between two barriers needs nothing
//     saved around them; one that lives across does not get a register
//     anywhere.
//
//   - One home per value, and no span crosses a label. The IR gives
//     every virtual register exactly one instruction that writes it,
//     which makes plain first-to-last spans tempting: unlike a frame
//     slot, a register here is written once, so no second write can
//     run ahead of a read. What single assignment does not stop is
//     re-entry: a value defined outside a loop and read inside it dies
//     textually at its last mention, but the back edge comes around
//     and reads it again after some other value has taken the
//     register. Every wrap re-enters through a label, so a span with
//     no label inside it cannot be re-entered either, and within one
//     straight run the linear span is exact. The one label allowed
//     inside is a join every jump to which starts within the span -
//     an if or a bounds check - since nothing can arrive there from
//     outside it; see spanClear.
//
// Floats are not pooled, though xmm4 and xmm5 sit idle for the taking.
// Reading a pooled float sometimes means getting its raw bits into a
// general register - storing it to a slot, passing it beyond the fourth
// argument position - and that move is movq, which the byte writer does
// not encode yet. An integer home works everywhere a slot works, which
// is the property that keeps this pass small.

import "sort"

var raIntRegs = [4]string{"r8", "r9", "r10", "r11"}

// raBarrier reports whether this op calls code the emitter does not see.
// Every one of these can allocate, and every one clobbers the volatile
// registers the pool draws from.
func raBarrier(in *Instr) bool {
	switch in.Op {
	case OpCall, OpCallClosure, OpCallAddr, OpGCPoll,
		OpConcat, OpStrEq, OpStrLen, OpIntToStr, OpFloatToStr,
		OpAlloc, OpFMod,
		OpPrintInt, OpPrintFloat, OpPrintStr, OpPrintBool,
		OpWriteStr, OpWriteInt, OpWriteFloat,
		OpBoundsFail, OpMustFail:
		return true
	}
	return false
}

// allocateRegs returns one home per pooled virtual register: a machine
// register for the few that qualify, absence for everything else. A nil
// map means the function was not suited to allocation and every read
// goes to its slot, as it would with the pass off.
func allocateRegs(f *Func) map[Reg]string {
	if len(f.RegTypes) < f.NRegs || f.NRegs == 0 {
		return nil
	}
	jumps := jumpsTo(f)

	// First and last mention of every register, and where the barriers
	// sit. Mentions outside the register range cannot exist, but a
	// defensive bound costs one comparison.
	defOf := make([]int, f.NRegs)
	lastOf := make([]int, f.NRegs)
	barrier := make([]bool, len(f.Code))
	for i := range defOf {
		defOf[i] = -1
	}
	note := func(r Reg, pos int) {
		if r >= 0 && int(r) < f.NRegs {
			if defOf[r] < 0 {
				defOf[r] = pos
			}
			lastOf[r] = pos
		}
	}
	for i := range f.Code {
		in := &f.Code[i]
		if raBarrier(in) {
			barrier[i] = true
		}
		note(in.Dst, i)
		note(in.A, i)
		note(in.B, i)
		for _, a := range in.Args {
			note(a, i)
		}
	}

	type cand struct {
		r     Reg
		def   int
		last  int
		param bool // arrives in a fixed argument register; not worth moving
	}
	var cands []cand
	for r := 0; r < f.NRegs; r++ {
		t := f.RegTypes[r]
		if t.holdsPointer() || t.k == kFloat {
			continue
		}
		def := defOf[r]
		if def < 0 {
			continue
		}
		// A result carried out of a call arrives after every volatile
		// register has been taken; its slot is written by callResult
		// directly and that is where readers should look.
		if barrier[def] {
			continue
		}
		last := lastOf[r]
		if !spanClear(f, def, last, jumps) {
			continue
		}
		cands = append(cands, cand{
			r: Reg(r), def: def, last: last,
			param: f.Code[def].Op == OpParam,
		})
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].def != cands[j].def {
			return cands[i].def < cands[j].def
		}
		return cands[i].r < cands[j].r
	})

	// Linear scan. A register whose holder dies before or at this
	// definition point is free again: both mentions sit in the same
	// instruction, the read happens before the write, and every shape
	// the emitter produces reads its inputs first.
	type held struct {
		until int
		home  string
	}
	var busy []held
	homes := make(map[Reg]string)
next:
	for _, c := range cands {
		if c.param {
			continue // parameters arrive in fixed registers; moving them buys nothing
		}
	free:
		for _, name := range raIntRegs[:] {
			for _, b := range busy {
				if b.home == name && b.until >= c.def {
					continue free
				}
			}
			homes[c.r] = name
			busy = append(busy, held{until: c.last, home: name})
			continue next
		}
		// Nothing free. The value keeps its slot; nothing already
		// holding a register is evicted over it.
	}
	if len(homes) == 0 {
		return nil
	}
	return homes
}

// raFloatRegs are the xmm registers float temporaries are pooled into.
// xmm0 and xmm1 are the emitter's scratch pair; xmm2 and xmm3 also carry
// call arguments, but a call is a barrier and no pooled value lives
// across one.
var raFloatRegs = [4]string{"xmm2", "xmm3", "xmm4", "xmm5"}

// floatPoolable reports whether an instruction may define (def) or read
// a float value that lives in an xmm register. Only the ones the
// selector emits with movsd and the SSE arithmetic are allowed: every
// other op moves its operands through general registers, and a float
// cannot reach one of those without movq.
func floatPoolable(op Op, def bool) bool {
	switch op {
	case OpFAdd, OpFSub, OpFMul, OpFDiv, OpFNeg:
		return true
	case OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe:
		return !def
	case OpFConst, OpLoad:
		return def
	case OpStore, OpRet:
		return !def
	}
	return false
}

// allocateFloatRegs is allocateRegs for float temporaries: same spans,
// same barrier and label rules, into xmm2-xmm5.
func allocateFloatRegs(f *Func) map[Reg]string {
	if len(f.RegTypes) < f.NRegs || f.NRegs == 0 {
		return nil
	}
	jumps := jumpsTo(f)
	defOf := make([]int, f.NRegs)
	lastOf := make([]int, f.NRegs)
	usable := make([]bool, f.NRegs)
	for i := range defOf {
		defOf[i] = -1
	}
	for r := 0; r < f.NRegs; r++ {
		t := f.RegTypes[r]
		usable[r] = t.k == kFloat && !t.null && !t.res
	}
	use := func(r Reg, pos int, op Op) {
		if r < 0 || int(r) >= f.NRegs {
			return
		}
		lastOf[r] = pos
		if !floatPoolable(op, false) {
			usable[r] = false
		}
	}
	for i := range f.Code {
		in := &f.Code[i]
		use(in.A, i, in.Op)
		use(in.B, i, in.Op)
		for _, a := range in.Args {
			use(a, i, OpCall) // never poolable
		}
		if d := in.Dst; d >= 0 && int(d) < f.NRegs && usable[d] && defOf[d] < 0 &&
			floatPoolable(in.Op, true) && in.Op != OpRet && in.Op != OpStore {
			defOf[d] = i
			if lastOf[d] < i {
				lastOf[d] = i
			}
		}
	}

	type cand struct {
		r         Reg
		def, last int
	}
	var cands []cand
	for r := 0; r < f.NRegs; r++ {
		def := defOf[r]
		if !usable[r] || def < 0 || lastOf[r] <= def {
			continue
		}
		if spanClear(f, def, lastOf[r], jumps) {
			cands = append(cands, cand{Reg(r), def, lastOf[r]})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].def < cands[j].def })

	homes := map[Reg]string{}
	until := map[string]int{}
next:
	for _, c := range cands {
		for _, name := range raFloatRegs {
			if u, busy := until[name]; busy && u >= c.def {
				continue
			}
			homes[c.r] = name
			until[name] = c.last
			continue next
		}
	}
	return homes
}

// noReturn reports whether an instruction ends the program. Nothing
// after it runs on that path, so what it clobbers does not matter.
func noReturn(in *Instr) bool { return in.Op == OpBoundsFail || in.Op == OpMustFail }

// jumpsTo lists, for each label, where the jumps to it are.
func jumpsTo(f *Func) map[int64][]int {
	out := map[int64][]int{}
	for i, in := range f.Code {
		switch in.Op {
		case OpJump, OpJumpIf, OpJumpNot:
			out[in.Imm] = append(out[in.Imm], i)
		}
	}
	return out
}

// spanClear reports whether a value defined at def and last read at
// last can stay in one register the whole way.
//
// A call on the way clobbers it, unless the call ends the program. A
// label on the way is a place control can arrive from elsewhere, with
// the register holding something else - a loop's back edge is the case
// that matters. But a label every jump to which comes from inside the
// span, ahead of the label, is only the join of an if or an else within
// it: the value was defined before any of those jumps and nothing has
// touched the register since, so arriving there changes nothing. A
// bounds check is exactly that shape, and used to push every value
// alive across one out to memory.
func spanClear(f *Func, def, last int, jumps map[int64][]int) bool {
	for p := def + 1; p < last; p++ {
		in := &f.Code[p]
		if raBarrier(in) && !noReturn(in) {
			return false
		}
		if in.Op == OpLabel {
			for _, from := range jumps[in.Imm] {
				if from <= def || from >= p {
					return false
				}
			}
		}
	}
	return true
}
