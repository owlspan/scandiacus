// A library: the top level runs once, on load.
print("mathmod loaded")

export fn add(a: int, b: int) -> int {
    return a + b
}

export fn hypotenuse(a: float, b: float) -> float {
    return sqrt(a * a + b * b)
}

export fn shout(s: str) -> int {
    print(upper(s))
    return len(s)
}

export fn either(a: bool, b: bool) -> bool {
    return a || b
}

extern struct Pair { x: i32, y: i32 }
export fn swap(p: Pair) {
    let t = p.x
    p.x = p.y
    p.y = t
}

// Fixed-width arguments. Native code may leave anything above an
// argument's width, so the export's stub extends each one first.
export fn narrow(a: u8, b: i8, c: u16, d: i16) {
    print("{a} {b} {c} {d}")
}

export fn narrow6(a: int, b: int, c: int, d: int, e: u32, f: i8) {
    print("{a + b + c + d} {e} {f}")
}

// An f32 arrives as a single and goes back as one.
export fn blend(a: f32, b: f32, t: f32) -> f32 {
    return a + (b - a) * t
}
