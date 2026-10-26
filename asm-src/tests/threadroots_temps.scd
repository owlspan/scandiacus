// A thread parked for another thread's collection is scanned by its
// stack alone. These workers build struct lists, string lists and maps
// that exist only as the intermediate values of an expression, so the
// one reference to each sits in a frame word or nowhere the collector
// can see unless the compiler keeps it there. Main collects the whole
// time. A wrong word shows as a bad sum or a crash.

struct P {
    name: str
    a: int
    b: int
}

fn make(n: int, tag: int) -> []P {
    let out: []P = []
    let i = 0
    while i < n {
        push(out, P{name: "p{tag}-{i}", a: i, b: tag})
        i += 1
    }
    return out
}

fn words(n: int, tag: int) -> []str {
    let out: []str = []
    let i = 0
    while i < n {
        push(out, "w{tag}.{i}")
        i += 1
    }
    return out
}

fn table(n: int, tag: int) -> {str: int} {
    let m: {str: int} = {}
    let i = 0
    while i < n {
        m["k{tag}-{i}"] = i * tag
        i += 1
    }
    return m
}

fn sumP(ps: []P) -> int {
    let s = 0
    for p in ps {
        s += p.a + p.b + len(p.name)
    }
    return s
}

fn sumW(ws: []str) -> int {
    let s = 0
    for w in ws {
        s += len(w)
    }
    return s
}

fn sumT(m: {str: int}) -> int {
    let s = 0
    for k, v in m {
        s += v + len(k)
    }
    return s
}

fn worker(tag: int) -> int {
    let total = 0
    let round = 0
    while round < 60 {
        // Each call's result is consumed straight away, so nothing
        // but a temporary holds it while the next one allocates.
        total += sumP(make(40, tag)) + sumW(words(40, tag)) + sumT(table(20, tag))
        total += sumP(make(10, tag)) * 2
        round += 1
    }
    return total
}

// Lists that grow and are replaced while other threads allocate: the
// grow step holds the old block and the new one at the same time.
fn grower(tag: int) -> int {
    let acc: []str = []
    let k = 0
    while k < 6000 {
        push(acc, "g{tag}-{k}")
        if len(acc) > 100 {
            let fresh: []str = []
            acc = fresh
        }
        k += 1
    }
    return len(acc)
}

let before = mem.collections()
let sums = atomic.new(0)
let grown = atomic.new(0)
let ts: []int = []
let w = 1
while w <= 4 {
    let id = w
    push(ts, thread.spawn(fn() {
        atomic.add(sums, worker(id))
    }))
    push(ts, thread.spawn(fn() {
        atomic.add(grown, grower(id))
    }))
    w += 1
}

let r = 0
while r < 40 {
    let junk: []str = []
    let j = 0
    while j < 500 {
        push(junk, "main {r} {j}")
        j += 1
    }
    mem.collect()
    r += 1
}

for t in ts {
    thread.join(t)
}

// What one worker computes, worked out again without any threads.
let want = 0
let k = 1
while k <= 4 {
    want += worker(k)
    k += 1
}
print(atomic.get(sums) == want)
print(atomic.get(grown) == 4 * grower(0))
print(mem.collections() - before >= 40)
