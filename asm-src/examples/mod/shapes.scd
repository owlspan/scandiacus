// An imported file can only declare things. `pub` is what makes a
// declaration reachable from the file that imports it.

pub const UNIT = 1.0

// Not pub: private to this file, and used by circleArea below.
const half = 0.5

pub struct Box {
    w: float
    h: float
}

impl Box {
    fn area(self) -> float {
        return self.w * self.h
    }

    fn grow(self, by: float) {
        self.w += by
        self.h += by
    }
}

pub fn circleArea(r: float) -> float {
    return PI * r * r * half * 2.0
}

pub fn describe(b: Box) -> str {
    return "box " + str(b.w) + "x" + str(b.h)
}
