// Self calls in tail position run as loops (tailcall.go).
fn gcd(a: int, b: int) -> int {
    if b == 0 { return a }
    return gcd(b, a % b)
}

// 200000 deep: as real recursion this would overflow the 8 MB stack.
fn countDown(n: int, acc: int) -> int {
    if n == 0 { return acc }
    return countDown(n - 1, acc + 2)
}

fn fib(n: int) -> int {
    if n < 2 { return n }
    return fib(n - 1) + fib(n - 2)
}

fn fact(n: int) -> int {
    if n <= 1 { return 1 }
    return n * fact(n - 1)
}

// The arguments swap: both are read before either is stored.
fn swapSteps(a: int, b: int, k: int) -> int {
    if k == 0 { return a * 1000 + b }
    return swapSteps(b, a, k - 1)
}

fn halve(x: float, steps: int) -> float {
    if steps == 0 { return x }
    return halve(x / 2.0, steps - 1)
}

fn parity(n: int, even: bool) -> bool {
    if n == 0 { return even }
    return parity(n - 1, !even)
}

// A closure made on each trip keeps its own copy of the local.
fn collect(n: int, fs: []fn() -> int) -> int {
    if n == 0 {
        let total = 0
        for f in fs {
            total = total * 10 + f()
        }
        return total
    }
    let k = n
    push(fs, fn() -> int { return k })
    return collect(n - 1, fs)
}

// Not a tail call: the addition happens after the call returns, on
// the left. Still correct, just recursion.
fn depth(n: int) -> int {
    if n == 0 { return 0 }
    return depth(n - 1) + 1
}

// Mixed: an accumulator and a plain tail call in one function.
fn mixed(n: int) -> int {
    if n <= 0 { return 7 }
    if n % 2 == 0 { return mixed(n - 1) }
    return n + mixed(n - 1)
}

print(gcd(1071, 462))
print(countDown(200000, 0))
print(fib(20))
print(fact(20))
print(swapSteps(1, 2, 3))
print(halve(1024.0, 12))
print(parity(100001, true))
let fs: []fn() -> int = []
print(collect(4, fs))
print(depth(1000))
print(mixed(10))
