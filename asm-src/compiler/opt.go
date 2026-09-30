package main

// The IR optimizer. Each pass is a function over one Func, checked by
// its own environment variable so a suspect pass can be turned off
// without touching the rest:
//
//	VEYL_NOFOLD      constant and branch folding
//	VEYL_NODCE       dead code elimination
//	VEYL_NOFORWARD   redundant load elimination
//	VEYL_NOSLOTPACK  frame slot packing
//
// VEYL_NOOPT=1 turns every pass off and leaves the output identical to
// what the unoptimized compiler emits.

import (
	"math"
	"os"
	"sort"
)

func envOff(name string) bool { return os.Getenv(name) == "1" }

// Optimize rewrites every function of the module in place. It runs
// after Lower has reported no errors, so the IR it sees always came
// from a program that checked clean.
func Optimize(m *Module) {
	if envOff("VEYL_NOOPT") {
		return
	}
	for _, fn := range m.Funcs {
		if !envOff("VEYL_NOFOLD") {
			fold(m, fn)
		}
		if !envOff("VEYL_NODCE") {
			dce(fn)
		}
		if !envOff("VEYL_NOFORWARD") {
			forwardLoads(fn)
			if !envOff("VEYL_NODCE") {
				// Sweep once more: forwarding just turned loads into
				// plain register mentions, which can leave the loaded
				// constants they fed with no remaining readers.
				dce(fn)
			}
		}
		if !envOff("VEYL_NOPOLLS") {
			dropPolls(fn)
		}
		if !envOff("VEYL_NOSLOTPACK") {
			packSlots(fn)
		}
	}
}

// ---- constant and branch folding ----

// fold evaluates operations whose operands are constants, folds
// branches on a known condition, and then drops everything no path
// reaches.
//
// Virtual registers are single assignment, so a constant definition
// holds at every mention of its register no matter how control flow
// runs between them. No dominance analysis is needed.
//
// Division folds only when the divisor is a nonzero constant other
// than minus one over the most negative value: those two trap at
// runtime, and a folded program must keep trapping exactly there.
func fold(m *Module, fn *Func) {
	for round := 0; round < 4; round++ {
		iconst := map[Reg]int64{}
		fconst := map[Reg]float64{}
		for _, in := range fn.Code {
			switch in.Op {
			case OpConst:
				iconst[in.Dst] = in.Imm
			case OpFConst:
				if int(in.Imm) < len(m.Floats) {
					fconst[in.Dst] = m.Floats[in.Imm]
				}
			}
		}

		changed := false
		out := make([]Instr, 0, len(fn.Code))
		for _, in := range fn.Code {
			if newin, ok := foldInstr(m, in, iconst, fconst); ok {
				in = newin
				changed = true
				switch in.Op {
				case OpConst:
					iconst[in.Dst] = in.Imm
				case OpFConst:
					fconst[in.Dst] = m.Floats[in.Imm]
				}
			}
			switch in.Op {
			case OpJumpIf:
				if c, ok := iconst[in.A]; ok {
					changed = true
					if c != 0 {
						in = Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: in.Imm}
					} else {
						continue // known true: never taken
					}
				}
			case OpJumpNot:
				if c, ok := iconst[in.A]; ok {
					changed = true
					if c == 0 {
						in = Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: in.Imm}
					} else {
						continue // known false: never taken
					}
				}
			}
			out = append(out, in)
		}
		fn.Code = out
		if !changed {
			break
		}
	}
	dropUnreachable(fn)
}

