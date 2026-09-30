package frontend

import (
	"fmt"
	"sort"
	"strings"
)

// Checker is the fourth pipeline stage, between resolve and codegen. The
// resolver answers "does this name exist"; the checker answers "does this
// expression make sense", and records the answer on the AST so codegen
// can emit explicit Go types instead of leaning on Go's inference.
//
// Like every other stage it accumulates errors rather than aborting, and
// every error names Veyl types (str, float) and never Go ones.
type Checker struct {
	file     string
	funcs    map[string]*FnDecl
	structs  map[string]*StructDecl
	enums    map[string]*EnumDecl
	methods  map[string]map[string]*FnDecl // struct name -> method name -> decl
	scopes   []map[string]*Type
	consts   []map[string]bool // parallel to scopes: names declared const
	narrowed []map[string]bool // names proved non-nil, innermost last
	curFn    *FnDecl
	curGlob  *LetStmt            // the global being checked, if any
	globals  map[string]*LetStmt // top-level const and var, by name
	Errors   []string

	// lib is the backend's set of builtins. The checker never assumes
	// which backend it is serving; see library.go.
	lib Library

	// Generics; see generics.go. Templates by name, the instances whose
	// bodies are still to be checked, and which instance, if any, the
	// code being checked belongs to.
	prog       *Program
	genFuncs   map[string]*FnDecl
	genStructs map[string]*StructDecl
	genMethods map[string][]*FnDecl
	genEnums   map[string]*EnumDecl
	// genMethodTmpl holds methods with type parameters of their own, by
	// the struct they are on, then by name.
	genMethodTmpl map[string]map[string]*FnDecl
	ifaces        map[string]*InterfaceDecl
	dispatch      []*FnDecl
	pending       []*FnDecl
	matchCount    int
	instances     int
	instName      string
	instFile      string
}

func NewChecker(file string, lib Library) *Checker {
	if lib == nil {
		lib = EmptyLibrary{}
	}
	return &Checker{
		file:    file,
		lib:     lib,
		funcs:   map[string]*FnDecl{},
		structs: map[string]*StructDecl{},
		enums:   map[string]*EnumDecl{},
		methods: map[string]map[string]*FnDecl{},
	}
}

// fieldType looks up one field of a struct.
func (c *Checker) fieldType(structName, field string) (*Type, bool) {
	d, ok := c.structs[structName]
	if !ok {
		return nil, false
	}
	for _, f := range d.Fields {
		if f.Name == field {
			return f.T, true
		}
	}
	return nil, false
}

// fieldNames lists a struct's fields, for error messages.
func (c *Checker) fieldNames(structName string) string {
	d, ok := c.structs[structName]
	if !ok {
		return ""
	}
	names := make([]string, 0, len(d.Fields))
	for _, f := range d.Fields {
		names = append(names, f.Name)
	}
	for m := range c.methods[structName] {
		names = append(names, m+"()")
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// pkg is the namespace of the code being checked. A bare call inside a
// package means that package's own function, so lookups qualify with
// this before giving up.
func (c *Checker) pkg() string {
	if c.curFn != nil {
		return c.curFn.Pkg
	}
	return ""
}

func (c *Checker) ErrorAt(n Node, format string, args ...any) {
	line, col := n.Pos()
	msg := fmt.Sprintf(format, args...)
	if c.instName != "" {
		msg += " (in " + c.instName + ")"
	}
	c.Errors = append(c.Errors, fmt.Sprintf("%s:%d:%d: %s", c.useFile(), line, col, msg))
}

// useFile is the file the code being checked is in: the function's, the
// global's, or, for the top-level statements, the main file. Imported
// declarations carry their own, so an error inside one names the file
// it is actually in.
func (c *Checker) useFile() string {
	switch {
	case c.instFile != "":
		return c.instFile
	case c.curFn != nil && c.curFn.File != "":
		return c.curFn.File
	case c.curGlob != nil && c.curGlob.File != "":
		return c.curGlob.File
	}
	return c.file
}

// private reports whether a declaration from another file is being used
// without being pub. The prelude is the compiler's own and is exempt.
func (c *Checker) private(declFile string, pub bool) bool {
	if pub || declFile == "" || declFile == "<prelude>" {
		return false
	}
	return declFile != c.useFile()
}

func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// ---- scopes ----

func (c *Checker) push() {
	c.scopes = append(c.scopes, map[string]*Type{})
	c.consts = append(c.consts, map[string]bool{})
}

func (c *Checker) pop() {
	c.scopes = c.scopes[:len(c.scopes)-1]
	c.consts = c.consts[:len(c.consts)-1]
}

// isConst reports whether the innermost declaration of a name is a const.
func (c *Checker) isConst(name string) bool {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if _, ok := c.scopes[i][name]; ok {
			return c.consts[i][name]
		}
	}
	return false
}

// ---- nil narrowing ----
//
// Inside `if x != nil { ... }`, x is known not to be nil, so it can be
// used as a plain T. The set of names currently proved non-nil is kept
// as a stack of frames matching the block structure, and every Ident
// that reads one is marked so codegen knows to dereference it.
//
// This is deliberately syntactic: it understands `x != nil`, `x == nil`
// and && chains of them, and nothing cleverer. A narrowing that only
// fires in obvious cases is easy to predict; one that sometimes fires
// is worse than none.

func (c *Checker) pushNarrow(names []string) {
	frame := map[string]bool{}
	for _, n := range names {
		frame[n] = true
	}
	c.narrowed = append(c.narrowed, frame)
}

func (c *Checker) popNarrow() { c.narrowed = c.narrowed[:len(c.narrowed)-1] }

func (c *Checker) isNarrowed(name string) bool {
	for i := len(c.narrowed) - 1; i >= 0; i-- {
		if c.narrowed[i][name] {
			return true
		}
	}
	return false
}

// nilChecks collects the names a condition proves non-nil when true,
// and the names it proves non-nil when false.
func (c *Checker) nilChecks(e Expr) (whenTrue, whenFalse []string) {
	b, ok := e.(*Binary)
	if !ok {
		return nil, nil
	}
	switch b.Op {
	case AND:
		// Both sides must hold, so both sides' guarantees apply. Nothing
		// is learned from the false branch: either side could be what
		// failed.
		lt, _ := c.nilChecks(b.L)
		rt, _ := c.nilChecks(b.R)
		return append(lt, rt...), nil
	case OR:
		// Mirror image: the false branch means both sides were false.
		_, lf := c.nilChecks(b.L)
		_, rf := c.nilChecks(b.R)
		return nil, append(lf, rf...)
	case NEQ, EQ:
		name, isNilTest := nilComparison(b)
		if !isNilTest {
			return nil, nil
		}
		t := c.lookup(name)
		if !t.IsNullable() {
			return nil, nil
		}
		if b.Op == NEQ {
			return []string{name}, nil
		}
		return nil, []string{name}
	}
	return nil, nil
}

// nilComparison recognises `x != nil` and `nil != x`.
func nilComparison(b *Binary) (string, bool) {
	if id, ok := b.L.(*Ident); ok {
		if _, isNil := b.R.(*NilLit); isNil {
			return id.Name, true
		}
	}
	if id, ok := b.R.(*Ident); ok {
		if _, isNil := b.L.(*NilLit); isNil {
			return id.Name, true
		}
	}
	return "", false
}

// coerce checks a value against an expected type and, where the value
// is a plain T going into a ?T, rewrites the expression in place to box
// it. Returns false if the types are simply incompatible.
func (c *Checker) coerce(slot *Expr, want *Type, got *Type) bool {
	if want == nil || want.IsUnknown() || got.IsUnknown() {
		return true
	}
	// A struct where an interface is wanted becomes that interface.
	if t, converted := c.toInterface(slot, want, got); converted {
		if t.IsUnknown() {
			return true // reported, with the reason
		}
		got = t
	}
	// An untyped literal becomes the fixed-width value it names, if it
	// fits; see fixed.go.
	if w := innerScalar(want); w.IsFixed() && !got.IsFixed() && isUntypedConst(*slot, w) {
		if !c.fitLiteral(slot, w) {
			return true // reported
		}
		got = w
	}
	if !want.Accepts(got) && !(IsUntypedInt(*slot) && innerScalar(want).Kind == KFloat) {
		return false
	}
	if !want.NeedsWrap(got) {
		return true
	}
	// Wrapping composes: putting an int into a ?int! boxes it into a
	// ?int first, then marks that as a success. Doing only the outer
	// layer would produce an int! where a ?int! was wanted.
	if want.IsResult() {
		c.coerce(slot, want.Elem, got)
	}
	line, col := (*slot).Pos()
	*slot = &Widen{Span: Span{Line: line, Col: col}, X: *slot, T: want}
	return true
}

// Coerce is coerce for a backend's own builtin checks: it converts a
// value where the type wanted calls for it, and reports whether the
// value fits.
func (c *Checker) Coerce(slot *Expr, want, got *Type) bool { return c.coerce(slot, want, got) }

// innerScalar strips every layer of ? and ! to reach the type actually
// being carried, so an untyped integer literal can still find the float
// inside a ?float!.
func innerScalar(t *Type) *Type {
	for t != nil && (t.Kind == KNullable || t.Kind == KResult) {
		t = t.Elem
	}
	return t
}

func (c *Checker) define(name string, t *Type) {
	c.scopes[len(c.scopes)-1][name] = t
	c.consts[len(c.consts)-1][name] = false
}

func (c *Checker) lookup(name string) *Type {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if t, ok := c.scopes[i][name]; ok {
			return t
		}
	}
	return nil
}

// resolveAnnotation turns a written `: type` into a Type, reporting a
// clear error if it names something that does not exist.
func (c *Checker) resolveAnnotation(text string, n Node) *Type {
	if text == "" {
		return nil
	}
	t := ParseType(text)
	if t == nil {
		c.ErrorAt(n, "unknown type %q", text)
		return Unknown
	}
	t = c.enumTypes(t)
	if bad := c.undeclaredStruct(t, n); bad != "" {
		if hint := c.genericHint(bad); hint != "" {
			c.ErrorAt(n, "%s", hint)
		} else {
			c.ErrorAt(n, "unknown type %q", bad)
		}
		return Unknown
	}
	if t.IsPtr() {
		if bad := c.ptrTarget(t); bad != "" {
			c.ErrorAt(n, "%s", bad)
			return Unknown
		}
	}
	return t
}

