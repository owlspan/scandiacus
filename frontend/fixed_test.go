package frontend

import (
	"strings"
	"testing"
)

func TestFixedWidthAccepted(t *testing.T) {
	ok := `
let a: u8 = 255
let b: i8 = -128
let c: u64 = 0xFFFFFFFFFFFFFFFF
let d: u32 = (255 << 16) | 7
let e: f32 = 0.1
let f: f32 = 3
let g = a + 1
let h = e * 2.5
let i: i16 = -(1 << 15)
let j = a << 3
let k = a >> j
let n: int = 3
let m = a << n
let p: ?u16 = 7
let xs: []i32 = [1, -2, 3]
a += 1
e /= 3
c >>= 1
let q = ~a
let r = -b
let s = a == 3
`
	if errs := checked(t, ok); len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
}

func TestFixedWidthErrors(t *testing.T) {
	bad := []struct{ src, want string }{
		{"let a: u8 = 256", "256 does not fit in u8, which holds 0 to 255"},
		{"let a: i8 = -129", "does not fit in i8"},
		{"let a: u32 = -1", "does not fit in u32"},
		{"let a: u64 = 0x1FFFFFFFFFFFFFFFF", "does not fit in u64"},
		{"let a: u8 = 1\nlet b: i8 = 1\nlet c = a + b", "cannot mix u8 and i8"},
		{"let a: u8 = 1\nlet n = 1\nlet c = a + n", "cannot mix u8 and int"},
		{"let a: i32 = 1\nlet n: int = a", "declared as int but the value is i32"},
		{"let n = 5\nlet a: u8 = n", "declared as u8 but the value is int"},
		{"let a: u8 = 1\na += 300", "300 does not fit in u8"},
		{"let a: u8 = 1\nlet n = 2\na += n", "cannot apply += with int"},
		{"let n = 1\nlet a: u8 = 1\nn += a", "cannot apply += with u8"},
		{"let e: f32 = 1\nlet c = e % 2", "works on integers, got f32"},
		{"let e: f32 = 1\nlet c = e << 1", "works on integers"},
		{"let a: u8 = 1\nlet c = a << -1", "cannot be negative"},
		{"let a: u8 = 1\nlet c = a + 1.5", "cannot mix u8 and float"},
		{"let e: f32 = 1\nlet x = 2.0\nlet c = e + x", "cannot mix f32 and float"},
		{"let a: u8 = 1 / 0", "divides by zero"},
	}
	for _, b := range bad {
		errs := strings.Join(checked(t, b.src), "\n")
		if !strings.Contains(errs, b.want) {
			t.Errorf("%q: want an error containing %q, got %q", b.src, b.want, errs)
		}
	}
}

func TestConstIntWraps(t *testing.T) {
	for _, c := range []struct {
		v    int64
		t    string
		want string
	}{
		{200, "i8", "-56"},
		{300, "u8", "44"},
		{-1, "u16", "65535"},
		{-1, "u64", "-1"},
		{40000, "i16", "-25536"},
	} {
		lit := &IntLit{Val: itoa64(c.v)}
		v, _ := ConstInt(lit)
		if got := wrapConst(v, FixedOf(c.t)).String(); got != c.want {
			t.Errorf("%s(%d) = %s, want %s", c.t, c.v, got, c.want)
		}
	}
}

func itoa64(v int64) string {
	if v < 0 {
		return "-" + itoa64(-v)
	}
	if v < 10 {
		return string(rune('0' + v))
	}
	return itoa64(v/10) + string(rune('0'+v%10))
}

func TestAddrLocalEscapes(t *testing.T) {
	ok := `
fn put(out: *i32) {
    *out = 3
}
fn fine() -> i32 {
    let x: i32 = 0
    put(&x)
    let p = &x
    let q = p
    *q += 1
    return x
}
`
	if errs := checked(t, ok); len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	bad := []struct{ src, want string }{
		{"fn f() -> *i32 {\n    let x: i32 = 5\n    return &x\n}", "cannot be returned"},
		{"fn f() -> *i32 {\n    let x: i32 = 5\n    let p = &x\n    let q = p + 1\n    return q\n}", "cannot be returned"},
		{"fn f() -> int {\n    let x = 5\n    return (&x) as int\n}", "cannot be returned"},
		{"var keep: *i32 = nil\nfn f() {\n    let x: i32 = 5\n    keep = &x\n}", "outlives the function"},
		{"extern struct B { p: *i32 }\nfn f(b: B) {\n    let x: i32 = 1\n    b.p = &x\n}", "outlives the function"},
		{"fn f() -> []*i32 {\n    let x: i32 = 5\n    return [&x]\n}", "put in a list"},
		{"fn f() -> fn() -> i32 {\n    let x: i32 = 1\n    let p = &x\n    return fn() -> i32 { return *p }\n}", "this closure uses p"},
		{"fn f() {\n    let x = 1\n    let g = fn() { let p = &x }\n}", "cannot take its address"},
		{"let s = \"a\"\nlet p = &s", "only a number, a bool or a pointer variable"},
		{"const k = 5\nlet p = &k", "k is const"},
		{"var g = 5\nfn f() {\n    let p = &g\n}", "is a global"},
		{"for i in 0..3 {\n    let p = &i\n}", "i is a loop variable"},
	}
	for _, b := range bad {
		errs := strings.Join(checked(t, b.src), "\n")
		if !strings.Contains(errs, b.want) {
			t.Errorf("%q: want an error containing %q, got %q", b.src, b.want, errs)
		}
	}
}

func TestNewChecks(t *testing.T) {
	ok := `
extern struct V { x: f32, y: f32 }
let a = new V
let b = new V{x: 1}
let c = new [4]V
let d = new i32
let e = new [16]u8
let f = new *V
let g: *V = b
let n: u32 = 3
let h = new [n]i32
`
	if errs := checked(t, ok); len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	bad := []struct{ src, want string }{
		{"struct P { x: int }\nlet p = new P", "is a Veyl struct"},
		{"let p = new str", "new allocates C memory"},
		{"let p = new [1.5]u8", "must be an integer"},
		{"extern struct V { x: f32 }\nlet p = new V{y: 1}", "no field"},
	}
	for _, b := range bad {
		errs := strings.Join(checked(t, b.src), "\n")
		if !strings.Contains(errs, b.want) {
			t.Errorf("%q: want an error containing %q, got %q", b.src, b.want, errs)
		}
	}
}
