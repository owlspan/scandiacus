import "geo.scd" as geo

pub fn diag(v: geo.Vec) -> float {
    return v.len2()
}

pub fn unit() -> geo.Vec {
    return geo.Vec{x: 1.0, y: 0.0}
}
