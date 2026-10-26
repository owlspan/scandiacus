// Float literals and negations inside loops with no call: nested
// loops, a literal used twice, -0.0 and NaN through a negation, a
// compare against a literal, and the same loop with a call in it.
struct P {
    x: float
    vx: float
}

fn bump(v: float) -> float {
    return v + 0.25
}

let ps: []P = []
for i in 0..4 {
    push(ps, P{x: float(i) * 40.0, vx: 1.0})
}
for frame in 0..30 {
    for i in 0..len(ps) {
        ps[i].x += ps[i].vx * 0.5
        if ps[i].x > 100.0 { ps[i].vx = -ps[i].vx }
        if ps[i].x < 0.0 { ps[i].vx = -ps[i].vx }
    }
}
for p in ps {
    print(str(p.x) + " " + str(p.vx))
}

// the same literal twice, and a negation of a loop-carried value
let a = 1.5
let b = 0.0
for i in 0..6 {
    a = -a * 2.0 + 0.5
    b += a * 2.0
}
print(str(a) + " " + str(b))

// negation keeps the sign of zero and passes NaN through
let z = 0.0
let n = 0.0 / 0.0
let zs = 0.0
for i in 0..3 {
    z = -z
    zs += 1.0
}
print(str(z) + " " + str(zs))
print(-n != -n)

// a call in the loop: literals stay where they were
let c = 0.0
for i in 0..5 {
    c = bump(c) * 2.0 - 1.0
    if c > 3.0 { c = -c }
}
print(c)

// an infinity compared against a literal inside the loop
let inf = 1.0 / 0.0
let hits = 0
for i in 0..4 {
    if inf > 1000000.0 { hits += 1 }
    if -inf < -1000000.0 { hits += 1 }
}
print(hits)
