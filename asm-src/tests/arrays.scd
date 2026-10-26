// Fixed-size arrays: values that copy on let, assignment, passing and
// returning; in locals, parameters, struct fields, globals, closures,
// data enum variants and lists of structs; passed to C as a pointer.
struct Particle {
    name: str
    pos: [3]f32
    hits: int
}

fn total(v: [4]i32) -> int {
    let t = 0
    for x in v {
        t += int(x)
    }
    v[0] = 99
    return t
}

fn make() -> [4]i32 {
    let r: [4]i32 = [9, 8, 7]
    return r
}

let a: [4]i32 = [1, 2, 3, 4]
let b = a
b[0] = 100
print("{a[0]} {b[0]} {len(a)}")
print("{total(a)} {total(make())} {a[0]}")
let z: [8]u8
z[7] = 255
z[6] += 3
print(z)
let p = Particle{name: "p", pos: [1.5, 2.5, 3.5], hits: 2}
p.pos[1] = 9.25
print("{p.pos[1]} {p}")
let q = p
q.pos[0] = 0.0
print("{p.pos[0]} {q.pos[0]}")
for i, v in a {
    print("{i}={v}")
}
print("{a == b} {a == a}")
b = a
print(b)
let e = Particle{name: "e"}
print(e)
var g: [3]float = [0.5]
fn bump() {
    g[2] = g[0] + 1.0
}
bump()
print(g)
let fns: []fn() -> int = []
for k in 0..3 {
    let c: [2]int = [k, k * 10]
    push(fns, fn() -> int { return c[0] + c[1] })
}
for f in fns {
    print(f())
}
let big: [20000]int
big[19999] = 7
print(big[19999] + len(big))

extern fn qsort(base: ptr, n: int, size: int, cmp: fn(ptr, ptr) -> int)
extern fn memset(p: *u8, c: int, n: int) -> ptr

struct P {
    tag: str
    v: [2]f32
}

let xs: [6]i32 = [5, -2, 9, 0, 3, 3]
qsort(xs, len(xs), 4, fn(a: int, b: int) -> int { return mem.readI32(a) - mem.readI32(b) })
print(xs)
let bytes: [5]u8
memset(bytes, 65, 4)
print(bytes)
print(mem.str(bytes))
let ps: []P = []
push(ps, P{tag: "a", v: [1.0, 2.0]})
push(ps, P{tag: "b"})
ps[1].v[0] = 7.5
print(ps)
let twice = fn(m: [3]int) -> [3]int {
    m[0] = m[0] * 2
    return m
}
let m: [3]int = [4, 5, 6]
print(twice(m))
print(m)
let flags: [4]bool
flags[2] = true
print(flags)
let d: [2]float = [1.25, -0.0]
let e: [2]float = [1.25, 0.0]
print(d == e)
let i = 4
print(xs[i])

enum Cmd {
    Move(to: [2]int)
    Stop
}
let c = Cmd.Move([3, 4])
let d = Cmd.Move([3, 4])
print(c)
print(c == d)
match c {
    Cmd.Move(to) => print(to[1])
    Cmd.Stop => print("stop")
}

struct J { v: [2]int, ok: [2]bool }
print(json.encode(J{v: [1, 2], ok: [true, false]}))
