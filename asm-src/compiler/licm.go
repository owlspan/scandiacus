package main

import "sort"

// Loop-invariant loads.
//
// `for j in 0..n { c[j] += x * b[j] }` reads c's and b's length and
// data pointer from their headers on every trip, though nothing in the
// loop can change either: the loop writes elements, and an element
// buffer is a different allocation from the header that points to it.
// This pass reads each such word once, before the loop, into a slot of
// its own, and the loop reads the slot instead - which promote.go then
// usually turns into a register.
//
// A loop here is a label and the last jump back to it. It qualifies
// when control can only enter it at the top, by falling into the
// label: every jump to the label, and to any label inside, comes from
// inside. Code placed just before the label then runs once each time
// the loop is entered, and is the preheader.
//
// A load is invariant when its base is - a register defined before the
// loop, or a load of a local the loop never assigns - and nothing in
// the loop could write the word, by the same reasoning as cse.go: a
// call writes anything, a store writes the words of its own kind of
// object, and a store through a bare address writes anything. Reading
// the word early is safe even when the loop runs no trips: the base is
// a list or a struct, never nil, so the word is there to be read.

// licm hoists what it can, loop by loop, innermost first; a word
// hoisted out of an inner loop lands in the outer one and may move
// again from there.
func licm(m *Module, fn *Func) {
	if len(fn.RegTypes) < fn.NRegs {
		return
	}
	escaped := map[int64]bool{}
	for _, in := range fn.Code {
		if in.Op == OpSlotAddr {
			escaped[in.Imm] = true
		}
	}
	if fn.Env {
		escaped[0] = true
	}
	// Loops go in batches: every loop still to do that has no other
	// such loop inside it. Those cannot overlap, so their moves are all
	// planned against the same code and made in one rebuild, and the
	// loops around them come in a later batch and see what was moved
	// out of their bodies - an inner loop's loads can climb out of the
	// outer loop too. Planning one loop and rebuilding the function
	// before the next was quadratic in the loops of a function.
	tried := map[int64]bool{}
	triedPure := map[int64]bool{}
	for round := 0; round < 256; round++ {
		loops := findLoops(fn)
		var pending []loopSpan
		for _, lp := range loops {
			if !tried[lp.label] || !triedPure[lp.label] {
				pending = append(pending, lp)
			}
		}
		var batch []loopSpan
		for _, a := range pending {
			leaf := true
			for _, b := range pending {
				if b.label != a.label && b.head > a.head && b.end < a.end {
					leaf = false
					break
				}
			}
			if leaf {
				batch = append(batch, a)
			}
		}
		if len(batch) == 0 {
			return
		}
		defAt, elem := defsOf(fn)
		var plans []*licmPlan
		for _, lp := range batch {
			if !tried[lp.label] {
				tried[lp.label] = true
				if p := hoistLoop(m, fn, lp, escaped, defAt, elem); p != nil {
					plans = append(plans, p)
					continue // its arithmetic in the next round, against the new code
				}
			}
			triedPure[lp.label] = true
			if p := hoistPure(fn, lp, escaped, defAt); p != nil {
				plans = append(plans, p)
			}
		}
		applyPlans(fn, plans)
	}
}

// licmPlan is one loop's moves: instructions to put in front of its
// head, and instructions of its body to replace.
type licmPlan struct {
	at      int
	pre     []Instr
	replace map[int]Instr
}

// applyPlans makes the moves of loops that do not overlap, in one pass.
func applyPlans(fn *Func, plans []*licmPlan) {
	if len(plans) == 0 {
		return
	}
	before := map[int][]Instr{}
	replace := map[int]Instr{}
	extra := 0
	for _, p := range plans {
		before[p.at] = append(before[p.at], p.pre...)
		extra += len(p.pre)
		for at, in := range p.replace {
			replace[at] = in
		}
	}
	out := make([]Instr, 0, len(fn.Code)+extra)
	for i, in := range fn.Code {
		out = append(out, before[i]...)
		if r, ok := replace[i]; ok {
			in = r
		}
		out = append(out, in)
	}
	fn.Code = out
}

