// Division and remainder by a constant are a multiply and shifts rather
// than idiv. Both have to truncate towards zero the way idiv does, for
// negative numbers and at the ends of the range.

let values = [0, 1, -1, 6, 7, 8, -6, -7, -8, 13, -13, 100, -100, 1000003, -1000003,
    2147483647, -2147483648, 9223372036854775807, -9223372036854775807]
let minInt = -9223372036854775807 - 1
push(values, minInt)

let sum = 0
for v in values {
    sum += v / 2 + v % 2
    sum += v / 3 + v % 3
    sum += v / 7 + v % 7
    sum += v / 8 + v % 8
    sum += v / 10 + v % 10
    sum += v / 1000 + v % 1000
    sum += v / 1024 + v % 1024
    sum += v / 7919 + v % 7919
    sum += v / 2147483647 + v % 2147483647
}
print(sum)

for v in [minInt, -13, -8, -7, 7, 8, 13, 9223372036854775807] {
    print("{v / 7} {v % 7} {v / 8} {v % 8} {v / 10} {v % 10}")
}

// A divisor that is only known at runtime still divides.
let d = len(values) - 13
print("{100 / d} {-100 % d}")
