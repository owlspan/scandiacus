package main

import "fmt"

// Four elements a trip: a counted loop over []float lists, run with
// SSE2's packed doubles.
//
//	for j in 0..n {
//	    c[i * n + j] += x * b[k * n + j]
//	}
//
// is the inner loop of a matrix multiply, and of most numeric code. When
// a counted loop's body is one assignment like that - an element of a
// []float list, at the loop variable plus something that does not
// change in the loop, set from such elements, float variables and
// literals with + - and * - the lowerer puts a loop in front of the
// ordinary one that does four elements at once, as two pairs: movupd to load
// two elements, mulpd and addpd, movupd to store them, twice. Every
// element goes through the same operations in the same order as before,
// so the results are the same to the bit. A pair does two of what is
// left over, and the ordinary loop then does the one that may remain, or
// the whole range.
//
// The packed loops run only when, checked once before them, every index
// they will touch is in range and the list written to is not one read at a
// different place (two names can hold the same list). Otherwise the
// ordinary loop does all of it, and so an index out of range stops the
// program with the same message at the same trip as it always did.
//
// Only xmm0 and xmm1 are used for the expression, the two float
// registers nothing keeps a value in, so it has to fit in two: c[...] +=
// x * b[...] does, a * b + d * e does not, and is left alone. One scalar
// that is only ever the right-hand operand of its operation is spread
// across xmm5 once, before the loop, and read from there; the register
// allocator is told to leave xmm5 alone in a function with such a loop.

// vecHoistReg holds the scalar spread out before the loop. xmm5 is the
// last of the allocator's float pool, and the one nothing else here
// names.
const vecHoistReg = 5

// vecWide in a packed op's Imm makes it work on the whole ymm register,
// four doubles, with AVX. The wide loop runs only when __vy_cpufeat,
// called once at the start of main, found AVX2 and an OS that saves the
// ymm registers; anywhere else, and in a DLL, which has no main, the
// SSE2 loop below it does the work. SCANDIACUS_NOAVX leaves it out.
const vecWide = 1 << 40

const (
	vecAdd = iota
	vecSub
	vecMul
)

// vecNode is the body's right-hand side as the packed loop sees it.
type vecNode struct {
	op     int // for an inner node
	l, r   *vecNode
	access int  // index into vecPlan.accesses, for an element
	scalar int  // index into vecPlan.scalars, for a float value
	leaf   bool // an element or a scalar
	isElem bool
}

type vecAccess struct {
	list   string // the variable holding the list
	offset Expr   // added to the loop variable; nil for none
}

type vecPlan struct {
	target   int // the access written to
	accesses []vecAccess
	scalars  []Expr
	root     *vecNode
}

// vectorizable reports whether a counted loop's body has the shape this
// file handles, and how to run it.
func (l *lowerer) vectorizable(st *ForStmt) (*vecPlan, bool) {
	if st.Body == nil || len(st.Body.Stmts) != 1 {
		return nil, false
	}
	as, ok := st.Body.Stmts[0].(*AssignStmt)
	if !ok {
		return nil, false
	}
	p := &vecPlan{}
	tgt, ok := as.Target.(*Index)
	if !ok {
		return nil, false
	}
	ti, ok := l.vecAccessOf(tgt, st.Var, p)
	if !ok {
		return nil, false
	}
	p.target = ti
	val, ok := l.vecTree(as.Value, st.Var, p)
	if !ok {
		return nil, false
	}
	switch as.Op {
	case ASSIGN:
		p.root = val
	case PLUSEQ:
		p.root = &vecNode{op: vecAdd, l: &vecNode{leaf: true, isElem: true, access: ti}, r: val}
	case MINUSEQ:
		p.root = &vecNode{op: vecSub, l: &vecNode{leaf: true, isElem: true, access: ti}, r: val}
	case STAREQ:
		p.root = &vecNode{op: vecMul, l: &vecNode{leaf: true, isElem: true, access: ti}, r: val}
	default:
		return nil, false
	}
	if vecNeed(p.root) > 2 {
		return nil, false
	}
	// The list written to may be read in the same statement only at the
	// same place; reading it at another index is a dependence between
	// trips that two at a time would get wrong. Another name for the
	// same list is checked when the loop runs.
	t := p.accesses[ti]
	for _, a := range p.accesses {
		if a.list == t.list && !sameExpr(a.offset, t.offset) {
			return nil, false
		}
	}
	return p, true
}

