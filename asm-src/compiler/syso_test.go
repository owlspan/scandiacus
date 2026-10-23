package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// The icon scandiacus.exe itself shows in Explorer. Go links any .syso in the
// package into a Windows build, so rsrc_windows_amd64.syso is a COFF
// object holding a .rsrc section made by buildResources from
// icons/scandiacus.ico. This test fails when the two drift apart;
// SCANDIACUS_REGEN=1 rewrites the object.
const sysoName = "rsrc_windows_amd64.syso"

func TestIconSysoUpToDate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "icons", "scandiacus.ico"))
	if err != nil {
		t.Fatal(err)
	}
	imgs, err := parseICO(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := resourceObject(imgs)
	if os.Getenv("SCANDIACUS_REGEN") == "1" {
		if err := os.WriteFile(sysoName, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(sysoName)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s does not match icons/scandiacus.ico; run the test with SCANDIACUS_REGEN=1", sysoName)
	}
}

// resourceObject wraps the resource tree in a one-section COFF object.
// The tree is laid out at offset 0, and each data entry's offset gets an
// ADDR32NB relocation against the section, which the linker turns into
// the RVA it ends up at.
func resourceObject(imgs []icoImage) []byte {
	le := binary.LittleEndian
	tree := buildResources(imgs, 0)
	// The data entries are the 16-byte records whose first word the
	// relocations fix; buildResources puts n+1 of them just before the
	// bytes they describe. Find them by walking the language directories
	// the same way the test in icon_test.go does.
	var fields []int
	var walk func(o, depth int)
	walk = func(o, depth int) {
		n := int(le.Uint16(tree[o+12:]) + le.Uint16(tree[o+14:]))
		for i := 0; i < n; i++ {
			v := le.Uint32(tree[o+16+8*i+4:])
			if v&0x80000000 != 0 {
				walk(int(v&^0x80000000), depth+1)
			} else {
				fields = append(fields, int(v))
			}
		}
	}
	walk(0, 0)

	const fileHeader, sectionHeader = 20, 40
	rawAt := fileHeader + sectionHeader
	relocAt := rawAt + len(tree)
	symAt := relocAt + 10*len(fields)

	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, le, v) }
	w(uint16(0x8664)) // AMD64
	w(uint16(1))      // sections
	w(uint32(0))      // timestamp, 0 so the file is reproducible
	w(uint32(symAt))
	w(uint32(1)) // symbols
	w(uint16(0)) // no optional header
	w(uint16(0))

	name := [8]byte{'.', 'r', 's', 'r', 'c'}
	w(name)
	w(uint32(0)) // virtual size, unused in an object
	w(uint32(0))
	w(uint32(len(tree)))
	w(uint32(rawAt))
	w(uint32(relocAt))
	w(uint32(0)) // no line numbers
	w(uint16(len(fields)))
	w(uint16(0))
	w(uint32(0x40000040)) // initialised data, readable

	b.Write(tree)
	for _, f := range fields {
		w(uint32(f)) // where the field is
		w(uint32(0)) // symbol 0, the section
		w(uint16(3)) // IMAGE_REL_AMD64_ADDR32NB
	}
	w(name)
	w(uint32(0)) // value
	w(uint16(1)) // section 1
	w(uint16(0)) // type
	w(uint8(3))  // IMAGE_SYM_CLASS_STATIC
	w(uint8(0))  // no aux records
	w(uint32(4)) // empty string table
	return b.Bytes()
}
