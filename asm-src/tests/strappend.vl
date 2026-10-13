// += appends in place to a string only one variable holds. Nothing
// read from the variable before an append may change after it.
let s = "ab"
s += "cd"
let t = s
s += "ef"
print("{t} {s}")

let xs: []str = []
let u = ""
for i in 0..5 {
    u += "x"
    push(xs, u)
}
print(join(xs, ","))

fn grow(p: str) -> str {
    let q = p
    q += "!"
    return q
}
let base = "hi"
base += "?"
let g = grow(base)
base += "?"
print("{g} {base}")

let w = ""
while len(w) < 70 {
    w += "ab"
}
print("{len(w)} {w}")

let v = "z"
v = v + "y"
let keep = v
v = v + "x"
print("{keep} {v}")

let e = "e"
e += ""
e += e
e += e
print(e)

let m: {str: str} = {}
let k = "key"
k += "1"
m[k] = "a"
k += "2"
m[k] = "b"
print("{m["key1"]} {m["key12"]} {len(m)}")

// Across collections, and a big one.
let big = ""
for i in 0..3000 {
    big += "{i % 10}"
    if i % 500 == 0 {
        mem.collect()
    }
}
let snap = big
big += "end"
print("{len(snap)} {len(big)} {substr(big, 2995, 3003)}")
