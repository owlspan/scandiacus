package main

// Calls a function makes to itself in tail position become jumps.
//
//	fn gcd(a: int, b: int) -> int {
//	    if b == 0 { return a }
//	    return gcd(b, a % b)
//	}
//
// The `return gcd(b, a % b)` stores the new arguments into the
// parameters and jumps back to the top of the body, so the recursion
// runs as a loop in one frame.
//
// A return that adds or multiplies an int onto a self call is turned
// into a loop too, with an accumulator:
//
//	fn fib(n: int) -> int {
//	    if n < 2 { return n }
//	    return fib(n - 1) + fib(n - 2)
//	}
//
// keeps acc = 0, and `return fib(n - 1) + fib(n - 2)` becomes
// acc += fib(n - 1); n = n - 2; jump to the top, with every other return
// handing back acc + value. One of the two calls is gone, which is most
// of what an optimizing C compiler does to fib. Ints wrap, so the sums
// come out the same in any grouping. Only the right operand is taken:
// the left is worked out first, as it was before.
//
// Kept narrow on purpose. The parameters must be int, float or bool -
// a struct argument is copied and a str or list one has ownership rules
// a plain store would skip - and the function must have no defer, no
// parameter a closure captures or whose address is taken, and no local
// of its own name. A tail call that recursed forever used to end in
// the stack overflow report; now it is a loop that does not end, as in
// C.

type tailInfo struct {
	fn *Func // the function it is for: a closure or helper lowered
	// in the middle of it must not see it
	name  string
	top   int64
	slots []int64
	types []vty
	acc   int64 // the accumulator's slot, -1 when there is none
	op    Kind  // PLUS or STAR, with acc
}

// tailShape reports whether fd has a self call in tail position the
// lowerer can turn into a jump, and the accumulating operator if any
// return needs one (0 if none). It is false for any function outside
// the narrow shape described above.
func tailShape(fd *FnDecl, params []vty, ret vty) (bool, Kind) {
	if fd.Recv != "" || fd.Pkg != "" || fd.Extern || fd.Body == nil || fd.TestName != "" {
		return false, 0
	}
	for i, p := range fd.Params {
		t := params[i]
		if p.Addressed || p.Name == fd.Name || !(t.eq(vInt) || t.eq(vFloat) || t.eq(vBool)) {
			return false, 0
		}
	}
	if len(capturedIn(fd)) > 0 {
		for _, p := range fd.Params {
			if capturedIn(fd)[p.Name] {
				return false, 0
			}
		}
	}
	self := func(e Expr) bool { return isSelfCall(e, fd) }
	ok, bad := false, false
	var op Kind
	var stmt func(s Stmt)
	block := func(b *Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			stmt(s)
		}
	}
	stmt = func(s Stmt) {
		switch x := s.(type) {
		case *Block:
			block(x)
		case *DeferStmt:
			bad = true
		case *LetStmt:
			if x.Name == fd.Name {
				bad = true
			}
		case *ReturnStmt:
			if x.Value == nil {
				return
			}
			if self(x.Value) {
				ok = true
				return
			}
			if b, isBin := x.Value.(*Binary); isBin && (b.Op == PLUS || b.Op == STAR) &&
				self(b.R) && ret.eq(vInt) {
				if op != 0 && op != b.Op {
					bad = true
				}
				op = b.Op
				ok = true
			}
		case *IfStmt:
			block(x.Then)
			if x.Else != nil {
				stmt(x.Else)
			}
		case *WhileStmt:
			block(x.Body)
		case *ForStmt:
			block(x.Body)
			if x.Lowered != nil {
				stmt(x.Lowered)
			}
		case *MatchStmt:
			for _, c := range x.Cases {
				if c.Body != nil {
					stmt(c.Body)
				}
			}
			if x.Else != nil {
				stmt(x.Else)
			}
		}
	}
	block(fd.Body)
	return ok && !bad, op
}

func isSelfCall(e Expr, fd *FnDecl) bool {
	c, ok := e.(*Call)
	if !ok || len(c.Args) != len(fd.Params) {
		return false
	}
	id, ok := c.Callee.(*Ident)
	return ok && id.Name == fd.Name
}

// tailStart is called after the parameters are stored: it sets the
// accumulator and marks the top the tail calls jump back to.
func (l *lowerer) tailStart(fd *FnDecl, params []vty, ret vty) {
	l.tail = nil
	ok, op := tailShape(fd, params, ret)
	if !ok {
		return
	}
	t := &tailInfo{fn: l.fn, name: fd.Name, acc: -1, op: op, types: params}
	for _, p := range fd.Params {
		slot, _ := l.lookup(p.Name)
		t.slots = append(t.slots, slot)
	}
	if op != 0 {
		t.acc = l.temp(vInt)
		start := int64(0)
		if op == STAR {
			start = 1
		}
		l.emit(Instr{Op: OpStore, A: l.constant(start), Dst: NoReg, Imm: t.acc})
	}
	t.top = l.newLabel()
	l.mark(t.top)
	l.tail = t
}

// tailReturn lowers a return in a function tailStart accepted. It
// reports false when the return is an ordinary one, which the caller
// lowers as usual and then passes through tailResult.
func (l *lowerer) tailReturn(st *ReturnStmt) bool {
	t := l.tail
	if t == nil || t.fn != l.fn || st.Value == nil || l.hasDefers(0) {
		return false
	}
	call := st.Value
	if b, isBin := st.Value.(*Binary); isBin && t.acc >= 0 && b.Op == t.op {
		call = b.R
		if !l.isTailCall(call) {
			return false
		}
		v := l.rvalueAs(b.L, vInt)
		accOp := OpAdd
		if t.op == STAR {
			accOp = OpMul
		}
		l.emit(Instr{Op: OpStore, A: l.arith(accOp, l.load(t.acc, vInt), v), Dst: NoReg, Imm: t.acc})
	} else if !l.isTailCall(call) {
		return false
	}
	c := call.(*Call)
	// Every argument first, then every store: an argument may read a
	// parameter an earlier store would have changed.
	vals := make([]Reg, len(c.Args))
	for i, a := range c.Args {
		v := l.rvalueAs(a, t.types[i])
		fitted, ok := l.coerce(v, t.types[i])
		if !ok {
			l.errorAt(a, "argument %d is %s, but %s takes %s", i+1, l.regTy[v], t.name, t.types[i])
			return true
		}
		vals[i] = fitted
	}
	for i, v := range vals {
		l.storeLocal(t.slots[i], v)
	}
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: t.top, Comment: "tail call"})
	return true
}

// isTailCall checks a call names this function and not a local that
// shadows it, which a nested block can still declare by another route.
func (l *lowerer) isTailCall(e Expr) bool {
	c, ok := e.(*Call)
	if !ok || len(c.Args) != len(l.tail.slots) {
		return false
	}
	id, ok := c.Callee.(*Ident)
	if !ok || id.Name != l.tail.name {
		return false
	}
	_, local := l.lookup(id.Name)
	return !local
}

// tailResult folds the accumulator into an ordinary return's value.
func (l *lowerer) tailResult(v Reg) Reg {
	t := l.tail
	if t == nil || t.fn != l.fn || t.acc < 0 || !l.regTy[v].eq(vInt) {
		return v
	}
	accOp := OpAdd
	if t.op == STAR {
		accOp = OpMul
	}
	return l.arith(accOp, l.load(t.acc, vInt), v)
}
