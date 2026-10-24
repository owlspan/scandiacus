package main

// The register allocator.
//
// A handful of virtual registers get machine registers instead of frame
// slots, so that arithmetic between two barriers runs in registers
// rather than through memory. Which handful is decided by three rules,
// each of which exists because breaking it breaks something quiet:
//
//   - No interval crosses a barrier: a call, direct or through a
//     closure, or one of the helpers that allocates or can abort. The
//     calling convention lets the callee take every volatile register,
//     and every barrier can allocate. A value whose whole life fits
//     between two barriers needs nothing saved around them; one that
//     lives across does not get a register anywhere.
//
//     That is also what lets a pointer have a register. The collector
//     finds roots by scanning stack slots, and a pointer only in a
//     register is a word the scan never sees - but a collection only
//     ever starts inside a barrier, this thread's or, for another
//     thread's, at a check or a blocking call this thread is parked
//     in, which are barriers too. A pointer whose life holds no
//     barrier is never in a register while a collection runs.
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

import (
	"sort"
	"strings"
)

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
	return allocateRegsIn(f, raIntRegs[:])
}

// allocateRegsIn is allocateRegs with a pool of its own: the volatile
// four, and whichever callee-saved registers the function has not
// promoted a slot into, which the prologue then saves.
func allocateRegsIn(f *Func, pool []string) map[Reg]string {
	if len(f.RegTypes) < f.NRegs || f.NRegs == 0 {
		return nil
	}
	jumps := jumpsTo(f)
	labelAt := labelPositions(f)

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
		// saved: the value lives across a call, so only a callee-saved
		// register will do; see crossesCalls.
		saved bool
	}
	var cands []cand
	for r := 0; r < f.NRegs; r++ {
		if f.RegTypes[r].k == kFloat {
			continue
		}
		def := defOf[r]
		if def < 0 {
			continue
		}
		last := lastOf[r]
		// A result carried out of a call arrives after every volatile
		// register has been taken; its slot is written by callResult
		// directly and that is where readers should look.
		if barrier[def] || !spanClear(f, Reg(r), def, last, jumps, labelAt) {
			if crossesCalls(f, Reg(r), def, last, jumps) {
				// With no call after it, a volatile register serves.
				saved := !spanClear(f, Reg(r), def, last, jumps, labelAt)
				cands = append(cands, cand{r: Reg(r), def: def, last: last, saved: saved})
			}
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
	//
	// Only the last holder of each register can still be holding it:
	// candidates come in order of definition, so any earlier holder of
	// a register given out since had died before that. One "busy until"
	// per register is the whole state - a list of every holder made
	// this quadratic, and a long list literal took seconds to compile.
	until := make(map[string]int, len(pool))
	homes := make(map[Reg]string)
	for _, c := range cands {
		if c.param {
			continue // parameters arrive in fixed registers; moving them buys nothing
		}
		if h, ok := reuseA(f, c.def, homes, until); ok {
			homes[c.r] = h
			until[h] = c.last
			continue
		}
		for _, name := range pool {
			if c.saved && volatileReg(name) {
				continue
			}
			if u, taken := until[name]; taken && u >= c.def {
				continue
			}
			homes[c.r] = name
			until[name] = c.last
			break
		}
		// Nothing free: the value keeps its slot; nothing already
		// holding a register is evicted over it.
	}
	if len(homes) == 0 {
		return nil
	}
	return homes
}

// reuseA gives the value defined at def its first operand's register
// when that operand dies right there: x86 arithmetic is two-address,
// so a result in its own register starts with a copy of A that this
// saves. Only for the operations whose emitters read A before writing
// the result.
func reuseA(f *Func, def int, homes map[Reg]string, until map[string]int) (string, bool) {
	in := f.Code[def]
	switch in.Op {
	case OpAdd, OpSub, OpMul, OpBAnd, OpBOr, OpBXor, OpShl, OpShr, OpNeg, OpBNot,
		OpFAdd, OpFSub, OpFMul, OpFDiv, OpLoadMem:
	default:
		return "", false
	}
	h, ok := homes[in.A]
	if !ok || until[h] != def {
		return "", false
	}
	return h, true
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
	case OpFAdd, OpFSub, OpFMul, OpFDiv, OpFNeg, OpF32Round:
		return true
	case OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe:
		return !def
	case OpFConst, OpLoad, OpLoadMem:
		return def
	case OpStore, OpRet, OpStoreMem:
		return !def
	}
	return false
}

