package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestPreludeHasNoWarnings brings in every prelude function - the
// prelude takes what the source names, and a comment naming them all is
// enough - and fails on any warning about the prelude's own code. Such a
// warning is printed to the user of every program that uses that part
// of the library, and there is nothing they can do about it: a "sent is
// declared but never used" from the http server reached them once.
func TestPreludeHasNoWarnings(t *testing.T) {
	scandiacus := asmBackend(t)
	var names []string
	for k := range preludeOf {
		names = append(names, k)
	}
	for k := range preludeAlso {
		names = append(names, k)
	}
	sort.Strings(names)
	src := filepath.Join(t.TempDir(), "all.scd")
	if err := os.WriteFile(src, []byte("// "+strings.Join(names, " ")+"\nprint(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(scandiacus, "check", src).CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "<prelude>") {
			t.Errorf("%s", line)
		}
	}
}
