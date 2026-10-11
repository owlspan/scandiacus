package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWarnings checks what is warned about and what is not, and that
// warnings never fail a check or a build.
func TestWarnings(t *testing.T) {
	veyl := asmBackend(t)
	dir := t.TempDir()
	src := `fn f(n: int) -> int {
    let spare = n * 2
    return n
    print("never")
}

let shown = 3
print(shown)
let assignedOnly = 1
assignedOnly = 2
let _kept = 5
let g = fn(x: int) -> int { return x + 1 }
print(g(1))
let captured = 4
let h = fn() -> int { return captured }
print(h())
let i = 0
while i < 3 {
    i += 1
    if i == 2 {
        break
        print("also never")
    }
}
print(f(1))
fn bump(p: *i32) {
    *p += 1
}
let handed: i32 = 0
bump(&handed)
`
	p := filepath.Join(dir, "w.vl")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(veyl, "check", p).CombinedOutput()
	if err != nil {
		t.Fatalf("warnings alone must not fail veyl check: %v\n%s", err, out)
	}
	got := strings.ReplaceAll(string(out), p, "w.vl")
	want := `w.vl:2:5: warning: "spare" is declared but never used
w.vl:4:5: warning: this can never run - it comes after a return
w.vl:9:1: warning: "assignedOnly" is declared but never used
w.vl:22:9: warning: this can never run - it comes after a break
`
	if strings.ReplaceAll(got, "\r\n", "\n") != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	// veyl build prints them too and still builds; veyl run does not.
	out, err = exec.Command(veyl, "build", p).CombinedOutput()
	if err != nil || !strings.Contains(string(out), `warning: "spare" is declared but never used`) {
		t.Errorf("build: %v\n%s", err, out)
	}
	out, err = exec.Command(veyl, "run", p).CombinedOutput()
	if err != nil || strings.Contains(string(out), "warning") {
		t.Errorf("run: %v\n%s", err, out)
	}

	// An error still fails.
	bad := filepath.Join(dir, "bad.vl")
	os.WriteFile(bad, []byte("let x: int = \"no\"\nprint(x)\n"), 0o644)
	if out, err := exec.Command(veyl, "check", bad).CombinedOutput(); err == nil {
		t.Errorf("an error passed veyl check:\n%s", out)
	}
}
