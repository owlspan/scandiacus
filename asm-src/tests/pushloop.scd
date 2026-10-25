// A counted loop whose body pushes one literal is done as one fill.
// Each shape of list, onto lists that already hold something, and
// counts that are empty or negative.
let flags: []bool = [true]
for i in 0..5 {
    push(flags, false)
}
for i in 0..=2 {
    push(flags, true)
}
print(flags)
print(len(flags))

let ns: []int = []
for i in 3..3 {
    push(ns, 9)
}
for i in 5..2 {
    push(ns, 9)
}
print(len(ns))
for i in 0..20 {
    push(ns, -7)
}
push(ns, 1)
print(ns)

let fs = [0.5, 0.25]
for k in 1..4 {
    push(fs, 2.5)
}
print(fs)

// Big enough to grow past several doublings at once.
let big: []bool = []
let n = 100000
for i in 0..n + 1 {
    push(big, true)
}
print(len(big))
print(big[0] && big[n])
let total = 0
for b in big {
    if b { total += 1 }
}
print(total)
