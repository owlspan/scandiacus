package main

// Multi-file programs: `import "helpers.vl"`.
//
// A file import folds another file's declarations into this program
// before anything is checked, so from the checker down there is one
// program and nothing else has to know files exist. That is the same
// thing the Go backend's loader does.
//
// What this does not do is the other half of that loader: importing a
// *package* by bare name. That needs the package registry, which lives
// in src/ and is about where a package is installed rather than about
// the language. A bare-word import is a clean error naming the gap
// instead of a mysterious missing file.
//
// This is duplicated from the Go backend rather than shared, which is
// the same papercut as the resolver: both belong in the front end, and
// neither is there yet.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type importLoader struct {
	seen   map[string]bool // absolute paths already folded in
	stack  []string        // in progress, for cycle detection
	errors []string

	// The namespace each file was imported under with `as`, by path, and
	// the file each namespace belongs to; see namespace.go.
	aliasOf  map[string]string
	fileOfNS map[string]string

	// The text of every file folded in. The prelude decides what to
	// pull in by looking for names in the source, and an imported file
	// is as much of the program as the one that imported it.
	sources []string
}

// loadImports pulls in every file this program imports, and every file
// those import, depth first.
func loadImports(prog *Program, from string) ([]string, []string) {
	abs, err := filepath.Abs(from)
	if err != nil {
		abs = from
	}
	l := &importLoader{seen: map[string]bool{abs: true},
		aliasOf: map[string]string{}, fileOfNS: map[string]string{}}
	namespaceFile(prog, "")
	l.resolve(prog, abs)
	return l.errors, l.sources
}

func (l *importLoader) resolve(prog *Program, from string) {
	for _, imp := range prog.Imports {
		target := imp.Path

		// A bare name is an installed package: `import "sqlite"` finds
		// veyl_modules/sqlite/sqlite.vl. A path ending in .vl is a file,
		// relative to the importing file as before.
		if !strings.HasSuffix(target, ".vl") {
			if strings.ContainsAny(target, `/\`) {
				l.errorAt(imp, "an import is either a package name or a path to a "+
					".vl file, and %q is neither", imp.Path)
				continue
			}
			pkg := filepath.Join(filepath.Dir(from), modulesDir, target, target+".vl")
			if _, err := os.Stat(pkg); err != nil {
				l.errorAt(imp, "no package named %q - install it with `veyl get %s`",
					target, target)
				continue
			}
			target = pkg
		}

		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(from), target)
		}
		if abs, err := filepath.Abs(target); err == nil {
			target = abs
		}

		if l.onStack(target) {
			l.errorAt(imp, "import cycle: %s imports itself, directly or indirectly",
				filepath.Base(target))
			continue
		}
		if !l.checkAlias(imp, target) {
			continue
		}
		if l.seen[target] {
			continue // already folded in; importing twice is harmless
		}
		l.seen[target] = true

		sub, ok := l.load(imp, target)
		if !ok {
			continue
		}
		namespaceFile(sub, imp.As)

		l.stack = append(l.stack, target)
		l.resolve(sub, target)
		l.stack = l.stack[:len(l.stack)-1]

		prog.Structs = append(prog.Structs, sub.Structs...)
		prog.Funcs = append(prog.Funcs, sub.Funcs...)
		prog.Globals = append(prog.Globals, sub.Globals...)
		prog.Enums = append(prog.Enums, sub.Enums...)
		prog.Ifaces = append(prog.Ifaces, sub.Ifaces...)
		prog.FormatSpecs = prog.FormatSpecs || sub.FormatSpecs
	}
}

// load parses one imported file. Its top-level statements are refused
// rather than silently dropped: a file that runs code on import would
// run it at a moment nobody chose.
func (l *importLoader) load(imp *ImportDecl, target string) (*Program, bool) {
	srcBytes, err := os.ReadFile(target)
	if err != nil {
		// The path as written, not the resolved one. The absolute path
		// is noise, and it would make error output differ between
		// machines for no reason.
		if os.IsNotExist(err) {
			l.errorAt(imp, "cannot find %q next to %s", imp.Path, filepath.Base(imp.File))
		} else {
			l.errorAt(imp, "cannot read %q", imp.Path)
		}
		return nil, false
	}

	name := filepath.Base(target)
	l.sources = append(l.sources, string(srcBytes))
	lx := NewLexer(name, string(srcBytes))
	toks := lx.Scan()
	l.errors = append(l.errors, lx.Errors...)

	ps := NewParser(name, toks)
	sub := ps.ParseProgram()
	l.errors = append(l.errors, ps.Errors...)

	for _, s := range sub.Main {
		line, col := s.Pos()
		l.errors = append(l.errors, fmt.Sprintf(
			"%s:%d:%d: an imported file can only declare things - "+
				"move this statement into the program that imports it", name, line, col))
	}

	if sub.GC != "" {
		l.errors = append(l.errors, fmt.Sprintf(
			"%s:%d:%d: gc %s is for the whole program, so it goes in the file you build, not one it imports",
			name, sub.GCAt.Line, sub.GCAt.Col, sub.GC))
	}

	stampImportedFile(sub, target)
	return sub, true
}

// checkAlias holds a file to one namespace: imported with `as` under
// the same name everywhere or never, and no two files under one name.
func (l *importLoader) checkAlias(imp *ImportDecl, target string) bool {
	if imp.As != "" && libraryNamespace(imp.As) {
		l.errorAt(imp, "%q is a library namespace already - import this file under another name", imp.As)
		return false
	}
	if l.seen[target] {
		if was := l.aliasOf[target]; was != imp.As {
			switch {
			case was == "":
				l.errorAt(imp, "%s is already imported without a namespace, so it cannot also be %q - "+
					"import it the same way everywhere", imp.Path, imp.As)
			case imp.As == "":
				l.errorAt(imp, "%s is already imported as %q - import it as %q here too", imp.Path, was, was)
			default:
				l.errorAt(imp, "%s is already imported as %q, so it cannot also be %q", imp.Path, was, imp.As)
			}
			return false
		}
		return true
	}
	if imp.As != "" {
		if other, taken := l.fileOfNS[imp.As]; taken && other != target {
			l.errorAt(imp, "%q already names %s - give this import another name", imp.As, filepath.Base(other))
			return false
		}
		l.fileOfNS[imp.As] = target
	}
	l.aliasOf[target] = imp.As
	return true
}

func (l *importLoader) onStack(path string) bool {
	for _, p := range l.stack {
		if p == path {
			return true
		}
	}
	return false
}

func (l *importLoader) errorAt(n Node, format string, args ...any) {
	line, col := n.Pos()
	file := "?"
	if imp, ok := n.(*ImportDecl); ok {
		file = imp.File
	}
	l.errors = append(l.errors,
		fmt.Sprintf("%s:%d:%d: %s", file, line, col, fmt.Sprintf(format, args...)))
}

// stampImportedFile records which file every declaration came from,
// using the absolute path as the identity. That is what lets `pub` be
// enforced across files that happen to share a base name.
func stampImportedFile(prog *Program, abs string) {
	for _, d := range prog.Structs {
		d.File = abs
	}
	for _, f := range prog.Funcs {
		f.File = abs
	}
	for _, g := range prog.Globals {
		g.File = abs
	}
	for _, e := range prog.Enums {
		e.File = abs
	}
	for _, d := range prog.Ifaces {
		d.File = abs
	}
}
