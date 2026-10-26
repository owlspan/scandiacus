// Counted loops with literal bounds are unrolled while lowering. Each
// section here prints a result that a while-loop version of the same
// loop must also give, and the loops that must stay loops are checked
// for the right answer too.

struct Grid {
    cells: [6]int
    w: [3]float
}

// A 4x4 float matrix product, the shape the unroller was made for.
fn mul(a: [16]float, b: [16]float) -> [16]float {
    let r: [16]float
    for i in 0..4 {
        for j in 0..4 {
            let s = 0.0
            for k in 0..4 {
                s += a[i * 4 + k] * b[k * 4 + j]
            }
            r[i * 4 + j] = s
        }
    }
    return r
}

let ma: [16]float
let mb: [16]float
for i in 0..16 {
    ma[i] = float(i) + 1.0
    mb[i] = float(16 - i) * 0.5
}
let mc = mul(ma, mb)
for i in 0..4 {
    print("{mc[i * 4]} {mc[i * 4 + 1]} {mc[i * 4 + 2]} {mc[i * 4 + 3]}")
}

// Ranges: inclusive, negative step, one trip, 16 trips, 17 trips.
let t1 = 0
for i in 1..=5 {
    for j in -2..=2 {
        t1 += i * j * j + i
    }
}
let w1 = 0
let wi = 1
while wi <= 5 {
    let wj = -2
    while wj <= 2 {
        w1 += wi * wj * wj + wi
        wj += 1
    }
    wi += 1
}
print("{t1} {w1}")

let t2 = 0
for i in 10..0 step -3 {
    t2 = t2 * 10 + i
}
for i in 10..=1 step -3 {
    t2 = t2 * 10 + i
}
print(t2)

let t3 = 0
for i in 7..8 {
    t3 += i
}
for i in 3..3 {
    t3 += 1000
}
print(t3)

let t16 = 0
for i in 0..16 {
    t16 += i * i
}
let t17 = 0
for i in 0..17 {
    t17 += i * i
}
print("{t16} {t17}")

// An if with else-if, a match, a string built in the body, and an
// early return from inside a loop in a helper.
let acc = 0
let text = ""
for i in 0..8 {
    if i % 3 == 0 {
        acc += 1
    } else if i % 3 == 1 {
        acc += 10
    } else {
        acc += 100
    }
    match i {
        2, 3 => acc += 1000
        5 => acc += 5000
        else => acc += 0
    }
    text = "{text}[{i}:{acc}]"
}
print(acc)
print(text)

fn firstOver(limit: int) -> int {
    for i in 0..10 {
        for j in 0..3 {
            if i * 3 + j > limit {
                return i * 3 + j
            }
        }
    }
    return -1
}
print("{firstOver(4)} {firstOver(20)} {firstOver(29)} {firstOver(50)}")

// Loops that must not unroll.
let b1 = 0
for i in 0..10 {
    if i == 6 {
        break
    }
    b1 += i
}
let c1 = 0
for i in 0..10 {
    if i % 2 == 0 {
        continue
    }
    c1 += i
}
let a1 = 0
for i in 0..10 {
    i = i + 1
    a1 += i
}
print("{b1} {c1} {a1}")

let fns: []fn() -> int = []
for i in 0..4 {
    push(fns, fn() -> int { return i * 7 })
}
let cs = ""
for f in fns {
    cs = "{cs}{f()} "
}
print(cs)

for i in 0..3 {
    defer print("end of trip {i}")
    print("trip {i}")
}

let n1 = 0
for i in 0..4 {
    let k = 0
    while k < i {
        n1 += k + 1
        k += 1
    }
}
print(n1)

// Constant indexes into a local array, an array parameter, an array
// field and through a pointer.
fn sumParam(a: [5]int) -> int {
    let s = 0
    for i in 0..5 {
        s += a[i] * (i + 1)
    }
    return s
}

fn fill(p: *f64) {
    for i in 0..4 {
        p[i] = float(i) * 1.5 + 0.25
    }
}

fn sumPtr(p: *f64) -> float {
    let s = 0.0
    for i in 0..4 {
        s += p[i]
    }
    return s
}

let loc: [5]int
for i in 0..5 {
    loc[i] = i * i + 1
}
print(sumParam(loc))
print(loc)

let g = Grid{}
for i in 0..6 {
    g.cells[i] = i * 3
}
for i in 0..3 {
    g.w[i] = float(i) + 0.5
    g.cells[i] += int(g.w[i] * 2.0)
}
print(g)

let buf = new [4]f64
fill(buf)
print(sumPtr(buf))
delete(buf)

// Inclusive ranges, in a helper called with growing values.
fn tri(n: int) -> int {
    let s = 0
    for i in 0..=3 {
        s += n + i
    }
    return s
}
let tt = 0
let q = 0
while q < 5 {
    tt += tri(q)
    q += 1
}
print(tt)