// allocateFloatRegs is allocateRegs for float temporaries: same spans,
// same barrier and label rules, into xmm2-xmm5.
func allocateFloatRegs(f *Func) map[Reg]string {
	return allocateFloatRegsIn(f, raFloatRegs[:])
}

func allocateFloatRegsIn(f *Func, pool []string) map[Reg]string {
	if len(f.RegTypes) < f.NRegs || f.NRegs == 0 {
		return nil
	}
	jumps := jumpsTo(f)
	labelAt := labelPositions(f)
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
		if spanClear(f, Reg(r), def, lastOf[r], jumps, labelAt) {
			cands = append(cands, cand{Reg(r), def, lastOf[r]})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].def < cands[j].def })

	homes := map[Reg]string{}
	until := map[string]int{}
next:
	for _, c := range cands {
		if h, ok := reuseA(f, c.def, homes, until); ok {
			homes[c.r] = h
			until[h] = c.last
			continue
		}
		for _, name := range pool {
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
// crossesCalls reports whether a value can keep a callee-saved register
// across the calls in its span - a call preserves those, so only the
// labels matter. Only the result of a call to a program's own function
// declared to return an int or a bool: the collector does not look in
// registers, and a thread parked for another thread's collection - in
// __gc_park, or in a blocking call - leaves the callee-saved ones
// wherever the code it is in keeps them, so a value that might secretly
// be the only reference to an object stays on the stack. The runtime's
// own helpers are left out for exactly that: __alloc returns a new
// object's address as an int, and kept in rbx across the next
// allocation it was freed by a collection on another thread.
// fib(n - 1) + fib(n - 2) is the shape this is for: the first result
// waits out the second call in rbx.
func crossesCalls(f *Func, r Reg, def, last int, jumps map[int64][]int) bool {
	in := f.Code[def]
	if in.Op != OpCall || in.Extern || in.Dst != r || last <= def || strings.HasPrefix(in.Sym, "__") {
		return false
	}
	if in.RetType.k != kInt && in.RetType.k != kBool || in.RetType.null || in.RetType.res {
		return false
	}
	for p := def + 1; p < last; p++ {
		q := &f.Code[p]
		if q.Op == OpLabel {
			for _, from := range jumps[q.Imm] {
				if from <= def || from >= p {
					return false
				}
			}
		}
	}
	return true
}

func volatileReg(name string) bool {
	for _, v := range raIntRegs {
		if v == name {
			return true
		}
	}
	return false
}

func noReturn(in *Instr) bool { return in.Op == OpBoundsFail || in.Op == OpMustFail }

// deadAfter reports whether the code after position p goes straight to
// a jump beyond last, the value's last read, without reading r.
func deadAfter(f *Func, r Reg, p, last int, labelAt map[int64]int) bool {
	for q := p + 1; q < last; q++ {
		in := &f.Code[q]
		for _, x := range instrRegs(*in) {
			if x == r {
				return false
			}
		}
		switch in.Op {
		case OpJump:
			at, ok := labelAt[in.Imm]
			return ok && at > last
		case OpLabel, OpJumpIf, OpJumpNot, OpRet:
			return false
		}
	}
	return false
}

func labelPositions(f *Func) map[int64]int {
	out := map[int64]int{}
	for i, in := range f.Code {
		if in.Op == OpLabel {
			out[in.Imm] = i
		}
	}
	return out
}

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
//
// A call after which the value is dead is no trouble either: one from
// which the code runs to a jump past the value's last read, reading
// nothing of it on the way. The slow path of a push is that shape - the
// call that grows the list, then a jump over the fast path - and taking
// it for a clobber kept the list and the value in memory on every push.
func spanClear(f *Func, r Reg, def, last int, jumps map[int64][]int, labelAt map[int64]int) bool {
	for p := def + 1; p < last; p++ {
		in := &f.Code[p]
		if raBarrier(in) && !noReturn(in) && !deadAfter(f, r, p, last, labelAt) {
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
