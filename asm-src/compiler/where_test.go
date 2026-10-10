package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestErrorLocations holds the second line of a runtime error, and the
// crash handler's report, to the text they must print.
func TestErrorLocations(t *testing.T) {
	veyl := asmBackend(t)
	dir := t.TempDir()
	cases := []struct {
		name, src, want string
	}{
		{"bounds",
			"fn get(xs: []int, i: int) -> int {\n    return xs[i]\n}\nprint(get([1, 2, 3], 1))\nprint(get([1, 2, 3], 5))\n",
			"2\nruntime error: index 5 is out of range for a list of length 3\n    at bounds.vl:2 in get\n"},
		{"must",
			"fn check(n: int) -> int! {\n    if n < 0 {\n        return fail(\"negative\")\n    }\n    return n\n}\nprint(must(check(4)))\nprint(must(check(-1)))\n",
			"4\nruntime error: negative\n    at must.vl:8 in the top level\n"},
		{"method",
			"struct Grid { cells: []int }\nimpl Grid {\n    fn at(self, i: int) -> int {\n        return self.cells[i]\n    }\n}\nlet g = Grid{cells: [7]}\nprint(g.at(0))\nprint(g.at(3))\n",
			"7\nruntime error: index 3 is out of range for a list of length 1\n    at method.vl:4 in Grid.at\n"},
		{"crash",
			"fn poke(p: int) -> int {\n    return mem.readI64(p)\n}\nprint(\"before\")\nprint(poke(16))\n",
			"before\ncrash: access violation - an address that is not valid was read or written\n    in poke\n"},
		{"overflow",
			"fn down(n: int) -> int {\n    return down(n + 1) + 1\n}\nprint(\"start\")\nprint(down(0))\n",
			"start\ncrash: stack overflow - probably a function that calls itself without end\n    in down\n"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			// wine ends a program whose stack overflows without running
			// its exception handlers; Windows and Linux both report it.
			if c.name == "overflow" && !targetLinux() && !targetMac() && runtime.GOOS != "windows" {
				t.Skip("a stack overflow under wine is not reported the way Windows reports it")
			}
			t.Parallel()
			p := filepath.Join(dir, c.name+".vl")
			if err := os.WriteFile(p, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(veyl, "run", p).CombinedOutput()
			if err == nil {
				t.Fatalf("expected the program to fail, it printed:\n%s", out)
			}
			got := strings.ReplaceAll(string(out), "\r\n", "\n")
			if !strings.HasPrefix(got, c.want) {
				t.Fatalf("got:\n%q\nwant it to start with:\n%q", got, c.want)
			}
		})
	}
}
