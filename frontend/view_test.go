package frontend

import (
	"strings"
	"testing"
)

func TestExternStructLayout(t *testing.T) {
	src := `
extern struct Vec3 { x: f32, y: f32, z: f32 }
extern struct Mixed {
    a: u8
    b: i32
    c: u16
    d: f64
    name: [5]u8
    pos: Vec3
}
extern struct Pinned {
    first: i32
    hp: f32 at 0x100
    ammo: i16
}
`
	lx := NewLexer("t.vl", src)
	prog := NewParser("t.vl", lx.Scan()).ParseProgram()
	ck := NewChecker("t.vl", EmptyLibrary{})
	ck.Check(prog)
	if errs := append(lx.Errors, ck.Errors...); len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}

	offsets := func(d *StructDecl) string {
		var parts []string
		for _, f := range d.Fields {
			parts = append(parts, f.Name+"@"+itoa(f.Offset))
		}
		return strings.Join(parts, " ") + " size " + itoa(d.Size) + " align " + itoa(d.Align)
	}
	want := []string{
		"x@0 y@4 z@8 size 12 align 4",
		// b is aligned to 4, d to 8; the array has byte alignment and
		// the nested Vec3 four-byte.
		"a@0 b@4 c@8 d@16 name@24 pos@32 size 48 align 8",
		"first@0 hp@256 ammo@260 size 264 align 4",
	}
	for i, d := range prog.Structs {
		if got := offsets(d); got != want[i] {
			t.Errorf("%s: got %s, want %s", d.Name, got, want[i])
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestExternStructUse(t *testing.T) {
	ok := `
extern struct Vec3 { x: f32, y: f32, z: f32 }
extern struct Player { hp: i32, pos: Vec3, name: [16]u8 }
extern fn GetThing(p: Player, buf: bytes) -> Player
let p = Player(4096)
let hp: i32 = p.hp
let x: f32 = p.pos.x
let addr: int = p.name
p.hp = 5
p.hp -= 1
p.pos.y = 2.5
let size: int = Player.size
let local = Vec3{x: 1.0, z: 3.0}
`
	if errs := checked(t, ok); len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}

	bad := []struct{ src, want string }{
		{"extern struct A { x: nope }", "unknown field type"},
		{"struct B { n: int }\nextern struct A { b: B }", "has no C layout"},
		{"extern struct A { a: A }", "contains itself"},
		{"extern struct A { n: [4]u8 }\nlet a = A(0)\na.n = 1", "is an array"},
		{"extern struct A { n: [4]u8 }\nlet a = A{n: 1}", "cannot be given in a literal"},
		{"extern struct A { n: i32 }\nlet a = A(\"x\")", "needs an int address"},
		{"extern struct A { n: i32 }\nlet s = A.count", "A.size is its size"},
		{"extern struct A { n: i32 }\nlet a = A(0)\nlet s: str = a.n", "declared as str"},
	}
	for _, b := range bad {
		errs := strings.Join(checked(t, b.src), "\n")
		if !strings.Contains(errs, b.want) {
			t.Errorf("%q: want an error containing %q, got %q", b.src, b.want, errs)
		}
	}
}

func TestExportFn(t *testing.T) {
	src := `
extern struct Vec2 { x: f32, y: f32 }
export fn add(a: int, b: int) -> int { return a + b }
export fn scale(v: Vec2, k: f32) { v.x = v.x * k }
fn export(n: int) -> int { return n }
let export2 = export(3)
`
	lx := NewLexer("t.vl", src)
	ps := NewParser("t.vl", lx.Scan())
	prog := ps.ParseProgram()
	ck := NewChecker("t.vl", EmptyLibrary{})
	ck.Check(prog)
	if errs := append(append(lx.Errors, ps.Errors...), ck.Errors...); len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	var exported []string
	for _, f := range prog.Funcs {
		if f.Export {
			exported = append(exported, f.Name)
		}
	}
	if strings.Join(exported, ",") != "add,scale" {
		t.Errorf("exported %v, want add and scale", exported)
	}

	errs := strings.Join(checked(t, "export fn bad(xs: []int) -> int { return 0 }"), "\n")
	if !strings.Contains(errs, "cannot take []int") {
		t.Errorf("a list parameter was not refused: %q", errs)
	}
}
