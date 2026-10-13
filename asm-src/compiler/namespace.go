package main

// Namespaced imports: `import "geometry.vl" as geo`.
//
// A plain import folds a file's names into the program as they are, so
// two files that both export `area` collide. An import with `as` keeps
// them apart: every top-level name of that file becomes `geo.name` -
// `geo.area(2.0)`, `geo.Vec{x: 1.0, y: 2.0}`, `let v: geo.Vec`,
// `geo.TAU` - and nothing of it is reachable bare.
//
// It is done here, on the trees, before anything is checked. The file
// that is imported has its declarations renamed and every reference to
// them inside it rewritten, minding locals that shadow them; the file
// that imports it has `geo.x` rewritten from a field access into the one
// name `geo.x`. From the checker down there is still one flat program,
// whose names happen to contain a dot - the form front.Qual was written
// for - so nothing below this file had to change.
//
// The namespace is the file's, not the importer's: a file imported with
// `as` must be imported under the same name everywhere, and one name
// cannot be given to two files, so `geo.x` means the same thing in every
// file of a program. A name the library already uses (`http`, `os`, ...)
// cannot be an import name.
//
// Extern functions keep their names, since the name is the native
// symbol.

import (
	"strings"

	front "veylfront"
)

// nsWalk rewrites one file.
type nsWalk struct {
	prefix  string          // this file's namespace, or "" when it has none
	own     map[string]bool // its top-level names, renamed to prefix.name
	aliases map[string]bool // namespaces it imported with `as`
	scopes  []map[string]bool
}

// namespaceFile rewrites a parsed file: its own top-level names under
// prefix, when it has one, and its references into the namespaces it
// imports. It runs before the file's own imports are folded in, so only
// the file's own declarations are touched.
func namespaceFile(prog *Program, prefix string) {
	w := &nsWalk{prefix: prefix, own: map[string]bool{}, aliases: map[string]bool{}}
	for _, imp := range prog.Imports {
		if imp.As != "" {
			w.aliases[imp.As] = true
		}
	}
	if prefix == "" && len(w.aliases) == 0 {
		return
	}
	if prefix != "" {
		for _, f := range prog.Funcs {
			if f.Recv == "" && !f.Extern {
				w.own[f.Name] = true
			}
		}
		for _, d := range prog.Structs {
			w.own[d.Name] = true
		}
		for _, e := range prog.Enums {
			w.own[e.Name] = true
		}
		for _, i := range prog.Ifaces {
			w.own[i.Name] = true
		}
		for _, g := range prog.Globals {
			w.own[g.Name] = true
		}
	}

	for _, d := range prog.Structs {
		d.Name = w.declName(d.Name)
		for i := range d.Fields {
			d.Fields[i].Type = w.typeText(d.Fields[i].Type)
		}
		d.Toks = w.toks(d.Toks)
	}
	for _, e := range prog.Enums {
		e.Name = w.declName(e.Name)
		for _, ps := range e.Payloads {
			for i := range ps {
				ps[i].Type = w.typeText(ps[i].Type)
			}
		}
		e.Toks = w.toks(e.Toks)
	}
	for _, i := range prog.Ifaces {
		i.Name = w.declName(i.Name)
		for _, m := range i.Methods {
			w.fnSig(m)
		}
	}
	for _, g := range prog.Globals {
		g.Name = w.declName(g.Name)
		g.Type = w.typeText(g.Type)
		g.Value = w.expr(g.Value)
	}
	for _, f := range prog.Funcs {
		if f.Recv == "" && !f.Extern {
			f.Name = w.declName(f.Name)
		}
		w.fn(f)
	}
	for _, f := range prog.Tests {
		w.fn(f)
	}
	w.push()
	for i, s := range prog.Main {
		prog.Main[i] = w.stmt(s)
	}
	w.pop()
}

func (w *nsWalk) declName(name string) string {
	if w.prefix == "" || strings.Contains(name, ".") {
		return name
	}
	return w.prefix + "." + name
}

