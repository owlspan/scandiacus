package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestUndefinedNames checks that a name which does not exist is reported
// by the checker, in the same list as the type errors, with the closest
// real name offered when one is a likely slip.
func TestUndefinedNames(t *testing.T) {
	scandiacus := asmBackend(t)
	dir := t.TempDir()
	src := `let x: int = "a"
let total = 3
print(totl)
fn average(xs: []int) -> int {
    return 0
}
print(avrage([1]))
print(lenn([1]))
struct Point {
    x: int
}
let p = Piont{x: 1}
print(zzzzzz)
`
	p := filepath.Join(dir, "names.scd")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(scandiacus, "ir", p).CombinedOutput()
	if err == nil {
		t.Fatalf("expected errors, got:\n%s", out)
	}
	want := []string{
		`names.scd:1:1: "x" is declared as int but the value is str`,
		`names.scd:3:7: undefined variable "totl" - did you mean "total"?`,
		`names.scd:7:7: undefined function "avrage" - did you mean "average"?`,
		`names.scd:8:7: undefined function "lenn" - did you mean "len"?`,
		`names.scd:12:9: undefined struct "Piont" - did you mean "Point"?`,
		`names.scd:13:7: undefined variable "zzzzzz"`,
		`scandiacus: 6 error(s)`,
	}
	got := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n")), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(want), out)
	}
	for i := range want {
		if !strings.HasSuffix(got[i], want[i]) {
			t.Errorf("line %d:\n got %q\nwant it to end %q", i+1, got[i], want[i])
		}
	}

	// A let in the program body exists but is out of reach from a
	// function and from a const or var, and the message says which.
	reach := map[string]string{
		"fnlet":    "let limit = 3\nfn f() -> int {\n    return limit\n}\nprint(f())\n",
		"constlet": "let n = 2\nconst K = n + 1\nprint(K)\n",
	}
	wantReach := map[string]string{
		"fnlet":    `fnlet.scd:3:12: "limit" belongs to the program body, so functions cannot see it - declare it with var instead of let to share it`,
		"constlet": `constlet.scd:2:11: "n" is a let in the program body, which runs after every const and var is computed - make it a const or a var too`,
	}
	for name, src := range reach {
		p := filepath.Join(dir, name+".scd")
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		out, _ := exec.Command(scandiacus, "ir", p).CombinedOutput()
		first := strings.SplitN(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n", 2)[0]
		if !strings.HasSuffix(first, wantReach[name]) {
			t.Errorf("%s:\n got %q\nwant it to end %q", name, first, wantReach[name])
		}
	}
}