// undeclaredStruct returns the name of the first struct inside a type
// that was never declared, so `[]Widget` reports Widget rather than the
// whole type.
//
// A generic struct's instance, Box<int>, is made here the first time a
// type names it.
func (c *Checker) undeclaredStruct(t *Type, n Node) string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case KStruct:
		if _, ok := c.structs[t.Name]; !ok && !c.instStruct(t.Name, n) {
			return t.Name
		}
	case KList:
		return c.undeclaredStruct(t.Elem, n)
	case KPtr:
		if t.Elem != nil {
			return c.undeclaredStruct(t.Elem, n)
		}
	case KMap:
		if bad := c.undeclaredStruct(t.Key, n); bad != "" {
			return bad
		}
		return c.undeclaredStruct(t.Elem, n)
	case KNullable, KResult:
		if t.Elem != nil && strings.Contains(t.Elem.String(), "<") {
			return c.undeclaredStruct(t.Elem, n)
		}
	case KFunc:
		for _, p := range t.Params {
			if strings.Contains(p.String(), "<") {
				if bad := c.undeclaredStruct(p, n); bad != "" {
					return bad
				}
			}
		}
		if t.Elem != nil && strings.Contains(t.Elem.String(), "<") {
			return c.undeclaredStruct(t.Elem, n)
		}
	}
	return ""
}

// ---- entry point ----

func (c *Checker) Check(p *Program) {
	c.prog = p
	c.collectTemplates(p)

	// Pass 0: register struct names before resolving anything, so a field
	// may refer to a struct declared further down the file - including
	// itself, through a list.
	for _, d := range p.Structs {
		c.structs[d.Name] = d
	}
	for _, e := range p.Enums {
		c.declareEnum(e)
	}
	c.ifaces = map[string]*InterfaceDecl{}
	for _, d := range p.Ifaces {
		c.declareInterface(d)
	}
	for _, d := range p.Ifaces {
		c.declareIfaceMethods(d)
	}
	// A data enum is a struct from here on, which a backend lowers as
	// one, so it leaves the list of enums that are ints.
	plain := p.Enums[:0:0]
	for _, e := range p.Enums {
		if !e.Data {
			plain = append(plain, e)
		}
	}
	p.Enums = plain
	for _, d := range p.Structs {
		if d.Extern {
			c.layout(d, map[string]bool{})
			continue
		}
		c.resolveFields(d)
	}
	for _, f := range p.Funcs {
		if f.Recv == "" || f.Instance {
			continue
		}
		if c.methods[f.Recv] == nil {
			c.methods[f.Recv] = map[string]*FnDecl{}
		}
		c.methods[f.Recv][f.Name] = f
	}

	// Scope 0 is the globals, so a function body can see them.
	c.push()
	c.globals = map[string]*LetStmt{}
	for _, g := range p.Globals {
		c.curGlob = g
		c.stmt(g)
		c.curGlob = nil
		c.globals[g.Name] = g
	}

	// Pass 1: resolve every signature before checking any body, so calls
	// to functions declared later in the file type-check correctly.
	for _, f := range p.Funcs {
		if !f.Instance {
			c.resolveSig(f)
		}
	}

	// Pass 2: each function body, in its own scope. An extern has no
	// body to check - it names code that lives outside the program.
	for _, f := range p.Funcs {
		if f.Extern || f.Instance {
			continue
		}
		c.checkFn(f)
	}

	// Pass 3: the top-level statements, which become main().
	c.curFn = nil
	c.push()
	c.stmts(p.Main)
	c.pop()

	// Last, the generic instances all that asked for, in the scope of
	// the globals alone.
	c.checkPending()

	// Every struct used as an interface is known now, so the interface
	// methods, which call each one's, can be written and checked.
	c.writeDispatch()
	c.checkPending()
	c.pop() // globals
}

// resolveFields gives each field of a struct its type.
func (c *Checker) resolveFields(d *StructDecl) {
	for i := range d.Fields {
		f := &d.Fields[i]
		f.T = c.resolveAnnotation(f.Type, f)
		if f.T == nil {
			f.T = Unknown
		}
		// A struct cannot contain itself by value: the type would need
		// infinite space. Through a list or map it is fine.
		// A data enum's variant can hold another of the enum, Mul(a:
		// Expr, b: Expr): only the variant built holds anything.
		if f.T.Kind == KStruct && f.T.Name == d.Name && d.Enum == nil {
			c.ErrorAt(f, "%s cannot contain itself - use []%s if you meant a list of them",
				d.Name, d.Name)
			f.T = Unknown
		}
	}
}

// resolveSig gives a function's parameters and result their types, and
// files it under its name.
func (c *Checker) resolveSig(f *FnDecl) {
	// The entry point is the top-level statements, which each backend
	// wraps in a generated main. A function the author named main
	// collides with it: the Go backend refuses the generated file
	// with "main redeclared", and the assembly one linked the empty
	// wrapper over the real body and ran nothing. Saying it here
	// points at the author's line instead.
	if f.Name == "main" && f.Recv == "" {
		c.ErrorAt(f, "a program starts at its top-level statements, which are already "+
			"called main - rename this function or move its body to the top level")
	}
	for i := range f.Params {
		prm := &f.Params[i]
		// `self` takes its type from the impl block, not an annotation.
		if i == 0 && f.Recv != "" && prm.Name == "self" {
			prm.T = StructOf(f.Recv)
			continue
		}
		prm.T = c.resolveAnnotation(prm.Type, prm)
		if prm.T == nil {
			prm.T = Unknown
		}
	}
	if f.Ret == "" {
		f.RetT = Void
	} else {
		f.RetT = c.resolveAnnotation(f.Ret, f)
	}
	if f.Export {
		c.checkExportDecl(f)
	}
	if f.Extern {
		c.checkExternDecl(f)
	} else if f.Variadic {
		c.ErrorAt(f, "'...' is only allowed on an extern declaration")
	}
	if f.Recv == "" {
		c.funcs[Qual(f.Pkg, f.Name)] = f
	}
	c.checkDefaults(f)
}

func (c *Checker) checkFn(f *FnDecl) {
	prev := c.curFn
	c.curFn = f
	// A function body sees the globals and its own parameters, and
	// nothing from the implicit main.
	c.push()
	for _, prm := range f.Params {
		c.define(prm.Name, prm.T)
	}
	c.stmts(f.Body.Stmts)
	c.pop()
	c.curFn = prev
}

// checkExternDecl validates the shape of an extern declaration. Only
// scalars cross the C boundary - a list or struct has no layout foreign
// code could agree on, and a result type would have nowhere to put its
// failure half.
func (c *Checker) checkExternDecl(f *FnDecl) {
	if f.Body != nil {
		c.ErrorAt(f, "extern %s cannot have a body", f.Name)
	}
	if f.Recv != "" {
		c.ErrorAt(f, "%s cannot be an extern method - methods run as Veyl code", f.Name)
	}
	for i := range f.Params {
		prm := &f.Params[i]
		if prm.Name == "self" {
			c.ErrorAt(prm, "extern %s cannot take self", f.Name)
			continue
		}
		if prm.T != nil && prm.T.Kind == KFunc {
			c.checkCallbackType(prm, f.Name)
			continue
		}
		if !externScalar(prm.T) && !c.isExternStruct(prm.T) && prm.T.Kind != KBytes {
			c.ErrorAt(prm, "extern parameter %q must be int, float, str, bool or ptr - %s cannot cross into native code",
				prm.Name, prm.T)
		}
	}
	if f.RetT != nil && f.RetT != Void && !externScalar(f.RetT) && !c.isExternStruct(f.RetT) {
		c.ErrorAt(f, "extern return type must be int, float, str, bool or ptr - %s cannot come back from native code",
			f.RetT)
	}
}

// externScalar reports whether t is one of the types that can cross the
// native boundary. Unknown suppresses: the real error was already
// reported where the annotation failed to resolve.
func externScalar(t *Type) bool {
	if t == nil || t.IsUnknown() {
		return true
	}
	switch t.Kind {
	case KInt, KFloat, KStr, KBool, KPtr, KFixed:
		return true
	}
	return false
}

// ---- statements ----

func (c *Checker) stmt(s Stmt) {
	switch st := s.(type) {

	case *DeferStmt:
		// A deferred statement runs on the way out, so it cannot itself
		// be a way out: a return in one would say where to go while
		// already going somewhere.
		if leavesVia(st.Body) {
			c.ErrorAt(st, "a deferred statement cannot return, break or continue")
		}
		if _, isLet := st.Body.(*LetStmt); isLet {
			c.ErrorAt(st, "a deferred let would declare a name nothing after it can see")
		}
		c.stmt(st.Body)

	case *LetStmt:
		annot := c.resolveAnnotation(st.Type, st)
		valT := c.exprWant(st.Value, annot)

		switch {
		case annot == nil:
			// No annotation: infer, but reject types that carry no value.
			switch valT.Kind {
			case KVoid:
				c.ErrorAt(st, "cannot assign the result of a call that returns nothing to %q", st.Name)
				valT = Unknown
			case KNilLit:
				c.ErrorAt(st, "cannot tell what %q can hold - annotate it, as in: let %s: ?int = nil",
					st.Name, st.Name)
				valT = Unknown
			}
			st.T = valT

		case c.coerce(&st.Value, annot, valT):
			st.T = annot

		default:
			c.ErrorAt(st, "%q is declared as %s but the value is %s", st.Name, annot, valT)
			st.T = annot
		}
		c.define(st.Name, st.T)
		if st.Const {
			c.consts[len(c.consts)-1][st.Name] = true
		}

	case *AssignStmt:
		if id, ok := st.Target.(*Ident); ok && c.isConst(id.Name) {
			c.expr(st.Value)
			c.ErrorAt(st, "cannot assign to %q because it was declared const", id.Name)
			return
		}
		if _, ok := c.arrayField(st.Target); ok {
			c.expr(st.Target)
			c.expr(st.Value)
			c.ErrorAt(st, "%s is an array, so reading it gives a pointer to its first element and "+
				"it cannot be assigned - write one element, as in %s[0] = ...",
				describeTarget(st.Target), exprText(st.Target))
			return
		}
		want := c.expr(st.Target)
		valT := c.exprWant(st.Value, want)
		if want.IsUnknown() {
			return // already reported
		}
		if st.Op != ASSIGN {
			c.checkCompound(st, want, valT)
			return
		}
		if !c.coerce(&st.Value, want, valT) {
			c.ErrorAt(st, "cannot assign %s to %s, which is %s",
				valT, describeTarget(st.Target), want)
		}

	case *ExprStmt:
		c.expr(st.X)

	case *IfStmt:
		c.condition(st.Cond, "an if")
		whenTrue, whenFalse := c.nilChecks(st.Cond)
		c.pushNarrow(whenTrue)
		c.block(st.Then)
		c.popNarrow()
		if st.Else != nil {
			c.pushNarrow(whenFalse)
			c.stmt(st.Else)
			c.popNarrow()
		}

	case *WhileStmt:
		c.condition(st.Cond, "a while")
		whenTrue, _ := c.nilChecks(st.Cond)
		c.pushNarrow(whenTrue)
		c.block(st.Body)
		c.popNarrow()

	case *ForStmt:
		if st.Coll != nil {
			c.forEach(st)
			return
		}
		startT := c.expr(st.Start)
		endT := c.expr(st.End)
		for _, pair := range []struct {
			t *Type
			n Node
		}{{startT, st.Start}, {endT, st.End}} {
			if !pair.t.IsUnknown() && pair.t.Kind != KInt {
				c.ErrorAt(pair.n, "a for-loop range must be int, got %s", pair.t)
			}
		}
		if st.Step != nil {
			if stepT := c.expr(st.Step); !stepT.IsUnknown() && stepT.Kind != KInt {
				c.ErrorAt(st.Step, "a for-loop step must be int, got %s", stepT)
			}
		}
		c.push()
		c.define(st.Var, Int)
		c.stmts(st.Body.Stmts)
		c.pop()

	case *ReturnStmt:
		if c.curFn == nil {
			return // the resolver already reported this
		}
		want := c.curFn.RetT
		if st.Value == nil {
			return // resolver already checked that a value is present when needed
		}
		got := c.exprWant(st.Value, want)
		if want.Kind == KVoid {
			return // resolver already reported the mismatch
		}
		if !c.coerce(&st.Value, want, got) {
			c.ErrorAt(st, "function %q returns %s but this returns %s",
				c.curFn.Name, want, got)
		}

	case *MatchStmt:
		c.match(st)

	case *Block:
		c.block(st)
	}
}

