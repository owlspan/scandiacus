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
	"sort"
	"strconv"
)

var calleeSaved = []string{"rbx", "rsi", "rdi", "r12", "r13", "r14", "r15"}

// promoteSlots picks the slots to keep in registers.
func promoteSlots(f *Func) map[int64]string {
	ok := map[int64]bool{}
	uses := map[int64]int{}
	plain := func(r Reg) bool {
		if r < 0 || int(r) >= len(f.RegTypes) {
			return false
		}
		t := f.RegTypes[r]
		return !t.holdsPointer() && t.k != kFloat && !t.null && !t.res &&
			(t.k == kInt || t.k == kBool)
	}
	bad := map[int64]bool{}
	for _, in := range f.Code {
		switch in.Op {
		case OpLoad:
			uses[in.Imm]++
			if !plain(in.Dst) {
				bad[in.Imm] = true
			}
			ok[in.Imm] = true
		case OpStore:
			uses[in.Imm]++
			if !plain(in.A) {
				bad[in.Imm] = true
			}
			ok[in.Imm] = true
		case OpSlotAddr:
			bad[in.Imm] = true
		case OpStoreByte:
			uses[in.Imm]++
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
		if uses[picks[i]] != uses[picks[j]] {
			return uses[picks[i]] > uses[picks[j]]
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

// savedRegs is which callee-saved registers this function must save:
// the ones it promotes into, or all of them for the collector.
func (e *Emitter) savedRegs() []string {
	if e.f.Name == collectSym {
		return calleeSaved
	}
	var used []string
	for _, r := range calleeSaved {
		for _, p := range e.promoted {
			if p == r {
				used = append(used, r)
				break
			}
		}
	}
	return used
}

// saveAt is where callee-saved register i is kept in the frame.
func (e *Emitter) saveAt(i int) string {
	off := (int64(e.f.NSlots) + int64(e.f.NRegs) + int64(i) + 1) * 8
	return "qword ptr [rbp-" + strconv.FormatInt(off, 10) + "]"
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
// optimisations on, so VEYL_NOOPT keeps the plain form to compare with.
func (e *Emitter) fuseBranch(cmp, br Instr, uses map[Reg]int) bool {
	if e.homes == nil {
		return false
	}
	cc, ok := map[Op]string{OpEq: "e", OpNe: "ne", OpLt: "l", OpLe: "le", OpGt: "g", OpGe: "ge"}[cmp.Op]
	if !ok || cmp.Dst == NoReg || uses[cmp.Dst] != 1 {
		return false
	}
	if (br.Op != OpJumpIf && br.Op != OpJumpNot) || br.A != cmp.Dst {
		return false
	}
	if br.Op == OpJumpNot {
		cc = map[string]string{"e": "ne", "ne": "e", "l": "ge", "ge": "l", "le": "g", "g": "le"}[cc]
	}
	e.cmp(cmp)
	e.line("j%s .L%s_%d", cc, e.labelBase(), br.Imm)
	return true
}
