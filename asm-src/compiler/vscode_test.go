package main

import (
	"archive/zip"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// TestVSIX packs the real extension folder and checks the parts VS Code
// reads when it installs one: the manifest's identity, which must match
// package.json, and every file under extension/.
func TestVSIX(t *testing.T) {
	dir := filepath.Join("..", "..", "editors", "vscode")
	m, err := readExtManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "x.vsix")
	if err := writeVSIX(p, dir, m); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	have := map[string]string{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		r.Close()
		have[f.Name] = string(b)
	}
	if _, ok := have["extension/test/run.js"]; ok {
		t.Errorf("the extension's own tests were packed")
	}
	if !strings.Contains(have["extension/package.json"], `"main": "./extension.js"`) {
		t.Errorf("package.json lost its main")
	}
	for _, want := range []string{"extension/extension.js", "extension.vsixmanifest", "[Content_Types].xml", "extension/package.json",
		"extension/syntaxes/scandiacus.tmLanguage.json", "extension/language-configuration.json",
		"extension/icons/scandiacus.png", "extension/readme.md"} {
		if _, ok := have[want]; !ok {
			t.Errorf("the package has no %s", want)
		}
	}
	id := `<Identity Language="en-US" Id="scandiacus-lang" Version="` + Version + `" Publisher="scandiacus" />`
	if !strings.Contains(have["extension.vsixmanifest"], id) {
		t.Errorf("manifest does not carry %s:\n%s", id, have["extension.vsixmanifest"])
	}
	if !strings.Contains(have["[Content_Types].xml"], `Extension=".json" ContentType="application/json"`) {
		t.Errorf("content types lack .json:\n%s", have["[Content_Types].xml"])
	}
}