// match checks that every arm compares against the same type as the
// subject, and that the subject is something comparable at all.
func (c *Checker) match(st *MatchStmt) {
	subj := c.expr(st.Subject)
	if e := c.dataEnumOf(subj); e != nil {
		c.matchData(st, subj, e)
		return
	}
	if !subj.IsUnknown() && (subj.IsCollection() || subj.Kind == KStruct) {
		c.ErrorAt(st.Subject, "cannot match on %s - match compares values, so it needs an int, float, str, bool or enum", subj)
		subj = Unknown
	}
	handled := map[string]bool{}

	seen := map[string]bool{}
	for _, arm := range st.Cases {
		for vi, v := range arm.Values {
			got := c.exprWant(v, subj)
			if subj.IsFixed() && !got.IsFixed() && isUntypedConst(v, subj) {
				if c.fitLiteral(&arm.Values[vi], subj) {
					got = subj
				} else {
					continue
				}
			}
			if !subj.Accepts(got) && !(IsUntypedInt(v) && subj.Kind == KFloat) {
				c.ErrorAt(v, "this match is on %s, but this arm compares against %s", subj, got)
				continue
			}
			// Duplicate constants are dead code, and Go rejects them
			// outright in a switch, so catch them here with a better
			// message than the backend would give.
			if key, isConst := constKey(v); isConst {
				if seen[key] {
					c.ErrorAt(v, "this value is already handled by an earlier arm")
				}
				seen[key] = true
			}
			if f, ok := v.(*Field); ok && subj.Kind == KEnum {
				handled[f.Name] = true
			}
		}
		c.stmt(arm.Body)
	}
	if st.Else != nil {
		c.stmt(st.Else)
	} else if subj.Kind == KEnum {
		// Without an else, every variant has to have an arm: adding a
		// variant later then points at each match that forgot it.
		if e := c.enums[subj.Name]; e != nil {
			var missing []string
			for _, v := range e.Variants {
				if !handled[v] {
					missing = append(missing, subj.Name+"."+v)
				}
			}
			if len(missing) > 0 {
				c.ErrorAt(st, "this match on %s does not handle %s - add an arm for each, or an else",
					subj.Name, strings.Join(missing, ", "))
			}
		}
	}
}

// constKey renders a literal arm value so duplicates can be spotted.
// Non-literal arms return false and are not checked.
func constKey(e Expr) (string, bool) {
	switch x := e.(type) {
	case *IntLit:
		return "i" + x.Val, true
	case *FloatLit:
		return "f" + x.Val, true
	case *StrLit:
		return "s" + x.Val, true
	case *BoolLit:
		return fmt.Sprintf("b%t", x.Val), true
	case *Field:
		if id, ok := x.X.(*Ident); ok {
			return "e" + id.Name + "." + x.Name, true
		}
	}
	return "", false
}

// forEach checks `for x in list` and `for k, v in map`, binding the
// loop variables to the collection's element types.
func (c *Checker) forEach(st *ForStmt) {
	collT := c.expr(st.Coll)
	st.CollT = collT
	if collT.Kind == KStruct && strings.HasPrefix(collT.Name, "Channel<") {
		c.forChannel(st, collT)
		return
	}

	var keyT, valT *Type
	switch {
	case collT.IsUnknown():
		keyT, valT = Unknown, Unknown

	case collT.Kind == KList:
		if st.Var2 != "" {
			// Two names over a list means index and element.
			keyT, valT = Int, collT.Elem
		} else {
			keyT = collT.Elem
		}

	case collT.Kind == KMap:
		if st.Var2 == "" {
			c.ErrorAt(st, "iterating a map binds two names, as in: for key, value in %s { ... }",
				exprText(st.Coll))
			keyT = collT.Key
		} else {
			keyT, valT = collT.Key, collT.Elem
		}

	case collT.Kind == KStr:
		c.ErrorAt(st, "cannot iterate a str directly - use chars(...) or split(...)")
		keyT, valT = Unknown, Unknown

	default:
		c.ErrorAt(st, "cannot iterate %s", collT)
		keyT, valT = Unknown, Unknown
	}

	c.push()
	c.define(st.Var, keyT)
	if st.Var2 != "" {
		c.define(st.Var2, valT)
	}
	c.stmts(st.Body.Stmts)
	c.pop()
}

func (c *Checker) block(b *Block) {
	c.push()
	c.stmts(b.Stmts)
	c.pop()
}

// condition enforces that if/while take a real bool. This is the check
// that turns `if 5 { }` - legal in C, a silent bug everywhere - into a
// compile error.
func (c *Checker) condition(e Expr, kw string) {
	t := c.expr(e)
	if !t.IsUnknown() && t.Kind != KBool {
		c.ErrorAt(e, "%s condition must be bool, got %s", kw, t)
	}
}

// checkCompound validates `+=` and friends, which are just the binary
// operator followed by an assignment.
func (c *Checker) checkCompound(st *AssignStmt, want, got *Type) {
	op := CompoundOp[st.Op]
	target := describeTarget(st.Target)

	if want.IsPtr() {
		if (op != PLUS && op != MINUS) || (!got.IsUnknown() && got.Kind != KInt) {
			c.ErrorAt(st, "a pointer only moves by an int, with += or -=")
		}
		return
	}

	if want.IsFixed() {
		c.fixedCompound(st, op, want, got)
		return
	}

	// The bitwise family and %= are int-only, like their binary forms.
	switch op {
	case PERCENT, AMP, PIPE, CARET, SHL, SHR:
		if !want.IsUnknown() && want.Kind != KInt {
			c.ErrorAt(st, "%s needs an int, but %s is %s", AssignOpText(st.Op), target, want)
		}
		if !got.IsUnknown() && got.Kind != KInt {
			c.ErrorAt(st, "%s needs an int, got %s", AssignOpText(st.Op), got)
		}
		return
	}

	if op == PLUS && want.Kind == KStr {
		if got.Kind != KStr && !got.IsUnknown() {
			c.ErrorAt(st, "cannot append %s to %s, which is str", got, target)
		}
		return
	}
	if got.IsFixed() && want.IsNumeric() {
		c.ErrorAt(st, "cannot apply %s with %s to %s, which is %s - convert it, as in %s(...)",
			AssignOpText(st.Op), got, target, want, want)
		return
	}
	if !want.IsNumeric() && !want.IsUnknown() {
		c.ErrorAt(st, "%s needs a number, but %s is %s", AssignOpText(st.Op), target, want)
		return
	}
	if !got.IsNumeric() && !got.IsUnknown() {
		c.ErrorAt(st, "%s needs a number, got %s", AssignOpText(st.Op), got)
		return
	}
	// An untyped integer literal adapts to a float target, as in Go.
	if want.Kind == KFloat && got.Kind == KInt && !IsUntypedInt(st.Value) {
		c.ErrorAt(st, "cannot apply %s with an int to %s, which is float (use float(...))",
			AssignOpText(st.Op), target)
	}
	if want.Kind == KInt && got.Kind == KFloat {
		c.ErrorAt(st, "cannot apply %s with a float to %s, which is int (use int(...))",
			AssignOpText(st.Op), target)
	}
}

// ---- expressions ----

// exprWant is expr with an expected type. The hint matters only for
// empty collection literals, which carry no element type of their own:
// `let xs: []int = []` is the whole reason this exists.
func (c *Checker) exprWant(e Expr, want *Type) *Type {
	if want == nil || want.IsUnknown() {
		return c.expr(e)
	}
	// Option.None where an Option<int> is wanted is that one's None.
	var named Expr
	switch x := e.(type) {
	case *Field:
		named = x.X
	case *Call:
		if f, ok := x.Callee.(*Field); ok {
			named = f.X
		}
	}
	if g := c.genericEnumNamed(named); g != nil && want.Kind == KStruct {
		if base, _, ok := SplitGeneric(want.Name); ok && base == g.Name {
			named.(*Ident).Name = want.Name
		}
	}
	switch x := e.(type) {
	case *ListLit:
		if want.Kind != KList {
			break
		}
		if len(x.Elems) == 0 {
			x.T = want
			return want
		}
		// With an expected element type, use it rather than inferring
		// from the first element. `let xs: []?int = [1, nil, 3]` needs
		// this - inference would read element one as a plain int and then
		// reject nil.
		return c.listLitAs(x, want)

	case *MapLit:
		if want.Kind != KMap {
			break
		}
		if len(x.Keys) == 0 {
			x.T = want
			return want
		}
		return c.mapLitAs(x, want)
	case *Call:
		// A builtin that decodes into a type learns that type from here.
		if name, ok := DottedName(x.Callee); ok {
			if b, isBuiltin := c.lib.Signature(name); isBuiltin && b.WantsTarget {
				x.Want = want
			}
		}
	}
	return c.expr(e)
}

