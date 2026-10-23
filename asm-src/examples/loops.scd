for i in 1..5 { print(i) }
print(0)
for i in 1..=5 { print(i) }
print(0)
for i in 10..0 step -2 { print(i) }
print(0)
for i in 0..=10 step 5 { print(i) }
print(0)

let total = 0
for i in 1..=100 {
    if i % 3 == 0 { continue }
    if i > 50 { break }
    total += i
}
print(total)

print(6 & 3)
print(6 | 3)
print(6 ^ 3)
print(~6)
print(1 << 10)
print(-1024 >> 3)
print(0xFF)
print(0b1010)
print(1_000_000)
print(abs(-9))
print(min(3, 8))
print(max(3, 8))
