package main

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEncoderMatchesAssembler is the whole argument that the byte
// encoder is correct.
//
// For every example, the same assembly text goes to GNU as and to the
// encoder here, and the machine code has to come out identical. There is
// no reasoning to check and no table to trust: the assembler is the
// definition, exactly as the Go backend is the definition of what Scandiacus
// means.
//
// Only the bytes are compared, not the relocations. An instruction that
// refers to a symbol is encoded with a zero in the field either way, and
// filling those in is the linker's job today and the PE writer's job
// later.
func TestEncoderMatchesAssembler(t *testing.T) {
	as, _, binDir := findToolchainForTest(t)

	programs, err := filepath.Glob(filepath.Join("..", "examples", "*.scd"))
	if err != nil || len(programs) == 0 {
		t.Fatalf("no examples to encode: %v", err)
	}

	scandiacus := asmBackend(t)

	for _, src := range programs {
		src := src
		t.Run(filepath.Base(src), func(t *testing.T) {
			t.Parallel()

			out, err := exec.Command(scandiacus, "asm", src).Output()
			if err != nil {
				t.Fatalf("could not produce assembly: %v", err)
			}
			text := string(out)

			want, spots := assembleText(t, as, binDir, text)
			got := encodeText(t, text)

			// A field the assembler left for the linker holds an addend
			// in its own convention, and this encoder holds a zero,
			// because the two do not link the same way. Those four bytes
			// are not a difference in the instruction, so both sides are
			// blanked wherever the assembler recorded a relocation.
			for _, at := range spots {
				blank(got, at)
				blank(want, at)
			}

			if len(got) > len(want) {
				t.Fatalf("encoded %d bytes, the assembler produced %d",
					len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("byte %d differs: got %#02x, the assembler says %#02x\n%s",
						i, got[i], want[i], firstDifference(got, want))
				}
			}

			// The assembler pads the section out to an alignment
			// boundary. That padding is not code and is not compared,
			// but every byte of it has to be part of a nop.
			for i := len(got); i < len(want); i++ {
				switch want[i] {
				case 0x90, 0x00, 0x66, 0x0f, 0x1f, 0x44, 0x2e, 0x84, 0x40:
				default:
					t.Fatalf("the assembler produced %d extra bytes and byte %d "+
						"is %#02x, which is not padding", len(want)-len(got), i, want[i])
				}
			}
		})
	}
}

// encodeText runs the encoder over the .text portion of an assembly
// file, which is everything the section directives say is code.
func encodeText(t *testing.T, text string) []byte {
	t.Helper()
	b := &block{at: map[string]int{}}
	inText := true
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, ".section") || trimmed == ".text" {
			inText = strings.Contains(trimmed, ".text")
			continue
		}
		if !inText {
			continue
		}
		if err := b.encodeLine(line); err != nil {
			t.Fatalf("%v", err)
		}
	}
	b.settle()
	return b.code
}

// assembleText runs GNU as and reads the .text section out of the object
// it produces.
func blank(b []byte, at int) {
	for i := at; i < at+4 && i < len(b); i++ {
		b[i] = 0
	}
}

func assembleText(t *testing.T, as, binDir, text string) ([]byte, []int) {
	t.Helper()
	dir := t.TempDir()
	sfile := filepath.Join(dir, "in.s")
	ofile := filepath.Join(dir, "out.o")
	if err := os.WriteFile(sfile, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(as, "-o", ofile, sfile)
	cmd.Env = append(os.Environ(), "PATH="+binDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("as failed: %v\n%s", err, out)
	}

	obj, err := os.ReadFile(ofile)
	if err != nil {
		t.Fatal(err)
	}
	return coffSection(t, obj, ".text")
}

// coffSection pulls one section's raw bytes out of a COFF object.
//
// The header is twenty bytes, then one forty-byte entry per section. A
// section name of eight bytes or fewer is stored inline, which .text is.
func coffSection(t *testing.T, obj []byte, want string) ([]byte, []int) {
	t.Helper()
	if len(obj) < 20 {
		t.Fatal("object file is too short to be COFF")
	}
	nsections := int(binary.LittleEndian.Uint16(obj[2:4]))
	optSize := int(binary.LittleEndian.Uint16(obj[16:18]))
	at := 20 + optSize

	for i := 0; i < nsections; i++ {
		e := obj[at+i*40 : at+(i+1)*40]
		name := strings.TrimRight(string(e[0:8]), "\x00")
		if name != want {
			continue
		}
		size := int(binary.LittleEndian.Uint32(e[16:20]))
		off := int(binary.LittleEndian.Uint32(e[20:24]))

		// Each relocation is ten bytes: the address it patches, the
		// symbol, and the kind. Only the address matters here.
		relOff := int(binary.LittleEndian.Uint32(e[24:28]))
		nrel := int(binary.LittleEndian.Uint16(e[32:34]))
		spots := make([]int, 0, nrel)
		for r := 0; r < nrel; r++ {
			at := relOff + r*10
			spots = append(spots, int(binary.LittleEndian.Uint32(obj[at:at+4])))
		}
		return obj[off : off+size], spots
	}
	t.Fatalf("no %s section in the object", want)
	return nil, nil
}

// firstDifference reports where two encodings diverge, with a little
// context, because a raw byte index is not much help on its own.
func firstDifference(got, want []byte) string {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	for i := 0; i < n; i++ {
		if got[i] != want[i] {
			from := i - 8
			if from < 0 {
				from = 0
			}
			return "around byte " + itoa(i) + ":\n  ours: " + hex(got, from, i+8) +
				"\n  as:   " + hex(want, from, i+8)
		}
	}
	return "the shorter encoding is a prefix of the longer one"
}

func hex(b []byte, from, to int) string {
	if to > len(b) {
		to = len(b)
	}
	var sb strings.Builder
	for i := from; i < to; i++ {
		sb.WriteString(hexByte(b[i]))
		sb.WriteByte(' ')
	}
	return sb.String()
}

func hexByte(b byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[b>>4], digits[b&15]})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func findToolchainForTest(t *testing.T) (as, cc, binDir string) {
	t.Helper()
	if env := os.Getenv("SCANDIACUS_MINGW"); env != "" {
		return filepath.Join(env, "as.exe"), filepath.Join(env, "gcc.exe"), env
	}
	// Off Windows the host `as` writes ELF, which is not what this
	// compares against, so only the MinGW cross assembler will do.
	if runtime.GOOS != "windows" {
		if a, err := exec.LookPath("x86_64-w64-mingw32-as"); err == nil {
			return a, "", filepath.Dir(a)
		}
		t.Skip("no x86_64-w64-mingw32-as to compare against")
	}
	if a, err := exec.LookPath("as"); err == nil {
		if c, err := exec.LookPath("gcc"); err == nil {
			return a, c, filepath.Dir(c)
		}
	}
	for _, dir := range []string{
		`C:\msys64\mingw64\bin`, `C:\msys64\ucrt64\bin`,
		`C:\mingw64\bin`, `C:\MinGW\bin`,
	} {
		a := filepath.Join(dir, "as.exe")
		c := filepath.Join(dir, "gcc.exe")
		if _, err := os.Stat(a); err == nil {
			if _, err := os.Stat(c); err == nil {
				return a, c, dir
			}
		}
	}
	t.Skip("no assembler to compare against")
	return "", "", ""
}
