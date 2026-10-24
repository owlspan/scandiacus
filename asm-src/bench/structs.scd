struct P {
    x: float
    y: float
    vx: float
    vy: float
}
let ps: []P = []
for i in 0..1000 {
    push(ps, P{x: float(i), y: 0.0, vx: 1.0, vy: 0.5})
}
for frame in 0..20000 {
    for i in 0..len(ps) {
        ps[i].x += ps[i].vx * 0.01
        ps[i].y += ps[i].vy * 0.01
        if ps[i].x > 100.0 { ps[i].vx = -ps[i].vx }
    }
}
let t = 0.0
for p in ps {
    t += p.x + p.y
}
print(t)
