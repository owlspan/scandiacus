// Loops the optimiser works on: counters in callee-saved registers,
// compares fused with their branch, and collection checks only where
// something may have been allocated since the last one.

fn arith(n: int) -> int {
    let total = 0
    let i = 0
    while i < n {
        let j = 0
        while j < 50 {
            total += (i * j) % 7
            j += 1
        }
        i += 1
    }
    return total
}

// Allocating in the loop keeps its check, so a long run still collects.
fn churn(n: int) -> int {
    let kept = 0
    let i = 0
    while i < n {
        let xs = [i, i + 1, i + 2]
        let s = "item {i}"
        if len(s) > 6 {
            kept += xs[2]
        }
        i += 1
    }
    return kept
}

// Many locals at once: more than there are registers to keep them in.
fn many(a: int) -> int {
    let b = a + 1
    let c = b + 1
    let d = c + 1
    let e = d + 1
    let f = e + 1
    let g = f + 1
    let h = g + 1
    let k = h + 1
    let m = k + 1
    let i = 0
    while i < 10 {
        b += c
        c += d
        d += e
        e += f
        f += g
        g += h
        h += k
        k += m
        m += 1
        i += 1
    }
    return b + c + d + e + f + g + h + k + m
}

fn fib(n: int) -> int {
    if n < 2 {
        return n
    }
    return fib(n - 1) + fib(n - 2)
}

print(arith(20000))
print(churn(200000))
print(many(3))
print(fib(25))
let before = mem.used()
print(churn(100000) > 0)
print(mem.used() < before + 50000000)
