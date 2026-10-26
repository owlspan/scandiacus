// first, last, pop and removeAt hand back a value, not the element:
// writing to the result must not change the list, and a popped value
// must not change when the slot is reused. first and last on 1.100.0
// return the element object itself, so the first two checks fail until
// struct elements are stored in place and copied out. Move this to tests/
// with its .out when that is the default.
struct V {
    a: int
    b: int
}

let xs: []V = [V{a: 1, b: 2}, V{a: 3, b: 4}]
let f = first(xs)
f.a = 100
print(xs[0].a)                  // 1
let l = last(xs)
l.b = 200
print(xs[1].b)                  // 4
let p = pop(xs)
push(xs, V{a: 7, b: 8})
print(p.a)                      // 3
print(xs[1].a)                  // 7
