package main

// Slot promotion: the busiest locals live in callee-saved registers.
//
// The register allocator in regalloc.go keeps a value in r8-r11 only
// between two barriers and never across a label, so a loop counter is
// loaded from its frame slot and stored back on every trip round. This
// pass gives whole slots - every read and write of a local, for the life
// of the function - to rbx, rsi, rdi and r12-r15. Those survive calls,
// so a barrier costs nothing, and a register is only ever that one slot,
// so a label is no reason to give it up either. `i += 1` in a loop
// becomes one add to a register.
//
// A slot qualifies when everything stored into it and loaded out of it
// is an int or a bool, and nothing takes its address. Floats would need
// movq to reach a general register, which the byte writer does not
// encode; anything holding a pointer stays where the collector scans.
// The busiest seven, by how many loads and stores name them, win.
//
// The collector finds roots by scanning the stack. A register is not on
// the stack, and the lowerer does keep the odd object address in an int
// slot of its own, so the collector's entry function saves all seven
// registers into its frame whether or not it uses them: whatever a
// caller had in one is then a stack word like any other.
//
// The registers are saved in the prologue and put back before each ret,
// in a block of the frame between the virtual registers and the
// outgoing arguments.

import (
	"fmt"
	"sort"
	"strings"
)

var calleeSaved = []string{"rbx", "rsi", "rdi", "r12", "r13", "r14", "r15"}

// loopWeights gives each instruction a weight for how often it runs,
// as far as can be told without running it: eight times for each loop
// around it. A slot read once in an inner loop then outranks one read
// five times in straight-line code, which is the order that matters
// when there are more slots than registers.
func loopWeights(f *Func) []int {
	at := map[int64]int{}
	for i, in := range f.Code {
		if in.Op == OpLabel {
			at[in.Imm] = i
		}
	}
	depth := make([]int, len(f.Code)+1)
	for i, in := range f.Code {
		switch in.Op {
		case OpJump, OpJumpIf, OpJumpNot:
			if h, ok := at[in.Imm]; ok && h < i {
				depth[h]++
				depth[i+1]--
			}
		}
	}
	w := make([]int, len(f.Code))
	d := 0
	for i := range f.Code {
		d += depth[i]
		n := d
		if n > 4 {
			n = 4
		}
		w[i] = 1 << (3 * n)
	}
	return w
}

// promoteSlots picks the slots to keep in registers.
//
// Not a captured local's slot, which holds a pointer to its cell, and in
// a program with threads not the lowerer's own temporaries either, which
// can hold new objects as ints. A temporary made with scalarTemp is a
// number by construction and is promoted like a local. A thread parked for another thread's
// collection is scanned on its stack only: a pointer held nowhere but a
// callee-saved register is invisible to that collection and gets freed.
// One thread alone collects in its own call, which saves the registers
// first, so there the temporaries are safe.
func promoteSlots(f *Func, threaded bool) map[int64]string {
	ok := map[int64]bool{}
	uses := map[int64]int{}
	weight := map[int64]int{}
	w := loopWeights(f)
	plain := func(r Reg) bool {
		if r < 0 || int(r) >= len(f.RegTypes) {
			return false
		}
		t := f.RegTypes[r]
		// An address from the allocator is as safe in a callee-saved
		// register as any number when one thread runs: the collector
		// saves the registers first. See the note above.
		if t.k == kRawPtr && !threaded && !t.null && !t.res {
			return true
		}
		return !t.holdsPointer() && t.k != kFloat && !t.null && !t.res &&
			(t.k == kInt || t.k == kBool)
	}
	bad := map[int64]bool{}
	for slot := range f.Boxed {
		bad[slot] = true
	}
	if threaded {
		for slot := range f.Temps {
			bad[slot] = true
		}
	}
	for i, in := range f.Code {
		switch in.Op {
		case OpLoad:
			uses[in.Imm]++
			weight[in.Imm] += w[i]
			if !plain(in.Dst) {
				bad[in.Imm] = true
			}
			ok[in.Imm] = true
		case OpStore:
			uses[in.Imm]++
			weight[in.Imm] += w[i]
			if !plain(in.A) {
				bad[in.Imm] = true
			}
			ok[in.Imm] = true
		case OpSlotAddr:
			bad[in.Imm] = true
		case OpStoreByte:
			uses[in.Imm]++
			weight[in.Imm] += w[i]
		}
	}
	if f.Env {
		bad[0] = true // the environment, written in the prologue
	}
	var picks []int64
	for s := range ok {
		if !bad[s] && uses[s] >= 2 {
			picks = append(picks, s)
		}
	}
	sort.Slice(picks, func(i, j int) bool {
		if weight[picks[i]] != weight[picks[j]] {
			return weight[picks[i]] > weight[picks[j]]
		}
		return picks[i] < picks[j]
	})
	out := map[int64]string{}
	for i, s := range picks {
		if i >= len(calleeSaved) {
			break
		}
		out[s] = calleeSaved[i]
	}
	return out
}

