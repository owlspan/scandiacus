package frontend

import (
	"fmt"
	"strconv"
	"strings"
)

// noBrace suppresses struct literals while parsing the header of an if,
// while or for. Without it `if ready {` reads as the start of a struct
// literal named ready, and the block brace is never found. Go has the
// same rule for the same reason; inside brackets the suppression lifts,
// so `if (Point{x: 1}).x > 0 {` still works.
type Parser struct {
	toks    []Token
	i       int
	file    string
	noBrace int

	// stmtErrMark is how many errors had been reported when the current
	// statement started, so endStmt can tell a fresh mistake from debris
	// left by one already reported.
	stmtErrMark int

	// pendingGT is the second half of a `>>` that closed one type
	// argument list and still has another to close: Box<Box<int>>.
	pendingGT int

	Errors []string
}

func NewParser(file string, toks []Token) *Parser {
	return &Parser{toks: toks, file: file}
}

// header parses an expression in a position where a following '{' opens
// a block rather than a struct literal.
func (p *Parser) header(parse func() Expr) Expr {
	p.noBrace++
	x := parse()
	p.noBrace--
	return x
}

// grouped parses an expression inside brackets, where a '{' is
// unambiguous again.
func (p *Parser) grouped(parse func() Expr) Expr {
	saved := p.noBrace
	p.noBrace = 0
	x := parse()
	p.noBrace = saved
	return x
}

// ---- cursor helpers ----

func (p *Parser) cur() Token { return p.toks[p.i] }

func (p *Parser) advance() Token {
	t := p.toks[p.i]
	if t.Kind != EOF {
		p.i++
	}
	return t
}

func (p *Parser) check(k Kind) bool { return p.cur().Kind == k }

// peekKind is the kind of the token n places ahead, EOF past the end.
func (p *Parser) peekKind(n int) Kind {
	if p.i+n >= len(p.toks) {
		return EOF
	}
	return p.toks[p.i+n].Kind
}

func (p *Parser) match(k Kind) bool {
	if p.check(k) {
		p.advance()
		return true
	}
	return false
}

func (p *Parser) expect(k Kind, what string) Token {
	if p.check(k) {
		return p.advance()
	}
	p.errorAt(p.cur(), "expected %s, found %s", what, describe(p.cur()))
	return p.cur()
}

func describe(t Token) string {
	switch t.Kind {
	case EOF:
		return "end of file"
	case NEWLINE:
		return "end of line"
	default:
		return fmt.Sprintf("%q", t.Lex)
	}
}

// expectClose consumes a closing bracket, and when it is missing says
// where the thing being closed was opened.
//
// "expected '}'" is nearly useless in a file of any size - the reader
// already knows a brace is missing, and what they need is which one.
// The opening line is only mentioned when it differs from where the
// error surfaced, since repeating the current line is noise.
func (p *Parser) expectClose(k Kind, opener Token, closer, what string) Token {
	if p.check(k) {
		return p.advance()
	}
	cur := p.cur()
	if cur.Line == opener.Line {
		p.errorAt(cur, "expected %s to close the %s, found %s", closer, what, describe(cur))
	} else {
		p.errorAt(cur, "expected %s to close the %s opened on line %d, found %s",
			closer, what, opener.Line, describe(cur))
	}
	return cur
}

func (p *Parser) errorAt(t Token, format string, args ...any) {
	p.Errors = append(p.Errors,
		fmt.Sprintf("%s:%d:%d: %s", p.file, t.Line, t.Col, fmt.Sprintf(format, args...)))
}

func (p *Parser) skipNewlines() {
	for p.check(NEWLINE) {
		p.advance()
	}
}

// synchronize skips ahead to the next plausible statement start so that
// one syntax error doesn't cascade into twenty bogus ones.
func (p *Parser) synchronize() {
	for !p.check(EOF) {
		if p.check(NEWLINE) {
			p.advance()
			return
		}
		switch p.cur().Kind {
		case LET, CONST, IF, WHILE, FOR, FN, EXTERN, RETURN, BREAK, CONTINUE, RBRACE:
			return
		}
		p.advance()
	}
}

// ---- top level ----

