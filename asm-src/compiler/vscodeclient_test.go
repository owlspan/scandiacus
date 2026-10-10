package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestVSCodeClient runs editors/vscode/extension.js under node, against
// a stand-in for the vscode module and a real veyl lsp, so the client's
// side of the protocol is tested as well as the server's.
func TestVSCodeClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	veyl := asmBackend(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "client.vl")
	prog := "fn twice(n: int) -> int {\n    return n * 2\n}\n\nprint(twice(4))\nlet bad: int = \"x\"\nprint(1+2)\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, filepath.Join("..", "..", "editors", "vscode", "test", "run.js"), veyl, src).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("%v\n%s", err, out)
	}
}
