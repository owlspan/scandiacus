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
// directory back: a sorted, non-overlapping RUNTIME_FUNCTION per Veyl
// function, each starting on push rbp; mov rbp, rsp, covering main and
// the program's own functions, all sharing one well-formed UNWIND_INFO.
func TestUnwindTable(t *testing.T) {
	veyl := asmBackend(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "u.vl")
	prog := "fn f(n: int) -> int {\n    if n == 0 { return 0 }\n    return f(n - 1) + 1\n}\nprint(f(5))\nprint(\"x\" + str(f(2)))\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(veyl, "build", "--windows", src).CombinedOutput(); err != nil {
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
		if got := read(begin, 4); !bytes.Equal(got, framePrologue) {
			t.Errorf("entry %d starts with % x, not push rbp; mov rbp, rsp", i/12, got)
		}
		if got := read(info, 8); !bytes.Equal(got, unwindInfo()) {
			t.Errorf("entry %d's UNWIND_INFO is % x", i/12, got)
		}
		prevEnd = end
	}

	// At least the entry, main and f.
	if n := len(table) / 12; n < 3 {
		t.Fatalf("only %d functions described", n)
	}
}
