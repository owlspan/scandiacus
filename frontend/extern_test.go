package frontend

import (
	"strings"
	"testing"
)

// parseExterns runs a snippet through the lexer and parser, returning
// the program and any errors. Extern declarations are top-level, so
// that is all a test needs to reach.
func parseExterns(t *testing.T, src string) (*Program, []string) {
	t.Helper()
	lx := NewLexer("t.scd", src)
	toks := lx.Scan()
	ps := NewParser("t.scd", toks)
	prog := ps.ParseProgram()
	return prog, append(append([]string{}, lx.Errors...), ps.Errors...)
}

func TestLexEllipsis(t *testing.T) {
	toks, errs := scan(t, `printf(fmt: str, ...)`)
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	want := "IDENT LPAREN IDENT COLON IDENT COMMA ELLIPSIS RPAREN"
	if got := kinds(toks); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestParseExternDecl(t *testing.T) {
	src := `
extern fn MessageBoxA(hwnd: int, text: str, cap: str, kind: int) -> int
pub extern fn Loud(s: str) -> bool from "loud.dll"
extern fn printf(fmt: str, ...) -> int
extern fn Beep(freq: int, ms: int)

fn from(x: int) -> int { return x }
`
	prog, errs := parseExterns(t, src)
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(prog.Funcs) != 5 {
		t.Fatalf("expected 4 externs plus fn from, got %d declarations", len(prog.Funcs))
	}

	box, loud, printf := prog.Funcs[0], prog.Funcs[1], prog.Funcs[2]
	fromFn := prog.Funcs[4]

	if !box.Extern || box.DLL != "" || box.Variadic || box.Pub {
		t.Errorf("MessageBoxA parsed wrong: %+v", box)
	}
	if box.Ret != "int" || len(box.Params) != 4 {
		t.Errorf("MessageBoxA signature wrong: ret=%q params=%d", box.Ret, len(box.Params))
	}
	if !loud.Extern || loud.DLL != "loud.dll" || !loud.Pub || loud.Ret != "bool" {
		t.Errorf("Loud parsed wrong: extern=%v dll=%q pub=%v ret=%q",
			loud.Extern, loud.DLL, loud.Pub, loud.Ret)
	}
	if !printf.Extern || !printf.Variadic {
		t.Errorf("printf should carry the ... marker")
	}
	if fromFn.Extern || fromFn.Name != "from" {
		// `from` is matched contextually; it must stay usable as an
		// ordinary function name.
		t.Errorf("`from` as a plain function name broke: %+v", fromFn)
	}
}

func TestParseExternErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{"extern 3", "'extern' declares a native function"},
		{"extern fn f(a: int, ..., b: int)", "'...' must come last"},
	}
	for _, tt := range cases {
		_, errs := parseExterns(t, tt.src+"\n")
		ok := false
		for _, e := range errs {
			if strings.Contains(e, tt.want) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%q: wanted an error containing %q, got %v", tt.src, tt.want, errs)
		}
	}
}
