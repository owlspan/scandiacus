package main

// Which names a function literal takes from around it.
//
// This is the one place in the backend that needs a real walk over the
// syntax tree. Everything else here works from the checker's answers or
// from one node at a time; a closure cannot, because what it captures is
// a property of the whole body and of every scope enclosing it.
//
// The walk tracks scopes rather than collecting every identifier and
// subtracting. The difference shows up in
//
//	let f = fn() -> int { let y = x  let x = 2  return y }
//
// where subtracting every name bound anywhere inside would decide x is
// not free, and it is - the outer one, used before the inner one is
// declared. Over-approximating a capture is harmless; missing one
// compiles and then reads a variable that is not there.

// freeNames lists what fd refers to and does not bind, in first-use
// order. The order is what fixes the layout of the environment, so it
// has to be deterministic - a map would make two compilations of the
// same program disagree.
func freeNames(fd *FnDecl) []string {
	w := &capWalk{}
	w.push()
	for _, p := range fd.Params {
		w.bind(p.Name)
	}
	w.block(fd.Body)
	w.pop()
	return w.free
}

type capWalk struct {
	scopes []map[string]bool
	free   []string
	seen   map[string]bool
}

func (w *capWalk) push() { w.scopes = append(w.scopes, map[string]bool{}) }
func (w *capWalk) pop()  { w.scopes = w.scopes[:len(w.scopes)-1] }

func (w *capWalk) bind(name string) {
	if len(w.scopes) == 0 {
		w.push()
	}
	w.scopes[len(w.scopes)-1][name] = true
}

func (w *capWalk) bound(name string) bool {
	for i := len(w.scopes) - 1; i >= 0; i-- {
		if w.scopes[i][name] {
			return true
		}
	}
	return false
}

func (w *capWalk) use(name string) {
	if w.bound(name) {
		return
	}
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	if w.seen[name] {
		return
	}
	w.seen[name] = true
	w.free = append(w.free, name)
}

func (w *capWalk) block(b *Block) {
	if b == nil {
		return
	}
	w.push()
	for _, s := range b.Stmts {
		w.stmt(s)
	}
	w.pop()
}

func (w *capWalk) stmt(s Stmt) {
	switch x := s.(type) {
	case *Block:
		w.block(x)

	case *LetStmt:
		// The value is walked before the name is bound, so `let x = x`
		// reads the outer one, which is what it means.
		w.expr(x.Value)
		w.bind(x.Name)

	case *AssignStmt:
		w.expr(x.Target)
		w.expr(x.Value)

	case *ExprStmt:
		w.expr(x.X)

	case *IfStmt:
		w.expr(x.Cond)
		w.block(x.Then)
		w.stmt(x.Else)

	case *WhileStmt:
		w.expr(x.Cond)
		w.block(x.Body)

	case *ReturnStmt:
		w.expr(x.Value)

	case *ForStmt:
		w.expr(x.Start)
		w.expr(x.End)
		w.expr(x.Step)
		w.expr(x.Coll)
		w.push()
		w.bind(x.Var)
		if x.Var2 != "" {
			w.bind(x.Var2)
		}
		w.block(x.Body)
		w.pop()

	case *MatchStmt:
		w.expr(x.Subject)
		for _, c := range x.Cases {
			for _, v := range c.Values {
				w.expr(v)
			}
			w.stmt(c.Body)
		}
		w.stmt(x.Else)
	}
	// break and continue bind and refer to nothing.
}

