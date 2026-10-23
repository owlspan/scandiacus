// Structs. A fixed set of named fields, laid out at compile time, so
// every access is a load or a store at an offset the compiler already
// knows.

struct Point {
    x: int
    y: int
}

struct Segment {
    from: Point
    to: Point
    label: str
}

fn lengthSquared(s: Segment) -> int {
    let dx = s.to.x - s.from.x
    let dy = s.to.y - s.from.y
    return dx * dx + dy * dy
}

// A struct is a value, not a reference. This takes a copy, so the
// caller's Point is untouched.
fn shifted(p: Point, by: int) -> Point {
    p.x += by
    p.y += by
    return p
}

let origin = Point{x: 0, y: 0}
let corner = Point{x: 3, y: 4}
print(origin)
print(corner.x)
print(corner.y)

// Fields left out of a literal take their zero value.
let partial = Point{x: 7}
print(partial)

// Assignment copies, so changing one name does not touch the other.
let moved = corner
moved.x = 99
print(corner.x)
print(moved.x)

// So does passing to a function.
let bumped = shifted(corner, 10)
print(corner)
print(bumped)

// Structs nest, by value all the way down.
let diag = Segment{from: origin, to: corner, label: "diagonal"}
print(diag)
print(lengthSquared(diag))
print(diag.to.y)

// And they go in the containers.
let path = [origin, corner, Point{x: 6, y: 8}]
print(path)
print(len(path))
print(path[2].x)

let named = {"start": origin, "end": corner}
print(named)
print(named["end"].y)

// Fields update in place through the variable that holds them.
let cursor = Point{x: 1, y: 1}
cursor.x += 4
cursor.y = cursor.x * 2
print(cursor)

// A struct carried through a result, since both landed together.
fn topRight(ps: []Point) -> Point! {
    if len(ps) == 0 {
        return fail("no points")
    }
    let best = ps[0]
    for i in 1..len(ps) {
        if ps[i].x + ps[i].y > best.x + best.y {
            best = ps[i]
        }
    }
    return best
}

print(must(topRight(path)))

// An empty list literal still needs an annotation to say what it
// holds: this backend infers an element type from the elements, and
// there are none.
let nowhere: []Point = []
print(errorOf(topRight(nowhere)))

// Methods. `self` is passed by reference, not copied, so one that
// changes a field changes the value it was called on - the Go backend
// declares every method on a pointer receiver and this has to match.
impl Point {
    fn length(self) -> float {
        return abs(self.x) + abs(self.y)
    }

    fn shift(self, by: int) {
        self.x += by
        self.y += by
    }

    fn show(self) -> str {
        return "(" + str(self.x) + ", " + str(self.y) + ")"
    }
}

let m = Point{x: 3, y: 4}
print(m.length())
print(m.show())
m.shift(10)
print(m.show())

// Through a list element, and through a field of another struct.
let pts = [Point{x: 1, y: 1}, Point{x: 2, y: 2}]
pts[0].shift(5)
print(pts[0].show())
print(str(pts))

let seg = Segment{from: Point{x: 0, y: 0}, to: Point{x: 6, y: 8}}
print(seg.to.length())
seg.from.shift(2)
print(seg.from.show())

// A method on a literal, which needs the parentheses to keep the brace
// out of the condition.
print((Point{x: 3, y: 4}).length())
