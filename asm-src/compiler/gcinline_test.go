package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// TestGCBlocks runs testdata/gcblocks.scd, which builds data blocks with
// a struct descriptor through the mem.gcblock hook and checks that
// children survive and victims named only by non-pointer words do not.
// It runs under each collection mode, because the timing of collections
// is what differs between them.
func TestGCBlocks(t *testing.T) {
	if !targetLinux() && !targetMac() && runtime.GOOS != "windows" {
		if _, err := exec.LookPath("wine"); err != nil {
			t.Skip("the programs are Windows executables, and there is no wine to run them")
		}
	}
	want, err := os.ReadFile("testdata/gcblocks.out")
	if err != nil {
		t.Fatal(err)
	}
	scandiacus := asmBackend(t)
	modes := map[string][]string{
		"default": nil,
		"eager":   {"SCANDIACUS_GC=eager"},
		"stress":  {"SCANDIACUS_GC_STRESS=1"},
		"noopt":   {"SCANDIACUS_NOOPT=1"},
	}
	for name, env := range modes {
		env := env
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, scandiacus, "run", "testdata/gcblocks.scd")
			cmd.WaitDelay = 10 * time.Second
			cmd.Env = append(append(os.Environ(), "SCANDIACUS_TESTHOOKS=1"), env...)
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Run(); err != nil {
				t.Fatalf("%v\n%s", err, out.Bytes())
			}
			if !bytes.Equal(out.Bytes(), want) {
				t.Fatalf("output differs\n--- got ---\n%s--- want ---\n%s", out.Bytes(), want)
			}
		})
	}
}

// Without the hook switch the intrinsic is an error, so a program cannot
// reach the unfinished block layout by accident.
func TestGCBlockNeedsHook(t *testing.T) {
	scandiacus := asmBackend(t)
	cmd := exec.Command(scandiacus, "run", "testdata/gcblocks.scd")
	cmd.Env = os.Environ()
	for i, e := range cmd.Env {
		if len(e) > 21 && e[:21] == "SCANDIACUS_TESTHOOKS=" {
			cmd.Env = append(cmd.Env[:i], cmd.Env[i+1:]...)
			break
		}
	}
	out, err := cmd.CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("SCANDIACUS_TESTHOOKS")) {
		t.Fatalf("expected a refusal naming SCANDIACUS_TESTHOOKS, got %v\n%s", err, out)
	}
}
