package main

import (
	"strings"
	"testing"
)

// TestDoc writes the reference for a small library: only what it
// exports, methods under their struct, each with the comment above it,
// and the file's opening comment as the introduction.
func TestDoc(t *testing.T) {
	src := `// Geometry helpers.

// A point on the grid.
pub struct Point {
    x: int
    y: int
}

impl Point {
    // How far from the origin, along the grid.
    fn manhattan(self) -> int {
        return self.x + self.y
    }
}

// Adds two points.
pub fn add(a: Point, b: Point) -> Point {
    return Point{x: a.x + b.x, y: a.y + b.y}
}

// Private, so left out.
fn helper() {
}

let trailing = 1 // about this line only
`
	entries, errs := docEntries("geo.scd", src)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	got := docMarkdown("geo", docIntro(src), entries)
	want := "# geo\n\nGeometry helpers.\n\n## Contents\n\n- [struct Point](#struct-point)\n- [fn add](#fn-add)\n\n" +
		"## struct Point\n\n```scandiacus\nstruct Point {\n    x: int\n    y: int\n}\n```\n\nA point on the grid.\n\n" +
		"### Point.manhattan\n\n```scandiacus\nfn manhattan(self) -> int\n```\n\nHow far from the origin, along the grid.\n\n" +
		"## fn add\n\n```scandiacus\nfn add(a: Point, b: Point) -> Point\n```\n\nAdds two points.\n\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s\n%s", got, want, firstDiff(got, want))
	}
	if h := docHTML("geo", entries); !strings.Contains(h, `<h2 id="fn-add">`) || !strings.Contains(h, "<p>Adds two points.</p>") {
		t.Errorf("html:\n%s", h)
	}
}
