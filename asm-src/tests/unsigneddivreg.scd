// Unsigned division and remainder by constants where the dividend and
// the result share a register, are in different registers, or the
// divisor does not fit an immediate.
let a = 0
let b = 0
let c = 0
for i in 0..3000 {
    let n = i * 7 + 1
    let r = n % 13
    a += r
    let q = n / 13
    b += q
    let big = n % 5000000000
    c += big + (n % 1024) + (n / 1024)
}
print(a)
print(b)
print(c)

let x = 1
for i in 1..200 {
    x = (x * i + 5) % 1000003
}
print(x)

let y = 0
for i in 0..500 {
    y = y + i % 97
    y = y % 1000
}
print(y)
