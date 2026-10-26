package main

import "strings"

// Common subexpressions within a block.
//
// `ps[i].x += ps[i].vx * dt` names ps[i] twice, and each one lowers to
// the same work: the list's length and data pointer loaded from its
// header, the index checked against the length, the element's address,
// the element loaded. The second time round all of it gives what the
// first did, so this pass keeps the first and renames every use of the
// second's results to it.
//
// The facts are kept from one label to the next. A label is a place
// control arrives from elsewhere, where what held on the way down
// need not hold, and every block starts empty. Bounds checks no longer
// make labels (OpCheckIndex), so a block is usually a whole statement
// or a whole loop body.
//
// Pure operations - arithmetic, comparisons, an element's address - are
// the same whenever their operands are, registers being single
// assignment. A bounds check that passed passes again for the same two
// registers. A load from memory is the same only while nothing has
// written there, and that is where the work is: a call may write
// anything, so it forgets every load; a store forgets only the loads it
// could have written over.
//
// What a store could have written over is decided by the kind of object
// its address points into. Scandiacus objects of different kinds are
// different allocations, so a list's header, the buffer holding a
// list's elements, a struct of each type, a map, a string and a bytes
// never overlap one another. An address that is just a number -
// mem.write64, an extern struct, a pointer - could be anything, so a
// store through one forgets everything, and a load through one is
// forgotten by any store.

type memClass string

const rawMem memClass = ""

// classOf is the kind of object a base register points into.
func classOf(m *Module, fn *Func, elem map[Reg]bool, r Reg) memClass {
	if elem[r] {
		return "elem"
	}
	if r < 0 || int(r) >= len(fn.RegTypes) {
		return rawMem
	}
	t := fn.RegTypes[r]
	if t.res || t.null {
		return rawMem
	}
	switch t.k {
	case kList:
		return "list"
	case kMap:
		return "map"
	case kStr:
		return "str"
	case kBytes:
		return "bytes"
	case kFunc:
		return "func"
	case kStruct:
		if t.name == "" || m.Views[t.name] {
			return rawMem
		}
		return memClass("struct " + t.name)
	}
	return rawMem
}

type pureKey struct {
	op   Op
	a, b Reg
	imm  int64
	// An operand that is an integer constant is keyed by its value
	// (ca, cb set, a or b cleared), so x * 32 written twice is one
	// multiplication though each 32 is a register of its own.
	ca, cb bool
	av, bv int64
}

type loadKey struct {
	base Reg
	off  int64
}

type loadFact struct {
	dst   Reg
	class memClass
	off   int64
}

// byField reports whether a class's objects are only ever written a
// whole word at a known offset - a struct's fields, a list's header -
// so a store at one offset leaves the others as they were, whichever
// object of the class it hit.
func byField(c memClass) bool {
	return c == "list" || strings.HasPrefix(string(c), "struct ")
}

// csePure lists the operations whose result depends on nothing but
// their operands. Loads from slots are forwardLoads' business, and
// constants are left alone: a constant is cheaper to have again than to
// keep alive.
func csePure(op Op) bool {
	switch op {
	case OpAdd, OpSub, OpMul, OpNeg,
		OpFAdd, OpFSub, OpFMul, OpFDiv, OpFNeg,
		OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe,
		OpIntToFloat, OpFloatToInt, OpSqrt,
		OpBAnd, OpBOr, OpBXor, OpBNot, OpShl, OpShr,
		OpEq, OpNe, OpLt, OpLe, OpGt, OpGe, OpNot,
		OpExt, OpF32Round, OpIntToF32,
		OpShrU, OpLtU, OpLeU, OpGtU, OpGeU,
		OpIndexAddr:
		return true
	}
	return false
}

func cse(m *Module, fn *Func) {
	if len(fn.RegTypes) < fn.NRegs {
		return // hand-built IR in a test, with no types to go by
	}
	escaped := map[int64]bool{}
	elem := map[Reg]bool{}
	for _, in := range fn.Code {
		switch in.Op {
		case OpSlotAddr:
			escaped[in.Imm] = true
		case OpIndexAddr:
			elem[in.Dst] = true
		}
	}

	sameAs := map[Reg]Reg{}
	consts := map[Reg]int64{}
	for _, in := range fn.Code {
		if in.Op == OpConst && in.Dst != NoReg {
			consts[in.Dst] = in.Imm
		}
	}
	follow := func(r Reg) Reg {
		for {
			next, ok := sameAs[r]
			if !ok {
				return r
			}
			r = next
		}
	}
	pure := map[pureKey]Reg{}
	loads := map[loadKey]loadFact{}
	checked := map[[2]Reg]bool{}
	reset := func() {
		pure = map[pureKey]Reg{}
		loads = map[loadKey]loadFact{}
		checked = map[[2]Reg]bool{}
	}
	forget := func(c memClass, off int64, known bool) {
		for k, f := range loads {
			if c == rawMem || f.class == rawMem ||
				f.class == c && !(known && byField(c) && f.off != off) {
				delete(loads, k)
			}
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

		switch {
		case in.Op == OpLabel:
			reset()

		case csePure(in.Op):
			k := pureKey{op: in.Op, a: in.A, b: in.B, imm: in.Imm}
			if v, ok := consts[in.A]; ok && in.A != NoReg {
				k.a, k.ca, k.av = NoReg, true, v
			}
			if v, ok := consts[in.B]; ok && in.B != NoReg {
				k.b, k.cb, k.bv = NoReg, true, v
			}
			if d, ok := pure[k]; ok && sameClass(fn, d, in.Dst) {
				sameAs[in.Dst] = d
				continue
			}
			pure[k] = in.Dst

		case in.Op == OpCheckIndex:
			k := [2]Reg{in.A, in.B}
			if checked[k] {
				continue
			}
			checked[k] = true

		case in.Op == OpLoadMem:
			k := loadKey{in.A, in.Imm}
			if f, ok := loads[k]; ok && sameClass(fn, f.dst, in.Dst) {
				sameAs[in.Dst] = f.dst
				continue
			}
			loads[k] = loadFact{in.Dst, classOf(m, fn, elem, in.A), in.Imm}

		case in.Op == OpStoreMem:
			// What was stored is what a load reads back, until
			// something else writes there.
			c := classOf(m, fn, elem, in.A)
			forget(c, in.Imm, true)
			if in.B != NoReg {
				loads[loadKey{in.A, in.Imm}] = loadFact{in.B, c, in.Imm}
			}

		case in.Op == OpPoke, in.Op == OpStoreByte:
			forget(classOf(m, fn, elem, in.A), 0, false)

		case in.Op == OpStore:
			if escaped[in.Imm] {
				forget(rawMem, 0, false)
			}

		case in.Op == OpLoad, in.Op == OpConst, in.Op == OpFConst, in.Op == OpStr,
			in.Op == OpLoadByte, in.Op == OpPeek, in.Op == OpSymAddr,
			in.Op == OpGlobalAddr, in.Op == OpSlotAddr, in.Op == OpParam,
			in.Op == OpJump, in.Op == OpJumpIf, in.Op == OpJumpNot:
			// Reads, or control flow: nothing written.

		default:
			// A call, an allocation, an atomic, anything else: it may
			// have written anywhere.
			forget(rawMem, 0, false)
		}
		out = append(out, in)
	}
	fn.Code = out
}
