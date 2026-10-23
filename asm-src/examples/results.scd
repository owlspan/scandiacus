// The error type. A function that can fail returns T!, which is either
// a T or the reason there is not one. There are no exceptions in Scandiacus,
// so this is the only way anything reports failure.

fn half(n: int) -> int! {
    if n % 2 != 0 {
        return fail("{n} is odd")
    }
    return n / 2
}

// `?` unwraps a result, or stops and hands the failure to the caller.
// The two calls below are four lines of error checking in Go.
fn quarter(n: int) -> int! {
    let h = half(n)?
    return half(h)?
}

// An action that either worked or explains why not carries no value.
fn checkPositive(n: int) -> void! {
    if n < 1 {
        return fail("{n} is not positive")
    }
    return ok()
}

fn describe(n: int) -> str {
    let r = half(n)
    if isOk(r) {
        return "half of {n} is {must(r)}"
    }
    return "no half: {errorOf(r)}"
}

print(describe(10))
print(describe(7))

// The four ways to read a result without propagating it.
let good = half(8)
let bad = half(3)

print(isOk(good))
print(failed(bad))
print(must(good))
print(valueOr(bad, -1))
print(errorOf(bad))

// An empty reason is what a result that worked reports.
print("[{errorOf(good)}]")

// `?` chains through as many frames as it needs to.
print(must(quarter(20)))
print(errorOf(quarter(6)))
print(errorOf(quarter(2)))

// void! carries no value, only the reason.
print(isOk(checkPositive(3)))
print(errorOf(checkPositive(0)))

// Results work with the rest of the language: a loop over inputs,
// counting how many of them the halving accepted.
let inputs = [4, 7, 12, 9, 20]
let halved = 0
let total = 0
for i in 0..len(inputs) {
    let r = half(inputs[i])
    if isOk(r) {
        halved += 1
        total += must(r)
    }
}
print("{halved} of {len(inputs)} halved, totalling {total}")
