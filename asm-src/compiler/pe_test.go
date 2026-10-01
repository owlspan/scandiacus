package main

// Structural checks on the executables the PE writer produces.
//
// The differential test already proves the strong thing - that these
// binaries run and print the same bytes as the Go backend - so this is
// not about whether the output is right. It is about what a wrong header
// looks like when it happens.
//
// A malformed PE does not fail with a message. Windows refuses to start
// it and the only thing that surfaces is a status code: 0xC0000139 for a
// symbol that is not in the DLL it was asked for, 0xC000007B for a
// header the loader would not accept. Those cost real time to read the
// first time, and the differential test reports them exactly as "the
// assembly backend failed", which does not point anywhere. Checking the
// structure here means a header that drifts says which field did.

import (
	"debug/pe"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutableStructure(t *testing.T) {
	veyl := asmBackend(t)

	// dirs.vl reaches both libraries: Win32 for the file system and the
	// C runtime for everything else. One program that imports from two
	// DLLs exercises the whole import table.
	//
	// `build` writes next to the source, so the source is copied
	// somewhere disposable rather than leaving an executable in
	// examples/ for the next run to trip over.
	dir := t.TempDir()
	source, err := os.ReadFile(filepath.Join("..", "examples", "dirs.vl"))
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "dirs.vl")
	if err := os.WriteFile(src, source, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "dirs.exe")
	if o, err := exec.Command(veyl, "build", "--windows", src).CombinedOutput(); err != nil {
		t.Fatalf("could not build: %v\n%s", err, o)
	}

	f, err := pe.Open(out)
	if err != nil {
		t.Fatalf("the loader would not accept this either: %v", err)
	}
	defer f.Close()

	if f.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		t.Errorf("machine is %#x, want AMD64", f.Machine)
	}

	oh, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		t.Fatal("not a PE32+ image, so nothing 64-bit will load it")
	}
	if oh.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_CUI {
		t.Errorf("subsystem is %d, want console", oh.Subsystem)
	}
	if oh.ImageBase != imageBase {
		t.Errorf("image base is %#x, want %#x", oh.ImageBase, imageBase)
	}

	// The entry point has to land inside the code, and the image has to
	// be large enough to hold every section. Both are off-by-one
	// mistakes that produce a binary that looks fine and will not start.
	text := f.Section(".text")
	if text == nil {
		t.Fatal("no .text section")
	}
	if oh.AddressOfEntryPoint < text.VirtualAddress ||
		oh.AddressOfEntryPoint >= text.VirtualAddress+text.VirtualSize {
		t.Errorf("the entry point at %#x is not inside .text",
			oh.AddressOfEntryPoint)
	}
	for _, s := range f.Sections {
		if s.VirtualAddress+s.VirtualSize > oh.SizeOfImage {
			t.Errorf("%s runs past the end of the image", s.Name)
		}
	}

	// Every import has to be one Windows actually ships. This is the
	// check that would have caught snprintf, which msvcrt.dll does not
	// export - MinGW supplied its own, so the linked build worked and
	// the self-written one refused to start.
	// Each entry comes back as "symbol:library".
	syms, err := f.ImportedSymbols()
	if err != nil {
		t.Fatalf("the import table is unreadable: %v", err)
	}
	if len(syms) == 0 {
		t.Fatal("the import table is empty, so nothing would resolve")
	}

	seen := map[string]bool{}
	for _, s := range syms {
		i := strings.LastIndexByte(s, ':')
		if i < 0 {
			t.Fatalf("unreadable import %q", s)
		}
		name, lib := s[:i], s[i+1:]
		seen[lib] = true
		switch lib {
		case "kernel32.dll", "msvcrt.dll":
		default:
			t.Errorf("%s comes from %s, which is not a library that ships "+
				"with Windows", name, lib)
		}
		if importDLL(name) != lib {
			t.Errorf("%s was filed under %s", name, lib)
		}
	}
	if !seen["kernel32.dll"] || !seen["msvcrt.dll"] {
		t.Errorf("dirs.vl imports from %v, want both libraries", seen)
	}
}

func TestRdataMerge(t *testing.T) {
	// A four-byte instruction whose whole body is one reference into
	// .rdata, and six bytes of data. Small enough to do the address
	// arithmetic by hand in both layouts, which is the point: the merge
	// moves where every string resolves to, so a wrong base here is a
	// program reading garbage with nothing anywhere saying why.
	build := func() *object {
		return &object{
			text:  []byte{0, 0, 0, 0}, // the rel32 field itself
			rdata: []byte("hello!"),
			sym: map[string]symbol{
				"s":         {secRdata, 0},
				entrySymbol: {secText, 0},
			},
			relocs: []reloc{{at: 0, sym: "s", next: 4}},
		}
	}

	write := func(t *testing.T, obj *object) []byte {
		t.Helper()
		out := filepath.Join(t.TempDir(), "out.exe")
		if err := writePE(obj, out, peOptions{}); err != nil {
			t.Fatal(err)
		}
		bs, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if f, err := pe.Open(out); err != nil {
			t.Fatalf("the loader would not accept this either: %v", err)
		} else {
			f.Close()
		}
		return bs
	}

	// Merged. The data starts eight bytes in, after padding for
	// alignment, at RVA 0x1008; the reference points there. The switch
	// is cleared first, so the test says what it checks rather than
	// inheriting it from whatever ran it.
	t.Setenv("VEYL_NOMERGE", "")
	bs := write(t, build())
	if got := binary.LittleEndian.Uint16(bs[0x46:]); got != 2 {
		t.Fatalf("the merged image has %d sections, want text and idata", got)
	}
	if got := binary.LittleEndian.Uint32(bs[512:516]); got != 0x1008-0x1004 {
		t.Fatalf("the merged reference points at %#x, want %#x", got, 4)
	}
	if string(bs[520:526]) != "hello!" {
		t.Fatalf("the data did not land behind the code: %q", bs[520:526])
	}

	// Separate. The switch puts everything back exactly as it was:
	// three sections, the data on its own page at RVA 0x2000.
	t.Setenv("VEYL_NOMERGE", "1")
	bs = write(t, build())
	if got := binary.LittleEndian.Uint16(bs[0x46:]); got != 3 {
		t.Fatalf("the separate image has %d sections, want three", got)
	}
	if got := binary.LittleEndian.Uint32(bs[512:516]); got != 0x2000-0x1004 {
		t.Fatalf("the separate reference points at %#x, want %#x", got, 0xFFC)
	}
	if string(bs[1024:1030]) != "hello!" {
		t.Fatalf("the data did not land on its own page: %q", bs[1024:1030])
	}
}