// defsOf maps each register to the instruction that writes it, and
// lists the registers that hold an element's address.
func defsOf(fn *Func) (map[Reg]int, map[Reg]bool) {
	defAt := make(map[Reg]int, fn.NRegs)
	elem := map[Reg]bool{}
	for i, in := range fn.Code {
		if in.Dst != NoReg {
			if _, seen := defAt[in.Dst]; !seen {
				defAt[in.Dst] = i
			}
		}
		// An address inside an inline struct element is a struct
		// pointer like any other, and takes its type's class, so that a
		// store through it and a load through the loop variable or self
		// are known to meet.
		if in.Op == OpIndexAddr && !structAddr(fn, in.Dst) {
			elem[in.Dst] = true
		}
	}
	return defAt, elem
}

type loopSpan struct {
	label     int64
	head, end int // the label's position and the last jump back to it
}

// findLoops lists the loops that can only be entered at the top,
// smallest first.
func findLoops(fn *Func) []loopSpan {
	at := map[int64]int{}
	for i, in := range fn.Code {
		if in.Op == OpLabel {
			at[in.Imm] = i
		}
	}
	jumps := jumpsTo(fn)
	var out []loopSpan
	for label, h := range at {
		end := -1
		for _, from := range jumps[label] {
			if from > h && from > end {
				end = from
			}
		}
		if end < 0 || h == 0 {
			continue
		}
		switch fn.Code[h-1].Op {
		case OpJump, OpRet, OpBoundsFail, OpMustFail:
			continue // nothing falls into the label
		}
		ok := true
		for p := h; p <= end && ok; p++ {
			if fn.Code[p].Op != OpLabel {
				continue
			}
			for _, from := range jumps[fn.Code[p].Imm] {
				if from < h || from > end || (p == h && from == h) {
					ok = false
					break
				}
			}
		}
		if ok {
			out = append(out, loopSpan{label, h, end})
		}
	}
	// Smallest first, so inner loops go before the loops around them;
	// ties by position, so the order does not depend on map iteration.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.end-a.head != b.end-b.head {
			return a.end-a.head < b.end-b.head
		}
		return a.head < b.head
	})
	return out
}

type hoistKey struct {
	slot int64 // the local the base is loaded from, or -1
	base Reg   // the base itself when it is defined before the loop
	off  int64
}

