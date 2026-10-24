// Shortest float formatting (Ryu): the fewest digits that read back to
// the same double, the closest such when there are several, and Go's
// rule for when to switch to an exponent.
let xs: []float = [0.1, 1.0, -2.5, 3.14159, 100.0, 123456.0, 1234567.0,
    0.0001, 0.00001, 1e21, 1e100, 5e-324, 1.7976931348623157e308,
    2.2250738585072014e-308, 2.225073858507201e-308, 0.3, 9007199254740993.0,
    123.456, 1e15, 1e-7, 5e-310, 1e23, 8.41e21, 4.35e-6, 1.5e-323,
    0.000123, 999999.0, 999999.9, 1000000.0, 4503599627370497.5,
    2.0e-5, 33554432.0, 0.1 + 0.2, -0.0, 0.0, 1.0 / 3.0, 2.0 / 3.0]
for x in xs {
    print(x)
}
print(1.0 / 0.0)
print(-1.0 / 0.0)
print("{0.25} and {1e-9}")
