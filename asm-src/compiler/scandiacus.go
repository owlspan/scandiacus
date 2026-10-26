package main

// The driver for the assembly backend.
//
//     scandiacus run   f.scd    compile and run
//     scandiacus build f.scd    write an executable next to the source
//     scandiacus asm   f.scd    print the generated assembly
//     scandiacus ir    f.scd    print the intermediate representation
//
// `asm` and `ir` are the debugging tools, and they matter more here than
// `scandiacus emit` does on the Go backend: when a register allocator produces
// something wrong, reading the instruction stream is the only way to see
// it. They exist from the first commit for that reason.
//
// Nothing here shells out. The assembly text goes to the byte encoder
// in encode.go, the linker in link.go lays it out, and pe.go writes a
// Windows executable - so `scandiacus build` needs no assembler, no linker
// and no C runtime installed, which is the thing this backend exists
// for. SCANDIACUS_LINK=mingw takes the old route through `as` and `gcc`, and
// is kept as the reference the encoder is checked against.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
)

const Version = "2.3.0"

const usage = `scandiacus ` + Version + ` - the Scandiacus compiler

usage:
  scandiacus run   <file.scd>    compile and run
  scandiacus build <file.scd>    compile to an executable next to the source
  scandiacus build --dll <file.scd>
                          compile to a DLL; export fn marks what it exports
  scandiacus build <file.scd> -o <path>
                          write the executable somewhere else
  scandiacus build <file.scd> --icon <file.ico>
                          give a Windows executable its icon
  scandiacus build <file.scd> --product <name> --description <text>
             --company <name> --copyright <text> --file-version <1.2.3>
                          what Properties, Details shows; the product
                          and description default to the file's name
  scandiacus run --linux <file.scd>, scandiacus build --windows <file.scd>
                          pick the system the program is for; the default
                          is the one scandiacus runs on (or SCANDIACUS_TARGET)
  scandiacus cross install      set up building for Linux and macOS from any
                          system (downloads zig, about 80 MB, once)
  scandiacus asm   <file.scd>    print the generated assembly
  scandiacus ir    <file.scd>    print the intermediate representation
  scandiacus check <file.scd>... report errors and warnings without building
  scandiacus doc   <file.scd>    a reference from its declarations and comments
                          (-o FILE writes it there; a .html name, a web page)
  scandiacus test  <file.scd>... run the test "name" { } blocks in files or folders
                          (--run TEXT only the tests whose names contain it,
                          --bench runs the bench "name" { } blocks instead)
  scandiacus fmt   <file.scd>... rewrite files in the standard layout
                          (--check lists the ones that are not, and fails)
  scandiacus lsp                a language server on stdin and stdout, for editors
  scandiacus vscode             install the Scandiacus extension into VS Code
                          (--vsix FILE only writes the package,
                          --uninstall removes it)
  scandiacus version            print the version

packages:
  scandiacus get <name>            install a package into ./scandiacus_modules
  scandiacus get <user/repo>       install from a GitHub repository
  scandiacus get <url>             install from a url
  scandiacus get officials         install every official package
  scandiacus get officials --nodlls   the same, minus the ones carrying a
                                native library, which are the large ones
  scandiacus get <user/repo> --all every package a registry lists
  scandiacus list                  what is installed here
  scandiacus remove <name>         uninstall it

A package installs next to the program that uses it rather than
machine-wide, so a project carries its own dependencies and deleting
scandiacus_modules uninstalls everything. Reach one with a bare import:

  import "sqlite"

Anything after the .scd file goes to your program rather than to scandiacus,
so ` + "`scandiacus run app.scd --verbose`" + ` reaches os.args().

This compiles Scandiacus straight to x86-64 and writes the .exe itself: it
encodes the instructions, resolves the symbols and lays out the PE. No
assembler, no linker, no C toolchain, nothing to install, and none of
them in what comes out.

On Linux, scandiacus builds a Linux executable from the same code, linked
with the system's as and cc against a small runtime that stands in for
the Windows libraries. Console programs work; the Windows-only libraries
(windows and drawing, sound, COM, sockets, HTTP, sqlite, proc) say so
when a program uses them.

asm and ir are the debugging tools. When a program does something
strange, read what it actually compiled to. ir is the readable one:
three-address code over virtual registers, before anything knows an x86
register exists.

Set SCANDIACUS_LINK=mingw to go through as and gcc instead. That is only for
checking this compiler's own output against a known assembler.
`

