// gc off and defer: each trip's list is freed on that trip.
gc off

let start = mem.objects()
let total = 0
for i in 0..100000 {
    let xs = [i, i + 1]
    defer delete(xs)
    if i % 2 == 0 {
        continue
    }
    total += xs[1]
}
print(total)
print(mem.objects() - start < 5)
