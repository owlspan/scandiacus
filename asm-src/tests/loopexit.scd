// Loops that decide at the top whether to leave, by returning: the
// leaving code is moved after the loop so the loop can be rotated. The
// returns read values the test worked out, which the moved code has to
// work out again. Nothing here allocates, so no collection check stands
// at the top of the loops in the way.
fn fib(n: int) -> int {
    if n < 2 { return n }
    return fib(n - 1) + fib(n - 2)
}

fn gcd(a: int, b: int) -> int {
    if b == 0 { return a * 1 + 0 }
    return gcd(b, a % b)
}

// The leaving test reads a value the moved code uses again.
fn digits(n: int, count: int) -> int {
    let m = n / 10
    if m == 0 { return count * 1000 + n + m }
    return digits(m, count + 1)
}

// Seven parameters, three of them on the stack, in a loop that leaves
// at the top.
fn walk(n: int, a: int, b: int, c: int, d: int, e: int, f: int) -> int {
    if n <= 0 { return a + b * 2 + c * 3 + d * 4 + e * 5 + f * 6 + n }
    return walk(n - 1, b, c, d, e, f, a + 1)
}

print(fib(20))
print(gcd(1071, 462))
print(gcd(17, 0))
print(digits(9, 1))
print(digits(98765, 1))
print(walk(10, 1, 2, 3, 4, 5, 6))
print(walk(0, 1, 2, 3, 4, 5, 6))