func (w *nsWalk) push() { w.scopes = append(w.scopes, map[string]bool{}) }
func (w *nsWalk) pop()  { w.scopes = w.scopes[:len(w.scopes)-1] }

func (w *nsWalk) bind(name string) {
	if len(w.scopes) > 0 && name != "" {
		w.scopes[len(w.scopes)-1][name] = true
	}
}

func (w *nsWalk) bound(name string) bool {
	for i := len(w.scopes) - 1; i >= 0; i-- {
		if w.scopes[i][name] {
			return true
		}
	}
	return false
}

// name is a reference to name, rewritten when it means one of this
// file's own top-level names.
func (w *nsWalk) name(n string) string {
	if w.prefix != "" && w.own[n] && !w.bound(n) {
		return w.prefix + "." + n
	}
	return n
}

// typeText renames this file's own type names inside a written type,
// `[]Vec` or `{str: Box<Vec>}`. Locals cannot shadow a type.
func (w *nsWalk) typeText(s string) string {
	if w.prefix == "" || s == "" {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_') {
			b.WriteByte(c)
			i++
			continue
		}
		j := i
		for j < len(s) && (s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' ||
			s[j] >= '0' && s[j] <= '9' || s[j] == '_' || s[j] == '.') {
			j++
		}
		word := s[i:j]
		if w.own[word] {
			word = w.prefix + "." + word
		}
		b.WriteString(word)
		i = j
	}
	return b.String()
}

// toks rewrites the token copy a generic declaration keeps to
// instantiate from: own names prefixed, and `geo . x` joined into the
// one name geo.x, which is how the rewritten trees spell it. The tokens
// carry no scopes, so a local in a generic body that shadows a
// top-level name of a namespaced file is renamed with it.
func (w *nsWalk) toks(ts []Token) []Token {
	if len(ts) == 0 {
		return ts
	}
	out := make([]Token, 0, len(ts))
	for i := 0; i < len(ts); i++ {
		t := ts[i]
		if t.Kind == IDENT {
			afterDot := len(out) > 0 && out[len(out)-1].Kind == DOT
			if w.aliases[t.Lex] && !afterDot && i+2 < len(ts) && ts[i+1].Kind == DOT && ts[i+2].Kind == IDENT {
				t.Lex = t.Lex + "." + ts[i+2].Lex
				out = append(out, t)
				i += 2
				continue
			}
			if !afterDot && w.prefix != "" && w.own[t.Lex] {
				t.Lex = w.prefix + "." + t.Lex
			}
		}
		out = append(out, t)
	}
	return out
}

func (w *nsWalk) fnSig(f *FnDecl) {
	if f.Recv != "" {
		f.Recv = w.typeText(f.Recv)
	}
	for i := range f.Params {
		f.Params[i].Type = w.typeText(f.Params[i].Type)
		if f.Params[i].Default != nil {
			f.Params[i].Default = w.expr(f.Params[i].Default)
		}
	}
	f.Ret = w.typeText(f.Ret)
}

func (w *nsWalk) fn(f *FnDecl) {
	if f == nil {
		return
	}
	w.fnSig(f)
	f.Toks = w.toks(f.Toks)
	if f.Body == nil {
		return
	}
	w.push()
	w.bind("self")
	for _, p := range f.Params {
		w.bind(p.Name)
	}
	for i, s := range f.Body.Stmts {
		f.Body.Stmts[i] = w.stmt(s)
	}
	w.pop()
}

func (w *nsWalk) block(b *Block) {
	if b == nil {
		return
	}
	w.push()
	for i, s := range b.Stmts {
		b.Stmts[i] = w.stmt(s)
	}
	w.pop()
}