// calleeSavedXmm are the float registers a callee must preserve on
// Windows x64. All 128 bits of each, which is why they are saved with
// movups rather than movsd.
var calleeSavedXmm = []string{"xmm6", "xmm7", "xmm8", "xmm9", "xmm10",
	"xmm11", "xmm12", "xmm13", "xmm14", "xmm15"}

// promoteFloatSlots does for float locals what promoteSlots does for
// ints, into xmm6-xmm15: `x = x * 0.5 + y` in a loop then reads and
// writes registers rather than the frame. A slot qualifies when every
// load and store of it moves a float and nothing takes its address.
func promoteFloatSlots(f *Func) map[int64]string {
	uses := map[int64]int{}
	weight := map[int64]int{}
	w := loopWeights(f)
	bad := map[int64]bool{}
	isFloat := func(r Reg) bool {
		if r < 0 || int(r) >= len(f.RegTypes) {
			return false
		}
		t := f.RegTypes[r]
		return t.k == kFloat && !t.null && !t.res
	}
	for i, in := range f.Code {
		switch in.Op {
		case OpLoad:
			uses[in.Imm]++
			weight[in.Imm] += w[i]
			if !isFloat(in.Dst) {
				bad[in.Imm] = true
			}
		case OpStore:
			uses[in.Imm]++
			weight[in.Imm] += w[i]
			if !isFloat(in.A) {
				bad[in.Imm] = true
			}
		case OpSlotAddr, OpStoreByte:
			bad[in.Imm] = true
		}
	}
	if f.Env {
		bad[0] = true
	}
	var picks []int64
	for s, n := range uses {
		if !bad[s] && n >= 2 {
			picks = append(picks, s)
		}
	}
	sort.Slice(picks, func(i, j int) bool {
		if weight[picks[i]] != weight[picks[j]] {
			return weight[picks[i]] > weight[picks[j]]
		}
		return picks[i] < picks[j]
	})
	out := map[int64]string{}
	for i, s := range picks {
		if i >= len(calleeSavedXmm) {
			break
		}
		out[s] = calleeSavedXmm[i]
	}
	return out
}

func isXmm(s string) bool { return strings.HasPrefix(s, "xmm") }

// savedRegs is which callee-saved registers this function must save:
// the ones it promotes into, or all the general ones for the collector.
func (e *Emitter) savedRegs() []string {
	if e.f.Name == collectSym {
		return calleeSaved
	}
	in := map[string]bool{}
	for _, p := range e.promoted {
		in[p] = true
	}
	for _, h := range e.homes {
		in[h] = true
	}
	var used []string
	for _, r := range append(append([]string{}, calleeSaved...), calleeSavedXmm...) {
		if in[r] {
			used = append(used, r)
		}
	}
	return used
}

// saveAt is where callee-saved register i is kept in the frame: two
// words each, room for the whole of an xmm register.
func (e *Emitter) saveAt(i int) string {
	// i counts every saved register; the pushed general ones come first
	// in that order and take no room here.
	i -= e.pushedGP
	off := (int64(e.f.NSlots)+e.nvslots+2*int64(i)+2)*8 + int64(e.pushedGP)*8
	return e.frameRef(off)
}

// epilogue puts back every callee-saved register and returns. With a
// frame pointer the pushed ones are read from under rbp with a mov
// each, so the epilogue keeps the shape the unwinder knows: mov rsp,
// rbp, pop rbp, ret. Without one the shape is add rsp, the pops in
// reverse, ret, which is the other form Windows recognises.
func (e *Emitter) epilogue() {
	gp := 0
	for i, r := range e.saved {
		if isXmm(r) {
			e.restoreReg(i, r)
			continue
		}
		gp++
		if !e.frameless {
			e.line("mov %s, qword ptr [rbp-%d]", r, gp*8)
		}
	}
	if !e.frameless {
		e.line("mov rsp, rbp")
		e.line("pop rbp")
		e.line("ret")
		return
	}
	e.line("add rsp, %d", e.frameBytes)
	for i := len(e.saved) - 1; i >= 0; i-- {
		if !isXmm(e.saved[i]) {
			e.line("pop %s", e.saved[i])
		}
	}
	e.line("ret")
}

