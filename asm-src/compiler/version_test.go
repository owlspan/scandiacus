package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestVersionsAgree checks every place outside this package that states
// the version against Version.
//
// They drifted before: the compiler said 0.19.0 while the installer and
// the VS Code extension still said 0.18.1, and the extension's README was
// on 0.10.0. The installer names its output after AppVersion and installs
// the extension into a folder named after ExtVersion, so a mismatch is a
// release that says one thing and ships another.
func TestVersionsAgree(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	iss := read("asm-src/installer/scandiacus.iss")
	for _, name := range []string{"AppVersion", "ExtVersion"} {
		m := regexp.MustCompile(`#define ` + name + ` "([^"]+)"`).FindStringSubmatch(iss)
		if m == nil {
			t.Errorf("scandiacus.iss has no %s", name)
		} else if m[1] != Version {
			t.Errorf("scandiacus.iss %s is %s, the compiler is %s", name, m[1], Version)
		}
	}

	var pkg struct{ Version string }
	if err := json.Unmarshal([]byte(read("editors/vscode/package.json")), &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != Version {
		t.Errorf("editors/vscode/package.json is %s, the compiler is %s", pkg.Version, Version)
	}

	if !strings.Contains(read("editors/vscode/README.md"), "scandiacus-lang-"+Version+".vsix") {
		t.Errorf("editors/vscode/README.md does not name scandiacus-lang-%s.vsix", Version)
	}

	syntax := read("docs/SYNTAX.md")
	if !strings.Contains(syntax, "**Version "+Version+"**") {
		t.Errorf("docs/SYNTAX.md does not open with Version %s", Version)
	}
	if !strings.Contains(syntax, "v"+Version+" does not do yet") {
		t.Errorf("the known limitations in docs/SYNTAX.md are not for v%s", Version)
	}
}
