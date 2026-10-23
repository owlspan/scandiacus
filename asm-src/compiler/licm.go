package main

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
	tried := map[int64]bool{}
	for round := 0; round < 256; round++ {
		moved := false
		for _, lp := range findLoops(fn) {
			if tried[lp.label] {
				continue
			}
			tried[lp.label] = true
			if hoistLoop(m, fn, lp, escaped) {
				moved = true
				break // positions have shifted; find the loops again
			}
		}
		if !moved {
			return
		}
	}
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
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a, b := out[j-1], out[j]
			if b.end-b.head < a.end-a.head || (b.end-b.head == a.end-a.head && b.head < a.head) {
				out[j-1], out[j] = b, a
			}
		}
	}
	return out
}

type hoistKey struct {
	slot int64 // the local the base is loaded from, or -1
	base Reg   // the base itself when it is defined before the loop
	off  int64
}

func hoistLoop(m *Module, fn *Func, lp loopSpan, escaped map[int64]bool) bool {
	code := fn.Code
	def := map[Reg]int{}
	for i, in := range code {
		if in.Dst != NoReg {
			if _, seen := def[in.Dst]; !seen {
				def[in.Dst] = i
			}
		}
	}
	elem := map[Reg]bool{}
	for _, in := range code {
		if in.Op == OpIndexAddr {
			elem[in.Dst] = true
		}
	}

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
		case in.Op == OpPoke, in.Op == OpStoreByte:
			stores = append(stores, store{classOf(m, fn, elem, in.A), 0, false})
		case csePure(in.Op), in.Op == OpCheckIndex, in.Op == OpLoadMem,
			in.Op == OpLoad, in.Op == OpConst, in.Op == OpFConst, in.Op == OpStr,
			in.Op == OpLoadByte, in.Op == OpPeek, in.Op == OpSymAddr,
			in.Op == OpGlobalAddr, in.Op == OpLabel,
			in.Op == OpJump, in.Op == OpJumpIf, in.Op == OpJumpNot:
		case in.Op == OpBoundsFail, in.Op == OpMustFail:
			// The program stops; nothing after it in the loop runs.
		default:
			return false // a call, or something else that may write anywhere
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
		return false
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

	out := make([]Instr, 0, len(code)+len(pre))
	out = append(out, code[:lp.head]...)
	out = append(out, pre...)
	for p := lp.head; p < len(code); p++ {
		in := code[p]
		if s, ok := replace[p]; ok {
			in = Instr{Op: OpLoad, Dst: in.Dst, A: NoReg, B: NoReg, Imm: s, Comment: in.Comment}
		}
		out = append(out, in)
	}
	fn.Code = out
	return true
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
// copy: loads, constants and pure arithmetic, no calls. A `continue`
// still jumps to the top and its test.
func rotateLoops(fn *Func) {
	if len(fn.RegTypes) < fn.NRegs {
		return
	}
	for _, lp := range findLoops(fn) {
		code := fn.Code
		h, end := lp.head, lp.end
		if code[end].Op != OpJump || end+1 >= len(code) || code[end+1].Op != OpLabel {
			continue
		}
		exit := code[end+1].Imm
		k := h + 1
		for k < end && (code[k].Op == OpLoad || code[k].Op == OpConst ||
			code[k].Op == OpFConst || code[k].Op == OpLoadMem || csePure(code[k].Op)) {
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
		leaks := false
		for p, in := range code {
			if p > h && p <= k {
				continue
			}
			for _, r := range append([]Reg{in.A, in.B}, in.Args...) {
				if r != NoReg && mine[r] {
					leaks = true
				}
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

		out := make([]Instr, 0, len(code)+len(copyTest)+1)
		out = append(out, code[:k+1]...)
		out = append(out, Instr{Op: OpLabel, Dst: NoReg, A: NoReg, B: NoReg, Imm: again})
		out = append(out, code[k+1:end]...)
		out = append(out, copyTest...)
		out = append(out, code[end+1:]...)
		fn.Code = out
		rotateLoops(fn) // positions have moved; the rest from scratch
		return
	}
}
