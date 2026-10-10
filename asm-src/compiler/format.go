package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fmtCommand is veyl fmt: each file named, or every .vl under a folder
// named, rewritten in place. --check rewrites nothing and lists the
// files that would change, failing if there are any.
func fmtCommand(args []string) {
	check := false
	var paths []string
	for _, a := range args {
		if a == "--check" {
			check = true
			continue
		}
		paths = append(paths, a)
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			fail("%v", err)
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		filepath.Walk(p, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if fi.IsDir() && (fi.Name() == "veyl_modules" || strings.HasPrefix(fi.Name(), ".") && path != p) {
				return filepath.SkipDir
			}
			if !fi.IsDir() && strings.HasSuffix(fi.Name(), ".vl") {
				files = append(files, path)
			}
			return nil
		})
	}
	changed, broken := 0, 0
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			fail("%v", err)
		}
		out, ok := Format(file, string(b))
		if !ok {
			fmt.Fprintf(os.Stderr, "%s: does not lex, left as it is\n", file)
			broken++
			continue
		}
		if out == string(b) {
			continue
		}
		changed++
		if check {
			fmt.Println(file)
			continue
		}
		if err := os.WriteFile(file, []byte(out), 0o644); err != nil {
			fail("%v", err)
		}
	}
	if broken > 0 || check && changed > 0 {
		os.Exit(1)
	}
}

// The formatter.
//
// It works on the token stream, not the AST, and that is a deliberate
// choice. An AST-based printer would have to reconstruct every comment
// from scratch, because the parser throws them away - and a formatter
// that silently deletes comments is worse than no formatter at all.
// Working from tokens means anything the formatter does not understand
// passes through untouched.
//
// What it does: fixes indentation, normalises spacing around operators
// and punctuation, collapses runs of blank lines to one, and strips
// trailing whitespace. What it deliberately does not do: reflow long
// lines, or reorder anything. A formatter that rewrites your line
// breaks has to be much cleverer than this one before it earns the
// right.

// Format reformats Veyl source. It returns the source unchanged if
// the input does not lex cleanly - reformatting a file with a broken
// string literal in it is a good way to lose work.
func Format(file, src string) (string, bool) {
	lx := NewLexer(file, src)
	lx.KeepComments = true
	toks := lx.Scan()
	if len(lx.Errors) > 0 {
		return src, false
	}

	f := &formatter{toks: toks}
	out := f.run()

	// Exactly one newline at the end: no trailing blank lines, but never
	// a file without a final newline either.
	out = strings.TrimRight(out, "\n")
	if out != "" {
		out += "\n"
	}
	return out, true
}

// open is a bracket not yet closed, and the indent it added.
type open struct {
	give int
	line int
}

type formatter struct {
	toks   []Token
	out    strings.Builder
	indent int

	lineStarted bool // something has been written on the current line
	blankRun    int  // consecutive blank lines just emitted
	lineIndent  int  // indent the current line began at
	lineNo      int  // lines ended so far
	opens       []open

	// literalBrace marks each `{` that opens a map or struct literal
	// rather than a block. Literals hug their contents and do not
	// indent; blocks do the opposite.
	literalBrace map[int]bool
	matching     map[int]int // `{` index -> its `}` index
}

