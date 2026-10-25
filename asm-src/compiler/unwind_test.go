package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestUnwindTable builds a Windows executable and reads its exception
// directory back: a sorted, non-overlapping RUNTIME_FUNCTION per Scandiacus
// function, covering main and the program's own functions, each either
// starting on push rbp; mov rbp, rsp and sharing the one UNWIND_INFO, or
// starting on pushes and a sub rsp and having the UNWIND_INFO that
// describes them.
func TestUnwindTable(t *testing.T) {
	scandiacus := asmBackend(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "u.scd")
	prog := "fn f(n: int) -> int {\n    if n == 0 { return 0 }\n    return f(n - 1) + 1\n}\nprint(f(5))\nprint(\"x\" + str(f(2)))\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(scandiacus, "build", "--windows", src).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	f, err := pe.Open(filepath.Join(dir, "u.exe"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	oh := f.OptionalHeader.(*pe.OptionalHeader64)
	exc := oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_EXCEPTION]
	if exc.Size == 0 || exc.Size%12 != 0 {
		t.Fatalf("exception directory is %d bytes", exc.Size)
	}
	read := func(rva uint32, n int) []byte {
		for _, s := range f.Sections {
			if rva >= s.VirtualAddress && rva+uint32(n) <= s.VirtualAddress+s.VirtualSize {
				b, _ := s.Data()
				return b[rva-s.VirtualAddress : rva-s.VirtualAddress+uint32(n)]
			}
		}
		t.Fatalf("rva %#x is in no section", rva)
		return nil
	}
	table := read(exc.VirtualAddress, int(exc.Size))
	text := f.Section(".text")
	var prevEnd uint32
	for i := 0; i < len(table); i += 12 {
		begin := binary.LittleEndian.Uint32(table[i:])
		end := binary.LittleEndian.Uint32(table[i+4:])
		info := binary.LittleEndian.Uint32(table[i+8:])
		if begin < prevEnd || end <= begin {
			t.Fatalf("entry %d: [%#x, %#x) after one ending at %#x", i/12, begin, end, prevEnd)
		}
		if begin < text.VirtualAddress || end > text.VirtualAddress+text.VirtualSize {
			t.Fatalf("entry %d: [%#x, %#x) is outside .text", i/12, begin, end)
		}
		code := read(begin, int(end-begin))
		if bytes.HasPrefix(code, framePrologue) {
			if got := read(info, 8); !bytes.Equal(got, unwindInfo()) {
				t.Errorf("entry %d's UNWIND_INFO is % x", i/12, got)
			}
		} else if want := framelessInfo(code); want == nil {
			t.Errorf("entry %d starts with % x, neither frame shape", i/12, code[:4])
		} else if got := read(info, len(want)); !bytes.Equal(got, want) {
			t.Errorf("entry %d's UNWIND_INFO is % x, want % x", i/12, got, want)
		}
		if info%4 != 0 {
			t.Errorf("entry %d's UNWIND_INFO at %#x is not four-byte aligned", i/12, info)
		}
		prevEnd = end
	}

	// At least the entry, main and f.
	if n := len(table) / 12; n < 3 {
		t.Fatalf("only %d functions described", n)
	}
}

// TestFramelessInfo checks the UNWIND_INFO written for prologues with no
// frame pointer against ones worked out by hand from the x64 unwind
// format: codes newest first, each at the offset past its instruction,
// padded to an even count.
func TestFramelessInfo(t *testing.T) {
	cases := []struct {
		name string
		code []byte
		want []byte
	}{
		// sub rsp, 40
		{"sub only", []byte{0x48, 0x83, 0xEC, 0x28, 0xC3},
			[]byte{1, 4, 1, 0, 4, 2 | 4<<4, 0, 0}},
		// push rbx; push rsi; sub rsp, 72
		{"two pushes", []byte{0x53, 0x56, 0x48, 0x83, 0xEC, 0x48},
			[]byte{1, 6, 3, 0, 6, 2 | 8<<4, 2, 0 | 6<<4, 1, 0 | 3<<4, 0, 0}},
		// push r12; sub rsp, 200 (imm32 form, a UWOP_ALLOC_LARGE)
		{"large", []byte{0x41, 0x54, 0x48, 0x81, 0xEC, 200, 0, 0, 0},
			[]byte{1, 9, 3, 0, 9, 1, 25, 0, 2, 0 | 12<<4, 0, 0}},
		// push rbx; push rdi; push r15; sub rsp, 8
		{"three pushes", []byte{0x53, 0x57, 0x41, 0x57, 0x48, 0x83, 0xEC, 0x08},
			[]byte{1, 8, 4, 0, 8, 2 | 0<<4, 4, 0 | 15<<4, 2, 0 | 7<<4, 1, 0 | 3<<4}},
	}
	for _, c := range cases {
		if got := framelessInfo(c.code); !bytes.Equal(got, c.want) {
			t.Errorf("%s: % x, want % x", c.name, got, c.want)
		}
	}
	// Not prologues of that shape: an rbp frame, pushes with no sub, a
	// helper that starts with something else.
	for _, code := range [][]byte{
		{0x55, 0x48, 0x89, 0xE5},
		{0x53, 0x56, 0xC3},
		{0x48, 0x89, 0xC8, 0xC3},
	} {
		if got := framelessInfo(code); got != nil {
			t.Errorf("% x: % x, want nothing", code, got)
		}
	}
}
