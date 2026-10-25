// A float passed fifth, past the four argument registers, while the
// register allocator keeps it in an xmm register: the move into the
// outgoing area has to be a movsd. This one used to stop the build.
fn spread(a: int, x: float, b: int, c: int, y: float) -> int {
    let s = 0
    for i in 0..a {
        s += b + c + int(x) + int(y)
    }
    return s
}

fn gather(p0: float, p1: int, p2: float, p3: int) -> int {
    let s = 0
    for i in 0..3 {
        s += p3 + int(p2 * 3.0) + spread(7, p2, p1, 1, p0)
    }
    return s
}

print(gather(1.5, 2, 0.5, 4))
print(gather(-2.5, 1, 4.0, 0))
