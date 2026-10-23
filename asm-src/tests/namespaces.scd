// import "file.scd" as name: the file's names are reached as name.x, so
// two files exporting the same names do not collide. ns/geo.scd and
// ns/shapes.scd both export area and Vec.
import "ns/geo.scd" as geo
import "ns/shapes.scd" as shapes
import "ns/gen.scd" as g
import "ns/mid.scd" as mid

print(geo.area(2.0))
print(shapes.area(3))

// A struct, its methods, a type annotation, and a local inside geo.scd
// that shadows one of its own top-level names.
let v: geo.Vec = geo.make(3.0, 4.0)
print(v.len2())
let s = shapes.Vec{w: 7}
print(s.w)
let u = geo.Vec{x: 1.0, y: 0.0}
print(u.x + geo.TAU)

// An enum, and a list of a namespaced type.
print(geo.kindOf(v) == geo.Kind.Round)
let vs: []geo.Vec = [v, u]
print(len(vs))

// Generics declared in a namespaced file, instantiated with a type from
// another one.
let b = g.wrap(5)
print(b.get())
let c: g.Box<str> = g.wrap("hi")
print(c.item)
print(g.biggest([3, 9, 2], fn(x: int, y: int) -> bool { return x > y }))
let boxes: []g.Box<geo.Vec> = [g.wrap(geo.make(1.0, 2.0))]
print(boxes[0].get().y)

// A namespaced file using another namespace this file also imports.
let one = mid.unit()
print(mid.diag(geo.make(2.0, 1.0)) + one.x)

// A local may take a namespace's name; it is just a local then.
fn shadow() -> int {
    let geo = 41
    return geo + 1
}
print(shadow())
