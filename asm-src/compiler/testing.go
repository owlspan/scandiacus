package main

// scandiacus test: tests written in Scandiacus.
//
//	test "adds" {
//	    expect(add(1, 2) == 3)
//	    expectEq(add(2, 2), 4)
//	}
//
// A test block is a function with no parameters, which only a test build
// compiles. That build replaces the program body with a harness that
// runs the one test named by its first argument and exits 1 if any
// expectation failed, and scandiacus test runs the executable once per test. A
// test that crashes, or stops on a runtime error, is then one failed
// test rather than the end of the run, and each is timed on its own.
//
// expect and expectEq do not stop a test: each failure is written to
// stderr, with the line, and the test goes on, so one run reports every
// expectation that is wrong. expectEq evaluates each side once and says
// what both were.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// gcTestSlot is the runtime word set when an expectation fails.
const gcTestSlot = 14

// testMode makes compile build the test blocks and the harness, and
// testNames is what it found, in order.
var (
	testMode    bool
	testNames   []string
	testBenches []bool
)

func compileTests(src, path string) (*Module, []string, []bool) {
	mod := compile(src, path)
	return mod, testNames, testBenches
}

// benchLine turns the harness's "__scandiacus_bench N NANOS" into what is shown.
func benchLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == "__scandiacus_bench" {
			n, _ := strconv.ParseFloat(f[1], 64)
			ns, _ := strconv.ParseFloat(f[2], 64)
			per := ns / n
			unit := "ns"
			switch {
			case per >= 1e9:
				per, unit = per/1e9, "s"
			case per >= 1e6:
				per, unit = per/1e6, "ms"
			case per >= 1e3:
				per, unit = per/1e3, "us"
			}
			return fmt.Sprintf("%s runs, %.3g %s each", f[1], per, unit)
		}
	}
	return "(no timing printed)"
}

func (l *lowerer) testBuiltin(c *Call, name string) (Reg, bool) {
	switch name {
	case "expect", "expectEq":
	case "__testFailed":
		return l.rtLoad(gcTestSlot), true
	default:
		return NoReg, false
	}
	where := fmt.Sprintf("%s:%d", filepath.Base(l.curFile), l.curLine)
	if name == "expect" {
		if len(c.Args) != 1 {
			l.errorAt(c, "expect takes 1 argument, got %d", len(c.Args))
			return l.junk(), true
		}
		ok := l.expr(c.Args[0])
		done := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: ok, Dst: NoReg, Imm: done})
		l.testFail(l.strLit(where + ": expect failed\n"))
		l.mark(done)
		return l.void(), true
	}

	if len(c.Args) != 2 {
		l.errorAt(c, "expectEq takes 2 arguments, got %d", len(c.Args))
		return l.junk(), true
	}
	got := l.rvalue(c.Args[0])
	t := l.regTy[got]
	l.hint = t
	want := l.rvalue(c.Args[1])
	l.hint = vVoid
	if wt := l.regTy[want]; t.k == kFloat && wt.k == kInt && !wt.res && !wt.null {
		want = l.convert(want, vFloat) // expectEq(f, 2)
	}
	gotSlot := l.temp(t)
	l.emit(Instr{Op: OpStore, A: got, Dst: NoReg, Imm: gotSlot})
	wantSlot := l.temp(t)
	l.emit(Instr{Op: OpStore, A: want, Dst: NoReg, Imm: wantSlot})
	same, ok := l.deepEqual(c, l.load(gotSlot, t), l.load(wantSlot, t), t)
	if !ok {
		return l.junk(), true
	}
	done := l.newLabel()
	l.emit(Instr{Op: OpJumpIf, A: same, Dst: NoReg, Imm: done})
	msg := l.concatAll(l.strLit(where+": got "), l.strOf(c, l.load(gotSlot, t), t),
		l.strLit(", want "), l.strOf(c, l.load(wantSlot, t), t), l.strLit("\n"))
	l.testFail(msg)
	l.mark(done)
	return l.void(), true
}

// testFail writes a failure to stderr and marks the test failed.
func (l *lowerer) testFail(msg Reg) {
	l.ccall("_write", []Reg{l.constant(2), msg, l.strLen(msg)}, []vty{vInt, vInt, vInt}, vInt, true, false)
	l.rtStore(gcTestSlot, l.constant(1))
}

