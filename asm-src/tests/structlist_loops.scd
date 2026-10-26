// What a loop variable is when it ranges over a list of structs, and
// the same for a map's values: the element itself, not a copy, so a
// field assigned through it changes the container. This is how the
// compiler behaves today and programs rely on it (`for p in ps { p.x
// += 1 }`), so it is pinned here and kept by any change of storage.
struct P {
    a: int
    b: int
}

let xs: []P = []
for i in 0..4 {
    push(xs, P{a: i, b: 0})
}
for p in xs {
    p.b = p.a * 10
}
let s = ""
for p in xs {
    s = s + "{p.a}/{p.b} "
}
print(s)                        // 0/0 1/10 2/20 3/30

for i, p in xs {
    p.a += i
}
s = ""
for p in xs {
    s = s + "{p.a} "
}
print(s)                        // 0 2 4 6

// assigning the whole variable is a copy, as everywhere
for p in xs {
    let c = p
    c.a = 99
}
print(xs[0].a)                  // 0

// a read-only loop and an index loop agree
let tot = 0
for p in xs {
    tot += p.a + p.b
}
let tot2 = 0
for i in 0..len(xs) {
    tot2 += xs[i].a + xs[i].b
}
print(tot)                      // 72
print(tot2)                     // 72

let m: {str: P} = {}
m["k"] = P{a: 1, b: 2}
for k, v in m {
    v.a = 50
}
print(m["k"].a)                 // 50
