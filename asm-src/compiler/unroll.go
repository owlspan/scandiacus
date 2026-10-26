package main

// Unrolling of counted loops.
//
// A loop whose body is a few instructions spends a fair part of each
// trip on the counter: the add, the compare and the branch. clang runs
// several trips of the benchmark loop per pass and pays that once. Here
// a rotated counted loop (see rotateLoops)
//
//	top:   test; jumpnot exit
//	again: body
//	       i = i + s; test'; jumpif again
//	exit:
//
// becomes
//
//	top:   test; jumpnot exit
//	       if limit - i < need goto again
//	more:  body, k times, trip j reading i0 + j*s for the counter
//	       i = i0 + k*s
//	       if limit - i >= need goto more
//	       goto top
//	again: body                       (the remainder, one trip at a time)
//	       i = i + s; test'; jumpif again
//	exit:
//
// where need is the span the k trips cover: the last one still has to
// pass the loop's test. The difference is compared rather than i + k*s
// against the limit so that nothing near the top of the range can
// wrap into a false "enough left". If limit - i itself wraps it comes
// out negative, which only sends the loop down the one-at-a-time path.
//
// Only loops whose body is straight-line code are taken: no labels,
// no jumps but out of the loop (a bounds check is one instruction, not
// a branch), no calls, and nothing that writes the counter or the
// limit. A body with a call spends its time there; one with branches
// would need its labels copied.

// unrollMax is the largest body, in instructions, that is repeated, and
// unrollWays how many times. A body of up to unrollSmall instructions
// is repeated unrollWays times, a larger one twice.
const (
	unrollMax   = 24
	unrollSmall = 12
	unrollWays  = 4
)

func unrollLoops(fn *Func) {
	if len(fn.RegTypes) < fn.NRegs {
		return
	}
	loops := findLoops(fn)
	if len(loops) == 0 {
		return
	}
	escaped := map[int64]bool{}
	consts := map[Reg]int64{}
	uses := map[Reg]int{}
	defAt := map[Reg]int{}
	for i, in := range fn.Code {
		if in.Op == OpSlotAddr {
			escaped[in.Imm] = true
		}
		if in.Op == OpConst {
			consts[in.Dst] = in.Imm
		}
		if in.Dst != NoReg {
			defAt[in.Dst] = i
		}
		for _, r := range instrRegs(in) {
			uses[r]++
		}
	}
	if fn.Env {
		escaped[0] = true
	}
	before := map[int][]Instr{}
	for _, lp := range loops {
		if ins := planUnroll(fn, lp, escaped, consts, uses, defAt); ins != nil {
			before[lp.head] = ins
		}
	}
	if len(before) == 0 {
		return
	}
	out := make([]Instr, 0, len(fn.Code)+16*len(before))
	for i, in := range fn.Code {
		out = append(out, before[i]...)
		out = append(out, in)
	}
	fn.Code = out
}

