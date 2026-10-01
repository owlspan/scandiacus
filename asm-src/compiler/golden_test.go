package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestGolden runs the programs in ../tests and the top-level ones in
// ../examples, and compares what they print with the .out file beside
// each one. A program that reads standard input gets the .in file beside
// it when there is one.
//
// The examples used to be compared against the old Go backend on the
// veylgo branch instead. Their .out files are that backend's output,
// frozen when it was retired.
func TestGolden(t *testing.T) {
	// On Linux the programs are built for Linux and run directly; the
	// Windows target needs wine anywhere but Windows.
	if !targetLinux() && runtime.GOOS != "windows" {
		if _, err := exec.LookPath("wine"); err != nil {
			t.Skip("the programs are Windows executables, and there is no wine to run them")
		}
	}
	var programs []string
	for _, dir := range []string{"tests", "examples"} {
		found, err := filepath.Glob(filepath.Join("..", dir, "*.vl"))
		if err != nil || len(found) == 0 {
			t.Fatalf("no golden programs in %s: %v", dir, err)
		}
		programs = append(programs, found...)
	}
	veyl := asmBackend(t)

	for _, src := range programs {
		src := src
		name := filepath.Base(filepath.Dir(src)) + "/" + filepath.Base(src)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if targetLinux() && windowsOnlyProgram(filepath.Base(src)) {
				t.Skip("exercises a Windows-only facility (COM, the PE image, raw native calls, " +
					"the Windows process APIs, or COFF static libraries)")
			}
			base := strings.TrimSuffix(src, ".vl")
			want, err := os.ReadFile(base + ".out")
			if err != nil {
				t.Fatalf("no expected output: %v", err)
			}
			// A miscompiled loop can print nothing and never end, which
			// would hang the suite instead of failing one program. Two
			// minutes is generous on purpose: every program here builds
			// and runs at once, and slow machines exist.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, veyl, "run", src)
			if in, err := os.Open(base + ".in"); err == nil {
				defer in.Close()
				cmd.Stdin = in
			}
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Run(); err != nil {
				if targetLinux() && windowsOnly(out.Bytes()) {
					t.Skip("uses a library that is Windows-only so far")
				}
				if ctx.Err() == context.DeadlineExceeded {
					t.Fatalf("did not finish within 2 minutes, which usually means "+
						"a loop was miscompiled into one that never ends\n%s", out.Bytes())
				}
				t.Fatalf("%v\n%s", err, out.Bytes())
			}
			if !bytes.Equal(out.Bytes(), want) {
				t.Fatalf("output differs\n--- got ---\n%q\n--- want ---\n%q", out.Bytes(), want)
			}
		})
	}
}

// windowsOnlyProgram names the golden programs that test a Windows
// facility with no meaning on Linux and so cannot match the same output:
// COM objects, the PE layout of the running image, raw native calls made
// with the Windows calling convention (mem.call / mem.symbol), the
// Windows process APIs, and COFF static libraries. They run on the
// Windows target; on Linux they are skipped rather than ported into
// something that only pretends to be Windows.
func windowsOnlyProgram(name string) bool {
	switch name {
	case "com.vl", "native.vl", "nativecall.vl", "proc.vl", "proc_list.vl",
		"staticlib.vl", "staticdll.vl":
		return true
	}
	return false
}

// windowsOnly reports whether a Linux build refused a program for using
// something that exists only on Windows so far - a skip, not a failure.
func windowsOnly(out []byte) bool {
	for _, s := range []string{"only exist on Windows so far", "Windows-only for now"} {
		if bytes.Contains(out, []byte(s)) {
			return true
		}
	}
	return false
}
