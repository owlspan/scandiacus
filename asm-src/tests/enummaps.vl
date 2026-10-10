// Maps keyed by an enum: the key is the variant's number underneath,
// and printing, iterating and keys() still show it by name.
enum Color { Red, Green, Blue }
let m: {Color: int} = {}
m[Color.Red] = 1
m[Color.Blue] = 3
print(m[Color.Red])
print(m)
for k, v in m {
    print("{k} {v}")
}
print(keys(m))
print(has(m, Color.Green))
let lit = {Color.Green: "g", Color.Red: "r"}
print(lit)
print(str(m))
fn tallyOf(xs: []Color) -> {Color: int} {
    let out: {Color: int} = {}
    for c in xs {
        out[c] = out[c] + 1
    }
    return out
}
let tally = tallyOf([Color.Red, Color.Blue, Color.Red])
print(tally)
print(tally == {Color.Red: 2, Color.Blue: 1})
remove(tally, Color.Red)
print(tally)
let nested: {Color: []str} = {}
nested[Color.Green] = ["leaf", "grass"]
print(nested)