func hoistLoop(m *Module, fn *Func, lp loopSpan, escaped map[int64]bool, def map[Reg]int, elem map[Reg]bool) *licmPlan {
	code := fn.Code

	// What the loop writes.
	assigned := map[int64]bool{}
	type store struct {
		class memClass
		off   int64
		known bool
	}
	var stores []store
	for p := lp.head; p <= lp.end; p++ {
		in := code[p]
		switch {
		case in.Op == OpStore:
			assigned[in.Imm] = true
			if escaped[in.Imm] {
				stores = append(stores, store{rawMem, 0, false})
			}
		case in.Op == OpStoreMem:
			stores = append(stores, store{classOf(m, fn, elem, in.A), in.Imm, true})
		case in.Op == OpPoke, in.Op == OpStoreByte, in.Op == OpVStore:
			stores = append(stores, store{classOf(m, fn, elem, in.A), 0, false})
		case in.Op == OpVLoad, in.Op == OpVSplat, in.Op == OpVArith:
			// Packed reads, and arithmetic in xmm0 and xmm1: no memory
			// written. See vecloop.go.
		case csePure(in.Op), in.Op == OpCheckIndex, in.Op == OpLoadMem,
			in.Op == OpLoad, in.Op == OpConst, in.Op == OpFConst, in.Op == OpStr,
			in.Op == OpLoadByte, in.Op == OpPeek, in.Op == OpSymAddr,
			in.Op == OpGlobalAddr, in.Op == OpLabel,
			in.Op == OpJump, in.Op == OpJumpIf, in.Op == OpJumpNot:
		case in.Op == OpBoundsFail, in.Op == OpMustFail:
			// The program stops; nothing after it in the loop runs.
		default:
			return nil // a call, or something else that may write anywhere
		}
	}
	written := func(c memClass, off int64) bool {
		for _, s := range stores {
			if s.class == rawMem || c == rawMem || s.class == c && !(s.known && byField(c) && s.off != off) {
				return true
			}
		}
		return false
	}

	// The loads to move.
	type hoist struct {
		key  hoistKey
		slot int64
	}
	slotOf := map[hoistKey]int64{}
	var order []hoistKey
	baseType := map[hoistKey]vty{}
	valType := map[hoistKey]vty{}
	replace := map[int]int64{} // position in the loop -> slot to load instead
	for p := lp.head; p <= lp.end; p++ {
		in := code[p]
		if in.Op != OpLoadMem || in.A == NoReg || int(in.A) >= len(fn.RegTypes) {
			continue
		}
		c := classOf(m, fn, elem, in.A)
		if c == rawMem || c == "elem" || written(c, in.Imm) {
			continue
		}
		bt := fn.RegTypes[in.A]
		if bt.null || bt.res {
			continue
		}
		key := hoistKey{slot: -1, base: in.A, off: in.Imm}
		d, ok := def[in.A]
		switch {
		case ok && d < lp.head:
			// Defined before the loop: the same value on every trip.
		case ok && code[d].Op == OpLoad && d > lp.head && d <= lp.end &&
			!assigned[code[d].Imm] && !escaped[code[d].Imm]:
			key = hoistKey{slot: code[d].Imm, base: NoReg, off: in.Imm}
		default:
			continue
		}
		if _, seen := slotOf[key]; !seen {
			slotOf[key] = int64(fn.NSlots)
			fn.NSlots++
			order = append(order, key)
			baseType[key] = bt
			valType[key] = fn.RegTypes[in.Dst]
		}
		replace[p] = slotOf[key]
	}
	if len(order) == 0 {
		return nil
	}

	newReg := func(t vty) Reg {
		r := Reg(fn.NRegs)
		fn.NRegs++
		fn.RegTypes = append(fn.RegTypes, t)
		return r
	}
	var pre []Instr
	for _, key := range order {
		base := key.base
		if key.slot >= 0 {
			base = newReg(baseType[key])
			pre = append(pre, Instr{Op: OpLoad, Dst: base, A: NoReg, B: NoReg, Imm: key.slot})
		}
		v := newReg(valType[key])
		pre = append(pre,
			Instr{Op: OpLoadMem, Dst: v, A: base, B: NoReg, Imm: key.off, Comment: "hoisted"},
			Instr{Op: OpStore, Dst: NoReg, A: v, B: NoReg, Imm: slotOf[key]})
	}

	plan := &licmPlan{at: lp.head, pre: pre, replace: map[int]Instr{}}
	for p, s := range replace {
		plan.replace[p] = Instr{Op: OpLoad, Dst: code[p].Dst, A: NoReg, B: NoReg, Imm: s, Comment: code[p].Comment}
	}
	return plan
}

