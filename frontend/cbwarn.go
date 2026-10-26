package frontend

// Warnings for closure callbacks that are never freed.
//
// mem.callback(closure) gives native code an address that keeps the
// closure alive, and takes one of a fixed number of slots, until
// mem.freeCallback(address). Forgetting the free leaks both, so a
// function whose body takes one and never frees it is warned about:
//
//	let cb = mem.callback(fn(a: int) -> int { return a + n })
//	...                                   // no mem.freeCallback(cb)
//
// The check is per function and does not follow paths: one free
// anywhere in the body is enough. An address that leaves the function -
// returned, stored, captured, passed on - may be freed somewhere else,
// so it gets a softer note asking for that instead. A function declared
// with fn needs no slot and is never warned about, and neither is a
// closure passed straight to an extern function, which is freed when
// that call returns.

type cbWalk struct {
	c       *Checker
	locals  map[string]bool
	taken   map[string]*LetStmt // name -> the let that took a callback
	order   []string
	freed   map[string]bool
	escaped map[string]bool
}

// callbackLeaks runs the check over one function body.
func (c *Checker) callbackLeaks(params []Param, body []Stmt) {
	if c.inLibrary() {
		return
	}
	w := &cbWalk{c: c, locals: map[string]bool{}, taken: map[string]*LetStmt{},
		freed: map[string]bool{}, escaped: map[string]bool{}}
	for _, p := range params {
		w.locals[p.Name] = true
	}
	w.stmts(body)
	for _, name := range w.order {
		switch {
		case w.freed[name]:
		case w.escaped[name]:
			c.WarnAt(w.taken[name], "%s holds a closure callback and leaves this function - make sure "+
				"mem.freeCallback runs on it once native code is done with it", name)
		default:
			c.WarnAt(w.taken[name], "%s holds a closure callback that is never freed - call "+
				"mem.freeCallback(%s) once native code is done with it, or it keeps its slot "+
				"until the program ends", name, name)
		}
	}
}

// takesClosure reports a mem.callback call whose argument needs a slot:
// anything but a function declared with fn, named directly.
func (w *cbWalk) takesClosure(e Expr) bool {
	call, ok := e.(*Call)
	if !ok || len(call.Args) != 1 {
		return false
	}
	if name, ok := DottedName(call.Callee); !ok || name != "mem.callback" {
		return false
	}
	if id, ok := call.Args[0].(*Ident); ok && !w.locals[id.Name] {
		if _, isFn := w.c.funcs[id.Name]; isFn {
			return false
		}
	}
	return true
}

func (w *cbWalk) stmts(list []Stmt) {
	for _, s := range list {
		w.stmt(s)
	}
}

func (w *cbWalk) stmt(s Stmt) {
	switch st := s.(type) {
	case *LetStmt:
		if w.takesClosure(st.Value) {
			w.expr(st.Value.(*Call).Args[0])
			if _, seen := w.taken[st.Name]; !seen {
				w.order = append(w.order, st.Name)
			}
			w.taken[st.Name] = st
			delete(w.freed, st.Name)
			delete(w.escaped, st.Name)
		} else {
			w.expr(st.Value)
		}
		w.locals[st.Name] = true
	case *AssignStmt:
		w.expr(st.Target)
		w.value(st.Value)
	case *ExprStmt:
		w.value(st.X)
	case *AsmStmt:
		for _, op := range st.In {
			w.value(op.X)
		}
	case *ReturnStmt:
		if w.takesClosure(st.Value) {
			w.expr(st.Value.(*Call).Args[0])
			w.c.WarnAt(st, "this closure callback leaves the function - make sure mem.freeCallback "+
				"runs on it once native code is done with it")
			return
		}
		w.expr(st.Value)
	case *IfStmt:
		w.expr(st.Cond)
		w.stmts(st.Then.Stmts)
		if st.Else != nil {
			w.stmt(st.Else)
		}
	case *WhileStmt:
		w.expr(st.Cond)
		w.stmts(st.Body.Stmts)
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
		w.stmts(st.Body.Stmts)
	case *MatchStmt:
		w.expr(st.Subject)
		if st.Lowered != nil {
			w.stmt(st.Lowered)
			return
		}
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
		w.stmts(st.Stmts)
	}
}

// value is an expression whose result is used or stored; a callback
// taken there has no name to free it by.
func (w *cbWalk) value(e Expr) {
	if w.takesClosure(e) {
		w.c.WarnAt(e, "this closure callback is never freed, since its address is not kept - "+
			"put it in a variable and call mem.freeCallback on that once native code is done with it")
	}
	w.expr(e)
}

func (w *cbWalk) expr(e Expr) {
	switch x := e.(type) {
	case nil:
	case *Ident:
		if _, ok := w.taken[x.Name]; ok {
			w.escaped[x.Name] = true
		}
	case *Unary:
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
		name, _ := DottedName(x.Callee)
		for i, a := range x.Args {
			tracked := false
			id, isIdent := a.(*Ident)
			if isIdent {
				_, tracked = w.taken[id.Name]
			}
			switch {
			case tracked && name == "mem.freeCallback":
				w.freed[id.Name] = true
			case tracked && i == 0 && (name == "mem.call" || name == "mem.callF"):
				// Calling it is not keeping it.
			default:
				w.value(a)
			}
		}
		w.expr(x.Callee)
	case *ListLit:
		for _, el := range x.Elems {
			w.value(el)
		}
	case *MapLit:
		for i := range x.Keys {
			w.expr(x.Keys[i])
			w.value(x.Vals[i])
		}
	case *StructLit:
		for _, v := range x.Vals {
			w.value(v)
		}
	case *NewExpr:
		w.expr(x.Count)
		if x.Lit != nil {
			w.expr(x.Lit)
		}
	case *FuncLit:
		// A closure that uses the name may free it later, or keep it.
		for n := range namesIn(x.Decl.Body) {
			if _, ok := w.taken[n]; ok {
				w.escaped[n] = true
			}
		}
	}
}
