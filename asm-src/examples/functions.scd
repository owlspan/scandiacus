fn square(n: int) -> int {
    return n * n
}

fn fib(n: int) -> int {
    if n < 2 { return n }
    return fib(n - 1) + fib(n - 2)
}

fn gcd(a: int, b: int) -> int {
    while b != 0 {
        let t = b
        b = a % b
        a = t
    }
    return a
}

fn many(a: int, b: int, c: int, d: int, e: int, f: int) -> int {
    return a + b + c + d + e + f
}

fn isEven(n: int) -> bool {
    return n % 2 == 0
}

print(square(7))
print(fib(20))
print(gcd(1071, 462))
print(many(1, 2, 3, 4, 5, 6))
print(isEven(10))
print(isEven(7))
