struct S {
    x: float
    y: float
    z: float
    vx: float
    vy: float
}
let ps: []S = []
for i in 0..1000 {
    push(ps, S{x: float(i), y: 0.5, z: 1.0, vx: 0.5, vy: 1.0})
}
for frame in 0..20000 {
    for i in 0..len(ps) {
        ps[i].x += ps[i].vx * 0.01
        ps[i].y += ps[i].vy * 0.01
        ps[i].z += ps[i].x * 0.0001
        if ps[i].x > 100.0 { ps[i].vx = -ps[i].vx }
    }
}
let t = 0.0
for p in ps {
    t += p.x + p.y + p.z
}
print(t)
