package main

// The macOS target.
//
// macOS on x86-64 is reached the same way Linux is: the code generator's
// output is unchanged, every library call is renamed to __vyw_<name> and
// answered by the same runtime (veylrt.c, veylgdi.c, veylwin.c) compiled
// under the Windows calling convention, and the C compiler does the
// register translation on each call into the real C library. Only two
// things differ from Linux, and both are in the assembly the compiler
// hands to the toolchain rather than in the runtime:
//
//   - Mach-O symbols carry a leading underscore. A C function __vyw_printf
//     is the assembly symbol ___vyw_printf, so the program's calls are
//     renamed to match, and __start (which the runtime calls) becomes
//     ___start and is made global.
//   - Mach-O spells its sections and byte alignment differently: .rdata
//     is __TEXT,__const, .bss is __DATA,__bss, and .align counts bytes
//     through .balign rather than clang's power-of-two .align.
//
// The build runs a C compiler that targets macOS. On a Mac that is the
// system cc; from another system it is a cross compiler named in
// VEYL_MACCC, for example zig: "python3 -m ziglang cc -target
// x86_64-macos". The result is an x86-64 Mach-O, which runs natively on an
// Intel Mac and through Rosetta on Apple Silicon. It is left unsigned;
// recent macOS wants at least an ad-hoc signature, which is a one-line
// `codesign -s - <file>` on the Mac (no account), done by the helper
// script a batch build writes.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func targetMac() bool { return target == "macos" }

// macCC is the C compiler argv that targets macOS.
func macCC() []string {
	if env := os.Getenv("VEYL_MACCC"); env != "" {
		return strings.Fields(env)
	}
	return []string{"cc"}
}

// buildMac turns a module into a macOS executable.
func buildMac(mod *Module, out string) {
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

	tmp, err := os.MkdirTemp("", "veyl-mac-*")
	if err != nil {
		fail("%v", err)
	}
	defer os.RemoveAll(tmp)

	asmPath := filepath.Join(tmp, "prog.s")
	if err := os.WriteFile(asmPath, []byte(transformMacAsm(asmText, externs)), 0o644); err != nil {
		fail("%v", err)
	}

	// The runtime sources and the font header, written together so the
	// #include resolves.
	if err := os.WriteFile(filepath.Join(tmp, "font8x8.h"), []byte(linuxFontHeader), 0o644); err != nil {
		fail("%v", err)
	}
	cc := macCC()
	args := append([]string{}, cc[1:]...)
	args = append(args, "-O2", "-o", out, asmPath)
	for _, name := range runtimeSourceNames() {
		p := filepath.Join(tmp, name)
		if err := os.WriteFile(p, []byte(linuxRuntimeSources[name]), 0o644); err != nil {
			fail("%v", err)
		}
		args = append(args, p)
	}

	if outp, err := exec.Command(cc[0], args...).CombinedOutput(); err != nil {
		fail("the macOS build failed. If this is not a Mac, set VEYL_MACCC to a cross "+
			"compiler such as \"python3 -m ziglang cc -target x86_64-macos\".\n%s\n%s", err, outp)
	}
}

// transformMacAsm rewrites the generated assembly for the Mach-O
// assembler: Mach-O section names, byte alignment, and the leading
// underscore on the symbols that cross into the C runtime.
func transformMacAsm(asmText string, externs []string) string {
	// Longest extern first, so one name is not rewritten inside another.
	sorted := append([]string(nil), externs...)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if len(sorted[j]) > len(sorted[i]) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	res := make([]*regexp.Regexp, len(sorted))
	for i, e := range sorted {
		res[i] = regexp.MustCompile(`\b` + regexp.QuoteMeta(e) + `\b`)
	}
	startRe := regexp.MustCompile(`\b__start\b`)
	mainRe := regexp.MustCompile(`\bmain\b`)
	rename := func(line string) string {
		for i, re := range res {
			line = re.ReplaceAllString(line, "___vyw_"+sorted[i])
		}
		line = startRe.ReplaceAllString(line, "___start")
		line = mainRe.ReplaceAllString(line, "__vy_main")
		return line
	}

	alignRe := regexp.MustCompile(`^\s*\.align\s+(\d+)\s*$`)
	p2Re := regexp.MustCompile(`^\s*\.p2align\s+(\d+)`)

	var out []string
	for _, raw := range strings.Split(asmText, "\n") {
		s := strings.TrimSpace(raw)
		// clang's assembler reads a line starting with # as a preprocessor
		// directive, not a comment, so the comments GNU as accepts are
		// stripped here. String lines keep their bytes, # and all.
		if !strings.HasPrefix(s, ".asciz") && !strings.HasPrefix(s, ".string") &&
			!strings.HasPrefix(s, ".ascii") {
			raw = stripComment(raw)
			s = strings.TrimSpace(raw)
		}
		switch {
		case strings.HasPrefix(s, ".intel_syntax"):
			out = append(out, raw, "    .globl ___start")
		case strings.HasPrefix(s, ".extern"):
			// Mach-O makes undefined symbols external on its own.
		case strings.HasPrefix(s, ".asciz"), strings.HasPrefix(s, ".string"),
			strings.HasPrefix(s, ".ascii"):
			out = append(out, raw)
		case strings.HasPrefix(s, ".section"):
			arg := strings.TrimSpace(strings.TrimPrefix(s, ".section"))
			switch arg {
			case ".rdata", ".rodata":
				out = append(out, "    .section __TEXT,__const")
			case ".bss":
				out = append(out, "    .section __DATA,__bss")
			default:
				out = append(out, raw)
			}
		case s == ".text" || strings.HasPrefix(s, ".text "):
			out = append(out, "    .text")
		case alignRe.MatchString(s):
			out = append(out, "    .balign "+alignRe.FindStringSubmatch(s)[1])
		case p2Re.MatchString(s):
			out = append(out, "    .p2align "+p2Re.FindStringSubmatch(s)[1])
		case strings.HasPrefix(s, ".global"), strings.HasPrefix(s, ".globl"):
			f := strings.Fields(s)
			if len(f) == 2 {
				out = append(out, "    .globl "+rename(f[1]))
			} else {
				out = append(out, rename(raw))
			}
		default:
			out = append(out, rename(raw))
		}
	}
	return strings.Join(out, "\n")
}
