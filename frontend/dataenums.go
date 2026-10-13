package frontend

// Data enums: variants that carry values.
//
//	enum Shape {
//	    Circle(r: float)
//	    Rect(w: float, h: float)
//	    Empty
//	}
//
//	let s = Shape.Circle(2.0)
//	match s {
//	    Shape.Circle(r) => print(3.14159 * r * r)
//	    Shape.Rect(w, h) => print(w * h)
//	    Shape.Empty => print(0)
//	}
//
// An enum whose variants carry nothing stays an int, as before. One
// where any variant carries something is kept in a struct: which
// variant it is, in a field called #tag, and every variant's fields
// beside it, named Circle.r, Rect.w and so on. Neither name can be
// written in Veyl, so the fields are only reached through a match.
//
// The checker does the rest by rewriting. A variant built with its
// values becomes that struct's literal, and a match becomes a chain of
// ifs on the tag with each arm's names bound to the fields it names. A
// backend needs only to lower the rewritten forms and to print one of
// these structs by its variant, Circle(2.0), rather than field by field.
//
// A data enum can be generic, enum Option<T> { Some(v: T), None },
// through the same machinery as a generic struct.

import (
	"fmt"
	"strconv"
	"strings"
)

// TagField is the field holding which variant a data enum value is.
const TagField = "#tag"

// PayloadField names the field holding one value a variant carries.
func PayloadField(variant, field string) string { return variant + "." + field }

// declareData makes the struct a data enum is kept in.
func (c *Checker) declareData(e *EnumDecl) *StructDecl {
	d := &StructDecl{Span: e.Span, Name: e.Name, Pub: e.Pub, File: e.File, Enum: e}
	d.Fields = append(d.Fields, StructField{Span: e.Span, Name: TagField, Type: "int"})
	for i, v := range e.Variants {
		seen := map[string]bool{}
		for _, p := range e.Payloads[i] {
			if seen[p.Name] {
				c.ErrorAt(p, "%s.%s has two fields called %s", e.Name, v, p.Name)
			}
			seen[p.Name] = true
			d.Fields = append(d.Fields, StructField{Span: p.Span, Name: PayloadField(v, p.Name), Type: p.Type})
		}
	}
	c.structs[e.Name] = d
	c.prog.Structs = append(c.prog.Structs, d)
	return d
}

// dataEnumOf is the data enum a type is, or nil.
func (c *Checker) dataEnumOf(t *Type) *EnumDecl {
	if t == nil || t.Kind != KStruct {
		return nil
	}
	if d, ok := c.structs[t.Name]; ok && d.Enum != nil {
		return d.Enum
	}
	return nil
}

func variantIndex(e *EnumDecl, name string) int {
	for i, v := range e.Variants {
		if v == name {
			return i
		}
	}
	return -1
}

// genericEnumNamed is the generic enum a bare name refers to, Option in
// Option.Some(1), or nil.
func (c *Checker) genericEnumNamed(x Expr) *EnumDecl {
	id, ok := x.(*Ident)
	if !ok || c.lookup(id.Name) != nil {
		return nil
	}
	return c.genEnums[id.Name]
}

// variantValue is a variant written without values, Shape.Empty.
func (c *Checker) variantValue(x *Field, e *EnumDecl) *Type {
	i := variantIndex(e, x.Name)
	if i < 0 {
		c.ErrorAt(x, "%s has no variant %q - it has: %s", e.Name, x.Name, strings.Join(e.Variants, ", "))
		return Unknown
	}
	if e.Payloads[i] != nil {
		c.ErrorAt(x, "%s.%s holds %s - give it, as in %s.%s(...)",
			e.Name, x.Name, describePayload(e.Payloads[i]), e.Name, x.Name)
		return Unknown
	}
	t := StructOf(e.Name)
	x.Lit = &StructLit{Span: x.Span, Name: e.Name, T: t,
		Fields: []string{TagField}, Vals: []Expr{&IntLit{Span: x.Span, Val: strconv.Itoa(i)}}}
	return t
}