// planUnroll returns the instructions to put in front of the loop's
// head label, or nil when the loop is not one it takes.
func planUnroll(fn *Func, lp loopSpan, escaped map[int64]bool,
	consts map[Reg]int64, uses map[Reg]int, defAt map[Reg]int) []Instr {
	code := fn.Code
	h, end := lp.head, lp.end
	if code[end].Op != OpJumpIf || code[end].Imm != lp.label || end+1 >= len(code) || code[end+1].Op != OpLabel {
		return nil
	}
	exit := code[end+1].Imm

	// The top of the loop: label, test, jump to exit, then the head.
	if h < 2 || code[h-1].Op != OpJumpNot || code[h-1].Imm != exit {
		return nil
	}
	top := -1
	for p := h - 2; p >= 0 && h-p <= 14; p-- {
		if code[p].Op == OpLabel {
			top = p
			break
		}
		if !testPrep(code[p].Op) {
			return nil
		}
	}
	if top < 0 {
		return nil
	}

	// The tail: counter step, then the test copy rotateLoops made.
	s := -1
	for p := end - 1; p > h && end-p <= 12; p-- {
		if code[p].Op == OpStore {
			s = p
			break
		}
		if !testPrep(code[p].Op) {
			return nil
		}
	}
	if s < 3 || s-3 <= h {
		return nil
	}
	ctr := code[s].Imm
	add := code[s-1]
	var ld, cst Instr
	switch {
	case code[s-3].Op == OpLoad && code[s-2].Op == OpConst:
		ld, cst = code[s-3], code[s-2]
	case code[s-3].Op == OpConst && code[s-2].Op == OpLoad:
		ld, cst = code[s-2], code[s-3]
	default:
		return nil
	}
	if add.Op != OpAdd || add.A != ld.Dst || add.B != cst.Dst || code[s].A != add.Dst ||
		ld.Imm != ctr || cst.Imm < 1 || cst.Imm > 1<<20 {
		return nil
	}
	step := cst.Imm
	incStart := s - 3
	// A label left in front of the step is the target of `continue`;
	// with nothing jumping to it it can stay behind.
	bodyEnd := incStart
	if code[bodyEnd-1].Op == OpLabel && bodyEnd-1 > h {
		bodyEnd--
	}

	// The comparison and the limit.
	cmp := code[end-1]
	if (cmp.Op != OpLt && cmp.Op != OpLe) || cmp.Dst != code[end].A {
		return nil
	}
	// Before forwardLoads the test copy reads the counter back from its
	// slot rather than using the sum.
	if cmp.A != add.Dst {
		if q, ok := defAt[cmp.A]; !ok || q <= s || q >= end || code[q].Op != OpLoad || code[q].Imm != ctr {
			return nil
		}
	}
	limSlot := int64(-1)
	var limReg Reg
	p, ok := defAt[cmp.B]
	switch {
	case !ok:
		return nil
	case p > s && p < end:
		if code[p].Op != OpLoad || code[p].Imm == ctr || escaped[code[p].Imm] {
			return nil
		}
		limSlot = code[p].Imm
	case p >= h && p <= end:
		return nil
	default:
		limReg = cmp.B
	}
	// The polls and loads between the step and the comparison are all
	// the tail may hold.
	for p := s + 1; p < end-1; p++ {
		switch code[p].Op {
		case OpLoad, OpConst, OpGCPoll:
		default:
			return nil
		}
	}
	if escaped[ctr] {
		return nil
	}

	// The body.
	body := code[h+1 : bodyEnd]
	if len(body) == 0 || len(body) > unrollMax {
		return nil
	}
	inBody := map[Reg]int{}
	for _, in := range body {
		switch in.Op {
		case OpLabel, OpJump, OpCall, OpCallClosure, OpCallAddr, OpRet,
			OpAtomicAdd, OpAtomicSwap, OpAtomicCAS:
			return nil
		case OpJumpIf, OpJumpNot:
			// A jump out of the loop is fine; a jump to a label inside
			// it is not, and the only labels inside are the head and a
			// continue label.
			for p := h; p < end; p++ {
				if code[p].Op == OpLabel && code[p].Imm == in.Imm {
					return nil
				}
			}
		case OpStore, OpStoreByte:
			if in.Imm == ctr || in.Imm == limSlot {
				return nil
			}
		}
		if in.Dst != NoReg {
			inBody[in.Dst] = 0
		}
	}
	for _, in := range body {
		for _, r := range instrRegs(in) {
			if _, ok := inBody[r]; ok {
				inBody[r]++
			}
		}
	}
	for r, n := range inBody {
		if uses[r] != n {
			return nil // read outside the body
		}
	}

	ways := unrollWays
	if len(body) > unrollSmall {
		ways = 2
	}
	need := int64(ways-1) * step
	if cmp.Op == OpLt {
		need++
	}

	nreg := func(like Reg) Reg {
		r := Reg(fn.NRegs)
		fn.NRegs++
		fn.RegTypes = append(fn.RegTypes, fn.RegTypes[like])
		return r
	}
	ins := func(op Op, dst, a, b Reg, imm int64) Instr {
		return Instr{Op: op, Dst: dst, A: a, B: b, Imm: imm}
	}
	var out []Instr
	konst := func(v int64) Reg {
		r := nreg(ld.Dst)
		out = append(out, ins(OpConst, r, NoReg, NoReg, v))
		return r
	}
	limit := func() Reg {
		if limSlot < 0 {
			return limReg
		}
		r := nreg(ld.Dst)
		out = append(out, ins(OpLoad, r, NoReg, NoReg, limSlot))
		return r
	}
	// enough emits "jump to label if fewer than need trips are left"
	// (or, with the opposite sense, if enough are) on the counter in c.
	enough := func(c Reg, jop Op, label int64) {
		d := nreg(ld.Dst)
		out = append(out, ins(OpSub, d, limit(), c, 0))
		nd := konst(need)
		t := nreg(ld.Dst)
		out = append(out, ins(OpLt, t, d, nd, 0))
		out = append(out, ins(jop, NoReg, t, NoReg, label))
	}

	more := int64(fn.NLabels)
	fn.NLabels++
	topLabel := code[top].Imm

	c0 := nreg(ld.Dst)
	out = append(out, ins(OpLoad, c0, NoReg, NoReg, ctr))
	enough(c0, OpJumpIf, lp.label)
	out = append(out, ins(OpLabel, NoReg, NoReg, NoReg, more))
	c0 = nreg(ld.Dst)
	out = append(out, ins(OpLoad, c0, NoReg, NoReg, ctr))
	for j := 0; j < ways; j++ {
		cj := c0
		if j > 0 {
			cj = nreg(ld.Dst)
			out = append(out, ins(OpAdd, cj, c0, konst(int64(j)*step), 0))
		}
		rename := map[Reg]Reg{}
		get := func(r Reg) Reg {
			if r == NoReg {
				return r
			}
			if n, ok := rename[r]; ok {
				return n
			}
			return r
		}
		for _, in := range body {
			if in.Op == OpLoad && in.Imm == ctr {
				rename[in.Dst] = cj
				continue
			}
			c := in
			c.A, c.B = get(in.A), get(in.B)
			if len(in.Args) > 0 {
				c.Args = make([]Reg, len(in.Args))
				for i, r := range in.Args {
					c.Args[i] = get(r)
				}
			}
			if in.Dst != NoReg {
				n := nreg(in.Dst)
				rename[in.Dst] = n
				c.Dst = n
			}
			out = append(out, c)
		}
	}
	next := nreg(ld.Dst)
	out = append(out, ins(OpAdd, next, c0, konst(int64(ways)*step), 0))
	out = append(out, ins(OpStore, NoReg, next, NoReg, ctr))
	enough(next, OpJumpNot, more)
	out = append(out, ins(OpJump, NoReg, NoReg, NoReg, topLabel))
	return out
}