// foldInstr rewrites one instruction to a constant when it can.
// Integer arithmetic uses Go's own semantics: signed overflow wraps,
// shifts mask their count with 63 to match what the hardware does.
// Float results are pooled through internFloat like any literal, but
// folding stops for NaN, infinity and zero results: the first two have
// bit patterns the hardware would not produce, and internFloat cannot
// tell negative from positive zero because Go's == says they are equal.
func foldInstr(m *Module, in Instr, ic map[Reg]int64, fc map[Reg]float64) (Instr, bool) {
	a, aok := ic[in.A]
	b, bok := ic[in.B]

	toInt := func(v int64) (Instr, bool) {
		// Unused operands say NoReg, as every hand-written emit site
		// does: register zero is a real value, and passes that scan
		// operands must be able to tell an empty slot from it.
		return Instr{Op: OpConst, Dst: in.Dst, A: NoReg, B: NoReg,
			Imm: v}, true
	}

	if out, ok := foldFixed(m, in, ic, fc); ok {
		return out, true
	}

	switch in.Op {
	case OpNeg:
		if aok {
			return toInt(-a)
		}
	case OpBNot:
		if aok {
			return toInt(^a)
		}
	case OpNot:
		if aok {
			if a == 0 {
				return toInt(1)
			}
			return toInt(0)
		}
	case OpAdd:
		if aok && bok {
			return toInt(a + b)
		}
	case OpSub:
		if aok && bok {
			return toInt(a - b)
		}
	case OpMul:
		if aok && bok {
			return toInt(a * b)
		}
	case OpDiv:
		if aok && bok && b != 0 && !(a == math.MinInt64 && b == -1) {
			return toInt(a / b)
		}
	case OpMod:
		if aok && bok && b != 0 && !(a == math.MinInt64 && b == -1) {
			return toInt(a % b)
		}
	case OpBAnd:
		if aok && bok {
			return toInt(a & b)
		}
	case OpBOr:
		if aok && bok {
			return toInt(a | b)
		}
	case OpBXor:
		if aok && bok {
			return toInt(a ^ b)
		}
	case OpShl:
		if aok && bok {
			return toInt(a << (uint64(b) & 63))
		}
	case OpShr:
		if aok && bok {
			return toInt(a >> (uint64(b) & 63)) // arithmetic, like sar
		}
	case OpEq:
		if aok && bok {
			if a == b {
				return toInt(1)
			}
			return toInt(0)
		}
	case OpNe:
		if aok && bok {
			if a != b {
				return toInt(1)
			}
			return toInt(0)
		}
	case OpLt:
		if aok && bok {
			if a < b {
				return toInt(1)
			}
			return toInt(0)
		}
	case OpLe:
		if aok && bok {
			if a <= b {
				return toInt(1)
			}
			return toInt(0)
		}
	case OpGt:
		if aok && bok {
			if a > b {
				return toInt(1)
			}
			return toInt(0)
		}
	case OpGe:
		if aok && bok {
			if a >= b {
				return toInt(1)
			}
			return toInt(0)
		}
	case OpIntToFloat:
		if aok {
			return Instr{Op: OpFConst, Dst: in.Dst, A: NoReg, B: NoReg,
				Imm: m.internFloat(float64(a))}, true
		}
	case OpFAdd:
		if fa, faok := fc[in.A]; faok {
			if fb, fbok := fc[in.B]; fbok {
				if r := fa + fb; !isNaNInf(r) && r != 0 {
					return Instr{Op: OpFConst, Dst: in.Dst, A: NoReg, B: NoReg,
						Imm: m.internFloat(r)}, true
				}
			}
		}
	case OpFSub:
		if fa, faok := fc[in.A]; faok {
			if fb, fbok := fc[in.B]; fbok {
				if r := fa - fb; !isNaNInf(r) && r != 0 {
					return Instr{Op: OpFConst, Dst: in.Dst, A: NoReg, B: NoReg,
						Imm: m.internFloat(r)}, true
				}
			}
		}
	case OpFMul:
		if fa, faok := fc[in.A]; faok {
			if fb, fbok := fc[in.B]; fbok {
				if r := fa * fb; !isNaNInf(r) && r != 0 {
					return Instr{Op: OpFConst, Dst: in.Dst, A: NoReg, B: NoReg,
						Imm: m.internFloat(r)}, true
				}
			}
		}
	}
	return Instr{}, false
}

func isNaNInf(v float64) bool {
	return math.IsNaN(v) || math.IsInf(v, 0)
}

// dropUnreachable keeps only the instructions some execution path can
// reach, following the jumps as they now stand. A label nothing jumps
// to any more disappears with its block.
func dropUnreachable(fn *Func) {
	n := len(fn.Code)
	if n == 0 {
		return
	}
	labelAt := map[int64]int{}
	for i, in := range fn.Code {
		if in.Op == OpLabel {
			labelAt[in.Imm] = i
		}
	}

	reach := make([]bool, n)
	work := []int{0}
	for len(work) > 0 {
		i := work[len(work)-1]
		work = work[:len(work)-1]
		for i < n && !reach[i] {
			reach[i] = true
			switch fn.Code[i].Op {
			case OpLabel:
				i++
			case OpJump:
				work = append(work, labelAt[fn.Code[i].Imm])
				i = n
			case OpJumpIf, OpJumpNot:
				work = append(work, labelAt[fn.Code[i].Imm])
				i++
			case OpRet, OpMustFail, OpBoundsFail:
				i = n
			default:
				i++
			}
		}
	}

	code := make([]Instr, 0, n)
	for i, in := range fn.Code {
		if reach[i] {
			code = append(code, in)
		}
	}
	fn.Code = code
}