// vecAccessOf records xs[j] or xs[e + j] or xs[j + e], for a []float xs
// and an e that does not mention j.
func (l *lowerer) vecAccessOf(ix *Index, loopVar string, p *vecPlan) (int, bool) {
	id, ok := ix.X.(*Ident)
	if !ok {
		return 0, false
	}
	t, ok := l.vecVarType(id.Name)
	if !ok || t.k != kList || !t.elemType().eq(vFloat) {
		return 0, false
	}
	var off Expr
	switch x := ix.Idx.(type) {
	case *Ident:
		if x.Name != loopVar {
			return 0, false
		}
	case *Binary:
		if x.Op != PLUS {
			return 0, false
		}
		if j, isJ := x.R.(*Ident); isJ && j.Name == loopVar && l.vecInvariant(x.L, loopVar) {
			off = x.L
		} else if j, isJ := x.L.(*Ident); isJ && j.Name == loopVar && l.vecInvariant(x.R, loopVar) {
			off = x.R
		} else {
			return 0, false
		}
	default:
		return 0, false
	}
	p.accesses = append(p.accesses, vecAccess{list: id.Name, offset: off})
	return len(p.accesses) - 1, true
}

// vecTree reads the right-hand side.
func (l *lowerer) vecTree(e Expr, loopVar string, p *vecPlan) (*vecNode, bool) {
	switch x := e.(type) {
	case *Index:
		i, ok := l.vecAccessOf(x, loopVar, p)
		if !ok {
			return nil, false
		}
		return &vecNode{leaf: true, isElem: true, access: i}, true
	case *FloatLit:
		p.scalars = append(p.scalars, x)
		return &vecNode{leaf: true, scalar: len(p.scalars) - 1}, true
	case *Ident:
		if x.Name == loopVar {
			return nil, false
		}
		if t, ok := l.vecVarType(x.Name); !ok || !t.eq(vFloat) {
			return nil, false
		}
		p.scalars = append(p.scalars, x)
		return &vecNode{leaf: true, scalar: len(p.scalars) - 1}, true
	case *Binary:
		op := -1
		switch x.Op {
		case PLUS:
			op = vecAdd
		case MINUS:
			op = vecSub
		case STAR:
			op = vecMul
		}
		if op < 0 {
			return nil, false
		}
		a, ok := l.vecTree(x.L, loopVar, p)
		if !ok {
			return nil, false
		}
		b, ok := l.vecTree(x.R, loopVar, p)
		if !ok {
			return nil, false
		}
		return &vecNode{op: op, l: a, r: b}, true
	}
	return nil, false
}

// vecInvariant accepts an int expression of locals, globals, literals
// and + - *, none of them the loop variable: nothing in the body can
// change it, the body being one store to a list element.
func (l *lowerer) vecInvariant(e Expr, loopVar string) bool {
	switch x := e.(type) {
	case *IntLit:
		return true
	case *Ident:
		if x.Name == loopVar {
			return false
		}
		t, ok := l.vecVarType(x.Name)
		return ok && t.eq(vInt)
	case *Binary:
		if x.Op != PLUS && x.Op != MINUS && x.Op != STAR {
			return false
		}
		return l.vecInvariant(x.L, loopVar) && l.vecInvariant(x.R, loopVar)
	}
	return false
}