// expr returns the type of an expression, reporting any mismatch inside
// it. It never returns nil: unknown stands in for "already reported".
func (c *Checker) expr(e Expr) *Type {
	switch x := e.(type) {

	case *NilLit:
		return NilLitT

	case *Try:
		return c.try(x)

	case *FuncLit:
		return c.funcLit(x)

	case *Widen:
		// Inserted by this pass; already checked when it was created.
		return x.T

	case *Convert:
		return x.T

	case *NewExpr:
		return c.newExpr(x)

	case *IntLit:
		return Int

	case *FloatLit:
		return Float

	case *StrLit:
		return Str

	case *BoolLit:
		return Bool

	case *Interp:
		for i := range x.Parts {
			if x.Parts[i].X == nil {
				continue
			}
			t := c.expr(x.Parts[i].X)
			x.Parts[i].T = t
			// Interpolation is the one place a value gets used without
			// going through a parameter type, so the result check has to
			// be made here too - otherwise "{load(p)}" prints the wrapper.
			if t.IsResult() {
				c.ErrorAt(x.Parts[i].X,
					"%s might have failed - unwrap it with '?', must(...) or valueOr(...) before printing it", t)
				x.Parts[i].T = Unknown
			}
		}
		return Str

	case *Ident:
		if g := c.globalNamed(x.Name); g != nil && c.private(g.File, g.Pub) {
			kind := "const"
			if !g.Const {
				kind = "var"
			}
			c.ErrorAt(x, "%q is private to %s - mark it 'pub %s %s' to use it from another file",
				x.Name, baseName(g.File), kind, x.Name)
		}
		if t := c.lookup(x.Name); t != nil {
			// Inside a proven `x != nil`, the narrowed binding shadows the
			// nullable one and the use is marked so codegen dereferences.
			if c.isNarrowed(x.Name) {
				x.Narrowed = true
				return t.Unwrap()
			}
			return t
		}
		if t, ok := c.lib.ConstType(x.Name); ok {
			return t
		}
		// A declared function used as a value.
		if f, ok := c.funcs[x.Name]; ok {
			c.checkFnPrivacy(x, f)
			if f.Extern {
				c.ErrorAt(x, "extern %s names native code and cannot be used as a value - call it directly", f.Name)
				return Unknown
			}
			return signatureOf(f)
		}
		return Unknown // the resolver reported the undefined name

	case *Unary:
		switch x.Op {
		case STAR:
			return c.deref(x)
		case AMP:
			x.T = c.addrOf(x)
			return x.T
		}
		t := c.expr(x.X)
		if t.IsUnknown() {
			return Unknown
		}
		if x.Op == BANG {
			if t.Kind != KBool {
				c.ErrorAt(x, "'!' needs a bool, got %s", t)
				return Unknown
			}
			return Bool
		}
		if x.Op == TILDE {
			if t.IsFixedInt() {
				return t
			}
			if t.Kind != KInt {
				c.ErrorAt(x, "'~' needs an int, got %s", t)
				return Unknown
			}
			return Int
		}
		if !t.IsNumeric() && !t.IsFixed() {
			c.ErrorAt(x, "'-' needs a number, got %s", t)
			return Unknown
		}
		return t

	case *Binary:
		t := c.binary(x)
		x.T = t
		return t

	case *Cast:
		return c.cast(x)

	case *Field:
		return c.field(x)

	case *StructLit:
		return c.structLit(x)

	case *ListLit:
		return c.listLit(x)

	case *MapLit:
		return c.mapLit(x)

	case *Index:
		return c.index(x)

	case *Call:
		return c.call(x)
	}
	return Unknown
}

// field types `user.name`. A dotted library path never reaches here -
// the resolver reports those, because they are only valid as a call.
func (c *Checker) field(x *Field) *Type {
	if e := c.enumNamed(x.X); e != nil && e.Data {
		if c.private(e.File, e.Pub) {
			c.ErrorAt(x, "enum %q is private to %s - mark it 'pub enum %s' to use it from another file",
				e.Name, baseName(e.File), e.Name)
		}
		return c.variantValue(x, e)
	}
	if g := c.genericEnumNamed(x.X); g != nil {
		c.ErrorAt(x, "%s is generic - say what it holds, as in %s<int>.%s", g.Name, g.Name, x.Name)
		return Unknown
	}
	if e := c.enumNamed(x.X); e != nil {
		for _, v := range e.Variants {
			if v == x.Name {
				if c.private(e.File, e.Pub) {
					c.ErrorAt(x, "enum %q is private to %s - mark it 'pub enum %s' to use it from another file",
						e.Name, baseName(e.File), e.Name)
				}
				return EnumOf(e.Name)
			}
		}
		c.ErrorAt(x, "%s has no variant %q - it has: %s", e.Name, x.Name, strings.Join(e.Variants, ", "))
		return Unknown
	}
	if d := c.externStructNamed(x.X); d != nil {
		if x.Name == "size" {
			return Int
		}
		c.ErrorAt(x, "%s is an extern struct; %s.size is its size in bytes, and a view of one "+
			"is made with %s(address)", d.Name, d.Name, d.Name)
		return Unknown
	}
	recv := c.expr(x.X)
	if recv.IsUnknown() {
		return Unknown
	}
	// p.hp through a *Player reads the field it points at, which is
	// what C spells p->hp.
	if recv.IsPtr() && recv.Elem != nil && recv.Elem.Kind == KStruct {
		recv = recv.Elem
	}
	if recv.Kind != KStruct {
		c.ErrorAt(x, "%s has no fields, so %q cannot be read from it", recv, x.Name)
		return Unknown
	}
	if d, ok := c.structs[recv.Name]; ok && d.Enum != nil && !strings.HasPrefix(x.Name, "#") &&
		!strings.Contains(x.Name, ".") {
		if d.Enum.Interface {
			c.ErrorAt(x, "%s is an interface - call its methods, or match to get the struct inside", recv.Name)
		} else {
			c.ErrorAt(x, "%s is an enum - a match says which variant it is and names what it holds", recv.Name)
		}
		return Unknown
	}
	if t, ok := c.fieldType(recv.Name, x.Name); ok {
		return t
	}
	if _, isMethod := c.methods[recv.Name][x.Name]; isMethod {
		c.ErrorAt(x, "%s is a method on %s; did you mean %s()?", x.Name, recv.Name, x.Name)
		return Unknown
	}
	c.ErrorAt(x, "%s has no field called %q - it has: %s",
		recv.Name, x.Name, c.fieldNames(recv.Name))
	return Unknown
}

// structLit checks `User{name: "ada"}`. Fields may come in any order,
// and any left out take their zero value - but a name that is not a
// field at all, or given twice, is an error.
func (c *Checker) structLit(x *StructLit) *Type {
	if strings.Contains(x.Name, "<") {
		if t := ParseType(x.Name); t != nil && t.Kind == KStruct {
			x.Name = t.Name
		}
		c.instStruct(x.Name, x)
	}
	d, ok := c.structs[x.Name]
	if !ok {
		if hint := c.genericHint(x.Name); hint != "" {
			c.ErrorAt(x, "%s", hint)
		}
		for i := range x.Vals {
			c.expr(x.Vals[i])
		}
		x.T = Unknown
		return Unknown // the resolver already reported it
	}
	if d.Enum != nil {
		for i := range x.Vals {
			c.expr(x.Vals[i])
		}
		if d.Enum.Interface {
			c.ErrorAt(x, "%s is an interface - a value of it is any struct with its methods", x.Name)
		} else {
			c.ErrorAt(x, "%s is an enum - a value of it is one of its variants, as in %s.%s",
				x.Name, x.Name, d.Enum.Variants[0])
		}
		x.T = Unknown
		return Unknown
	}
	if c.private(d.File, d.Pub) {
		c.ErrorAt(x, "struct %q is private to %s - mark it 'pub struct %s' to use it from another file",
			x.Name, baseName(d.File), x.Name)
	}

	seen := map[string]bool{}
	for i, name := range x.Fields {
		want, isField := c.fieldType(x.Name, name)
		if !isField {
			c.expr(x.Vals[i])
			c.ErrorAt(x.Vals[i], "%s has no field called %q - it has: %s",
				x.Name, name, c.fieldNames(x.Name))
			continue
		}
		if seen[name] {
			c.ErrorAt(x.Vals[i], "field %q is given twice", name)
		}
		if d.Extern {
			if f := externField(d, name); f != nil && f.Len > 0 {
				c.expr(x.Vals[i])
				c.ErrorAt(x.Vals[i], "%s.%s is an array and cannot be given in a literal", x.Name, name)
				seen[name] = true
				continue
			}
		}
		seen[name] = true

		got := c.exprWant(x.Vals[i], want)
		if c.coerce(&x.Vals[i], want, got) {
			continue
		}
		c.ErrorAt(x.Vals[i], "%s.%s is %s, got %s", x.Name, name, want, got)
	}

	// Missing fields are allowed and zero-filled, which is what makes
	// Point{} and Config{debug: true} both reasonable to write.
	_ = d
	x.T = StructOf(x.Name)
	return x.T
}

// callValue checks a call made through a value rather than a declared
// name, and returns what it produces.
func (c *Checker) callValue(x *Call, fnType *Type, what string) *Type {
	if fnType.IsUnknown() {
		return Unknown
	}
	if !fnType.IsFunc() {
		c.ErrorAt(x, "%s is %s, which cannot be called", what, fnType)
		return Unknown
	}
	if len(x.Args) != len(fnType.Params) {
		c.ErrorAt(x, "%s expects %s, got %d",
			what, ArityText(len(fnType.Params), len(fnType.Params)), len(x.Args))
	}
	for i := 0; i < len(x.Args) && i < len(fnType.Params); i++ {
		want := fnType.Params[i]
		if c.coerce(&x.Args[i], want, x.ArgT[i]) {
			continue
		}
		c.ErrorAt(x.Args[i], "%s expects %s for argument %d, got %s",
			what, want, i+1, x.ArgT[i])
	}
	return fnType.Elem
}

