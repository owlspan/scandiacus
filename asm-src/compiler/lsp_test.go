package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lspClient drives a server over pipes, the way an editor does.
type lspClient struct {
	t    *testing.T
	in   io.WriteCloser
	out  *bufio.Reader
	next int
	// notifications that arrived while waiting for a reply
	notes []rpcMsg
}

func startLSP(t *testing.T) *lspClient {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &lspServer{out: outW, docs: map[string]*lspDoc{}}
	go func() {
		s.serve(inR)
		outW.Close()
	}()
	c := &lspClient{t: t, in: inW, out: bufio.NewReader(outR)}
	t.Cleanup(func() { inW.Close() })
	return c
}

func (c *lspClient) write(m map[string]any) {
	m["jsonrpc"] = "2.0"
	b, _ := json.Marshal(m)
	fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n%s", len(b), b)
}

func (c *lspClient) read() rpcMsg {
	done := make(chan rpcMsg, 1)
	go func() {
		body, err := readMessage(c.out)
		if err != nil {
			close(done)
			return
		}
		var m rpcMsg
		json.Unmarshal(body, &m)
		done <- m
	}()
	select {
	case m, ok := <-done:
		if !ok {
			c.t.Fatal("the server closed its output")
		}
		return m
	case <-time.After(20 * time.Second):
		c.t.Fatal("no message from the server")
	}
	return rpcMsg{}
}

func (c *lspClient) call(method string, params any) json.RawMessage {
	c.next++
	id := c.next
	c.write(map[string]any{"id": id, "method": method, "params": params})
	for {
		m := c.read()
		if m.Method != "" {
			c.notes = append(c.notes, m)
			continue
		}
		var got int
		json.Unmarshal(m.ID, &got)
		if got != id {
			continue
		}
		if m.Error != nil {
			c.t.Fatalf("%s: %s", method, m.Error.Message)
		}
		b, _ := json.Marshal(m.Result)
		return b
	}
}

// diagnostics waits for the next publishDiagnostics.
func (c *lspClient) diagnostics() []string {
	for {
		var m rpcMsg
		if len(c.notes) > 0 {
			m, c.notes = c.notes[0], c.notes[1:]
		} else {
			m = c.read()
		}
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p struct {
			Diagnostics []struct {
				Range   lspRange `json:"range"`
				Message string   `json:"message"`
			} `json:"diagnostics"`
		}
		json.Unmarshal(m.Params, &p)
		var out []string
		for _, d := range p.Diagnostics {
			out = append(out, fmt.Sprintf("%d:%d %s", d.Range.Start.Line+1, d.Range.Start.Character+1, d.Message))
		}
		return out
	}
}

