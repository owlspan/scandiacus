package main

// veyl vscode: put the Veyl extension into Visual Studio Code.
//
// The installer used to copy the extension folder into
// ~/.vscode/extensions and leave it there. VS Code no longer works that
// way: it keeps a registry, extensions/extensions.json, and once that
// file exists - which it does for anyone who has installed any other
// extension - a folder that is not listed in it is not loaded. No error,
// no highlighting. So this packs the folder as a .vsix, the format VS
// Code installs from, and hands it to VS Code's own command line:
//
//	code --install-extension veyl-lang-0.58.0.vsix --force
//
// which writes the registry entry itself. Folders an older installer
// copied in by hand are removed afterwards, since nothing loads them.
//
//	veyl vscode               install (the installer runs this)
//	veyl vscode --vsix FILE   only write the .vsix, for Install from VSIX...
//	veyl vscode --uninstall   remove it again
//
// The extension folder is found beside veyl.exe (editors\vscode, where
// the installer puts it) or in a source checkout. VEYL_CODE names the
// VS Code command line when it is not one of the usual places.

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type extManifest struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"displayName"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	Publisher   string            `json:"publisher"`
	Engines     map[string]string `json:"engines"`
	Icon        string            `json:"icon"`
}

func vscodeCommand(args []string) {
	var vsix, dir string
	uninstall := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--vsix":
			if i+1 >= len(args) {
				fail("--vsix needs a file name")
			}
			vsix = args[i+1]
			i++
		case "--uninstall":
			uninstall = true
		default:
			dir = args[i]
		}
	}

	if uninstall {
		code := findCode()
		if code == nil {
			fail("VS Code's command line (code) was not found; uninstall Veyl from VS Code's Extensions view")
		}
		out, err := runCode(code, "--uninstall-extension", "veyl.veyl-lang")
		fmt.Print(out)
		if err != nil {
			os.Exit(1)
		}
		return
	}

	if dir == "" {
		dir = findExtensionDir()
	}
	m, err := readExtManifest(dir)
	if err != nil {
		fail("%v", err)
	}

	if vsix != "" {
		if err := writeVSIX(vsix, dir, m); err != nil {
			fail("%v", err)
		}
		fmt.Printf("wrote %s - in VS Code, run Extensions: Install from VSIX... and pick it\n", vsix)
		return
	}

	code := findCode()
	if code == nil {
		p := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%s.vsix", m.Name, m.Version))
		if err := writeVSIX(p, dir, m); err != nil {
			fail("%v", err)
		}
		fail("VS Code's command line (code) was not found. The extension is packed at\n  %s\n"+
			"In VS Code, run Extensions: Install from VSIX... and pick that file.", p)
	}

	tmp, err := os.MkdirTemp("", "veyl-vsix-*")
	if err != nil {
		fail("%v", err)
	}
	defer os.RemoveAll(tmp)
	p := filepath.Join(tmp, fmt.Sprintf("%s-%s.vsix", m.Name, m.Version))
	if err := writeVSIX(p, dir, m); err != nil {
		fail("%v", err)
	}
	out, err := runCode(code, "--install-extension", p, "--force")
	fmt.Print(out)
	if err != nil {
		fail("VS Code did not install the extension: %v", err)
	}
	removeHandCopies(m)
	fmt.Printf("Veyl %s is installed in VS Code. Reload any open window to pick it up.\n", m.Version)
}

func readExtManifest(dir string) (*extManifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil, fmt.Errorf("no VS Code extension in %s: %v", dir, err)
	}
	var m extManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %v", filepath.Join(dir, "package.json"), err)
	}
	if m.Name == "" || m.Publisher == "" || m.Version == "" {
		return nil, fmt.Errorf("%s needs a name, a publisher and a version", filepath.Join(dir, "package.json"))
	}
	return &m, nil
}