// signatureOf builds the function type of a declaration, so it can be
// handed around as a value.
func signatureOf(f *FnDecl) *Type {
	params := make([]*Type, 0, len(f.Params))
	for _, p := range f.Params {
		params = append(params, p.T)
	}
	return FuncOf(params, f.RetT)
}

// funcLit checks an anonymous function and returns its type. The body
// is checked in its own scope, stacked on whatever is visible where the
// literal was written, so it can close over locals.
func (c *Checker) funcLit(x *FuncLit) *Type {
	f := x.Decl
	for i := range f.Params {
		prm := &f.Params[i]
		prm.T = c.resolveAnnotation(prm.Type, prm)
		if prm.T == nil {
			prm.T = Unknown
		}
	}
	if f.Ret == "" {
		f.RetT = Void
	} else {
		f.RetT = c.resolveAnnotation(f.Ret, f)
	}

	prev := c.curFn
	c.curFn = f
	c.push()
	for _, prm := range f.Params {
		c.define(prm.Name, prm.T)
	}
	c.stmts(f.Body.Stmts)
	c.pop()
	c.curFn = prev

	x.T = signatureOf(f)
	return x.T
}

// try checks the postfix `?`. Both sides have to line up: the value has
// to be a result, and the enclosing function has to return one too,
// since that is where the failure goes.
func (c *Checker) try(x *Try) *Type {
	inner := c.expr(x.X)
	if inner.IsUnknown() {
		return Unknown
	}
	if !inner.IsResult() {
		c.ErrorAt(x, "'?' needs a value that can fail, and %s cannot", inner)
		return Unknown
	}
	x.T = inner.Elem

	if c.curFn == nil {
		return x.T // the resolver already reported the misplacement
	}
	if !c.curFn.RetT.IsResult() {
		c.ErrorAt(x, "'?' returns the failure from %q, so %q must return a type ending in '!' - it returns %s",
			c.curFn.Name, c.curFn.Name, c.curFn.RetT)
		return x.T
	}
	return x.T
}

// listLitAs checks a list literal against a known list type.
func (c *Checker) listLitAs(x *ListLit, want *Type) *Type {
	ok := true
	for i := range x.Elems {
		got := c.exprWant(x.Elems[i], want.Elem)
		if c.coerce(&x.Elems[i], want.Elem, got) {
			continue
		}
		c.ErrorAt(x.Elems[i], "this list holds %s, but element %d is %s", want.Elem, i+1, got)
		ok = false
		break
	}
	if !ok {
		x.T = Unknown
		return Unknown
	}
	x.T = want
	return want
}

// mapLitAs checks a map literal against a known map type.
func (c *Checker) mapLitAs(x *MapLit, want *Type) *Type {
	ok := true
	for i := range x.Keys {
		if got := c.expr(x.Keys[i]); !want.Key.Accepts(got) {
			c.ErrorAt(x.Keys[i], "this map has %s keys, but key %d is %s", want.Key, i+1, got)
			ok = false
			break
		}
		got := c.exprWant(x.Vals[i], want.Elem)
		if c.coerce(&x.Vals[i], want.Elem, got) {
			continue
		}
		c.ErrorAt(x.Vals[i], "this map holds %s, but value %d is %s", want.Elem, i+1, got)
		ok = false
		break
	}
	if !ok {
		x.T = Unknown
		return Unknown
	}
	x.T = want
	return want
}

// listLit infers a list type from the elements, which must all agree.
func (c *Checker) listLit(x *ListLit) *Type {
	if len(x.Elems) == 0 {
		c.ErrorAt(x, "cannot tell what kind of list this is - annotate it, as in: let xs: []int = []")
		x.T = Unknown
		return Unknown
	}

	elem := c.expr(x.Elems[0])
	// A list of integer literals mixed with any float becomes a float
	// list, following the same untyped-constant rule as arithmetic.
	if elem.Kind == KInt && IsUntypedInt(x.Elems[0]) && c.anyFloat(x.Elems) {
		elem = Float
	}

	for i := range x.Elems {
		got := c.exprWant(x.Elems[i], elem)
		if c.coerce(&x.Elems[i], elem, got) {
			continue
		}
		c.ErrorAt(x.Elems[i], "this list holds %s, but element %d is %s", elem, i+1, got)
		elem = Unknown
		break
	}

	if elem.IsUnknown() {
		x.T = Unknown
		return Unknown
	}
	x.T = ListOf(elem)
	return x.T
}

// anyFloat reports whether any element is a float, so a list written
// [1, 2.5, 3] becomes []float rather than failing on the first element.
func (c *Checker) anyFloat(elems []Expr) bool {
	for _, el := range elems {
		if _, ok := el.(*FloatLit); ok {
			return true
		}
	}
	return false
}

func (c *Checker) mapLit(x *MapLit) *Type {
	if len(x.Keys) == 0 {
		c.ErrorAt(x, "cannot tell what kind of map this is - annotate it, as in: let m: {str: int} = {}")
		x.T = Unknown
		return Unknown
	}

	keyT := c.expr(x.Keys[0])
	if keyT.Kind != KStr && keyT.Kind != KInt && !keyT.IsUnknown() {
		c.ErrorAt(x.Keys[0], "a map key must be str or int, got %s", keyT)
		keyT = Unknown
	}
	valT := c.expr(x.Vals[0])

	for i := range x.Keys {
		if got := c.expr(x.Keys[i]); !keyT.Accepts(got) {
			c.ErrorAt(x.Keys[i], "this map has %s keys, but key %d is %s", keyT, i+1, got)
			keyT = Unknown
			break
		}
		got := c.exprWant(x.Vals[i], valT)
		if c.coerce(&x.Vals[i], valT, got) {
			continue
		}
		c.ErrorAt(x.Vals[i], "this map holds %s, but value %d is %s", valT, i+1, got)
		valT = Unknown
		break
	}

	if keyT.IsUnknown() || valT.IsUnknown() {
		x.T = Unknown
		return Unknown
	}
	x.T = MapOf(keyT, valT)
	return x.T
}

func (c *Checker) index(x *Index) *Type {
	collT := c.expr(x.X)
	idxT := c.expr(x.Idx)
	x.T = collT

	switch {
	case collT.IsUnknown():
		return Unknown

	case collT.Kind == KList:
		if !idxT.IsUnknown() && !idxT.IsInteger() {
			c.ErrorAt(x.Idx, "a list index must be int, got %s", idxT)
		}
		return collT.Elem

	case collT.Kind == KMap:
		if !collT.Key.Accepts(idxT) {
			c.ErrorAt(x.Idx, "this map has %s keys, got %s", collT.Key, idxT)
		}
		return collT.Elem

	case collT.IsPtr():
		if !idxT.IsUnknown() && !idxT.IsInteger() {
			c.ErrorAt(x.Idx, "a pointer index must be int, got %s", idxT)
		}
		return collT.Pointee()

	case collT.Kind == KStr:
		c.ErrorAt(x, "cannot index a str - use charAt(s, i) or substr(s, a, b)")
		return Unknown

	case collT.Kind == KBytes:
		if !idxT.IsUnknown() && idxT.Kind != KInt {
			c.ErrorAt(x.Idx, "a bytes index must be int, got %s", idxT)
		}
		// One byte, as a number from 0 to 255. There is no separate
		// byte type: a language with int already has somewhere to put
		// a small number, and a second one would infect every
		// arithmetic rule for nothing.
		return Int
	}

	c.ErrorAt(x, "cannot index %s", collT)
	return Unknown
}

// describeTarget names an assignment target for an error message.
func describeTarget(e Expr) string {
	switch t := e.(type) {
	case *Ident:
		return `"` + t.Name + `"`
	case *Field:
		return `"` + t.Name + `"`
	case *Index:
		return "this element"
	}
	return "this target"
}

// exprText renders an expression compactly, for error messages that
// want to quote the user's own code back at them.
func exprText(e Expr) string {
	switch t := e.(type) {
	case *Ident:
		return t.Name
	case *Index:
		return exprText(t.X) + "[...]"
	case *Field:
		return exprText(t.X) + "." + t.Name
	case *Call:
		return exprText(t.Callee) + "(...)"
	}
	return "it"
}

