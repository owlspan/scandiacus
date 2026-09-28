package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// asmBackend builds the compiler into a temp dir, once per test that
// runs programs. Most of the other test files use it too.
func asmBackend(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "veyl.exe")
	build := exec.Command("go", "build", "-o", out, ".")
	if outp, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building veyl failed: %v\n%s", err, outp)
	}
	return out
}

// TestUnsupportedIsAnError checks that the subset boundary is a clean
// compile error rather than wrong output. A backend that silently
// mis-compiles what it does not understand is worse than one that
// refuses, and this is the whole reason the subset is safe to ship.
func TestUnsupportedIsAnError(t *testing.T) {
	veyl := asmBackend(t)
	dir := t.TempDir()

	// Keep this list honest as the subset grows. `while` and comparisons
	// were here until branches landed, and this test is what said so:
	// they started compiling, the assertion that they could not failed,
	// and the boundary moved on purpose rather than by drift.
	cases := map[string]string{
		"undefined":       "print(nope)\n",
		"bare empty":      "let xs = []\nprint(len(xs))\n",
		"mixed list":      "let xs = [1, \"two\"]\nprint(xs[0])\n",
		"bare empty map":  "let m = {}\nprint(len(m))\n",
		"mixed map":       "let m = {\"a\": 1, \"b\": \"two\"}\nprint(len(m))\n",
		"float key":       "let m = {1.5: 2}\nprint(len(m))\n",
		"wrong key type":  "let m: {str: int} = {\"a\": 1}\nprint(m[1])\n",
		"struct T! field": "struct P {\n x: int!\n}\nlet p = P{}\nprint(1)\n",
		"list of results": "fn f() -> int! {\n return 1\n}\nlet xs = [f()]\nprint(len(xs))\n",
		"arity":           "fn f(a: int) -> int { return a }\nprint(f(1, 2))\n",
		"bad annot":       "let x: int = \"hi\"\nprint(x)\n",
		"str minus":       "print(\"a\" - \"b\")\n",
		"out of scope":    "if true {\n let inner = 1\n}\nprint(inner)\n",
	}

	for name, src := range cases {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// One file per case: these subtests run in parallel, and a
			// shared path would have them overwriting each other's source
			// and passing for the wrong reason.
			p := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".vl")
			if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(veyl, "run", p).CombinedOutput()
			if err == nil {
				t.Fatalf("expected a compile error, but it compiled and printed:\n%s", out)
			}
		})
	}
}
