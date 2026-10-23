package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestFormatPreservesBehaviour is the test that matters for a
// formatter: it must not change what a program does. Every golden
// program is copied with its folder, formatted, and run against the same
// expected output. Formatting must also reach a fixed point: a second
// pass changes nothing, or scandiacus fmt would rewrite a file forever.
func TestFormatPreservesBehaviour(t *testing.T) {
	scandiacus := asmBackend(t)
	work := t.TempDir()
	var cases []string
	for _, dir := range []string{"../tests", "../examples"} {
		dst := filepath.Join(work, filepath.Base(dir))
		if err := copyDir(dir, dst); err != nil {
			t.Fatal(err)
		}
		srcs, _ := filepath.Glob(filepath.Join(dst, "*.scd"))
		cases = append(cases, srcs...)
	}
	for _, src := range cases {
		src := src
		name := filepath.Base(src)
		if windowsOnlyProgram(name) && (targetLinux() || targetMac()) ||
			unixOnlyProgram(name) && !targetLinux() && !targetMac() || strings.HasPrefix(name, "input") {
			continue
		}
		want, err := os.ReadFile(strings.TrimSuffix(src, ".scd") + ".out")
		if err != nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			before, _ := os.ReadFile(src)
			if out, err := exec.Command(scandiacus, "fmt", src).CombinedOutput(); err != nil {
				t.Fatalf("fmt: %v\n%s", err, out)
			}
			once, _ := os.ReadFile(src)
			exec.Command(scandiacus, "fmt", src).Run()
			twice, _ := os.ReadFile(src)
			if string(once) != string(twice) {
				t.Errorf("not idempotent:\n%s", firstDiff(string(once), string(twice)))
			}
			cmd := exec.Command(scandiacus, "run", filepath.Base(src))
			cmd.Dir = filepath.Dir(src)
			got, _ := cmd.CombinedOutput()
			if strings.ReplaceAll(string(got), "\r\n", "\n") != string(want) {
				t.Errorf("formatting changed what it prints\n%s\nfirst change from formatting:\n%s",
					got, firstDiff(string(before), string(once)))
			}
		})
	}
}

func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) && i < len(bl); i++ {
		if al[i] != bl[i] {
			return "line " + strconv.Itoa(i+1) + ":\n  " + al[i] + "\n  " + bl[i]
		}
	}
	return "(lengths differ)"
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, info.Mode())
	})
}

// TestFormatCases is the layout itself, on input written badly on
// purpose: indentation, spacing, blank lines, comments, and the shapes
// whose spacing carries meaning.
func TestFormatCases(t *testing.T) {
	in := "// a comment stays\n" +
		"struct Point { x: int\n" +
		"y: int }\n" +
		"fn   add(a:int,b :int)->int{\n" +
		"return a+b   // and this one\n" +
		"}\n" +
		"\n\n\n" +
		"let p=Point{x:1,y:2}\n" +
		"let xs:[]?int=[1,nil]\n" +
		"let b=Box<int>{v:3}\n" +
		"let n=max<int>(1,2)\n" +
		"let q=&p\n" +
		"if n<2&&n>0{\n" +
		"print( add(1,-2) )\n" +
		"}\n" +
		"push(xs,thread.spawn(fn(){\n" +
		"print(\"hi\")\n" +
		"}))\n" +
		"let r=load(p)?\n"
	want := "// a comment stays\n" +
		"struct Point { x: int\n" +
		"    y: int }\n" +
		"fn add(a: int, b: int) -> int {\n" +
		"    return a + b // and this one\n" +
		"}\n" +
		"\n" +
		"let p = Point{x: 1, y: 2}\n" +
		"let xs: []?int = [1, nil]\n" +
		"let b = Box<int>{x: 3}\n" +
		"let n = max<int>(1, 2)\n" +
		"let q = &p\n" +
		"if n<2 && n>0 {\n" +
		"    print(add(1, -2))\n" +
		"}\n" +
		"push(xs, thread.spawn(fn() {\n" +
		"    print(\"hi\")\n" +
		"}))\n" +
		"let r = load(p)?\n"
	want = strings.Replace(want, "{x: 3}", "{v: 3}", 1)
	got, ok := Format("t.scd", in)
	if !ok {
		t.Fatal("did not lex")
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s\n%s", got, want, firstDiff(got, want))
	}
	if again, _ := Format("t.scd", got); again != got {
		t.Errorf("not idempotent:\n%s", firstDiff(got, again))
	}
}
