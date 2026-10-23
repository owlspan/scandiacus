package main

import (
	"math"
	"testing"
)

// mkfn builds a one-block function for the opt tests.
func mkfn(code []Instr, nregs int) *Func {
	return &Func{Name: "t", NRegs: nregs, Code: code}
}

func TestFoldArith(t *testing.T) {
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 2},
		{Op: OpConst, Dst: 1, Imm: 3},
		{Op: OpAdd, Dst: 2, A: 0, B: 1},
		{Op: OpStore, A: 2, Imm: 0},
	}, 3)
	fold(&Module{}, fn)

	got := fn.Code[2]
	if got.Op != OpConst || got.Imm != 5 {
		t.Fatalf("add of two consts did not fold: %+v", got)
	}
}

func TestFoldChains(t *testing.T) {
	// (2 + 3) * 4 as three ops folds to one const within the sweeps.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 2},
		{Op: OpConst, Dst: 1, Imm: 3},
		{Op: OpAdd, Dst: 2, A: 0, B: 1},
		{Op: OpConst, Dst: 3, Imm: 4},
		{Op: OpMul, Dst: 4, A: 2, B: 3},
		{Op: OpStore, A: 4, Imm: 0},
	}, 5)
	fold(&Module{}, fn)

	if got := fn.Code[4]; got.Op != OpConst || got.Imm != 20 {
		t.Fatalf("chained fold wrong: %+v", got)
	}
}

func TestFoldDivGuards(t *testing.T) {
	const min64 = math.MinInt64

	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 7},
		{Op: OpConst, Dst: 1, Imm: 2},
		{Op: OpDiv, Dst: 2, A: 0, B: 1},
	}, 3)
	fold(&Module{}, fn)
	if got := fn.Code[2]; got.Op != OpConst || got.Imm != 3 {
		t.Fatalf("7/2 did not fold: %+v", got)
	}

	// x / 0 must stay a runtime event.
	fn = mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 7},
		{Op: OpConst, Dst: 1, Imm: 0},
		{Op: OpDiv, Dst: 2, A: 0, B: 1},
	}, 3)
	fold(&Module{}, fn)
	if got := fn.Code[2]; got.Op != OpDiv {
		t.Fatalf("div by zero folded: %+v", got)
	}

	// MinInt / -1 traps on the hardware; folding would remove the trap.
	fn = mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: min64},
		{Op: OpConst, Dst: 1, Imm: -1},
		{Op: OpDiv, Dst: 2, A: 0, B: 1},
	}, 3)
	fold(&Module{}, fn)
	if got := fn.Code[2]; got.Op != OpDiv {
		t.Fatalf("MinInt/-1 folded: %+v", got)
	}
}

func TestFoldShiftMask(t *testing.T) {
	// The hardware masks the count with 63, so shifting by 64 leaves
	// the value alone rather than producing zero.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 1},
		{Op: OpConst, Dst: 1, Imm: 64},
		{Op: OpShl, Dst: 2, A: 0, B: 1},
	}, 3)
	fold(&Module{}, fn)
	if got := fn.Code[2]; got.Op != OpConst || got.Imm != 1 {
		t.Fatalf("shift by 64 did not follow the hardware mask: %+v", got)
	}
}

func TestFoldBranchKillsFallthrough(t *testing.T) {
	// cond is 1, so the folded unconditional jump makes everything
	// between it and its target unreachable. The label survives.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 1},
		{Op: OpJumpIf, A: 0, Imm: 0},
		{Op: OpConst, Dst: 1, Imm: 99},
		{Op: OpLabel, Imm: 0},
		{Op: OpRet, A: NoReg},
	}, 2)
	fold(&Module{}, fn)

	if got := fn.Code[1]; got.Op != OpJump {
		t.Fatalf("jumpif on known one did not become a jump: %+v", got)
	}
	for _, in := range fn.Code {
		if in.Op == OpConst && in.Imm == 99 {
			t.Fatal("block under the taken branch survived")
		}
	}
	if len(fn.Code) != 4 {
		t.Fatalf("wrong shape after fold: %+v", fn.Code)
	}
}

