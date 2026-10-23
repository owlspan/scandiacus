// Namespaced library calls.
//
// time.now() is a clock, so its value cannot be printed and compared
// against the Go backend. What is stable is everything around it: that
// the call resolves, returns an int, and that the int is a plausible
// unix timestamp. Both backends read the same clock within the same
// second or two, so every line here is byte-identical on both.

let t = time.now()

print(t > 1700000000)
print(t < 4000000000)

// It is an ordinary int, so it takes part in arithmetic and in the
// integer builtins like any other.
let later = t + 60
print(later - t)
print(min(t, later) == t)
print(max(t, later) == later)

// Called twice in one expression, and inside a function.
fn elapsed(from: int) -> int {
    return time.now() - from
}

let d = elapsed(t)
print(d >= 0)
print(d < 5)

// In a condition and in an interpolation.
if time.now() > 0 {
    print("clock reads forward")
}

let ok = time.now() > 1700000000
print("plausible: {ok}")
