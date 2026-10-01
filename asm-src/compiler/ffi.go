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

// cTypeOf maps a Veyl boundary type to the C type a wrapper declares it
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
	seen := map[string]bool{}
	for _, name := range names {
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
		var decl, call []string
		for i, p := range es.params {
			decl = append(decl, fmt.Sprintf("%s a%d", cTypeOf(p), i))
			call = append(call, fmt.Sprintf("a%d", i))
		}
		params := strings.Join(decl, ", ")
		if params == "" {
			params = "void"
		}
		pre := "return "
		if ret == "void" {
			pre = ""
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
func splitExterns(mod *Module, externs []string) (wrap []string, windowsOnly []string) {
	for _, e := range externs {
		if linuxProvided[e] {
			continue
		}
		if _, ok := mod.ExternSigs[e]; ok {
			wrap = append(wrap, e)
		} else {
			windowsOnly = append(windowsOnly, e)
		}
	}
	return
}
