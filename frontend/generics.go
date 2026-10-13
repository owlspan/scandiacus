package frontend

// Generics, by copying.
//
//	fn largest<T>(xs: []T) -> T { ... }
//	struct Stack<T> { items: []T }
//	impl Stack<T> { fn push(self, x: T) { ... } }
//
// A generic declaration is a template. It is never checked or compiled
// as written; the parser keeps its tokens, and each distinct set of
// types it is used with gets its own copy, re-parsed with the type
// parameters replaced by those types. largest called on a []int and on
// a []str is two ordinary functions, largest<int> and largest<str>, and
// Stack<int> is an ordinary struct whose methods are ordinary methods.
//
// That is C++'s model, and it is chosen for the same reason: everything
// after the checker - both backends, the optimiser, the collector's
// view of a struct's layout - only ever sees concrete types, so none of
// it had to change. The costs are C++'s too: a template is only checked
// when something uses it, and each instance is code of its own.
//
// Type arguments are worked out from the arguments of a call, or given
// by name where they cannot be: largest<int>([]) or Stack<str>{}.

import (
	"fmt"
	"strings"
)

// maxInstances stops a template that instantiates itself with ever
// larger types - f<T> calling f<[]T> - from running forever.
const maxInstances = 5000

// collectTemplates takes the generic declarations out of the program,
// so no pass that walks it ever meets one, and files them by name.
func (c *Checker) collectTemplates(p *Program) {
	c.genFuncs = map[string]*FnDecl{}
	c.genStructs = map[string]*StructDecl{}
	c.genMethods = map[string][]*FnDecl{}
	c.genEnums = map[string]*EnumDecl{}
	c.genMethodTmpl = map[string]map[string]*FnDecl{}

	var enums []*EnumDecl
	for _, e := range p.Enums {
		if e.TypeParams != nil {
			c.genEnums[e.Name] = e
			continue
		}
		enums = append(enums, e)
	}
	p.Enums = enums

	var structs []*StructDecl
	for _, d := range p.Structs {
		if d.TypeParams != nil {
			c.genStructs[d.Name] = d
			continue
		}
		structs = append(structs, d)
	}
	var funcs []*FnDecl
	for _, f := range p.Funcs {
		switch {
		case f.RecvParams != nil:
			c.genMethods[f.Recv] = append(c.genMethods[f.Recv], f)
		case f.TypeParams != nil && f.Recv == "":
			c.genFuncs[Qual(f.Pkg, f.Name)] = f
		case f.TypeParams != nil:
			// A generic method on a plain struct.
			c.addMethodTemplate(f.Recv, f)
		default:
			if f.Recv != "" {
				if _, generic := c.genStructs[f.Recv]; generic {
					c.ErrorAt(f, "%s is generic, so its methods go in impl %s<%s> { ... }",
						f.Recv, f.Recv, strings.Join(c.genStructs[f.Recv].TypeParams, ", "))
					continue
				}
			}
			funcs = append(funcs, f)
		}
	}
	for recv, ms := range c.genMethods {
		d, ok := c.genStructs[recv]
		if !ok {
			c.ErrorAt(ms[0], "%s is not a generic struct, so its methods go in plain impl %s { ... }", recv, recv)
			continue
		}
		for _, m := range ms {
			if len(m.RecvParams) != len(d.TypeParams) {
				c.ErrorAt(m, "%s has %d type parameter(s), but this impl names %d",
					recv, len(d.TypeParams), len(m.RecvParams))
			}
		}
	}
	p.Structs, p.Funcs = structs, funcs
}

