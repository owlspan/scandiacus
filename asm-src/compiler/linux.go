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
// What is not on Linux yet: COM, the HTTP client (WinHTTP), process
// inspection, and static .lib archives (those are COFF). A program that
// needs one is told which calls it used rather than getting a link error.
// Windows and drawing, sound, TCP sockets and sqlite all work here now.

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

// The Linux runtime: the C runtime shim, the software GDI, and the X11
// window / audio layer. Each is embedded, compiled, and linked into the
// program. veylgdi.c includes font8x8.h, so that is embedded too and
// written beside the sources before they are compiled.
//
//go:embed linuxrt/veylrt.c
var linuxRuntimeSource string

//go:embed linuxrt/veylgdi.c
var linuxGdiSource string

//go:embed linuxrt/veylwin.c
var linuxWinSource string

//go:embed linuxrt/veylmac.c
var macWinSource string

//go:embed linuxrt/font8x8.h
var linuxFontHeader string

// The db library's sqlite calls, linked only when a program uses them so
// that other programs need no sqlite installed.
//
//go:embed linuxrt/veylsqlite.c
var sqliteSource string

// The macOS sqlite calls, which dlopen the system libsqlite3.dylib at run
// time rather than linking it, because the cross toolchain has no macOS
// SDK to link against. Linked only when a program uses db.
//
//go:embed linuxrt/veylmacsqlite.c
var macSqliteSource string

// linuxRuntimeSources is every runtime .c, by file name. The order does
// not matter; they are linked together. The window layer differs by
// system - X11 on Linux, Cocoa on macOS - so each target picks its own.
var linuxRuntimeSources = map[string]string{
	"veylrt.c":  linuxRuntimeSource,
	"veylgdi.c": linuxGdiSource,
	"veylwin.c": linuxWinSource,
}

var macRuntimeSources = map[string]string{
	"veylrt.c":  linuxRuntimeSource,
	"veylgdi.c": linuxGdiSource,
	"veylmac.c": macWinSource,
}

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
	case "macos", "mac", "darwin":
		return "macos"
	}
	if runtime.GOOS == "darwin" {
		return "macos"
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
	if targetLinux() || targetMac() {
		return base
	}
	return base + ".exe"
}

// linuxProvided is every import the Linux runtime implements, read off
// its source so the two cannot drift apart.
var linuxProvided = func() map[string]bool {
	m := map[string]bool{}
	re := regexp.MustCompile(`(?:VYW\(|__vyw_)(\w+)`)
	for _, src := range linuxRuntimeSources {
		for _, match := range re.FindAllStringSubmatch(src, -1) {
			m[match[1]] = true
		}
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
	wrap, windowsOnly := splitExterns(mod, externs)
	if len(windowsOnly) > 0 {
		fail("this program uses functions that only exist on Windows so far: %s\n"+
			"        Build it with --windows, or leave out the library that needs them.",
			strings.Join(windowsOnly, ", "))
	}
	wrapperC, wrapperLibs, variadic := externWrappers(mod, wrap)
	if len(variadic) > 0 {
		fail("a variadic extern function cannot be called on Linux yet: %s\n"+
			"        Build it with --windows, or wrap it in a non-variadic C function.",
			strings.Join(variadic, ", "))
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

	linkArgs := []string{"-no-pie", "-o", out, objPath}
	// Wrappers for the C functions the program declared with `extern fn`.
	if wrapperC != "" {
		wp := filepath.Join(tmp, "wrappers.c")
		if err := os.WriteFile(wp, []byte(wrapperC), 0o644); err != nil {
			fail("%v", err)
		}
		linkArgs = append(linkArgs, wp)
	}
	linkArgs = append(linkArgs, linuxRuntimeObjects(cc)...)
	if needsSqlite(externs) {
		sp := filepath.Join(tmp, "veylsqlite.c")
		if err := os.WriteFile(sp, []byte(sqliteSource), 0o644); err != nil {
			fail("%v", err)
		}
		linkArgs = append(linkArgs, sp)
	}
	linkArgs = append(linkArgs, "-lm", "-lpthread", "-ldl")
	if needsSqlite(externs) {
		linkArgs = append(linkArgs, "-lsqlite3")
	}
	linkArgs = append(linkArgs, wrapperLibs...)
	if outp, err := exec.Command(cc, linkArgs...).CombinedOutput(); err != nil {
		fail("linking failed.\n%s\n%s", err, outp)
	}
}

// linuxRuntimeObjects compiles every runtime .c once per version of its
// source and keeps the objects in the user's cache directory, so a build
// only pays for them the first time. The cache key covers every source
// and the font header, so any change rebuilds.
func linuxRuntimeObjects(cc string) []string {
	var all string
	for _, name := range runtimeSourceNames() {
		all += name + "\x00" + linuxRuntimeSources[name] + "\x00"
	}
	all += linuxFontHeader
	sum := sha256.Sum256([]byte(all))
	tag := fmt.Sprintf("%x", sum[:8])

	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "veyl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail("%v", err)
	}

	names := runtimeSourceNames()
	objs := make([]string, 0, len(names))
	var toBuild []string
	for _, name := range names {
		obj := filepath.Join(dir, "veylrt-"+tag+"-"+strings.TrimSuffix(name, ".c")+".o")
		objs = append(objs, obj)
		if _, err := os.Stat(obj); err != nil {
			toBuild = append(toBuild, name)
		}
	}
	if len(toBuild) == 0 {
		return objs
	}

	// Compile in a scratch directory with the sources and the header
	// together, so the #include resolves and partial objects are never
	// seen by a concurrent build.
	work, err := os.MkdirTemp(dir, "build-*")
	if err != nil {
		fail("%v", err)
	}
	defer os.RemoveAll(work)
	if err := os.WriteFile(filepath.Join(work, "font8x8.h"), []byte(linuxFontHeader), 0o644); err != nil {
		fail("%v", err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(work, name), []byte(linuxRuntimeSources[name]), 0o644); err != nil {
			fail("%v", err)
		}
	}
	for _, name := range toBuild {
		part := filepath.Join(work, strings.TrimSuffix(name, ".c")+".o")
		if outp, err := exec.Command(cc, "-c", "-O2", "-o", part, filepath.Join(work, name)).CombinedOutput(); err != nil {
			fail("the Linux runtime could not be compiled.\n%s\n%s", err, outp)
		}
		final := filepath.Join(dir, "veylrt-"+tag+"-"+strings.TrimSuffix(name, ".c")+".o")
		if err := os.Rename(part, final); err != nil {
			// A rename across the same directory should not fail; copy as
			// a fallback so a build still succeeds.
			if data, rerr := os.ReadFile(part); rerr == nil {
				os.WriteFile(final, data, 0o644)
			} else {
				fail("%v", err)
			}
		}
	}
	return objs
}

// runtimeSourceNames is the runtime .c file names in a stable order.
func runtimeSourceNames() []string {
	names := make([]string, 0, len(linuxRuntimeSources))
	for name := range linuxRuntimeSources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
