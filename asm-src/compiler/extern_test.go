package main

// Tests for `extern fn` - native functions declared in Veyl source and
// called through the foreign-call path. The frontend tests cover what
// the parser accepts; these cover what reaches the module: which
// symbols are imported, how wide the return register is read, and what
// the build does about a library it cannot find.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lowerForTest runs a program through the whole front end and lowering,
// returning the module and every error reported. It mirrors compile()
// in veyl.go but collects errors instead of exiting on them.
func lowerForTest(t *testing.T, src string) (*Module, []string) {
	t.Helper()
	const path = "test.vl"
	lx := NewLexer(path, src)
	toks := lx.Scan()
	ps := NewParser(path, toks)
	prog := ps.ParseProgram()
	stampImportedFile(prog, path)
	if errs := addPrelude(prog, []string{src}); len(errs) > 0 {
		t.Fatalf("prelude errors: %v", errs)
	}
	ck := NewChecker(path, asmLibrary{})
	ck.Check(prog)
	errs := append(append([]string{}, lx.Errors...), ps.Errors...)
	errs = append(errs, ck.Errors...)
	if len(errs) > 0 {
		t.Fatalf("unexpected front-end errors:\n%s", strings.Join(errs, "\n"))
	}
	mod, lowerErrs := Lower(prog, path)
	return mod, lowerErrs
}

// externCall finds the lowered call to a named foreign symbol. It
// returns the instruction and the function holding it, since callers
// often want to look at what surrounds the call.
func externCall(t *testing.T, mod *Module, sym string) (*Instr, *Func) {
	t.Helper()
	for fi := range mod.Funcs {
		f := mod.Funcs[fi]
		for i := range f.Code {
			in := &f.Code[i]
			if in.Op == OpCall && in.Extern && in.Sym == sym {
				return in, f
			}
		}
	}
	t.Fatalf("no lowered call to %s in the module", sym)
	return nil, nil
}

func TestExternSymbolIsImported(t *testing.T) {
	src := `
extern fn Beep(freq: int, ms: int) -> bool

let ok = Beep(440, 200)
print(ok)
`
	mod, errs := lowerForTest(t, src)
	if len(errs) > 0 {
		t.Fatalf("errors:\n%s", strings.Join(errs, "\n"))
	}
	if !mod.Externs["Beep"] {
		t.Errorf("Beep not recorded as a foreign symbol; have %v", mod.Externs)
	}
	// PascalCase lands in kernel32 by the naming rule, no override needed.
	if got := importDLL("Beep"); got != "kernel32.dll" {
		t.Errorf("importDLL(Beep) = %q, want kernel32.dll", got)
	}
	in, _ := externCall(t, mod, "Beep")
	if !in.Ret32 {
		t.Errorf("a -> bool return must be sign-extended from eax (Ret32)")
	}
	if in.Variadic {
		t.Errorf("Beep is not variadic")
	}
}

func TestExternFromClausePinsLibrary(t *testing.T) {
	src := `
extern fn mz_extract(zipPath: str, dest: str) -> int from "miniz"

mz_extract("pack.zip", "out")
`
	mod, errs := lowerForTest(t, src)
	if len(errs) > 0 {
		t.Fatalf("errors:\n%s", strings.Join(errs, "\n"))
	}
	if got := importDLL("mz_extract"); got != "miniz.dll" {
		t.Errorf("importDLL(mz_extract) = %q, want miniz.dll (the .dll suffix is added)", got)
	}
	externCall(t, mod, "mz_extract") // presence is the assertion
}

func TestExternRetWidths(t *testing.T) {
	tests := []struct {
		ret     string
		ret32   bool
		strCopy bool
	}{
		{"int", true, false},
		{"bool", true, false},
		{"ptr", false, false},
		{"str", false, true},
		{"float", false, false},
	}
	for _, tt := range tests {
		src := `
extern fn probe(n: int) -> ` + tt.ret + `

print(probe(3))
`
		if tt.ret == "" {
			src = `
extern fn probe(n: int)

probe(3)
`
		}
		mod, errs := lowerForTest(t, src)
		if len(errs) > 0 {
			t.Fatalf("ret %s: errors:\n%s", tt.ret, strings.Join(errs, "\n"))
		}
		in, holder := externCall(t, mod, "probe")
		if in.Ret32 != tt.ret32 {
			t.Errorf("ret %s: Ret32 = %v, want %v", tt.ret, in.Ret32, tt.ret32)
		}
		hasCopy := false
		for i := range holder.Code {
			if &holder.Code[i] == in {
				// The copy measures the returned buffer with C's strlen,
				// right after the call in the same function: a char* has
				// no header to read a length from.
				for k := i + 1; k < len(holder.Code) && k <= i+4; k++ {
					if holder.Code[k].Sym == "strlen" {
						hasCopy = true
					}
				}
				break
			}
		}
		if hasCopy != tt.strCopy {
			t.Errorf("ret %s: returned char* copied into a Veyl string = %v, want %v",
				tt.ret, hasCopy, tt.strCopy)
		}
	}
}

