// Lists of structs whose fields are all plain numbers or bools: the
// shape a list can hold the fields in place. Everything here has to read
// the same whichever way the list stores them.
struct V {
    x: float
    y: float
    n: int
    on: bool
}

impl V {
    fn nudge(self, d: float) {
        self.x += d
        self.n += 1
    }
    fn mag(self) -> float {
        return self.x * self.x + self.y * self.y
    }
}

struct World {
    vs: []V
    gen: int
}

fn total(vs: []V) -> float {
    let t = 0.0
    for i in 0..len(vs) {
        t += vs[i].x + vs[i].y
    }
    return t
}

fn lift(v: V) -> V {
    v.y += 10.0
    return v
}

let vs: []V = []
for i in 0..40 {
    push(vs, V{x: float(i), y: 1.0, n: i, on: i % 2 == 0})
}
print(len(vs))                      // 40
print(total(vs))                    // 780 + 40 = 820

// a literal with fields left out: zero values
push(vs, V{x: 5.0})
print(vs[40].n)                     // 0
print(vs[40].on)                    // false
print(vs[40].y)                     // 0

// element writes, compound writes, methods
vs[3].x = 100.0
vs[3].y += 0.5
vs[4].nudge(0.25)
vs[4].nudge(0.25)
print(vs[3].x + vs[3].y)            // 101.5
print(vs[4].x)                      // 4.5
print(vs[4].n)                      // 6
print(vs[5].mag())                  // 26

// copies out of the list and into it
let c = vs[3]
c.x = -1.0
print(vs[3].x)                      // 100
vs[0] = c
c.x = -2.0
print(vs[0].x)                      // -1
vs[1] = vs[2]
vs[2].x = 77.0
print(vs[1].x)                      // 2

let l = lift(vs[6])
print(l.y)                          // 11
print(vs[6].y)                      // 1

// insert and removeAt shift whole elements
insert(vs, 1, V{x: 9.0, y: 9.0, n: 9, on: true})
print(vs[1].x)                      // 9
print(vs[2].x)                      // 2
let gone = removeAt(vs, 0)
print(gone.x)                       // -1
vs[0].x = 55.0
print(gone.x)                       // -1: a copy
print(len(vs))                      // 41
print(vs[0].x)                      // 55

// pop hands back a copy; the next push reuses the slot
let top = pop(vs)
push(vs, V{x: 1000.0})
print(top.x)                        // 5
print(vs[40].x)                     // 1000

// a list held by a struct, copied with the struct
let w = World{vs: vs, gen: 1}
w.vs[7].x = 7.5
print(vs[7].x)                      // 7.5: the list is shared
print(w.gen)

// loops
let sum = 0
for v in vs {
    sum += v.n
}
print(sum)
for v in vs {
    v.n = 1
}
sum = 0
for i, v in vs {
    sum += v.n + i
}
print(sum)                          // 41 + 820 = 861

// equality and printing
let a: []V = [V{x: 1.0, y: 2.0, n: 3, on: true}, V{x: 4.0, y: 5.0, n: 6, on: false}]
let b: []V = []
for v in a {
    push(b, v)
}
print(a == b)                       // true
b[1].y = 0.0
print(a == b)                       // false
print(a)

// growth past several doublings keeps every element
let big: []V = []
for i in 0..1000 {
    push(big, V{x: float(i), n: i})
}
let ok = true
for i in 0..1000 {
    if big[i].n != i || big[i].x != float(i) {
        ok = false
    }
}
print(ok)                           // true

// map, filter and reduce see the elements
let xs = map(a, fn(v: V) -> float { return v.x })
print(xs)                           // [1, 4]
let hi = filter(big, fn(v: V) -> bool { return v.n >= 998 })
print(len(hi))                      // 2
print(hi[1].n)                      // 999
