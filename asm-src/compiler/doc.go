package main

// scandiacus doc: a reference for a file, written from its declarations and
// the comments above them.
//
//	// Area is width times height.
//	pub fn area(w: int, h: int) -> int { ... }
//
// becomes a heading, the signature, and the comment as text. The
// comment is the run of whole-line // comments directly above a
// declaration, with no blank line between. A file that marks anything
// pub is a library, and only what it exports is documented; otherwise
// everything at the top level is.
//
//	scandiacus doc shapes.scd            Markdown on stdout
//	scandiacus doc shapes.scd -o api.md  into a file; .html writes a web page
//
// The same comments show when hovering a name in the editor.

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"

	front "scandiacusfront"
)

// docComments maps a line to the comment block that ends on the line
// above it: what a declaration on that line is documented by.
func docComments(path, src string) map[int]string {
	lx := NewLexer(path, src)
	lx.KeepComments = true
	toks := lx.Scan()
	// Whole-line comments only: one that follows code on its line is
	// about that code, not about what comes next.
	whole := map[int]string{}
	lastCode := map[int]bool{}
	for _, t := range toks {
		switch t.Kind {
		case COMMENT:
			if !lastCode[t.Line] {
				text := strings.TrimPrefix(t.Text(), "//")
				text = strings.TrimPrefix(text, " ")
				whole[t.Line] = text
			}
		case NEWLINE, EOF:
		default:
			lastCode[t.Line] = true
		}
	}
	out := map[int]string{}
	for line := range lastCode {
		var block []string
		for l := line - 1; ; l-- {
			text, ok := whole[l]
			if !ok {
				break
			}
			block = append([]string{text}, block...)
		}
		if len(block) > 0 {
			out[line] = strings.Join(block, "\n")
		}
	}
	return out
}

// docIntro is the comment a file opens with, when a blank line separates
// it from what follows; touching a declaration, it is that one's.
func docIntro(src string) string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var block []string
	i := 0
	for ; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(t, "//") {
			break
		}
		block = append(block, strings.TrimPrefix(strings.TrimPrefix(t, "//"), " "))
	}
	if len(block) == 0 || i < len(lines) && strings.TrimSpace(lines[i]) != "" {
		return ""
	}
	return strings.Join(block, "\n")
}

type docEntry struct {
	kind, name, sig, doc string
	line                 int
	members              []docEntry
}

func docCommand(args []string) {
	var file, out string
	for i := 0; i < len(args); i++ {
		if args[i] == "-o" && i+1 < len(args) {
			out = args[i+1]
			i++
			continue
		}
		file = args[i]
	}
	if file == "" {
		fail("scandiacus doc needs a .scd file")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		fail("%v", err)
	}
	entries, errs := docEntries(file, string(b))
	if len(errs) > 0 {
		report(errs)
	}
	title := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	text := docMarkdown(title, docIntro(string(b)), entries)
	if strings.HasSuffix(strings.ToLower(out), ".html") {
		text = docHTML(title, entries)
	}
	if out == "" {
		fmt.Print(text)
		return
	}
	if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
		fail("%v", err)
	}
	fmt.Printf("wrote %s\n", out)
}

// docEntries reads a file's declarations, in source order.
func docEntries(path, src string) ([]docEntry, []string) {
	lx := NewLexer(path, src)
	toks := lx.Scan()
	p := NewParser(path, toks)
	prog := p.ParseProgram()
	if errs := append(append([]string{}, lx.Errors...), p.Errors...); len(errs) > 0 {
		return nil, errs
	}
	docs := docComments(path, src)

	library := false
	for _, f := range prog.Funcs {
		library = library || f.Pub
	}
	for _, s := range prog.Structs {
		library = library || s.Pub
	}
	for _, e := range prog.Enums {
		library = library || e.Pub
	}
	for _, g := range prog.Globals {
		library = library || g.Pub
	}
	shown := func(pub bool, name string) bool {
		return (pub || !library) && !strings.HasPrefix(name, "_")
	}

	var out []docEntry
	methods := map[string][]docEntry{}
	for _, f := range prog.Funcs {
		// A method is as visible as its struct, which decides below.
		if !shown(f.Pub || f.Recv != "", f.Name) {
			continue
		}
		e := docEntry{kind: "fn", name: f.Name, sig: docFnSig(f), doc: docs[f.Line], line: f.Line}
		if f.Extern {
			e.kind = "extern fn"
		}
		if f.Recv != "" {
			e.kind = "method"
			methods[f.Recv] = append(methods[f.Recv], e)
			continue
		}
		out = append(out, e)
	}
	for _, s := range prog.Structs {
		if !shown(s.Pub, s.Name) {
			continue
		}
		name := s.Name
		if len(s.TypeParams) > 0 {
			name += "<" + strings.Join(s.TypeParams, ", ") + ">"
		}
		var fs []string
		for _, f := range s.Fields {
			fs = append(fs, "    "+f.Name+": "+f.Type)
		}
		kw := "struct"
		if s.Extern {
			kw = "extern struct"
		}
		e := docEntry{kind: "struct", name: s.Name, sig: kw + " " + name + " {\n" + strings.Join(fs, "\n") + "\n}",
			doc: docs[s.Line], line: s.Line, members: methods[s.Name]}
		out = append(out, e)
	}
	for _, en := range prog.Enums {
		if !shown(en.Pub, en.Name) {
			continue
		}
		var vs []string
		for i, v := range en.Variants {
			if i < len(en.Payloads) && len(en.Payloads[i]) > 0 {
				var ps []string
				for _, p := range en.Payloads[i] {
					ps = append(ps, p.Name+": "+p.Type)
				}
				v += "(" + strings.Join(ps, ", ") + ")"
			}
			vs = append(vs, "    "+v)
		}
		out = append(out, docEntry{kind: "enum", name: en.Name, sig: "enum " + en.Name + " {\n" + strings.Join(vs, ",\n") + "\n}",
			doc: docs[en.Line], line: en.Line, members: methods[en.Name]})
	}
	for _, it := range prog.Ifaces {
		if !shown(it.Pub, it.Name) {
			continue
		}
		var ms []string
		for _, m := range it.Methods {
			ms = append(ms, "    "+docFnSig(m))
		}
		out = append(out, docEntry{kind: "interface", name: it.Name, sig: "interface " + it.Name + " {\n" + strings.Join(ms, "\n") + "\n}",
			doc: docs[it.Line], line: it.Line})
	}
	for _, g := range prog.Globals {
		if !shown(g.Pub, g.Name) {
			continue
		}
		kw := "var"
		if g.Const {
			kw = "const"
		}
		sig := kw + " " + g.Name
		if g.Type != "" {
			sig += ": " + g.Type
		}
		out = append(out, docEntry{kind: kw, name: g.Name, sig: sig, doc: docs[g.Line], line: g.Line})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].line < out[j].line })
	return out, nil
}

