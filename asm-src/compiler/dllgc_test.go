package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDLLCollects calls a DLL that allocates heavily from four host
// threads at once. The DLL must collect - its heap stays bounded and
// the count of collections goes up - and every answer must be right,
// including a str a thread holds on to while the others churn: what an
// export returns stays alive until that thread calls in again.
func TestDLLCollects(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a DLL runs on Windows")
	}
	veyl := asmBackend(t)
	dir := t.TempDir()
	lib := `export fn churn(n: int) -> int {
    let total = 0
    for i in 0..n {
        let xs: []str = []
        for k in 0..50 {
            push(xs, "item {k} of {i}")
        }
        total += len(join(xs, ","))
    }
    return total
}

export fn label(i: int) -> str {
    let parts: []str = []
    for k in 0..20 {
        push(parts, str(i * k))
    }
    return join(parts, "-")
}

export fn collections() -> int {
    return mem.collections()
}

export fn used() -> int {
    return mem.used()
}
`
	host := `extern fn churn(n: ptr) -> ptr from "gcmod"
extern fn label(i: ptr) -> ptr from "gcmod"
extern fn collections() -> ptr from "gcmod"
extern fn used() -> ptr from "gcmod"

fn expected(i: int) -> str {
    let parts: []str = []
    for k in 0..20 {
        push(parts, str(i * k))
    }
    return join(parts, "-")
}

let bad = atomic.new(0)
let ts: []int = []
for w in 0..4 {
    push(ts, thread.spawn(fn() {
        for r in 0..40 {
            let p = label(r)
            // Other threads collect in the meantime; what this thread
            // was handed must survive until it calls in again.
            sleep(2)
            if mem.str(p) != expected(r) {
                atomic.add(bad, 1)
            }
            if churn(100) != churn(100) {
                atomic.add(bad, 1)
            }
            let q = label(r + 1)
            if mem.str(q) != expected(r + 1) {
                atomic.add(bad, 1)
            }
        }
    }))
}
for t in ts {
    thread.join(t)
}
print("wrong answers: {atomic.get(bad)}")
print("collected: {collections() > 0}")
print("heap under 64 MB: {used() < 67108864}")
`
	os.WriteFile(filepath.Join(dir, "gcmod.vl"), []byte(lib), 0o644)
	os.WriteFile(filepath.Join(dir, "host.vl"), []byte(host), 0o644)
	for _, args := range [][]string{
		{"build", "--dll", filepath.Join(dir, "gcmod.vl")},
		{"build", filepath.Join(dir, "host.vl")},
	} {
		if out, err := exec.Command(veyl, args...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	cmd := exec.Command(filepath.Join(dir, "host.exe"))
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	got := strings.ReplaceAll(string(out), "\r\n", "\n")
	want := "wrong answers: 0\ncollected: true\nheap under 64 MB: true\n"
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}
