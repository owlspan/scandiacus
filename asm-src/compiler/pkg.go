package main

// The package manager.
//
//	veyl get sqlite                  from the official packages
//	veyl get github.com/you/thing    from any GitHub repo
//	veyl get https://.../thing.vl    from a URL
//	veyl list
//	veyl remove sqlite
//
// Packages land in ./veyl_modules/<name>/ next to the program that uses
// them, not in a machine-wide location. A project carries its own
// dependencies, two projects can want different versions of the same
// thing, and deleting the directory uninstalls everything.
//
// A package is Veyl source, and may also carry a native DLL. That is
// the whole reason `veyl get sqlite` exists rather than the compiler
// shipping SQLite: the installer stays at 5 MB, and a program that
// never touches a database never sees the library. `veyl build` copies
// any DLL in a used package next to the executable it writes.
//
// The compiler is a Go program, so this uses Go's own HTTPS rather than
// the WinHTTP path that compiled Veyl programs use. Two implementations
// of "fetch a URL" is not duplication here - they run in different
// processes, at different times, and one of them has to work before the
// other one exists.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// modulesDir is where packages are installed, relative to the working
// directory.
const modulesDir = "veyl_modules"

// officialBase is where a bare name comes from.
//
// Its own repository rather than a directory in the compiler's, so the
// registry can be public while the compiler is not, and so a package
// can be added without touching the compiler at all.
//
// HEAD rather than a branch name, so this keeps working whatever the
// registry calls its default branch.
const officialBase = "https://raw.githubusercontent.com/owlspan/veyl-packages/HEAD/"

// manifestName lists the files a package is made of. A package without
// one is a single .vl file named after the package.
const manifestName = "veyl.pkg"

// indexName lists every package a registry holds, one name per line.
//
// A file rather than the GitHub API: the API needs a token past sixty
// requests an hour and only knows about GitHub, where this works for
// any registry reachable over HTTP.
const indexName = "veyl.index"

// pkgClient has a timeout, because a fetch that hangs forever looks
// exactly like a compiler that has crashed.
var pkgClient = &http.Client{Timeout: 30 * time.Second}

// resolveSpec turns what the user typed into a base URL and a package
// name.
func resolveSpec(spec string) (base, name string, err error) {
	switch {
	case strings.HasPrefix(spec, "http://"), strings.HasPrefix(spec, "https://"):
		if strings.HasSuffix(spec, ".vl") {
			// A direct file: its own base, named after the file.
			name = strings.TrimSuffix(filepath.Base(spec), ".vl")
			return spec, name, nil
		}
		return strings.TrimSuffix(spec, "/") + "/", filepath.Base(strings.TrimSuffix(spec, "/")), nil

	case strings.HasPrefix(spec, "github.com/"):
		spec = strings.TrimPrefix(spec, "github.com/")
		fallthrough

	case strings.Count(spec, "/") >= 1:
		parts := strings.Split(strings.Trim(spec, "/"), "/")
		if len(parts) < 2 {
			return "", "", fmt.Errorf("expected user/repo, got %q", spec)
		}
		user, repo := parts[0], parts[1]
		// HEAD rather than main or master, so this works whatever the
		// repository calls its default branch.
		b := "https://raw.githubusercontent.com/" + user + "/" + repo + "/HEAD/"
		if len(parts) > 2 {
			b += strings.Join(parts[2:], "/") + "/"
			return b, parts[len(parts)-1], nil
		}
		return b, repo, nil

	case spec == "":
		return "", "", fmt.Errorf("nothing to get")

	default:
		if strings.ContainsAny(spec, `\:`) {
			return "", "", fmt.Errorf("%q does not look like a package name or a url", spec)
		}
		return officialBase + spec + "/", spec, nil
	}
}