func (p *Parser) ParseProgram() *Program {
	prog := &Program{}
	p.skipNewlines()

	for !p.check(EOF) {
		before := p.i

		// `pub` marks the next declaration as visible to files that
		// import this one.
		pub := false
		if p.check(PUB) {
			kw := p.advance()
			pub = true
			switch {
			case p.check(FN), p.check(STRUCT), p.check(CONST), p.check(EXTERN):
			case p.check(IDENT) && (p.cur().Lex == "var" || p.cur().Lex == "enum" || p.cur().Lex == "interface"):
			default:
				p.errorAt(kw, "'pub' can only go before fn, struct, const, var or extern")
			}
		}

		// `export fn` puts a function in a DLL's export table. `export`
		// is only a keyword here, so it stays usable as a name.
		if !pub && p.check(IDENT) && p.cur().Lex == "export" && p.peekKind(1) == FN {
			p.advance()
			if f := p.parseFn(); f != nil {
				f.Pub = true
				f.Export = true
				f.File = p.file
				prog.Funcs = append(prog.Funcs, f)
			}
			p.skipNewlines()
			if p.i == before {
				p.advance()
			}
			continue
		}

		switch {
		case p.check(IMPORT):
			if d := p.parseImport(); d != nil {
				prog.Imports = append(prog.Imports, d)
			}
		case p.check(FN):
			if f := p.parseFn(); f != nil {
				f.Pub = pub
				f.File = p.file
				prog.Funcs = append(prog.Funcs, f)
			}
		case p.check(EXTERN) && p.peekKind(1) == STRUCT:
			p.advance() // 'extern'
			if d := p.parseExternStruct(); d != nil {
				d.Pub = pub
				d.File = p.file
				prog.Structs = append(prog.Structs, d)
			}
		case p.check(EXTERN):
			if f := p.parseExtern(); f != nil {
				f.Pub = pub
				f.File = p.file
				prog.Funcs = append(prog.Funcs, f)
			}
		case p.check(STRUCT):
			if d := p.parseStruct(); d != nil {
				d.Pub = pub
				d.File = p.file
				prog.Structs = append(prog.Structs, d)
			}
		case p.check(IDENT) && p.cur().Lex == "enum" && p.peekKind(1) == IDENT &&
			(p.peekKind(2) == LBRACE || p.peekKind(2) == LT):
			// `enum` is only a keyword at the top level, before a name
			// and a brace, so it stays usable as a name everywhere else.
			if d := p.parseEnum(); d != nil {
				d.Pub, d.File = pub, p.file
				prog.Enums = append(prog.Enums, d)
			}
		case p.check(IDENT) && p.cur().Lex == "interface" && p.peekKind(1) == IDENT && p.peekKind(2) == LBRACE:
			// Like `enum`, only a keyword here.
			if d := p.parseInterface(); d != nil {
				d.Pub, d.File = pub, p.file
				prog.Ifaces = append(prog.Ifaces, d)
			}
		case p.check(IDENT) && p.cur().Lex == "gc" && p.peekKind(1) == IDENT &&
			(p.peekKind(2) == NEWLINE || p.peekKind(2) == EOF):
			// `gc off` or `gc on`, for the whole program. Only a keyword
			// in exactly this shape, so gc stays usable as a name.
			kw := p.advance()
			mode := p.advance()
			if mode.Lex != "off" && mode.Lex != "on" {
				p.errorAt(mode, "gc is either on or off, as in: gc off")
			} else if prog.GC != "" {
				p.errorAt(kw, "gc is already set for this file, at line %d", prog.GCAt.Line)
			} else {
				prog.GC = mode.Lex
				prog.GCAt = at(kw)
			}
			p.endStmt()
		case p.check(IDENT) && p.cur().Lex == "var" && p.peekKind(1) == IDENT:
			// `var` is a mutable global: visible inside functions, like
			// a const, and assignable, like a let. It is only a keyword
			// here, so `var` stays usable as a name.
			kw := p.cur()
			p.advance()
			name := p.expect(IDENT, "a variable name")
			g := &LetStmt{Span: at(kw), Name: name.Lex, Global: true, Pub: pub, File: p.file}
			if p.match(COLON) {
				g.Type = p.parseTypeRef()
			}
			p.expect(ASSIGN, "'='")
			g.Value = p.parseExpr(0)
			p.endStmt()
			prog.Globals = append(prog.Globals, g)
		case p.check(CONST):
			// A top-level const is a genuine global: visible inside
			// functions, unlike a top-level `let`, which stays a local of
			// the implicit main.
			if s := p.parseLet(); s != nil {
				g := s.(*LetStmt)
				g.Global, g.Pub, g.File = true, pub, p.file
				prog.Globals = append(prog.Globals, g)
			}
		case p.check(IMPL):
			// Methods are hoisted straight into the function list with a
			// receiver attached, so nothing downstream needs a notion of an
			// impl block.
			if b := p.parseImpl(); b != nil {
				for _, m := range b.Methods {
					m.File = p.file
				}
				prog.Funcs = append(prog.Funcs, b.Methods...)
			}
		default:
			if s := p.parseStmt(); s != nil {
				prog.Main = append(prog.Main, s)
			}
		}

		if p.i == before { // guarantee forward progress
			p.advance()
		}
		p.skipNewlines()
	}
	return prog
}

// parseImport reads `import "helpers.vl"`. The path is a plain string
// resolved relative to the file the import appears in - there is no
// module registry and no search path to learn.
func (p *Parser) parseImport() *ImportDecl {
	kw := p.advance() // 'import'
	pathTok := p.expect(STRING, `a file path in quotes, as in: import "helpers.vl"`)
	p.endStmt()
	if pathTok.Kind != STRING {
		return nil
	}
	return &ImportDecl{Span: at(kw), Path: pathTok.Lex, File: p.file}
}

// parseStruct reads `struct User { name: str, age: int }`. Fields are
// separated by line breaks or commas, whichever the author prefers.
func (p *Parser) parseStruct() *StructDecl {
	start := p.i
	kw := p.advance() // 'struct'
	name := p.expect(IDENT, "a struct name")
	d := &StructDecl{Span: at(kw), Name: name.Lex}
	if p.check(LT) {
		d.TypeParams = p.typeParams()
	}

	open := p.expect(LBRACE, "'{'")
	p.skipNewlines()

	for !p.check(RBRACE) && !p.check(EOF) {
		before := p.i

		fn := p.expect(IDENT, "a field name")
		f := StructField{Span: at(fn), Name: fn.Lex}
		if p.match(COLON) {
			f.Type = p.parseTypeRef()
		} else {
			p.errorAt(fn, "field %q needs a type, like %s: int", fn.Lex, fn.Lex)
		}
		d.Fields = append(d.Fields, f)

		p.match(COMMA)
		p.skipNewlines()
		if p.i == before { // guarantee forward progress
			p.advance()
		}
	}
	p.expectClose(RBRACE, open, "'}'", "struct")
	if d.TypeParams != nil {
		d.Toks = append([]Token(nil), p.toks[start:p.i]...)
	}
	p.endStmt()
	return d
}

// parseExternStruct reads a C layout, having consumed `extern`:
//
//	extern struct Player {
//	    health: f32 at 0x100
//	    pos: Vec3
//	    name: [16]u8
//	}
//
// Each field is a C type, another extern struct, or an array of either
// written [N]T. `at` pins a field to an offset; fields after it carry on
// from there.
func (p *Parser) parseExternStruct() *StructDecl {
	kw := p.advance() // 'struct'
	name := p.expect(IDENT, "a struct name")
	d := &StructDecl{Span: at(kw), Name: name.Lex, Extern: true}

	open := p.expect(LBRACE, "'{'")
	p.skipNewlines()

	for !p.check(RBRACE) && !p.check(EOF) {
		before := p.i

		fn := p.expect(IDENT, "a field name")
		f := StructField{Span: at(fn), Name: fn.Lex, At: -1}
		if p.match(COLON) {
			if p.match(LBRACKET) {
				n := p.expect(NUMBER, "an array length, as in [16]u8")
				if v, err := strconv.ParseInt(strings.ReplaceAll(n.Lex, "_", ""), 0, 64); err == nil && v > 0 {
					f.Len = int(v)
				} else if n.Kind == NUMBER {
					p.errorAt(n, "an array length has to be a positive whole number")
				}
				p.expect(RBRACKET, "']'")
			}
			stars := ""
			for p.match(STAR) {
				stars += "*"
			}
			t := p.expect(IDENT, "a C type such as i32, f32 or ptr")
			f.Type = stars + t.Lex
		} else {
			p.errorAt(fn, "field %q needs a type, like %s: i32", fn.Lex, fn.Lex)
		}
		if p.check(IDENT) && p.cur().Lex == "at" {
			p.advance()
			n := p.expect(NUMBER, "an offset, as in at 0x10")
			if v, err := strconv.ParseInt(strings.ReplaceAll(n.Lex, "_", ""), 0, 64); err == nil && v >= 0 {
				f.At = int(v)
			} else if n.Kind == NUMBER {
				p.errorAt(n, "an offset has to be a whole number of bytes")
			}
		}
		d.Fields = append(d.Fields, f)

		p.match(COMMA)
		p.skipNewlines()
		if p.i == before {
			p.advance()
		}
	}
	p.expectClose(RBRACE, open, "'}'", "struct")
	p.endStmt()
	return d
}

