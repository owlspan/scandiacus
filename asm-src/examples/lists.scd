let xs = [1, 2, 3]
print(xs)
print(len(xs))
print(xs[0])
print(xs[2])
xs[1] = 99
print(xs)

push(xs, 4)
push(xs, 5)
push(xs, 6)
push(xs, 7)
print(xs)
print(len(xs))

let names = ["ada", "grace"]
push(names, "alan")
print(names)
print(names[1])
print(len(names))

let flags = [true, false]
print(flags)

let total = 0
let i = 0
while i < len(xs) {
    total += xs[i]
    i += 1
}
print(total)

for v in xs { print(v) }
for nm in names { print(nm) }
let sum = 0
for v in xs {
    if v == 99 { continue }
    sum += v
}
print(sum)