func (c *Checker) binary(x *Binary) *Type {
	// `a && b` proves a before b runs, so anything a establishes is
	// available inside b. Without this, `x != nil && x > 3` would reject
	// its own right-hand side.
	if x.Op == AND || x.Op == OR {
		lt := c.expr(x.L)
		whenTrue, whenFalse := c.nilChecks(x.L)
		if x.Op == AND {
			c.pushNarrow(whenTrue)
		} else {
			c.pushNarrow(whenFalse)
		}
		rt := c.expr(x.R)
		c.popNarrow()

		if lt.IsUnknown() || rt.IsUnknown() {
			return Unknown
		}
		if lt.Kind != KBool || rt.Kind != KBool {
			c.ErrorAt(x, "'%s' needs bool on both sides, got %s and %s",
				OpText(x.Op), lt, rt)
			return Unknown
		}
		return Bool
	}

	lt := c.expr(x.L)
	rt := c.expr(x.R)

	if lt.IsUnknown() || rt.IsUnknown() {
		return Unknown
	}

	// A nullable in any operator other than a nil comparison is the
	// mistake this type exists to catch, so name the fix.
	if x.Op != EQ && x.Op != NEQ {
		if bad := firstNullable(lt, rt); bad != nil {
			c.ErrorAt(x, "%s might be nil - %s", bad, nilAdvice(x))
			return Unknown
		}
	}
	// Same idea for a result: it holds a value only if it did not fail.
	if bad := firstResult(lt, rt); bad != nil {
		c.ErrorAt(x, "%s might have failed - unwrap it with '?', must(...) or valueOr(...) first", bad)
		return Unknown
	}

	if t, handled := c.ptrBinary(x, lt, rt); handled {
		return t
	}
	if t, handled := c.fixedBinary(x, lt, rt); handled {
		return t
	}

	// Untyped integer literals adapt to a float operand, exactly as Go's
	// untyped constants do. This keeps `radius * 2` working without
	// opening the door to implicit conversion between two variables.
	//
	// The originals are kept for error messages: after adaptation both
	// sides of `7 % 2.0` look like floats, and reporting that would send
	// the reader hunting for a float they never wrote.
	origL, origR := lt, rt
	if lt.Kind == KInt && rt.Kind == KFloat && IsUntypedInt(x.L) {
		lt = Float
	}
	if rt.Kind == KInt && lt.Kind == KFloat && IsUntypedInt(x.R) {
		rt = Float
	}

	switch x.Op {

	case EQ, NEQ:
		// Comparing against nil is the whole point of a nullable, and is
		// the one place a nullable may appear without being checked.
		if lt.Kind == KNilLit || rt.Kind == KNilLit {
			other := lt
			if lt.Kind == KNilLit {
				other = rt
			}
			if !other.IsNullable() && other.Kind != KNilLit && !other.IsPtr() {
				c.ErrorAt(x, "%s can never be nil, so this comparison is always %t",
					other, x.Op == NEQ)
				return Bool
			}
			return Bool
		}
		// A struct compared with an interface is converted to it, and
		// equal when the interface holds an equal one.
		if c.ifaceOf(lt) != nil && !lt.Equal(rt) {
			if t, ok := c.toInterface(&x.R, lt, rt); ok {
				rt = t
			}
		} else if c.ifaceOf(rt) != nil && !lt.Equal(rt) {
			if t, ok := c.toInterface(&x.L, rt, lt); ok {
				lt = t
			}
		}
		if lt.IsUnknown() || rt.IsUnknown() {
			return Bool
		}
		if !lt.Equal(rt) {
			c.ErrorAt(x, "cannot compare %s with %s", lt, rt)
			return Unknown
		}
		if lt.IsFunc() {
			c.ErrorAt(x, "cannot compare two functions")
			return Unknown
		}
		// Lists, maps and structs compare by their contents, which is
		// what people mean by == and what the printed form suggests.
		x.OpT = lt
		return Bool

	case LT, LTE, GT, GTE:
		if !lt.Equal(rt) {
			c.ErrorAt(x, "cannot compare %s with %s", lt, rt)
			return Unknown
		}
		if !lt.IsNumeric() && lt.Kind != KStr {
			c.ErrorAt(x, "'%s' needs numbers or strings, got %s", OpText(x.Op), lt)
			return Unknown
		}
		return Bool

	case PLUS:
		if lt.Kind == KStr && rt.Kind == KStr {
			return Str
		}
		if lt.Kind == KStr || rt.Kind == KStr {
			c.ErrorAt(x, "cannot add %s and %s (use \"{...}\" interpolation or str(...))", lt, rt)
			return Unknown
		}
		return c.arithmetic(x, lt, rt)

	case MINUS, STAR, SLASH:
		return c.arithmetic(x, lt, rt)

	case PERCENT:
		// Checked against the originals: `%` does no float promotion, so
		// an untyped literal stays an int here.
		if origL.Kind != KInt || origR.Kind != KInt {
			c.ErrorAt(x, "'%%' needs two ints, got %s and %s (use mod(...) for floats)",
				origL, origR)
			return Unknown
		}
		return Int

	case AMP, PIPE, CARET, SHL, SHR:
		if origL.Kind != KInt || origR.Kind != KInt {
			c.ErrorAt(x, "'%s' works on ints, got %s and %s%s",
				OpText(x.Op), origL, origR, bitwiseHint(x, origL, origR))
			return Unknown
		}
		return Int
	}

	return Unknown
}

func firstNullable(ts ...*Type) *Type {
	for _, t := range ts {
		if t.IsNullable() {
			return t
		}
	}
	return nil
}

func firstResult(ts ...*Type) *Type {
	for _, t := range ts {
		if t.IsResult() {
			return t
		}
	}
	return nil
}

// nilAdvice suggests the fix, naming the variable when there is one to
// name so the suggestion can be pasted as written.
func nilAdvice(x *Binary) string {
	for _, side := range []Expr{x.L, x.R} {
		if id, ok := side.(*Ident); ok {
			return "check it first with 'if " + id.Name + " != nil'"
		}
	}
	return "put it in a variable and check that for nil first"
}

// bitwise operators bind looser than comparison in C, and Veyl copies
// that ladder. `flags & MASK == 0` therefore parses as
// `flags & (MASK == 0)`, which is almost never what was meant. When one
// side turns out to be a bool, say so rather than leaving the reader to
// rediscover a fifty-year-old wart.
func bitwiseHint(x *Binary, lt, rt *Type) string {
	if lt.Kind == KBool || rt.Kind == KBool {
		return " - comparison binds tighter than '" + OpText(x.Op) +
			"', so you probably want parentheses"
	}
	return ""
}

// arithmetic enforces the no-implicit-conversion rule for `- * / +`.
func (c *Checker) arithmetic(x *Binary, lt, rt *Type) *Type {
	if !lt.IsNumeric() || !rt.IsNumeric() {
		c.ErrorAt(x, "'%s' needs numbers, got %s and %s", OpText(x.Op), lt, rt)
		return Unknown
	}
	if !lt.Equal(rt) {
		c.ErrorAt(x, "cannot mix %s and %s in '%s' - convert one with int(...) or float(...)",
			lt, rt, OpText(x.Op))
		return Unknown
	}
	return lt
}

// ---- calls ----

func (c *Checker) call(x *Call) *Type {
	// Shape.Circle(2.0): a data enum's variant, built with its values.
	if fld, ok := x.Callee.(*Field); ok {
		if e := c.enumNamed(fld.X); e != nil && e.Data {
			x.T = c.variantCall(x, fld, e, nil)
			return x.T
		}
		if g := c.genericEnumNamed(fld.X); g != nil {
			e, args := c.inferVariant(x, fld, g)
			if e == nil {
				x.T = Unknown
				return x.T
			}
			x.T = c.variantCall(x, fld, e, args)
			return x.T
		}
	}

	// A generic called with its types named, max<int>(a, b), becomes a
	// call to that instance before anything else looks at it.
	if name, ok := DottedName(x.Callee); ok && strings.Contains(name, "<") {
		if f, generic := c.genericCall(x, name, nil); generic && f == nil {
			for i := range x.Args {
				c.expr(x.Args[i])
			}
			x.T = Unknown
			return x.T
		}
	}

	// Arguments are typed with the parameter type as a hint, so an empty
	// collection literal passed straight to a function knows what it is.
	// That means working out the callee first.
	hints := c.paramHints(x)
	dynamic := c.dynamicHint(x)
	args := make([]*Type, len(x.Args))
	for i := range x.Args {
		var hint *Type
		if i < len(hints) {
			hint = hints[i]
		}
		// A builtin may work out an argument's type from the ones before
		// it, which a fixed parameter list cannot express.
		if dynamic != nil {
			if h := dynamic(x, args[:i], i); h != nil {
				hint = h
			}
		}
		args[i] = c.exprWant(x.Args[i], hint)
	}
	x.ArgT = args

	// A method call, if the callee is a field on something that types as
	// a struct. Library paths are not values, so they never get here.
	if fld, isField := x.Callee.(*Field); isField {
		if recv := c.receiverType(fld); recv != nil {
			x.Method = true
			x.T = c.methodCall(x, fld, recv, args)
			return x.T
		}
	}

	name, ok := DottedName(x.Callee)
	if !ok {
		// Calling whatever an expression evaluates to.
		x.T = c.callValue(x, c.expr(x.Callee), "this")
		x.ViaValue = true
		return x.T
	}

	// A variable holding a function shadows a declaration of the same
	// name, which is ordinary scoping.
	if t := c.lookup(name); t != nil {
		x.ViaValue = true
		x.T = c.callValue(x, t, name)
		return x.T
	}

	// Player(addr) is a view of an extern struct at an address.
	if d, ok := c.structs[name]; ok && d.Extern {
		if len(args) != 1 {
			c.ErrorAt(x, "%s(address) takes one address, got %d arguments", name, len(args))
		} else if !args[0].IsUnknown() && args[0].Kind != KInt {
			c.ErrorAt(x.Args[0], "%s(address) needs an int address, got %s", name, args[0])
		}
		x.T = StructOf(name)
		return x.T
	}

	if b, isBuiltin := c.lib.Signature(name); isBuiltin {
		if b.Check != nil {
			// The same rule Accepts applies on the fixed-signature path:
			// an argument that already failed carries Unknown, and a
			// second error about it is noise that buries the real one.
			// Guarding at the seam rather than inside each custom check
			// means a new one cannot forget to.
			if anyUnknown(args) {
				x.T = Unknown
				return x.T
			}
			x.T = b.Check(c, x, args)
			return x.T
		}
		x.T = c.checkBuiltin(x, name, b, args)
		return x.T
	}

	f, isUser := c.funcs[name]
	if !isUser {
		f, isUser = c.funcs[Qual(c.pkg(), name)]
	}
	if !isUser {
		g, generic := c.genericCall(x, name, args)
		if !generic {
			return Unknown // the resolver reported it
		}
		if g == nil {
			x.T = Unknown
			return x.T
		}
		f, name = g, g.Name
	}
	c.checkFnPrivacy(x, f)
	if len(args) < len(f.Params) {
		args = c.fillDefaults(x, f.Params, args)
	}
	// Arity is the resolver's job; only check the arguments we have.
	n := len(args)
	if n > len(f.Params) {
		n = len(f.Params)
	}
	for i := 0; i < n; i++ {
		want := f.Params[i].T
		if c.coerce(&x.Args[i], want, args[i]) {
			continue
		}
		c.ErrorAt(x.Args[i], "%s expects %s for %q, got %s",
			name, want, f.Params[i].Name, args[i])
	}
	x.T = f.RetT
	return f.RetT
}

// dynamicHint returns a builtin's hintFor hook, if it has one.
func (c *Checker) dynamicHint(x *Call) func(*Call, []*Type, int) *Type {
	name, ok := DottedName(x.Callee)
	if !ok {
		return nil
	}
	if b, isBuiltin := c.lib.Signature(name); isBuiltin {
		return b.HintFor
	}
	return nil
}

// paramHints returns the declared parameter types of whatever this call
// resolves to, so each argument can be checked against the type it is
// going into. Anything unresolvable yields no hints, which is harmless:
// the argument is simply typed on its own.
func (c *Checker) paramHints(x *Call) []*Type {
	if fld, isField := x.Callee.(*Field); isField {
		if recv := c.receiverType(fld); recv != nil {
			if m, ok := c.methods[recv.Name][fld.Name]; ok && len(m.Params) > 0 {
				out := make([]*Type, 0, len(m.Params)-1)
				for _, p := range m.Params[1:] { // skip self
					out = append(out, p.T)
				}
				return out
			}
			return nil
		}
	}
	name, ok := DottedName(x.Callee)
	if !ok {
		return nil
	}
	if b, isBuiltin := c.lib.Signature(name); isBuiltin {
		if b.Check != nil {
			return nil // a custom checker decides for itself
		}
		hints := make([]*Type, len(x.Args))
		for i := range hints {
			if i < len(b.Params) {
				hints[i] = b.Params[i]
			} else {
				hints[i] = b.Rest
			}
		}
		return hints
	}
	f, isUser := c.funcs[name]
	if !isUser {
		f, isUser = c.funcs[Qual(c.pkg(), name)]
	}
	if isUser {
		out := make([]*Type, len(f.Params))
		for i, p := range f.Params {
			out[i] = p.T
		}
		return out
	}
	return nil
}

