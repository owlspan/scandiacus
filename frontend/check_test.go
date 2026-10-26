package frontend

import (
	"strings"
	"testing"
)

// checked compiles a snippet through lexer, parser and checker,
// returning every diagnostic. EmptyLibrary keeps the sources free of
// builtins, so what a test asserts is never about one backend's set.
func checked(t *testing.T, src string) []string {
	t.Helper()
	lx := NewLexer("t.scd", src)
	ps := NewParser("t.scd", lx.Scan())
	prog := ps.ParseProgram()
	ck := NewChecker("t.scd", EmptyLibrary{})
	ck.Check(prog)
	return append(append(lx.Errors, ps.Errors...), ck.Errors...)
}

func TestMainIsImplicit(t *testing.T) {
	errs := checked(t, `
fn main() {
    let a = 1
}
`)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "already called main") {
		t.Errorf("declaring fn main must be refused with a pointed message, got %v", errs)
	}

	// Script style is how programs are written; it stays clean.
	if errs := checked(t, "let n = 1 + 2"); len(errs) != 0 {
		t.Errorf("top-level statements rejected: %v", errs)
	}

	// A method named main belongs to its struct, not to the program.
	errs = checked(t, `
struct Box { n: int }
impl Box {
    fn main(self) -> int { return self.n }
}
let b = Box { n: 3 }
let r = b.main()
`)
	for _, e := range errs {
		if strings.Contains(e, "already called main") {
			t.Errorf("a method named main was mistaken for the entry point: %s", e)
		}
	}
}

func TestConstAssignment(t *testing.T) {
	errs := strings.Join(checked(t, `
const LIMIT = 3
LIMIT = 4
fn f() {
    const inner = 1
    inner += 1
}
let fine = 1
fine = 2
`), "\n")
	for _, name := range []string{"LIMIT", "inner"} {
		if !strings.Contains(errs, `cannot assign to "`+name+`" because it was declared const`) {
			t.Errorf("assigning to const %s was not refused: %q", name, errs)
		}
	}
	if strings.Contains(errs, `"fine"`) {
		t.Errorf("a let was treated as const: %q", errs)
	}
}

func TestVarGlobal(t *testing.T) {
	if errs := checked(t, `
var score = 0
fn add(n: int) {
    score += n
}
add(2)
var var = 1
`); len(errs) > 0 {
		t.Errorf("a var global was refused: %v", errs)
	}
}

func TestPubAcrossFiles(t *testing.T) {
	lx := NewLexer("main.scd", "print(secret())\nprint(hidden)\nlet h = Box{n: 1}\nprint(open())\n")
	prog := NewParser("main.scd", lx.Scan()).ParseProgram()
	lib := NewParser("lib.scd", NewLexer("lib.scd",
		"fn secret() -> int { return 1 }\nconst hidden = 2\nstruct Box { n: int }\npub fn open() -> int { return 3 }\n").Scan()).ParseProgram()
	for _, f := range lib.Funcs {
		f.File = "lib.scd"
	}
	for _, g := range lib.Globals {
		g.File = "lib.scd"
	}
	for _, s := range lib.Structs {
		s.File = "lib.scd"
	}
	prog.Funcs = append(prog.Funcs, lib.Funcs...)
	prog.Globals = append(prog.Globals, lib.Globals...)
	prog.Structs = append(prog.Structs, lib.Structs...)
	ck := NewChecker("main.scd", EmptyLibrary{})
	ck.Check(prog)
	errs := strings.Join(ck.Errors, "\n")
	for _, want := range []string{`"secret" is private to lib.scd`, `"hidden" is private to lib.scd`,
		`struct "Box" is private to lib.scd`} {
		if !strings.Contains(errs, want) {
			t.Errorf("want %q, got %q", want, errs)
		}
	}
	if strings.Contains(errs, `"open"`) {
		t.Errorf("a pub function was refused: %q", errs)
	}
}

func TestEnums(t *testing.T) {
	if errs := checked(t, `
enum State { Idle, Running, Done }
fn next(s: State) -> State {
    match s {
        State.Idle => return State.Running
        State.Running => return State.Done
        State.Done => return State.Idle
    }
    return s
}
let s: State = next(State.Idle)
let same = s == State.Running
let enum = 1
`); len(errs) > 0 {
		t.Fatalf("a valid enum program was refused: %v", errs)
	}

	bad := []struct{ src, want string }{
		{"enum S { A, B }\nlet s = S.A\nmatch s {\n    S.A => print(1)\n}", "does not handle S.B"},
		{"enum S { A }\nlet n = S.A + 1", "needs numbers"},
		{"enum S { A }\nlet b = S.A == 0", "cannot compare"},
		{"enum S { A }\nenum T { A }\nlet b = S.A == T.A", "cannot compare"},
		{"enum S { A }\nlet x = S.B", `has no variant "B"`},
		{"enum S { A, A }", "declared twice"},
		{"enum S { }", "at least one variant"},
	}
	for _, b := range bad {
		errs := strings.Join(checked(t, b.src), "\n")
		if !strings.Contains(errs, b.want) {
			t.Errorf("%q: want %q, got %q", b.src, b.want, errs)
		}
	}
}

func TestArrays(t *testing.T) {
	ok := `
struct P { pos: [3]f32 }
fn first(a: [4]i32) -> i32 { return a[0] }
let a: [4]i32 = [1, 2]
let z: [8]u8
z[7] = 255
let p = P{pos: [1.5, 2.5, 3.5]}
p.pos[1] = 2.0
let _f = first(a)
for x in z { let _y = x }
`
	if errs := checked(t, ok); len(errs) != 0 {
		t.Errorf("valid arrays rejected: %v", errs)
	}
	for src, want := range map[string]string{
		"let a: [2]int = [1, 2, 3]":    "holds 2 elements",
		"let xs: [][2]int = []":        "cannot be inside a list",
		"let a: [2]str":                "is not an array type",
		"let a: [2]int\nlet b = [a]":   "cannot be inside a list",
		"let a: [2]int\nlet b = a + 1": "needs numbers",
	} {
		errs := strings.Join(checked(t, src), "\n")
		if !strings.Contains(errs, want) {
			t.Errorf("%q: want an error containing %q, got %q", src, want, errs)
		}
	}
}