// variantCall is a variant built with its values, Shape.Circle(2.0).
// args are the arguments' types when they have been checked already.
func (c *Checker) variantCall(x *Call, fld *Field, e *EnumDecl, args []*Type) *Type {
	if c.private(e.File, e.Pub) {
		c.ErrorAt(x, "enum %q is private to %s - mark it 'pub enum %s' to use it from another file",
			e.Name, baseName(e.File), Unqual(e.Name))
	}
	check := func() {
		if args == nil {
			for _, a := range x.Args {
				c.expr(a)
			}
		}
	}
	i := variantIndex(e, fld.Name)
	if i < 0 {
		check()
		c.ErrorAt(fld, "%s has no variant %q - it has: %s", e.Name, fld.Name, strings.Join(e.Variants, ", "))
		return Unknown
	}
	payload := e.Payloads[i]
	if payload == nil {
		check()
		c.ErrorAt(x, "%s.%s holds nothing, so it is written without brackets", e.Name, fld.Name)
		return Unknown
	}
	if len(x.Args) != len(payload) {
		check()
		c.ErrorAt(x, "%s.%s holds %s, got %d value(s)", e.Name, fld.Name, describePayload(payload), len(x.Args))
		return Unknown
	}

	t := StructOf(e.Name)
	lit := &StructLit{Span: x.Span, Name: e.Name, T: t,
		Fields: []string{TagField}, Vals: []Expr{&IntLit{Span: x.Span, Val: strconv.Itoa(i)}}}
	for k := range x.Args {
		name := PayloadField(fld.Name, payload[k].Name)
		want, _ := c.fieldType(e.Name, name)
		var got *Type
		if args != nil {
			got = args[k]
		} else {
			got = c.exprWant(x.Args[k], want)
		}
		if !c.coerce(&x.Args[k], want, got) {
			c.ErrorAt(x.Args[k], "%s.%s expects %s for %q, got %s", e.Name, fld.Name, want, payload[k].Name, got)
		}
		lit.Fields = append(lit.Fields, name)
		lit.Vals = append(lit.Vals, x.Args[k])
	}
	x.Lit = lit
	x.T = t
	return t
}

// inferVariant works out a generic enum's type arguments from the
// values a variant is built with - Option.Some(3) is an Option<int> -
// and returns that instance.
func (c *Checker) inferVariant(x *Call, fld *Field, tmpl *EnumDecl) (*EnumDecl, []*Type) {
	args := make([]*Type, len(x.Args))
	for k := range x.Args {
		args[k] = c.expr(x.Args[k])
	}
	i := variantIndex(tmpl, fld.Name)
	if i < 0 || tmpl.Payloads[i] == nil {
		c.ErrorAt(x, "%s is generic - say what it holds, as in %s<int>.%s", tmpl.Name, tmpl.Name, fld.Name)
		return nil, args
	}
	params := map[string]bool{}
	for _, p := range tmpl.TypeParams {
		params[p] = true
	}
	bind := map[string]*Type{}
	for k, p := range tmpl.Payloads[i] {
		if k < len(args) {
			unify(ParseType(p.Type), args[k], params, bind)
		}
	}
	names := make([]string, len(tmpl.TypeParams))
	for k, p := range tmpl.TypeParams {
		if bind[p] == nil || bind[p].IsUnknown() {
			if !anyUnknown(args) {
				c.ErrorAt(x, "cannot tell what %s is from the values - name it, as in %s<int>.%s(...)",
					p, tmpl.Name, fld.Name)
			}
			return nil, args
		}
		names[k] = bind[p].String()
	}
	inst := GenericName(tmpl.Name, names)
	if !c.instStruct(inst, x) {
		return nil, args
	}
	line, col := fld.X.Pos()
	fld.X = &Ident{Span: Span{Line: line, Col: col}, Name: inst}
	return c.enums[inst], args
}

