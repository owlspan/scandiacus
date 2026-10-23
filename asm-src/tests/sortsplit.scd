// sort and sortBy are merge sorts, stable; repeat sizes its result once;
// split and replace compare in place. Sizes here are large enough that
// the old quadratic versions would be slow, small enough to be quick.
print(sort([5, 3, 9, 1, 3, 7]))
print(sort(["pear", "apple", "fig", "apple"]))
print(sort([2.5, -1.0, 0.5]))
let none: []int = []
print(sort(none))
print(sort([42]))

// Stable: equal keys keep the order they came in.
let names = ["bob", "al", "cy", "ed", "jo", "flo"]
print(sortBy(names, fn(a: str, b: str) -> bool { return len(a) < len(b) }))

let big: []int = []
for i in 0..20000 {
    push(big, (i * 7919) % 20011)
}
let sorted = sort(big)
let ok = true
for i in 1..len(sorted) {
    if sorted[i - 1] > sorted[i] {
        ok = false
    }
}
print("{len(sorted)} sorted: {ok} first {sorted[0]} last {sorted[len(sorted) - 1]}")
let down = sortBy(big, fn(a: int, b: int) -> bool { return a > b })
print("{down[0]} {down[len(down) - 1]}")

print("[{repeat("ab", 3)}] [{repeat("x", 0)}] [{repeat("x", -1)}] [{repeat("", 4)}]")
let r = repeat("abc", 100001)
print("{len(r)} {substr(r, 299997, 300003)}")

print(join(split("a,,b,", ","), "|"))
print(join(split("aaaa", "aa"), "|"))
print(len(split(repeat("w ", 50000), " ")))
print(replace("aaaa", "aa", "b"))
print(replace("abcabc", "c", ""))
print(replace("no match", "zz", "y"))
print(replace("x", "", "y"))
print(len(replace(repeat("hello world ", 20000), "world", "there!")))