// parseEnum reads `enum State { Idle, Running, Done }`. The variants
// may be separated by commas, newlines or both.
func (p *Parser) parseEnum() *EnumDecl {
	start := p.i
	kw := p.advance() // 'enum'
	name := p.expect(IDENT, "an enum name")
	d := &EnumDecl{Span: at(kw), Name: name.Lex}
	if p.check(LT) {
		d.TypeParams = p.typeParams()
	}
	open := p.expect(LBRACE, "'{'")
	p.skipNewlines()
	for !p.check(RBRACE) && !p.check(EOF) {
		before := p.i
		v := p.expect(IDENT, "a variant name")
		if v.Kind == IDENT {
			// Circle(r: float): the variant carries fields, written as a
			// function's parameters are.
			var payload []Param
			if p.check(LPAREN) {
				sig := p.parseFnSignature(v, v.Lex)
				if sig != nil {
					payload = sig.Params
					if payload == nil {
						payload = []Param{}
					}
					if sig.Ret != "" || sig.Variadic {
						p.errorAt(v, "a variant lists what it holds, as in %s(x: int, y: int)", v.Lex)
					}
					for _, prm := range payload {
						if prm.Name == "self" {
							p.errorAt(v, "a variant's fields need names and types, as in %s(x: int)", v.Lex)
						}
					}
				}
				d.Data = true
			}
			d.Variants = append(d.Variants, v.Lex)
			d.Payloads = append(d.Payloads, payload)
		}
		p.match(COMMA)
		p.skipNewlines()
		if p.i == before {
			p.advance()
		}
	}
	p.expectClose(RBRACE, open, "'}'", "enum")
	if d.TypeParams != nil {
		d.Data = true
		d.Toks = append([]Token(nil), p.toks[start:p.i]...)
	}
	p.endStmt()
	return d
}

// parseInterface reads `interface Name { fn m(self, x: int) -> str }`:
// method signatures with no bodies, each taking self first.
func (p *Parser) parseInterface() *InterfaceDecl {
	kw := p.advance() // 'interface'
	name := p.expect(IDENT, "an interface name")
	d := &InterfaceDecl{Span: at(kw), Name: name.Lex}
	open := p.expect(LBRACE, "'{'")
	p.skipNewlines()
	for !p.check(RBRACE) && !p.check(EOF) {
		before := p.i
		if !p.check(FN) {
			p.errorAt(p.cur(), "an interface lists methods, as in fn area(self) -> float")
			p.synchronize()
		} else {
			fk := p.advance()
			mn := p.expect(IDENT, "a method name")
			if m := p.parseFnSignature(fk, mn.Lex); m != nil {
				if len(m.Params) == 0 || m.Params[0].Name != "self" || m.Params[0].Type != "" {
					p.errorAt(mn, "%s needs self first, as in fn %s(self)", mn.Lex, mn.Lex)
				}
				if p.check(LBRACE) {
					p.errorAt(p.cur(), "an interface's methods have no body - the structs give them")
					p.parseBlock()
				}
				m.Recv = d.Name
				d.Methods = append(d.Methods, m)
			}
		}
		p.match(COMMA)
		p.skipNewlines()
		if p.i == before {
			p.advance()
		}
	}
	p.expectClose(RBRACE, open, "'}'", "interface")
	p.endStmt()
	return d
}

// parseImpl reads `impl User { fn greet(self) -> str { ... } }` and
// returns the methods with their receiver attached.
func (p *Parser) parseImpl() *ImplBlock {
	kw := p.advance() // 'impl'
	name := p.expect(IDENT, "a struct name after 'impl'")
	b := &ImplBlock{Span: at(kw), Type: name.Lex}
	var params []string
	if p.check(LT) {
		params = p.typeParams()
	}

	open := p.expect(LBRACE, "'{'")
	p.skipNewlines()

	for !p.check(RBRACE) && !p.check(EOF) {
		before := p.i
		switch {
		case p.check(PUB):
			// A method is as visible as the struct it belongs to, so
			// marking one individually would mean nothing.
			p.errorAt(p.cur(), "methods are as visible as their struct - put 'pub' on 'struct %s' instead",
				b.Type)
			p.advance()

		case p.check(FN):
			start := p.i
			m := p.parseFn()
			if m != nil {
				m.Recv = b.Type
				if params != nil {
					m.RecvParams = params
					m.Toks = append([]Token(nil), p.toks[start:p.i]...)
				}
				b.Methods = append(b.Methods, m)
			}

		default:
			p.errorAt(p.cur(), "an impl block can only contain functions")
			p.advance()
		}
		p.skipNewlines()
		if p.i == before {
			p.advance()
		}
	}
	p.expectClose(RBRACE, open, "'}'", "impl block")
	p.endStmt()
	return b
}

func (p *Parser) parseFn() *FnDecl {
	start := p.i
	kw := p.advance() // 'fn'
	name := p.expect(IDENT, "a function name")
	var params []string
	if p.check(LT) {
		params = p.typeParams()
	}
	f := p.parseFnSignature(kw, name.Lex)
	if f == nil {
		return nil
	}
	f.Body = p.parseBlock()
	if params != nil {
		f.TypeParams = params
		f.Toks = append([]Token(nil), p.toks[start:p.i]...)
	}
	p.endStmt()
	return f
}