// receiverType returns the struct type a method is being called on, or
// nil when this is a library path rather than a method call.
func (c *Checker) receiverType(fld *Field) *Type {
	// A plain dotted path that names a builtin is a library call.
	if name, ok := DottedName(fld); ok {
		if _, isBuiltin := c.lib.Signature(name); isBuiltin {
			return nil
		}
		// A path rooted at a name that is not a variable is a library
		// path too - a broken one, which the resolver has reported.
		if root, isIdent := rootIdent(fld); isIdent && c.lookup(root) == nil {
			return nil
		}
	}
	t := c.expr(fld.X)
	if t.Kind == KStruct {
		return t
	}
	// A method is called through a pointer to a C struct the way C++
	// calls one through ->: the pointer is the struct's address, which
	// is all the method's self is.
	if t.IsPtr() && t.Elem != nil && c.isExternStruct(t.Elem) {
		return t.Elem
	}
	return nil
}

func rootIdent(e Expr) (string, bool) {
	for {
		switch x := e.(type) {
		case *Ident:
			return x.Name, true
		case *Field:
			e = x.X
		default:
			return "", false
		}
	}
}

func (c *Checker) methodCall(x *Call, fld *Field, recv *Type, args []*Type) *Type {
	m, ok := c.methods[recv.Name][fld.Name]
	if !ok {
		if g, generic := c.genericMethod(x, fld, recv, args); generic {
			if g == nil {
				return Unknown
			}
			m, ok = g, true
		}
	}
	if !ok {
		if ft, isField := c.fieldType(recv.Name, fld.Name); isField {
			// A field holding a function is callable, it is just not a
			// method - no receiver is passed.
			if ft.IsFunc() {
				x.Method = false
				x.ViaValue = true
				return c.callValue(x, ft, recv.Name+"."+fld.Name)
			}
			c.ErrorAt(x, "%s.%s is a field, not a method", recv.Name, fld.Name)
			return Unknown
		}
		c.ErrorAt(x, "%s has no method called %q - it has: %s",
			recv.Name, fld.Name, c.fieldNames(recv.Name))
		return Unknown
	}

	// Params[0] is self, which the receiver supplies.
	want := m.Params[1:]
	if len(args) < len(want) {
		args = c.fillDefaults(x, want, args)
	}
	if len(args) != len(want) {
		c.ErrorAt(x, "%s.%s expects %s, got %d",
			recv.Name, fld.Name, ArityText(len(want), len(want)), len(args))
	}
	for i := 0; i < len(args) && i < len(want); i++ {
		if c.coerce(&x.Args[i], want[i].T, args[i]) {
			continue
		}
		c.ErrorAt(x.Args[i], "%s.%s expects %s for %q, got %s",
			recv.Name, fld.Name, want[i].T, want[i].Name, args[i])
	}
	return m.RetT
}

func (c *Checker) checkBuiltin(x *Call, name string, b Signature, args []*Type) *Type {
	// A builtin with no declared signature is unchecked. Every builtin
	// should have one; this guard keeps an omission from panicking.
	if b.Ret == nil && b.RetOf == nil {
		return Unknown
	}

	for i, got := range args {
		want := b.Rest
		if i < len(b.Params) {
			want = b.Params[i]
		}
		if want == nil {
			continue // variadic tail with no declared element type
		}
		if want.Accepts(got) || (IsUntypedInt(x.Args[i]) && want.Kind == KFloat) {
			continue
		}
		c.ErrorAt(x.Args[i], "%s expects %s for argument %d, got %s",
			name, want, i+1, got)
	}

	if b.RetOf != nil {
		return b.RetOf(args)
	}
	return b.Ret
}

// anyUnknown reports whether an argument already failed to check.
func anyUnknown(args []*Type) bool {
	for _, a := range args {
		if a != nil && a.Kind == KUnknown {
			return true
		}
	}
	return false
}

// ---- untyped constants ----

// IsUntypedInt reports whether an expression is built entirely out of
// integer literals, and so - following Go's untyped-constant rule - can
// stand in for a float without an explicit conversion.
//
// `x * 2` is fine when x is a float. `x * y` is not, when y is an int
// variable. That distinction is the whole reason this function exists.
func IsUntypedInt(e Expr) bool {
	switch x := e.(type) {
	case *IntLit:
		return true
	case *Unary:
		return x.Op == MINUS && IsUntypedInt(x.X)
	case *Binary:
		switch x.Op {
		case PLUS, MINUS, STAR, SLASH, PERCENT:
			return IsUntypedInt(x.L) && IsUntypedInt(x.R)
		}
	}
	return false
}

// ---- extern structs ----

// cTypes are the field types an extern struct may use: size, and what a
// read gives back in Veyl. bool is one byte; the Windows BOOL is an i32.
var cTypes = map[string]struct {
	size int
	t    *Type
}{
	"i8": {1, fixedTypes["i8"]}, "u8": {1, fixedTypes["u8"]},
	"i16": {2, fixedTypes["i16"]}, "u16": {2, fixedTypes["u16"]},
	"i32": {4, fixedTypes["i32"]}, "u32": {4, fixedTypes["u32"]},
	"i64": {8, Int}, "u64": {8, fixedTypes["u64"]},
	"f32": {4, fixedTypes["f32"]}, "f64": {8, Float}, "ptr": {8, Int}, "bool": {1, Bool},
}

// CTypeSize is the size of a C field type, or 0 if it is not one.
func CTypeSize(name string) int { return cTypes[name].size }

// layout places the fields of an extern struct the way a C compiler
// would: each at the next multiple of its own alignment, the whole
// padded to a multiple of the largest. A field with `at` goes exactly
// where it says, which is how a structure read out of another program
// is described when only some offsets are known, and the fields after
// it carry on from its end.
func (c *Checker) layout(d *StructDecl, visiting map[string]bool) {
	if d.Size > 0 || d.Align > 0 {
		return
	}
	if visiting[d.Name] {
		c.ErrorAt(d, "%s contains itself - use a pointer field, *%s, for a link to another one", d.Name, d.Name)
		d.Align = 1
		return
	}
	visiting[d.Name] = true
	defer delete(visiting, d.Name)

	at, align := 0, 1
	for i := range d.Fields {
		f := &d.Fields[i]
		size, fAlign := 0, 1
		if strings.HasPrefix(f.Type, "*") {
			size, fAlign = 8, 8
			f.T = c.externFieldType(f)
		} else if ct, ok := cTypes[f.Type]; ok {
			size, fAlign = ct.size, ct.size
			f.T = ct.t
		} else if inner, ok := c.structs[f.Type]; ok && inner.Extern {
			c.layout(inner, visiting)
			size, fAlign = inner.Size, inner.Align
			f.T = StructOf(inner.Name)
		} else {
			if ok {
				c.ErrorAt(f, "%s is a Veyl struct and has no C layout - make it an extern struct", f.Type)
			} else if f.Type != "" {
				c.ErrorAt(f, "unknown field type %q - an extern struct field is i8, u8, i16, u16, "+
					"i32, u32, i64, u64, f32, f64, ptr, bool or another extern struct", f.Type)
			}
			f.T = Unknown
			size, fAlign = 1, 1
		}
		if fAlign < 1 {
			fAlign = 1
		}
		if f.Len > 0 {
			// An array reads as a pointer to its first element, so
			// p.name[3] is the fourth one.
			size *= f.Len
			switch {
			case f.T.IsUnknown():
			case f.T.IsPtr() || f.T.Kind == KStruct:
				f.T = PtrOf(f.T)
			default:
				f.T = PtrToC(f.Type)
			}
		}
		if f.At >= 0 {
			at = f.At
		} else if at%fAlign != 0 {
			at += fAlign - at%fAlign
		}
		f.Offset = at
		at += size
		if fAlign > align {
			align = fAlign
		}
	}
	if at%align != 0 {
		at += align - at%align
	}
	d.Size, d.Align = at, align
}

func externField(d *StructDecl, name string) *StructField {
	for i := range d.Fields {
		if d.Fields[i].Name == name {
			return &d.Fields[i]
		}
	}
	return nil
}

// externStructNamed is the extern struct an expression names as a type,
// as in Player.size, or nil. A variable of the same name wins.
func (c *Checker) externStructNamed(e Expr) *StructDecl {
	id, ok := e.(*Ident)
	if !ok || c.lookup(id.Name) != nil {
		return nil
	}
	if d, ok := c.structs[id.Name]; ok && d.Extern {
		return d
	}
	return nil
}

func (c *Checker) isExternStruct(t *Type) bool {
	if t == nil || t.Kind != KStruct {
		return false
	}
	d, ok := c.structs[t.Name]
	return ok && d.Extern
}

// arrayField reports whether an assignment target is an array field of
// an extern struct, which reads as an address and cannot be assigned.
func (c *Checker) arrayField(e Expr) (*StructField, bool) {
	fld, ok := e.(*Field)
	if !ok {
		return nil, false
	}
	t := c.peekType(fld.X)
	if t.IsPtr() && t.Elem != nil {
		t = t.Elem
	}
	if !c.isExternStruct(t) {
		return nil, false
	}
	f := externField(c.structs[t.Name], fld.Name)
	return f, f != nil && f.Len > 0
}

// peekType is the type of a variable or a field chain without checking
// it, for the handful of questions asked before an expression is
// checked. Anything else answers nil.
func (c *Checker) peekType(e Expr) *Type {
	switch x := e.(type) {
	case *Ident:
		return c.lookup(x.Name)
	case *Field:
		t := c.peekType(x.X)
		if t.IsPtr() && t.Elem != nil {
			t = t.Elem
		}
		if t == nil || t.Kind != KStruct {
			return nil
		}
		if ft, ok := c.fieldType(t.Name, x.Name); ok {
			return ft
		}
	}
	return nil
}

