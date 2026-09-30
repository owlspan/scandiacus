package frontend

// &local: the address of a variable.
//
// A native function with an out-parameter wants somewhere to write, and
// before this the only somewhere was memory from mem.alloc or new. Now
// a local number, bool or pointer can be pointed at directly:
//
//	let size: u32 = 0
//	GetFileSize(handle, &size)
//
// The pointer has the variable's own type underneath, *u32 there, and
// the variable is kept in memory at that width for the rest of the
// function, so whatever native code writes is what the variable reads.
//
// The variable lives as long as the function's frame, so its address
// must not outlive the call. The checker follows it through the
// function (into other locals, through pointer arithmetic and `as`)
// and refuses the ways out it can see: returning it, storing it in a
// global, a field, a list, a map or through another pointer, and a
// closure holding on to it. Handing it to a function you call is
// allowed, as in C; that function must not keep it.

// addrLocal types &x for a variable x.
func (c *Checker) addrLocal(x *Unary, id *Ident) *Type {
	t, depth := c.lookupDepth(id.Name)
	if t == nil {
		c.ErrorAt(x, "'&' needs a variable, and %s is not one", id.Name)
		return Unknown
	}
	if t.IsUnknown() {
		return Unknown
	}
	// An extern struct variable already is an address: &v is a pointer
	// to the struct it views, not to the variable holding the view.
	if c.isExternStruct(t) {
		return PtrOf(t)
	}
	if c.isConst(id.Name) {
		c.ErrorAt(x, "%s is const, so it cannot be written through a pointer", id.Name)
		return Unknown
	}
	if depth == 0 {
		c.ErrorAt(x, "%s is a global, which has no address to give yet - allocate it with new", id.Name)
		return Unknown
	}
	if c.loopVars[depth][id.Name] {
		c.ErrorAt(x, "%s is a loop variable, which the loop itself keeps changing - copy it into "+
			"a let and take the address of that", id.Name)
		return Unknown
	}
	var p *Type
	switch {
	case t.Kind == KInt:
		p = PtrToC("i64")
	case t.Kind == KFloat:
		p = PtrToC("f64")
	case t.Kind == KBool:
		p = PtrToC("bool")
	case t.IsFixed():
		p = PtrToC(t.Name)
	case t.IsPtr():
		p = PtrOf(t)
	default:
		c.ErrorAt(x, "only a number, a bool or a pointer variable has an address to give - %s is %s",
			id.Name, t)
		return Unknown
	}
	x.Local = true
	return p
}

// lookupDepth is lookup, also saying which scope the name was found in:
// 0 is the globals.
func (c *Checker) lookupDepth(name string) (*Type, int) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if t, ok := c.scopes[i][name]; ok {
			return t, i
		}
	}
	return nil, -1
}

// escapes checks one function body for a local's address leaving it,
// and marks the variables whose address is taken so a backend keeps
// them in memory.
func (c *Checker) escapes(params []Param, body []Stmt) {
	w := &escWalk{c: c, tainted: map[string]bool{}, addressed: map[string]bool{},
		locals: map[string]bool{}}
	for _, p := range params {
		w.locals[p.Name] = true
	}
	// Which names hold a local's address, to a fixed point: `q = p` in
	// a loop can come before the `p = &x` that makes it matter.
	for {
		before := len(w.tainted)
		w.stmts(body)
		if len(w.tainted) == before {
			break
		}
	}
	w.report = true
	w.stmts(body)
	for i := range params {
		if w.addressed[params[i].Name] {
			params[i].Addressed = true
		}
	}
	markAddressed(body, w.addressed)
}

type escWalk struct {
	c         *Checker
	tainted   map[string]bool // names holding a local's address
	addressed map[string]bool // names whose own address is taken
	locals    map[string]bool // names this function declares
	report    bool
}

// isAddr reports an expression that is, or is computed from, the
// address of one of this function's variables.
func (w *escWalk) isAddr(e Expr) bool {
	switch x := e.(type) {
	case *Unary:
		if x.Op == AMP && x.Local {
			return true
		}
	case *Ident:
		return w.tainted[x.Name]
	case *Binary:
		if x.Op == PLUS || x.Op == MINUS {
			return w.isAddr(x.L) || w.isAddr(x.R)
		}
	case *Cast:
		return w.isAddr(x.X)
	case *Widen:
		return w.isAddr(x.X)
	case *Convert:
		return w.isAddr(x.X)
	}
	return false
}