// typeParams reads the `<T, U>` after a generic declaration's name.
func (p *Parser) typeParams() []string {
	open := p.advance() // '<'
	params := []string{}
	for {
		t := p.expect(IDENT, "a type parameter name, as in <T>")
		if t.Kind != IDENT {
			break
		}
		for _, q := range params {
			if q == t.Lex {
				p.errorAt(t, "type parameter %s is listed twice", t.Lex)
			}
		}
		params = append(params, t.Lex)
		if !p.match(COMMA) {
			break
		}
	}
	if !p.closeAngle() {
		p.errorAt(p.cur(), "expected '>' to close the '<' at %d:%d, found %s", open.Line, open.Col, describe(p.cur()))
	}
	return params
}

// closeAngle consumes one '>', which may be half of a '>>'.
func (p *Parser) closeAngle() bool {
	switch {
	case p.pendingGT > 0:
		p.pendingGT--
		return true
	case p.check(GT):
		p.advance()
		return true
	case p.check(SHR):
		p.advance()
		p.pendingGT++
		return true
	}
	return false
}

// typeArgs reads `<int, []str>` after a generic type's name and returns
// the arguments as written.
func (p *Parser) typeArgs() ([]string, bool) {
	p.advance() // '<'
	var args []string
	for {
		args = append(args, p.parseTypeRef())
		if !p.match(COMMA) {
			break
		}
	}
	return args, p.closeAngle()
}

// tryTypeArgs reads `<int>` after a name in an expression, which is
// either a generic struct literal, Box<int>{v: 1}, or a call naming its
// types, max<int>(a, b). Anything else - a comparison - puts the parser
// back where it was.
func (p *Parser) tryTypeArgs() ([]string, bool) {
	i, errs, gt := p.i, len(p.Errors), p.pendingGT
	args, ok := p.typeArgs()
	if ok && p.pendingGT == 0 && len(p.Errors) == errs &&
		(p.check(LPAREN) || p.check(DOT) || p.check(LBRACE) && p.noBrace == 0) {
		return args, true
	}
	p.i, p.Errors, p.pendingGT = i, p.Errors[:errs], gt
	return nil, false
}

// GenericName is how an instance of a generic is named: Box<int>,
// Pair<str, []int>. The spelling is canonical - one space after each
// comma - so the same types always make the same name.
func GenericName(base string, args []string) string {
	return base + "<" + strings.Join(args, ", ") + ">"
}

// parseExtern reads a native function declaration:
//
//	extern fn MessageBoxA(hwnd: int, text: str, cap: str, kind: int) -> int
//	extern fn mz_extract(zipPath: str, dest: str) -> int from "miniz.dll"
//
// There is no body. The declaration states the symbol's ABI, and every
// call to it compiles straight to that symbol in the named library.
// The optional `from "<dll>"` tail names the exporting DLL; without it,
// the backend's own rule decides where the symbol comes from.
//
// `from` is matched as an identifier rather than reserved, so it stays
// usable as an ordinary variable name everywhere else.
func (p *Parser) parseExtern() *FnDecl {
	kw := p.advance() // 'extern'
	if !p.match(FN) {
		p.errorAt(p.cur(), "'extern' declares a native function, as in: extern fn Beep(freq: int, ms: int) -> bool")
		p.synchronize()
		return nil
	}
	name := p.expect(IDENT, "a function name")
	f := p.parseFnSignature(kw, name.Lex)
	if f == nil {
		return nil
	}
	f.Extern = true

	if p.check(IDENT) && p.cur().Lex == "from" {
		p.advance()
		dll := p.expect(STRING, `a library name in quotes, as in: from "miniz.dll"`)
		if dll.Kind == STRING {
			f.DLL = dll.Lex
		}
	}
	p.endStmt()
	return f
}

// parseFnSignature reads the parameters and return type, having already
// consumed `fn` and, for a declaration, the name. A function literal
// takes the same path with an empty name, so the two never drift.
func (p *Parser) parseFnSignature(kw Token, name string) *FnDecl {
	if name == "" {
		p.advance() // the 'fn' of a literal, which the caller only peeked
	}
	f := &FnDecl{Span: at(kw), Name: name}

	open := p.expect(LPAREN, "'('")
	p.skipNewlines()

	if !p.check(RPAREN) {
		for {
			// A trailing `...` marks a variadic parameter list, which only
			// an extern declaration can carry: printf(fmt: str, ...)
			if p.check(ELLIPSIS) {
				e := p.advance()
				f.Variadic = true
				if p.match(COMMA) {
					p.errorAt(e, "'...' must come last in the parameter list")
					p.skipNewlines()
					continue
				}
				break
			}

			// `self` is a parameter with no type: the impl block supplies it.
			if p.check(SELF) {
				sf := p.advance()
				f.Params = append(f.Params, Param{Span: at(sf), Name: "self"})
				p.skipNewlines()
				if !p.match(COMMA) {
					break
				}
				p.skipNewlines()
				continue
			}
			pn := p.expect(IDENT, "a parameter name")
			prm := Param{Span: at(pn), Name: pn.Lex}
			if p.match(COLON) {
				prm.Type = p.parseTypeRef()
			} else {
				p.errorAt(pn, "parameter %q needs a type, like %s: int", pn.Lex, pn.Lex)
			}
			if p.match(ASSIGN) {
				prm.Default = p.grouped(func() Expr { return p.parseExpr(0) })
			}
			f.Params = append(f.Params, prm)

			p.skipNewlines()
			if !p.match(COMMA) {
				break
			}
			p.skipNewlines()
		}
	}
	p.skipNewlines()
	p.expectClose(RPAREN, open, "')'", "parameter list")

	if p.match(ARROW) {
		f.Ret = p.parseTypeRef()
	}
	return f
}