// checkCallbackType checks a function-typed extern parameter: the
// native code will call back into Veyl through it, so everything in its
// signature has to be something C can pass and take back.
func (c *Checker) checkCallbackType(prm *Param, fn string) {
	ok := func(t *Type) bool { return externScalar(t) || c.isExternStruct(t) }
	for _, p := range prm.T.Params {
		if !ok(p) {
			c.ErrorAt(prm, "a callback given to %s can only take int, float, str, bool, ptr or "+
				"an extern struct - %s cannot come from native code", fn, p)
			return
		}
	}
	if r := prm.T.Elem; r != nil && r != Void && !ok(r) {
		c.ErrorAt(prm, "a callback given to %s can only return int, float, bool, ptr or an "+
			"extern struct, not %s", fn, r)
	}
	if r := prm.T.Elem; r.IsFixed() && r.Name == "f32" && len(prm.T.Params) > 4 {
		c.ErrorAt(prm, "a callback returning f32 can take at most four arguments")
	}
}

// checkExportDecl checks an `export fn`: native code calls it, so its
// parameters and result have to be things C can pass and take back.
func (c *Checker) checkExportDecl(f *FnDecl) {
	if f.Recv != "" {
		c.ErrorAt(f, "a method cannot be exported - export a plain fn that calls it")
		return
	}
	ok := func(t *Type) bool { return externScalar(t) || c.isExternStruct(t) }
	for i := range f.Params {
		if prm := &f.Params[i]; !ok(prm.T) {
			c.ErrorAt(prm, "exported %s cannot take %s - native code can only pass int, float, "+
				"str, bool, ptr or an extern struct", f.Name, prm.T)
		}
	}
	if f.RetT != nil && f.RetT != Void && !ok(f.RetT) {
		c.ErrorAt(f, "exported %s cannot return %s - native code can only take back int, "+
			"float, bool, ptr or an extern struct", f.Name, f.RetT)
	}
	if f.RetT.IsFixed() && f.RetT.Name == "f32" && len(f.Params) > 4 {
		c.ErrorAt(f, "an exported function returning f32 can take at most four arguments")
	}
}

// globalNamed is the top-level const or var a name refers to, or nil
// when it is not one or a local shadows it.
func (c *Checker) globalNamed(name string) *LetStmt {
	g, ok := c.globals[name]
	if !ok || len(c.scopes) == 0 {
		return nil
	}
	for i := len(c.scopes) - 1; i > 0; i-- {
		if _, local := c.scopes[i][name]; local {
			return nil
		}
	}
	return g
}

func (c *Checker) checkFnPrivacy(at Node, f *FnDecl) {
	// An instance was checked as its template, under the name written.
	if f.Recv == "" && !f.Instance && c.private(f.File, f.Pub) {
		c.ErrorAt(at, "%q is private to %s - mark it 'pub fn %s' to use it from another file",
			f.Name, baseName(f.File), f.Name)
	}
}

// ---- enums ----

func (c *Checker) declareEnum(e *EnumDecl) {
	if prev, dup := c.enums[e.Name]; dup {
		// The same file imported along two paths is the same enum.
		if prev.File != e.File || prev.Span != e.Span {
			c.ErrorAt(e, "enum %s is declared twice", e.Name)
		}
		return
	}
	if _, clash := c.structs[e.Name]; clash {
		c.ErrorAt(e, "%s is already a struct", e.Name)
		return
	}
	if len(e.Variants) == 0 {
		c.ErrorAt(e, "enum %s needs at least one variant", e.Name)
	}
	seen := map[string]bool{}
	for _, v := range e.Variants {
		if seen[v] {
			c.ErrorAt(e, "%s.%s is declared twice", e.Name, v)
		}
		seen[v] = true
	}
	c.enums[e.Name] = e
	if e.Data {
		c.declareData(e)
	}
}

// enumNamed is the enum an expression names as a type, as in State.Idle,
// or nil. A variable of the same name wins.
func (c *Checker) enumNamed(e Expr) *EnumDecl {
	id, ok := e.(*Ident)
	if !ok || c.lookup(id.Name) != nil {
		return nil
	}
	// Option<int>, the instance of a generic enum, made when first named.
	if strings.Contains(id.Name, "<") {
		if t := ParseType(id.Name); t != nil && t.Kind == KStruct {
			id.Name = t.Name
		}
		c.instStruct(id.Name, e)
	}
	return c.enums[id.Name]
}

// enumTypes turns a name ParseType read as a struct into the enum it
// is, anywhere inside a type: ParseType has no table of declarations,
// so it cannot tell the two apart itself.
func (c *Checker) enumTypes(t *Type) *Type {
	if t == nil {
		return nil
	}
	switch t.Kind {
	case KStruct:
		if e, ok := c.enums[t.Name]; ok && !e.Data {
			return EnumOf(t.Name)
		}
	case KList, KNullable, KResult:
		cp := *t
		cp.Elem = c.enumTypes(t.Elem)
		return &cp
	case KMap:
		cp := *t
		cp.Key = c.enumTypes(t.Key)
		cp.Elem = c.enumTypes(t.Elem)
		return &cp
	case KFunc:
		cp := *t
		cp.Params = make([]*Type, len(t.Params))
		for i, p := range t.Params {
			cp.Params[i] = c.enumTypes(p)
		}
		cp.Elem = c.enumTypes(t.Elem)
		return &cp
	}
	return t
}

// stmts checks a list of statements in order, with one rule on top: an
// `if x == nil { return }` - no else, a body that always leaves - proves
// x is not nil for the rest of the list, as an `if x != nil { ... }`
// proves it inside. The early return is how a nil is usually dealt
// with, and without this the code after it cannot use x at all.
//
// Only while nothing later in the list assigns to x: a narrowing that
// an assignment could quietly undo would let a nil through unchecked.
func (c *Checker) stmts(list []Stmt) {
	pushed := 0
	for i, s := range list {
		c.stmt(s)
		st, ok := s.(*IfStmt)
		if !ok || st.Else != nil || !leaves(st.Then) {
			continue
		}
		_, whenFalse := c.nilChecks(st.Cond)
		var keep []string
		for _, name := range whenFalse {
			if !assignsTo(list[i+1:], name) {
				keep = append(keep, name)
			}
		}
		if len(keep) > 0 {
			c.pushNarrow(keep)
			pushed++
		}
	}
	for ; pushed > 0; pushed-- {
		c.popNarrow()
	}
}

// leaves reports whether a block always ends by leaving: a return, a
// break or a continue as its last statement.
func leaves(b *Block) bool {
	if b == nil || len(b.Stmts) == 0 {
		return false
	}
	switch b.Stmts[len(b.Stmts)-1].(type) {
	case *ReturnStmt, *BreakStmt, *ContinueStmt:
		return true
	}
	return false
}

// assignsTo reports whether any of these statements, at any depth,
// assigns to a name.
func assignsTo(list []Stmt, name string) bool {
	for _, s := range list {
		switch x := s.(type) {
		case *AssignStmt:
			if x.TargetName() == name {
				return true
			}
		case *Block:
			if assignsTo(x.Stmts, name) {
				return true
			}
		case *IfStmt:
			if assignsTo([]Stmt{x.Then}, name) || (x.Else != nil && assignsTo([]Stmt{x.Else}, name)) {
				return true
			}
		case *WhileStmt:
			if assignsTo(x.Body.Stmts, name) {
				return true
			}
		case *ForStmt:
			if assignsTo(x.Body.Stmts, name) {
				return true
			}
		case *MatchStmt:
			for _, arm := range x.Cases {
				if assignsTo([]Stmt{arm.Body}, name) {
					return true
				}
			}
			if x.Else != nil && assignsTo([]Stmt{x.Else}, name) {
				return true
			}
		}
	}
	return false
}

// leavesVia reports whether a statement contains a return, or a break
// or continue that is not inside a loop of its own.
func leavesVia(s Stmt) bool {
	var walk func(s Stmt, inLoop bool) bool
	walk = func(s Stmt, inLoop bool) bool {
		switch x := s.(type) {
		case *ReturnStmt:
			return true
		case *BreakStmt, *ContinueStmt:
			return !inLoop
		case *Block:
			for _, t := range x.Stmts {
				if walk(t, inLoop) {
					return true
				}
			}
		case *IfStmt:
			return walk(x.Then, inLoop) || (x.Else != nil && walk(x.Else, inLoop))
		case *WhileStmt:
			return walk(x.Body, true)
		case *ForStmt:
			return walk(x.Body, true)
		case *MatchStmt:
			for _, arm := range x.Cases {
				if walk(arm.Body, inLoop) {
					return true
				}
			}
			return x.Else != nil && walk(x.Else, inLoop)
		case *DeferStmt:
			return walk(x.Body, inLoop)
		}
		return false
	}
	return walk(s, false)
}

// forChannel checks `for v in ch` by rewriting it as the loop it means:
//
//	{
//	    let #ch = ch
//	    while true {
//	        let v = #ch.recv()
//	        if v == nil { break }
//	        body
//	    }
//	}
//
// The early break proves v is not nil for the rest of the body, so v is
// a plain T there.
func (c *Checker) forChannel(st *ForStmt, collT *Type) {
	if st.Var2 != "" {
		c.ErrorAt(st, "a loop over a channel takes one name: for v in ch")
		return
	}
	if _, ok := c.methods[collT.Name]["recv"]; !ok {
		c.ErrorAt(st.Coll, "cannot loop over %s", collT)
		return
	}
	c.matchCount++
	tmp := fmt.Sprintf("#ch%d", c.matchCount)
	at := st.Span
	recv := &Call{Span: at, Callee: &Field{Span: at, X: &Ident{Span: at, Name: tmp}, Name: "recv"}}
	stop := &IfStmt{Span: at,
		Cond: &Binary{Span: at, Op: EQ, L: &Ident{Span: at, Name: st.Var}, R: &NilLit{Span: at}},
		Then: &Block{Span: at, Stmts: []Stmt{&BreakStmt{Span: at}}}}
	body := &Block{Span: at, Stmts: append([]Stmt{&LetStmt{Span: at, Name: st.Var, Value: recv}, stop},
		st.Body.Stmts...)}
	loop := &WhileStmt{Span: at, Cond: &BoolLit{Span: at, Val: true}, Body: body}
	st.Lowered = &Block{Span: at, Stmts: []Stmt{&LetStmt{Span: at, Name: tmp, Value: st.Coll}, loop}}
	c.stmt(st.Lowered)
}