// findExtensionDir is where the installer puts the extension, beside
// veyl.exe, or the editors folder of a source checkout.
func findExtensionDir() string {
	exe, err := os.Executable()
	if err == nil {
		base := filepath.Dir(exe)
		for _, c := range []string{
			filepath.Join(base, "editors", "vscode"),
			filepath.Join(base, "..", "..", "editors", "vscode"),
			filepath.Join(base, "..", "editors", "vscode"),
		} {
			if _, err := os.Stat(filepath.Join(c, "package.json")); err == nil {
				return c
			}
		}
	}
	fail("the VS Code extension was not found beside veyl; name its folder: veyl vscode path\\to\\editors\\vscode")
	return ""
}

// findCode is the command that runs VS Code's command line, or nil.
func findCode() []string {
	if c := os.Getenv("VEYL_CODE"); c != "" {
		return strings.Fields(c)
	}
	names := []string{"code", "code-insiders", "codium"}
	if runtime.GOOS == "windows" {
		names = []string{"code.cmd", "code-insiders.cmd", "codium.cmd"}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return []string{p}
		}
	}
	if runtime.GOOS == "windows" {
		// The user installer puts code.cmd on PATH, but a PATH change
		// reaches only processes started after it, and the system
		// installer can leave it off entirely.
		for _, base := range []string{os.Getenv("LOCALAPPDATA") + `\Programs`, os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
			if base == "" || base == `\Programs` {
				continue
			}
			p := filepath.Join(base, "Microsoft VS Code", "bin", "code.cmd")
			if _, err := os.Stat(p); err == nil {
				return []string{p}
			}
		}
	}
	if runtime.GOOS == "darwin" {
		p := "/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"
		if _, err := os.Stat(p); err == nil {
			return []string{p}
		}
	}
	return nil
}

