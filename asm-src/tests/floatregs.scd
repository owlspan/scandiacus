// Floats in registers: locals promoted into xmm6-xmm15 have to survive
// calls into functions that promote their own, recursion, and native
// code; temporaries pooled into xmm2-xmm5 must never cross a call.

struct Body {
    x: float
    v: float
}

fn advance(b: Body, dt: float) -> Body {
    let a = -b.x * 4.0
    let v = b.v + a * dt
    return Body{x: b.x + v * dt, v: v}
}

fn energy(b: Body) -> float {
    return 0.5 * b.v * b.v + 2.0 * b.x * b.x
}

fn series(n: int) -> float {
    let total = 0.0
    let sign = 1.0
    let k = 0
    while k < n {
        total = total + sign / float(2 * k + 1)
        sign = -sign
        k += 1
    }
    return total * 4.0
}

fn depth(x: float, n: int) -> float {
    if n == 0 {
        return x
    }
    let kept = x * 1.5
    let inner = depth(x + 1.0, n - 1)
    return kept + inner - x * 1.5
}

let b = Body{x: 1.0, v: 0.0}
let e0 = energy(b)
let t = 0.0
let dt = 0.001
while t < 1.0 {
    b = advance(b, dt)
    t = t + dt
}
print(b.x)
print(b.v)
print(energy(b) - e0 < 0.01)

let pi = series(20000)
let s = sqrt(pi)
let c = pi / 2.0
print(pi)
print(s * s - pi < 0.000001)
print(c > 1.5 && c < 1.6)
print(depth(0.25, 12))

let xs: []float = []
let acc = 0.0
for i in 0..10 {
    acc = acc * 0.5 + float(i)
    push(xs, acc)
}
print(xs)
let lo = xs[0]
let hi = xs[0]
for v in xs {
    if v < lo {
        lo = v
    }
    if v > hi {
        hi = v
    }
}
print("{lo} {hi} {hi - lo} {-hi}")
