// Every way the address of an element of an inline list could end up in
// the heap. Each route must hold a copy: the list is rebuilt and the
// collector run in between, so a stored interior address would read
// freed or reused memory.
struct Rec {
    name: str
    n: int
}

struct H {
    p: Rec
    tag: str
}

fn churn() {
    for r in 0..40 {
        let junk: []Rec = []
        for i in 0..200 { push(junk, Rec{name: "junk{r}-{i}", n: 7}) }
        let s: []str = []
        for i in 0..100 { push(s, "s{i}") }
    }
}

fn build() -> []Rec {
    let xs: []Rec = []
    for i in 0..50 { push(xs, Rec{name: "r{i}", n: i}) }
    return xs
}

fn pick(xs: []Rec) -> Rec {
    for p in xs {
        if p.n == 20 { return p }
    }
    return xs[0]
}

fn viaIndex(xs: []Rec) -> Rec {
    let p = xs[21]
    return p
}

impl Rec {
    fn grabber(self) -> fn() -> str {
        return fn() -> str { return "{self.name}:{self.n}" }
    }
}

fn captured(xs: []Rec) -> fn() -> str {
    for i, p in xs {
        if i == 22 {
            // a captured name is bound to a copy of its element
            return fn() -> str { return "{p.name}:{p.n}" }
        }
    }
    return fn() -> str { return "none" }
}

fn deferred(xs: []Rec) -> str {
    let out = ""
    let p = xs[23]
    defer show(xs[24])
    return "{out}{p.name}"
}

fn show(r: Rec) {
    churn()
    print("deferred {r.name}:{r.n}")
}

let xs = build()
let ret = pick(xs)
let idx = viaIndex(xs)
let cap = captured(xs)
let g = xs[25].grabber()

let sink: []Rec = []
push(sink, xs[26])
let holder = H{p: xs[27], tag: "h"}
holder.p = xs[28]
let m: {str: Rec} = {}
m["k"] = xs[29]
let lit = [xs[30], xs[31]]
let h2 = H{p: xs[32], tag: "h2"}
let direct = xs[33]

// the list is dropped and replaced; only the copies may survive
xs[20].name = "gone20"
xs[26].n = -26
xs[28].n = -28
xs[29].n = -29
xs[30].n = -30
xs[32].n = -32
xs[33].n = -33
churn()
let dead = deferred(xs)
print(dead)
churn()

print("{ret.name}:{ret.n}")
print("{idx.name}:{idx.n}")
print(cap())
print(g())
print("{sink[0].name}:{sink[0].n}")
print("{holder.p.name}:{holder.p.n}")
print("{m["k"].name}:{m["k"].n}")
print("{lit[0].name}:{lit[0].n}")
print("{h2.p.name}:{h2.p.n}")
print("{direct.name}:{direct.n}")
