package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestDLL builds tests/dll/mathmod.scd as a DLL, then runs host.scd beside
// it: the library's top level has to run on load, every export has to
// be callable with each kind of argument, and a second copy has to load
// at another address and still work.
func TestDLL(t *testing.T) {
	if runtime.GOOS != "windows" {
		if _, err := exec.LookPath("wine"); err != nil {
			t.Skip("no wine to run Windows executables")
		}
	}
	scandiacus := asmBackend(t)
	dir := t.TempDir()
	src := filepath.Join("..", "tests", "dll")
	for _, f := range []string{"mathmod.scd", "host.scd"} {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command(scandiacus, "build", "--windows", "--dll", filepath.Join(dir, "mathmod.scd")).CombinedOutput(); err != nil {
		t.Fatalf("building the DLL: %v\n%s", err, out)
	}
	lib, err := os.ReadFile(filepath.Join(dir, "mathmod.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mathmod2.dll"), lib, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(scandiacus, "build", "--windows", filepath.Join(dir, "host.scd")).CombinedOutput(); err != nil {
		t.Fatalf("building the host: %v\n%s", err, out)
	}

	host := filepath.Join(dir, "host.exe")
	cmd := exec.Command(host)
	if runtime.GOOS != "windows" {
		cmd = exec.Command("wine", host)
		// wine's own diagnostics go to the same stream as the program's.
		cmd.Env = append(os.Environ(), "WINEDEBUG=-all")
	}
	cmd.Dir = dir
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	want, err := os.ReadFile(filepath.Join(src, "host.out"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("output differs\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}
