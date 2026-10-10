package main

// veyl lsp: a language server, over stdin and stdout.
//
// Any editor that speaks the Language Server Protocol can use it; the VS
// Code extension in editors/vscode starts it. What it answers:
//
//   - errors as you type: the same ones veyl build reports, each at its
//     place in the file;
//   - completion, of names in scope, the library, a struct's fields and
//     methods after a dot, and an enum's variants;
//   - go to definition, for functions, methods, structs, enums,
//     interfaces, fields, globals and locals, across imported files;
//   - hover, the type of a variable or the signature of a function;
//   - the outline of a file.
//
// Each change runs the compiler's front half - parse, imports, prelude,
// type check - over the text in the editor, never the copy on disk, and
// stops before lowering. Nothing in that half ends the process, and a
// panic in it is caught, so a half-typed program cannot take the server
// down. The declarations and locals are collected from the parsed tree
// before checking, and the checker fills in their types on those same
// nodes, so a local declared without a type still hovers with one.
//
// When the text does not parse, completion and definitions come from the
// last version that did, which is usually one keystroke ago.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	front "veylfront"
)

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type lspPos struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPos `json:"start"`
	End   lspPos `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspServer struct {
	out  io.Writer
	docs map[string]*lspDoc // by URI
}

type lspDoc struct {
	uri, path string
	text      string
	index     *lspIndex // the last version that type-checked, or failing that parsed
	broken    bool      // the current text does not parse
}

func lspCommand() {
	s := &lspServer{out: os.Stdout, docs: map[string]*lspDoc{}}
	s.serve(os.Stdin)
}

func (s *lspServer) serve(in io.Reader) {
	r := bufio.NewReader(in)
	for {
		body, err := readMessage(r)
		if err != nil {
			return
		}
		var m rpcMsg
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		if m.Method == "exit" {
			return
		}
		s.handle(&m)
	}
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	n := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			n, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	if n < 0 {
		return nil, fmt.Errorf("no Content-Length")
	}
	body := make([]byte, n)
	_, err := io.ReadFull(r, body)
	return body, err
}

func (s *lspServer) send(m rpcMsg) {
	m.JSONRPC = "2.0"
	b, _ := json.Marshal(m)
	fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n%s", len(b), b)
}

func (s *lspServer) reply(id json.RawMessage, result any) {
	if result == nil {
		// A result of null has to be sent as one; omitempty would drop it.
		b, _ := json.Marshal(struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Result  any             `json:"result"`
		}{"2.0", id, nil})
		fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n%s", len(b), b)
		return
	}
	s.send(rpcMsg{ID: id, Result: result})
}

func (s *lspServer) notify(method string, params any) {
	b, _ := json.Marshal(params)
	s.send(rpcMsg{Method: method, Params: b})
}

func (s *lspServer) handle(m *rpcMsg) {
	defer func() {
		if r := recover(); r != nil && m.ID != nil {
			s.send(rpcMsg{ID: m.ID, Error: &rpcError{Code: -32603, Message: fmt.Sprint(r)}})
		}
	}()
	type docParams struct {
		TextDocument struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
		Position lspPos `json:"position"`
	}
	var p docParams
	_ = json.Unmarshal(m.Params, &p)

	switch m.Method {
	case "initialize":
		s.reply(m.ID, map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":       1, // the whole text on every change
				"completionProvider":     map[string]any{"triggerCharacters": []string{"."}},
				"definitionProvider":     true,
				"hoverProvider":          true,
				"documentSymbolProvider": true,
			},
			"serverInfo": map[string]any{"name": "veyl", "version": Version},
		})
	case "shutdown":
		s.reply(m.ID, nil)
	case "textDocument/didOpen":
		d := &lspDoc{uri: p.TextDocument.URI, path: uriToPath(p.TextDocument.URI), text: p.TextDocument.Text}
		s.docs[d.uri] = d
		s.analyze(d)
	case "textDocument/didChange":
		d := s.docs[p.TextDocument.URI]
		if d == nil || len(p.ContentChanges) == 0 {
			return
		}
		d.text = p.ContentChanges[len(p.ContentChanges)-1].Text
		s.analyze(d)
	case "textDocument/didClose":
		delete(s.docs, p.TextDocument.URI)
		s.notify("textDocument/publishDiagnostics", map[string]any{"uri": p.TextDocument.URI, "diagnostics": []any{}})
	case "textDocument/completion":
		d := s.docs[p.TextDocument.URI]
		if d == nil {
			s.reply(m.ID, []any{})
			return
		}
		s.reply(m.ID, d.complete(p.Position))
	case "textDocument/definition":
		d := s.docs[p.TextDocument.URI]
		if d == nil {
			s.reply(m.ID, nil)
			return
		}
		if loc := d.definition(p.Position); loc != nil {
			s.reply(m.ID, loc)
		} else {
			s.reply(m.ID, nil)
		}
	case "textDocument/hover":
		d := s.docs[p.TextDocument.URI]
		if d == nil {
			s.reply(m.ID, nil)
			return
		}
		if h := d.hover(p.Position); h != "" {
			s.reply(m.ID, map[string]any{"contents": map[string]any{"kind": "markdown", "value": "```veyl\n" + h + "\n```"}})
		} else {
			s.reply(m.ID, nil)
		}
	case "textDocument/documentSymbol":
		d := s.docs[p.TextDocument.URI]
		if d == nil {
			s.reply(m.ID, []any{})
			return
		}
		s.reply(m.ID, d.symbols())
	default:
		if m.ID != nil && !strings.HasPrefix(m.Method, "$/") {
			s.send(rpcMsg{ID: m.ID, Error: &rpcError{Code: -32601, Message: "not supported: " + m.Method}})
		}
	}
}

// ---- analysis ----

type lspLocal struct {
	name     string
	t        func() *front.Type // read after checking, which fills it in
	typeText string
	file     string
	line     int
	col      int
	from, to int // the lines it is in scope over
}

type lspIndex struct {
	path    string
	funcs   []*front.FnDecl
	structs map[string]*front.StructDecl
	enums   map[string]*front.EnumDecl
	ifaces  map[string]*front.InterfaceDecl
	globals []*front.LetStmt
	locals  []lspLocal
	lines   []string // the main file's text, for columns
	checked bool     // the checker ran over it, so the types are filled in
}

var errPos = regexp.MustCompile(`^(.*):(\d+):(\d+): (.*)$`)

func (s *lspServer) analyze(d *lspDoc) {
	idx, errs := analyzeSource(d.text, d.path)
	d.broken = idx == nil || !idx.checked
	if idx != nil && (idx.checked || d.index == nil) {
		d.index = idx
	}
	lines := strings.Split(d.text, "\n")
	diags := []map[string]any{}
	seen := map[string]bool{}
	for _, e := range errs {
		if seen[e] {
			continue
		}
		seen[e] = true
		line, col, msg := 1, 1, e
		if m := errPos.FindStringSubmatch(e); m != nil {
			line, _ = strconv.Atoi(m[2])
			col, _ = strconv.Atoi(m[3])
			msg = m[4]
			if !samePath(m[1], d.path) {
				// An error in an imported file shows on the first line,
				// naming where it is.
				msg = fmt.Sprintf("%s:%s:%s: %s", filepath.Base(m[1]), m[2], m[3], m[4])
				line, col = 1, 1
			}
		}
		start := lspPos{Line: line - 1, Character: utf16Col(lines, line, col)}
		end := lspPos{Line: line - 1, Character: start.Character + wordLenAt(lines, line, col)}
		diags = append(diags, map[string]any{
			"range": lspRange{Start: start, End: end}, "severity": 1, "source": "veyl", "message": msg,
		})
	}
	s.notify("textDocument/publishDiagnostics", map[string]any{"uri": d.uri, "diagnostics": diags})
}

// analyzeSource runs the front half of the compiler over text, as if it
// were the file at path. The index is nil when the text did not parse.
func analyzeSource(text, path string) (idx *lspIndex, errs []string) {
	defer func() {
		if r := recover(); r != nil {
			errs = append(errs, fmt.Sprintf("%s:1:1: the checker failed on this file: %v", path, r))
		}
	}()
	lex := NewLexer(path, text)
	toks := lex.Scan()
	p := NewParser(path, toks)
	prog := p.ParseProgram()
	errs = append(errs, lex.Errors...)
	errs = append(errs, p.Errors...)
	parsed := len(lex.Errors) == 0 && len(p.Errors) == 0

	stampImportedFile(prog, path)
	importErrs, imported := loadImports(prog, path)
	errs = append(errs, importErrs...)

	idx = buildIndex(prog, path, text)
	if len(errs) > 0 {
		if !parsed {
			// Still worth something for the declarations it did get,
			// but a parse that failed is not trusted over the last one.
			if len(idx.funcs) == 0 && len(idx.structs) == 0 {
				idx = nil
			}
		}
		if !parsed {
			return idx, errs
		}
	}

	all := append([]string{text}, imported...)
	if e := addPrelude(prog, all); len(e) > 0 {
		return idx, append(errs, e...)
	}
	if e := addLibrary(prog, all); len(e) > 0 {
		return idx, append(errs, e...)
	}
	ck := NewChecker(path, asmLibrary{})
	ck.Check(prog)
	idx.checked = true
	return idx, append(errs, ck.Errors...)
}

func buildIndex(prog *front.Program, path, text string) *lspIndex {
	idx := &lspIndex{path: path, structs: map[string]*front.StructDecl{}, enums: map[string]*front.EnumDecl{},
		ifaces: map[string]*front.InterfaceDecl{}, lines: strings.Split(text, "\n")}
	for _, f := range prog.Funcs {
		idx.funcs = append(idx.funcs, f)
		if f.Body != nil && (f.File == "" || samePath(f.File, path)) {
			from := f.Line
			to := maxLine(f.Body)
			for _, pr := range f.Params {
				pr := pr
				idx.addLocal(pr.Name, pr.Type, func() *front.Type { return pr.T }, path, pr.Line, pr.Col, from, to)
			}
			idx.walk(f.Body, path, to)
		}
	}
	for _, sd := range prog.Structs {
		idx.structs[sd.Name] = sd
	}
	for _, e := range prog.Enums {
		idx.enums[e.Name] = e
	}
	for _, it := range prog.Ifaces {
		idx.ifaces[it.Name] = it
	}
	idx.globals = append(idx.globals, prog.Globals...)
	end := len(idx.lines) + 1
	for _, st := range prog.Main {
		idx.walk(st, path, end)
	}
	return idx
}

func (idx *lspIndex) addLocal(name, typeText string, t func() *front.Type, file string, line, col, from, to int) {
	if name == "" || name == "_" || strings.HasPrefix(name, "#") || strings.HasPrefix(name, "__") {
		return
	}
	idx.locals = append(idx.locals, lspLocal{name: name, t: t, typeText: typeText, file: file, line: line, col: col, from: from, to: to})
}

// walk collects the locals under a node: lets, loop variables, and the
// parameters of function literals, each in scope from where it is
// declared to the end of the block around it.
func (idx *lspIndex) walk(n any, file string, scopeEnd int) {
	switch x := n.(type) {
	case nil:
		return
	case *front.Block:
		if x == nil {
			return
		}
		end := maxLine(x)
		for _, st := range x.Stmts {
			idx.walk(st, file, end)
		}
		return
	case *front.LetStmt:
		lt := x
		idx.addLocal(x.Name, x.Type, func() *front.Type { return lt.T }, file, x.Line, x.Col, x.Line, scopeEnd)
	case *front.ForStmt:
		if x.Body != nil {
			end := maxLine(x.Body)
			idx.addLocal(x.Var, "", func() *front.Type { return nil }, file, x.Line, x.Col, x.Line, end)
			idx.addLocal(x.Var2, "", func() *front.Type { return nil }, file, x.Line, x.Col, x.Line, end)
		}
	case *front.FuncLit:
		if x.Decl != nil && x.Decl.Body != nil {
			end := maxLine(x.Decl.Body)
			for _, pr := range x.Decl.Params {
				pr := pr
				idx.addLocal(pr.Name, pr.Type, func() *front.Type { return pr.T }, file, pr.Line, pr.Col, x.Line, end)
			}
			idx.walk(x.Decl.Body, file, end)
			return
		}
	}
	// Everything else: look inside for more.
	eachChild(n, func(c any) { idx.walk(c, file, scopeEnd) })
}

var (
	stmtType  = reflect.TypeOf((*front.Stmt)(nil)).Elem()
	exprType  = reflect.TypeOf((*front.Expr)(nil)).Elem()
	blockType = reflect.TypeOf(&front.Block{})
)

// eachChild calls fn for every statement, expression and block directly
// inside n. Reflection rather than a case per node, so a node added to
// the language later is walked without anyone remembering to.
func eachChild(n any, fn func(any)) {
	v := reflect.ValueOf(n)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		name := v.Type().Field(i).Name
		if name == "Lowered" || !v.Type().Field(i).IsExported() {
			continue // the checker's rewrite of what is walked already
		}
		visit(f, fn)
	}
}

func visit(f reflect.Value, fn func(any)) {
	switch f.Kind() {
	case reflect.Interface:
		if !f.IsNil() && (f.Type() == stmtType || f.Type() == exprType) {
			fn(f.Interface())
		}
	case reflect.Pointer:
		if f.IsNil() {
			return
		}
		if f.Type() == blockType {
			fn(f.Interface())
			return
		}
		if f.Elem().Kind() == reflect.Struct {
			if _, ok := f.Interface().(front.Stmt); ok {
				fn(f.Interface())
				return
			}
			if _, ok := f.Interface().(front.Expr); ok {
				fn(f.Interface())
				return
			}
			if f.Type() == reflect.TypeOf(&front.FnDecl{}) || f.Type() == reflect.TypeOf(&front.MatchCase{}) {
				eachChild(f.Interface(), fn)
			}
		}
	case reflect.Slice:
		for j := 0; j < f.Len(); j++ {
			visit(f.Index(j), fn)
		}
	case reflect.Struct:
		// MatchCase and InterpPart are held by value.
		if f.CanAddr() {
			eachChild(f.Addr().Interface(), fn)
		}
	}
}

// maxLine is the last line anything under n starts on, which is as close
// to the end of a block as the tree records.
func maxLine(n any) int {
	best := 0
	var rec func(any)
	rec = func(x any) {
		if p, ok := x.(interface{ Pos() (int, int) }); ok {
			if l, _ := p.Pos(); l > best {
				best = l
			}
		}
		eachChild(x, rec)
	}
	rec(n)
	return best + 1 // the closing brace
}

// ---- positions ----

func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		p = strings.TrimPrefix(p, "/")
		return filepath.FromSlash(p)
	}
	return p
}

func pathToURI(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(aa, bb)
	}
	return aa == bb
}

// utf16Col turns a 1-based byte column into the 0-based UTF-16 one LSP
// counts in.
func utf16Col(lines []string, line, col int) int {
	if line < 1 || line > len(lines) {
		return 0
	}
	s := lines[line-1]
	if col-1 > len(s) {
		col = len(s) + 1
	}
	if col < 1 {
		col = 1
	}
	return len(utf16.Encode([]rune(s[:col-1])))
}

// byteCol is the other way: an LSP character offset on a line to a
// 0-based byte offset.
func byteCol(lines []string, line, char int) int {
	if line < 0 || line >= len(lines) {
		return 0
	}
	s := lines[line]
	n := 0
	for i, r := range s {
		if n >= char {
			return i
		}
		n += len(utf16.Encode([]rune{r}))
	}
	return len(s)
}

func isIdent(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func wordLenAt(lines []string, line, col int) int {
	if line < 1 || line > len(lines) {
		return 1
	}
	s := lines[line-1]
	i := col - 1
	j := i
	for j < len(s) && isIdent(s[j]) {
		j++
	}
	if j == i {
		return 1
	}
	return len(utf16.Encode([]rune(s[i:j])))
}

// wordAt is the identifier under a position, and the dotted chain before
// it: on "a.b.ci|" it is ("a.b", "ci").
func (d *lspDoc) wordAt(pos lspPos, wholeWord bool) (qual, word string) {
	lines := strings.Split(d.text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return "", ""
	}
	s := lines[pos.Line]
	at := byteCol(lines, pos.Line, pos.Character)
	start := at
	for start > 0 && isIdent(s[start-1]) {
		start--
	}
	end := at
	if wholeWord {
		for end < len(s) && isIdent(s[end]) {
			end++
		}
	}
	word = s[start:end]
	q := start
	for q > 0 && s[q-1] == '.' {
		k := q - 1
		for k > 0 && isIdent(s[k-1]) {
			k--
		}
		if k == q-1 {
			break
		}
		q = k
	}
	if q < start {
		qual = s[q : start-1]
	}
	return qual, word
}

func (idx *lspIndex) loc(file string, line, col int, name string) *lspLocation {
	lines := idx.lines
	if !samePath(file, idx.path) {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil
		}
		lines = strings.Split(string(b), "\n")
	}
	// A declaration's position is its keyword; point at the name itself.
	if line >= 1 && line <= len(lines) {
		s := lines[line-1]
		from := col - 1
		if from < 0 || from > len(s) {
			from = 0
		}
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
		if m := re.FindStringIndex(s[from:]); m != nil {
			col = from + m[0] + 1
		}
	}
	c := utf16Col(lines, line, col)
	n := len(utf16.Encode([]rune(name)))
	return &lspLocation{URI: pathToURI(file), Range: lspRange{
		Start: lspPos{Line: line - 1, Character: c}, End: lspPos{Line: line - 1, Character: c + n}}}
}

// withoutMember is the text with the ".partial" being typed at pos taken
// out, or "" when there is none.
func (d *lspDoc) withoutMember(pos lspPos) string {
	lines := strings.Split(d.text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return ""
	}
	s := lines[pos.Line]
	at := byteCol(lines, pos.Line, pos.Character)
	start := at
	for start > 0 && isIdent(s[start-1]) {
		start--
	}
	if start == 0 || s[start-1] != '.' {
		return ""
	}
	lines[pos.Line] = s[:start-1] + s[at:]
	return strings.Join(lines, "\n")
}

// ---- lookups ----

func (idx *lspIndex) localAt(name string, line int) *lspLocal {
	var best *lspLocal
	for i := range idx.locals {
		l := &idx.locals[i]
		if l.name == name && line >= l.from && line <= l.to && (l.line < line || l.line == line) {
			// The innermost: the one declared latest whose scope still
			// covers the line.
			if best == nil || l.from >= best.from {
				best = l
			}
		}
	}
	return best
}

func (idx *lspIndex) global(name string) *front.LetStmt {
	for _, g := range idx.globals {
		if g.Name == name {
			return g
		}
	}
	return nil
}

func (idx *lspIndex) fn(name, recv string) *front.FnDecl {
	for _, f := range idx.funcs {
		if f.Recv == recv && (f.Name == name || strings.HasPrefix(f.Name, name+"<")) && !strings.HasPrefix(f.Name, "__") {
			return f
		}
	}
	return nil
}

// typeOf is the type a name has at a line, or nil.
func (idx *lspIndex) typeOf(name string, line int) *front.Type {
	if l := idx.localAt(name, line); l != nil {
		if t := l.t(); t != nil && !t.IsUnknown() {
			return t
		}
		if l.typeText != "" {
			return front.ParseType(l.typeText)
		}
		return nil
	}
	if g := idx.global(name); g != nil {
		if g.T != nil {
			return g.T
		}
		if g.Type != "" {
			return front.ParseType(g.Type)
		}
	}
	return nil
}

// resolveQual follows a dotted chain to the struct it ends on: "p" is
// the struct p holds, "p.pos" the struct its pos field holds.
func (idx *lspIndex) resolveQual(qual string, line int) string {
	parts := strings.Split(qual, ".")
	t := idx.typeOf(parts[0], line)
	if parts[0] == "self" {
		if f := idx.fnAt(line); f != nil && f.Recv != "" {
			t = front.StructOf(f.Recv)
		}
	}
	for _, p := range parts[1:] {
		sd := idx.structOf(t)
		if sd == nil {
			return ""
		}
		t = nil
		for _, f := range sd.Fields {
			if f.Name == p {
				t = f.T
				if t == nil {
					t = front.ParseType(f.Type)
				}
			}
		}
	}
	if sd := idx.structOf(t); sd != nil {
		return sd.Name
	}
	return ""
}

func (idx *lspIndex) structOf(t *front.Type) *front.StructDecl {
	for t != nil && (t.Kind == front.KNullable || t.Kind == front.KPtr) {
		t = t.Elem
	}
	if t == nil || t.Kind != front.KStruct {
		return nil
	}
	return idx.structs[t.Name]
}

// fnAt is the function whose body a line is in.
func (idx *lspIndex) fnAt(line int) *front.FnDecl {
	var best *front.FnDecl
	for _, f := range idx.funcs {
		if f.Body == nil || !(f.File == "" || samePath(f.File, idx.path)) {
			continue
		}
		if line >= f.Line && line <= maxLine(f.Body) && (best == nil || f.Line > best.Line) {
			best = f
		}
	}
	return best
}

func fnSig(f *front.FnDecl) string {
	var ps []string
	for _, p := range f.Params {
		if p.Name == "self" && p.Type == "" {
			ps = append(ps, "self")
			continue
		}
		t := p.Type
		if p.T != nil && !p.T.IsUnknown() {
			t = p.T.String()
		}
		ps = append(ps, p.Name+": "+t)
	}
	name := f.Name
	if f.Recv != "" {
		name = f.Recv + "." + f.Name
	}
	s := "fn " + name + "(" + strings.Join(ps, ", ") + ")"
	if f.Ret != "" {
		s += " -> " + f.Ret
	}
	return s
}

func builtinSig(name string) string {
	sig, ok := asmLibrary{}.Signature(name)
	if !ok {
		return ""
	}
	if sig.Check != nil {
		return name + "(...)"
	}
	var ps []string
	for _, p := range sig.Params {
		ps = append(ps, p.String())
	}
	s := name + "(" + strings.Join(ps, ", ") + ")"
	if sig.Ret != nil && sig.Ret.Kind != front.KVoid {
		s += " -> " + sig.Ret.String()
	}
	return s
}

// ---- requests ----

func (d *lspDoc) definition(pos lspPos) *lspLocation {
	idx := d.index
	if idx == nil {
		return nil
	}
	qual, word := d.wordAt(pos, true)
	if word == "" {
		return nil
	}
	line := pos.Line + 1
	if qual != "" {
		if e, ok := idx.enums[qual]; ok {
			return idx.loc(e.File, e.Line, e.Col, word)
		}
		if sname := idx.resolveQual(qual, line); sname != "" {
			if f := idx.fn(word, sname); f != nil {
				return idx.loc(f.File, f.Line, f.Col, word)
			}
			if sd := idx.structs[sname]; sd != nil {
				for _, fl := range sd.Fields {
					if fl.Name == word {
						return idx.loc(sd.File, fl.Line, fl.Col, word)
					}
				}
			}
		}
		return nil
	}
	if l := idx.localAt(word, line); l != nil {
		return idx.loc(l.file, l.line, l.col, word)
	}
	if g := idx.global(word); g != nil {
		return idx.loc(g.File, g.Line, g.Col, word)
	}
	if f := idx.fn(word, ""); f != nil {
		return idx.loc(f.File, f.Line, f.Col, word)
	}
	if sd, ok := idx.structs[word]; ok && sd.Enum == nil {
		return idx.loc(sd.File, sd.Line, sd.Col, word)
	}
	if e, ok := idx.enums[word]; ok {
		return idx.loc(e.File, e.Line, e.Col, word)
	}
	if it, ok := idx.ifaces[word]; ok {
		return idx.loc(it.File, it.Line, it.Col, word)
	}
	return nil
}

func (d *lspDoc) hover(pos lspPos) string {
	idx := d.index
	if idx == nil {
		return ""
	}
	qual, word := d.wordAt(pos, true)
	if word == "" {
		return ""
	}
	line := pos.Line + 1
	if qual != "" {
		if sig := builtinSig(qual + "." + word); sig != "" {
			return sig
		}
		if e, ok := idx.enums[qual]; ok {
			return qual + "." + word + "  // a " + e.Name
		}
		if sname := idx.resolveQual(qual, line); sname != "" {
			if f := idx.fn(word, sname); f != nil {
				return fnSig(f)
			}
			if sd := idx.structs[sname]; sd != nil {
				for _, fl := range sd.Fields {
					if fl.Name == word {
						t := fl.Type
						if fl.T != nil {
							t = fl.T.String()
						}
						return sname + "." + word + ": " + t
					}
				}
			}
		}
		return ""
	}
	if l := idx.localAt(word, line); l != nil {
		if t := idx.typeOf(word, line); t != nil {
			return word + ": " + t.String()
		}
		return word
	}
	if g := idx.global(word); g != nil {
		kind := "var"
		if g.Const {
			kind = "const"
		}
		if t := idx.typeOf(word, line); t != nil {
			return kind + " " + word + ": " + t.String()
		}
		return kind + " " + word
	}
	if f := idx.fn(word, ""); f != nil {
		return fnSig(f)
	}
	if sd, ok := idx.structs[word]; ok && sd.Enum == nil {
		var fs []string
		for _, fl := range sd.Fields {
			fs = append(fs, "    "+fl.Name+": "+fl.Type)
		}
		return "struct " + word + " {\n" + strings.Join(fs, "\n") + "\n}"
	}
	if e, ok := idx.enums[word]; ok {
		return "enum " + word + " { " + strings.Join(e.Variants, ", ") + " }"
	}
	if sig := builtinSig(word); sig != "" {
		return sig
	}
	return ""
}

// Completion item kinds, from the protocol.
const (
	ciMethod   = 2
	ciFunction = 3
	ciField    = 5
	ciVariable = 6
	ciModule   = 9
	ciKeyword  = 14
	ciStruct   = 22
	ciEnum     = 13
	ciMember   = 20 // EnumMember
	ciConstant = 21
)

func (d *lspDoc) complete(pos lspPos) []map[string]any {
	idx := d.index
	qual, _ := d.wordAt(pos, false)
	if qual != "" && d.broken {
		// "p." on its own does not parse, so nothing was checked and p
		// has no type. Check the text as it was before the dot instead.
		if fixed := d.withoutMember(pos); fixed != "" {
			if again, _ := analyzeSource(fixed, d.path); again != nil && again.checked {
				idx = again
			}
		}
	}
	line := pos.Line + 1
	var items []map[string]any
	seen := map[string]bool{}
	add := func(label string, kind int, detail string) {
		if label == "" || seen[label] || strings.HasPrefix(label, "__") || strings.Contains(label, "<") {
			return
		}
		seen[label] = true
		it := map[string]any{"label": label, "kind": kind}
		if detail != "" {
			it["detail"] = detail
		}
		items = append(items, it)
	}
	builtins := asmLibrary{}.Names()
	sort.Strings(builtins)

	if qual != "" {
		// A library namespace: the next part of every name under it.
		prefix := qual + "."
		for _, n := range builtins {
			if strings.HasPrefix(n, prefix) {
				rest := n[len(prefix):]
				if i := strings.Index(rest, "."); i >= 0 {
					add(rest[:i], ciModule, "")
				} else {
					add(rest, ciFunction, builtinSig(n))
				}
			}
		}
		if idx != nil {
			if e, ok := idx.enums[qual]; ok {
				for _, v := range e.Variants {
					add(v, ciMember, e.Name)
				}
			}
			if sname := idx.resolveQual(qual, line); sname != "" {
				if sd := idx.structs[sname]; sd != nil {
					for _, fl := range sd.Fields {
						if !strings.Contains(fl.Name, "#") && !strings.Contains(fl.Name, ".") {
							add(fl.Name, ciField, fl.Type)
						}
					}
				}
				for _, f := range idx.funcs {
					if f.Recv == sname {
						add(f.Name, ciMethod, fnSig(f))
					}
				}
			}
		}
		return items
	}

	if idx != nil {
		for i := range idx.locals {
			l := &idx.locals[i]
			if line >= l.from && line <= l.to && l.line <= line {
				detail := l.typeText
				if t := l.t(); t != nil && !t.IsUnknown() {
					detail = t.String()
				}
				add(l.name, ciVariable, detail)
			}
		}
		for _, g := range idx.globals {
			add(g.Name, ciConstant, g.Type)
		}
		for _, f := range idx.funcs {
			if f.Recv == "" && f.File != "" {
				name := f.Name
				if i := strings.Index(name, "<"); i >= 0 {
					name = name[:i]
				}
				add(name, ciFunction, fnSig(f))
			}
		}
		for n, sd := range idx.structs {
			if sd.Enum == nil && sd.File != "" {
				add(n, ciStruct, "")
			}
		}
		for n, e := range idx.enums {
			if e.File != "" {
				add(n, ciEnum, "")
			}
		}
	}
	for _, n := range builtins {
		if i := strings.Index(n, "."); i >= 0 {
			add(n[:i], ciModule, "")
		} else {
			add(n, ciFunction, builtinSig(n))
		}
	}
	for k := range front.Keywords {
		add(k, ciKeyword, "")
	}
	for _, c := range []string{"PI", "E", "INF", "NAN"} {
		add(c, ciConstant, "float")
	}
	return items
}

// Symbol kinds, from the protocol.
const (
	skStruct    = 23
	skEnum      = 10
	skFunction  = 12
	skMethod    = 6
	skVariable  = 13
	skInterface = 11
)

func (d *lspDoc) symbols() []map[string]any {
	idx := d.index
	if idx == nil {
		return []map[string]any{}
	}
	var out []map[string]any
	add := func(name string, kind int, line, col int) {
		l := idx.loc(idx.path, line, col, name)
		if l == nil {
			return
		}
		full := lspRange{Start: lspPos{Line: line - 1}, End: l.Range.End}
		out = append(out, map[string]any{"name": name, "kind": kind, "range": full, "selectionRange": l.Range})
	}
	mine := func(file string) bool { return file != "" && samePath(file, idx.path) }
	for _, f := range idx.funcs {
		if mine(f.File) && !f.Instance {
			if f.Recv != "" {
				add(f.Recv+"."+f.Name, skMethod, f.Line, f.Col)
			} else {
				add(f.Name, skFunction, f.Line, f.Col)
			}
		}
	}
	for _, sd := range idx.structs {
		if mine(sd.File) && sd.Enum == nil {
			add(sd.Name, skStruct, sd.Line, sd.Col)
		}
	}
	for _, e := range idx.enums {
		if mine(e.File) {
			add(e.Name, skEnum, e.Line, e.Col)
		}
	}
	for _, it := range idx.ifaces {
		if mine(it.File) {
			add(it.Name, skInterface, it.Line, it.Col)
		}
	}
	for _, g := range idx.globals {
		if mine(g.File) {
			add(g.Name, skVariable, g.Line, g.Col)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i]["range"].(lspRange), out[j]["range"].(lspRange)
		return a.Start.Line < b.Start.Line
	})
	if out == nil {
		out = []map[string]any{}
	}
	return out
}
