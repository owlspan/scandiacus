package main

// The Linux target.
//
// The code generator speaks one calling convention, the Windows x64 one,
// and calls the C runtime and kernel32 by name. A Linux program is built
// from exactly the same assembly. Every import it makes is renamed to
// __vyw_<name> and linked against a small runtime, linuxrt/veylrt.c,
// that implements each of those names on POSIX under the Windows
// convention (GCC's ms_abi does the register and stack translation). So
// the instructions the program runs are the ones the Windows target runs,
// and only the layer under them differs.
//
// For now the object is assembled and linked by the system's own tools -
// as, objcopy and cc, which every Linux machine that builds software
// has - the way the Windows target went through MinGW before it had its
// own PE writer. An ELF writer of the compiler's own is the obvious next
// step and does not change anything above this file.
//
// What is not on Linux yet: windows and drawing, sound, COM, sockets and
// HTTP, sqlite, process inspection, and static .lib archives (those are
// COFF). A program that needs one is told which calls it used rather
// than getting a link error.

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

//go:embed linuxrt/veylrt.c
var linuxRuntimeSource string

// Which operating system the output is for: "windows" or "linux".
// Chosen by --windows / --linux on the command line, else VEYL_TARGET,
// else the system the compiler runs on.
var target = defaultTarget()

func defaultTarget() string {
	switch strings.ToLower(os.Getenv("VEYL_TARGET")) {
	case "linux":
		return "linux"
	case "windows":
		return "windows"
	}
	if runtime.GOOS == "linux" {
		return "linux"
	}
	return "windows"
}

func targetLinux() bool { return target == "linux" }

// exeName is the file a build writes for a source path.
func exeName(source string) string {
	base := strings.TrimSuffix(source, filepath.Ext(source))
	if targetLinux() {
		return base
	}
	return base + ".exe"
}

// linuxProvided is every import the Linux runtime implements, read off
// its source so the two cannot drift apart.
var linuxProvided = func() map[string]bool {
	m := map[string]bool{}
	for _, match := range regexp.MustCompile(`VYW\((\w+)\)`).FindAllStringSubmatch(linuxRuntimeSource, -1) {
		m[match[1]] = true
	}
	return m
}()

// asmExterns lists the symbols a program's assembly imports.
func asmExterns(asmText string) []string {
	var out []string
	for _, line := range strings.Split(asmText, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, ".extern ") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, ".extern ")))
		}
	}
	sort.Strings(out)
	return out
}

// buildLinux turns a module into a Linux executable.
func buildLinux(mod *Module, out string) {
	if mod.DLL {
		fail("--dll builds a Windows DLL; build it with --windows")
	}
	if len(staticImports) > 0 {
		fail("static libraries (.lib, .a) are linked as COFF, which is Windows-only for now; " +
			"build this program with --windows")
	}

	asmText := Emit(mod)
	if !envOff("VEYL_NOPEEP") && !envOff("VEYL_NOOPT") {
		asmText = peephole(asmText)
	}

	externs := asmExterns(asmText)
	var missing []string
	for _, e := range externs {
		if !linuxProvided[e] {
			missing = append(missing, e)
		}
	}
	if len(missing) > 0 {
		fail("this program uses functions that only exist on Windows so far: %s\n"+
			"        Build it with --windows, or leave out the library that needs them.",
			strings.Join(missing, ", "))
	}

	asTool := linuxTool("as")
	objcopy := linuxTool("objcopy")
	cc := linuxTool("cc", "gcc", "clang")

	tmp, err := os.MkdirTemp("", "veyl-build-*")
	if err != nil {
		fail("%v", err)
	}
	defer os.RemoveAll(tmp)

	// The assembly is the Windows target's, with the read-only section
	// given its ELF name and the stack marked non-executable.
	asmText = strings.ReplaceAll(asmText, ".section .rdata", ".section .rodata")
	asmText += "\n    .section .note.GNU-stack,\"\",@progbits\n"

	asmPath := filepath.Join(tmp, "prog.s")
	objPath := filepath.Join(tmp, "prog.o")
	if err := os.WriteFile(asmPath, []byte(asmText), 0o644); err != nil {
		fail("%v", err)
	}
	if outp, err := exec.Command(asTool, "--64", "-o", objPath, asmPath).CombinedOutput(); err != nil {
		fail("the assembler rejected the generated code. This is a compiler "+
			"bug, not a mistake in your program.\n%s\n%s", err, outp)
	}

	// Every import becomes the runtime's __vyw_ name, and the program's
	// own main makes way for the C one the runtime starts from.
	var renames strings.Builder
	for _, e := range externs {
		fmt.Fprintf(&renames, "%s __vyw_%s\n", e, e)
	}
	renames.WriteString("main __vy_main\n")
	renPath := filepath.Join(tmp, "renames.txt")
	if err := os.WriteFile(renPath, []byte(renames.String()), 0o644); err != nil {
		fail("%v", err)
	}
	if outp, err := exec.Command(objcopy, "--redefine-syms="+renPath,
		"--globalize-symbol="+entrySymbol, objPath).CombinedOutput(); err != nil {
		fail("objcopy failed.\n%s\n%s", err, outp)
	}

	rt := linuxRuntimeObject(cc)
	if outp, err := exec.Command(cc, "-no-pie", "-o", out, objPath, rt,
		"-lm", "-lpthread").CombinedOutput(); err != nil {
		fail("linking failed.\n%s\n%s", err, outp)
	}
}

// linuxRuntimeObject compiles the runtime once per version of its source
// and keeps the object in the user's cache directory.
func linuxRuntimeObject(cc string) string {
	sum := sha256.Sum256([]byte(linuxRuntimeSource))
	name := "veylrt-" + fmt.Sprintf("%x", sum[:8]) + ".o"

	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "veyl")
	obj := filepath.Join(dir, name)
	if _, err := os.Stat(obj); err == nil {
		return obj
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail("%v", err)
	}

	// Built under a temporary name and renamed into place, so builds
	// running at the same time never see half an object.
	src, err := os.CreateTemp(dir, "veylrt-*.c")
	if err != nil {
		fail("%v", err)
	}
	defer os.Remove(src.Name())
	if _, err := src.WriteString(linuxRuntimeSource); err != nil {
		fail("%v", err)
	}
	src.Close()
	part := strings.TrimSuffix(src.Name(), ".c") + ".o"
	if outp, err := exec.Command(cc, "-c", "-O2", "-o", part, src.Name()).CombinedOutput(); err != nil {
		os.Remove(part)
		fail("the Linux runtime could not be compiled.\n%s\n%s", err, outp)
	}
	if err := os.Rename(part, obj); err != nil {
		fail("%v", err)
	}
	return obj
}

// linuxTool finds the first of the named programs on PATH.
func linuxTool(names ...string) string {
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	fail("building for Linux needs %s on PATH (the binutils and a C compiler; "+
		"on Debian or Ubuntu: apt install build-essential)", names[0])
	return ""
}
