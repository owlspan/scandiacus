// A struct as a map key: lookups, updates and removal, sorted printing
// and iteration field by field, keys the map copies on the way in and
// out, nested structs, enums and bools inside a key, and many keys.
enum Dir {
    north
    south
}

struct P {
    x: int
    y: int
}

struct K {
    name: str
    at: P
    d: Dir
    on: bool
}

let m: {P: str} = {}
let a = P{x: 1, y: 2}
m[a] = "a"
m[P{x: 0, y: 5}] = "b"
m[P{x: 1, y: -3}] = "c"
a.x = 99
print(m[P{x: 1, y: 2}])
print(has(m, a))
print(len(m))
print(m)
for k, v in m {
    k.x = 1000
    print("{k.x} {v}")
}
let ks = keys(m)
ks[0].y = 777
print(m)
m[P{x: 1, y: 2}] = "again"
print(m[P{x: 1, y: 2}])
remove(m, P{x: 0, y: 5})
print(m)

let n: {K: int} = {}
n[K{name: "b", at: P{x: 1, y: 1}, d: Dir.south, on: true}] = 1
n[K{name: "a", at: P{x: 2, y: 0}, d: Dir.north, on: false}] = 2
n[K{name: "a", at: P{x: 1, y: 9}, d: Dir.north, on: false}] = 3
print(n)
print(n[K{name: "a", at: P{x: 2, y: 0}, d: Dir.north, on: false}])
let big: {P: int} = {}
for i in 0..20000 {
    big[P{x: i % 100, y: i / 100}] = i
}
print(len(big))
print(big[P{x: 42, y: 199}])
let a: {P: int} = {}
let b: {P: int} = {}
a[P{x: 1, y: 2}] = 5
b[P{x: 1, y: 2}] = 5
print(a == b)
b[P{x: 3, y: 2}] = 1
print(a == b)
let lit = {P{x: 2, y: 2}: "two", P{x: 1, y: 1}: "one"}
print(lit)
print(str(lit))