// vecVarType is the type of a plain local or global, and false for a
// name captured from an enclosing function or anything else.
func (l *lowerer) vecVarType(name string) (vty, bool) {
	if slot, ok := l.lookup(name); ok {
		if l.boxed[slot] {
			return vty{}, false
		}
		t, ok := l.slotTy[slot]
		return t, ok
	}
	if g, ok := l.globals[name]; ok {
		return l.globalTy[g], true
	}
	return vty{}, false
}

// vecNeed is how many registers a node takes to work out, the
// Sethi-Ullman number: a leaf takes one, an operation the larger of its
// sides', or one more when they are equal.
func vecNeed(n *vecNode) int {
	if n.leaf {
		return 1
	}
	a, b := vecNeed(n.l), vecNeed(n.r)
	if a == b {
		return a + 1
	}
	if a > b {
		return a
	}
	return b
}

// sameExpr reports whether two offsets are the same expression as
// written, which is all the dependence check needs.
func sameExpr(a, b Expr) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch x := a.(type) {
	case *IntLit:
		y, ok := b.(*IntLit)
		return ok && x.Val == y.Val
	case *Ident:
		y, ok := b.(*Ident)
		return ok && x.Name == y.Name
	case *Binary:
		y, ok := b.(*Binary)
		return ok && x.Op == y.Op && sameExpr(x.L, y.L) && sameExpr(x.R, y.R)
	}
	return false
}

// vecCanon swaps the operands of + and * so that a scalar sits on the
// right, where it can be read straight from its register, and returns
// the first scalar leaf found there. IEEE addition and multiplication
// commute, so the results do not change.
func vecCanon(n *vecNode) *vecNode {
	if n.leaf {
		return nil
	}
	if n.op != vecSub && n.l.leaf && !n.l.isElem && !(n.r.leaf && !n.r.isElem) {
		n.l, n.r = n.r, n.l
	}
	if h := vecCanon(n.l); h != nil {
		return h
	}
	if n.r.leaf && !n.r.isElem {
		return n.r
	}
	return vecCanon(n.r)
}

// usesVecHoist reports whether a function spreads a scalar into
// vecHoistReg before a packed loop.
func usesVecHoist(f *Func) bool {
	for i := range f.Code {
		if f.Code[i].Op == OpVSplat && f.Code[i].Imm&^vecWide == vecHoistReg {
			return true
		}
	}
	return false
}