func TestFoldZeroCondFallsThrough(t *testing.T) {
	// cond is 0: the branch never takes, the branch is deleted, and the
	// code after it still runs. Deleting the branch must NOT delete the
	// fallthrough path; that is the path execution takes.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 0},
		{Op: OpJumpIf, A: 0, Imm: 0},
		{Op: OpConst, Dst: 1, Imm: 7},
		{Op: OpStore, A: 1, Imm: 0},
	}, 2)
	fold(&Module{}, fn)

	found := false
	for _, in := range fn.Code {
		if in.Op == OpConst && in.Imm == 7 {
			found = true
		}
		if in.Op == OpJumpIf || in.Op == OpJump {
			t.Fatal("a branch survived on a known-zero condition")
		}
	}
	if !found {
		t.Fatal("fallthrough path was deleted with the branch")
	}
}

func TestFoldFloatPoolResult(t *testing.T) {
	m := &Module{Floats: []float64{2.5, 4}}
	fn := mkfn([]Instr{
		{Op: OpFConst, Dst: 0, Imm: 0},
		{Op: OpFConst, Dst: 1, Imm: 1},
		{Op: OpFMul, Dst: 2, A: 0, B: 1},
		{Op: OpStore, A: 2, Imm: 0},
	}, 3)
	fold(m, fn)

	got := fn.Code[2]
	if got.Op != OpFConst {
		t.Fatalf("fmul of pooled consts did not fold: %+v", got)
	}
	if m.Floats[got.Imm] != 10 {
		t.Fatalf("folded float value wrong: %v", m.Floats[got.Imm])
	}
}

func TestFoldFloatZeroNotPooled(t *testing.T) {
	// internFloat cannot tell -0.0 from +0.0, so a zero result never
	// folds: the sign would come out of whatever entry was pooled first.
	m := &Module{Floats: []float64{2.5, 2.5}}
	fn := mkfn([]Instr{
		{Op: OpFConst, Dst: 0, Imm: 0},
		{Op: OpFConst, Dst: 1, Imm: 1},
		{Op: OpFSub, Dst: 2, A: 0, B: 1},
		{Op: OpStore, A: 2, Imm: 0},
	}, 3)
	fold(m, fn)

	if got := fn.Code[2]; got.Op != OpFSub {
		t.Fatalf("zero float result folded and lost its sign: %+v", got)
	}
}

func TestDCERemovesUnusedChains(t *testing.T) {
	// The add is unused, so it dies; that leaves const r1 unused, so it
	// dies on the next sweep. r0 survives on the store.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 1},
		{Op: OpConst, Dst: 1, Imm: 2},
		{Op: OpAdd, Dst: 2, A: 0, B: 1},
		{Op: OpStore, A: 0, Imm: 0},
	}, 3)
	dce(fn)

	if len(fn.Code) != 2 {
		t.Fatalf("dead chain survived: %+v", fn.Code)
	}
	if fn.Code[0].Op != OpConst || fn.Code[0].Dst != 0 ||
		fn.Code[1].Op != OpStore || fn.Code[1].A != 0 {
		t.Fatalf("wrong instructions kept: %+v", fn.Code)
	}
}

func TestDCEKeepsTraps(t *testing.T) {
	// Nobody reads the quotient, but x/0 still has to stop the program.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 7},
		{Op: OpConst, Dst: 1, Imm: 0},
		{Op: OpDiv, Dst: 2, A: 0, B: 1},
	}, 3)
	dce(fn)

	if len(fn.Code) != 3 {
		t.Fatalf("dividing by zero was deleted: %+v", fn.Code)
	}
}

func TestDCEKeepsEffects(t *testing.T) {
	// A store stays; a call with an ignored result stays (it may print,
	// or write through a pointer); an unused load goes.
	fn := mkfn([]Instr{
		{Op: OpStr, Dst: 0, Imm: 0},
		{Op: OpStore, A: 0, Imm: 0},
		{Op: OpLoad, Dst: 1, Imm: 0},
		{Op: OpCall, Dst: 2, Sym: "f", Args: []Reg{0}},
	}, 3)
	dce(fn)

	if len(fn.Code) != 3 {
		t.Fatalf("wrong number of instructions kept: %+v", fn.Code)
	}
	sawLoad, sawCall := false, false
	for _, in := range fn.Code {
		switch in.Op {
		case OpLoad:
			sawLoad = true
		case OpCall:
			sawCall = true
		}
	}
	if sawLoad {
		t.Fatal("the dead load should have gone")
	}
	if !sawCall {
		t.Fatal("a call must survive even when its result is ignored")
	}
}