func (w *nsWalk) stmt(s Stmt) Stmt {
	switch x := s.(type) {
	case *Block:
		w.block(x)
	case *LetStmt:
		x.Type = w.typeText(x.Type)
		x.Value = w.expr(x.Value)
		w.bind(x.Name)
	case *AssignStmt:
		x.Target = w.expr(x.Target)
		x.Value = w.expr(x.Value)
	case *ExprStmt:
		x.X = w.expr(x.X)
	case *IfStmt:
		x.Cond = w.expr(x.Cond)
		w.block(x.Then)
		if x.Else != nil {
			x.Else = w.stmt(x.Else)
		}
	case *WhileStmt:
		x.Cond = w.expr(x.Cond)
		w.block(x.Body)
	case *ReturnStmt:
		x.Value = w.expr(x.Value)
	case *ForStmt:
		x.Start = w.expr(x.Start)
		x.End = w.expr(x.End)
		x.Step = w.expr(x.Step)
		x.Coll = w.expr(x.Coll)
		w.push()
		w.bind(x.Var)
		w.bind(x.Var2)
		w.block(x.Body)
		w.pop()
	case *MatchStmt:
		x.Subject = w.expr(x.Subject)
		for i := range x.Cases {
			c := &x.Cases[i]
			w.push()
			for j, v := range c.Values {
				c.Values[j] = w.pattern(v)
			}
			if c.Body != nil {
				c.Body = w.stmt(c.Body)
			}
			w.pop()
		}
		if x.Else != nil {
			x.Else = w.stmt(x.Else)
		}
	case *DeferStmt:
		if x.Body != nil {
			x.Body = w.stmt(x.Body)
		}
	}
	return s
}

// pattern is one value of a match arm. `Shape.Circle(r)` binds r rather
// than reading it, so the names inside a variant's parentheses are
// declared, not rewritten.
func (w *nsWalk) pattern(e Expr) Expr {
	if c, ok := e.(*Call); ok {
		c.Callee = w.expr(c.Callee)
		for _, a := range c.Args {
			if id, isName := a.(*Ident); isName {
				w.bind(id.Name)
			}
		}
		return c
	}
	return w.expr(e)
}

func (w *nsWalk) expr(e Expr) Expr {
	switch x := e.(type) {
	case nil:
		return nil
	case *Ident:
		x.Name = w.name(x.Name)
	case *Field:
		// geo.area: a name from a file imported as geo, which is one
		// name now rather than a field of something.
		if id, ok := x.X.(*Ident); ok && w.aliases[id.Name] && !w.bound(id.Name) {
			return &Ident{Span: id.Span, Name: id.Name + "." + x.Name}
		}
		x.X = w.expr(x.X)
	case *Call:
		x.Callee = w.expr(x.Callee)
		for i, a := range x.Args {
			x.Args[i] = w.expr(a)
		}
	case *FuncLit:
		w.fn(x.Decl)
	case *front.Try:
		x.X = w.expr(x.X)
	case *front.Widen:
		x.X = w.expr(x.X)
	case *front.Convert:
		x.X = w.expr(x.X)
	case *front.NewExpr:
		x.Type = w.typeText(x.Type)
		x.Count = w.expr(x.Count)
		if x.Lit != nil {
			w.expr(x.Lit)
		}
	case *Unary:
		x.X = w.expr(x.X)
	case *front.Cast:
		x.X = w.expr(x.X)
		x.Type = w.typeText(x.Type)
	case *Binary:
		x.L = w.expr(x.L)
		x.R = w.expr(x.R)
	case *ListLit:
		for i, v := range x.Elems {
			x.Elems[i] = w.expr(v)
		}
	case *MapLit:
		for i := range x.Keys {
			x.Keys[i] = w.expr(x.Keys[i])
			x.Vals[i] = w.expr(x.Vals[i])
		}
	case *Index:
		x.X = w.expr(x.X)
		x.Idx = w.expr(x.Idx)
	case *front.Interp:
		for i := range x.Parts {
			x.Parts[i].X = w.expr(x.Parts[i].X)
		}
	case *StructLit:
		x.Name = w.typeText(x.Name)
		for i, v := range x.Vals {
			x.Vals[i] = w.expr(v)
		}
	}
	return e
}

// libraryNamespace reports whether the library already has functions
// under name, as it does under http or os.
func libraryNamespace(name string) bool {
	p := name + "."
	for k := range sigs {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	for k := range preludeOf {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}
