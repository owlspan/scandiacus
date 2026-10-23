package frontend

import (
	"strings"
	"testing"
)

// scan lexes a snippet and returns its tokens without the trailing EOF,
// which every case would otherwise have to mention.
func scan(t *testing.T, src string) ([]Token, []string) {
	t.Helper()
	lx := NewLexer("t.scd", src)
	toks := lx.Scan()
	if n := len(toks); n > 0 && toks[n-1].Kind == EOF {
		toks = toks[:n-1]
	}
	return toks, lx.Errors
}

// kinds renders a token stream compactly, so a mismatch reads as a
// difference between two short strings rather than two structs.
func kinds(toks []Token) string {
	var parts []string
	for _, tk := range toks {
		parts = append(parts, tk.Kind.String())
	}
	return strings.Join(parts, " ")
}

func TestLexNumbers(t *testing.T) {
	cases := []struct {
		src   string
		kinds string
		lex   string // the first token's text
	}{
		{"42", "NUMBER", "42"},
		{"3.14", "NUMBER", "3.14"},
		{"1_000_000", "NUMBER", "1_000_000"},
		{"1_234.5", "NUMBER", "1_234.5"},
		{"0xFF", "NUMBER", "0xFF"},
		{"0xff", "NUMBER", "0xff"},
		{"0b1010", "NUMBER", "0b1010"},
		{"0b1010_1010", "NUMBER", "0b1010_1010"},

		// The rule that keeps ranges working: a fractional part is only
		// consumed when a digit follows the dot.
		{"1..10", "NUMBER DOTDOT NUMBER", "1"},
		{"1..=10", "NUMBER DOTDOTEQ NUMBER", "1"},
		{"1.5", "NUMBER", "1.5"},
	}

	for _, c := range cases {
		toks, errs := scan(t, c.src)
		if len(errs) > 0 {
			t.Errorf("%q: unexpected errors %v", c.src, errs)
			continue
		}
		if got := kinds(toks); got != c.kinds {
			t.Errorf("%q lexed as %s, want %s", c.src, got, c.kinds)
			continue
		}
		if toks[0].Lex != c.lex {
			t.Errorf("%q first token is %q, want %q", c.src, toks[0].Lex, c.lex)
		}
	}
}

func TestLexBadNumbers(t *testing.T) {
	// A malformed literal must say so rather than lex as two tokens.
	// `0b1210` once became `0b1` followed by `210`.
	bad := []string{"0x", "0b", "0b1210", "0xGG", "12abc"}
	for _, src := range bad {
		_, errs := scan(t, src)
		if len(errs) == 0 {
			t.Errorf("%q lexed without complaint, want an error", src)
		}
	}
}

func TestLexStrings(t *testing.T) {
	cases := []struct {
		src  string
		want string // the decoded value
	}{
		{`"hello"`, "hello"},
		{`""`, ""},
		{`"a\nb"`, "a\nb"},
		{`"a\tb"`, "a\tb"},
		{`"say \"hi\""`, `say "hi"`},
		{`"back\\slash"`, `back\slash`},
		{`"{{literal}}"`, "{{literal}}"},
	}
	for _, c := range cases {
		toks, errs := scan(t, c.src)
		if len(errs) > 0 {
			t.Errorf("%s: unexpected errors %v", c.src, errs)
			continue
		}
		if toks[0].Kind != STRING {
			t.Errorf("%s lexed as %s, want STRING", c.src, toks[0].Kind)
			continue
		}
		if toks[0].Lex != c.want {
			t.Errorf("%s decoded to %q, want %q", c.src, toks[0].Lex, c.want)
		}
	}
}

func TestLexStringKeepsInterpolationEscapes(t *testing.T) {
	// Escapes inside {} belong to the nested lexer that parses the
	// expression. Decoding them here as well meant a regex needed four
	// backslashes to survive both passes.
	toks, errs := scan(t, `"{re.find("\\d", s)}"`)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors %v", errs)
	}
	if !strings.Contains(toks[0].Lex, `\\d`) {
		t.Errorf("escapes inside {} were decoded early: got %q", toks[0].Lex)
	}
}

func TestLexNestedStringInInterpolation(t *testing.T) {
	// A quote inside {} must not end the outer literal.
	toks, _ := scan(t, `"shouting {upper("hi")}"`)
	if len(toks) != 1 || toks[0].Kind != STRING {
		t.Fatalf("lexed as %s, want one STRING", kinds(toks))
	}
	if !strings.Contains(toks[0].Lex, `upper("hi")`) {
		t.Errorf("nested string was lost: %q", toks[0].Lex)
	}
}

func TestLexRawStrings(t *testing.T) {
	toks, errs := scan(t, "`a\\nb`")
	if len(errs) > 0 {
		t.Fatalf("unexpected errors %v", errs)
	}
	if toks[0].Kind != RAWSTRING {
		t.Fatalf("lexed as %s, want RAWSTRING", toks[0].Kind)
	}
	if toks[0].Lex != `a\nb` {
		t.Errorf("raw string decoded escapes: got %q", toks[0].Lex)
	}

	// They span lines, and CRLF is normalised so a file saved on
	// Windows behaves like one saved anywhere else.
	multi, _ := scan(t, "`one\r\ntwo`")
	if multi[0].Lex != "one\ntwo" {
		t.Errorf("raw string across lines = %q, want %q", multi[0].Lex, "one\ntwo")
	}

	if _, errs := scan(t, "`unterminated"); len(errs) == 0 {
		t.Error("an unterminated raw string should be an error")
	}
}