func (w *escWalk) escape(at Node, e Expr, where string) {
	if w.report && w.isAddr(e) {
		what := "this is the address of a local variable"
		if id, ok := e.(*Ident); ok {
			what = id.Name + " holds the address of a local variable"
		}
		w.c.ErrorAt(at, "%s, which is gone once the function returns, so it cannot be %s - "+
			"allocate the value with new instead", what, where)
	}
}

func (w *escWalk) stmts(list []Stmt) {
	for _, s := range list {
		w.stmt(s)
	}
}

func (w *escWalk) block(b *Block) {
	if b != nil {
		w.stmts(b.Stmts)
	}
}

func (w *escWalk) stmt(s Stmt) {
	switch st := s.(type) {
	case *LetStmt:
		w.locals[st.Name] = true
		w.expr(st.Value)
		if w.isAddr(st.Value) {
			w.tainted[st.Name] = true
		}
	case *AssignStmt:
		w.expr(st.Target)
		w.expr(st.Value)
		if id, ok := st.Target.(*Ident); ok && w.locals[id.Name] {
			if w.isAddr(st.Value) {
				w.tainted[id.Name] = true
			}
			return
		}
		w.escape(st, st.Value, "stored anywhere that outlives the function")
	case *ExprStmt:
		w.expr(st.X)
	case *ReturnStmt:
		if st.Value != nil {
			w.expr(st.Value)
			w.escape(st, st.Value, "returned")
		}
	case *IfStmt:
		w.expr(st.Cond)
		w.block(st.Then)
		if st.Else != nil {
			w.stmt(st.Else)
		}
	case *WhileStmt:
		w.expr(st.Cond)
		w.block(st.Body)
	case *ForStmt:
		if st.Lowered != nil {
			w.stmt(st.Lowered)
			return
		}
		w.locals[st.Var] = true
		if st.Var2 != "" {
			w.locals[st.Var2] = true
		}
		for _, e := range []Expr{st.Start, st.End, st.Step, st.Coll} {
			w.expr(e)
		}
		w.block(st.Body)
	case *MatchStmt:
		if st.Lowered != nil {
			w.locals[st.Temp] = true
			w.expr(st.Subject)
			w.stmt(st.Lowered)
			return
		}
		w.expr(st.Subject)
		for _, arm := range st.Cases {
			for _, v := range arm.Values {
				w.expr(v)
			}
			w.stmt(arm.Body)
		}
		if st.Else != nil {
			w.stmt(st.Else)
		}
	case *DeferStmt:
		w.stmt(st.Body)
	case *Block:
		w.block(st)
	}
}

