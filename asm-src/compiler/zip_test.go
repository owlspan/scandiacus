package main

import (
	"archive/zip"
	"bytes"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestZip checks zip.make, zip.list and zip.extract against Go's own
// archive/zip in both directions: what veyl writes, Go reads back byte
// for byte, and what Go writes (dynamic Huffman, the usual kind), veyl
// unpacks. An entry climbing out of the destination is refused before
// anything is written.
func TestZip(t *testing.T) {
	veyl := asmBackend(t)
	dir := t.TempDir()
	r := rand.New(rand.NewSource(1))
	random := make([]byte, 6000)
	r.Read(random)
	files := map[string][]byte{
		"notes.txt":          []byte(strings.Repeat("hello zip\n", 3)),
		"src/code.vl":        []byte(strings.Repeat("let x = 1\nprint(x + 2)\n", 900)),
		"src/sub/binary.bin": append(random, 0, 0, 0, 1),
		"src/sub/zeros.bin":  make([]byte, 70000),
	}
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, data, 0o644)
	}
	os.MkdirAll(filepath.Join(dir, "src", "empty"), 0o755)

	run := func(src string) string {
		t.Helper()
		os.WriteFile(filepath.Join(dir, "z.vl"), []byte(src), 0o644)
		cmd := exec.Command(veyl, "run", "z.vl")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return strings.ReplaceAll(string(out), "\r\n", "\n")
	}

	got := run(`must(zip.make("out.zip", ["notes.txt", "src"]))
print(join(must(zip.list("out.zip")), " "))
`)
	want := "notes.txt src/ src/code.vl src/empty/ src/sub/ src/sub/binary.bin src/sub/zeros.bin\n"
	if got != want {
		t.Errorf("zip.list: got %q, want %q", got, want)
	}

	// Go reads veyl's archive.
	zr, err := zip.OpenReader(filepath.Join(dir, "out.zip"))
	if err != nil {
		t.Fatalf("Go cannot open the archive: %v", err)
	}
	compressed := 0
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc) // checks the CRC too
		rc.Close()
		if err != nil || !bytes.Equal(data, files[f.Name]) {
			t.Errorf("%s does not read back from veyl's archive: %v", f.Name, err)
		}
		if f.Method == zip.Deflate {
			compressed++
		}
	}
	zr.Close()
	if compressed < 3 {
		t.Errorf("only %d entries were compressed", compressed)
	}

	// Veyl reads Go's archive.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, _ := zw.Create("go/" + name)
		w.Write(data)
	}
	zw.Close()
	os.WriteFile(filepath.Join(dir, "go.zip"), buf.Bytes(), 0o644)
	got = run(`print(must(zip.extract("go.zip", "fromgo")))
`)
	if got != "4\n" {
		t.Errorf("zip.extract of Go's archive: %q", got)
	}
	for name, data := range files {
		back, err := os.ReadFile(filepath.Join(dir, "fromgo", "go", filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(back, data) {
			t.Errorf("%s from Go's archive: %v", name, err)
		}
	}

	// Escaping the destination is refused, and nothing is written.
	buf.Reset()
	zw = zip.NewWriter(&buf)
	w, _ := zw.Create("fine.txt")
	w.Write([]byte("ok"))
	w, _ = zw.Create("../../escaped.txt")
	w.Write([]byte("bad"))
	zw.Close()
	os.WriteFile(filepath.Join(dir, "evil.zip"), buf.Bytes(), 0o644)
	got = run(`print(errorOf(zip.extract("evil.zip", "evil")))
print(os.file.exists("evil/fine.txt"))
print(errorOf(zip.list("notes.txt")))
`)
	want = "archive contains an unsafe path: ../../escaped.txt\nfalse\nnotes.txt is not a zip archive\n"
	if got != want {
		t.Errorf("unsafe archive: got %q, want %q", got, want)
	}
}
