let best = 0
let at = 0
let i = 1
while i < 10000 {
    let x = i
    let steps = 0
    while x != 1 {
        if x % 2 == 0 { x = x / 2 } else { x = 3 * x + 1 }
        steps += 1
    }
    if steps > best {
        best = steps
        at = i
    }
    i += 1
}
print(best)
print(at)
