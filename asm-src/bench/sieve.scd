let n = 50000000
let comp: []bool = []
for i in 0..n + 1 {
    push(comp, false)
}
let count = 0
let i = 2
while i <= n {
    if !comp[i] {
        count += 1
        let j = i * i
        while j <= n {
            comp[j] = true
            j += i
        }
    }
    i += 1
}
print(count)