// parseTypeRef reads a type annotation and returns its canonical source
// text. The checker turns that text into a Type; keeping it as a string
// here means the parser needs no knowledge of the type system.
//
//	int    []str    [][]int    {str: int}    {str: []int}
func (p *Parser) parseTypeRef() string {
	var base string
	switch {
	case p.check(FN):
		// fn(int, str) -> bool
		p.advance()
		p.expect(LPAREN, "'(' after fn in a type")
		params := []string{}
		if !p.check(RPAREN) {
			for {
				params = append(params, p.parseTypeRef())
				if !p.match(COMMA) {
					break
				}
			}
		}
		p.expect(RPAREN, "')'")
		base = "fn(" + strings.Join(params, ", ") + ")"
		if p.match(ARROW) {
			base += " -> " + p.parseTypeRef()
		}

	case p.match(QUESTION):
		base = "?" + p.parseTypeRef()

	case p.match(STAR):
		// *i32, *Player, **u8: a typed pointer.
		base = "*" + p.parseTypeRef()

	case p.match(LBRACKET):
		p.expect(RBRACKET, "']' - a list type is written []int")
		base = "[]" + p.parseTypeRef()

	case p.match(LBRACE):
		key := p.parseTypeRef()
		p.expect(COLON, "':' - a map type is written {str: int}")
		val := p.parseTypeRef()
		p.expect(RBRACE, "'}'")
		base = "{" + key + ": " + val + "}"

	default:
		name := p.expect(IDENT, "a type name")
		base = name.Lex
		if p.check(LT) {
			args, ok := p.typeArgs()
			if !ok {
				p.errorAt(p.cur(), "expected '>' to close the type arguments of %s, found %s",
					name.Lex, describe(p.cur()))
			}
			base = GenericName(name.Lex, args)
		}
	}

	// A trailing `!` makes it a result type: str! is a str or a failure.
	if p.match(BANG) {
		base += "!"
	}
	return base
}

// ---- statements ----

func (p *Parser) parseStmt() Stmt {
	p.stmtErrMark = len(p.Errors)
	switch p.cur().Kind {
	case LET, CONST:
		return p.parseLet()
	case IF:
		return p.parseIf()
	case WHILE:
		return p.parseWhile()
	case FOR:
		return p.parseFor()
	case MATCH:
		return p.parseMatch()
	case BREAK:
		t := p.advance()
		p.endStmt()
		return &BreakStmt{Span: at(t)}
	case CONTINUE:
		t := p.advance()
		p.endStmt()
		return &ContinueStmt{Span: at(t)}
	case RETURN:
		return p.parseReturn()
	case DEFER:
		// `defer stmt`: stmt runs when this block is left.
		kw := p.advance()
		body := p.parseStmt()
		if body == nil {
			return nil
		}
		return &DeferStmt{Span: at(kw), Body: body}
	case LBRACE:
		return p.parseBlock()
	case FN:
		p.errorAt(p.cur(), "functions can only be declared at the top level")
		p.synchronize()
		return nil
	case STRUCT, IMPL, EXTERN:
		p.errorAt(p.cur(), "'%s' can only appear at the top level", p.cur().Lex)
		p.synchronize()
		return nil
	default:
		return p.parseSimpleStmt()
	}
}

func (p *Parser) parseLet() Stmt {
	kw := p.advance()
	name := p.expect(IDENT, "a variable name")

	st := &LetStmt{Span: at(kw), Name: name.Lex, Const: kw.Kind == CONST}

	if p.match(COLON) {
		st.Type = p.parseTypeRef()
	}
	p.expect(ASSIGN, "'='")
	st.Value = p.parseExpr(0)
	p.endStmt()
	return st
}

func (p *Parser) parseReturn() Stmt {
	kw := p.advance()
	st := &ReturnStmt{Span: at(kw)}

	// A bare `return` is one followed immediately by a line break or '}'.
	if !p.check(NEWLINE) && !p.check(RBRACE) && !p.check(EOF) {
		st.Value = p.parseExpr(0)
	}
	p.endStmt()
	return st
}

func (p *Parser) parseIf() Stmt {
	kw := p.advance()
	st := &IfStmt{Span: at(kw)}
	st.Cond = p.header(func() Expr { return p.parseExpr(0) })
	st.Then = p.parseBlock()

	if p.check(ELSE) {
		p.advance()
		if p.check(IF) {
			st.Else = p.parseIf()
			return st // the nested if already consumed its terminator
		}
		st.Else = p.parseBlock()
	}
	p.endStmt()
	return st
}

func (p *Parser) parseWhile() Stmt {
	kw := p.advance()
	st := &WhileStmt{Span: at(kw)}
	st.Cond = p.header(func() Expr { return p.parseExpr(0) })
	st.Body = p.parseBlock()
	p.endStmt()
	return st
}

func (p *Parser) parseFor() Stmt {
	kw := p.advance()
	st := &ForStmt{Span: at(kw)}

	name := p.expect(IDENT, "a loop variable name")
	st.Var = name.Lex

	// `for k, v in map` binds two names.
	if p.match(COMMA) {
		st.Var2 = p.expect(IDENT, "a second loop variable name").Lex
	}

	if !p.match(IN) {
		p.errorAt(p.cur(), "expected 'in', as in: for %s in 0..10 { ... }", st.Var)
	}

	first := p.header(func() Expr { return p.parseExpr(0) })

	// A range if `..` or `..=` follows; otherwise a collection to iterate.
	switch {
	case p.match(DOTDOTEQ):
		st.Inclusive = true
	case p.match(DOTDOT):
	default:
		st.Coll = first
		if p.check(STEP) {
			p.errorAt(p.cur(), "'step' only applies to a range, as in: for i in 0..10 step 2")
		}
		st.Body = p.parseBlock()
		p.endStmt()
		return st
	}

	if st.Var2 != "" {
		p.errorAt(kw, "a range loop binds one variable, not two")
	}

	st.Start = first
	st.End = p.header(func() Expr { return p.parseExpr(0) })

	if p.match(STEP) {
		st.Step = p.header(func() Expr { return p.parseExpr(0) })
	}

	st.Body = p.parseBlock()
	p.endStmt()
	return st
}