func fetch(url string) ([]byte, error) {
	resp, err := pkgClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: server replied %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// pkgGetAll installs every package a registry lists in its index.
//
// skipNative leaves out the ones carrying a .dll. Those are wrappers
// around a native library and are the large ones - sqlite is three
// megabytes against five kilobytes for everything else - so wanting the
// libraries without them is the common case.
//
// Whether a package is native is read from its own manifest rather than
// marked in the index. One small fetch per package, and a registry
// cannot get it wrong by forgetting to update a marker.
func pkgGetAll(base string, skipNative bool) error {
	body, err := fetch(base + indexName)
	if err != nil {
		return fmt.Errorf("this does not look like a registry: %v", err)
	}

	var names []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.ContainsAny(line, `/\:.`) {
			return fmt.Errorf("%s names something that is not a package: %q",
				indexName, line)
		}
		names = append(names, line)
	}
	if len(names) == 0 {
		return fmt.Errorf("%s lists no packages", base+indexName)
	}

	var installed, skipped, failed int
	for _, name := range names {
		if skipNative {
			native, err := hasNativeFile(base + name + "/")
			if err == nil && native {
				fmt.Printf("skipping %s (carries a native library)\n", name)
				skipped++
				continue
			}
		}
		fmt.Printf("%s:\n", name)
		if err := pkgGet(base + name + "/"); err != nil {
			// One package failing should not stop the rest. A registry
			// with a broken entry is still worth the others.
			fmt.Fprintf(os.Stderr, "  could not get %s: %v\n", name, err)
			failed++
			continue
		}
		installed++
	}

	fmt.Printf("\n%d installed", installed)
	if skipped > 0 {
		fmt.Printf(", %d skipped", skipped)
	}
	if failed > 0 {
		fmt.Printf(", %d failed", failed)
	}
	fmt.Println()
	if failed > 0 {
		return fmt.Errorf("%d package(s) could not be installed", failed)
	}
	return nil
}

// hasNativeFile reports whether a package's manifest names a .dll.
func hasNativeFile(base string) (bool, error) {
	body, err := fetch(base + manifestName)
	if err != nil {
		// No manifest means a single .vl file, which is not native.
		return false, nil
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.EqualFold(filepath.Ext(line), ".dll") {
			return true, nil
		}
	}
	return false, nil
}

// pkgGet installs one package.
func pkgGet(spec string) error {
	base, name, err := resolveSpec(spec)
	if err != nil {
		return err
	}

	var files []string
	if strings.HasSuffix(base, ".vl") {
		// A direct link to one file. The base is the file itself.
		body, err := fetch(base)
		if err != nil {
			return err
		}
		return writePackage(name, map[string][]byte{name + ".vl": body})
	}

	// A manifest if there is one, otherwise the single obvious file.
	if body, err := fetch(base + manifestName); err == nil {
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			files = append(files, line)
		}
		if len(files) == 0 {
			return fmt.Errorf("%s lists no files", base+manifestName)
		}
	} else {
		files = []string{name + ".vl"}
	}

	contents := map[string][]byte{}
	for _, f := range files {
		// A manifest naming ../ or an absolute path would write outside
		// the package directory.
		if strings.Contains(f, "..") || filepath.IsAbs(f) || strings.ContainsAny(f, `\:`) {
			return fmt.Errorf("%s names an unsafe path: %q", manifestName, f)
		}
		body, err := fetch(base + f)
		if err != nil {
			return err
		}
		contents[f] = body
	}
	return writePackage(name, contents)
}

func writePackage(name string, files map[string][]byte) error {
	dir := filepath.Join(modulesDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for f := range files {
		names = append(names, f)
	}
	sort.Strings(names)

	var total int
	for _, f := range names {
		path := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, files[f], 0o644); err != nil {
			return err
		}
		total += len(files[f])
		fmt.Printf("  %s (%s)\n", f, human(len(files[f])))
	}
	fmt.Printf("installed %s into %s (%s)\n", name, dir, human(total))
	return nil
}

func human(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

func pkgList() error {
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("no packages installed")
			return nil
		}
		return err
	}
	found := false
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		found = true
		var size int64
		var count int
		_ = filepath.Walk(filepath.Join(modulesDir, e.Name()),
			func(_ string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() {
					size += info.Size()
					count++
				}
				return nil
			})
		fmt.Printf("%-20s %d file(s), %s\n", e.Name(), count, human(int(size)))
	}
	if !found {
		fmt.Println("no packages installed")
	}
	return nil
}

