// print and write show a nullable's value or nil, not the box it is kept in.
let a: ?int = 5
print(a)
let b: ?int = nil
print(b)
let c: ?str = "x"
print(c)
print("{a}")
let fa: ?float = 1.5
print(fa)
let bb: ?bool = true
print(bb)
struct P { x: int }
let pc: ?P = P{x: 1}
print(pc)
let d: ?int = 4
write(d)
write("\n")
let e: ?str = nil
write(e)
write("\n")
