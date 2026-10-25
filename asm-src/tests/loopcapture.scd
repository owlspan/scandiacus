// Closures that capture a loop's variable: each trip's closures keep
// that trip's value, and an assignment to a for variable still moves
// the loop. Capturing one used to crash.
let fs: []fn() -> int = []
for k in 0..3 {
    push(fs, fn() -> int { return k * 10 })
}
for i in 0..5 {
    let g = fn() -> int { return i }
    if i == 1 { i += 2 }
    print(g())
}
let names = ["a", "b", "c"]
let gs: []fn() -> str = []
for n in names {
    push(gs, fn() -> str { return n + "!" })
}
for j, n in names {
    push(gs, fn() -> str { return str(j) + n })
}
let m = {"x": 1, "y": 2}
for key, v in m {
    push(gs, fn() -> str { return key + str(v) })
}
for f in fs { print(f()) }
for g in gs { print(g()) }
