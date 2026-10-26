struct S {
    f0: float
    f1: float
    f2: float
    f3: float
    f4: float
    f5: float
    f6: float
    f7: float
    f8: float
    f9: float
    f10: float
    f11: float
    f12: float
    f13: float
    f14: float
    f15: float
}
let ps: []S = []
for i in 0..1000 {
    push(ps, S{f0: float(i), f1: 0.5, f2: 1.0, f3: 0.5, f4: 1.0, f5: 0.5, f6: 1.0, f7: 0.5, f8: 1.0, f9: 0.5, f10: 1.0, f11: 0.5, f12: 1.0, f13: 0.5, f14: 1.0, f15: 0.5})
}
for frame in 0..20000 {
    for i in 0..len(ps) {
        ps[i].f0 += ps[i].f8 * 0.01
        ps[i].f1 += ps[i].f9 * 0.01
        ps[i].f2 += ps[i].f10 * 0.01
        if ps[i].f0 > 100.0 { ps[i].f8 = -ps[i].f8 }
    }
}
let t = 0.0
for p in ps {
    t += p.f0 + p.f1 + p.f2
}
print(t)
