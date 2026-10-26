package main

// Full unrolling of small counted loops, done while lowering.
//
//	for k in 0..4 { s += a[i*4 + k] }
//
// becomes four copies of the body, the first with k bound to 0, the
// next to 1, and so on. The variable is then a constant in every copy:
// forwardLoads turns its loads into constants, fold works out the index
// arithmetic, and a bounds check on a constant index folds away. Doing
// it here, on the tree, means there is no loop to find in the IR and no
// labels to copy; lowering the same body again is all it takes, because
// lowering does not write to the tree.
//
// Only loops whose bounds are literals and whose body has no way out
// but the end are taken (see unrollScan), and only while the copies
// stay small. Nested loops either all unroll or none of the enclosing
// ones do, so the size of the result can be told from the source.

const (
	unrollFullTrips  = 16   // most trips of one loop
	unrollFullBudget = 1500 // most nodes in all the copies of one loop
	unrollFullBody   = 400  // most nodes in one copy
	unrollFullBound  = 1 << 40
)

// constTrips is the number of trips a range loop with literal bounds
// makes, and the value of the variable on the first. ok is false when
// a bound or the step is not a literal, the bounds are too large to
// reason about without overflow, or the loop runs no times.
func constTrips(st *ForStmt, step int64) (n, first int64, ok bool) {
	if st.Coll != nil || st.Var2 != "" || st.Lowered != nil || step == 0 {
		return 0, 0, false
	}
	start, ok1 := constInt(st.Start)
	end, ok2 := constInt(st.End)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	if start < -unrollFullBound || start > unrollFullBound ||
		end < -unrollFullBound || end > unrollFullBound {
		return 0, 0, false
	}
	if step > unrollFullBound || step < -unrollFullBound {
		return 0, 0, false
	}
	if step > 0 {
		last := end - 1
		if st.Inclusive {
			last = end
		}
		if last >= start {
			n = (last-start)/step + 1
		}
	} else {
		last := end + 1
		if st.Inclusive {
			last = end
		}
		if start >= last {
			n = (start-last)/(-step) + 1
		}
	}
	return n, start, n >= 1
}

// unrollTrips decides whether a range loop is unrolled, and if so how
// many times and from which value. NOOPT must produce what the plain
// compiler does, since the fuzzers compare the two.
func (l *lowerer) unrollTrips(st *ForStmt, step int64) (n, first int64, ok bool) {
	if envOff("SCANDIACUS_NOOPT") || envOff("SCANDIACUS_NOUNROLL") {
		return 0, 0, false
	}
	n, first, ok = constTrips(st, step)
	if !ok || n > unrollFullTrips {
		return 0, 0, false
	}
	cost, ok := unrollScan(st.Body, []string{st.Var})
	if !ok || cost > unrollFullBody || cost*int(n) > unrollFullBudget {
		return 0, 0, false
	}
	return n, first, true
}

// unrollFor lowers the body once per trip. Each copy gets a scope and
// a slot of its own for the variable, so a nested loop's copies cannot
// see each other's.
func (l *lowerer) unrollFor(st *ForStmt, first, step, n int64) {
	for t := int64(0); t < n; t++ {
		l.pushScope()
		slot := l.declareLoopVar(st.Var, vInt)
		l.bindTrip(slot, l.constant(first+t*step), st.Var)
		l.stmt(st.Body)
		l.popScope()
	}
}

// unrollWalk measures a loop body and checks that it can be copied.
// names are the loop variables of the loop and every loop inside it
// that is being unrolled with it.
type unrollWalk struct {
	names []string
	cost  int
	bad   bool
}

// unrollScan returns the size of a body in nodes and whether it can be
// unrolled: no way out of the loop but its end (break, continue), no
// defer, no closure, no while, no write to or address of a loop
// variable, no redeclaration of one, and every loop inside it a
// constant one that unrolls in its own right. A closure would need a
// cell per trip, and lowering a function literal several times would
// lift several functions. A redeclared name is refused so that no later
// assignment has to be traced to the variable it means.
func unrollScan(body *Block, names []string) (int, bool) {
	w := &unrollWalk{names: names}
	w.stmt(body)
	return w.cost, !w.bad
}

func (w *unrollWalk) isLoopVar(name string) bool {
	for _, n := range w.names {
		if n == name {
			return true
		}
	}
	return false
}