func TestForwardStoreToLoad(t *testing.T) {
	// The load of a just-stored value dies and later uses read the
	// stored register instead.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 5},
		{Op: OpStore, A: 0, Imm: 1},
		{Op: OpLoad, Dst: 1, Imm: 1},
		{Op: OpAdd, Dst: 2, A: 1, B: 1},
		{Op: OpStore, A: 2, Imm: 0},
	}, 3)
	forwardLoads(fn)

	if len(fn.Code) != 4 {
		t.Fatalf("the redundant load survived: %+v", fn.Code)
	}
	if got := fn.Code[2]; got.Op != OpAdd || got.A != 0 || got.B != 0 {
		t.Fatalf("uses were not renamed to the stored register: %+v", got)
	}
}

func TestForwardKeepsABitcast(t *testing.T) {
	// __frombits stores an int and loads the slot back as a float. That
	// load is the conversion, not a copy: forwarding it handed an int
	// register to movsd once the allocator put the int in r9, and
	// floor(3.7) came out as 0.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 4611686018427387904},
		{Op: OpStore, A: 0, Imm: 0},
		{Op: OpLoad, Dst: 1, Imm: 0},
		{Op: OpRet, A: 1},
	}, 2)
	fn.RegTypes = []vty{vInt, vFloat}
	forwardLoads(fn)

	if len(fn.Code) != 4 || fn.Code[2].Op != OpLoad || fn.Code[3].A != 1 {
		t.Fatalf("the reinterpreting load was forwarded: %+v", fn.Code)
	}
}

func TestForwardRestoresAtLabel(t *testing.T) {
	// A join can be reached by a path that never saw the store, so the
	// slot map resets and the load after the label stays. (The register
	// equivalence from before the label is unrelated to this and still
	// holds; see TestForwardRenamesAcrossLabel.)
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 1},
		{Op: OpStore, A: 0, Imm: 0},
		{Op: OpLabel, Imm: 0},
		{Op: OpLoad, Dst: 1, Imm: 0},
		{Op: OpStore, A: 1, Imm: 1},
	}, 2)
	forwardLoads(fn)

	if len(fn.Code) != 5 {
		t.Fatalf("a load across a join was forwarded: %+v", fn.Code)
	}
}

func TestForwardRenamesAcrossLabel(t *testing.T) {
	// The load dies, but one of its uses sits after a join. That use
	// must still rename: registers are single assignment, so the dead
	// load's number means the same word everywhere below it. Renaming
	// only up to the label would leave the later mention reading a
	// register nobody ever writes.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 7},
		{Op: OpStore, A: 0, Imm: 0},
		{Op: OpLoad, Dst: 1, Imm: 0},  // deleted, means v0
		{Op: OpLoadMem, A: 1, Dst: 2}, // before the label
		{Op: OpLabel, Imm: 0},
		{Op: OpLoadMem, A: 1, Dst: 3}, // after the label
	}, 4)
	forwardLoads(fn)

	if len(fn.Code) != 5 {
		t.Fatalf("the load survived: %+v", fn.Code)
	}
	if got := fn.Code[4]; got.A != 0 {
		t.Fatalf("the use after the label was not renamed: %+v", got)
	}
}

func TestForwardEscapedSlot(t *testing.T) {
	// Slot 2 escapes through its address, so the C callee may rewrite it
	// at any point after the call; nothing about it is trusted.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 7},
		{Op: OpStore, A: 0, Imm: 2},
		{Op: OpSlotAddr, Dst: 3, Imm: 2},
		{Op: OpLoad, Dst: 1, Imm: 2},
		{Op: OpCall, Sym: "frexp", Args: []Reg{0, 3}, Dst: 4},
		{Op: OpStore, A: 1, Imm: 1},
	}, 5)
	forwardLoads(fn)

	if len(fn.Code) != 6 {
		t.Fatalf("an escaped slot was forwarded: %+v", fn.Code)
	}
}

