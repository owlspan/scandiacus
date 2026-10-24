let n = 400
let a: []float = []
let b: []float = []
let c: []float = []
for i in 0..n * n {
    push(a, float(i % 13) * 0.5)
    push(b, float(i % 7) * 0.25)
    push(c, 0.0)
}
for i in 0..n {
    for k in 0..n {
        let x = a[i * n + k]
        for j in 0..n {
            c[i * n + j] += x * b[k * n + j]
        }
    }
}
let t = 0.0
for v in c {
    t += v
}
print(t)