func (w *unrollWalk) block(b *Block) {
	if b == nil {
		return
	}
	w.cost++
	for _, s := range b.Stmts {
		w.stmt(s)
	}
}

func (w *unrollWalk) stmt(s Stmt) {
	if w.bad {
		return
	}
	switch st := s.(type) {
	case nil:
	case *Block:
		w.block(st)
	case *LetStmt:
		w.cost++
		if w.isLoopVar(st.Name) {
			w.bad = true
			return
		}
		w.expr(st.Value)
	case *AssignStmt:
		w.cost++
		if w.isLoopVar(st.TargetName()) {
			w.bad = true
			return
		}
		w.expr(st.Target)
		w.expr(st.Value)
	case *ExprStmt:
		w.cost++
		w.expr(st.X)
	case *ReturnStmt:
		w.cost++
		w.expr(st.Value)
	case *IfStmt:
		w.cost++
		w.expr(st.Cond)
		w.block(st.Then)
		w.stmt(st.Else)
	case *MatchStmt:
		w.cost++
		w.expr(st.Subject)
		if st.Lowered != nil {
			w.stmt(st.Lowered)
			return
		}
		for _, c := range st.Cases {
			for _, v := range c.Values {
				w.expr(v)
			}
			w.stmt(c.Body)
		}
		w.stmt(st.Else)
	case *AsmStmt:
		w.cost++
		for _, op := range st.In {
			w.expr(op.X)
		}
		for _, op := range st.Out {
			if id, ok := op.X.(*Ident); ok && w.isLoopVar(id.Name) {
				w.bad = true
				return
			}
			w.expr(op.X)
		}
	case *ForStmt:
		w.forStmt(st)
	default:
		// break, continue, defer, while, and anything added later
		w.bad = true
	}
}

// forStmt measures a nested loop. It counts for all of its copies, and
// must pass the same tests the lowerer will apply to it when it gets
// there, or the outer loop would unroll and the inner one not.
func (w *unrollWalk) forStmt(st *ForStmt) {
	step := int64(1)
	if st.Step != nil {
		n, ok := constInt(st.Step)
		if !ok {
			w.bad = true
			return
		}
		step = n
	}
	n, _, ok := constTrips(st, step)
	if !ok || n > unrollFullTrips || w.isLoopVar(st.Var) {
		w.bad = true
		return
	}
	inner := &unrollWalk{names: append(append([]string{}, w.names...), st.Var)}
	inner.stmt(st.Body)
	if inner.bad || inner.cost > unrollFullBody || inner.cost*int(n) > unrollFullBudget {
		w.bad = true
		return
	}
	w.cost += inner.cost*int(n) + 1
}

func (w *unrollWalk) expr(e Expr) {
	if w.bad {
		return
	}
	w.cost++
	switch x := e.(type) {
	case nil:
		w.cost--
	case *Ident, *IntLit, *FloatLit, *StrLit, *BoolLit, *NilLit:
	case *Unary:
		if id, ok := x.X.(*Ident); ok && x.Op == AMP && w.isLoopVar(id.Name) {
			w.bad = true
			return
		}
		w.expr(x.X)
	case *Binary:
		w.expr(x.L)
		w.expr(x.R)
	case *Cast:
		w.expr(x.X)
	case *Widen:
		w.expr(x.X)
	case *Convert:
		w.expr(x.X)
	case *Try:
		w.expr(x.X)
	case *Field:
		w.expr(x.X)
		w.expr(x.Lit)
	case *Index:
		w.expr(x.X)
		w.expr(x.Idx)
	case *Interp:
		for _, p := range x.Parts {
			w.expr(p.X)
		}
	case *Call:
		w.expr(x.Callee)
		for _, a := range x.Args {
			w.expr(a)
		}
		w.expr(x.Lit)
	case *ListLit:
		for _, el := range x.Elems {
			w.expr(el)
		}
	case *MapLit:
		for i := range x.Keys {
			w.expr(x.Keys[i])
			w.expr(x.Vals[i])
		}
	case *StructLit:
		for _, v := range x.Vals {
			w.expr(v)
		}
	case *NewExpr:
		w.expr(x.Count)
		if x.Lit != nil {
			w.expr(x.Lit)
		}
	default:
		// FuncLit, and anything added later
		w.bad = true
	}
}
