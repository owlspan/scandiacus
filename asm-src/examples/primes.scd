let n = 2
let found = 0
while n < 60 {
    let d = 2
    let prime = true
    while d * d <= n {
        if n % d == 0 {
            prime = false
            break
        }
        d += 1
    }
    if prime {
        print(n)
        found += 1
    }
    n += 1
}
print(found)