func (w *escWalk) expr(e Expr) {
	switch x := e.(type) {
	case nil:
	case *Unary:
		if x.Op == AMP {
			if id, ok := x.X.(*Ident); ok && x.Local {
				w.addressed[id.Name] = true
				if w.report && !w.locals[id.Name] {
					w.c.ErrorAt(x, "%s belongs to the function around this one, and a closure "+
						"cannot take its address", id.Name)
				}
			}
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
		// A library call that files its argument away keeps it past
		// this function.
		if name, ok := DottedName(x.Callee); ok && keepsArgument(name) {
			for _, a := range x.Args[min(1, len(x.Args)):] {
				w.escape(a, a, "put into a collection or memory")
			}
		}
	case *ListLit:
		for _, el := range x.Elems {
			w.expr(el)
			w.escape(el, el, "put in a list")
		}
	case *MapLit:
		for i := range x.Keys {
			w.expr(x.Keys[i])
			w.expr(x.Vals[i])
			w.escape(x.Vals[i], x.Vals[i], "put in a map")
		}
	case *StructLit:
		for _, v := range x.Vals {
			w.expr(v)
			w.escape(v, v, "stored in a struct")
		}
	case *NewExpr:
		w.expr(x.Count)
		if x.Lit != nil {
			w.expr(x.Lit)
		}
	case *FuncLit:
		// A closure can outlive the call that made it, so it must not
		// hold on to an address here - it has its own check for its own
		// variables.
		if w.report {
			for name := range namesIn(x.Decl.Body) {
				if w.tainted[name] {
					w.c.ErrorAt(x, "this closure uses %s, which holds the address of a local variable "+
						"that is gone once the function returns", name)
				}
			}
		}
	}
}

// keepsArgument reports a library call that stores its arguments after
// the first: pushing onto a list, writing a word into memory.
func keepsArgument(name string) bool {
	switch name {
	case "push", "insert", "mem.write64":
		return true
	}
	return false
}

// namesIn is every name a block mentions, for the closure check.
func namesIn(b *Block) map[string]bool {
	out := map[string]bool{}
	var expr func(e Expr)
	var stmt func(s Stmt)
	expr = func(e Expr) {
		switch x := e.(type) {
		case *Ident:
			out[x.Name] = true
		case *Unary:
			expr(x.X)
		case *Binary:
			expr(x.L)
			expr(x.R)
		case *Cast:
			expr(x.X)
		case *Widen:
			expr(x.X)
		case *Try:
			expr(x.X)
		case *Field:
			expr(x.X)
		case *Index:
			expr(x.X)
			expr(x.Idx)
		case *Interp:
			for _, p := range x.Parts {
				expr(p.X)
			}
		case *Call:
			expr(x.Callee)
			for _, a := range x.Args {
				expr(a)
			}
		case *ListLit:
			for _, el := range x.Elems {
				expr(el)
			}
		case *MapLit:
			for i := range x.Keys {
				expr(x.Keys[i])
				expr(x.Vals[i])
			}
		case *StructLit:
			for _, v := range x.Vals {
				expr(v)
			}
		case *FuncLit:
			for n := range namesIn(x.Decl.Body) {
				out[n] = true
			}
		}
	}
	stmt = func(s Stmt) {
		switch st := s.(type) {
		case *LetStmt:
			expr(st.Value)
		case *AssignStmt:
			expr(st.Target)
			expr(st.Value)
		case *ExprStmt:
			expr(st.X)
		case *ReturnStmt:
			expr(st.Value)
		case *IfStmt:
			expr(st.Cond)
			stmt(st.Then)
			if st.Else != nil {
				stmt(st.Else)
			}
		case *WhileStmt:
			expr(st.Cond)
			stmt(st.Body)
		case *ForStmt:
			for _, e := range []Expr{st.Start, st.End, st.Step, st.Coll} {
				expr(e)
			}
			stmt(st.Body)
		case *MatchStmt:
			expr(st.Subject)
			for _, arm := range st.Cases {
				for _, v := range arm.Values {
					expr(v)
				}
				stmt(arm.Body)
			}
			if st.Else != nil {
				stmt(st.Else)
			}
		case *DeferStmt:
			stmt(st.Body)
		case *Block:
			if st != nil {
				for _, s := range st.Stmts {
					stmt(s)
				}
			}
		}
	}
	stmt(b)
	return out
}

// markAddressed flags the let statements of the names whose address is
// taken, outside any closure (a closure is its own function).
func markAddressed(list []Stmt, names map[string]bool) {
	var stmt func(s Stmt)
	stmt = func(s Stmt) {
		switch st := s.(type) {
		case *LetStmt:
			if names[st.Name] {
				st.Addressed = true
			}
		case *IfStmt:
			stmt(st.Then)
			if st.Else != nil {
				stmt(st.Else)
			}
		case *WhileStmt:
			stmt(st.Body)
		case *ForStmt:
			if st.Lowered != nil {
				stmt(st.Lowered)
			}
			if st.Body != nil {
				stmt(st.Body)
			}
		case *MatchStmt:
			if st.Lowered != nil {
				stmt(st.Lowered)
			}
			for _, arm := range st.Cases {
				stmt(arm.Body)
			}
			if st.Else != nil {
				stmt(st.Else)
			}
		case *DeferStmt:
			stmt(st.Body)
		case *Block:
			if st != nil {
				for _, s := range st.Stmts {
					stmt(s)
				}
			}
		}
	}
	for _, s := range list {
		stmt(s)
	}
}

// markLoopVars records a for loop's variables, in the scope just
// pushed for them, so &i can be refused.
func (c *Checker) markLoopVars(names ...string) {
	if c.loopVars == nil {
		c.loopVars = map[int]map[string]bool{}
	}
	depth := len(c.scopes) - 1
	c.loopVars[depth] = map[string]bool{}
	for _, n := range names {
		if n != "" {
			c.loopVars[depth][n] = true
		}
	}
}
