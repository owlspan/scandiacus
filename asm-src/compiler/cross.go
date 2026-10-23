package main

// Cross compiling: building for Linux and macOS from any system.
//
// A Windows executable needs nothing outside the compiler - it has its
// own encoder, linker and PE writer - so --windows works from Linux and
// macOS as it is. A Linux or macOS executable is linked by a C toolchain
// for that system, which the system itself has and the others do not.
// Zig carries one for every target in a single download (clang, lld, and
// the libc headers and stubs of glibc and macOS), so it is what scandiacus
// uses when the target is not the system it runs on. `scandiacus cross install`
// fetches a pinned release of it into the cache directory, checked
// against a pinned hash; the Windows installer offers to do that as its
// "Cross compiling" component.
//
// Where zig is looked for, first match wins: SCANDIACUS_ZIG (the zig program),
// a zig folder beside the scandiacus executable, the one `scandiacus cross install`
// put in the cache, and zig on PATH.

import (
	"archive/zip"
	"crypto/sha256"
	enchex "encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const zigVersion = "0.13.0"

// zigWheels are the zig releases as the ziglang Python wheels, which are
// zip files on every system - Go reads zip, not the .tar.xz the other
// downloads come in. Keyed by GOOS/GOARCH of the system scandiacus runs on.
var zigWheels = map[string]struct{ url, sha256 string }{
	"windows/amd64": {"https://files.pythonhosted.org/packages/e0/0c/66f4710aedf5544e15779ff9c6e770322986e15daf9299b1a4df0be19e09/ziglang-0.13.0-py3-none-win_amd64.whl",
		"b64b2aa936ebbfeecfbb2611df331a5474edebe1246b98c2f56d6ae4d58b71fc"},
	"linux/amd64": {"https://files.pythonhosted.org/packages/0c/b7/ecfdce6368cc3664d301f7f52db4fe1004aa7da7a12c4a9bf1de534ff6ab/ziglang-0.13.0-py3-none-manylinux_2_12_x86_64.manylinux2010_x86_64.musllinux_1_1_x86_64.whl",
		"3ce0c9f16547e5d61b32e0d226926e9a2552ef4b91fccf7ab5ea1a623a77824b"},
	"darwin/amd64": {"https://files.pythonhosted.org/packages/4b/38/4f2ef2b1d727d7317b08ef309e52d8c90089272b57d5d940c74524cc4315/ziglang-0.13.0-py3-none-macosx_12_0_x86_64.whl",
		"d1b00173b8a5bfbaccb255240b8c091bd523c13743178bc248355ec6484fced7"},
	"darwin/arm64": {"https://files.pythonhosted.org/packages/e6/14/3587387c715175ed5bb103c94c0df554fabac5bc97de153e9eec2fa905aa/ziglang-0.13.0-py3-none-macosx_12_0_arm64.whl",
		"5408ad3a6cb342fb4e4e2351a3725adfcae82eaadebe85fdbbdad9c0ac8b1e24"},
}

// The targets handed to zig cc. glibc 2.28 is the oldest the runtime is
// built against, so the result runs on any Linux from 2018 on.
const (
	zigLinuxTarget = "x86_64-linux-gnu.2.28"
	zigMacTarget   = "x86_64-macos"
)

func zigExe() string {
	if runtime.GOOS == "windows" {
		return "zig.exe"
	}
	return "zig"
}

// zigCacheDir is where `scandiacus cross install` puts zig by default.
func zigCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "scandiacus", "zig-"+zigVersion)
}

// findZig is the zig program to cross compile with, or "".
func findZig() string {
	if env := os.Getenv("SCANDIACUS_ZIG"); env != "" {
		return env
	}
	var dirs []string
	if self, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(self), "zig"))
	}
	dirs = append(dirs, zigCacheDir())
	for _, d := range dirs {
		p := filepath.Join(d, zigExe())
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("zig"); err == nil {
		return p
	}
	return ""
}

// crossCC is the argv of a C compiler for the target through zig, or
// a failure that says how to get one.
func crossCC(zigTarget, system string) []string {
	z := findZig()
	if z == "" {
		fail("building for %s from %s needs the cross toolchain, which is not installed.\n"+
			"        Run `scandiacus cross install` once (about 80 MB), then build again.",
			system, hostName())
	}
	return []string{z, "cc", "-target", zigTarget}
}

func hostName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	}
	return "Windows"
}

