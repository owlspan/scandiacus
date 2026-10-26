struct P {
    x: float
    y: float
    vx: float
    vy: float
}
let t = 0.0
for round in 0..20 {
    let ps: []P = []
    for i in 0..500000 {
        push(ps, P{x: float(i), y: 0.0, vx: 1.0, vy: 0.5})
    }
    for i in 0..len(ps) {
        t += ps[i].x + ps[i].vy
    }
}
print(t)