func (w *capWalk) expr(e Expr) {
	switch x := e.(type) {
	case nil:
		return

	case *Ident:
		w.use(x.Name)

	case *FuncLit:
		// A nested literal's own free names are free here too, unless
		// this level binds them. Walking it in place, with this scope
		// stack, gets that without a second pass.
		w.push()
		for _, p := range x.Decl.Params {
			w.bind(p.Name)
		}
		w.block(x.Decl.Body)
		w.pop()

	case *Try:
		w.expr(x.X)

	case *Widen:
		w.expr(x.X)

	case *Unary:
		w.expr(x.X)

	case *Cast:
		w.expr(x.X)

	case *Binary:
		w.expr(x.L)
		w.expr(x.R)

	case *Call:
		w.expr(x.Callee)
		for _, a := range x.Args {
			w.expr(a)
		}

	case *Interp:
		for _, p := range x.Parts {
			w.expr(p.X)
		}

	case *Field:
		// Only the receiver is an expression; the field name is not a
		// variable. A library path like os.file.read reaches here as a
		// Field over an Ident that names no variable, and use() on it is
		// harmless: nothing declares it, so it is never a capture.
		w.expr(x.X)

	case *ListLit:
		for _, v := range x.Elems {
			w.expr(v)
		}

	case *MapLit:
		for _, k := range x.Keys {
			w.expr(k)
		}
		for _, v := range x.Vals {
			w.expr(v)
		}

	case *Index:
		w.expr(x.X)
		w.expr(x.Idx)

	case *StructLit:
		for _, v := range x.Vals {
			w.expr(v)
		}
	}
	// The literals bind and refer to nothing.
}

// capturedIn reports which of a function's own names are taken by some
// literal inside it. Those are the ones that have to live in a box:
// a closure outliving the frame that made it cannot read a stack slot,
// and `seen += n` inside a closure has to be visible outside it.
func capturedIn(fd *FnDecl) map[string]bool {
	out := map[string]bool{}
	collectCaptured(fd.Body, out)
	return out
}

// capturedInStmts is capturedIn for the top level, which is a list of
// statements rather than a declaration. main has locals like any other
// function and a literal can capture one, so it needs the same answer.
func capturedInStmts(stmts []Stmt) map[string]bool {
	out := map[string]bool{}
	for _, s := range stmts {
		captureStmt(s, out)
	}
	return out
}

func collectCaptured(b *Block, out map[string]bool) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		captureStmt(s, out)
	}
}

func captureStmt(s Stmt, out map[string]bool) {
	switch x := s.(type) {
	case *Block:
		collectCaptured(x, out)
	case *LetStmt:
		captureExpr(x.Value, out)
	case *AssignStmt:
		captureExpr(x.Target, out)
		captureExpr(x.Value, out)
	case *ExprStmt:
		captureExpr(x.X, out)
	case *IfStmt:
		captureExpr(x.Cond, out)
		collectCaptured(x.Then, out)
		captureStmt(x.Else, out)
	case *WhileStmt:
		captureExpr(x.Cond, out)
		collectCaptured(x.Body, out)
	case *ReturnStmt:
		captureExpr(x.Value, out)
	case *ForStmt:
		captureExpr(x.Start, out)
		captureExpr(x.End, out)
		captureExpr(x.Step, out)
		captureExpr(x.Coll, out)
		collectCaptured(x.Body, out)
	case *MatchStmt:
		captureExpr(x.Subject, out)
		for _, c := range x.Cases {
			for _, v := range c.Values {
				captureExpr(v, out)
			}
			captureStmt(c.Body, out)
		}
		captureStmt(x.Else, out)
	}
}

func captureExpr(e Expr, out map[string]bool) {
	switch x := e.(type) {
	case nil:
		return
	case *FuncLit:
		for _, n := range freeNames(x.Decl) {
			out[n] = true
		}
		// A literal nested inside this one may capture something from
		// two levels up, which is this level's business too.
		collectCaptured(x.Decl.Body, out)
	case *Try:
		captureExpr(x.X, out)
	case *Widen:
		captureExpr(x.X, out)
	case *Unary:
		captureExpr(x.X, out)
	case *Cast:
		captureExpr(x.X, out)
	case *Binary:
		captureExpr(x.L, out)
		captureExpr(x.R, out)
	case *Call:
		captureExpr(x.Callee, out)
		for _, a := range x.Args {
			captureExpr(a, out)
		}
	case *Interp:
		for _, p := range x.Parts {
			captureExpr(p.X, out)
		}
	case *Field:
		captureExpr(x.X, out)
	case *ListLit:
		for _, v := range x.Elems {
			captureExpr(v, out)
		}
	case *MapLit:
		for _, k := range x.Keys {
			captureExpr(k, out)
		}
		for _, v := range x.Vals {
			captureExpr(v, out)
		}
	case *Index:
		captureExpr(x.X, out)
		captureExpr(x.Idx, out)
	case *StructLit:
		for _, v := range x.Vals {
			captureExpr(v, out)
		}
	}
}