// ---- dead code elimination ----

// dce removes pure definitions whose result is never used, down to a
// fixed point: killing one definition can make the one feeding it dead.
// Anything that traps, stores, prints, allocates or calls stays even
// with an ignored result, because removing it would change what the
// program does. Division and modulo stay too - they trap on a zero
// divisor whether or not anyone reads the quotient.
func dce(fn *Func) {
	for {
		used := map[Reg]bool{}
		mark := func(r Reg) {
			if r != NoReg {
				used[r] = true
			}
		}
		for _, in := range fn.Code {
			mark(in.A)
			mark(in.B)
			for _, arg := range in.Args {
				mark(arg)
			}
		}

		kept := fn.Code[:0]
		changed := false
		for _, in := range fn.Code {
			if in.Dst != NoReg && !used[in.Dst] && deadOK(in.Op) {
				changed = true
				continue
			}
			kept = append(kept, in)
		}
		fn.Code = kept
		if !changed {
			return
		}
	}
}

// deadOK says whether the operation is safe to delete when its result
// is unused. One list, read as "everything else has an effect".
func deadOK(op Op) bool {
	switch op {
	case OpConst, OpStr, OpLoad, OpFConst,
		OpAdd, OpSub, OpMul, OpNeg,
		OpFAdd, OpFSub, OpFMul, OpFDiv, OpFNeg,
		OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe,
		OpIntToFloat, OpFloatToInt, OpSqrt, OpFMod,
		OpBAnd, OpBOr, OpBXor, OpBNot, OpShl, OpShr,
		OpEq, OpNe, OpLt, OpLe, OpGt, OpGe, OpNot,
		OpExt, OpF32Round, OpIntToF32, OpU64ToFloat, OpU64ToF32, OpFloatToU64,
		OpShrU, OpLtU, OpLeU, OpGtU, OpGeU,
		OpIndexAddr, OpLoadByte, OpPeek,
		OpStackPtr, OpGlobalAddr, OpSymAddr, OpSlotAddr:
		return true
	}
	return false
}

// ---- redundant load elimination ----

// forwardLoads deletes a load of a slot whose value some register is
// already known to hold, renaming every later use of the loaded
// register to that one.
//
// The two halves of the bookkeeping have different lifetimes, and the
// difference is the whole pass:
//
//   - Which register holds a slot's value is runtime state. Two paths
//     into a join can store different things, so slotVal resets at
//     every label and nothing is forwarded across one.
//   - What a register means is a static fact. Registers are single
//     assignment, so a deleted load leaves its number with exactly one
//     meaning for the rest of the function, and every mention of it -
//     including mentions after joins and around back-edges, which its
//     definition always dominates - renames to the same word. sameAs
//     therefore lives until the function ends. Wiping it at a label
//     strands the later mentions on a register whose defining load no
//     longer exists, and they read whatever the frame happened to
//     hold. That bug shipped for exactly one evening before this
//     paragraph did.
//
// Equivalences survive calls. Frame slots are not addressable from
// Veyl, boxes live on the heap rather than in the frame, and today
// every register value physically lives in its own stack slot across a
// call. The one exception is a slot handed out by OpSlotAddr: a C
// function with an out-parameter writes it behind our back, anywhere
// in the function, so an escaped slot never joins the map at all.
func forwardLoads(fn *Func) {
	escaped := map[int64]bool{}
	for _, in := range fn.Code {
		if in.Op == OpSlotAddr {
			escaped[in.Imm] = true
		}
	}

	slotVal := map[int64]Reg{} // slot -> a register holding its value
	sameAs := map[Reg]Reg{}    // deleted load dst -> register to use instead
	follow := func(r Reg) Reg {
		for {
			next, ok := sameAs[r]
			if !ok {
				return r
			}
			r = next
		}
	}

	out := make([]Instr, 0, len(fn.Code))
	for _, in := range fn.Code {
		if in.A != NoReg {
			in.A = follow(in.A)
		}
		if in.B != NoReg {
			in.B = follow(in.B)
		}
		for i := range in.Args {
			in.Args[i] = follow(in.Args[i])
		}

		switch in.Op {
		case OpLabel:
			slotVal = map[int64]Reg{}
		case OpStore:
			slotVal[in.Imm] = in.A
		case OpLoad:
			if v, ok := slotVal[in.Imm]; ok && !escaped[in.Imm] && sameClass(fn, v, in.Dst) {
				sameAs[in.Dst] = v
				continue // the load dies; later uses read v instead
			}
			slotVal[in.Imm] = in.Dst
		}
		out = append(out, in)
	}
	fn.Code = out
}

