// f32: IEEE single precision. Every operation rounds to single, the
// way a C float does, and it prints as the shortest decimal that reads
// back to the same f32.

let a: f32 = 0.1
let b: f32 = 0.2
print(a)
print(a + b)
print(a + b == 0.3)
print(float(a))
print(float(a) + float(b) == 0.3)

// Rounding after every step: 1/3 in single, times 3.
let third: f32 = 1.0 / 3.0
print(third)
print(third * 3.0)
let one: f32 = 1
let three: f32 = 3
print(one / three)

// Large values lose low integer digits at 24 bits of mantissa.
let big: f32 = 16777216
print(big + 1)
print(big + 2)
print(f32(16777217))
print(f32(123456789))
print(int(f32(123456789)))

// An accumulation drifts the way it does in C.
let sum: f32 = 0
for i in 0..1000 {
    sum += 0.1
}
print(sum)
let dsum = 0.0
for i in 0..1000 {
    dsum += 0.1
}
print(dsum)

// Negation, comparison, and conversion both ways.
let n: f32 = -2.5
print(-n)
print(n < 0.0)
print(i32(n))
print(u8(f32(250.9)))
print(f32(i32(-7)))
print(f32(u64(18446744073709551615)))
print(f32(1.0e40) > f32(3.0e38))
let tiny: f32 = 1.0e-45
print(tiny)
print(tiny / 2.0)

// Compound assignment rounds too.
let c: f32 = 1
c /= 3
print(c)
c *= 3
print(c)
c -= 0.5
print(c)

// Printing in every place a value can be printed.
let v: []f32 = [0.1, 0.25, 1.0e10]
print(v)
print("{a} {n}")
print(str(third))
struct Vec2 {
    x: f32
    y: f32
}
let p = Vec2{x: 0.1, y: 3}
p.y += 0.1
print(p)

// Functions.
fn dot(ax: f32, ay: f32, bx: f32, by: f32) -> f32 {
    return ax * bx + ay * by
}
print(dot(0.1, 0.2, 0.3, 0.4))
fn lerp(a: f32, b: f32, t: f32) -> f32 {
    return a + (b - a) * t
}
print(lerp(0, 10, 0.3))
