// Counted loops over []float lists run two elements at a time
// (vecloop.go). Every case must print what the one-at-a-time loop does.
let n = 7
let a: []float = []
let b: []float = []
let c: []float = []
for i in 0..n {
    push(a, float(i) + 0.5)
    push(b, float(i) * 2.0)
    push(c, 1.0)
}

// An odd count: three pairs, then one more the ordinary loop does.
for j in 0..n {
    c[j] += 3.0 * a[j]
}
print(c)

// An offset on both sides, and subtraction in both orders.
let off = 2
for j in 0..n - off {
    c[off + j] -= b[j]
}
print(c)
for j in 0..n {
    c[j] = b[j] - a[j]
}
print(c)

// Plain assignment from a product of two lists and a sum.
let k = 0.25
for j in 0..n {
    c[j] = a[j] * b[j]
}
print(c)
for j in 1..n {
    c[j] = a[j] + k
}
print(c)

// Two names for one list, read at another place: must stay in order.
let d = c
for j in 0..n - 1 {
    c[j + 1] = d[j] + 1.0
}
print(c)

// The list written to read at another index: not done two at a time.
for j in 0..n - 1 {
    b[j] = b[j + 1] * 0.5
}
print(b)

// An empty range and a one-element range.
for j in 3..3 {
    c[j] = 99.0
}
for j in 3..4 {
    c[j] = 42.0
}
print(c)

// Too much to fit in two registers: left as it is.
for j in 0..n {
    c[j] = a[j] * b[j] + a[j] * k
}
print(c)