// instStruct makes the instance a generic struct's name asks for, such
// as Box<int>, along with its methods. It reports whether the name was
// one: false leaves the caller to call it an unknown type.
func (c *Checker) instStruct(name string, at Node) bool {
	if _, done := c.structs[name]; done {
		return true
	}
	base, args, ok := SplitGeneric(name)
	if !ok {
		return false
	}
	if te, isEnum := c.genEnums[base]; isEnum {
		return c.instEnum(te, name, args, at)
	}
	tmpl, ok := c.genStructs[base]
	if !ok {
		return false
	}
	if len(args) != len(tmpl.TypeParams) {
		c.ErrorAt(at, "%s takes %d type argument(s), got %d", base, len(tmpl.TypeParams), len(args))
		return false
	}
	if !c.budget(at) {
		return false
	}

	d := &StructDecl{}
	if !c.instantiate(tmpl.File, tmpl.Toks, len(tmpl.TypeParams), tmpl.TypeParams, args, name, func(ps *Parser) {
		d = ps.parseStruct()
	}) {
		return false
	}
	d.Name, d.File, d.Pub, d.Pkg = name, tmpl.File, tmpl.Pub, tmpl.Pkg
	c.structs[name] = d
	c.prog.Structs = append(c.prog.Structs, d)
	c.within(name, d.File, func() { c.resolveFields(d) })

	for _, mt := range c.genMethods[base] {
		if len(mt.RecvParams) != len(args) {
			continue // reported when the templates were collected
		}
		m := &FnDecl{}
		if !c.instantiate(mt.File, mt.Toks, 0, mt.RecvParams, args, name, func(ps *Parser) {
			m = ps.parseFn()
		}) || m == nil {
			continue
		}
		if m.TypeParams != nil {
			// map<U> on a Box<int>: T is settled, U waits for a call.
			m.Recv, m.File, m.Pub, m.Pkg = name, mt.File, mt.Pub, mt.Pkg
			c.addMethodTemplate(name, m)
			continue
		}
		m.Recv, m.File, m.Pub, m.Pkg, m.Instance = name, mt.File, mt.Pub, mt.Pkg, true
		if c.methods[name] == nil {
			c.methods[name] = map[string]*FnDecl{}
		}
		c.methods[name][m.Name] = m
		c.within(name, m.File, func() { c.resolveSig(m) })
		c.prog.Funcs = append(c.prog.Funcs, m)
		c.pending = append(c.pending, m)
	}
	return true
}

// instEnum makes an instance of a generic enum, Option<int>.
func (c *Checker) instEnum(tmpl *EnumDecl, name string, args []string, at Node) bool {
	if len(args) != len(tmpl.TypeParams) {
		c.ErrorAt(at, "%s takes %d type argument(s), got %d", tmpl.Name, len(tmpl.TypeParams), len(args))
		return false
	}
	if !c.budget(at) {
		return false
	}
	var e *EnumDecl
	if !c.instantiate(tmpl.File, tmpl.Toks, len(tmpl.TypeParams), tmpl.TypeParams, args, name, func(ps *Parser) {
		e = ps.parseEnum()
	}) || e == nil {
		return false
	}
	e.Name, e.File, e.Pub, e.Data, e.TypeParams, e.Toks = name, tmpl.File, tmpl.Pub, true, nil, nil
	c.declareEnum(e)
	if d, ok := c.structs[name]; ok && d.Enum == e {
		c.within(name, d.File, func() { c.resolveFields(d) })
	}
	return true
}

// genericCall handles a call to a generic function: it works out the
// type arguments, makes the instance if it is new, and points the call
// at it. It reports false when the callee is not generic at all.
//
// Explicit type arguments, max<int>(a, b), are taken before the
// arguments are checked, so an argument such as [] learns its type from
// the parameter. Otherwise they come from the arguments' types.
func (c *Checker) genericCall(x *Call, name string, args []*Type) (*FnDecl, bool) {
	base, explicit, named := SplitGeneric(name)
	if !named {
		base = name
	}
	tmpl, ok := c.genFuncs[base]
	if !ok {
		tmpl, ok = c.genFuncs[Qual(c.pkg(), base)]
	}
	if !ok {
		return nil, false
	}
	c.checkFnPrivacy(x, tmpl)

	bind := map[string]*Type{}
	if named {
		if len(explicit) != len(tmpl.TypeParams) {
			c.ErrorAt(x, "%s takes %d type argument(s), got %d", base, len(tmpl.TypeParams), len(explicit))
			return nil, true
		}
		for i, a := range explicit {
			bind[tmpl.TypeParams[i]] = c.resolveAnnotation(a, x)
		}
	} else {
		params := map[string]bool{}
		for _, p := range tmpl.TypeParams {
			params[p] = true
		}
		for i := 0; i < len(args) && i < len(tmpl.Params); i++ {
			unify(ParseType(tmpl.Params[i].Type), args[i], params, bind)
		}
		for _, p := range tmpl.TypeParams {
			if bind[p] == nil {
				if anyUnknown(args) {
					return nil, true
				}
				c.ErrorAt(x, "cannot tell what %s is from the arguments - name it, as in %s<int>(...)",
					p, base)
				return nil, true
			}
		}
	}

	names := make([]string, len(tmpl.TypeParams))
	for i, p := range tmpl.TypeParams {
		t := bind[p]
		if t == nil || t.IsUnknown() {
			return nil, true
		}
		if t.Kind == KVoid {
			c.ErrorAt(x, "%s cannot be nothing", p)
			return nil, true
		}
		names[i] = t.String()
	}
	inst := GenericName(tmpl.Name, names)
	key := Qual(tmpl.Pkg, inst)

	f, done := c.funcs[key]
	if !done {
		if !c.budget(x) {
			return nil, true
		}
		f = &FnDecl{}
		if !c.instantiate(tmpl.File, tmpl.Toks, len(tmpl.TypeParams), tmpl.TypeParams, names, inst, func(ps *Parser) {
			f = ps.parseFn()
		}) || f == nil {
			return nil, true
		}
		f.Name, f.File, f.Pub, f.Pkg, f.Instance = inst, tmpl.File, tmpl.Pub, tmpl.Pkg, true
		c.within(inst, f.File, func() { c.resolveSig(f) })
		c.funcs[key] = f
		c.prog.Funcs = append(c.prog.Funcs, f)
		c.pending = append(c.pending, f)
	}

	line, col := x.Callee.Pos()
	x.Callee = &Ident{Span: Span{Line: line, Col: col}, Name: key}
	return f, true
}