// docFnSig is a function's signature as it was written.
func docFnSig(f *front.FnDecl) string {
	var ps []string
	for _, p := range f.Params {
		if p.Name == "self" && p.Type == "" {
			ps = append(ps, "self")
			continue
		}
		s := p.Name + ": " + p.Type
		if p.Default != nil {
			s += " = ..."
		}
		ps = append(ps, s)
	}
	name := f.Name
	if len(f.TypeParams) > 0 {
		name += "<" + strings.Join(f.TypeParams, ", ") + ">"
	}
	s := "fn " + name + "(" + strings.Join(ps, ", ") + ")"
	if f.Ret != "" {
		s += " -> " + f.Ret
	}
	return s
}

func docMarkdown(title, intro string, entries []docEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	if intro != "" {
		b.WriteString(intro + "\n\n")
	}
	if len(entries) == 0 {
		b.WriteString("Nothing to document.\n")
		return b.String()
	}
	b.WriteString("## Contents\n\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "- [%s %s](#%s)\n", e.kind, e.name, docAnchor(e))
	}
	b.WriteString("\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "## %s %s\n\n```scandiacus\n%s\n```\n\n", e.kind, e.name, e.sig)
		if e.doc != "" {
			b.WriteString(e.doc + "\n\n")
		}
		for _, m := range e.members {
			fmt.Fprintf(&b, "### %s.%s\n\n```scandiacus\n%s\n```\n\n", e.name, m.name, m.sig)
			if m.doc != "" {
				b.WriteString(m.doc + "\n\n")
			}
		}
	}
	return b.String()
}

func docAnchor(e docEntry) string {
	return strings.ToLower(strings.ReplaceAll(e.kind+"-"+e.name, " ", "-"))
}

func docHTML(title string, entries []docEntry) string {
	e := html.EscapeString
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
body { font: 16px/1.5 system-ui, sans-serif; max-width: 52rem; margin: 2rem auto; padding: 0 1rem; color: #1d1d1f; background: #fff; }
pre { background: #f4f4f6; padding: .75rem 1rem; border-radius: 6px; overflow-x: auto; }
code, pre { font-family: ui-monospace, Consolas, monospace; font-size: 14px; }
h2 { border-top: 1px solid #ddd; padding-top: 1rem; margin-top: 2rem; }
.kind { color: #777; font-weight: normal; }
@media (prefers-color-scheme: dark) { body { color: #e6e6e6; background: #17171a; } pre { background: #232328; } h2 { border-color: #333; } .kind { color: #999; } }
</style></head><body>
<h1>%s</h1>
`, e(title), e(title))
	if len(entries) == 0 {
		b.WriteString("<p>Nothing to document.</p>\n")
	} else {
		b.WriteString("<ul>\n")
		for _, en := range entries {
			fmt.Fprintf(&b, "<li><a href=\"#%s\">%s</a></li>\n", docAnchor(en), e(en.name))
		}
		b.WriteString("</ul>\n")
	}
	para := func(doc string) {
		for _, p := range strings.Split(doc, "\n\n") {
			if strings.TrimSpace(p) != "" {
				fmt.Fprintf(&b, "<p>%s</p>\n", e(p))
			}
		}
	}
	for _, en := range entries {
		fmt.Fprintf(&b, "<h2 id=\"%s\"><span class=\"kind\">%s</span> %s</h2>\n<pre>%s</pre>\n",
			docAnchor(en), e(en.kind), e(en.name), e(en.sig))
		para(en.doc)
		for _, m := range en.members {
			fmt.Fprintf(&b, "<h3>%s.%s</h3>\n<pre>%s</pre>\n", e(en.name), e(m.name), e(m.sig))
			para(m.doc)
		}
	}
	b.WriteString("</body></html>\n")
	return b.String()
}
