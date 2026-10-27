// A parameter whose setup makes a call - a captured one is boxed, a
// large array is copied with memmove - must not clobber the parameters
// after it, which still sit in their argument registers.

fn captured(a: int, b: int) -> int {
    let g = fn() -> int { return a * 10 }
    return g() + b
}
print(captured(3, 4))

fn capturedFloat(a: float, b: float, c: int) -> float {
    let g = fn() -> float { return a * 2.0 }
    return g() + b + float(c)
}
print(capturedFloat(1.5, 0.25, 7))

fn two(a: [40]f32, b: [40]f32) -> f32 {
    let r: f32 = 0.0
    for i in 0..40 {
        r += a[i] * b[i]
    }
    return r
}
let x: [40]f32
let y: [40]f32
for i in 0..40 {
    x[i] = f32(i % 7)
    y[i] = f32(i % 5)
}
print(two(x, y))

fn mixed(a: [20]f64, n: int, s: str, b: [20]f64) -> f64 {
    return a[19] + float(n) + float(len(s)) + b[0]
}
let p: [20]f64
let q: [20]f64
p[19] = 1.5
q[0] = 2.0
print(mixed(p, 10, "abc", q))

// The same inside a closure.
let k = 100
let h = fn(a: int, b: int) -> int {
    let inner = fn() -> int { return a + k }
    return inner() * b
}
print(h(5, 3))
