let n = 17
if n % 2 == 0 {
    print(0)
} else {
    print(1)
}

let i = 0
let total = 0
while i < 10 {
    i += 1
    if i == 3 { continue }
    if i > 8 { break }
    total += i
}
print(total)
print(i)

let a = 5
print(a > 3 && a < 10)
print(a > 9 || a == 5)
print(!(a == 5))
print(a >= 5)
print(a <= 4)