// unify matches a parameter's declared type against an argument's,
// recording what each type parameter stands for. The first match wins;
// an argument that disagrees with it is then an ordinary type error.
func unify(pat, got *Type, params map[string]bool, bind map[string]*Type) {
	if pat == nil || got == nil || got.IsUnknown() || got.Kind == KNilLit {
		return
	}
	switch pat.Kind {
	case KStruct:
		if params[pat.Name] {
			if bind[pat.Name] == nil {
				bind[pat.Name] = got
			}
			return
		}
		pb, pa, ok := SplitGeneric(pat.Name)
		if !ok || got.Kind != KStruct {
			return
		}
		gb, ga, ok := SplitGeneric(got.Name)
		if !ok || gb != pb || len(ga) != len(pa) {
			return
		}
		for i := range pa {
			unify(ParseType(pa[i]), ParseType(ga[i]), params, bind)
		}
	case KList, KNullable, KResult:
		if got.Kind == pat.Kind {
			unify(pat.Elem, got.Elem, params, bind)
		} else if pat.Kind == KNullable {
			unify(pat.Elem, got, params, bind) // an int passed as a ?T
		}
	case KMap:
		if got.Kind == KMap {
			unify(pat.Key, got.Key, params, bind)
			unify(pat.Elem, got.Elem, params, bind)
		}
	case KFunc:
		if got.Kind == KFunc && len(got.Params) == len(pat.Params) {
			for i := range pat.Params {
				unify(pat.Params[i], got.Params[i], params, bind)
			}
			unify(pat.Elem, got.Elem, params, bind)
		}
	}
}

// instantiate re-parses a template's tokens with its type parameters
// replaced. skip is how many type parameters follow the name in the
// header, which the copy leaves out: an instance is not generic.
func (c *Checker) instantiate(file string, toks []Token, skip int, params, args []string, inst string, parse func(*Parser)) bool {
	subst := map[string][]Token{}
	for i, p := range params {
		subst[p] = typeTokens(args[i])
	}

	var out []Token
	rest := toks
	if skip > 0 {
		// `fn name <T , U >` - the keyword, the name, then 2n+1 tokens.
		out = append(out, toks[0], toks[1])
		rest = toks[2+2*skip+1:]
	}
	for i, t := range rest {
		if t.Kind == IDENT {
			if rep, ok := subst[t.Lex]; ok && !(i > 0 && rest[i-1].Kind == DOT) {
				for _, r := range rep {
					r.Line, r.Col = t.Line, t.Col
					out = append(out, r)
				}
				continue
			}
		}
		out = append(out, t)
	}
	out = append(out, Token{Kind: EOF})

	ps := NewParser(file, out)
	parse(ps)
	for _, e := range ps.Errors {
		c.Errors = append(c.Errors, e+" (in "+inst+")")
	}
	return len(ps.Errors) == 0
}

// typeTokens lexes a type's name, for splicing into a template. A dot
// in a type name can only be a namespace, geo.Vec, so the three tokens
// it lexes as are put back together into the one name it is.
func typeTokens(s string) []Token {
	var out []Token
	for _, t := range NewLexer("", s).Scan() {
		if t.Kind == EOF || t.Kind == NEWLINE {
			continue
		}
		n := len(out)
		if t.Kind == IDENT && n >= 2 && out[n-1].Kind == DOT && out[n-2].Kind == IDENT {
			out[n-2].Lex += "." + t.Lex
			out = out[:n-1]
			continue
		}
		out = append(out, t)
	}
	return out
}

