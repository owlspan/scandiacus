// Division and remainder of values computed from loop counters. Where
// the counter's bounds show the dividend cannot be negative, the
// division is done unsigned; everywhere else it must still round
// towards zero.
let s = 0
for i in 0..2000 {
    s += (i * i) % 7 + (i >> 3) + (i * 3) / 10 + i % 8 + (i * 5) / 16
}
print(s)

// A counter that goes negative: signed.
let t = 0
for i in -50..50 {
    t += (i * 7) % 6 + i / 4 + i % 8
}
print(t)

// Inclusive, with a step.
let u = 0
for i in 3..=99 step 4 {
    u += (i * i) % 1000003 + i / 3
}
print(u)

// Big enough that i * i overflows: no range, so signed, and the
// wrapped products are negative.
let v = 0
for i in 3037000490..3037000510 {
    v += (i * i) % 1000
}
print(v)

// The body writes the counter: no range.
let w = 0
for i in 0..20 {
    w += (0 - i * 13) % 5
    if i == 3 { i = -40 }
    if i < 0 { i = 25 }
}
print(w)

// Not constant bounds, from a function: signed as before.
fn upto(n: int) -> int {
    let r = 0
    for i in 0..n {
        r += i % 9 + i / 7
    }
    return r
}
print(upto(100))
