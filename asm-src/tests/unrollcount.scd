// Counted loops that are run several trips at a time: the trip counts
// either side of the unroll factor, steps, inclusive bounds, bounds near
// the top of the integer range, and bodies the unroller must leave alone.
fn trips(n: int) -> int {
    let c = 0
    for i in 0..n {
        c += i + 1
    }
    return c
}
let s = 0
for n in 0..14 {
    s = s * 3 + trips(n)
    s = s % 1000003
}
print(s)

fn stepped3(a: int, b: int) -> int {
    let c = 0
    for i in a..b step 3 {
        c = c * 7 + i
        c = c % 99991
    }
    return c
}
fn stepped5(a: int, b: int) -> int {
    let c = 0
    for i in a..b step 5 {
        c = c * 7 + i
        c = c % 99991
    }
    return c
}
print(stepped3(0, 40))
print(stepped3(5, 6))
print(stepped5(-20, 20))
print(stepped5(10, 10))

fn incl(a: int, b: int) -> int {
    let c = 0
    for i in a..=b {
        c = c * 5 + i
        c = c % 10007
    }
    return c
}
print(incl(0, 0))
print(incl(0, 3))
print(incl(0, 4))
print(incl(-3, 9))
print(incl(4, 3))

// A bound at the top of the range must not wrap into "plenty left".
fn nearTop() -> int {
    let c = 0
    let top = 9223372036854775807
    for i in top - 9..top {
        c += 1
    }
    return c
}
print(nearTop())

fn farApart() -> int {
    let c = 0
    let lo = -9223372036854775807
    for i in lo..lo + 11 {
        c += 1
    }
    return c
}
print(farApart())

// The body writes the counter.
let w = 0
for i in 0..30 {
    w += i
    if i == 5 { i = 11 }
}
print(w)

// The body changes the bound.
fn shrink() -> int {
    let c = 0
    let lim = 40
    for i in 0..lim {
        c += i
        if i == 9 { lim = 17 }
    }
    return c
}
print(shrink())

// Leaving the loop from inside the body, and continuing.
fn early(n: int) -> int {
    let c = 0
    for i in 0..100 {
        if i == n { break }
        if i % 3 == 0 { continue }
        c += i
    }
    return c
}
print(early(0))
print(early(7))
print(early(50))

// An indexed body, with the bounds check inside the unrolled trips.
fn sumlist(n: int) -> int {
    let xs = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]
    let c = 0
    for i in 0..n {
        c += xs[i] * 2
    }
    return c
}
print(sumlist(11))
print(sumlist(6))

// A list written in the loop.
let ys = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]
for i in 0..13 {
    ys[i] = i * i
}
let t = 0
for i in 0..13 {
    t += ys[i]
}
print(t)

// Nested: the inner loop is the one run in fours.
let g = 0
for i in 0..7 {
    for j in 0..9 {
        g += i * j
    }
}
print(g)