// sameClass reports whether two registers live in the same register
// file. A store of an int and a load of the same slot as a float is how
// __bits and __frombits reinterpret a value, and that load is the
// conversion: forwarding it would hand an integer register to an
// instruction that reads an xmm one. Through a stack slot that happened
// to work, since memory has no register file, and it stopped working the
// day the register allocator gave the int a machine register.
//
// IR built by hand in a test carries no types at all, and is all one
// class.
func sameClass(fn *Func, a, b Reg) bool {
	if len(fn.RegTypes) == 0 {
		return true
	}
	if int(a) >= len(fn.RegTypes) || int(b) >= len(fn.RegTypes) || a < 0 || b < 0 {
		return false
	}
	return (fn.RegTypes[a].k == kFloat) == (fn.RegTypes[b].k == kFloat)
}

// ---- slot packing ----

// packSlots renumbers frame slots so that values whose live ranges do
// not meet share one. The lowerer never reuses a slot - "nothing can
// alias", and for a compiler with no type-driven liveness that is the
// safe call - but by the time optimization has run, every read and
// write of a slot is an explicit instruction, so liveness is known.
//
// Which slots may share is decided by real liveness, not by the line
// positions of the mentions. The first cut took each slot's range as
// the stretch from its first mention to its last and shared out
// anything whose stretches did not meet. That miscompiles any loop
// that carries a value backwards: a slot written below the join and
// read above it never meet on the page, but on the next lap round the
// back edge the write lands before the read. Backward liveness walks
// around the back edge and puts the two in the same range, and ranges
// that meet never share.
//
// Two kinds of slot never share anything. One whose address escapes
// through OpSlotAddr can be written by a C callee at any point after
// the address is taken, so it counts as live everywhere. And a
// closure's environment pointer is parked in slot zero by the
// prologue, named there by number rather than through any instruction
// this pass sees, so that slot keeps the number zero itself.
func packSlots(fn *Func) {
	n := len(fn.Code)

	// ---- basic blocks ----

	labelAt := map[int64]int{}
	isLead := make([]bool, n)
	if n > 0 {
		isLead[0] = true
	}
	for i := range fn.Code {
		switch fn.Code[i].Op {
		case OpLabel:
			labelAt[fn.Code[i].Imm] = i
			isLead[i] = true
		case OpJump, OpJumpIf, OpJumpNot, OpRet, OpMustFail, OpBoundsFail:
			if i+1 < n {
				isLead[i+1] = true
			}
		}
	}

	type blk struct {
		start, end int
		succ       []int
	}
	blocks := []blk{}
	idOf := make([]int, n)
	for i := 0; i < n; i++ {
		if !isLead[i] {
			continue
		}
		id := len(blocks)
		end := i
		for end+1 < n && !isLead[end+1] {
			end++
		}
		blocks = append(blocks, blk{start: i, end: end})
		for j := i; j <= end; j++ {
			idOf[j] = id
		}
	}
	for b := range blocks {
		last := blocks[b].end
		switch fn.Code[last].Op {
		case OpJump:
			blocks[b].succ = []int{idOf[labelAt[fn.Code[last].Imm]]}
		case OpJumpIf, OpJumpNot:
			blocks[b].succ = []int{b + 1, idOf[labelAt[fn.Code[last].Imm]]}
		case OpRet, OpMustFail, OpBoundsFail:
			// nothing follows
		default:
			if last+1 < n {
				blocks[b].succ = []int{b + 1}
			}
		}
	}

	// ---- backward liveness to a fixed point ----

	touches := func(in *Instr) int64 {
		switch in.Op {
		case OpLoad, OpStore, OpSlotAddr, OpStoreByte:
			return in.Imm
		}
		return -1
	}

	liveIn := make([]map[int64]bool, len(blocks))
	liveOut := make([]map[int64]bool, len(blocks))
	for b := range blocks {
		liveIn[b] = map[int64]bool{}
		liveOut[b] = map[int64]bool{}
	}
	sameSet := func(a, b map[int64]bool) bool {
		if len(a) != len(b) {
			return false
		}
		for k := range a {
			if !b[k] {
				return false
			}
		}
		return true
	}
	for changed := true; changed; {
		changed = false
		for b := len(blocks) - 1; b >= 0; b-- {
			out := map[int64]bool{}
			for _, s := range blocks[b].succ {
				for v := range liveIn[s] {
					out[v] = true
				}
			}
			live := make(map[int64]bool, len(out))
			for v := range out {
				live[v] = true
			}
			for i := blocks[b].end; i >= blocks[b].start; i-- {
				t := touches(&fn.Code[i])
				if t < 0 {
					continue
				}
				if fn.Code[i].Op == OpStore {
					delete(live, t)
				} else { // load, byte store, address taken: a read
					live[t] = true
				}
			}
			if !sameSet(live, liveIn[b]) || !sameSet(out, liveOut[b]) {
				liveIn[b], liveOut[b] = live, out
				changed = true
			}
		}
	}

	// ---- the range each slot stays live over ----
	//
	// Between two instructions that name slots, the live set cannot
	// change, so the walk below marks one stretch per gap rather than
	// one mark per instruction. Ends only, since a range that covers
	// its gap's ends covers the gap.

	gmin := map[int64]int{}
	gmax := map[int64]int{}
	note := func(s int64, a, z int) {
		if old, ok := gmin[s]; !ok || a < old {
			gmin[s] = a
		}
		if old, ok := gmax[s]; !ok || z > old {
			gmax[s] = z
		}
	}

	for b := range blocks {
		live := make(map[int64]bool, len(liveOut[b]))
		for v := range liveOut[b] {
			live[v] = true
		}
		start := blocks[b].start
		i := blocks[b].end
		for i >= start {
			j := i
			for j >= start && touches(&fn.Code[j]) < 0 {
				j--
			}
			if len(live) > 0 {
				lo := j + 1
				if lo < start {
					lo = start
				}
				if lo <= i {
					for s := range live {
						note(s, lo, i)
					}
				}
			}
			if j < start {
				break
			}
			t := touches(&fn.Code[j])
			note(t, j, j)
			if fn.Code[j].Op == OpStore {
				delete(live, t)
			} else {
				live[t] = true
			}
			i = j - 1
		}
		for s := range live {
			note(s, start, start) // carried into the predecessors
		}
	}

	// A slot handed to a C callee can be written whenever that callee
	// likes, so its range is the whole function.
	for i := range fn.Code {
		if fn.Code[i].Op == OpSlotAddr {
			s := fn.Code[i].Imm
			note(s, 0, n-1)
		}
	}

	// ---- hand out the indexes ----

	next := int64(0)
	remap := map[int64]int64{}

	// The prologue stores the environment into slot zero by number, so
	// a closure's slot zero keeps that number and no other slot may
	// take it. Reserving it here, before the loop below can hand the
	// first free index to anyone else.
	if fn.Env {
		remap[0] = 0
		next = 1
	}

	// The map yields its keys in an order that changes from build to
	// build, so the sort below must leave nothing to chance: two slots
	// coming alive over the same gap are common, and which of them got
	// which number used to depend on that map order. Ties fall back to
	// the slot's own number, making one total order and one output.
	order := make([]int64, 0, len(gmin))
	for s := range gmin {
		order = append(order, s)
	}
	sort.Slice(order, func(x, y int) bool {
		if gmin[order[x]] != gmin[order[y]] {
			return gmin[order[x]] < gmin[order[y]]
		}
		return order[x] < order[y]
	})

	type lease struct {
		until int
		id    int64
	}
	busy := []lease{}
	var free []int64

	for _, s := range order {
		if fn.Env && s == 0 {
			continue // already fixed at zero above
		}
		first := gmin[s]
		alive := busy[:0]
		for _, b := range busy {
			if b.until < first {
				free = append(free, b.id)
			} else {
				alive = append(alive, b)
			}
		}
		busy = alive

		var id int64
		if len(free) > 0 {
			id = free[len(free)-1]
			free = free[:len(free)-1]
		} else {
			id = next
			next++
		}
		busy = append(busy, lease{until: gmax[s], id: id})
		remap[s] = id
	}

	for i := range fn.Code {
		switch fn.Code[i].Op {
		case OpLoad, OpStore, OpSlotAddr, OpStoreByte:
			fn.Code[i].Imm = remap[fn.Code[i].Imm]
		}
	}
	fn.NSlots = int(next)
}