func TestForwardSecondStoreWins(t *testing.T) {
	// Each load forwards to the value stored most recently before it.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 1},
		{Op: OpStore, A: 0, Imm: 0},
		{Op: OpLoad, Dst: 1, Imm: 0},
		{Op: OpConst, Dst: 2, Imm: 2},
		{Op: OpStore, A: 2, Imm: 0},
		{Op: OpLoad, Dst: 3, Imm: 0},
		{Op: OpAdd, Dst: 4, A: 1, B: 3},
	}, 5)
	forwardLoads(fn)

	if len(fn.Code) != 5 {
		t.Fatalf("redundant loads survived: %+v", fn.Code)
	}
	if got := fn.Code[4]; got.Op != OpAdd || got.A != 0 || got.B != 2 {
		t.Fatalf("forwarding picked the wrong stores: %+v", got)
	}
}

func TestFoldKillSwitches(t *testing.T) {
	build := func() *Module {
		fn := mkfn([]Instr{
			{Op: OpConst, Dst: 0, Imm: 2},
			{Op: OpConst, Dst: 1, Imm: 3},
			{Op: OpAdd, Dst: 2, A: 0, B: 1},
			{Op: OpStore, A: 2, Imm: 0},
		}, 3)
		return &Module{Funcs: []*Func{fn}}
	}

	t.Setenv("SCANDIACUS_NOOPT", "1")
	m := build()
	Optimize(m)
	if m.Funcs[0].Code[2].Op != OpAdd {
		t.Fatal("SCANDIACUS_NOOPT did not disable folding")
	}

	t.Setenv("SCANDIACUS_NOOPT", "")
	t.Setenv("SCANDIACUS_NOFOLD", "1")
	m = build()
	Optimize(m)
	if m.Funcs[0].Code[2].Op != OpAdd {
		t.Fatal("SCANDIACUS_NOFOLD did not disable folding")
	}
}

func TestPackDisjointSpans(t *testing.T) {
	// Slot 6's whole life comes after slot 5's, so one index serves
	// both and the frame loses a slot.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 1},
		{Op: OpStore, A: 0, Imm: 5},
		{Op: OpLoad, Dst: 1, Imm: 5},
		{Op: OpConst, Dst: 2, Imm: 2},
		{Op: OpStore, A: 2, Imm: 6},
		{Op: OpLoad, Dst: 3, Imm: 6},
	}, 4)
	packSlots(fn)

	for _, i := range []int{1, 4} {
		if got := fn.Code[i]; got.Imm != 0 {
			t.Fatalf("disjoint slots did not share an index: %+v", got)
		}
	}
	if fn.NSlots != 1 {
		t.Fatalf("the frame did not shrink: %d", fn.NSlots)
	}
}

func TestPackOverlappingKept(t *testing.T) {
	// Slot 6 is written while slot 5 is still live (read below it), so
	// they cannot share an index.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 1},
		{Op: OpStore, A: 0, Imm: 5},
		{Op: OpConst, Dst: 1, Imm: 2},
		{Op: OpStore, A: 1, Imm: 6},
		{Op: OpLoad, Dst: 2, Imm: 5},
		{Op: OpStore, A: 2, Imm: 6},
	}, 3)
	packSlots(fn)

	if got := fn.Code[1]; got.Imm != 0 {
		t.Fatalf("first slot not numbered from zero: %+v", got)
	}
	if got := fn.Code[3]; got.Imm != 1 {
		t.Fatalf("overlapping slot shared an index: %+v", got)
	}
	if fn.NSlots != 2 {
		t.Fatalf("wrong slot count: %d", fn.NSlots)
	}
}

func TestPackEscapedPrivate(t *testing.T) {
	// Slot 4 escapes through its address after slot 8 is dead. The
	// spans do not meet, but a C callee may write slot 4 whenever it
	// likes, so sharing slot 8's index would let that write land on an
	// unrelated value.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 7},
		{Op: OpStore, A: 0, Imm: 8},
		{Op: OpLoad, Dst: 1, Imm: 8},
		{Op: OpSlotAddr, Dst: 2, Imm: 4},
	}, 3)
	packSlots(fn)

	// The escaped slot counts as live everywhere, so it is placed
	// first and nothing else may join it.
	if got := fn.Code[3]; got.Imm != 0 {
		t.Fatalf("an escaped slot lost its private index: %+v", got)
	}
	if got := fn.Code[1]; got.Imm != 1 {
		t.Fatalf("an ordinary slot joined an escaped one: %+v", got)
	}
	if fn.NSlots != 2 {
		t.Fatalf("wrong slot count: %d", fn.NSlots)
	}
}