func (c *Checker) budget(at Node) bool {
	c.instances++
	if c.instances > maxInstances {
		if c.instances == maxInstances+1 {
			c.ErrorAt(at, "more than %d generic instances - does a generic use itself with a bigger type each time?",
				maxInstances)
		}
		return false
	}
	return true
}

// within runs f with errors attributed to an instance: in the template's
// file, and naming which copy of it went wrong.
func (c *Checker) within(inst, file string, f func()) {
	prevName, prevFile := c.instName, c.instFile
	c.instName, c.instFile = inst, file
	f()
	c.instName, c.instFile = prevName, prevFile
}

// checkPending checks the bodies of instances made so far, which can
// make more.
func (c *Checker) checkPending() {
	for len(c.pending) > 0 {
		f := c.pending[0]
		c.pending = c.pending[1:]
		name := f.Name
		if f.Recv != "" {
			name = f.Recv + "." + f.Name
		}
		c.within(name, f.File, func() { c.checkFn(f) })
	}
}

// genericHint explains a generic used without its type arguments.
func (c *Checker) genericHint(name string) string {
	if d, ok := c.genStructs[name]; ok {
		return fmt.Sprintf("%s is generic - say what it holds, as in %s<%s>",
			name, name, strings.Repeat("int, ", len(d.TypeParams)-1)+"int")
	}
	if e, ok := c.genEnums[name]; ok {
		return fmt.Sprintf("%s is generic - say what it holds, as in %s<%s>",
			name, name, strings.Repeat("int, ", len(e.TypeParams)-1)+"int")
	}
	return ""
}

func (c *Checker) addMethodTemplate(recv string, f *FnDecl) {
	if c.genMethodTmpl[recv] == nil {
		c.genMethodTmpl[recv] = map[string]*FnDecl{}
	}
	c.genMethodTmpl[recv][f.Name] = f
}

// genericMethod handles a call to a method with type parameters of its
// own, as genericCall does a function: the types come from the
// arguments, and the instance, map<str>, is a method like any other on
// the same struct. The call is pointed at it by name. It reports false
// when there is no such generic method.
func (c *Checker) genericMethod(x *Call, fld *Field, recv *Type, args []*Type) (*FnDecl, bool) {
	base, explicit, named := SplitGeneric(fld.Name)
	if !named {
		base = fld.Name
	}
	tmpl, ok := c.genMethodTmpl[recv.Name][base]
	if !ok {
		return nil, false
	}
	params := map[string]bool{}
	for _, p := range tmpl.TypeParams {
		params[p] = true
	}
	bind := map[string]*Type{}
	if named {
		if len(explicit) != len(tmpl.TypeParams) {
			c.ErrorAt(x, "%s.%s takes %d type argument(s), got %d", recv.Name, base, len(tmpl.TypeParams), len(explicit))
			return nil, true
		}
		for i, a := range explicit {
			bind[tmpl.TypeParams[i]] = c.resolveAnnotation(a, x)
		}
	}
	for i := 0; i < len(args) && i+1 < len(tmpl.Params); i++ {
		unify(ParseType(tmpl.Params[i+1].Type), args[i], params, bind)
	}
	names := make([]string, len(tmpl.TypeParams))
	for i, p := range tmpl.TypeParams {
		t := bind[p]
		if t == nil || t.IsUnknown() {
			if !anyUnknown(args) {
				c.ErrorAt(x, "cannot tell what %s is from the arguments to %s.%s", p, recv.Name, fld.Name)
			}
			return nil, true
		}
		names[i] = t.String()
	}
	inst := GenericName(tmpl.Name, names)
	m, done := c.methods[recv.Name][inst]
	if !done {
		if !c.budget(x) {
			return nil, true
		}
		m = &FnDecl{}
		if !c.instantiate(tmpl.File, tmpl.Toks, len(tmpl.TypeParams), tmpl.TypeParams, names, inst, func(ps *Parser) {
			m = ps.parseFn()
		}) || m == nil {
			return nil, true
		}
		m.Name, m.Recv, m.File, m.Pub, m.Pkg, m.Instance = inst, recv.Name, tmpl.File, tmpl.Pub, tmpl.Pkg, true
		c.within(recv.Name+"."+inst, m.File, func() { c.resolveSig(m) })
		if c.methods[recv.Name] == nil {
			c.methods[recv.Name] = map[string]*FnDecl{}
		}
		c.methods[recv.Name][inst] = m
		c.prog.Funcs = append(c.prog.Funcs, m)
		c.pending = append(c.pending, m)
	}
	fld.Name = inst
	return m, true
}
