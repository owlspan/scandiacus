package main

// Calling arbitrary C functions on Linux and macOS.
//
// The Windows target resolves an `extern fn` through the import table. On
// Linux and macOS the runtime answers a fixed set of names (the library
// the win/os/mem builtins need); anything else the program declares with
// `extern fn ... from "libfoo.so"` is reached by generating a small C
// wrapper for it and letting the C compiler bridge the calling
// convention. The wrapper is an ms_abi function - so the generated code
// calls it the Windows way - that forwards to the real function, which
// the compiler emits under the system convention:
//
//	extern double sin(double);
//	__attribute__((ms_abi)) double __vyw_sin(double a0) { return sin(a0); }
//
// This is correct for any mix of integer, pointer and floating arguments,
// because the C compiler, not a hand-written thunk, places them. Variadic
// functions cannot be forwarded this way and are reported instead.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// cTypeOf maps a Scandiacus boundary type to the C type a wrapper declares it
// with, so the argument is passed in the right register and width.
func cTypeOf(t vty) string {
	switch fixedOf(t) {
	case "i8":
		return "signed char"
	case "u8":
		return "unsigned char"
	case "i16":
		return "short"
	case "u16":
		return "unsigned short"
	case "i32":
		return "int"
	case "u32":
		return "unsigned"
	case "i64":
		return "long long"
	case "u64":
		return "unsigned long long"
	case "f32":
		return "float"
	case "f64":
		return "double"
	}
	switch t.k {
	case kVoid:
		return "void"
	case kBool:
		return "int"
	case kFloat:
		return "double"
	case kStr:
		return "const char*"
	case kBytes:
		return "void*"
	case kFunc:
		return "void*"
	case kInt:
		if strings.HasPrefix(t.name, "*") {
			return "void*" // a typed pointer
		}
		return "long long"
	}
	return "void*"
}

// externWrappers builds the C source of a wrapper for each named extern,
// the library flags to link, and the names it could not wrap (variadic).
func externWrappers(mod *Module, names []string) (code string, libs []string, unsupported []string) {
	var b strings.Builder
	b.WriteString("/* Generated wrappers: each forwards a Windows-convention call\n")
	b.WriteString("   into the system convention the real C function uses. */\n")
	b.WriteString("void *__vy_lazy(const char *lib, const char *sym);\n")
	b.WriteString("void __vy_not_here(const char *sym, const char *lib) __attribute__((noreturn));\n")
	seen := map[string]bool{}
	for _, name := range names {
		if strings.HasPrefix(name, "vycall_") {
			b.WriteString(callThunk(name))
			continue
		}
		es, ok := mod.ExternSigs[name]
		if !ok {
			unsupported = append(unsupported, name)
			continue
		}
		if es.variadic {
			unsupported = append(unsupported, name)
			continue
		}
		ret := cTypeOf(es.ret)
		var decl, types, call []string
		for i, p := range es.params {
			decl = append(decl, fmt.Sprintf("%s a%d", cTypeOf(p), i))
			types = append(types, cTypeOf(p))
			call = append(call, fmt.Sprintf("a%d", i))
		}
		params := strings.Join(decl, ", ")
		if params == "" {
			params = "void"
		}
		ptypes := strings.Join(types, ", ")
		if ptypes == "" {
			ptypes = "void"
		}
		pre := "return "
		if ret == "void" {
			pre = ""
		}
		if lib := unixEquivalent(es.dll); lib != "" {
			// Loaded at the first call, so a program that never makes one
			// needs neither the library nor its development files.
			fmt.Fprintf(&b, "__attribute__((ms_abi)) %s __vyw_%s(%s) { static %s (*f)(%s); "+
				"if (!f) f = (%s (*)(%s))__vy_lazy(%q, %q); %sf(%s); }\n",
				ret, name, params, ret, ptypes, ret, ptypes, lib, name, pre, strings.Join(call, ", "))
			continue
		}
		if windowsOnlyDLL(es.dll) {
			// Only an imported file gets this far (see splitExterns): a
			// package's Windows path, which its Linux path never calls.
			fmt.Fprintf(&b, "__attribute__((ms_abi)) %s __vyw_%s(%s) { __vy_not_here(%q, %q); }\n",
				ret, name, params, name, es.dll)
			continue
		}
		fmt.Fprintf(&b, "extern %s %s(%s);\n", ret, name, params)
		fmt.Fprintf(&b, "__attribute__((ms_abi)) %s __vyw_%s(%s) { %s%s(%s); }\n",
			ret, name, params, pre, name, strings.Join(call, ", "))
		if lib := unixLib(es.dll); lib != "" && !seen[lib] {
			seen[lib] = true
			libs = append(libs, lib)
		}
	}
	return b.String(), libs, unsupported
}