// hoistPure moves arithmetic whose inputs do not change in the loop to
// just before it. In
//
//	for k in 0..n { for j in 0..n { c[i * n + j] += x * b[k * n + j] } }
//
// i * n and k * n were multiplied again on every trip of the inner
// loop. An input is unchanging when it is defined before the loop, is a
// constant, is a load of a local the loop never stores to (and whose
// address never escaped), or is itself such a computation. Only csePure
// operations move: none of them can fault, so working one out on the
// way into a loop whose body would have skipped it costs a few cycles
// and changes nothing. The result waits in a new slot, read where the
// computation was; the loads it no longer needs are left to dce.
func hoistPure(fn *Func, lp loopSpan, escaped map[int64]bool, defAt map[Reg]int) *licmPlan {
	code := fn.Code
	assigned := map[int64]bool{}
	for p := lp.head; p <= lp.end; p++ {
		if code[p].Op == OpStore {
			assigned[code[p].Imm] = true
		}
	}
	inv := map[Reg]bool{}
	steady := func(r Reg) bool {
		if r == NoReg {
			return true
		}
		if inv[r] {
			return true
		}
		d, ok := defAt[r]
		return ok && d < lp.head
	}
	var cands []int
	for p := lp.head; p <= lp.end; p++ {
		in := code[p]
		if in.Dst == NoReg || int(in.Dst) >= len(fn.RegTypes) {
			continue
		}
		switch {
		case in.Op == OpConst || in.Op == OpFConst:
			inv[in.Dst] = true
		case in.Op == OpLoad && !assigned[in.Imm] && !escaped[in.Imm]:
			inv[in.Dst] = true
		case csePure(in.Op) && len(in.Args) == 0 && steady(in.A) && steady(in.B):
			inv[in.Dst] = true
			cands = append(cands, p)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	// Only the outermost of each chain moves as a value: i * n + 1 moves
	// once, with i * n worked out on the way, not twice.
	usedByCand := map[Reg]bool{}
	for _, p := range cands {
		usedByCand[code[p].A] = true
		usedByCand[code[p].B] = true
	}
	usedInLoop := map[Reg]bool{}
	for p := lp.head; p <= lp.end; p++ {
		for _, r := range instrRegs(code[p]) {
			usedInLoop[r] = true
		}
	}
	var roots []int
	for _, p := range cands {
		d := code[p].Dst
		if !usedInLoop[d] {
			continue
		}
		// A root unless every reader is another invariant computation.
		readByOther := false
		for q := lp.head; q <= lp.end; q++ {
			if q == p {
				continue
			}
			for _, r := range instrRegs(code[q]) {
				if r == d && !(inv[code[q].Dst] && code[q].Dst != NoReg && csePure(code[q].Op)) {
					readByOther = true
				}
			}
		}
		if readByOther {
			roots = append(roots, p)
		}
	}
	if len(roots) == 0 {
		return nil
	}

	var pre []Instr
	replace := map[int]int64{}
	for _, p := range roots {
		rename := map[Reg]Reg{}
		var emit func(r Reg) Reg
		emit = func(r Reg) Reg {
			if r == NoReg {
				return r
			}
			if n, ok := rename[r]; ok {
				return n
			}
			d, ok := defAt[r]
			if !ok || d < lp.head {
				return r
			}
			in := code[d]
			in.A = emit(in.A)
			in.B = emit(in.B)
			n := Reg(fn.NRegs)
			fn.NRegs++
			fn.RegTypes = append(fn.RegTypes, fn.RegTypes[in.Dst])
			rename[in.Dst] = n
			in.Dst = n
			in.Comment = "hoisted"
			pre = append(pre, in)
			return n
		}
		v := emit(code[p].Dst)
		slot := int64(fn.NSlots)
		fn.NSlots++
		pre = append(pre, Instr{Op: OpStore, Dst: NoReg, A: v, B: NoReg, Imm: slot})
		replace[p] = slot
	}

	plan := &licmPlan{at: lp.head, pre: pre, replace: map[int]Instr{}}
	for p, s := range replace {
		plan.replace[p] = Instr{Op: OpLoad, Dst: code[p].Dst, A: NoReg, B: NoReg, Imm: s, Comment: code[p].Comment}
	}
	return plan
}

// sinkLoopExits moves a block that leaves a loop from the top of the
// loop to after it. A loop that starts by deciding whether to stop,
// which is what a recursive function turned into a loop looks like,
//
//	top:  test; jumpnot body
//	      ...; ret
//	body: ...
//	      jump top
//
// takes two jumps a trip, into the body and back, and rotateLoops does
// not see it, since the test does not jump past the end. With the
// leaving block moved out,
//
//	top:  test; jumpif out
//	body: ...
//	      jump top
//	out:  ...; ret
//
// it is the shape rotateLoops turns into one jump a trip. The moved
// block must end in a jump, a return or a stop, so nothing ran on from
// it into the body before and nothing runs on from it into what follows
// the loop now. A register the test's preparation computed and the
// block reads is computed again at the top of the block with registers
// of its own: rotation will bring control there from a copy of the test
// at the bottom, which computes the same values into other registers.
// The preparation is loads, constants and pure arithmetic, and nothing
// writes memory between it and the block, so working it out again gives
// the same values.
func sinkLoopExits(fn *Func) {
	if len(fn.RegTypes) < fn.NRegs {
		return
	}
	for round := 0; round < 4; round++ {
		if !sinkLoopExitsOnce(fn) {
			return
		}
	}
}

func sinkLoopExitsOnce(fn *Func) bool {
	code := fn.Code
	at := map[int64]int{}
	for i, in := range code {
		if in.Op == OpLabel {
			at[in.Imm] = i
		}
	}
	type sink struct {
		h, k, b, end int
		out          int64
	}
	var plans []sink
	taken := map[int]bool{} // positions inside a loop already being changed
	for _, lp := range findLoops(fn) {
		h, end := lp.head, lp.end
		if code[end].Op != OpJump {
			continue
		}
		k := h + 1
		for k < end && testPrep(code[k].Op) {
			k++
		}
		if k >= end || k-h > 12 {
			continue
		}
		test := code[k]
		if test.Op != OpJumpNot && test.Op != OpJumpIf {
			continue
		}
		b, ok := at[test.Imm]
		if !ok || b <= k+1 || b > end || !endsFlow(code[b-1].Op) {
			continue
		}
		clash := false
		for p := h; p <= end; p++ {
			if taken[p] {
				clash = true
				break
			}
		}
		if clash {
			continue
		}
		for p := h; p <= end; p++ {
			taken[p] = true
		}
		out := int64(fn.NLabels)
		fn.NLabels++
		plans = append(plans, sink{h, k, b, end, out})
	}
	if len(plans) == 0 {
		return false
	}
	byTest := map[int]sink{}
	byEnd := map[int]sink{}
	for _, pl := range plans {
		byTest[pl.k] = pl
		byEnd[pl.end] = pl
	}
	moved := func(pl sink) []Instr {
		// What the block reads of the preparation, worked out again.
		defs := map[Reg]bool{}
		for p := pl.h + 1; p < pl.k; p++ {
			if code[p].Dst != NoReg {
				defs[code[p].Dst] = true
			}
		}
		read := false
		for p := pl.k + 1; p < pl.b && !read; p++ {
			for _, r := range instrRegs(code[p]) {
				if defs[r] {
					read = true
				}
			}
		}
		block := []Instr{{Op: OpLabel, Dst: NoReg, A: NoReg, B: NoReg, Imm: pl.out}}
		rename := map[Reg]Reg{}
		re := func(r Reg) Reg {
			if n, ok := rename[r]; ok {
				return n
			}
			return r
		}
		renameIn := func(in Instr) Instr {
			if in.A != NoReg {
				in.A = re(in.A)
			}
			if in.B != NoReg {
				in.B = re(in.B)
			}
			if len(in.Args) > 0 {
				args := make([]Reg, len(in.Args))
				for i, r := range in.Args {
					args[i] = r
					if r != NoReg {
						args[i] = re(r)
					}
				}
				in.Args = args
			}
			return in
		}
		if read {
			for p := pl.h + 1; p < pl.k; p++ {
				if code[p].Op == OpGCPoll {
					continue // checked at the top already
				}
				in := renameIn(code[p])
				if in.Dst != NoReg {
					n := Reg(fn.NRegs)
					fn.NRegs++
					fn.RegTypes = append(fn.RegTypes, fn.RegTypes[in.Dst])
					rename[in.Dst] = n
					in.Dst = n
				}
				block = append(block, in)
			}
		}
		for p := pl.k + 1; p < pl.b; p++ {
			block = append(block, renameIn(code[p]))
		}
		return block
	}
	res := make([]Instr, 0, len(code)+8*len(plans))
	skipTo := -1
	for p, in := range code {
		if p < skipTo {
			continue
		}
		if pl, ok := byTest[p]; ok {
			flip := in
			flip.Imm = pl.out
			if in.Op == OpJumpIf {
				flip.Op = OpJumpNot
			} else {
				flip.Op = OpJumpIf
			}
			res = append(res, flip)
			skipTo = pl.b
			continue
		}
		res = append(res, in)
		if pl, ok := byEnd[p]; ok {
			res = append(res, moved(pl)...)
		}
	}
	fn.Code = res
	return true
}

// testPrep is what may come between a loop's label and its test for
// sinkLoopExits and rotateLoops to take the test on: loads, constants
// and pure arithmetic, which give the same values worked out again,
// and a collection check. The check is copied with the test, so a loop
// still checks once a trip: in a threaded program that is where a
// thread stops for another's collection, and one spinning without it
// would never stop. Where nothing in the body allocates, dropPolls then
// removes the copy.
func testPrep(op Op) bool {
	return op == OpLoad || op == OpConst || op == OpFConst || op == OpLoadMem ||
		op == OpGCPoll || csePure(op)
}

// rotateLoops moves a loop's test to the bottom. A loop is lowered as
//
//	top:  test; jumpnot exit
//	      body
//	      jump top
//	exit:
//
// which takes two jumps a trip: the one back, and the test's, not
// taken. With a copy of the test at the bottom,
//
//	top:  test; jumpnot exit
//	again: body
//	      test'; jumpif again
//	exit:
//
// it takes one. The copy is made with registers of its own, every
// register being written once, and only of a test simple enough to
// copy (see testPrep). A `continue` still jumps to the top and its
// test.
func rotateLoops(fn *Func) {
	if len(fn.RegTypes) < fn.NRegs {
		return
	}
	// The loops are found once, and every rotation is planned against
	// the code as it stands; see the end.
	loops := findLoops(fn)
	uses := map[Reg]int{}
	for _, in := range fn.Code {
		for _, r := range instrRegs(in) {
			uses[r]++
		}
	}
	after := map[int]Instr{}
	instead := map[int][]Instr{}
	code := fn.Code
	for _, lp := range loops {
		h, end := lp.head, lp.end
		if code[end].Op != OpJump || end+1 >= len(code) || code[end+1].Op != OpLabel {
			continue
		}
		exit := code[end+1].Imm
		k := h + 1
		for k < end && testPrep(code[k].Op) {
			k++
		}
		if k >= end || k-h > 12 {
			continue
		}
		test := code[k]
		if (test.Op != OpJumpNot && test.Op != OpJumpIf) || test.Imm != exit {
			continue
		}
		// Nothing outside the test may read what the test computed:
		// coming round from the bottom, those registers would still
		// hold the first trip's values.
		mine := map[Reg]bool{}
		for p := h + 1; p < k; p++ {
			if code[p].Dst != NoReg {
				mine[code[p].Dst] = true
			}
		}
		inside := map[Reg]int{}
		for p := h + 1; p <= k; p++ {
			for _, r := range instrRegs(code[p]) {
				if mine[r] {
					inside[r]++
				}
			}
		}
		leaks := false
		for r := range mine {
			if uses[r] > inside[r] {
				leaks = true
			}
		}
		if leaks {
			continue
		}
		again := int64(fn.NLabels)
		fn.NLabels++

		rename := map[Reg]Reg{}
		fresh := func(r Reg) Reg {
			if n, ok := rename[r]; ok {
				return n
			}
			uses[r]++
			return r
		}
		var copyTest []Instr
		for p := h + 1; p < k; p++ {
			in := code[p]
			if in.A != NoReg {
				in.A = fresh(in.A)
			}
			if in.B != NoReg {
				in.B = fresh(in.B)
			}
			for _, r := range in.Args {
				if r != NoReg {
					uses[r]++
				}
			}
			if in.Dst != NoReg {
				n := Reg(fn.NRegs)
				fn.NRegs++
				fn.RegTypes = append(fn.RegTypes, fn.RegTypes[in.Dst])
				rename[in.Dst] = n
				in.Dst = n
			}
			copyTest = append(copyTest, in)
		}
		back := Instr{Op: OpJumpIf, Dst: NoReg, A: fresh(test.A), B: NoReg, Imm: again}
		if test.Op == OpJumpIf {
			back.Op = OpJumpNot
		}
		copyTest = append(copyTest, back)

		after[k] = Instr{Op: OpLabel, Dst: NoReg, A: NoReg, B: NoReg, Imm: again}
		instead[end] = copyTest
	}
	if len(instead) == 0 {
		return
	}
	// Every rotation's edits are at places of its own in the original
	// code - a label after its test, the copied test where its jump
	// back was - so they are worked out against that code and made in
	// one pass. Copying the function once per loop rotated was
	// quadratic: 2000 loops in one function took half a minute.
	out := make([]Instr, 0, len(fn.Code)+len(after)+8*len(instead))
	for p, in := range fn.Code {
		if rep, ok := instead[p]; ok {
			out = append(out, rep...)
			continue
		}
		out = append(out, in)
		if lab, ok := after[p]; ok {
			out = append(out, lab)
		}
	}
	fn.Code = out
}

// instrRegs lists the registers an instruction reads.
func instrRegs(in Instr) []Reg {
	var out []Reg
	if in.A != NoReg {
		out = append(out, in.A)
	}
	if in.B != NoReg {
		out = append(out, in.B)
	}
	for _, r := range in.Args {
		if r != NoReg {
			out = append(out, r)
		}
	}
	return out
}
