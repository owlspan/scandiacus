// A packed loop whose right-hand side holds a subtraction with an
// operation on its right: the result of the inner subtraction ends up in
// the second register, and the left operand must not be loaded over it.
let n = 10
let a: []float = []
let b: []float = []
let c: []float = []
for i in 0..n + 6 {
    push(a, float(i % 5) * 0.5 + 0.25)
    push(b, float(i % 7) * 0.75 - 1.0)
    push(c, float(i) * 0.125)
}
for j in 3..n {
    b[j] *= a[j] - (a[j + 2] - c[j])
}
print(b)
for j in 0..n {
    c[j] = a[j] - (b[j] - (a[j] - c[j]))
}
print(c)
// The same subtraction as the left operand of an outer operation: the
// outer right operand must go in the register the subtraction did not
// end in.
let x = 3.25
for j in 0..n {
    c[j] = (a[j] - b[j] * 3.5) * x
}
print(c)