func pkgRemove(name string) error {
	if name == "" || strings.ContainsAny(name, `/\:.`) {
		return fmt.Errorf("%q is not a package name", name)
	}
	dir := filepath.Join(modulesDir, name)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("%s is not installed", name)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", name)
	return nil
}

// packageDLLs lists every DLL in every installed package, so a build
// can put them beside the executable.
//
// Every installed package rather than only the imported ones: working
// out which package an import came from means threading that back from
// the resolver, and a stray DLL next to an executable is harmless where
// a missing one is a program that will not start.
func packageDLLs(root string) []string {
	var out []string
	dir := filepath.Join(root, modulesDir)
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".dll") {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// officialCarrier maps a DLL to the official package that ships it, so
// a build that cannot find one can say what would. It is a hint about
// the published catalogue rather than compiler knowledge: a package not
// listed here still works, the error just names the file instead of the
// install command.
var officialCarrier = map[string]string{
	"sqlite3.dll": "sqlite",
}

// missingForeignDLLs checks that every non-system library this program
// will import can actually be found, before anything is built.
//
// Without this the build succeeds and the executable refuses to start
// with 0xC0000135 and no message, because Windows resolves imports
// before main runs and says nothing useful about which one was
// missing. Catching it here costs a few stats and turns it into a
// sentence naming the fix.
//
// A library counts as found when it sits beside the source, in any
// installed package, in System32, or on PATH - the same places Windows
// itself looks, minus the ones that would silently load something from
// another program's directory.
func missingForeignDLLs(root string, mod *Module) error {
	var missing []string
	seen := map[string]bool{}
	for sym := range mod.Externs {
		// A symbol from a static library is not a DLL import; the library
		// file is checked separately below.
		if staticImports[sym] != "" {
			continue
		}
		dll := importDLL(sym)
		if knownSystemDLL[dll] || seen[dll] {
			continue
		}
		seen[dll] = true
		if findDLL(root, dll) != "" {
			continue
		}
		msg := fmt.Sprintf("this program needs %s, which could not be found.", dll)
		if pkg, ok := officialCarrier[dll]; ok {
			msg += fmt.Sprintf("\n        Install it next to the program with:  veyl get %s", pkg)
		} else {
			msg += "\n        Put the DLL next to the program, or install the package that ships it."
		}
		missing = append(missing, msg)
	}

	// Static libraries are files that have to exist at build time. Check
	// each named one, resolving a relative path against the source.
	for _, f := range staticLibFiles() {
		path := f
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, f)
		}
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, fmt.Sprintf("this program links %s, which could not be found.\n"+
				"        Put the static library next to the program, or give its full path in the from clause.", f))
		}
	}

	sort.Strings(missing)
	if len(missing) == 0 {
		return nil
	}
	return errors.New(strings.Join(missing, "\n"))
}

// findDLL reports where a named library would be loaded from, or "".
func findDLL(root, dll string) string {
	dirs := []string{root}
	pkgs := filepath.Join(root, modulesDir)
	entries, err := os.ReadDir(pkgs)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(pkgs, e.Name()))
			}
		}
	}
	if sys := os.Getenv("SystemRoot"); sys != "" {
		dirs = append(dirs, filepath.Join(sys, "System32"))
	}
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p != "" {
			dirs = append(dirs, p)
		}
	}
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, dll)); err == nil {
			return d
		}
	}
	return ""
}

// copyDLLsBeside puts each one next to the executable, unless a file of
// that name is already there and identical in size.
func copyDLLsBeside(exePath string, dlls []string) {
	destDir := filepath.Dir(exePath)
	for _, src := range dlls {
		dest := filepath.Join(destDir, filepath.Base(src))
		if s, err := os.Stat(src); err == nil {
			if d, err := os.Stat(dest); err == nil && d.Size() == s.Size() {
				continue
			}
		}
		body, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		if err := os.WriteFile(dest, body, 0o644); err == nil {
			fmt.Printf("copied %s\n", filepath.Base(src))
		}
	}
}
