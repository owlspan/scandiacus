// Values that live only in a temporary while other threads collect.
// Each worker builds a list of structs and uses it at once, without
// naming it, while the others allocate and main forces collections. A
// collection that missed a pointer held only in a dead frame word or a
// register would free the list under its user and a sum would be wrong.
struct P {
    name: str
    a: int
}

fn make(n: int, id: int) -> []P {
    let xs: []P = []
    for i in 0..n {
        push(xs, P{name: "w{id}-{i}", a: i})
    }
    return xs
}

fn total(xs: []P) -> int {
    let t = 0
    for p in xs {
        t += p.a + len(p.name)
    }
    return t
}

fn churn(id: int) -> int {
    let sink = 0
    for r in 0..40 {
        let junk: []str = []
        for j in 0..100 {
            push(junk, "junk {r} {j} {id}")
        }
        sink += len(junk)
    }
    return sink
}

let good = atomic.new(0)
let ts: []int = []
for id in 0..4 {
    push(ts, thread.spawn(fn() {
        for round in 0..20 {
            let want = 0
            for i in 0..50 {
                want += i + len("w{id}-{i}")
            }
            let got = total(make(50, id))
            churn(id)
            if got == want {
                atomic.add(good, 1)
            }
        }
    }))
}
for r in 0..10 {
    mem.collect()
    churn(9)
}
for t in ts {
    thread.join(t)
}
print(atomic.get(good))         // 80