// unixLib turns a `from` string into a linker flag, or "" when there is
// nothing to add (no library named, or a Windows one that does not apply).
func unixLib(from string) string {
	if from == "" {
		return "" // in libc, already linked
	}
	low := strings.ToLower(from)
	if strings.HasSuffix(low, ".dll") || strings.HasSuffix(low, ".exe") {
		return "" // a Windows library name; the symbol must be in libc if anywhere
	}
	if low == "msvcrt" || low == "ucrtbase" {
		return "" // the C runtime, which is libc here
	}
	base := filepath.Base(from)
	if i := strings.Index(strings.ToLower(base), ".so"); i >= 0 {
		base = base[:i]
	} else if i := strings.Index(strings.ToLower(base), ".dylib"); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimPrefix(base, "lib")
	if base == "" {
		return ""
	}
	return "-l" + base
}

// splitExterns divides the externs a program references into the ones the
// runtime already provides, the ones to wrap as C calls, and the ones that
// are genuinely unavailable off Windows (a runtime builtin with no POSIX
// implementation, named by a library the program did not declare).
// isSqliteSym reports whether a symbol is part of sqlite, which the db
// library calls and scandiacussqlite.c provides against the system libsqlite3.
func isSqliteSym(s string) bool { return strings.HasPrefix(s, "sqlite3_") }

// needsSqlite reports whether any of the externs is a sqlite call, so the
// build links libsqlite3.
func needsSqlite(externs []string) bool {
	for _, e := range externs {
		if isSqliteSym(e) {
			return true
		}
	}
	return false
}

func splitExterns(mod *Module, externs []string) (wrap []string, windowsOnly []string) {
	for _, e := range externs {
		if linuxProvided[e] || isSqliteSym(e) {
			continue
		}
		if strings.HasPrefix(e, "vycall_") {
			wrap = append(wrap, e) // a mem.call thunk
			continue
		}
		es, ok := mod.ExternSigs[e]
		switch {
		case !ok:
			windowsOnly = append(windowsOnly, e)
		case windowsOnlyDLL(es.dll) && !es.imported:
			// The program itself calls into a Windows library: say so
			// now. A package doing it gets a stub instead, since it can
			// keep a Windows path it never takes here.
			windowsOnly = append(windowsOnly, e)
		default:
			wrap = append(wrap, e)
		}
	}
	return
}

// windowsLibs are Windows system libraries with nothing behind them on
// Linux or macOS: an extern into one is a Windows-only function.
var windowsLibs = map[string]bool{
	"kernel32": true, "user32": true, "gdi32": true, "advapi32": true, "shell32": true,
	"ole32": true, "oleaut32": true, "comdlg32": true, "comctl32": true, "winmm": true,
	"ws2_32": true, "dwmapi": true, "ntdll": true, "msimg32": true, "uxtheme": true,
	"dxgi": true, "d3d11": true, "d3d12": true, "xinput1_4": true, "winhttp": true,
	"crypt32": true, "bcrypt": true, "psapi": true, "shlwapi": true, "imm32": true,
}

func libBase(from string) string {
	return strings.TrimSuffix(strings.ToLower(filepath.Base(from)), ".dll")
}

func windowsOnlyDLL(from string) bool {
	return windowsLibs[libBase(from)]
}

// unixEquivalent is the library a Windows one is called on this target,
// for the few that are the same API under another name, or "".
func unixEquivalent(from string) string {
	switch libBase(from) {
	case "opengl32":
		if targetMac() {
			return "/System/Library/Frameworks/OpenGL.framework/OpenGL"
		}
		return "libGL.so.1"
	case "vulkan-1":
		if targetMac() {
			return "libvulkan.1.dylib"
		}
		return "libvulkan.so.1"
	}
	return ""
}

// callThunk is the C for one mem.call shape off Windows. The name says
// it: vycall_I_ifi returns a word and takes a word, a double and a
// word. The generated code calls it the Windows way, with the target
// address first, and the C compiler makes the system-convention call.
func callThunk(name string) string {
	parts := strings.SplitN(strings.TrimPrefix(name, "vycall_"), "_", 2)
	ret := "long long"
	if parts[0] == "F" {
		ret = "double"
	}
	sig := ""
	if len(parts) > 1 {
		sig = parts[1]
	}
	decl := []string{"void *fn"}
	var types, call []string
	for i, c := range sig {
		t := "long long"
		if c == 'f' {
			t = "double"
		}
		decl = append(decl, fmt.Sprintf("%s a%d", t, i))
		types = append(types, t)
		call = append(call, fmt.Sprintf("a%d", i))
	}
	ptypes := strings.Join(types, ", ")
	if ptypes == "" {
		ptypes = "void"
	}
	return fmt.Sprintf("__attribute__((ms_abi)) %s __vyw_%s(%s) { return ((%s (*)(%s))fn)(%s); }\n",
		ret, name, strings.Join(decl, ", "), ret, ptypes, strings.Join(call, ", "))
}
