package main

import "testing"

// The allocator's rules each exist because breaking one breaks
// something quiet, so each gets a test that breaks it on purpose and
// checks the allocator refused.

// rafn builds a function whose virtual registers carry exactly the
// types given, which is all the allocator reads. Unused operand slots
// say NoReg: register zero is a real value, and leaving the field at
// its zero value would read as a mention of it.
func rafn(types []vty, code []Instr) *Func {
	return &Func{Name: "t", NRegs: len(types), RegTypes: types, Code: code}
}

func TestRegAllocPoolsStraightLineTemps(t *testing.T) {
	fn := rafn([]vty{vInt, vInt, vInt}, []Instr{
		{Op: OpConst, Dst: 0, A: NoReg, B: NoReg, Imm: 1},
		{Op: OpConst, Dst: 1, A: NoReg, B: NoReg, Imm: 2},
		{Op: OpAdd, Dst: 2, A: 0, B: 1},
	})
	homes := allocateRegs(fn)
	if homes == nil || homes[0] == "" || homes[1] == "" {
		t.Fatalf("straight-line temps got no registers: %v", homes)
	}
	if homes[0] == homes[1] {
		t.Fatalf("two simultaneously live values share %s", homes[0])
	}
}

func TestRegAllocKeepsPointersSlotted(t *testing.T) {
	// A string alive across a call keeps its slot: the call can
	// collect, the collector finds roots by scanning slots, and a
	// register is a word the scan never sees.
	fn := rafn([]vty{vStr, vInt, vInt}, []Instr{
		{Op: OpStr, Dst: 0, A: NoReg, B: NoReg, Imm: 0},
		{Op: OpConst, Dst: 1, A: NoReg, B: NoReg, Imm: 7},
		{Op: OpCall, Dst: NoReg, A: NoReg, B: NoReg, Sym: "f"},
		{Op: OpPrintStr, Dst: NoReg, A: 0, B: NoReg},
		{Op: OpNeg, Dst: 2, A: 1, B: NoReg},
	})
	homes := allocateRegs(fn)
	if _, ok := homes[0]; ok {
		t.Fatalf("a string alive across a call got a register: %v", homes)
	}
}

func TestRegAllocPoolsPointersBetweenCalls(t *testing.T) {
	// With no call in its span nothing can collect while it is in the
	// register, so a pointer pools like anything else.
	fn := rafn([]vty{vStr, vInt, vInt}, []Instr{
		{Op: OpStr, Dst: 0, A: NoReg, B: NoReg, Imm: 0},
		{Op: OpLoadMem, Dst: 1, A: 0, B: NoReg, Imm: 0},
		{Op: OpNeg, Dst: 2, A: 1, B: NoReg},
	})
	homes := allocateRegs(fn)
	if homes == nil || homes[0] == "" || homes[1] == "" {
		t.Fatalf("values with no call in their span should pool: %v", homes)
	}
}

func TestRegAllocFloatsKeepTheirSlots(t *testing.T) {
	// Reading a pooled float sometimes means getting its raw bits into
	// a general register, and that move is movq, which the byte writer
	// does not encode. Until it does, floats stay in their slots.
	fn := rafn([]vty{vFloat, vFloat}, []Instr{
		{Op: OpFConst, Dst: 0, A: NoReg, B: NoReg, Imm: 0},
		{Op: OpFNeg, Dst: 1, A: 0, B: NoReg},
	})
	if homes := allocateRegs(fn); len(homes) != 0 {
		t.Fatalf("a float was pooled: %v", homes)
	}
}

func TestRegAllocNoValueCrossesACall(t *testing.T) {
	// r0 is born before the call and read after it; r2 is what the call
	// returned, written after every volatile register has been taken.
	// Only r3, whose whole life fits in the gap after the call, may
	// take a register.
	fn := rafn([]vty{vInt, vInt, vInt, vInt}, []Instr{
		{Op: OpConst, Dst: 0, A: NoReg, B: NoReg, Imm: 1},
		{Op: OpCall, Dst: 2, A: NoReg, B: NoReg, Sym: "f"},
		{Op: OpAdd, Dst: 3, A: 0, B: 2},
	})
	homes := allocateRegs(fn)
	for _, r := range []Reg{0, 2} {
		if _, ok := homes[r]; ok {
			t.Fatalf("register %d crossed or came out of a call: %v", r, homes)
		}
	}
	if _, ok := homes[3]; !ok {
		t.Fatalf("a value between two barriers should pool: %v", homes)
	}
}

func TestRegAllocNoSpanCrossesALabel(t *testing.T) {
	// r0 dies textually at the add, but the jump back to .L0 runs the
	// add again on the next lap, after some other value may have taken
	// the register. A span with a label inside it cannot be re-entered
	// safely; r1, defined and read entirely inside the loop body, is
	// rewritten every lap before it is read and pools fine.
	fn := rafn([]vty{vInt, vInt, vInt}, []Instr{
		{Op: OpConst, Dst: 0, A: NoReg, B: NoReg, Imm: 1},
		{Op: OpLabel, Dst: NoReg, A: NoReg, B: NoReg, Imm: 0},
		{Op: OpConst, Dst: 1, A: NoReg, B: NoReg, Imm: 2},
		{Op: OpAdd, Dst: 2, A: 0, B: 1},
		{Op: OpJump, Dst: NoReg, A: NoReg, B: NoReg, Imm: 0},
	})
	homes := allocateRegs(fn)
	if _, ok := homes[0]; ok {
		t.Fatalf("a span crossing a label was pooled: %v", homes)
	}
	if _, ok := homes[1]; !ok {
		t.Fatalf("a value whose life fits one lap should pool: %v", homes)
	}
}

