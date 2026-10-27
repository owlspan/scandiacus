// Fixed-size arrays inside lists, maps, nullables and results, and
// json.decode filling them. A list keeps the arrays' bytes inline; a map
// value, a nullable and a result hold a block of their own. Every holder
// has a copy, so changing the original afterwards never reaches it.

// ---- the case that used to keep frame addresses ----
fn mk() -> [][3]int {
    let xs: [][3]int = []
    for i in 0..3 {
        let a: [3]int = [i, i * 10, i * 100]
        push(xs, a)
    }
    return xs
}
fn junk(k: int) -> int {
    let z: [8]int = [k, k, k, k, k, k, k, k]
    return z[3]
}
let xs = mk()
print(junk(7))
print(xs)
let m: {int: [2]int} = {}
for i in 0..3 {
    let p: [2]int = [i, -i]
    m[i] = p
}
print(junk(5))
print(m)
let n: ?[2]int = nil
let q: [2]int = [9, 8]
n = q
q[0] = 0
print(junk(3))
print(n)
print(q)

// ---- lists: element access, in place writes, for ----
xs[1][2] += 5
xs[2][0] = -2
print(xs)
print("{xs[1]} {xs[1][2]} {len(xs)}")
let first_row = xs[0]
first_row[0] = 42
print("{first_row} {xs[0]}")
xs[0] = first_row
first_row[1] = 43
print("{first_row} {xs[0]}")
for a in xs {
    a[0] = 99
    push(xs, a)
}
print(xs)
for i, a in xs {
    if i < 2 {
        print("{i} {a[0] + a[1] + a[2]}")
    }
}
xs[0] = xs[1]
xs[1][0] = 7
print("{xs[0]} {xs[1]}")

// ---- list builtins ----
fn sum2(a: [2]int) -> int { return a[0] + a[1] }
fn bump(a: [2]int) -> [2]int {
    a[0] += 100
    return a
}
let ys: [][2]int = [[1, 2], [3, 4], [5, 6]]
print(first(ys))
print(last(ys))
let popped = pop(ys)
print(popped)
print(ys)
let removed = removeAt(ys, 0)
print(removed)
print(ys)
push(ys, [7, 8])
push(ys, [9, 10])
insert(ys, 0, [0, 0])
insert(ys, 1, ys[3])
print(ys)
print(reverse(ys))
print(slice(ys, 1, 3))
print(filter(ys, fn(a: [2]int) -> bool { return a[0] > 3 }))
print(map(ys, sum2))
print(map(ys, bump))
each(ys, fn(a: [2]int) { print(a[1]) })
print(any(ys, fn(a: [2]int) -> bool { return a[0] == 9 }))
print(all(ys, fn(a: [2]int) -> bool { return a[0] == 9 }))
print(reduce(ys, 0, fn(acc: int, a: [2]int) -> int { return acc + a[0] * a[1] }))
print(sortBy(ys, fn(a: [2]int, b: [2]int) -> bool { return a[0] > b[0] }))
print(ys)
let zs = slice(ys, 0, 100)
print(ys == zs)
zs[0][0] = 55
print(ys == zs)
print(ys[0])
print("{ys}")
print(str(ys[1]))
print(json.encode(ys))
print(join(ys, ";"))
rand.seed(3)
let shuffled = rand.shuffle(ys)
print(len(shuffled))
print(sum2(shuffled[0]) + sum2(shuffled[1]) + sum2(shuffled[2]) + sum2(shuffled[3]) + sum2(shuffled[4]))
clear(ys)
print(ys)
print(len(ys))

// ---- a long list of [4]f64 with collections in between ----
fn build(count: int) -> [][4]float {
    let rows: [][4]float = []
    let scratch: []str = []
    for i in 0..count {
        let f = float(i)
        let a: [4]float = [f, f * 2.0, f * 3.0, f * 4.0]
        push(rows, a)
        push(scratch, "item {i}")
        if len(scratch) > 100 {
            clear(scratch)
        }
    }
    return rows
}
let big = build(10000)
print(len(big))
var total = 0.0
for i in 0..len(big) {
    total += big[i][0] + big[i][1] + big[i][2] + big[i][3]
}
print(total)
print("{big[9999]} {big[1234]}")
var getters: []fn() -> float = []
for a in big {
    if a[0] < 3.0 {
        push(getters, fn() -> float { return a[1] })
        a[1] = -1.0
    }
}
for g in getters {
    print(g())
}
print(big[1])

