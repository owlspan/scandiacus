// x + x where x is a known float: the constant is read from memory as
// the second operand, and still has to be in a register as the first.
fn triple(p: float) -> int {
    p += p
    return int(p * 3.0)
}

for i in 1..3 {
    print(triple(0.5))
}
let h = 1.25
print(h + h)
print(h * h - h)