// vecLoop writes the packed loop. iSlot holds the loop variable and
// endSlot the limit; the packed loop advances iSlot past every pair it
// does, and the ordinary loop after it finishes the range.
func (l *lowerer) vecLoop(p *vecPlan, iSlot, endSlot int64) {
	skip := l.newLabel()

	// Everything the loop reads that does not change, worked out once:
	// each list's element block and length, each offset, each scalar.
	data := map[string]int64{}
	length := map[string]int64{}
	for _, a := range p.accesses {
		if _, done := data[a.list]; done {
			continue
		}
		lst := l.expr(&Ident{Name: a.list})
		data[a.list] = l.temp(vInt)
		l.setSlot(data[a.list], l.field(lst, listDataOff, vRawPtr))
		length[a.list] = l.temp(vInt)
		l.setSlot(length[a.list], l.field(lst, listLenOff, vInt))
	}
	offs := make([]int64, len(p.accesses))
	for i, a := range p.accesses {
		offs[i] = l.temp(vInt)
		if a.offset == nil {
			l.setSlot(offs[i], l.constant(0))
		} else {
			l.setSlot(offs[i], l.rvalueAs(a.offset, vInt))
		}
	}
	// Each access's first element, data + offset*8, so the loop's
	// address is base + j*8: one operand of the movupd.
	bases := make([]int64, len(p.accesses))
	for i, a := range p.accesses {
		bases[i] = l.temp(vInt)
		b := l.newReg()
		l.regTy[b] = vInt
		l.emit(Instr{Op: OpIndexAddr, Dst: b, A: l.load(data[a.list], vInt), B: l.load(offs[i], vInt)})
		l.setSlot(bases[i], b)
	}
	scal := make([]int64, len(p.scalars))
	for i, s := range p.scalars {
		scal[i] = l.temp(vFloat)
		l.setSlot(scal[i], l.rvalueAs(s, vFloat))
	}

	ld := func(slot int64) Reg { return l.load(slot, vInt) }
	lo, hi := ld(iSlot), ld(endSlot)

	// At least one pair, every index in range, no overlap.
	l.jumpNot(l.compare(OpLt, l.arith(OpAdd, lo, l.constant(1)), hi), skip)
	for i, a := range p.accesses {
		first := l.arith(OpAdd, ld(offs[i]), ld(iSlot))
		last := l.arith(OpSub, l.arith(OpAdd, ld(offs[i]), ld(endSlot)), l.constant(1))
		l.jumpIf(l.compare(OpLt, first, l.constant(0)), skip)
		l.jumpNot(l.compare(OpLt, last, ld(length[a.list])), skip)
	}
	tgt := p.accesses[p.target]
	for _, a := range p.accesses {
		if a.list != tgt.list {
			l.jumpIf(l.compare(OpEq, ld(data[a.list]), ld(data[tgt.list])), skip)
		}
	}

	hoisted := vecCanon(p.root)
	pair := l.newLabel()

	// When the CPU has AVX2: eight elements a trip, as two ymm steps,
	// the second 32 bytes on, so that one step's loads overlap the
	// other's arithmetic (matmul 0.021s with one step a trip, 0.017s
	// with two). Then at most one more step of four. vzeroupper after
	// it, because an SSE instruction that follows AVX with the upper
	// halves dirty costs a stall on many CPUs. It leaves the low halves
	// alone, so xmm5 still holds the hoisted scalar for the pair below.
	if !envOff("SCANDIACUS_NOAVX") {
		l.mod.needs("cpufeat")
		sse := l.newLabel()
		l.jumpNot(l.compare(OpEq, l.rtLoad(cpuSlot), l.constant(2)), sse)
		if hoisted != nil {
			l.emit(Instr{Op: OpVSplat, Dst: NoReg, A: l.load(scal[hoisted.scalar], vFloat), B: NoReg, Imm: vecHoistReg | vecWide})
		}
		wtop, wdone := l.newLabel(), l.newLabel()
		l.mark(wtop)
		l.jumpNot(l.compare(OpLt, l.arith(OpAdd, ld(iSlot), l.constant(7)), ld(endSlot)), wdone)
		l.vecBody(p, bases, scal, hoisted, ld(iSlot), 0, vecWide)
		l.vecBody(p, bases, scal, hoisted, ld(iSlot), 2, vecWide)
		l.setSlot(iSlot, l.arith(OpAdd, ld(iSlot), l.constant(8)))
		l.jump(wtop)
		l.mark(wdone)
		w4 := l.newLabel()
		l.jumpNot(l.compare(OpLt, l.arith(OpAdd, ld(iSlot), l.constant(3)), ld(endSlot)), w4)
		l.vecBody(p, bases, scal, hoisted, ld(iSlot), 0, vecWide)
		l.setSlot(iSlot, l.arith(OpAdd, ld(iSlot), l.constant(4)))
		l.mark(w4)
		l.emit(Instr{Op: OpVZeroUpper, Dst: NoReg, A: NoReg, B: NoReg})
		l.jump(pair)
		l.mark(sse)
	}

	if hoisted != nil {
		l.emit(Instr{Op: OpVSplat, Dst: NoReg, A: l.load(scal[hoisted.scalar], vFloat), B: NoReg, Imm: vecHoistReg})
	}

	// Four elements a trip: the body twice, the second at j+2. What is
	// left is two or three, so at most one more pair, and then the
	// ordinary loop takes the one that may remain.
	top := l.newLabel()
	l.mark(top)
	j := ld(iSlot)
	l.jumpNot(l.compare(OpLt, l.arith(OpAdd, j, l.constant(3)), ld(endSlot)), pair)
	l.vecBody(p, bases, scal, hoisted, ld(iSlot), 0, 0)
	l.vecBody(p, bases, scal, hoisted, ld(iSlot), 1, 0)
	l.setSlot(iSlot, l.arith(OpAdd, ld(iSlot), l.constant(4)))
	l.jump(top)
	l.mark(pair)
	l.jumpNot(l.compare(OpLt, l.arith(OpAdd, ld(iSlot), l.constant(1)), ld(endSlot)), skip)
	l.vecBody(p, bases, scal, hoisted, ld(iSlot), 0, 0)
	l.setSlot(iSlot, l.arith(OpAdd, ld(iSlot), l.constant(2)))
	l.mark(skip)
}