// ---- maps ----
let mm: {str: [2]int} = {"x": [1, 2], "y": [3, 4]}
print(mm)
print(mm["x"])
print(mm["zz"])
print("{has(mm, "x")} {has(mm, "zz")}")
print(find(mm, "y"))
print(find(mm, "q"))
print("{keys(mm)} {values(mm)}")
let held = mm["x"]
held[0] = 50
print("{held} {mm["x"]}")
mm["x"][1] = 60
print(mm)
for k, v in mm {
    v[0] = -1
    print("{k} {v}")
}
print(mm)
remove(mm, "x")
let mm2: {str: [2]int} = {"y": [3, 4]}
print(mm == mm2)
mm["y"][1] = 99
print(mm == mm2)
let nested: {str: [][2]int} = {}
nested["a"] = [[1, 2]]
let cur = nested["a"]
push(cur, [3, 4])
print(nested)
for i in 0..300 {
    let p: [2]int = [i, i * 2]
    m[i] = p
}
print("{len(m)} {m[299]} {m[150]}")

// ---- nullables ----
let u: ?[3]u8 = nil
print(u)
u = [1, 2, 3]
print(u)
let u2 = u
if u != nil {
    u[0] = 200
}
print("{u} {u2}")
print(u == u2)
let lq: []?[2]int = [nil, [1, 2]]
lq[0] = [3, 4]
print(lq)
print(lq == lq)
fn mk2(k: int) -> ?[2]int {
    if k < 0 {
        return nil
    }
    let a: [2]int = [k, k + 1]
    return a
}
let r1 = mk2(5)
print(r1)
print(mk2(-1))
if r1 != nil {
    let c = r1
    c[0] = 100
    print("{c} {r1} {r1[0] + r1[1]}")
}
struct Holder {
    id: int
    v: ?[2]f64
    w: [2]int
}
let h = Holder{id: 1, v: [1.5, 2.5], w: [3, 4]}
let h2 = h
print(h.v)
let hv = h.v
if hv != nil {
    hv[0] = 9.5
    h2.v = hv
}
print("{h} {h2}")
h2.w[0] = 30
h.v = nil
print("{h} {h2}")

// ---- results ----
fn mkr(k: int) -> [2]int! {
    if k < 0 {
        return fail("negative")
    }
    let a: [2]int = [k, k * k]
    return a
}
fn use(k: int) -> int! {
    let a = mkr(k)?
    return a[0] + a[1]
}
print(must(mkr(3)))
print(valueOr(mkr(-1), [1, 1]))
print(errorOf(mkr(-1)))
print(must(use(4)))
print(isOk(use(-4)))

// ---- json ----
struct Box {
    name: str
    p: [3]int
    q: ?[2]f64
    u: [3]u8
    f: [2]f32
}
let a1: [3]int! = json.decode("[1, 2, 3]")
print(must(a1))
let a2: [3]int! = json.decode("[1, 2, 3, 4, 5]")
print(must(a2))
let a3: [3]int! = json.decode("[7]")
print(must(a3))
let a4: [3]int! = json.decode("5")
print(must(a4))
let a5: [3]int! = json.decode("[1, 2")
print(errorOf(a5))
print(isOk(a5))
let jl: [][2]int! = json.decode("[[1, 2], [3, 4], [5], []]")
print(must(jl))
let jm: {str: [2]int}! = json.decode(`{"a": [1, 2], "b": [3, 4]}`)
print(must(jm))
let jn: ?[2]int! = json.decode("[1, 2]")
print(must(jn))
let jn2: ?[2]int! = json.decode("null")
print(must(jn2))
let jln: []?[2]int! = json.decode("[[1, 2], null, [3, 4]]")
print(must(jln))
let jb: Box! = json.decode(`{"name": "x", "p": [1, 2, 3], "q": [1.5, 2.5], "u": [1, 300, 255], "f": [0.1, 0.2]}`)
print(must(jb))
let jb2: Box! = json.decode(`{"name": "y", "q": null}`)
print(must(jb2))
print(json.encode(must(jb)))
print(json.encode(must(jl)))
print(json.encode(must(jm)))
print(json.encode(must(jln)))
print(json.encode(must(jn)))
print(json.encode(must(jn2)))
let round: [][3]int = [[1, 2, 3], [4, 5, 6]]
let back: [][3]int! = json.decode(json.encode(round))
print(must(back) == round)
let rm: {str: [2]float} = {"k": [0.5, 1.5]}
let rmb: {str: [2]float}! = json.decode(json.encode(rm))
print(must(rmb) == rm)
let rn: ?[3]u8 = [4, 5, 6]
let rnb: ?[3]u8! = json.decode(json.encode(rn))
print(must(rnb) == rn)
print(json.decodeOr("bad", round))