func TestLanguageServer(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "shapes.vl")
	if err := os.WriteFile(helper, []byte("pub fn area(w: int, h: int) -> int {\n    return w * h\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.vl")
	uri := pathToURI(main)
	src := `import "shapes.vl"

struct Point {
    x: int
    y: int
}

impl Point {
    fn sum(self) -> int {
        return self.x + self.y
    }
}

enum Color { Red, Green }

fn twice(n: int) -> int {
    let doubled = n * 2
    return doubled
}

let p = Point{x: 1, y: 2}
let c = Color.Red
print(twice(p.sum()))
print(area(2, 3))
let bad: int = "no"
`
	c := startLSP(t)
	c.call("initialize", map[string]any{"capabilities": map[string]any{}})
	c.write(map[string]any{"method": "initialized", "params": map[string]any{}})
	c.write(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "veyl", "version": 1, "text": src}}})

	diags := c.diagnostics()
	if len(diags) != 1 || !strings.Contains(diags[0], `"bad" is declared as int but the value is str`) || !strings.HasPrefix(diags[0], "25:") {
		t.Fatalf("diagnostics: %q", diags)
	}

	pos := func(line int, after string) map[string]any {
		l := strings.Split(src, "\n")[line-1]
		i := strings.Index(l, after)
		if i < 0 {
			t.Fatalf("no %q on line %d", after, line)
		}
		return map[string]any{"textDocument": map[string]any{"uri": uri},
			"position": map[string]any{"line": line - 1, "character": i + len(after)}}
	}
	defLine := func(r json.RawMessage) (string, int) {
		var l lspLocation
		if err := json.Unmarshal(r, &l); err != nil || l.URI == "" {
			t.Fatalf("no definition: %s", r)
		}
		return filepath.Base(uriToPath(l.URI)), l.Range.Start.Line + 1
	}

	// Definitions: a function, a method through a variable, a struct, a
	// local, a field, an imported function, an enum.
	for _, tc := range []struct {
		line  int
		after string
		file  string
		want  int
	}{
		{23, "print(tw", "main.vl", 16},
		{23, "p.su", "main.vl", 9},
		{21, "Poi", "main.vl", 3},
		{18, "return doub", "main.vl", 17},
		{10, "self.x", "main.vl", 4},
		{24, "print(ar", "shapes.vl", 1},
		{22, "Col", "main.vl", 14},
	} {
		f, l := defLine(c.call("textDocument/definition", pos(tc.line, tc.after)))
		if f != tc.file || l != tc.want {
			t.Errorf("definition at %d after %q: %s:%d, want %s:%d", tc.line, tc.after, f, l, tc.file, tc.want)
		}
	}

	// Hover: an inferred local's type, a function's signature, a builtin.
	hover := func(line int, after string) string {
		var h struct {
			Contents struct {
				Value string `json:"value"`
			} `json:"contents"`
		}
		json.Unmarshal(c.call("textDocument/hover", pos(line, after)), &h)
		return h.Contents.Value
	}
	if h := hover(18, "return doub"); !strings.Contains(h, "doubled: int") {
		t.Errorf("hover on doubled: %q", h)
	}
	if h := hover(23, "print(tw"); !strings.Contains(h, "fn twice(n: int) -> int") {
		t.Errorf("hover on twice: %q", h)
	}
	if h := hover(21, "let "); !strings.Contains(h, "p: Point") {
		t.Errorf("hover on p: %q", h)
	}

	// Completion after a dot: fields and methods, a library, variants.
	labels := func(r json.RawMessage) map[string]bool {
		var items []struct {
			Label string `json:"label"`
		}
		json.Unmarshal(r, &items)
		m := map[string]bool{}
		for _, it := range items {
			m[it.Label] = true
		}
		return m
	}
	edit := strings.Replace(src, "print(area(2, 3))", "p.\nos.\nColor.\ntw", 1)
	c.write(map[string]any{"method": "textDocument/didChange", "params": map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 2},
		"contentChanges": []map[string]any{{"text": edit}}}})
	c.diagnostics()
	at := func(line, char int) map[string]any {
		return map[string]any{"textDocument": map[string]any{"uri": uri},
			"position": map[string]any{"line": line - 1, "character": char}}
	}
	if got := labels(c.call("textDocument/completion", at(24, 2))); !got["x"] || !got["y"] || !got["sum"] {
		t.Errorf("after p.: %v", got)
	}
	if got := labels(c.call("textDocument/completion", at(25, 3))); !got["file"] || !got["args"] {
		t.Errorf("after os.: %v", got)
	}
	if got := labels(c.call("textDocument/completion", at(26, 6))); !got["Red"] || !got["Green"] {
		t.Errorf("after Color.: %v", got)
	}
	if got := labels(c.call("textDocument/completion", at(27, 2))); !got["twice"] || !got["print"] || !got["p"] || !got["while"] {
		t.Errorf("plain completion lacks twice, print, p or while")
	}

	// A variable that only the broken text has: "q." does not parse, and
	// q is in no earlier version, so this checks the text without the dot.
	edit2 := strings.Replace(src, "print(area(2, 3))", "let q = Point{x: 3, y: 4}\nq.", 1)
	c.write(map[string]any{"method": "textDocument/didChange", "params": map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 3},
		"contentChanges": []map[string]any{{"text": edit2}}}})
	c.diagnostics()
	if got := labels(c.call("textDocument/completion", at(25, 2))); !got["x"] || !got["sum"] {
		t.Errorf("after q.: %v", got)
	}

	// The outline.
	var syms []struct {
		Name string `json:"name"`
	}
	json.Unmarshal(c.call("textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": uri}}), &syms)
	var names []string
	for _, s := range syms {
		names = append(names, s.Name)
	}
	if strings.Join(names, " ") != "Point Point.sum Color twice" {
		t.Errorf("outline: %v", names)
	}

	c.call("shutdown", nil)
}
