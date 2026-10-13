package main

import (
	"debug/pe"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBuildIconAndOut checks `veyl build --icon` and `-o`: the program
// still runs, lands where -o said, and carries a resource tree with an
// RT_ICON per image of the .ico and one RT_GROUP_ICON. On Windows the
// shell is asked to find the icon too.
func TestBuildIconAndOut(t *testing.T) {
	veyl := asmBackend(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "hi.vl")
	os.WriteFile(src, []byte("print(\"iconic\")\n"), 0o644)
	ico, err := filepath.Abs(filepath.Join("..", "..", "icons", "veyl.ico"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(ico)
	if err != nil {
		t.Fatal(err)
	}
	want, err := parseICO(raw)
	if err != nil {
		t.Fatalf("the repo's own icon: %v", err)
	}

	// Options after the file, and an output in a folder that does not
	// exist yet.
	exe := filepath.Join(dir, "out", "game.exe")
	cmd := exec.Command(veyl, "build", "--windows", src, "--icon", ico, "-o", exe)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "hi.exe")); err == nil {
		t.Errorf("-o was ignored: hi.exe was written beside the source")
	}

	f, err := pe.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	opt := f.OptionalHeader.(*pe.OptionalHeader64)
	dirRes := opt.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_RESOURCE]
	sec := f.Section(".rsrc")
	if sec == nil || dirRes.Size == 0 || dirRes.VirtualAddress != sec.VirtualAddress {
		t.Fatalf("no resource section in the data directory: %+v", dirRes)
	}
	data, err := sec.Data()
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	count := func(o int) int { return int(le.Uint16(data[o+12:]) + le.Uint16(data[o+14:])) }
	entry := func(o, i int) (id uint32, sub int) {
		e := o + 16 + 8*i
		return le.Uint32(data[e:]), int(le.Uint32(data[e+4:]) &^ 0x80000000)
	}
	// The icon and its group, then the version information and the
	// manifest every executable carries, in ascending order of type.
	var types []uint32
	byType := map[uint32]int{}
	for i := 0; i < count(0); i++ {
		id, sub := entry(0, i)
		types = append(types, id)
		byType[id] = sub
	}
	if fmt.Sprint(types) != fmt.Sprint([]uint32{rtIcon, rtGroupIcon, rtVersion, rtManifest}) {
		t.Fatalf("resource types %v, want icon, group, version and manifest", types)
	}
	icons, groups := byType[rtIcon], byType[rtGroupIcon]
	if count(icons) != len(want) || count(groups) != 1 {
		t.Fatalf("%d icons and %d groups, want %d and 1", count(icons), count(groups), len(want))
	}
	// The group's data, through its language directory, lists every
	// image by ID.
	_, lang := entry(groups, 0)
	_, de := entry(lang, 0)
	rva := int(le.Uint32(data[de:]))
	size := int(le.Uint32(data[de+4:]))
	grp := data[rva-int(sec.VirtualAddress) : rva-int(sec.VirtualAddress)+size]
	if int(le.Uint16(grp[4:])) != len(want) {
		t.Errorf("the group lists %d images, want %d", le.Uint16(grp[4:]), len(want))
	}
	for i := range want {
		if id := le.Uint16(grp[6+14*i+12:]); int(id) != i+1 {
			t.Errorf("group entry %d names icon %d", i, id)
		}
	}

	if runtime.GOOS == "windows" {
		out, err := exec.Command(exe).CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "iconic" {
			t.Errorf("the program with an icon: %v %q", err, out)
		}
		if n := shellIconCount(exe); n != 1 {
			t.Errorf("Windows finds %d icons in the executable, want 1", n)
		}
	}

	// Something that is not an .ico is refused, naming the file.
	cmd = exec.Command(veyl, "build", "--windows", "--icon", src, src)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "not an .ico file") {
		t.Errorf("a .vl given as the icon: %v\n%s", err, out)
	}
}
