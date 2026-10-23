// Interfaces: any struct with the methods is one.

interface Shape {
    fn area(self) -> float
    fn name(self) -> str
    fn grow(self, by: float)
}

struct Square { side: float }
impl Square {
    fn area(self) -> float { return self.side * self.side }
    fn name(self) -> str { return "square" }
    fn grow(self, by: float) { self.side += by }
}

struct Circle { r: float }
impl Circle {
    fn area(self) -> float { return 3.0 * self.r * self.r }
    fn name(self) -> str { return "circle" }
    fn grow(self, by: float) { self.r += by }
}

fn total(shapes: []Shape) -> float {
    let t = 0.0
    for s in shapes {
        t += s.area()
    }
    return t
}

let shapes: []Shape = [Square{side: 2.0}, Circle{r: 1.0}]
push(shapes, Square{side: 1.0})
for s in shapes {
    print("{s.name()}: {s.area()}")
}
print(total(shapes))
let one: Shape = Circle{r: 2.0}
one.grow(1.0)
print(one.area())
print(one)
print(shapes)
match one {
    Shape.Circle(c) => print("circle of radius {c.r}")
    Shape.Square(sq) => print("square {sq.side}")
}
print(shapes[0] == Square{side: 2.0})

// Generic structs, returns, nullables and maps.
interface Named2 {
    fn name(self) -> str
}
struct Box<T> { v: T }
impl Box<T> { fn name(self) -> str { return "box of {self.v}" } }
struct P { n: str }
impl P { fn name(self) -> str { return self.n } }
fn pick(big: bool) -> Named2 {
    if big {
        return Box<int>{v: 9}
    }
    return P{n: "p"}
}
fn maybe(x: int) -> ?Named2 {
    if x > 0 {
        return P{n: "some"}
    }
    return nil
}
print(pick(true).name())
print(pick(false).name())
let m: {str: Named2} = {"a": P{n: "in map"}}
print(m["a"].name())
let mm = maybe(1)
if mm != nil {
    print(mm.name())
}
print(maybe(0) == nil)

// An interface can be a type argument, and a generic function works on
// one as on anything else.
fn names<T>(xs: []T, f: fn(T) -> str) -> []str {
    let out: []str = []
    for x in xs {
        push(out, f(x))
    }
    return out
}
print(names(shapes, fn(s: Shape) -> str { return s.name() }))

// An interface's method can take another interface.
interface Visitor {
    fn visit(self, s: Shape) -> str
}
struct Namer { prefix: str }
impl Namer {
    fn visit(self, s: Shape) -> str { return self.prefix + s.name() }
}
let v: Visitor = Namer{prefix: "> "}
for s in shapes {
    print(v.visit(s))
}
