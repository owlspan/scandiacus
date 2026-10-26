package main

// Pinning a package from GitHub to a commit.
//
//	scandiacus get you/scadgame            the default branch, as it is now
//	scandiacus get you/scadgame@v1.2       a tag or a branch
//	scandiacus get you/scadgame@4f2c9e1    a commit
//	scandiacus get                         everything scandiacus.deps lists
//
// Whatever was asked for is resolved to the full commit hash before any
// file is fetched, and the files come from that commit, so a push to the
// repository halfway through an install cannot mix two versions. The
// package and the hash go into scandiacus.deps beside the program, one
// line each:
//
//	scadgame github.com/you/scadgame@4f2c9e1d...
//
// Commit that file with the program, and `scandiacus get` on another
// machine installs exactly the same code. That is the whole of what a
// lock file is for; there is no separate one.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// depsName lists a project's packages from GitHub, pinned.
const depsName = "scandiacus.deps"

// pinCommit resolves a tag, branch or commit to its full hash. A full
// hash is taken as it is, so reinstalling from scandiacus.deps needs no
// API call: the API allows sixty an hour without a token. A variable so
// that tests need no network.
var pinCommit = func(user, repo, ref string) (string, error) {
	if isFullSHA(ref) {
		return ref, nil
	}
	body, err := fetch("https://api.github.com/repos/" + user + "/" + repo + "/commits/" + ref)
	if err != nil {
		return "", fmt.Errorf("could not find %s in %s/%s: %v", ref, user, repo, err)
	}
	var c struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(body, &c); err != nil || !isFullSHA(c.SHA) {
		return "", fmt.Errorf("GitHub gave no commit for %s in %s/%s", ref, user, repo)
	}
	return c.SHA, nil
}

func isFullSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// readDeps reads scandiacus.deps as package name to pinned spec.
func readDeps() (map[string]string, error) {
	body, err := os.ReadFile(depsName)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	deps := map[string]string{}
	for n, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("%s line %d: expected a name and a spec, got %q", depsName, n+1, line)
		}
		deps[f[0]] = f[1]
	}
	return deps, nil
}

// recordPin adds or replaces a package's line in scandiacus.deps.
func recordPin(name, pin string) error {
	deps, err := readDeps()
	if err != nil {
		return err
	}
	deps[name] = pin
	names := make([]string, 0, len(deps))
	for n := range deps {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# packages from GitHub, pinned by scandiacus get; see docs/SYNTAX.md, Packages\n")
	for _, n := range names {
		fmt.Fprintf(&b, "%s %s\n", n, deps[n])
	}
	return os.WriteFile(depsName, []byte(b.String()), 0o644)
}

// pkgGetDeps installs everything scandiacus.deps lists, at the commits
// it names.
func pkgGetDeps() error {
	deps, err := readDeps()
	if err != nil {
		return err
	}
	if len(deps) == 0 {
		return fmt.Errorf("get needs a package name or a url, or a %s to install from", depsName)
	}
	names := make([]string, 0, len(deps))
	for n := range deps {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := pkgGet(deps[n]); err != nil {
			return fmt.Errorf("%s: %v", n, err)
		}
	}
	return nil
}
