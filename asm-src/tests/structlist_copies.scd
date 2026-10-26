// The documented behaviour of the functions that read a list: they
// return a new list and leave the original alone, and a struct assigned
// or stored is a copy. reverse, slice and filter on 1.100.0 return lists
// that share the original's element objects, so a write through the
// result shows in the original. This test says what should happen. It
// lives in tests/pending because it fails until the storage of struct
// elements changes; move it to tests/ with its .out then.
struct P {
    a: int
    b: int
}

let xs: []P = [P{a: 1, b: 2}, P{a: 3, b: 4}, P{a: 5, b: 6}]

let r = reverse(xs)
r[0].a = 99
print(xs[2].a)                  // 5

let s = slice(xs, 0, 2)
s[0].a = 77
print(xs[0].a)                  // 1

let f = filter(xs, fn(p: P) -> bool { return p.a > 0 })
f[1].b = 55
print(xs[1].b)                  // 4

let m = map(xs, fn(p: P) -> P { return p })
m[0].b = 66
print(xs[0].b)                  // 2

print(r[0].a)                   // 99
print(s[0].a)                   // 77
print(f[1].b)                   // 55
