package main

import (
	"os"
	"strings"
	"testing"
)

const testSHA = "4f2c9e1d0a5b6c7d8e9f00112233445566778899"

func TestResolveSpecPins(t *testing.T) {
	old := pinCommit
	defer func() { pinCommit = old }()
	var asked string
	pinCommit = func(user, repo, ref string) (string, error) {
		asked = user + "/" + repo + "@" + ref
		return testSHA, nil
	}
	cases := []struct{ spec, ask, base, name, pin string }{
		{"you/scadgame", "you/scadgame@HEAD",
			"https://raw.githubusercontent.com/you/scadgame/" + testSHA + "/", "scadgame",
			"github.com/you/scadgame@" + testSHA},
		{"github.com/you/scadgame@v1.2", "you/scadgame@v1.2",
			"https://raw.githubusercontent.com/you/scadgame/" + testSHA + "/", "scadgame",
			"github.com/you/scadgame@" + testSHA},
		{"you/libs/vec@main", "you/libs@main",
			"https://raw.githubusercontent.com/you/libs/" + testSHA + "/vec/", "vec",
			"github.com/you/libs/vec@" + testSHA},
	}
	for _, c := range cases {
		base, name, pin, err := resolveSpecPin(c.spec)
		if err != nil || asked != c.ask || base != c.base || name != c.name || pin != c.pin {
			t.Errorf("%s: got %q %q %q %v, asked %q", c.spec, base, name, pin, err, asked)
		}
	}
	if _, _, _, err := resolveSpecPin("you/scadgame@"); err == nil {
		t.Error("an empty ref should be refused")
	}
	// A bare name is the official registry and is not pinned.
	if _, name, pin, err := resolveSpecPin("sqlite"); err != nil || name != "sqlite" || pin != "" {
		t.Errorf("sqlite: %q %q %v", name, pin, err)
	}
}

func TestRecordPin(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(dir)
	if err := recordPin("zeta", "github.com/a/zeta@"+testSHA); err != nil {
		t.Fatal(err)
	}
	if err := recordPin("alpha", "github.com/a/alpha@"+testSHA); err != nil {
		t.Fatal(err)
	}
	if err := recordPin("zeta", "github.com/a/zeta@"+strings.Repeat("0", 40)); err != nil {
		t.Fatal(err)
	}
	deps, err := readDeps()
	if err != nil || len(deps) != 2 || deps["zeta"] != "github.com/a/zeta@"+strings.Repeat("0", 40) {
		t.Fatalf("deps: %v %v", deps, err)
	}
	body, _ := os.ReadFile(depsName)
	if i, j := strings.Index(string(body), "alpha"), strings.Index(string(body), "zeta"); i < 0 || i > j {
		t.Fatalf("not sorted:\n%s", body)
	}
	if !isFullSHA(testSHA) || isFullSHA("v1.2") || isFullSHA(strings.ToUpper(testSHA)) {
		t.Fatal("isFullSHA")
	}
}
