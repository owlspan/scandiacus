package main

// The driver for the assembly backend.
//
//     veyl run   f.vl    compile and run
//     veyl build f.vl    write an executable next to the source
//     veyl asm   f.vl    print the generated assembly
//     veyl ir    f.vl    print the intermediate representation
//
// `asm` and `ir` are the debugging tools, and they matter more here than
// `veyl emit` does on the Go backend: when a register allocator produces
// something wrong, reading the instruction stream is the only way to see
// it. They exist from the first commit for that reason.
//
// Nothing here shells out. The assembly text goes to the byte encoder
// in encode.go, the linker in link.go lays it out, and pe.go writes a
// Windows executable - so `veyl build` needs no assembler, no linker
// and no C runtime installed, which is the thing this backend exists
// for. VEYL_LINK=mingw takes the old route through `as` and `gcc`, and
// is kept as the reference the encoder is checked against.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const Version = "0.40.0"

const usage = `veyl ` + Version + ` - the Veyl compiler

usage:
  veyl run   <file.vl>    compile and run
  veyl build <file.vl>    compile to an executable next to the source
  veyl build --dll <file.vl>
                          compile to a DLL; export fn marks what it exports
  veyl asm   <file.vl>    print the generated assembly
  veyl ir    <file.vl>    print the intermediate representation
  veyl version            print the version

packages:
  veyl get <name>            install a package into ./veyl_modules
  veyl get <user/repo>       install from a GitHub repository
  veyl get <url>             install from a url
  veyl get officials         install every official package
  veyl get officials --nodlls   the same, minus the ones carrying a
                                native library, which are the large ones
  veyl get <user/repo> --all every package a registry lists
  veyl list                  what is installed here
  veyl remove <name>         uninstall it

A package installs next to the program that uses it rather than
machine-wide, so a project carries its own dependencies and deleting
veyl_modules uninstalls everything. Reach one with a bare import:

  import "sqlite"

Anything after the .vl file goes to your program rather than to veyl,
so ` + "`veyl run app.vl --verbose`" + ` reaches os.args().

This compiles Veyl straight to x86-64 and writes the .exe itself: it
encodes the instructions, resolves the symbols and lays out the PE. No
assembler, no linker, no C toolchain, nothing to install, and none of
them in what comes out.

asm and ir are the debugging tools. When a program does something
strange, read what it actually compiled to. ir is the readable one:
three-address code over virtual registers, before anything knows an x86
register exists.

Set VEYL_LINK=mingw to go through as and gcc instead. That is only for
checking this compiler's own output against a known assembler.
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(usage)
		os.Exit(2)
	}

	cmd := args[0]
	if cmd == "version" {
		fmt.Printf("veyl %s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
		return
	}
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Print(usage)
		return
	}

	// The package commands, which take a name rather than a file.
	switch cmd {
	case "get":
		if len(args) < 2 {
			fail("get needs a package name or a url")
		}
		var specs []string
		noDLLs, all := false, false
		for _, a := range args[1:] {
			switch a {
			case "--nodlls", "--no-dlls":
				noDLLs = true
			case "--all":
				all = true
			default:
				if strings.HasPrefix(a, "-") {
					fail("unknown option %q - get takes --all and --nodlls", a)
				}
				specs = append(specs, a)
			}
		}
		if len(specs) == 0 {
			fail("get needs a package name or a url")
		}
		for _, spec := range specs {
			// `officials` is the whole registry rather than a package
			// in it, and is the one name `veyl get` reserves.
			if spec == "officials" || spec == "official" {
				if err := pkgGetAll(officialBase, noDLLs); err != nil {
					fail("%v", err)
				}
				continue
			}
			if all {
				base, _, err := resolveSpec(spec)
				if err != nil {
					fail("cannot get %s: %v", spec, err)
				}
				if err := pkgGetAll(base, noDLLs); err != nil {
					fail("cannot get %s: %v", spec, err)
				}
				continue
			}
			if err := pkgGet(spec); err != nil {
				fail("cannot get %s: %v", spec, err)
			}
		}
		return
	case "list":
		if err := pkgList(); err != nil {
			fail("%v", err)
		}
		return
	case "remove":
		if len(args) < 2 {
			fail("remove needs a package name")
		}
		if err := pkgRemove(args[1]); err != nil {
			fail("%v", err)
		}
		return
	}
	// `veyl f.vl` means `veyl run f.vl`, matching the Go backend.
	// A first argument ending in .vl can only be a file, since no command
	// does, so this cannot shadow one.
	if strings.HasSuffix(cmd, ".vl") {
		args = append([]string{"run"}, args...)
		cmd = "run"
	}

	// `veyl build --dll f.vl` makes a library instead of a program.
	dll := false
	if cmd == "build" && len(args) > 1 && args[1] == "--dll" {
		dll = true
		args = append([]string{args[0]}, args[2:]...)
	}

	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "veyl: %s needs a file\n", cmd)
		os.Exit(2)
	}

	lowerForDLL = dll
	path, err := filepath.Abs(args[1])
	if err != nil {
		fail("%v", err)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		fail("cannot read %s: %v", args[1], err)
	}

	mod := compile(string(source), path)

	switch cmd {
	case "ir":
		fmt.Print(mod)
	case "asm":
		fmt.Print(Emit(mod))
	case "build":
		out := strings.TrimSuffix(path, filepath.Ext(path)) + ".exe"
		if dll {
			out = strings.TrimSuffix(out, ".exe") + ".dll"
			mod.DLL = true
		}
		if err := missingForeignDLLs(filepath.Dir(path), mod); err != nil {
			fail("%v", err)
		}
		buildExe(mod, out)
		// A package may carry a native library. Put it beside the
		// executable, or the program will not start on a machine that
		// does not happen to have it.
		copyDLLsBeside(out, packageDLLs(filepath.Dir(path)))
		fmt.Printf("wrote %s\n", out)
	case "run":
		tmp, err := os.MkdirTemp("", "veyl-*")
		if err != nil {
			fail("%v", err)
		}
		defer os.RemoveAll(tmp)
		out := filepath.Join(tmp, "prog.exe")
		if err := missingForeignDLLs(filepath.Dir(path), mod); err != nil {
			fail("%v", err)
		}
		buildExe(mod, out)
		copyDLLsBeside(out, packageDLLs(filepath.Dir(path)))
		run := programCmd(out, args[2:])
		run.Stdout, run.Stderr, run.Stdin = os.Stdout, os.Stderr, os.Stdin
		if err := run.Run(); err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				os.Exit(exit.ExitCode())
			}
			fail("%v", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "veyl: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

// programCmd is the command that runs a compiled program, with the
// arguments that followed the .vl file on veyl's own command line.
//
// The output is always a Windows executable. On any other host it goes
// through wine when wine is installed, which is what lets the test suite
// run on a Linux machine; without wine it is attempted directly and the
// failure names the file.
func programCmd(exe string, args []string) *exec.Cmd {
	if runtime.GOOS != "windows" {
		if wine, err := exec.LookPath("wine"); err == nil {
			return exec.Command(wine, append([]string{exe}, args...)...)
		}
	}
	return exec.Command(exe, args...)
}

// compile runs the front end and lowers to IR, reporting every error it
// found in source order rather than only the first.
func compile(source, path string) *Module {
	lex := NewLexer(path, source)
	tokens := lex.Scan()

	p := NewParser(path, tokens)
	prog := p.ParseProgram()

	if errs := append(append([]string{}, lex.Errors...), p.Errors...); len(errs) > 0 {
		report(errs)
	}

	// Imported files are folded in before anything is checked, so from
	// here down there is one program and nothing else has to know that
	// more than one file was involved.
	stampImportedFile(prog, path)
	importErrs, imported := loadImports(prog, path)
	if len(importErrs) > 0 {
		report(importErrs)
	}

	// The prelude goes in before checking, so its functions are checked
	// and lowered like any other code the program contains. Which ones
	// it needs is read off the source, so the imported files count too.
	if errs := addPrelude(prog, append([]string{source}, imported...)); len(errs) > 0 {
		report(errs)
	}
	if errs := addLibrary(prog, append([]string{source}, imported...)); len(errs) > 0 {
		report(errs)
	}

	// The shared type checker, with this backend's own library. A
	// builtin this backend does not have is now an ordinary type error
	// with a position, reported alongside everything else, rather than
	// something the lowerer stumbles into later.
	ck := NewChecker(path, asmLibrary{})
	ck.Check(prog)
	if len(ck.Errors) > 0 {
		report(ck.Errors)
	}

	mod, lowerErrs := Lower(prog, path)
	if len(lowerErrs) > 0 {
		report(lowerErrs)
	}
	Optimize(mod)
	return mod
}

func report(errs []string) {
	sortByPosition(errs)
	// The same message at the same place twice is one mistake: a
	// receiver that is itself a call can be checked on the way to each
	// method called on it.
	seen := map[string]bool{}
	uniq := errs[:0]
	for _, e := range errs {
		if !seen[e] {
			seen[e] = true
			uniq = append(uniq, e)
		}
	}
	errs = uniq
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, e)
	}
	fmt.Fprintf(os.Stderr, "veyl: %d error(s)\n", len(errs))
	os.Exit(1)
}

// buildExe turns a module into an executable.
//
// The default path is entirely inside this compiler: Emit writes the
// assembly, assembleObject encodes it and lays it out, and writePE
// writes a PE. Nothing is installed and nothing is shelled out to.
//
// VEYL_LINK=mingw takes the old path instead, through `as` and `gcc`.
// It is kept because it is the reference the byte encoder is checked
// against, and because a program that runs one way and not the other
// localises a bug to the half that changed.
func buildExe(mod *Module, out string) {
	asmText := Emit(mod)
	if !envOff("VEYL_NOPEEP") && !envOff("VEYL_NOOPT") {
		asmText = peephole(asmText)
	}

	if os.Getenv("VEYL_LINK") != "mingw" {
		obj, err := assembleObject(asmText)
		if err != nil {
			fail("the generated assembly could not be encoded. This is a "+
				"compiler bug, not a mistake in your program.\n%v", err)
		}
		opt := peOptions{dll: mod.DLL, name: filepath.Base(out), exports: mod.Exports}
		if err := writePE(obj, out, opt); err != nil {
			fail("the executable could not be written. This is a compiler "+
				"bug, not a mistake in your program.\n%v", err)
		}
		return
	}

	if mod.DLL {
		fail("VEYL_LINK=mingw cannot build a DLL; unset it to use the built-in linker")
	}
	buildExeWithMinGW(asmText, out)
}

// buildExeWithMinGW writes the assembly, then assembles and links it.
func buildExeWithMinGW(asmText string, out string) {
	tmp, err := os.MkdirTemp("", "veyl-build-*")
	if err != nil {
		fail("%v", err)
	}
	defer os.RemoveAll(tmp)

	asmPath := filepath.Join(tmp, "prog.s")
	objPath := filepath.Join(tmp, "prog.o")

	if err := os.WriteFile(asmPath, []byte(asmText), 0o644); err != nil {
		fail("%v", err)
	}

	as, cc, binDir := findToolchain()

	if outp, err := toolchainCmd(binDir, as, asmPath, "-o", objPath).CombinedOutput(); err != nil {
		fail("the assembler rejected the generated code. This is a compiler "+
			"bug, not a mistake in your program.\n%s\n%s", err, outp)
	}
	if outp, err := toolchainCmd(binDir, cc, objPath, "-o", out).CombinedOutput(); err != nil {
		fail("linking failed.\n%s\n%s", err, outp)
	}
}

// toolchainCmd runs a MinGW tool with MinGW's bin directory on PATH.
//
// Finding gcc.exe by absolute path is not enough. gcc spawns collect2,
// which spawns ld, and ld lives in a different directory from the DLLs
// it needs. With MinGW off PATH - the normal state of a Windows machine
// that has it - ld cannot load them and exits 53, and the only thing
// that reaches the surface is "ld returned 53 exit status".
//
// This cost a real debugging session because it depends on the shell:
// Git Bash happens to carry compatible DLLs on PATH, so the same command
// worked there and failed from cmd.
//
// The Go backend has the same shape of problem and answers it the same
// way, by setting GOROOT for its child. Nothing is added to this
// process's own PATH, so a developer's own toolchain stays unshadowed.
func toolchainCmd(binDir, exe string, args ...string) *exec.Cmd {
	cmd := exec.Command(exe, args...)
	if binDir == "" {
		return cmd
	}
	env := os.Environ()
	for i, kv := range env {
		if len(kv) >= 5 && strings.EqualFold(kv[:5], "PATH=") {
			env[i] = "PATH=" + binDir + string(os.PathListSeparator) + kv[5:]
			cmd.Env = env
			return cmd
		}
	}
	cmd.Env = append(env, "PATH="+binDir)
	return cmd
}

// findToolchain locates the assembler and linker. MinGW is not usually
// on PATH on a Windows machine that has it, so the known install
// locations are checked too, the same way findGo does on the Go backend.
func findToolchain() (as string, cc string, binDir string) {
	if env := os.Getenv("VEYL_MINGW"); env != "" {
		return filepath.Join(env, "as.exe"), filepath.Join(env, "gcc.exe"), env
	}

	if a, err := exec.LookPath("as"); err == nil {
		if c, err := exec.LookPath("gcc"); err == nil {
			return a, c, filepath.Dir(c)
		}
	}

	for _, dir := range []string{
		`C:\msys64\mingw64\bin`,
		`C:\msys64\ucrt64\bin`,
		`C:\mingw64\bin`,
		`C:\MinGW\bin`,
	} {
		a := filepath.Join(dir, "as.exe")
		c := filepath.Join(dir, "gcc.exe")
		if exists(a) && exists(c) {
			return a, c, dir
		}
	}

	fail("cannot find an assembler. veyl needs MinGW's `as` and `gcc`.\n" +
		"Install MSYS2 and its mingw-w64 toolchain, or set VEYL_MINGW to the\n" +
		"folder holding as.exe and gcc.exe.")
	return "", "", ""
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "veyl: "+format+"\n", args...)
	os.Exit(1)
}

// sortByPosition puts errors in source order. Windows paths carry a
// colon of their own, so the line and column are taken from the end
// rather than by splitting on the first colon.
var positionRE = regexp.MustCompile(`:(\d+):(\d+): `)

func sortByPosition(errs []string) {
	// The first ":line:col:" is the position. Searching from the front
	// rather than splitting on the last colons matters twice over: a
	// Windows path has a colon of its own, and so do plenty of messages.
	key := func(s string) (int, int) {
		m := positionRE.FindStringSubmatch(s)
		if m == nil {
			return 0, 0
		}
		line, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		return line, col
	}
	sort.SliceStable(errs, func(i, j int) bool {
		li, ci := key(errs[i])
		lj, cj := key(errs[j])
		if li != lj {
			return li < lj
		}
		return ci < cj
	})
}