func TestPackEnvKeepsZero(t *testing.T) {
	// The prologue parks the environment pointer in slot zero by
	// number, outside the instruction stream. Another slot being
	// mentioned first must not take zero away from it.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 1},
		{Op: OpStore, A: 0, Imm: 2},
		{Op: OpLoad, Dst: 1, Imm: 0},
	}, 2)
	fn.Env = true
	packSlots(fn)

	if got := fn.Code[2]; got.Imm != 0 {
		t.Fatalf("the environment slot moved: %+v", got)
	}
	if got := fn.Code[1]; got.Imm != 1 {
		t.Fatalf("the other slot was not kept off zero: %+v", got)
	}
	if fn.NSlots != 2 {
		t.Fatalf("wrong slot count: %d", fn.NSlots)
	}
}

func TestPackRemapsAllSlotOps(t *testing.T) {
	// Every operation that names a slot by number must follow the
	// renumbering: stores, loads, byte stores through a slot-held
	// value, and addresses taken for out-parameters.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 65},
		{Op: OpStore, A: 0, Imm: 9},
		{Op: OpConst, Dst: 1, Imm: 66},
		{Op: OpStore, A: 1, Imm: 2},
		{Op: OpStoreByte, A: 5, B: NoReg, Imm: 9},
		{Op: OpSlotAddr, Dst: 6, Imm: 2},
		{Op: OpLoad, Dst: 7, Imm: 9},
	}, 8)
	packSlots(fn)

	// Slot 2's address escapes at instruction 5, which makes it live
	// everywhere and so first in line; slot 9 takes the second index.
	want := map[int]int64{1: 1, 3: 0, 4: 1, 5: 0, 6: 1}
	for i, w := range want {
		if got := fn.Code[i]; got.Imm != w {
			t.Fatalf("instruction %d kept the old slot number: %+v", i, got)
		}
	}
}

func TestPackBackedge(t *testing.T) {
	// Slot 3 is written before the loop and read at the top of it;
	// slot 5 is written inside the loop body. On the page their
	// mentions never meet, but each lap round the back edge runs the
	// write to slot 5 before the next read of slot 3. Sharing an index
	// would hand every lap a clobbered value; liveness around the edge
	// is what tells them apart.
	fn := mkfn([]Instr{
		{Op: OpConst, Dst: 0, Imm: 1},
		{Op: OpStore, A: 0, Imm: 3},
		{Op: OpLabel, Imm: 0},
		{Op: OpLoad, Dst: 1, Imm: 3},
		{Op: OpConst, Dst: 2, Imm: 9},
		{Op: OpStore, A: 2, Imm: 5},
		{Op: OpJump, Imm: 0},
	}, 3)
	packSlots(fn)

	if got := fn.Code[5]; got.Imm == fn.Code[1].Imm {
		t.Fatalf("a loop-carried slot shared an index: %+v", got)
	}
	if fn.NSlots != 2 {
		t.Fatalf("wrong slot count: %d", fn.NSlots)
	}
}

func TestPackSwitch(t *testing.T) {
	build := func() *Module {
		fn := mkfn([]Instr{
			{Op: OpConst, Dst: 0, Imm: 1},
			{Op: OpStore, A: 0, Imm: 5},
			{Op: OpLoad, Dst: 1, Imm: 5},
			{Op: OpConst, Dst: 2, Imm: 2},
			{Op: OpStore, A: 2, Imm: 6},
			{Op: OpLoad, Dst: 3, Imm: 6},
		}, 4)
		fn.NSlots = 10
		return &Module{Funcs: []*Func{fn}}
	}

	t.Setenv("SCANDIACUS_NOSLOTPACK", "1")
	m := build()
	Optimize(m)
	if m.Funcs[0].NSlots != 10 || m.Funcs[0].Code[1].Imm != 5 {
		t.Fatal("SCANDIACUS_NOSLOTPACK did not disable packing")
	}

	t.Setenv("SCANDIACUS_NOSLOTPACK", "")
	t.Setenv("SCANDIACUS_NOOPT", "1")
	m = build()
	Optimize(m)
	if m.Funcs[0].NSlots != 10 {
		t.Fatal("SCANDIACUS_NOOPT did not disable packing")
	}
}