// crossCommand is `scandiacus cross`.
func crossCommand(args []string) {
	if len(args) == 0 || args[0] == "status" {
		if z := findZig(); z != "" {
			fmt.Printf("cross compiling is set up: %s\n", z)
		} else {
			fmt.Println("cross compiling is not set up; run `scandiacus cross install`")
		}
		return
	}
	if args[0] != "install" {
		fail("unknown cross command %q - it takes install or status", args[0])
	}
	dir := zigCacheDir()
	for i := 1; i < len(args); i++ {
		if args[i] == "--dir" && i+1 < len(args) {
			dir = args[i+1]
			i++
		} else {
			fail("unknown option %q - cross install takes --dir DIR", args[i])
		}
	}
	if err := installZig(dir); err != nil {
		fail("cross install: %v", err)
	}
	fmt.Printf("installed zig %s in %s\n", zigVersion, dir)
}

// installZig downloads the pinned zig for this system, checks its hash,
// and unpacks it into dir.
func installZig(dir string) error {
	key := runtime.GOOS + "/" + runtime.GOARCH
	w, ok := zigWheels[key]
	if !ok {
		return fmt.Errorf("no zig download is known for %s", key)
	}
	if _, err := os.Stat(filepath.Join(dir, zigExe())); err == nil {
		return nil // already there
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dir), "zig-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	fmt.Printf("downloading zig %s (about 80 MB)...\n", zigVersion)
	resp, err := http.Get(w.url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), resp.Body)
	if err != nil {
		return err
	}
	if got := enchex.EncodeToString(h.Sum(nil)); got != w.sha256 {
		return fmt.Errorf("the download does not match its pinned hash (got %s)", got)
	}

	zr, err := zip.NewReader(tmp, n)
	if err != nil {
		return err
	}
	// Unpacked beside the final place and renamed into it, so a build
	// never sees half an install.
	part := dir + ".part"
	os.RemoveAll(part)
	for _, f := range zr.File {
		rel, ok := strings.CutPrefix(f.Name, "ziglang/")
		if !ok || rel == "" || strings.HasSuffix(rel, ".py") || strings.HasSuffix(f.Name, "/") {
			continue
		}
		if strings.Contains(rel, "..") {
			return fmt.Errorf("bad path in the archive: %s", f.Name)
		}
		dst := filepath.Join(part, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if rel == zigExe() || f.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if err := extractFile(f, dst, mode); err != nil {
			return err
		}
	}
	os.RemoveAll(dir)
	return os.Rename(part, dir)
}

func extractFile(f *zip.File, dst string, mode os.FileMode) error {
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// buildLinuxCross is buildLinux from another system: the same assembly
// and runtime, put together by zig. The symbol renames objcopy does
// natively are done on the assembly text, and sqlite is loaded at run
// time, since there is no libsqlite3 to link against here.
func buildLinuxCross(asmText string, externs []string, wrapperC string, wrapperLibs []string, out string) {
	cc := crossCC(zigLinuxTarget, "Linux")
	tmp, err := os.MkdirTemp("", "scandiacus-cross-*")
	if err != nil {
		fail("%v", err)
	}
	defer os.RemoveAll(tmp)

	asmText = transformCrossAsm(asmText, externs, false)
	asmText += "\n    .section .note.GNU-stack,\"\",@progbits\n"
	write := func(name, text string) string {
		p := filepath.Join(tmp, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			fail("%v", err)
		}
		return p
	}
	args := append([]string{}, cc[1:]...)
	args = append(args, "-O2", "-fno-PIE", "-no-pie", "-o", out, write("prog.s", asmText))
	write("font8x8.h", linuxFontHeader)
	for _, name := range runtimeSourceNames() {
		args = append(args, write(name, linuxRuntimeSources[name]))
	}
	if wrapperC != "" {
		args = append(args, write("wrappers.c", wrapperC))
	}
	if needsSqlite(externs) {
		src := strings.ReplaceAll(macSqliteSource, "/usr/lib/libsqlite3.dylib", "libsqlite3.so")
		src = strings.ReplaceAll(src, "libsqlite3.dylib", "libsqlite3.so.0")
		args = append(args, write("scandiacussqlite.c", src))
	}
	args = append(args, "-lm", "-lpthread", "-ldl")
	args = append(args, wrapperLibs...)
	if outp, err := exec.Command(cc[0], args...).CombinedOutput(); err != nil {
		fail("linking for Linux failed.\n%s\n%s", err, outp)
	}
}