// ---- collection checks ----

// dropPolls removes each statement's collection check that no path can
// reach having allocated since the last one.
//
// A check asks whether the heap has grown past the threshold. If nothing
// has allocated since the previous check said no, the answer is still
// no, so the check can go. A loop doing arithmetic had one at every
// statement of every trip round; now it has none.
//
// This is a forward dataflow over the function's blocks, with one bit:
// might something have been allocated since the last check? It starts
// set, since the caller may have allocated before calling. Anything
// raBarrier names - a call, an allocation, a helper - sets it, since
// all of those can allocate; a check clears it. Where paths join, a
// block's bit is set if any way in has it set, so a loop's back edge
// carries an allocation in its body round to the check at its top.
func dropPolls(fn *Func) {
	code := fn.Code
	n := len(code)
	if n == 0 {
		return
	}

	// Blocks start at the first instruction, at every label and after
	// every jump or return.
	starts := map[int]bool{0: true}
	labelAt := map[int64]int{}
	for i, in := range code {
		switch in.Op {
		case OpLabel:
			starts[i] = true
			labelAt[in.Imm] = i
		case OpJump, OpJumpIf, OpJumpNot, OpRet, OpBoundsFail, OpMustFail:
			if i+1 < n {
				starts[i+1] = true
			}
		}
	}
	var order []int
	for i := 0; i < n; i++ {
		if starts[i] {
			order = append(order, i)
		}
	}
	blockOf := map[int]int{}
	ends := make([]int, len(order))
	for b, s := range order {
		e := n
		if b+1 < len(order) {
			e = order[b+1]
		}
		ends[b] = e
		blockOf[s] = b
	}

	succ := make([][]int, len(order))
	for b, s := range order {
		last := code[ends[b]-1]
		fall := b + 1
		switch last.Op {
		case OpRet, OpBoundsFail, OpMustFail:
			// A failed bounds check or must ends the program, so its
			// call is no way back into the loop it sits in.
		case OpJump:
			if at, ok := labelAt[last.Imm]; ok {
				succ[b] = append(succ[b], blockOf[at])
			}
		case OpJumpIf, OpJumpNot:
			if at, ok := labelAt[last.Imm]; ok {
				succ[b] = append(succ[b], blockOf[at])
			}
			if fall < len(order) {
				succ[b] = append(succ[b], fall)
			}
		default:
			if fall < len(order) {
				succ[b] = append(succ[b], fall)
			}
		}
		_ = s
	}

	transfer := func(b int, dirty bool, drop map[int]bool) bool {
		for i := order[b]; i < ends[b]; i++ {
			in := &code[i]
			switch {
			case in.Op == OpGCPoll:
				if !dirty && drop != nil {
					drop[i] = true
				}
				dirty = false
			case raBarrier(in):
				dirty = true
			}
		}
		return dirty
	}

	in := make([]bool, len(order))
	seen := make([]bool, len(order))
	in[0], seen[0] = true, true
	work := []int{0}
	for len(work) > 0 {
		b := work[len(work)-1]
		work = work[:len(work)-1]
		out := transfer(b, in[b], nil)
		for _, s := range succ[b] {
			if !seen[s] || (out && !in[s]) {
				seen[s] = true
				in[s] = in[s] || out
				work = append(work, s)
			}
		}
	}

	drop := map[int]bool{}
	for b := range order {
		if seen[b] {
			transfer(b, in[b], drop)
		}
	}
	if len(drop) == 0 {
		return
	}
	kept := code[:0]
	for i, in := range code {
		if !drop[i] {
			kept = append(kept, in)
		}
	}
	fn.Code = kept
}
