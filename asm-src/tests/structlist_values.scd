// Structs in lists keep value semantics: what goes into a list, comes
// out of it, or is passed along is a copy, and only an element written
// through the list changes the list. Pinned before the storage of struct
// elements changes, so the change cannot alter any of it.
struct P {
    name: str
    a: int
    b: float
}

impl P {
    fn bump(self, d: int) {
        self.a += d
    }
}

fn grow(p: P) -> P {
    p.a += 1
    return p
}

fn readonly(p: P) -> int {
    return p.a * 2
}

struct Box {
    items: []P
    tag: int
}

fn show(xs: []P) -> str {
    let s = ""
    for p in xs {
        s = s + "{p.name}={p.a} "
    }
    return s
}

let xs: []P = []
let q = P{name: "q", a: 1, b: 0.5}
push(xs, q)
q.a = 2
push(xs, q)
q.a = 3
print(show(xs))                 // q=1 q=2

xs[0] = q
q.a = 4
print(show(xs))                 // q=3 q=2

let t = xs[1]
t.a = 50
print(show(xs))                 // unchanged: q=3 q=2

xs[1].a = 60
xs[0].b += 1.5
print(show(xs))                 // q=3 q=60
print(xs[0].b)                  // 2

xs[0] = xs[1]
xs[1].a = 61
print(show(xs))                 // q=60 q=61

let g = grow(xs[0])
print(g.a)                      // 61
print(xs[0].a)                  // 60
print(readonly(xs[1]))          // 122

xs[1].bump(5)
print(xs[1].a)                  // 66

let b = Box{items: xs, tag: 1}
b.items[0].a = 7
print(show(xs))                 // the same list: q=7 q=66
let c = b
c.tag = 2
c.items[1].a = 8
print(b.tag)                    // 1: a struct copy
print(xs[1].a)                  // 8: a list is shared

let ys: []P = []
for i in 0..5 {
    push(ys, P{name: "n{i}", a: i, b: 0.0})
}
print(pop(ys).a)                // 4
insert(ys, 1, P{name: "ins", a: 100, b: 0.0})
print(show(ys))                 // n0=0 ins=100 n1=1 n2=2 n3=3
removeAt(ys, 0)
print(show(ys))                 // ins=100 n1=1 n2=2 n3=3
print(ys == ys)                 // true
let zs: []P = []
for p in ys {
    push(zs, p)
}
zs[0].a = 0
print(ys == zs)                 // false
zs[0].a = 100
print(ys == zs)                 // true

let counter = 0
let add = fn(p: P) {
    counter += p.a
}
for i in 0..len(ys) {
    add(ys[i])
}
print(counter)                  // 106
clear(ys)
print(len(ys))                  // 0
print(len(zs))                  // 4