// parseMatch reads a multi-way branch:
//
//	match code {
//	    200      => print("ok")
//	    404, 410 => print("gone")
//	    else     => print("something else")
//	}
//
// An arm's body is a single statement or a block. Arms do not fall
// through - there is no `break` to forget.
func (p *Parser) parseMatch() Stmt {
	kw := p.advance()
	st := &MatchStmt{Span: at(kw)}
	st.Subject = p.header(func() Expr { return p.parseExpr(0) })

	p.expect(LBRACE, "'{' to open the match")
	p.skipNewlines()

	for !p.check(RBRACE) && !p.check(EOF) {
		before := p.i

		if p.check(ELSE) {
			el := p.advance()
			p.expect(FATARROW, "'=>' after 'else'")
			if st.Else != nil {
				p.errorAt(el, "a match can only have one 'else' arm")
			}
			st.Else = p.parseArmBody()
			p.skipNewlines()
			if p.i == before {
				p.advance()
			}
			continue
		}

		arm := MatchCase{Span: at(p.cur())}
		for {
			arm.Values = append(arm.Values, p.header(func() Expr { return p.parseExpr(0) }))
			if !p.match(COMMA) {
				break
			}
			p.skipNewlines()
		}
		p.expect(FATARROW, "'=>' after the values of a match arm")
		arm.Body = p.parseArmBody()
		st.Cases = append(st.Cases, arm)

		p.skipNewlines()
		if p.i == before { // guarantee forward progress
			p.advance()
		}
	}

	p.expectClose(RBRACE, kw, "'}'", "match")
	p.endStmt()
	return st
}

func (p *Parser) parseArmBody() Stmt {
	if p.check(LBRACE) {
		return p.parseBlock()
	}
	return p.parseStmt()
}

func (p *Parser) parseBlock() *Block {
	lb := p.expect(LBRACE, "'{'")
	b := &Block{Span: at(lb)}
	p.skipNewlines()

	for !p.check(RBRACE) && !p.check(EOF) {
		before := p.i
		s := p.parseStmt()
		if s != nil {
			b.Stmts = append(b.Stmts, s)
		}
		if p.i == before {
			p.advance()
		}
		p.skipNewlines()
	}
	p.expectClose(RBRACE, lb, "'}'", "block")
	return b
}

// parseSimpleStmt handles both `foo(1)` and `x += 2`.
func (p *Parser) parseSimpleStmt() Stmt {
	start := p.cur()
	x := p.parseExpr(0)

	switch p.cur().Kind {
	case ASSIGN, PLUSEQ, MINUSEQ, STAREQ, SLASHEQ,
		PERCENTEQ, AMPEQ, PIPEEQ, CARETEQ, SHLEQ, SHREQ:
		op := p.advance()
		switch t := x.(type) {
		case *Ident, *Index, *Field:
		case *Unary:
			if t.Op != STAR {
				p.errorAt(start, "left side of assignment must be a variable, a field, or an index like xs[0]")
				p.synchronize()
				return nil
			}
		default:
			p.errorAt(start, "left side of assignment must be a variable, a field, or an index like xs[0]")
			p.synchronize()
			return nil
		}
		st := &AssignStmt{Span: at(start), Target: x, Op: op.Kind}
		st.Value = p.parseExpr(0)
		p.endStmt()
		return st
	}

	p.endStmt()
	return &ExprStmt{Span: at(start), X: x}
}

// endStmt enforces that a statement is followed by a newline, '}' or EOF.
// This is what lets Veyl skip semicolons.
func (p *Parser) endStmt() {
	if p.check(NEWLINE) {
		p.advance()
		return
	}
	if p.check(RBRACE) || p.check(EOF) {
		return
	}
	// If this statement already produced an error, whatever the cursor
	// is sitting on is debris from that, not a second mistake. One
	// missing `]` should not also be reported as a stray `print`.
	if len(p.Errors) > p.stmtErrMark {
		p.synchronize()
		return
	}
	p.errorAt(p.cur(), "unexpected %s after statement", describe(p.cur()))
	p.synchronize()
}

// ---- expressions (Pratt) ----

// precOf returns binding power; 0 means "not a binary operator".
//
// The ladder follows C's, so anyone who has met `&` and `<<` before
// finds them where they expect. That does mean `a & b == c` parses as
// `a & (b == c)`, which is C's famous wart - the checker turns the
// resulting type error into a message suggesting parentheses.
func precOf(k Kind) int {
	switch k {
	case OR:
		return 1
	case AND:
		return 2
	case PIPE:
		return 3
	case CARET:
		return 4
	case AMP:
		return 5
	case EQ, NEQ:
		return 6
	case LT, LTE, GT, GTE:
		return 7
	case SHL, SHR:
		return 8
	case PLUS, MINUS:
		return 9
	case STAR, SLASH, PERCENT:
		return 10
	}
	return 0
}

func (p *Parser) parseExpr(minPrec int) Expr {
	left := p.parseUnary()

	for {
		// `x as *T` binds tighter than any binary operator, as in Rust:
		// a + b as *u8 is a + (b as *u8).
		if p.check(IDENT) && p.cur().Lex == "as" {
			kw := p.advance()
			left = &Cast{Span: at(kw), X: left, Type: p.parseTypeRef()}
			continue
		}
		prec := precOf(p.cur().Kind)
		if prec == 0 || prec < minPrec {
			break
		}
		op := p.advance()
		// prec+1 makes these left-associative: a-b-c parses as (a-b)-c
		right := p.parseExpr(prec + 1)
		left = &Binary{Span: at(op), Op: op.Kind, L: left, R: right}
	}
	return left
}

