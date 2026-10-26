struct S {
    x: float
    y: float
    v: float
}
let ps: []S = []
for i in 0..1000 {
    push(ps, S{x: float(i), y: 0.5, v: 1.0})
}
for frame in 0..20000 {
    for i in 0..len(ps) {
        ps[i].x += ps[i].v * 0.01
        ps[i].y += ps[i].v * 0.02
        if ps[i].x > 100.0 { ps[i].v = -ps[i].v }
    }
}
let t = 0.0
for p in ps {
    t += p.x + p.y + p.v
}
print(t)
