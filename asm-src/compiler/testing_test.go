package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestVeylTest runs veyl test on a file with passing, failing, crashing
// and bench blocks, and checks the report line by line.
func TestVeylTest(t *testing.T) {
	veyl := asmBackend(t)
	dir := t.TempDir()
	src := `struct P {
    x: int
}

fn add(a: int, b: int) -> int {
    return a + b
}

print("the body does not run")

test "passes" {
    expect(add(1, 2) == 3)
    expectEq(add(2, 2), 4)
    expectEq(P{x: 1}, P{x: 1})
}

test "fails" {
    expectEq(add(2, 2), 5)
    expect(false)
    expectEq([1, 2], [1, 3])
    let f = 1.5
    expectEq(f, 2)
}

test "crashes" {
    let xs: []int = []
    print(xs[0])
}

bench "adding" {
    let s = add(1, 2)
}
`
	p := filepath.Join(dir, "calc.vl")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	clean := func(out []byte) string {
		s := strings.ReplaceAll(string(out), "\r\n", "\n")
		return regexp.MustCompile(`\(\d+\.\d+s\)`).ReplaceAllString(s, "(T)")
	}

	out, err := exec.Command(veyl, "test", p).CombinedOutput()
	if err == nil {
		t.Fatalf("a failing test should fail the run:\n%s", out)
	}
	want := `PASS  calc.vl: passes (T)
FAIL  calc.vl: fails (T)
    calc.vl:18: got 4, want 5
    calc.vl:19: expect failed
    calc.vl:20: got [1, 2], want [1, 3]
    calc.vl:22: got 1.5, want 2
FAIL  calc.vl: crashes (T)
    runtime error: index 0 is out of range for a list of length 0
        at calc.vl:27 in test "crashes"
1 passed, 2 failed
`
	if got := clean(out); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", clean(out), want)
	}

	out, err = exec.Command(veyl, "test", "--run", "pass", p).CombinedOutput()
	if err != nil || clean(out) != "PASS  calc.vl: passes (T)\n1 passed, 0 failed\n" {
		t.Errorf("--run pass: %v\n%s", err, out)
	}

	out, err = exec.Command(veyl, "test", "--bench", p).CombinedOutput()
	if err != nil || !regexp.MustCompile(`^BENCH calc.vl: adding  \d+ runs, [\d.e+]+ (ns|us|ms|s) each\n1 benchmarks, 0 failed\n$`).MatchString(clean(out)) {
		t.Errorf("--bench: %v\n%s", err, out)
	}
}
