// Floats kept in xmm6-xmm15 going into and out of struct fields. The
// selector stored and loaded those with mov, which has no form for an
// xmm register, and the build stopped with "the generated assembly could
// not be encoded". Each store below put its value in such a register.

struct Weapon {
    name: str
    speed: float
    depth: float
}

struct Rig {
    label: str
    weapons: []Weapon
}

fn faster(w: Weapon, by: float) -> Weapon {
    let s = w.speed * by
    if s < 50.0 {
        s = 50.0
    }
    w.speed = s
    return w
}

fn rebuilt(w: Weapon, by: float) -> Weapon {
    let s = w.speed * by
    if s < 50.0 {
        s = 50.0
    }
    return Weapon{name: w.name, speed: s, depth: w.depth}
}

fn bump(x: float) -> float {
    return x + 1.0
}

// A field loaded into a local that has to live across calls.
fn across(w: Weapon) -> float {
    let d = w.depth
    let a = bump(d)
    let b = bump(a)
    return d + a + b
}

// A variable straight into a field of a list element.
let ws = [Weapon{name: "a"}]
let v = 2.5
ws[0].speed = v
print(ws[0].speed)

// The same from a loop variable, and into a list inside a struct.
let rig = Rig{label: "r", weapons: [Weapon{name: "b"}]}
for x in [1.25, 3.75] {
    rig.weapons[0].depth = x
}
print(rig.weapons[0].depth)

// A builtin's result into a field.
let w = Weapon{name: "c"}
w.speed = max(1.5, 2.5)
w.depth = clamp(9.0, 0.0, 4.0)
print(w)

// Through a parameter, kept and clamped.
print(faster(Weapon{speed: 100.0}, 1.5).speed)
print(faster(Weapon{speed: 10.0}, 1.5).speed)
print(rebuilt(Weapon{name: "d", speed: 10.0, depth: 0.5}, 2.0))

// Loads of those fields, kept across calls.
print(across(Weapon{depth: 0.5}))
let total = 0.0
for k in 0..3 {
    total = total + ws[0].speed + rig.weapons[0].depth
    ws[0].speed = total
}
print(total)