// ---- literals take their type from where they sit ----
struct Shelf {
    rows: [][2]int
    tags: {str: [2]int}
    opt: ?[2]int
}
fn rowsum(rows: [][2]int) -> int {
    let acc = 0
    for r in rows {
        acc += r[0] + r[1]
    }
    return acc
}
fn mkrows() -> [][2]int {
    return [[1, 2], [3, 4]]
}
fn mkmap() -> {str: [2]int} {
    return {"a": [1, 2]}
}
fn mkopt() -> ?[2]int {
    return [8, 9]
}
fn mkres() -> [2]int! {
    return [6, 7]
}
let shelf = Shelf{rows: [[1, 2], [3, 4]], tags: {"k": [5, 6]}, opt: [7, 8]}
print(shelf)
print(rowsum([[1, 2], [3, 4]]))
print(rowsum(mkrows()))
print(mkmap())
print(mkopt())
print(must(mkres()))
let plain = [[1, 2]]
print(plain)
let wrows = mkrows()
wrows[0][0] = 100
print("{wrows} {mkrows()}")
var shared: [][2]int = [[1, 1]]
fn addshared() {
    push(shared, [2, 2])
}
addshared()
print(shared)
let reader = fn() -> int {
    return shared[1][0] + rowsum(wrows)
}
print(reader())
shelf.rows[1][0] = 33
print(shelf.rows)
let shelf2 = shelf
shelf.opt = nil
print(shelf2.opt)

// ---- other sizes: padded to a word, copied by memcpy, too big for a frame ----
let w5: [5]i32 = [1, -2, 3, -4, 5]
let w5s: [][5]i32 = []
for i in 0..6 {
    push(w5s, w5)
    w5[i % 5] += 10
}
print(w5s)
let w20: [20]int
for i in 0..20 {
    w20[i] = i * i
}
let w20s: [][20]int = []
push(w20s, w20)
w20[0] = 7
push(w20s, w20)
insert(w20s, 1, w20)
print(w20s[1][19] + w20s[0][0] + w20s[2][0])
let huge: [10000]int
huge[9999] = 5
let huges: [][10000]int = []
push(huges, huge)
huge[9999] = 6
push(huges, huge)
print("{huges[0][9999]} {huges[1][9999]} {len(huges)}")
let hugem: {int: [10000]int} = {}
hugem[3] = huge
huge[0] = 1
print("{hugem[3][9999]} {hugem[3][0]}")
let hugen: ?[10000]int = huge
huge[0] = 2
if hugen != nil {
    print(hugen[0])
}
let one: [1]u8 = [9]
let ones: [][1]u8 = [one, one]
ones[1][0] = 4
print(ones)

// ---- a map of arrays that grows, reorders and loses keys ----
let churn: {int: [3]int} = {}
let seed = 12345
for i in 0..2000 {
    seed = (seed * 1103515245 + 12345) & 2147483647
    let k = seed % 997
    let v: [3]int = [k, i, k + i]
    churn[k] = v
    if i % 7 == 0 {
        remove(churn, (k + 3) % 997)
    }
}
let check = 0
for k, v in churn {
    check += v[0] + v[1] * 3 + v[2] * 5 + k
}
print("{len(churn)} {check}")

// rand.shuffle swaps through a held copy: an inline element's address
// would read back the slot it was just overwritten with.
struct Card {
    n: int
    face: str
}
let deck: []Card = []
for i in 0..20 { push(deck, Card{n: i, face: "c{i}"}) }
let dealt = rand.shuffle(deck)
let dsum = 0
for c in dealt { dsum += c.n }
print("{len(dealt)} {dsum}")