// testHarness is the program body of a test build: run the test whose
// number is the first argument, then exit 1 if it failed.
//
// A bench runs its body 1, 2, 4... times until one round takes half a
// second, and prints the count and the time that round took.
func testHarness(tests []*FnDecl) string {
	var b strings.Builder
	b.WriteString("let __args = os.args()\nlet __which = -1\nif len(__args) > 0 {\n    __which = toInt(__args[0])\n}\n")
	for i, t := range tests {
		if !t.Bench {
			fmt.Fprintf(&b, "if __which == %d {\n    __test_%d()\n}\n", i, i)
			continue
		}
		fmt.Fprintf(&b, `if __which == %d {
    let __n = 1
    while true {
        let __t0 = time.nanos()
        let __k = 0
        while __k < __n {
            __test_%d()
            __k += 1
        }
        let __dt = time.nanos() - __t0
        if __dt >= 500000000 || __n >= 1000000000 {
            print("__scandiacus_bench {__n} {__dt}")
            break
        }
        __n = __n * 2
    }
}
`, i, i)
	}
	b.WriteString("exit(__testFailed())\n")
	return b.String()
}

type testResult struct {
	name   string
	ok     bool
	took   time.Duration
	output string
	bench  string // for a bench: how many runs, and the time per run
}

// testCommand is scandiacus test: every test in the files named, or in each
// .scd under a folder named.
func testCommand(args []string) {
	filter := ""
	bench := false
	var paths []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--bench" {
			bench = true
			continue
		}
		if args[i] == "--run" && i+1 < len(args) {
			filter = args[i+1]
			i++
			continue
		}
		paths = append(paths, args[i])
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			fail("%v", err)
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		filepath.Walk(p, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if fi.IsDir() && (fi.Name() == "scandiacus_modules" || strings.HasPrefix(fi.Name(), ".") && path != p) {
				return filepath.SkipDir
			}
			if !fi.IsDir() && strings.HasSuffix(fi.Name(), ".scd") {
				if b, err := os.ReadFile(path); err == nil && bytes.Contains(b, []byte("test \"")) {
					files = append(files, path)
				}
			}
			return nil
		})
	}

	passed, failed := 0, 0
	for _, file := range files {
		results := runFileTests(file, filter, bench)
		for _, r := range results {
			if r.ok && r.bench != "" {
				passed++
				fmt.Printf("BENCH %s: %s  %s\n", filepath.Base(file), r.name, r.bench)
				continue
			}
			status := "PASS"
			if !r.ok {
				status = "FAIL"
				failed++
			} else {
				passed++
			}
			fmt.Printf("%s  %s: %s (%.2fs)\n", status, filepath.Base(file), r.name, r.took.Seconds())
			if !r.ok {
				for _, line := range strings.Split(strings.TrimRight(r.output, "\n"), "\n") {
					if line != "" {
						fmt.Printf("    %s\n", line)
					}
				}
			}
		}
	}
	if passed+failed == 0 {
		fmt.Println("no tests found")
		return
	}
	if bench {
		fmt.Printf("%d benchmarks, %d failed\n", passed, failed)
	} else {
		fmt.Printf("%d passed, %d failed\n", passed, failed)
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// runFileTests builds one file's tests into an executable and runs each.
func runFileTests(file, filter string, bench bool) []testResult {
	src, err := os.ReadFile(file)
	if err != nil {
		fail("%v", err)
	}
	abs, _ := filepath.Abs(file)
	testMode = true
	mod, names, benches := compileTests(string(src), abs)
	testMode = false
	if len(names) == 0 {
		return nil
	}

	tmp, err := os.MkdirTemp("", "scandiacus-test-*")
	if err != nil {
		fail("%v", err)
	}
	defer os.RemoveAll(tmp)
	exe := filepath.Join(tmp, "tests")
	if !targetLinux() && !targetMac() {
		exe += ".exe"
	}
	writeExecutable(mod, exe, filepath.Dir(abs))

	var out []testResult
	for i, name := range names {
		if filter != "" && !strings.Contains(name, filter) || benches[i] != bench {
			continue
		}
		start := time.Now()
		cmd := exec.Command(exe, strconv.Itoa(i))
		if !targetLinux() && !targetMac() && runtime.GOOS != "windows" {
			// A Windows build tested from Linux, as the test suite does.
			cmd = exec.Command("wine", exe, strconv.Itoa(i))
		}
		cmd.Dir = filepath.Dir(abs)
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		r := testResult{name: name, ok: err == nil, took: time.Since(start),
			output: strings.ReplaceAll(buf.String(), "\r\n", "\n")}
		if bench && r.ok {
			r.bench = benchLine(r.output)
		}
		out = append(out, r)
	}
	return out
}
