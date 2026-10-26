package main

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestAsmSetMatchesAssembler writes examples of every form in asmset.go
// and holds the encoder's bytes for each line to GNU as's. A form that
// fails here is one an asm statement would build differently on Windows
// than on Linux.
func TestAsmSetMatchesAssembler(t *testing.T) {
	assemble := asmTestAssembler(t)

	var lines []string
	names := make([]string, 0, len(asmSet))
	for n := range asmSet {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, f := range asmSet[n] {
			for _, size := range f.sizes {
				for k := 0; k < 4; k++ {
					line, ok := asmExample(n, f.ops, size, k)
					if ok {
						lines = append(lines, line)
					}
				}
			}
		}
	}
	// One file for all of them, a line apart by labels, so as runs once.
	var src strings.Builder
	src.WriteString(".intel_syntax noprefix\n.text\n")
	for i, ln := range lines {
		src.WriteString("Lx" + itoa(i) + ":\n" + ln + "\n")
	}
	src.WriteString("Lend:\nL1:\nnop\n")
	want := assemble(src.String())

	b := &block{at: map[string]int{}}
	for _, ln := range lines {
		if err := asmShape(ln); err != nil {
			t.Errorf("%s: the table's own example is refused: %v", ln, err)
		}
	}
	for i, ln := range lines {
		b.at["Lx"+itoa(i)] = len(b.items)
		if err := b.encodeLine(ln); err != nil {
			t.Fatalf("%v", err)
		}
	}
	b.at["Lend"] = len(b.items)
	b.at["L1"] = len(b.items)
	b.encodeLine("nop")
	b.settle()
	got := b.code
	t.Logf("%d lines, %d bytes", len(lines), len(got))
	for i, ln := range lines {
		from := b.labels["Lx"+itoa(i)]
		to := b.labels["Lend"]
		if i+1 < len(lines) {
			to = b.labels["Lx"+itoa(i+1)]
		}
		if to > len(want) || string(got[from:to]) != string(want[from:to]) {
			hi := min(to, len(want))
			t.Errorf("%s: encoded % x, the assembler says % x", ln, got[from:to], want[from:hi])
			if to > len(want) || len(got) != len(want) {
				// Everything after a wrong length is shifted; the first
				// is the one to fix.
				return
			}
		}
	}
}

// asmExample is the k-th example of a form, or false when the letters
// have nothing to show at that size.
func asmExample(mn, form string, size, k int) (string, bool) {
	if form == "" {
		return mn, true
	}
	regs := map[int][]string{
		64: {"rax", "rcx", "r9", "rbx", "r12", "rsi", "r15", "rdi"},
		32: {"eax", "ecx", "r9d", "ebx", "r12d", "esi", "r15d", "edi"},
		16: {"ax", "cx", "r9w", "bx", "r12w", "si", "r15w", "di"},
		8:  {"al", "cl", "r9b", "bl", "r12b", "sil", "r15b", "dil"},
	}
	ptr := map[int]string{8: "byte ptr ", 16: "word ptr ", 32: "dword ptr ", 64: "qword ptr "}
	mems := []string{"[rax]", "[rsp+8]", "[rbp-16]", "[r12+rcx*4+8]", "[r13]", "[rdx+1024]", "[r9+r10*8]"}
	xmms := []string{"xmm0", "xmm7", "xmm9", "xmm15", "xmm3", "xmm12"}
	hasReg := strings.Contains(","+form+",", ",r,")
	letters := strings.Split(form, ",")
	out := make([]string, len(letters))
	for i, l := range letters {
		j := k + 3*i
		switch l {
		case "r":
			out[i] = regs[size][j%len(regs[size])]
		case "m":
			m := mems[j%len(mems)]
			if !hasReg || k%2 == 1 {
				m = ptr[size] + m
			}
			out[i] = m
		case "M":
			out[i] = mems[j%len(mems)]
		case "S":
			out[i] = ptr[size] + mems[j%len(mems)]
		case "i":
			out[i] = []string{"5", "-7", "1000", "2147483647"}[k%4]
		case "I":
			out[i] = []string{"81985529216486895", "5", "-3", "4294967296"}[k%4]
		case "b":
			out[i] = []string{"3", "27", "1", "7"}[k%4]
		case "1":
			out[i] = "1"
		case "c":
			out[i] = "cl"
		case "B", "W", "D":
			w := map[string]int{"B": 8, "W": 16, "D": 32}[l]
			if k%2 == 0 {
				out[i] = regs[w][j%len(regs[w])]
			} else {
				out[i] = ptr[w] + mems[j%len(mems)]
			}
		case "x":
			out[i] = xmms[j%len(xmms)]
		case "X":
			if k%2 == 0 {
				out[i] = xmms[j%len(xmms)]
			} else {
				out[i] = mems[j%len(mems)]
			}
		case "l":
			out[i] = "L1"
		}
	}
	return mn + " " + strings.Join(out, ", "), true
}

// asmTestAssembler finds an assembler and returns a function giving the
// .text bytes it makes of a file: the MinGW one on Windows, which writes
// COFF, or the host's elsewhere, which writes ELF.
func asmTestAssembler(t *testing.T) func(string) []byte {
	if runtime.GOOS == "windows" || os.Getenv("SCANDIACUS_MINGW") != "" {
		as, _, binDir := findToolchainForTest(t)
		return func(text string) []byte {
			b, _ := assembleText(t, as, binDir, text)
			return b
		}
	}
	as, err := exec.LookPath("as")
	if err != nil {
		t.Skip("no assembler to compare against")
	}
	return func(text string) []byte {
		dir := t.TempDir()
		s, o := filepath.Join(dir, "in.s"), filepath.Join(dir, "out.o")
		if err := os.WriteFile(s, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(as, "--64", "-o", o, s).CombinedOutput(); err != nil {
			t.Fatalf("as failed: %v\n%s", err, out)
		}
		f, err := elf.Open(o)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		data, err := f.Section(".text").Data()
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
}