// findLiteralBraces decides, for every `{`, whether it opens a value or
// a block.
//
// The parser settles this with context the formatter does not have, so
// this uses a shape test instead: a brace group entirely on one line
// that contains a top-level `:` is a map or struct literal. That covers
// {"a": 1} and Point{x: 1}, and correctly leaves `if x { run() }` alone
// because it has no colon at that depth.
func (f *formatter) findLiteralBraces() {
	f.literalBrace = map[int]bool{}
	f.matching = map[int]int{}

	var stack []int
	for i, t := range f.toks {
		switch t.Kind {
		case LBRACE:
			stack = append(stack, i)
		case RBRACE:
			if len(stack) == 0 {
				continue
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			f.matching[open] = i

			oneLine, hasColon, depth := true, false, 0
			for j := open + 1; j < i; j++ {
				switch f.toks[j].Kind {
				case NEWLINE:
					oneLine = false
				case LBRACE, LBRACKET, LPAREN:
					depth++
				case RBRACE, RBRACKET, RPAREN:
					depth--
				case COLON:
					if depth == 0 {
						hasColon = true
					}
				}
			}
			// An empty pair is a literal only where a value belongs -
			// Point{} and `= {}`, but not the body of `fn f() {}`.
			empty := i == open+1
			before := f.prevMeaningful(open)
			if empty {
				// Stack<int>{} hugs its name in the source, a body never
				// does in practice: fn f() {} has its space.
				f.literalBrace[open] = before >= 0 && (startsValuePosition(f.toks[before].Kind) ||
					(f.toks[before].Kind == GT || f.toks[before].Kind == SHR) && f.adjacent(before, open))
				continue
			}
			if f.declBrace(open) {
				continue // struct Point { x: int } is a declaration, not a literal
			}
			f.literalBrace[open] = oneLine && hasColon
		}
	}
}

// declBrace reports whether the { at i opens the body of a struct, impl,
// enum or interface declaration: a name, perhaps with <T>, after one of
// those words.
func (f *formatter) declBrace(i int) bool {
	j := i - 1
	if j >= 0 && (f.toks[j].Kind == GT || f.toks[j].Kind == SHR) {
		depth := 0
		for ; j >= 0; j-- {
			switch f.toks[j].Kind {
			case GT:
				depth++
			case SHR:
				depth += 2
			case LT:
				depth--
			}
			if depth <= 0 {
				break
			}
		}
		j--
	}
	if j < 0 || f.toks[j].Kind != IDENT {
		return false
	}
	j--
	if j < 0 {
		return false
	}
	switch k := f.toks[j]; {
	case k.Kind == STRUCT || k.Kind == IMPL:
		return true
	case k.Kind == IDENT && (k.Lex == "enum" || k.Lex == "interface"):
		return true
	}
	return false
}

// namesLiteral reports whether the token before a { is a struct name the
// brace hugs: a capitalised name, or the > of Box<int>, that is not a
// function's return type.
func (f *formatter) namesLiteral(i int) bool {
	t := f.toks[i]
	switch t.Kind {
	case IDENT:
		if t.Lex == "" || t.Lex[0] < 'A' || t.Lex[0] > 'Z' {
			return false
		}
	case GT, SHR:
	default:
		return false
	}
	for j := i - 1; j >= 0; j-- {
		switch f.toks[j].Kind {
		case NEWLINE:
			return true
		case ARROW:
			return false
		case ASSIGN, LPAREN, COMMA, RETURN, COLON, LBRACKET:
			return true
		}
	}
	return true
}

// isLiteralClose reports whether the `}` at i closes a literal brace.
func (f *formatter) isLiteralClose(i int) bool {
	for open, close := range f.matching {
		if close == i {
			return f.literalBrace[open]
		}
	}
	return false
}

func (f *formatter) run() string {
	f.findLiteralBraces()
	for i := 0; i < len(f.toks); i++ {
		t := f.toks[i]

		switch t.Kind {
		case EOF, NEWLINE:
			f.endLine()
			continue
		case RBRACE, RBRACKET, RPAREN:
			// A closing bracket takes back exactly what its opener gave.
			// At the start of a line it does so before it is written, so
			// it lines up with the line that opened the group.
			give := f.pop()
			if !f.lineStarted {
				f.indent -= give
				f.write(t.Text(), i)
			} else {
				f.write(t.Text(), i)
				f.indent -= give
			}
		default:
			f.write(t.Text(), i)
		}

		switch t.Kind {
		case LBRACKET, LPAREN:
			f.push(1)
		case LBRACE:
			if f.literalBrace[i] {
				f.push(0)
			} else {
				f.push(1)
			}
		}
		if f.indent < 0 {
			f.indent = 0
		}
	}
	return f.out.String()
}

// push records an opening bracket and the indent it adds.
func (f *formatter) push(give int) {
	f.opens = append(f.opens, open{give: give, line: f.lineNo})
	f.indent += give
}

// pop is the indent the innermost open bracket added, which its closer
// takes away.
func (f *formatter) pop() int {
	if len(f.opens) == 0 {
		return 0
	}
	o := f.opens[len(f.opens)-1]
	f.opens = f.opens[:len(f.opens)-1]
	return o.give
}

// wasMidLine reports whether the token at i had something before it on
// its own line, which decides who owns the outdent.
func (f *formatter) wasMidLine(i int) bool {
	for j := i - 1; j >= 0; j-- {
		switch f.toks[j].Kind {
		case NEWLINE:
			return false
		case COMMENT:
			continue
		default:
			return true
		}
	}
	return false
}

func (f *formatter) write(text string, i int) {
	if text == "" {
		return
	}
	if !f.lineStarted {
		f.out.WriteString(strings.Repeat("    ", f.indent))
		f.lineStarted = true
		f.lineIndent = f.indent
		f.blankRun = 0
	} else if f.needsSpace(i) || f.wouldMerge(i, text) {
		f.out.WriteByte(' ')
	}
	f.out.WriteString(text)
}

// wouldMerge reports whether writing this token straight after the
// previous one would lex as something else.
//
// The case that motivated it: `Person! = x` has no space before `!` and
// none after, which produces `Person!=` - and `!=` is a single token.
// The formatter would have silently changed the program. Rather than
// enumerating the dangerous pairs, this re-lexes the join and checks
// that the first token still ends where it should.
func (f *formatter) wouldMerge(i int, text string) bool {
	prev := f.prevMeaningful(i)
	if prev < 0 {
		return false
	}
	left := f.toks[prev].Text()
	if left == "" || !mergeable(last(left)) || !mergeable(text[0]) {
		return false
	}

	probe := NewLexer("", left+text)
	toks := probe.Scan()
	if len(toks) == 0 {
		return false
	}
	return toks[0].Text() != left && toks[0].Lex != left
}

// mergeable reports whether a character can be part of a
// multi-character token, which is the only way two tokens can run
// together into a third.
func mergeable(c byte) bool {
	switch c {
	case '=', '!', '<', '>', '&', '|', '+', '-', '*', '/', '%', '^', '.', '?', ':':
		return true
	}
	return IsAlpha(c) || IsDigit(c)
}

func last(s string) byte { return s[len(s)-1] }

func (f *formatter) endLine() {
	if f.lineStarted {
		f.out.WriteByte('\n')
		f.lineStarted = false
		f.blankRun = 0
		// One line never adds more than one level of indent. Without
		// this, `f(a, fn(n: int) {` opens a paren and a brace on the
		// same line and the body lands two levels in, which is not how
		// anyone writes it.
		// Of the brackets this line opened, only the innermost indents:
		// the outer ones give nothing, so their closers take nothing
		// back, and in }) the brace lines up and the paren follows it.
		for k := 0; k < len(f.opens) && f.indent > f.lineIndent+1; k++ {
			if f.opens[k].line == f.lineNo && f.opens[k].give > 0 {
				f.opens[k].give = 0
				f.indent--
			}
		}
		f.lineNo++
		return
	}
	// A blank line. One is a paragraph break and worth keeping; three
	// in a row is just drift.
	if f.blankRun < 1 && f.out.Len() > 0 {
		f.out.WriteByte('\n')
		f.blankRun++
	}
	f.lineNo++
}

// needsSpace decides whether to put a space before token i, given what
// came before it on the same line.
//
// What comes *before* is checked first. An opening bracket never has a
// space after it, whatever follows - getting that order wrong produces
// `push( [1, 2], 3)`.
func (f *formatter) needsSpace(i int) bool {
	prev := f.prevMeaningful(i)
	if prev < 0 {
		return false
	}
	left, right := f.toks[prev].Kind, f.toks[i].Kind

	// <, >, * and & each mean two things: a comparison or the brackets
	// of max<int>, a multiplication or a pointer, a bitwise and or an
	// address. Telling them apart takes the parser, and guessing wrong
	// could change what a program means, so the spacing the author gave
	// them is kept as it was.
	unaryLeft := (left == STAR || left == AMP) && f.isUnary(prev)
	unaryRight := (right == STAR || right == AMP) && f.isUnary(i)
	switch {
	case unaryLeft:
		return false // &p, *p
	case unaryRight:
		// spaced like any operand: after = yes, after ( no
	case ambiguous(left) || ambiguous(right):
		return !f.adjacent(prev, i)
	case left == RBRACKET && (right == IDENT || right == QUESTION || right == FN || right == STAR) && f.adjacent(prev, i):
		return false // [8]u8 and []?int are types
	case left == QUESTION && prev > 0 && f.toks[prev-1].Kind == RBRACKET && f.adjacent(prev, i):
		return false // the ? of []?int
	case right == LBRACE && f.adjacent(prev, i) && f.namesLiteral(prev):
		return false // a multi-line Person{ literal still hugs its name
	case right == LBRACKET && f.toks[prev].Kind == IDENT && f.toks[prev].Lex == "new":
		return !f.adjacent(prev, i) // new [n]Vec3
	case right == LPAREN && left == RBRACE && f.adjacent(prev, i):
		return false // fn(a) { ... }(6) calls the literal
	}
	// Never a space before these, whatever came first.
	switch right {
	case COMMA, RPAREN, RBRACKET, DOT:
		if left != LBRACE {
			return false
		}
	}

	// Never a space after these.
	switch left {
	case LPAREN, LBRACKET, DOT, DOTDOT, DOTDOTEQ, TILDE:
		return false
	case LBRACE:
		return !f.literalBrace[prev]
	case BANG:
		// `str! = x` needs the space; `!ready` does not. Which one this
		// is depends on whether a value came before the `!`.
		return f.isPostfix(prev)
	case QUESTION:
		// Mirror image: `?int` is a prefix, `load(p)?` is a postfix.
		return f.isPostfix(prev)
	case RBRACKET:
		// `[]int`, `[]fn()` and `[]?int` are each one type and hug.
		// `xs[0] + 1` does not - but an empty pair of brackets can only
		// be a list type.
		if prev > 0 && f.toks[prev-1].Kind == LBRACKET {
			switch right {
			case IDENT, FN, QUESTION, LBRACKET, LBRACE:
				return false
			}
		}
	case MINUS:
		// A unary minus hugs its operand; a binary one does not.
		if f.isUnary(prev) {
			return false
		}
	}

	// Never a space before these.
	switch right {
	case COMMA, COLON, RPAREN, RBRACKET, DOT, DOTDOT, DOTDOTEQ:
		return false
	case BANG, QUESTION:
		// A suffix hugs what it applies to; a prefix does not.
		return !endsValue(left)
	case RBRACE:
		return !f.isLiteralClose(i)
	case LBRACE:
		// A struct literal hugs the name in front of it - Point{x: 1},
		// not Point {x: 1}. A bare map literal has no name to hug, so
		// `let m = {"a": 1}` keeps its space after the `=`.
		return !(f.literalBrace[i] && hugsLeft(left))
	case LPAREN, LBRACKET:
		// `f(x)` and `xs[0]` hug what they apply to. `if (a)`,
		// `x & (y)` and `= [1, 2]` do not. `fn(` is one word.
		return !hugsLeft(left) && left != FN
	}
	return true
}

func ambiguous(k Kind) bool {
	switch k {
	case LT, GT, STAR, AMP, SHR:
		return true
	}
	return false
}

// adjacent reports whether two tokens touched in the source.
func (f *formatter) adjacent(a, b int) bool {
	x, y := f.toks[a], f.toks[b]
	return x.Line == y.Line && x.Col+len(x.Text()) == y.Col
}

// isPostfix reports whether the `!` or `?` at i is a suffix on a value
// or type, rather than a prefix on what follows.
func (f *formatter) isPostfix(i int) bool {
	prev := f.prevMeaningful(i)
	return prev >= 0 && endsValue(f.toks[prev].Kind)
}

// isUnary reports whether the operator at i is prefix rather than
// infix, which is decided by whether a value could have preceded it.
func (f *formatter) isUnary(i int) bool {
	prev := f.prevMeaningful(i)
	if prev < 0 {
		return true
	}
	if t := f.toks[prev]; t.Kind == IDENT && t.Lex == "as" {
		return true // a type follows: as *i32
	}
	return !endsValue(f.toks[prev].Kind)
}

func (f *formatter) prevMeaningful(i int) int {
	for j := i - 1; j >= 0; j-- {
		switch f.toks[j].Kind {
		case NEWLINE:
			return -1
		case COMMENT:
			continue
		default:
			return j
		}
	}
	return -1
}

// endsValue reports whether a token can end an expression, which is how
// a binary minus is told from a unary one.
func endsValue(k Kind) bool {
	switch k {
	case IDENT, NUMBER, STRING, RAWSTRING, TRUE, FALSE, NIL, SELF,
		RPAREN, RBRACKET, RBRACE, QUESTION, BANG:
		return true
	}
	return false
}

// hugsLeft reports whether `[` directly follows something it indexes.
func hugsLeft(k Kind) bool {
	switch k {
	case IDENT, RPAREN, RBRACKET, STRING, RAWSTRING, SELF:
		return true
	}
	return false
}

// startsValuePosition reports whether a value could begin right after
// this token, which is how an empty struct literal is told from an
// empty block.
func startsValuePosition(k Kind) bool {
	switch k {
	case IDENT, ASSIGN, COMMA, LPAREN, COLON, RETURN, LBRACKET:
		return true
	}
	return false
}
