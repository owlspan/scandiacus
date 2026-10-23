pub const TAU = 6.0

pub struct Vec {
    x: float
    y: float
}

impl Vec {
    fn len2(self) -> float {
        return self.x * self.x + self.y * self.y
    }
}

pub enum Kind {
    Flat
    Round
}

pub fn area(r: float) -> float {
    return half(TAU) * r * r
}

fn half(v: float) -> float {
    return v / 2.0
}

pub fn make(x: float, y: float) -> Vec {
    let area = x
    return Vec{x: area, y: y}
}

pub fn kindOf(v: Vec) -> Kind {
    if v.len2() > 1.0 {
        return Kind.Round
    }
    return Kind.Flat
}