func describePayload(ps []Param) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.Name + ": " + p.Type
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// matchData rewrites a match on a data enum as a chain of ifs.
func (c *Checker) matchData(st *MatchStmt, subj *Type, e *EnumDecl) {
	c.matchCount++
	st.Temp = fmt.Sprintf("#match%d", c.matchCount)
	line, col := st.Pos()
	at := Span{Line: line, Col: col}
	tag := func(sp Span) Expr {
		return &Field{Span: sp, X: &Ident{Span: sp, Name: st.Temp}, Name: TagField}
	}

	handled := map[string]bool{}
	var arms []*IfStmt
	for _, arm := range st.Cases {
		var cond Expr
		var binds []Stmt
		for _, v := range arm.Values {
			vl, vc := v.Pos()
			sp := Span{Line: vl, Col: vc}
			name, args, isCall, ok := c.variantPattern(v, e)
			if !ok {
				continue
			}
			i := variantIndex(e, name)
			if i < 0 && e.Interface {
				// An arm can name a struct nothing has converted yet.
				if d := c.ifaces[e.Name]; d != nil {
					if _, isStruct := c.structs[name]; isStruct && c.missingMethod(name, d) == "" {
						i = c.ifaceVariant(d, name)
					}
				}
			}
			if i < 0 {
				c.ErrorAt(v, "%s has no variant %q - it has: %s", e.Name, name, strings.Join(e.Variants, ", "))
				continue
			}
			if handled[name] {
				c.ErrorAt(v, "this variant is already handled by an earlier arm")
			}
			handled[name] = true
			if isCall {
				payload := e.Payloads[i]
				switch {
				case payload == nil:
					c.ErrorAt(v, "%s.%s holds nothing, so its arm is written without brackets", e.Name, name)
				case len(args) != len(payload):
					c.ErrorAt(v, "%s.%s holds %s - name each, or _ for one you do not need",
						e.Name, name, describePayload(payload))
				case len(arm.Values) > 1:
					c.ErrorAt(v, "an arm that names what a variant holds can only match that one variant")
				default:
					for k, a := range args {
						id, isIdent := a.(*Ident)
						if !isIdent {
							c.ErrorAt(a, "a pattern names each value, as in %s.%s(x), or _ for one you do not need",
								e.Name, name)
							continue
						}
						if id.Name == "_" {
							continue
						}
						binds = append(binds, &LetStmt{Span: id.Span, Name: id.Name,
							Value: &Field{Span: id.Span, X: &Ident{Span: id.Span, Name: st.Temp},
								Name: PayloadField(name, payload[k].Name)}})
					}
				}
			}
			test := &Binary{Span: sp, Op: EQ, L: tag(sp), R: &IntLit{Span: sp, Val: strconv.Itoa(i)}}
			if cond == nil {
				cond = test
			} else {
				cond = &Binary{Span: sp, Op: OR, L: cond, R: test}
			}
		}
		if cond == nil {
			continue
		}
		body := &Block{Span: at, Stmts: append(binds, arm.Body)}
		arms = append(arms, &IfStmt{Span: at, Cond: cond, Then: body})
	}

	// An interface's structs are only the ones this program converts, so
	// a match on one is not held to naming them all.
	if st.Else == nil && !e.Interface {
		var missing []string
		for _, v := range e.Variants {
			if !handled[v] {
				missing = append(missing, e.Name+"."+v)
			}
		}
		if len(missing) > 0 {
			c.ErrorAt(st, "this match on %s does not handle %s - add an arm for each, or an else",
				e.Name, strings.Join(missing, ", "))
		}
	}

	var lowered Stmt = &Block{Span: at}
	if len(arms) > 0 {
		for k := len(arms) - 1; k > 0; k-- {
			if k == len(arms)-1 && st.Else != nil {
				arms[k].Else = st.Else
			}
			arms[k-1].Else = arms[k]
		}
		if len(arms) == 1 && st.Else != nil {
			arms[0].Else = st.Else
		}
		lowered = arms[0]
	} else if st.Else != nil {
		lowered = st.Else
	}
	st.Lowered = lowered

	c.push()
	c.define(st.Temp, subj)
	c.stmt(lowered)
	c.pop()
}

// variantPattern takes an arm's value apart: Shape.Circle(r) is the
// variant Circle with the pattern r. The enum may be named with or
// without its type arguments, Option.Some(v) or Option<int>.Some(v).
func (c *Checker) variantPattern(v Expr, e *EnumDecl) (string, []Expr, bool, bool) {
	var fld *Field
	var args []Expr
	isCall := false
	switch x := v.(type) {
	case *Field:
		fld = x
	case *Call:
		f, ok := x.Callee.(*Field)
		if !ok {
			break
		}
		fld, args, isCall = f, x.Args, true
	}
	if fld == nil {
		c.ErrorAt(v, "an arm of a match on %s names a variant, as in %s.%s", e.Name, e.Name, e.Variants[0])
		return "", nil, false, false
	}
	id, ok := fld.X.(*Ident)
	base := e.Name
	if b, _, generic := SplitGeneric(e.Name); generic {
		base = b
	}
	if !ok || (id.Name != e.Name && id.Name != base) {
		if ok {
			if t := ParseType(id.Name); t != nil && t.String() == e.Name {
				return fld.Name, args, isCall, true
			}
		}
		c.ErrorAt(v, "this match is on %s, but this arm names something else", e.Name)
		return "", nil, false, false
	}
	return fld.Name, args, isCall, true
}