// vecDisp is the byte offset a packed load or store carries in Imm, as
// text to follow a base in an address: the second pair of a trip is
// 16 bytes on from the first, with no register for its index.
func vecDisp(imm int64) string {
	if d := (imm &^ vecWide) >> 4; d != 0 {
		return fmt.Sprintf("+%d", d)
	}
	return ""
}

// vecMov and vecReg name the move and the register of a packed op.
func vecMov(imm int64) string {
	if imm&vecWide != 0 {
		return "vmovupd"
	}
	return "movupd"
}

func vecReg(imm, r int64) string {
	if imm&vecWide != 0 {
		return fmt.Sprintf("ymm%d", r)
	}
	return fmt.Sprintf("xmm%d", r)
}

// vecBody is one packed step: the two elements at j and j+1 of every
// access, plus 2*pair for the second step of a trip, worked out in xmm0
// and xmm1 and stored. With wide set to vecWide it is four elements, in
// ymm0 and ymm1.
func (l *lowerer) vecBody(p *vecPlan, bases []int64, scal []int64, hoisted *vecNode, j Reg, pair, wide int64) {
	addr := func(i int) Reg {
		a := l.newReg()
		l.regTy[a] = vInt
		l.emit(Instr{Op: OpIndexAddr, Dst: a, A: l.load(bases[i], vInt), B: j})
		return a
	}
	var gen func(n *vecNode, reg int) int
	gen = func(n *vecNode, reg int) int {
		if n == hoisted {
			return vecHoistReg
		}
		if n.leaf {
			if n.isElem {
				l.emit(Instr{Op: OpVLoad, Dst: NoReg, A: addr(n.access), B: NoReg, Imm: int64(reg) | pair*16<<4 | wide})
			} else {
				l.emit(Instr{Op: OpVSplat, Dst: NoReg, A: l.load(scal[n.scalar], vFloat), B: NoReg, Imm: int64(reg) | wide})
			}
			return reg
		}
		if vecNeed(n.l) >= vecNeed(n.r) {
			// The left side can end in either register (a subtraction
			// below it may leave its result in the other one), so the
			// right side goes in whichever one it did not use.
			a := gen(n.l, reg)
			b := gen(n.r, 1-a)
			l.emit(Instr{Op: OpVArith, Dst: NoReg, A: NoReg, B: NoReg, Imm: int64(n.op<<8|a<<4|b) | wide})
			return a
		}
		// A subtraction whose right side is itself an operation leaves
		// its result in the other register, so the left leaf goes in
		// whichever one the right side did not end in.
		b := gen(n.r, reg)
		a := gen(n.l, 1-b)
		if n.op == vecSub {
			l.emit(Instr{Op: OpVArith, Dst: NoReg, A: NoReg, B: NoReg, Imm: int64(n.op<<8|a<<4|b) | wide})
			return a
		}
		l.emit(Instr{Op: OpVArith, Dst: NoReg, A: NoReg, B: NoReg, Imm: int64(n.op<<8|b<<4|a) | wide})
		return b
	}
	res := gen(p.root, 0)
	l.emit(Instr{Op: OpVStore, Dst: NoReg, A: addr(p.target), B: NoReg, Imm: int64(res) | pair*16<<4 | wide})
}