func TestExternVariadicSetsFlagAndAllowsExtraArgs(t *testing.T) {
	src := `
extern fn printf(fmt: str, ...) -> int

printf("hello\n")
printf("%d %d\n", 1, 2)
`
	mod, errs := lowerForTest(t, src)
	if len(errs) > 0 {
		t.Fatalf("errors:\n%s", strings.Join(errs, "\n"))
	}
	in, _ := externCall(t, mod, "printf")
	if !in.Variadic {
		t.Errorf("variadic flag did not reach the instruction")
	}
	// The first of the two calls in main carries one argument, the
	// second three; externCall finds whichever lowered first.
	if n := len(in.Args); n != 1 && n != 3 {
		t.Errorf("expected a call with 1 or 3 arguments, got %d", n)
	}
}

func TestExternArityMismatchReports(t *testing.T) {
	src := `
extern fn MessageBoxA(hwnd: int, text: str, cap: str, kind: int) -> int

MessageBoxA(0, "hi")
`
	_, errs := lowerForTest(t, src)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "takes at least 4") {
			found = true
		}
	}
	if found {
		t.Errorf("non-variadic extern should report a plain arity error, got:\n%s",
			strings.Join(errs, "\n"))
	}
	if len(errs) == 0 {
		t.Errorf("an arity mismatch on a non-variadic extern must be reported")
	}
}

func TestMissingForeignDLLIsCaughtAtBuild(t *testing.T) {
	root := t.TempDir()
	mod := &Module{Externs: map[string]bool{"mz_extract": true}}
	err := missingForeignDLLs(root, mod)
	if err == nil || !strings.Contains(err.Error(), "miniz.dll") {
		t.Fatalf("expected an error naming miniz.dll, got %v", err)
	}

	// Beside the output counts as found.
	if werr := os.WriteFile(filepath.Join(root, "miniz.dll"), []byte("MZ"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if err := missingForeignDLLs(root, mod); err != nil {
		t.Fatalf("DLL beside the source should satisfy the check, got %v", err)
	}

	// A system library is never required of the user.
	sys := &Module{Externs: map[string]bool{"MessageBoxA": true}}
	if err := missingForeignDLLs(root, sys); err != nil {
		t.Fatalf("user32 should not need anything beside the program: %v", err)
	}
}

// The checker refuses anything that cannot cross the boundary, so one
// wrong declaration is one error rather than garbage assembly.
func TestExternRejectsNonScalars(t *testing.T) {
	cases := []string{
		"extern fn bad(xs: []int)\n",
		"extern fn bad(m: {str: int})\n",
		"extern fn bad(u: User) -> int\n",
		"extern fn bad(n: int) -> []str\n",
		"extern fn bad(n: int) -> str!\n",
	}
	for _, decl := range cases {
		src := "struct User { name: str }\n" + decl + "\n"
		const path = "test.vl"
		lx := NewLexer(path, src)
		ps := NewParser(path, lx.Scan())
		prog := ps.ParseProgram()
		ck := NewChecker(path, asmLibrary{})
		ck.Check(prog)
		errs := append(append([]string{}, lx.Errors...), ps.Errors...)
		errs = append(errs, ck.Errors...)
		if len(errs) == 0 {
			t.Errorf("%q was accepted but cannot cross into native code", decl)
		}
	}
}

// An extern used as a value would need its address, which the import
// table does not expose through this path; calling it is the only thing
// that works, so that is all the language allows.
func TestExternCannotBeUsedAsValue(t *testing.T) {
	src := `
extern fn Beep(freq: int, ms: int) -> bool

let f = Beep
print(f(440, 100))
`
	const path = "test.vl"
	lx := NewLexer(path, src)
	ps := NewParser(path, lx.Scan())
	prog := ps.ParseProgram()
	ck := NewChecker(path, asmLibrary{})
	ck.Check(prog)
	errs := append(append(lx.Errors, ps.Errors...), ck.Errors...)
	if len(errs) == 0 {
		t.Fatalf("an extern bound to a variable must be refused")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot be used as a value") {
		t.Errorf("error should say why, got:\n%s", strings.Join(errs, "\n"))
	}
}

// The parser accepts the whole surface: pub, from-clause, variadic, and
// keeps `from` usable as an ordinary name everywhere else.
func TestExternParsingSurface(t *testing.T) {
	src := `
pub extern fn Loud(s: str) -> int from "loud.dll"

extern fn sum(start: int, ...) -> int

let from = 3
print(from + sum(1, 2))
`
	lx := NewLexer("test.vl", src)
	ps := NewParser("test.vl", lx.Scan())
	prog := ps.ParseProgram()
	errs := append(append([]string{}, lx.Errors...), ps.Errors...)
	if len(errs) > 0 {
		t.Fatalf("parse errors:\n%s", strings.Join(errs, "\n"))
	}
	var loud, sum *FnDecl
	for _, f := range prog.Funcs {
		switch f.Name {
		case "Loud":
			loud = f
		case "sum":
			sum = f
		}
	}
	if loud == nil || sum == nil {
		t.Fatalf("declarations missing: %+v", prog.Funcs)
	}
	if !loud.Extern || loud.DLL != "loud.dll" || !loud.Pub {
		t.Errorf("Loud parsed wrong: extern=%v dll=%q pub=%v", loud.Extern, loud.DLL, loud.Pub)
	}
	if !sum.Variadic {
		t.Errorf("sum should carry the ... marker")
	}
}
