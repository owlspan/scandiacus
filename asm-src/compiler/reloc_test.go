package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAbsoluteRelocations links an object holding a 64-bit address - a
// function pointer in .data - into a program and calls through it. The
// image moves (ASLR), so the call lands only if the address is in the
// .reloc table and the loader adjusted it.
func TestAbsoluteRelocations(t *testing.T) {
	veyl := asmBackend(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "absptr.o"), absPointerObject(), 0o644)
	os.WriteFile(filepath.Join(dir, "abs.vl"), []byte(
		"extern fn vy_call() -> int from \"absptr.o\"\nprint(vy_call())\n"), 0o644)
	exe := filepath.Join(dir, "abs.exe")
	if out, err := exec.Command(veyl, "build", "--windows", filepath.Join(dir, "abs.vl")).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	f, err := pe.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	oh := f.OptionalHeader.(*pe.OptionalHeader64)
	if oh.DllCharacteristics&0x40 == 0 {
		t.Errorf("the executable is not marked DYNAMIC_BASE")
	}
	// The table lists the .data word: a DIR64 entry (type 10) in its
	// first block. An empty table has only padding entries, type 0.
	rel, err := f.Section(".reloc").Data()
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	dir64 := 0
	size := int(binary.LittleEndian.Uint32(rel[4:]))
	for at := 8; at+2 <= size; at += 2 {
		if binary.LittleEndian.Uint16(rel[at:])>>12 == 10 {
			dir64++
		}
	}
	if dir64 != 1 {
		t.Errorf("the relocation table lists %d 64-bit addresses, want the one in .data", dir64)
	}

	if runtime.GOOS == "windows" {
		out, err := exec.Command(exe).CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "42" {
			t.Errorf("calling through the relocated pointer: %v %q", err, out)
		}
	}
}

// absPointerObject is a COFF object:
//
//	.text  vy_answer: mov eax, 42; ret
//	       vy_call:   mov rax, [rip + table]; jmp rax
//	.data  table:     dq vy_answer          (IMAGE_REL_AMD64_ADDR64)
func absPointerObject() []byte {
	le := binary.LittleEndian
	text := []byte{
		0xB8, 0x2A, 0x00, 0x00, 0x00, 0xC3, // vy_answer
		0x48, 0x8B, 0x05, 0, 0, 0, 0, // vy_call: mov rax, [rip+table]
		0xFF, 0xE0, // jmp rax
	}
	data := make([]byte, 8)

	const fileHdr, secHdr, relSize, symSize = 20, 40, 10, 18
	textAt := fileHdr + 2*secHdr
	textRelAt := textAt + len(text)
	dataAt := textRelAt + relSize
	dataRelAt := dataAt + len(data)
	symAt := dataRelAt + relSize

	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, le, v) }
	w(uint16(0x8664))
	w(uint16(2))
	w(uint32(0))
	w(uint32(symAt))
	w(uint32(5)) // symbols
	w(uint16(0))
	w(uint16(0))
	section := func(name string, size, raw, rel, nrel int, chars uint32) {
		var n [8]byte
		copy(n[:], name)
		w(n)
		w(uint32(0))
		w(uint32(0))
		w(uint32(size))
		w(uint32(raw))
		w(uint32(rel))
		w(uint32(0))
		w(uint16(nrel))
		w(uint16(0))
		w(chars)
	}
	section(".text", len(text), textAt, textRelAt, 1, 0x60500020) // code, 16-aligned, x r
	section(".data", len(data), dataAt, dataRelAt, 1, 0xC0300040) // data, 4-aligned, r w
	b.Write(text)
	w(uint32(9)) // the rip displacement in vy_call
	w(uint32(4)) // symbol 4, table
	w(uint16(4)) // IMAGE_REL_AMD64_REL32
	b.Write(data)
	w(uint32(0)) // table's one word
	w(uint32(2)) // symbol 2, vy_answer
	w(uint16(1)) // IMAGE_REL_AMD64_ADDR64
	symbol := func(name string, value uint32, sec int16, class uint8) {
		var n [8]byte
		copy(n[:], name)
		w(n)
		w(value)
		w(sec)
		w(uint16(0x20)) // a function, for the two in .text
		w(class)
		w(uint8(0))
	}
	symbol(".text", 0, 1, 3)
	symbol(".data", 0, 2, 3)
	symbol("vy_answ", 0, 1, 2) // placeholder, renamed below
	symbol("vy_call", 6, 1, 2)
	symbol("table", 0, 2, 3)
	// vy_answer is nine characters, longer than the eight a symbol holds
	// inline, so it goes in the string table and the name field says
	// where: four zero bytes, then the offset.
	strtab := append([]byte("vy_answer"), 0)
	out := b.Bytes()
	s2 := symAt + 2*symSize
	copy(out[s2:s2+8], make([]byte, 8))
	le.PutUint32(out[s2+4:], 4)
	tab := make([]byte, 4)
	le.PutUint32(tab, uint32(4+len(strtab)))
	return append(append(out, tab...), strtab...)
}
