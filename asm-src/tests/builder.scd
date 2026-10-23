// Builder: a string from many pieces in linear time.

let sb = Builder{}
for i in 0..5 {
    sb.add("{i},")
}
sb.addLine("done")
print(sb.str())
print(sb.len())
sb.clear()
print(sb.str() == "")
let big = Builder{}
let i = 0
while i < 5000 {
    big.add("x")
    i += 1
}
print(big.len())
fn build(parts: []str) -> str {
    let b = Builder{}
    for p in parts {
        b.add(p)
        b.add("-")
    }
    return b.str()
}
print(build(["a", "b", "c"]))