func runCode(code []string, args ...string) (string, error) {
	all := append(append([]string(nil), code[1:]...), args...)
	cmd := exec.Command(code[0], all...)
	if runtime.GOOS == "windows" && strings.HasSuffix(strings.ToLower(code[0]), ".cmd") {
		// A .cmd is run by cmd.exe; Go does that, but the quoting of a
		// path with spaces is cmd's, so go through it explicitly.
		cmd = exec.Command("cmd", append([]string{"/c", code[0]}, all...)...)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// removeHandCopies deletes the veyl.veyl-lang-* folders an older
// installer copied into ~/.vscode/extensions, which the registry does not
// list and so nothing loads. One the registry does list is left alone.
func removeHandCopies(m *extManifest) {
	dir := os.Getenv("VSCODE_EXTENSIONS")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		dir = filepath.Join(home, ".vscode", "extensions")
	}
	b, err := os.ReadFile(filepath.Join(dir, "extensions.json"))
	if err != nil {
		return
	}
	var reg []struct {
		RelativeLocation string `json:"relativeLocation"`
		Location         struct {
			FsPath string `json:"fsPath"`
			Path   string `json:"path"`
		} `json:"location"`
	}
	if json.Unmarshal(b, &reg) != nil {
		return
	}
	listed := map[string]bool{}
	for _, e := range reg {
		for _, p := range []string{e.RelativeLocation, e.Location.FsPath, e.Location.Path} {
			if p != "" {
				listed[strings.ToLower(filepath.Base(filepath.FromSlash(p)))] = true
			}
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := strings.ToLower(m.Publisher + "." + m.Name + "-")
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if !e.IsDir() || !strings.HasPrefix(n, prefix) || listed[n] {
			continue
		}
		if old, err := readExtManifest(filepath.Join(dir, e.Name())); err != nil || old.Publisher != m.Publisher || old.Name != m.Name {
			continue
		}
		if os.RemoveAll(filepath.Join(dir, e.Name())) == nil {
			fmt.Printf("removed %s, an old copy VS Code was not loading\n", e.Name())
		}
	}
}

// writeVSIX packs an extension folder the way vsce does: the files under
// extension/, a vsixmanifest describing them, and the content types.
func writeVSIX(path, dir string, m *extManifest) error {
	var files []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && rel != "." || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(info.Name(), ".") || strings.HasSuffix(info.Name(), ".vsix") {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	z := zip.NewWriter(f)
	add := func(name string, data []byte) error {
		w, err := z.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}

	exts := map[string]bool{".vsixmanifest": true}
	readme := ""
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			z.Close()
			f.Close()
			return err
		}
		name := rel
		// vsce lowercases the readme, and the manifest points at it.
		if strings.EqualFold(rel, "README.md") {
			name = "readme.md"
			readme = name
		}
		if err := add("extension/"+name, data); err != nil {
			z.Close()
			f.Close()
			return err
		}
		exts[strings.ToLower(filepath.Ext(name))] = true
	}

	if err := add("extension.vsixmanifest", []byte(vsixManifest(m, readme))); err != nil {
		z.Close()
		f.Close()
		return err
	}
	if err := add("[Content_Types].xml", []byte(contentTypes(exts))); err != nil {
		z.Close()
		f.Close()
		return err
	}
	if err := z.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func vsixManifest(m *extManifest, readme string) string {
	e := html.EscapeString
	display := m.DisplayName
	if display == "" {
		display = m.Name
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>
<PackageManifest Version="2.0.0" xmlns="http://schemas.microsoft.com/developer/vsx-schema/2011" xmlns:d="http://schemas.microsoft.com/developer/vsx-schema-design/2011">
	<Metadata>
`)
	fmt.Fprintf(&b, "\t\t<Identity Language=\"en-US\" Id=\"%s\" Version=\"%s\" Publisher=\"%s\" />\n", e(m.Name), e(m.Version), e(m.Publisher))
	fmt.Fprintf(&b, "\t\t<DisplayName>%s</DisplayName>\n", e(display))
	fmt.Fprintf(&b, "\t\t<Description xml:space=\"preserve\">%s</Description>\n", e(m.Description))
	b.WriteString("\t\t<Categories>Programming Languages</Categories>\n\t\t<GalleryFlags>Public</GalleryFlags>\n\t\t<Properties>\n")
	fmt.Fprintf(&b, "\t\t\t<Property Id=\"Microsoft.VisualStudio.Code.Engine\" Value=\"%s\" />\n", e(m.Engines["vscode"]))
	b.WriteString(`			<Property Id="Microsoft.VisualStudio.Code.ExtensionDependencies" Value="" />
			<Property Id="Microsoft.VisualStudio.Code.ExtensionPack" Value="" />
			<Property Id="Microsoft.VisualStudio.Code.ExtensionKind" Value="workspace" />
			<Property Id="Microsoft.VisualStudio.Code.LocalizedLanguages" Value="" />
			<Property Id="Microsoft.VisualStudio.Services.GitHubFlavoredMarkdown" Value="true" />
		</Properties>
`)
	if m.Icon != "" {
		fmt.Fprintf(&b, "\t\t<Icon>extension/%s</Icon>\n", e(m.Icon))
	}
	b.WriteString(`	</Metadata>
	<Installation>
		<InstallationTarget Id="Microsoft.VisualStudio.Code"/>
	</Installation>
	<Dependencies/>
	<Assets>
		<Asset Type="Microsoft.VisualStudio.Code.Manifest" Path="extension/package.json" Addressable="true" />
`)
	if readme != "" {
		fmt.Fprintf(&b, "\t\t<Asset Type=\"Microsoft.VisualStudio.Services.Content.Details\" Path=\"extension/%s\" Addressable=\"true\" />\n", readme)
	}
	if m.Icon != "" {
		fmt.Fprintf(&b, "\t\t<Asset Type=\"Microsoft.VisualStudio.Services.Icons.Default\" Path=\"extension/%s\" Addressable=\"true\" />\n", e(m.Icon))
	}
	b.WriteString("\t</Assets>\n</PackageManifest>\n")
	return b.String()
}

func contentTypes(exts map[string]bool) string {
	known := map[string]string{
		".json": "application/json", ".vsixmanifest": "text/xml", ".md": "text/markdown",
		".png": "image/png", ".js": "application/javascript", ".txt": "text/plain",
	}
	var keys []string
	for x := range exts {
		if x != "" {
			keys = append(keys, x)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n" +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	for _, x := range keys {
		t := known[x]
		if t == "" {
			t = "application/octet-stream"
		}
		fmt.Fprintf(&b, `<Default Extension="%s" ContentType="%s"/>`, x, t)
	}
	b.WriteString("</Types>\n")
	return b.String()
}