func TestRegAllocLeavesParametersAlone(t *testing.T) {
	fn := &Func{
		Name: "t", NParams: 1, NRegs: 1, RegTypes: []vty{vInt},
		Code: []Instr{{Op: OpParam, Dst: 0, A: NoReg, B: NoReg, Imm: 0}},
	}
	if homes := allocateRegs(fn); len(homes) != 0 {
		t.Fatalf("a parameter was pooled: %v", homes)
	}
}

func TestRegAllocExhaustionSpillsWithoutEvicting(t *testing.T) {
	// Five integers alive together over four integer registers: four
	// pool and the fifth keeps its slot. Nothing already holding a
	// register is evicted over the fifth. The five neg results born
	// afterwards fit nowhere at birth either, but by then the early
	// temps are dead, so the same four registers serve them too - the
	// four whose operands die there take those operands' registers, and
	// the fifth, whose operand never had one, a register freed by then.
	// A register changing hands between values that never overlap is
	// the entire point of the pass, not a collision.
	var code []Instr
	for i := 0; i < 5; i++ {
		code = append(code, Instr{Op: OpConst, Dst: Reg(i),
			A: NoReg, B: NoReg, Imm: int64(i)})
	}
	for i := 0; i < 5; i++ {
		code = append(code, Instr{Op: OpNeg, Dst: Reg(5 + i),
			A: Reg(i), B: NoReg})
	}
	fn := rafn([]vty{vInt, vInt, vInt, vInt, vInt,
		vInt, vInt, vInt, vInt, vInt}, code)

	homes := allocateRegs(fn)
	count := 0
	for r := range homes {
		if r < 5 {
			count++
		}
	}
	if count != 4 {
		t.Fatalf("pooled %d of five simultaneous values, want 4: %v",
			count, homes)
	}
	if len(homes) != 9 {
		t.Fatalf("dead temps did not hand their registers on: %v", homes)
	}
}

func TestRegAllocReusesADeadRegister(t *testing.T) {
	// r0 dies at the add; r3 is born after it. One machine register can
	// serve both, which is the entire economy of the pass.
	fn := rafn([]vty{vInt, vInt, vInt, vInt}, []Instr{
		{Op: OpConst, Dst: 0, A: NoReg, B: NoReg, Imm: 1},
		{Op: OpConst, Dst: 1, A: NoReg, B: NoReg, Imm: 2},
		{Op: OpAdd, Dst: 2, A: 0, B: 1},
		{Op: OpConst, Dst: 3, A: NoReg, B: NoReg, Imm: 3},
	})
	homes := allocateRegs(fn)
	if homes == nil || homes[3] != homes[0] {
		t.Fatalf("r3 did not reuse r0's register: %v", homes)
	}
}

func TestRegAllocSkipsUnsuitableFunctions(t *testing.T) {
	if homes := allocateRegs(&Func{Name: "empty"}); homes != nil {
		t.Fatalf("a function with no registers got homes: %v", homes)
	}
	// Types missing for some registers means the lowerer did not finish
	// or this Func was built by hand. Refuse rather than guess.
	if homes := allocateRegs(&Func{Name: "short", NRegs: 3}); homes != nil {
		t.Fatalf("untyped registers got homes: %v", homes)
	}
}

func TestLoadWindowArgsOrdering(t *testing.T) {
	// The scheduler's shapes. Sources are named by hand: the point is
	// what order moves come out in when arguments already sit in the
	// registers the window wants to write.
	emit := func(homes map[Reg]string, args ...Reg) string {
		types := make([]vty, len(args))
		for i := range types {
			types[i] = vInt // no float detour on any of these paths
		}
		e := &Emitter{f: &Func{Name: "t", NSlots: 2}, homes: homes}
		e.loadWindowArgs(Instr{Args: args, ArgTypes: types,
			Dst: NoReg, A: NoReg, B: NoReg})
		return e.b.String()
	}

	// A chain: each window register holds what the next position wants.
	// Every source must be read out before its home is overwritten.
	got := emit(
		map[Reg]string{10: "r8", 11: "r9", 12: "r10", 13: "r11"},
		10, 11, 12, 13,
	)
	want := "    mov rcx, r8\n" +
		"    mov rdx, r9\n" +
		"    mov r8, r10\n" +
		"    mov r9, r11\n"
	if got != want {
		t.Fatalf("chain ordered wrong:\ngot:\n%s\nwant:\n%s", got, want)
	}

	// A swap: r8 holds what position three wants and r9 holds what
	// position two wants. No order of plain moves survives; only xchg.
	got = emit(
		map[Reg]string{10: "r10", 11: "r11", 12: "r9", 13: "r8"},
		10, 11, 12, 13,
	)
	want = "    mov rcx, r10\n" +
		"    mov rdx, r11\n" +
		"    xchg r8, r9\n"
	if got != want {
		t.Fatalf("swap did not become xchg:\ngot:\n%s\nwant:\n%s", got, want)
	}

	// Nothing risky: the fast path keeps the original shape untouched.
	got = emit(nil, 20, 21)
	ref := &Emitter{f: &Func{Name: "t", NSlots: 2}}
	want = "    mov rcx, " + ref.regAddr(20) + "\n" +
		"    mov rdx, " + ref.regAddr(21) + "\n"
	if got != want {
		t.Fatalf("fast path changed shape:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
