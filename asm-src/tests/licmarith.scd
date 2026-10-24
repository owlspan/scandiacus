// Arithmetic that does not change in a loop is worked out before it
// (hoistPure in licm.go). Each case has the answer worked out by hand.
let n = 5
let grid: []int = []
for i in 0..n * n {
    push(grid, i)
}

// i * n is the same on every trip of the j loop.
let diag = 0
for i in 0..n {
    for j in 0..n {
        if i == j {
            diag += grid[i * n + j]
        }
    }
}
print(diag)

// A loop that never runs: the hoisted product is computed and unused.
let none = 0
for i in 0..0 {
    none += n * 1000
}
print(none)

// k changes inside the loop, so k * 2 must not move.
var k = 1
var acc = 0
for i in 0..4 {
    acc += k * 2
    k += 1
}
print(acc)

// The invariant comparison and the work under the branch.
let limit = 3
var hits = 0
for i in 0..10 {
    if limit * 2 > 5 {
        hits += i % (limit + 1)
    }
}
print(hits)

// Floats.
let scale = 2.5
var f = 0.0
for i in 0..4 {
    f += float(i) * (scale * 2.0)
}
print(f)

// A continue skips to the test with the hoisted value still right.
var odd = 0
var m = 0
while m < 10 {
    m += 1
    if m % 2 == 0 { continue }
    odd += m * (n - 4)
}
print(odd)