func TestLexOperatorsLongestFirst(t *testing.T) {
	// Multi-character operators must win over their prefixes, or `<<=`
	// lexes as `<<` then `=`.
	cases := map[string]string{
		"<<=": "SHLEQ",
		">>=": "SHREQ",
		"<<":  "SHL",
		">>":  "SHR",
		"<=":  "LTE",
		">=":  "GTE",
		"==":  "EQ",
		"!=":  "NEQ",
		"&&":  "AND",
		"||":  "OR",
		"&=":  "AMPEQ",
		"|=":  "PIPEEQ",
		"^=":  "CARETEQ",
		"%=":  "PERCENTEQ",
		"->":  "ARROW",
		"=>":  "FATARROW",
		"..=": "DOTDOTEQ",
		"..":  "DOTDOT",
		"&":   "AMP",
		"|":   "PIPE",
		"^":   "CARET",
		"~":   "TILDE",
		"?":   "QUESTION",
	}
	for src, want := range cases {
		toks, errs := scan(t, src)
		if len(errs) > 0 {
			t.Errorf("%q: unexpected errors %v", src, errs)
			continue
		}
		if got := kinds(toks); got != want {
			t.Errorf("%q lexed as %s, want %s", src, got, want)
		}
	}
}

func TestLexComments(t *testing.T) {
	// Comments are dropped by default and kept only for the formatter.
	toks, _ := scan(t, "let x = 1 // trailing\nlet y = 2")
	if strings.Contains(kinds(toks), "COMMENT") {
		t.Error("comments should be dropped unless asked for")
	}

	// Block comments nest, so a region containing one can be commented
	// out without the inner */ ending it early.
	toks, errs := scan(t, "/* outer /* inner */ still outer */ let x = 1")
	if len(errs) > 0 {
		t.Fatalf("nested block comment errored: %v", errs)
	}
	if got := kinds(toks); got != "LET IDENT ASSIGN NUMBER" {
		t.Errorf("after a nested comment, got %s", got)
	}

	if _, errs := scan(t, "/* never closed"); len(errs) == 0 {
		t.Error("an unterminated block comment should be an error")
	}
}

func TestLexKeepComments(t *testing.T) {
	lx := NewLexer("t.scd", "let x = 1 // why\n")
	lx.KeepComments = true
	toks := lx.Scan()

	var found string
	for _, tk := range toks {
		if tk.Kind == COMMENT {
			found = tk.Lex
		}
	}
	if found != "// why" {
		t.Errorf("kept comment = %q, want %q", found, "// why")
	}
}

func TestLexPositions(t *testing.T) {
	// Every error message and every //line directive depends on these.
	toks, _ := scan(t, "let x = 1\nlet y = 2")
	want := []struct {
		lex       string
		line, col int
	}{
		{"let", 1, 1},
		{"x", 1, 5},
		{"=", 1, 7},
		{"1", 1, 9},
	}
	for i, w := range want {
		if toks[i].Lex != w.lex || toks[i].Line != w.line || toks[i].Col != w.col {
			t.Errorf("token %d = %q at %d:%d, want %q at %d:%d",
				i, toks[i].Lex, toks[i].Line, toks[i].Col, w.lex, w.line, w.col)
		}
	}
	// The second line starts over at column 1.
	if toks[5].Line != 2 || toks[5].Col != 1 {
		t.Errorf("second line starts at %d:%d, want 2:1", toks[5].Line, toks[5].Col)
	}
}

// Notepad writes a UTF-8 byte order mark by default, and so do several
// other Windows editors. It is metadata rather than content, and the
// lexer used to report three unexpected characters that are invisible
// in the editor that wrote them - unfixable by anyone reading their own
// file.
func TestLexSkipsByteOrderMark(t *testing.T) {
	const bom = "\ufeff"

	toks, errs := scan(t, bom+`let x = 1`)
	if len(errs) > 0 {
		t.Fatalf("a leading BOM should be ignored, got %v", errs)
	}
	if got := kinds(toks); got != "LET IDENT ASSIGN NUMBER" {
		t.Errorf("lexed as %s, want LET IDENT ASSIGN NUMBER", got)
	}
	// Positions are measured from the real first character, not the mark.
	if toks[0].Line != 1 || toks[0].Col != 1 {
		t.Errorf("first token at %d:%d, want 1:1", toks[0].Line, toks[0].Col)
	}

	// Only at the very start. One in the middle of a file is a genuine
	// stray character and should still be reported.
	if _, errs := scan(t, "let x = 1\n"+bom+"let y = 2"); len(errs) == 0 {
		t.Error("a BOM in the middle of a file should still be an error")
	}
}

func TestLexEscapes(t *testing.T) {
	cases := []struct{ src, want string }{
		{`"\x41\x62"`, "Ab"},
		{`"\xff"`, "\xff"},
		{`"\u{41}"`, "A"},
		{`"\u{e9}"`, "\xc3\xa9"},
		{`"\u{1F600}"`, "\xf0\x9f\x98\x80"},
		{`"\u{10FFFF}"`, "\xf4\x8f\xbf\xbf"},
		// A brace from an escape is literal text, so it is doubled for
		// the parser, which reads "{{" back as one brace.
		{`"\u{7B}\x7d"`, "{{}}"},
	}
	for _, c := range cases {
		toks, errs := scan(t, c.src)
		if len(errs) > 0 || len(toks) != 1 || toks[0].Lex != c.want {
			t.Errorf("%s: got %q %v, want %q", c.src, toks, errs, c.want)
		}
	}

	for _, bad := range []string{`"\x4"`, `"\xZZ"`, `"\u41"`, `"\u{}"`, `"\u{D800}"`,
		`"\u{110000}"`, `"\u{0000041}"`, `"\u{41"`} {
		if _, errs := scan(t, bad); len(errs) == 0 {
			t.Errorf("%s: accepted, want an error", bad)
		}
	}
}
