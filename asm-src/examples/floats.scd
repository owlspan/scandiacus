let a = 1.5
let b = 4.0
print(a + b)
print(a - b)
print(a * b)
print(a / b)
print(-a)

print(a < b)
print(a > b)
print(a == 1.5)
print(a != b)
print(b >= 4.0)
print(b <= 3.0)

print(int(3.7))
print(int(-3.7))
print(float(5))
print(divf(7, 2))
print(sqrt(16.0))
print(sqrt(2))
print(mod(7.5, 2.0))

let g: float = 5
print(g)
g += 0.5
print(g)
g *= 2.0
print(g)

fn area(r: float) -> float {
    return 3.14159 * r * r
}
print(area(2.0))

fn mixed(n: int, x: float, m: int, y: float) -> float {
    return x + y + float(n + m)
}
print(mixed(1, 2.5, 3, 4.5))

fn six(a: float, b: float, c: float, d: float, e: float, f: float) -> float {
    return a + b + c + d + e + f
}
print(six(1.0, 2.0, 3.0, 4.0, 5.0, 6.0))

print("pi is {3.14159}")
print(str(2.5))
let xs = [1.5, 2.5, 3.0]
print(xs)
print(xs[1])
push(xs, 9.5)
print(xs)
let total = 0.0
for v in xs { total += v }
print(total)