func (p *Parser) parseUnary() Expr {
	if p.check(BANG) || p.check(MINUS) || p.check(TILDE) || p.check(STAR) || p.check(AMP) {
		op := p.advance()
		return &Unary{Span: at(op), Op: op.Kind, X: p.parseUnary()}
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() Expr {
	x := p.parsePrimary()
	for {
		switch {
		case p.check(LPAREN):
			lp := p.advance()
			call := &Call{Span: at(lp), Callee: x}
			p.skipNewlines()

			if !p.check(RPAREN) {
				for {
					call.Args = append(call.Args, p.grouped(func() Expr { return p.parseExpr(0) }))
					p.skipNewlines()
					if !p.match(COMMA) {
						break
					}
					p.skipNewlines()
				}
			}
			p.skipNewlines()
			p.expectClose(RPAREN, lp, "')'", "call")
			x = call

		case p.check(LBRACKET):
			lb := p.advance()
			p.skipNewlines()
			idx := p.grouped(func() Expr { return p.parseExpr(0) })
			p.skipNewlines()
			p.expectClose(RBRACKET, lb, "']'", "index")
			x = &Index{Span: at(lb), X: x, Idx: idx}

		case p.check(LBRACE) && p.noBrace == 0 && isStructLitTarget(x):
			x = p.parseStructLit(x.(*Ident))

		case p.check(QUESTION):
			q := p.advance()
			x = &Try{Span: at(q), X: x}

		case p.check(DOT):
			dot := p.advance()
			name := p.expect(IDENT, "a name after '.'")
			fname := name.Lex
			// b.map<str>(f): a generic method with its types named.
			if p.check(LT) && p.peekKind(1) != EOF {
				save := p.i
				if args, ok := p.tryTypeArgs(); ok && p.check(LPAREN) {
					fname = GenericName(fname, args)
				} else {
					p.i = save
				}
			}
			x = &Field{Span: at(dot), X: x, Name: fname}

		default:
			return x
		}
	}
}

// isFloatLexeme decides whether a NUMBER token is a float.
//
// A dot is the obvious mark. An exponent is the other one: 1e9 is a
// float with no dot in it, and 6.02e23 has both. The check for e is
// guarded by the base, because 0xE is a hex digit and 0b1 has no
// exponent form at all - without that, every hex literal containing an
// E would become a float.
func isFloatLexeme(lex string) bool {
	if strings.HasPrefix(lex, "0x") || strings.HasPrefix(lex, "0X") ||
		strings.HasPrefix(lex, "0b") || strings.HasPrefix(lex, "0B") {
		return false
	}
	return strings.ContainsAny(lex, ".eE")
}

func (p *Parser) parsePrimary() Expr {
	t := p.cur()

	switch t.Kind {
	case NUMBER:
		p.advance()
		if isFloatLexeme(t.Lex) {
			return &FloatLit{Span: at(t), Val: t.Lex}
		}
		return &IntLit{Span: at(t), Val: t.Lex}

	case STRING:
		p.advance()
		return p.parseStringLit(t)

	case RAWSTRING:
		// No escapes and no interpolation - the text is the value.
		p.advance()
		return &StrLit{Span: at(t), Val: t.Lex}

	case TRUE:
		p.advance()
		return &BoolLit{Span: at(t), Val: true}

	case FALSE:
		p.advance()
		return &BoolLit{Span: at(t), Val: false}

	case NIL:
		p.advance()
		return &NilLit{Span: at(t)}

	case IDENT:
		// new T, new T{...}, new [n]T. `new` is only a keyword here, in
		// front of a type, so atomic.new(0) and a variable called new
		// are untouched.
		if t.Lex == "new" {
			switch p.peekKind(1) {
			case IDENT, STAR, LBRACKET:
				return p.parseNew()
			}
		}
		p.advance()
		if p.check(LT) {
			if args, ok := p.tryTypeArgs(); ok {
				return &Ident{Span: at(t), Name: GenericName(t.Lex, args)}
			}
		}
		return &Ident{Span: at(t), Name: t.Lex}

	case SELF:
		// `self` is a keyword so it cannot be declared as a variable, but
		// inside a method it reads as an ordinary name.
		p.advance()
		return &Ident{Span: at(t), Name: "self"}

	case LPAREN:
		open := p.advance()
		p.skipNewlines()
		x := p.grouped(func() Expr { return p.parseExpr(0) })
		p.skipNewlines()
		p.expectClose(RPAREN, open, "')'", "group")
		return x

	case LBRACKET:
		return p.parseListLit()

	case LBRACE:
		return p.parseMapLit()

	case FN:
		// An anonymous function used as a value.
		f := p.parseFnSignature(t, "")
		if f == nil {
			return &StrLit{Span: at(t), Val: ""}
		}
		f.Body = p.parseBlock()
		return &FuncLit{Span: at(t), Decl: f}
	}

	p.errorAt(t, "expected an expression, found %s", describe(t))
	p.advance()
	return &StrLit{Span: at(t), Val: ""} // placeholder so later passes don't nil-panic
}

// parseNew reads what follows `new`.
func (p *Parser) parseNew() Expr {
	kw := p.advance()
	n := &NewExpr{Span: at(kw)}
	if lb := p.cur(); p.match(LBRACKET) {
		p.skipNewlines()
		n.Count = p.grouped(func() Expr { return p.parseExpr(0) })
		p.skipNewlines()
		p.expectClose(RBRACKET, lb, "']'", "new [n]T")
		n.Type = p.parseTypeRef()
		return n
	}
	if p.check(IDENT) && p.peekKind(1) == LBRACE && p.noBrace == 0 {
		name := p.advance()
		if lit, ok := p.parseStructLit(&Ident{Span: at(name), Name: name.Lex}).(*StructLit); ok {
			n.Lit = lit
		}
		n.Type = name.Lex
		return n
	}
	n.Type = p.parseTypeRef()
	return n
}

// isStructLitTarget reports whether `x {` should be read as a struct
// literal. Only a bare name can name a struct, and only a capitalised
// one by convention - but the convention is not enforced, so any plain
// identifier qualifies and the checker decides whether it exists.
func isStructLitTarget(x Expr) bool {
	_, ok := x.(*Ident)
	return ok
}

// parseStructLit reads the `{name: "ada", age: 36}` half of a struct
// literal, the name having already been consumed.
func (p *Parser) parseStructLit(name *Ident) Expr {
	line, col := name.Pos()
	lit := &StructLit{Span: Span{Line: line, Col: col}, Name: name.Name}

	open := p.advance() // '{'
	p.skipNewlines()

	for !p.check(RBRACE) && !p.check(EOF) {
		before := p.i

		fn := p.expect(IDENT, "a field name")
		p.expect(COLON, "':' after a field name")
		p.skipNewlines()
		lit.Fields = append(lit.Fields, fn.Lex)
		lit.Vals = append(lit.Vals, p.grouped(func() Expr { return p.parseExpr(0) }))

		p.skipNewlines()
		if !p.match(COMMA) {
			break
		}
		p.skipNewlines()
		if p.i == before {
			p.advance()
		}
	}
	p.skipNewlines()
	p.expectClose(RBRACE, open, "'}'", "struct literal")
	return lit
}

// parseListLit reads `[]`, `[1, 2, 3]`, or a list spread over lines.
func (p *Parser) parseListLit() Expr {
	lb := p.advance() // '['
	lit := &ListLit{Span: at(lb)}
	p.skipNewlines()

	for !p.check(RBRACKET) && !p.check(EOF) {
		lit.Elems = append(lit.Elems, p.parseExpr(0))
		p.skipNewlines()
		if !p.match(COMMA) {
			break
		}
		p.skipNewlines()
	}
	p.skipNewlines()
	p.expectClose(RBRACKET, lb, "']'", "list")
	return lit
}

// parseMapLit reads `{}` or `{"a": 1, "b": 2}`.
//
// A map literal cannot start a statement, because `{` there opens a
// block. That costs nothing in practice - a bare map literal as a
// statement would do nothing anyway.
func (p *Parser) parseMapLit() Expr {
	lb := p.advance() // '{'
	lit := &MapLit{Span: at(lb)}
	p.skipNewlines()

	for !p.check(RBRACE) && !p.check(EOF) {
		lit.Keys = append(lit.Keys, p.parseExpr(0))
		p.expect(COLON, "':' between a map key and its value")
		p.skipNewlines()
		lit.Vals = append(lit.Vals, p.parseExpr(0))
		p.skipNewlines()
		if !p.match(COMMA) {
			break
		}
		p.skipNewlines()
	}
	p.skipNewlines()
	p.expectClose(RBRACE, lb, "'}'", "map")
	return lit
}

// parseStringLit splits "a {x} b" into literal and expression parts.
// Literal braces are written {{ and }}.
func (p *Parser) parseStringLit(t Token) Expr {
	src := t.Lex
	if !strings.ContainsAny(src, "{}") {
		return &StrLit{Span: at(t), Val: src}
	}

	var parts []InterpPart
	var lit strings.Builder

	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, InterpPart{Lit: lit.String()})
			lit.Reset()
		}
	}

	for i := 0; i < len(src); i++ {
		c := src[i]

		if c == '}' {
			if i+1 < len(src) && src[i+1] == '}' {
				lit.WriteByte('}')
				i++
				continue
			}
			p.errorAt(t, "unmatched '}' in string (write '}}' for a literal brace)")
			lit.WriteByte('}')
			continue
		}

		if c != '{' {
			lit.WriteByte(c)
			continue
		}
		if i+1 < len(src) && src[i+1] == '{' {
			lit.WriteByte('{')
			i++
			continue
		}

		// Find the matching close brace, stepping over nested braces and
		// over any string literal that appears inside the expression.
		j, d, quoted := i+1, 1, false
		for ; j < len(src); j++ {
			ch := src[j]
			if ch == '"' {
				quoted = !quoted
				continue
			}
			if quoted {
				continue
			}
			if ch == '{' {
				d++
			} else if ch == '}' {
				d--
				if d == 0 {
					break
				}
			}
		}
		if j >= len(src) {
			p.errorAt(t, "unclosed '{' in string")
			lit.WriteByte('{')
			continue
		}
		inner := strings.TrimSpace(src[i+1 : j])
		i = j

		if inner == "" {
			// Almost always someone writing JSON or a literal brace, now
			// that there is a json library to write it for.
			p.errorAt(t, "empty {} in string - for a literal brace write {{}}")
			continue
		}

		if x := p.parseSubExpr(inner, t); x != nil {
			flush()
			parts = append(parts, InterpPart{X: x})
		}
	}
	flush()

	if len(parts) == 1 && parts[0].X == nil {
		return &StrLit{Span: at(t), Val: parts[0].Lit}
	}
	return &Interp{Span: at(t), Parts: parts}
}