func main() {
	// A build is short and holds little: collecting when the heap has
	// grown fourfold rather than doubled takes most of the time Go's
	// collector spent on it, for a few more megabytes at the peak. The
	// language server runs for hours and keeps the default.
	if len(os.Args) < 2 || os.Args[1] != "lsp" {
		debug.SetGCPercent(400)
	}
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(usage)
		os.Exit(2)
	}

	cmd := args[0]
	if cmd == "version" {
		fmt.Printf("scandiacus %s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
		return
	}
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Print(usage)
		return
	}

	if cmd == "doc" {
		docCommand(args[1:])
		return
	}
	if cmd == "check" {
		checkCommand(args[1:])
		return
	}
	if cmd == "test" {
		testCommand(args[1:])
		return
	}
	if cmd == "fmt" {
		fmtCommand(args[1:])
		return
	}
	if cmd == "lsp" {
		lspCommand()
		return
	}
	if cmd == "cross" {
		crossCommand(args[1:])
		return
	}
	if cmd == "vscode" {
		vscodeCommand(args[1:])
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
			// in it, and is the one name `scandiacus get` reserves.
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
	// `scandiacus f.scd` means `scandiacus run f.scd`, matching the Go backend.
	// A first argument ending in .scd can only be a file, since no command
	// does, so this cannot shadow one.
	if strings.HasSuffix(cmd, ".scd") {
		args = append([]string{"run"}, args...)
		cmd = "run"
	}

	// Options between the command and the file. `--dll` makes a library
	// instead of a program; `--linux` and `--windows` pick the system the
	// output runs on (the default is the one scandiacus itself is running on,
	// or SCANDIACUS_TARGET when set).
	// For build, options may come after the file too - `scandiacus build
	// game.scd -o game.exe` - since nothing follows a build's file the
	// way a program's arguments follow run's. Move the file to the end
	// so the loop below sees every option first.
	if cmd == "build" {
		for i := 1; i < len(args); i++ {
			if strings.HasSuffix(args[i], ".scd") {
				file := args[i]
				args = append(append(args[:i:i], args[i+1:]...), file)
				break
			}
		}
	}

	dll := false
	outPath, iconPath := "", ""
	// value takes an option's value, written --opt=v or --opt v.
	value := func(opt, name string) string {
		if v, ok := strings.CutPrefix(opt, name+"="); ok {
			return v
		}
		if len(args) < 3 {
			fail("%s needs a value", name)
		}
		v := args[2]
		args = append(args[:2], args[3:]...)
		return v
	}
	for len(args) > 1 && strings.HasPrefix(args[1], "-") && !strings.HasSuffix(args[1], ".scd") {
		switch opt := args[1]; {
		case cmd == "build" && (opt == "-o" || opt == "--out" || strings.HasPrefix(opt, "--out=")):
			outPath = value(opt, "--out")
		case cmd == "build" && (opt == "--icon" || strings.HasPrefix(opt, "--icon=")):
			iconPath = value(opt, "--icon")
		case cmd == "build" && (opt == "--product" || strings.HasPrefix(opt, "--product=")):
			buildVersion.product = value(opt, "--product")
		case cmd == "build" && (opt == "--description" || strings.HasPrefix(opt, "--description=")):
			buildVersion.description = value(opt, "--description")
		case cmd == "build" && (opt == "--company" || strings.HasPrefix(opt, "--company=")):
			buildVersion.company = value(opt, "--company")
		case cmd == "build" && (opt == "--copyright" || strings.HasPrefix(opt, "--copyright=")):
			buildVersion.copyright = value(opt, "--copyright")
		case cmd == "build" && (opt == "--file-version" || strings.HasPrefix(opt, "--file-version=")):
			v, err := parseVersion(value(opt, "--file-version"))
			if err != nil {
				fail("--file-version: %v", err)
			}
			buildVersion.version = v
		case opt == "--dll" && cmd == "build":
			dll = true
		case opt == "--linux" || opt == "--target=linux":
			target = "linux"
		case opt == "--windows" || opt == "--target=windows":
			target = "windows"
		case opt == "--macos" || opt == "--mac" || opt == "--target=macos":
			target = "macos"
		default:
			fail("unknown option %q", opt)
		}
		args = append([]string{args[0]}, args[2:]...)
	}

	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "scandiacus: %s needs a file\n", cmd)
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

	showWarnings = cmd == "build"
	mod := compile(string(source), path)

	switch cmd {
	case "ir":
		fmt.Print(mod)
	case "asm":
		fmt.Print(Emit(mod))
	case "build":
		out := exeName(path)
		if dll {
			out = strings.TrimSuffix(path, filepath.Ext(path)) + ".dll"
			mod.DLL = true
		}
		if outPath != "" {
			if out, err = filepath.Abs(outPath); err != nil {
				fail("%v", err)
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				fail("%v", err)
			}
		}
		if iconPath != "" {
			if targetLinux() || targetMac() {
				fmt.Fprintf(os.Stderr, "scandiacus: --icon left out: a %s executable carries no icon\n", target)
			} else {
				raw, err := os.ReadFile(iconPath)
				if err != nil {
					fail("cannot read the icon: %v", err)
				}
				if buildIcon, err = parseICO(raw); err != nil {
					fail("%s: %v", iconPath, err)
				}
			}
		}
		writeExecutable(mod, out, filepath.Dir(path))
		fmt.Printf("wrote %s\n", out)
	case "run":
		tmp, err := os.MkdirTemp("", "scandiacus-*")
		if err != nil {
			fail("%v", err)
		}
		defer os.RemoveAll(tmp)
		out := exeName(filepath.Join(tmp, "prog.scd"))
		if targetMac() {
			buildMac(mod, out)
		} else if targetLinux() {
			buildLinux(mod, out)
		} else {
			if err := missingForeignDLLs(filepath.Dir(path), mod); err != nil {
				fail("%v", err)
			}
			buildExe(mod, out, filepath.Dir(path))
			copyDLLsBeside(out, packageDLLs(filepath.Dir(path)))
		}
		run := programCmd(out, args[2:])
		run.Stdout, run.Stderr, run.Stdin = os.Stdout, os.Stderr, os.Stdin
		if err := run.Run(); err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				os.Exit(exit.ExitCode())
			}
			fail("%v", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "scandiacus: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

// programCmd is the command that runs a compiled program, with the
// arguments that followed the .scd file on scandiacus's own command line.
//
// The output is always a Windows executable. On any other host it goes
// through wine when wine is installed, which is what lets the test suite
// run on a Linux machine; without wine it is attempted directly and the
// failure names the file.
func programCmd(exe string, args []string) *exec.Cmd {
	// Only a Windows-target program needs wine, and only off Windows. A
	// Linux or macOS program runs directly on its own system.
	if !targetLinux() && !targetMac() && runtime.GOOS != "windows" {
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
	// A test build compiles the test blocks as functions and swaps the
	// program body for the harness that runs one of them; see testing.go.
	harness := ""
	testNames, testBenches = nil, nil
	if testMode {
		for _, t := range prog.Tests {
			prog.Funcs = append(prog.Funcs, t)
			testNames = append(testNames, t.TestName)
			testBenches = append(testBenches, t.Bench)
		}
		harness = testHarness(prog.Tests)
		prog.Main = NewParser(path, NewLexer(path, harness).Scan()).ParseProgram().Main
	}

	stampImportedFile(prog, path)
	importErrs, imported := loadImports(prog, path)
	imported = append(imported, harness)
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
	if showWarnings {
		printWarnings(ck.Warnings)
	}

	mod, lowerErrs := Lower(prog, path)
	if len(lowerErrs) > 0 {
		report(lowerErrs)
	}
	Optimize(mod)
	return mod
}

// showWarnings is set by scandiacus build, which prints the checker's warnings
// on stderr. scandiacus run does not, so a program's own output is all it
// writes; scandiacus check prints them on their own.
var showWarnings bool

func printWarnings(ws []string) {
	ws = append([]string(nil), ws...)
	sortByPosition(ws)
	seen := map[string]bool{}
	for _, w := range ws {
		if seen[w] {
			continue
		}
		seen[w] = true
		if m := errPos.FindStringSubmatch(w); m != nil {
			fmt.Fprintf(os.Stderr, "%s:%s:%s: warning: %s\n", m[1], m[2], m[3], m[4])
		} else {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
	}
}

// checkCommand is scandiacus check: the errors and warnings scandiacus build would
// print, without building anything. It fails only on errors.
func checkCommand(args []string) {
	if len(args) == 0 {
		fail("scandiacus check needs a .scd file")
	}
	bad := false
	for _, file := range args {
		b, err := os.ReadFile(file)
		if err != nil {
			fail("%v", err)
		}
		_, errs, warns := analyzeWithWarnings(string(b), file)
		sortByPosition(errs)
		seen := map[string]bool{}
		for _, e := range errs {
			if !seen[e] {
				seen[e] = true
				fmt.Fprintln(os.Stderr, e)
			}
		}
		printWarnings(warns)
		if len(errs) > 0 {
			bad = true
		}
	}
	if bad {
		os.Exit(1)
	}
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
	fmt.Fprintf(os.Stderr, "scandiacus: %d error(s)\n", len(errs))
	os.Exit(1)
}

// buildExe turns a module into an executable.
//
// The default path is entirely inside this compiler: Emit writes the
// assembly, assembleObject encodes it and lays it out, and writePE
// writes a PE. Nothing is installed and nothing is shelled out to.
//
// SCANDIACUS_LINK=mingw takes the old path instead, through `as` and `gcc`.
// It is kept because it is the reference the byte encoder is checked
// against, and because a program that runs one way and not the other
// localises a bug to the half that changed.
// writeExecutable writes a compiled module for the target system.
func writeExecutable(mod *Module, out, root string) {
	if targetMac() {
		buildMac(mod, out)
		return
	}
	if targetLinux() {
		buildLinux(mod, out)
		return
	}
	if err := missingForeignDLLs(root, mod); err != nil {
		fail("%v", err)
	}
	buildExe(mod, out, root)
	// A package may carry a native library. Put it beside the
	// executable, or the program will not start on a machine that does
	// not happen to have it.
	copyDLLsBeside(out, packageDLLs(root))
}

// buildIcon is the icon --icon names, put in the next executable built.
var buildIcon []icoImage

// buildVersion is what --product, --description, --company, --copyright
// and --file-version say; what they leave out comes from the file name.
var buildVersion = versionInfo{version: [4]uint16{1, 0, 0, 0}}

// versionFor fills in buildVersion for the file being written.
func versionFor(out string, dll bool) *versionInfo {
	v := buildVersion
	v.fileName = filepath.Base(out)
	v.dll = dll
	name := strings.TrimSuffix(v.fileName, filepath.Ext(v.fileName))
	if v.product == "" {
		v.product = name
	}
	if v.description == "" {
		v.description = v.product
	}
	return &v
}

func buildExe(mod *Module, out, root string) {
	asmText := Emit(mod)
	if !envOff("SCANDIACUS_NOPEEP") && !envOff("SCANDIACUS_NOOPT") {
		asmText = peephole(asmText)
	}

	if mod.DLL && len(staticImports) > 0 {
		fail("a DLL cannot yet link a static library; the code that references " +
			"it would have to be linked into the DLL and that is not written")
	}

	if os.Getenv("SCANDIACUS_LINK") != "mingw" {
		obj, err := assembleObject(asmText, staticSymbolSet())
		if err != nil {
			fail("the generated assembly could not be encoded. This is a "+
				"compiler bug, not a mistake in your program.\n%v", err)
		}
		if err := linkStatic(obj, root, staticImports); err != nil {
			fail("%v", err)
		}
		opt := peOptions{dll: mod.DLL, name: filepath.Base(out), exports: mod.Exports, icon: buildIcon,
			version: versionFor(out, mod.DLL)}
		if err := writePE(obj, out, opt); err != nil {
			fail("the executable could not be written. This is a compiler "+
				"bug, not a mistake in your program.\n%v", err)
		}
		return
	}

	if mod.DLL {
		fail("SCANDIACUS_LINK=mingw cannot build a DLL; unset it to use the built-in linker")
	}
	buildExeWithMinGW(asmText, out, root)
}

// buildExeWithMinGW writes the assembly, then assembles and links it.
// Any static library the program named is handed to gcc, which links it
// the same way the built-in path does.
func buildExeWithMinGW(asmText string, out, root string) {
	tmp, err := os.MkdirTemp("", "scandiacus-build-*")
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
	linkArgs := []string{objPath, "-o", out}
	for _, f := range staticLibFiles() {
		if filepath.IsAbs(f) {
			linkArgs = append(linkArgs, f)
		} else {
			linkArgs = append(linkArgs, filepath.Join(root, f))
		}
	}
	if outp, err := toolchainCmd(binDir, cc, linkArgs...).CombinedOutput(); err != nil {
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
	if env := os.Getenv("SCANDIACUS_MINGW"); env != "" {
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

	fail("cannot find an assembler. scandiacus needs MinGW's `as` and `gcc`.\n" +
		"Install MSYS2 and its mingw-w64 toolchain, or set SCANDIACUS_MINGW to the\n" +
		"folder holding as.exe and gcc.exe.")
	return "", "", ""
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "scandiacus: "+format+"\n", args...)
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