func (e *Emitter) saveReg(i int, r string) {
	if isXmm(r) {
		e.line("movups %s, %s", e.saveAt(i), r)
		return
	}
	e.line("mov qword ptr %s, %s", e.saveAt(i), r)
}

func (e *Emitter) restoreReg(i int, r string) {
	if isXmm(r) {
		e.line("movups %s, %s", r, e.saveAt(i))
		return
	}
	e.line("mov %s, qword ptr %s", r, e.saveAt(i))
}

// regUses counts how many instructions read each virtual register.
func regUses(f *Func) map[Reg]int {
	uses := map[Reg]int{}
	for _, in := range f.Code {
		if in.A != NoReg {
			uses[in.A]++
		}
		if in.B != NoReg {
			uses[in.B]++
		}
		for _, a := range in.Args {
			uses[a]++
		}
	}
	return uses
}

// fuseBranch emits a compare whose only reader is the branch right
// after it as one cmp and one conditional jump, rather than building a
// 0 or 1, storing it, reading it back and testing it. Only with the
// optimisations on, so SCANDIACUS_NOOPT keeps the plain form to compare with.
// fuseFloatBranch is fuseBranch for a float comparison: comisd and a
// jump on its flags, rather than a 0 or 1 made with set and tested.
// The condition codes are the ones OpFGt and the rest use, chosen so a
// NaN on either side compares false: an unordered pair sets CF, ZF and
// PF together.
func (e *Emitter) fuseFloatBranch(cmp, br Instr, uses map[Reg]int) bool {
	if e.homes == nil || !fusesFloatBranch(cmp, br, uses) {
		return false
	}
	x, y := cmp.A, cmp.B
	if cmp.Op == OpFLt || cmp.Op == OpFLe {
		x, y = y, x
	}
	a := e.loc(x)
	if !isXmm(a) {
		e.line("movsd xmm0, %s", a)
		a = "xmm0"
	}
	b := e.loc(y)
	if idx, ok := e.fconsts[y]; ok {
		// Read from the pool, which is also how planConsts lets the
		// constant's own load go when nothing else wants it.
		b = fmt.Sprintf("__flt%d[rip]", idx)
	}
	e.line("comisd %s, %s", a, b)
	target := fmt.Sprintf(".L%s_%d", e.labelBase(), br.Imm)
	taken := br.Op == OpJumpIf
	switch cmp.Op {
	case OpFGt, OpFLt:
		if taken {
			e.line("ja %s", target)
		} else {
			e.line("jbe %s", target)
		}
	case OpFGe, OpFLe:
		if taken {
			e.line("jae %s", target)
		} else {
			e.line("jb %s", target)
		}
	default:
		// Equal is ZF set with PF clear; anything else, NaN included,
		// is not equal.
		equal := (cmp.Op == OpFEq) == taken
		if equal {
			e.nCold++
			skip := fmt.Sprintf(".Lne_%s_%d", e.labelBase(), e.nCold)
			e.line("jp %s", skip)
			e.line("je %s", target)
			e.label(skip)
		} else {
			e.line("jne %s", target)
			e.line("jp %s", target)
		}
	}
	return true
}

// fusesFloatBranch is the part of fuseFloatBranch's decision that reads
// only the two instructions, so planConsts can ask it before any code is
// written.
func fusesFloatBranch(cmp, br Instr, uses map[Reg]int) bool {
	switch cmp.Op {
	case OpFLt, OpFLe, OpFGt, OpFGe, OpFEq, OpFNe:
	default:
		return false
	}
	if cmp.Dst == NoReg || uses[cmp.Dst] != 1 {
		return false
	}
	return (br.Op == OpJumpIf || br.Op == OpJumpNot) && br.A == cmp.Dst
}

func (e *Emitter) fuseBranch(cmp, br Instr, uses map[Reg]int) bool {
	if e.homes == nil {
		return false
	}
	cc, ok := condCodes[cmp.Op]
	if !ok || cmp.Dst == NoReg || uses[cmp.Dst] != 1 {
		return false
	}
	if (br.Op != OpJumpIf && br.Op != OpJumpNot) || br.A != cmp.Dst {
		return false
	}
	if br.Op == OpJumpNot {
		cc = map[string]string{"e": "ne", "ne": "e", "l": "ge", "ge": "l", "le": "g", "g": "le",
			"b": "ae", "ae": "b", "be": "a", "a": "be"}[cc]
	}
	e.cmp(cmp)
	e.line("j%s .L%s_%d", cc, e.labelBase(), br.Imm)
	return true
}