// parseSubExpr runs a nested lexer+parser over the text inside {}.
// Positions inside interpolations point at the enclosing string for now.
func (p *Parser) parseSubExpr(src string, host Token) Expr {
	lx := NewLexer(p.file, src)
	toks := lx.Scan()
	p.Errors = append(p.Errors, lx.Errors...)

	sub := NewParser(p.file, toks)
	sub.skipNewlines()
	x := sub.parseExpr(0)
	sub.skipNewlines()

	if !sub.check(EOF) {
		p.errorAt(host, "unexpected %s inside {} in string - for literal braces write {{ and }}",
			describe(sub.cur()))
	}
	p.Errors = append(p.Errors, sub.Errors...)

	// The nested lexer starts its own line and column count, so every
	// node in here would otherwise claim to be at 1:something. Point the
	// whole subtree at the string that contains it. That is coarse - one
	// position for the entire interpolation - but it is inside the right
	// line, which is what makes an error findable.
	reanchor(x, at(host))
	return x
}

// reanchor rewrites the position of an expression and everything under
// it. Nodes embed Span by value, so the pointer receiver reaches it.
func reanchor(e Expr, at Span) {
	if e == nil {
		return
	}
	if s, ok := e.(interface{ setPos(Span) }); ok {
		s.setPos(at)
	}
	switch x := e.(type) {
	case *Unary:
		reanchor(x.X, at)
	case *Cast:
		reanchor(x.X, at)
	case *NewExpr:
		if x.Count != nil {
			reanchor(x.Count, at)
		}
		if x.Lit != nil {
			reanchor(x.Lit, at)
		}
	case *Binary:
		reanchor(x.L, at)
		reanchor(x.R, at)
	case *Call:
		reanchor(x.Callee, at)
		for _, a := range x.Args {
			reanchor(a, at)
		}
	case *Index:
		reanchor(x.X, at)
		reanchor(x.Idx, at)
	case *Field:
		reanchor(x.X, at)
	case *Try:
		reanchor(x.X, at)
	case *ListLit:
		for _, el := range x.Elems {
			reanchor(el, at)
		}
	case *MapLit:
		for i := range x.Keys {
			reanchor(x.Keys[i], at)
			reanchor(x.Vals[i], at)
		}
	case *StructLit:
		for _, v := range x.Vals {
			reanchor(v, at)
		}
	case *Interp:
		for _, part := range x.Parts {
			reanchor(part.X, at)
		}
	}
}
