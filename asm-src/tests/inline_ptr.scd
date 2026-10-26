// Structs with str and list fields stored inside a list's element block.
// The collector has to follow the pointer words of every element, and
// every operation has to keep value semantics: what comes out of a list
// is a copy, what goes in is a copy.
struct Rec {
    name: str
    tags: []str
    n: int
    w: float
}

var shared: []Rec = []

fn addMore() {
    push(shared, Rec{name: "more", tags: ["m"], n: 100, w: 0.5})
}

impl Rec {
    fn bump(self) {
        self.n += 1
    }
    // grows the list self lives in: the write-back path
    fn bumpGrow(self) {
        self.n += 1
        addMore()
        self.n += 10
    }
    fn later(self) -> fn() -> int {
        return fn() -> int { return self.n * 2 }
    }
}

fn show(r: Rec) -> str {
    return "{r.name}/{len(r.tags)}/{r.n}/{r.w}"
}

fn dump(xs: []Rec) {
    let s = ""
    for r in xs { s = s + show(r) + " " }
    print(s)
}

let recs: []Rec = []
push(recs, Rec{name: "alpha", tags: ["a", "b"], n: 3, w: 1.5})
let v = Rec{name: "beta", tags: [], n: 1, w: 2.5}
push(recs, v)
v.name = "changed"                      // pushing copied v
push(recs, Rec{name: "gamma", tags: ["g"], n: 2, w: 0.5})
dump(recs)                              // alpha/2/3/1.5 beta/0/1/2.5 gamma/1/2/0.5

// index read and write of every field
recs[0].name = "ALPHA"
recs[0].tags = ["x", "y", "z"]
recs[0].n = 30
recs[0].w = 3.25
print(show(recs[0]))                    // ALPHA/3/30/3.25
push(recs[1].tags, "t1")
print(recs[1].tags)                     // [t1]
print(recs[0].tags[2])                  // z

// whole-element assignment copies
recs[2] = recs[0]
recs[2].n = 31
print(recs[0].n)                        // 30
print(recs[2].n)                        // 31

// the loop variable is the element
for r in recs { r.n += 1 }
dump(recs)                              // ALPHA/3/31/3.25 beta/1/2/2.5 ALPHA/3/32/3.25

// methods: in place, and through the write-back path
recs[1].bump()
print(recs[1].n)                        // 3
shared = recs
recs[1].bumpGrow()
print(len(shared))                      // 4
print(shared[1].n)                      // 14
print(shared[3].name)                   // more

// a method whose self is captured by a closure
recs[0].n = 1000
let fn1 = recs[0].later()
print(fn1())                            // 2000
for i in 0..300 {
    let junk: []str = []
    for j in 0..50 { push(junk, "junk{i}-{j}") }
}
print(fn1())                            // 2000

// first, last, pop, removeAt hand back copies
let f = first(recs)
f.n = 99999
print(recs[0].n)                        // 1000
let la = last(recs)
la.name = "nope"
print(recs[3].name)                     // more
let p = pop(recs)
push(recs, Rec{name: "reused", tags: [], n: 0, w: 0.0})
print(show(p))                          // more/1/100/0.5
print(show(recs[3]))                    // reused/0/0/0
let rm = removeAt(recs, 1)
print(show(rm))                         // beta/1/14/2.5
dump(recs)                              // ALPHA/3/1000/3.25 ALPHA/3/32/3.25 reused/0/0/0

insert(recs, 1, Rec{name: "ins", tags: ["i"], n: 5, w: 5.0})
dump(recs)                              // ALPHA/3/1000/3.25 ins/1/5/5 ALPHA/3/32/3.25 reused/0/0/0
let rv = reverse(recs)
dump(rv)                                // reused/0/0/0 ALPHA/3/32/3.25 ins/1/5/5 ALPHA/3/1000/3.25
rv[0].n = 77
print(recs[3].n)                        // 0
let sl = slice(recs, 1, 3)
sl[0].n = 55
print(recs[1].n)                        // 5
dump(sl)                                // ins/1/55/5 ALPHA/3/32/3.25

// sort by a str field, and sortBy
let byName = sortBy(recs, fn(a: Rec, b: Rec) -> bool { return a.name < b.name })
dump(byName)                            // ALPHA/3/1000/3.25 ALPHA/3/32/3.25 ins/1/5/5 reused/0/0/0
let byN = sortBy(recs, fn(a: Rec, b: Rec) -> bool { return a.n > b.n })
dump(byN)                               // ALPHA/3/1000/3.25 ALPHA/3/32/3.25 ins/1/5/5 reused/0/0/0

// equality (contains and indexOf do not take structs)
let c1 = [Rec{name: "p", tags: ["q"], n: 1, w: 1.0}, Rec{name: "r", tags: [], n: 2, w: 2.0}]
let c2 = [Rec{name: "p", tags: ["q"], n: 1, w: 1.0}, Rec{name: "r", tags: [], n: 2, w: 2.0}]
print(c1 == c2)                         // true
c2[1].tags = ["z"]
print(c1 == c2)                         // false
print(c1)

// clear drops the elements
let cl: []Rec = [Rec{name: "c", tags: [], n: 1, w: 1.0}]
clear(cl)
print(len(cl))                          // 0
push(cl, Rec{name: "d", tags: [], n: 2, w: 2.0})
print(show(cl[0]))                      // d/0/2/2

// a big list with fresh strings, allocating in between so that the
// collector runs while the block is half full. The count is
// small because the same file runs with a collection on every
// allocation; a run of 100000 elements was checked by hand.
let big: []Rec = []
for i in 0..3000 {
    push(big, Rec{name: "rec{i}", tags: ["t{i % 7}"], n: i, w: float(i) / 2.0})
    if i % 3 == 0 {
        let junk: []str = []
        for j in 0..5 { push(junk, "junk{i}-{j}") }
    }
}
let bad = 0
let sum = 0
for i in 0..len(big) {
    let r = big[i]
    if r.name != "rec{i}" || r.tags[0] != "t{i % 7}" || r.n != i || r.w != float(i) / 2.0 {
        bad += 1
    }
    sum += r.n
}
print(bad)                              // 0
print(sum)                              // 499991500
while len(big) > 1500 { pop(big) }
for i in 0..600 { push(big, Rec{name: "again{i}", tags: [], n: -i, w: 0.0}) }
bad = 0
for i in 0..1500 { if big[i].name != "rec{i}" { bad += 1 } }
for i in 0..600 { if big[1500 + i].name != "again{i}" { bad += 1 } }
print(bad)                              // 0
print(len(big))                         // 2100

// first aliasing: the element object is not shared with the result
let ps: []Rec = [Rec{name: "one", tags: [], n: 1, w: 1.0}]
let pf = first(ps)
pf.n = 99
print(ps[0].n)                          // 1

// a captured loop variable is a copy of its element
fn mk() -> []fn() -> int {
    let qs: []Rec = []
    for i in 0..1000 { push(qs, Rec{name: "q{i}", tags: [], n: i, w: 1.0}) }
    let fns: []fn() -> int = []
    for q in qs {
        if q.n == 500 {
            push(fns, fn() -> int { return q.n + len(q.name) })
        }
    }
    return fns
}
let fns = mk()
for r in 0..50 {
    let more: []Rec = []
    for i in 0..1000 { push(more, Rec{name: "m{i}", tags: [], n: 7, w: 7.0}) }
}
print(fns[0]())                         // 504
