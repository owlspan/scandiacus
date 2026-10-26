package main

import "testing"

// The multiply is moved past the load that separates it from its add,
// so the emitter sees the pair together; a multiply with a second
// reader, or one that is the left side of a subtract, stays put.
func TestSinkFMul(t *testing.T) {
	fn := mkfn([]Instr{
		{Op: OpLoad, Dst: 0, Imm: 1},
		{Op: OpLoad, Dst: 1, Imm: 2},
		{Op: OpFMul, Dst: 2, A: 0, B: 1},
		{Op: OpLoad, Dst: 3, Imm: 3},
		{Op: OpFAdd, Dst: 4, A: 3, B: 2},
		{Op: OpFMul, Dst: 5, A: 0, B: 1},
		{Op: OpLoad, Dst: 6, Imm: 3},
		{Op: OpFSub, Dst: 7, A: 5, B: 6},
		{Op: OpStore, A: 4, Imm: 4},
		{Op: OpStore, A: 7, Imm: 5},
	}, 8)
	sinkFMul(fn)
	if fn.Code[3].Op != OpFMul || fn.Code[4].Op != OpFAdd {
		t.Fatalf("multiply not moved next to its add: %v", fn.Code[2:5])
	}
	if fn.Code[5].Op != OpFMul || fn.Code[6].Op != OpLoad {
		t.Fatalf("a*b - c should not be fused: %v", fn.Code[5:8])
	}
	if !fmaUser(Instr{Op: OpFSub, A: 9, B: 2}, 2) || fmaUser(Instr{Op: OpFSub, A: 2, B: 9}, 2) {
		t.Fatal("fmaUser: only the right side of a subtract fuses")
	}
}
