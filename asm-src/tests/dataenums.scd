// Enums whose variants carry values, and a match that names them.

enum Shape {
    Circle(r: float)
    Rect(w: float, h: float)
    Empty
}

fn area(s: Shape) -> float {
    match s {
        Shape.Circle(r) => return 3.0 * r * r
        Shape.Rect(w, h) => return w * h
        Shape.Empty => return 0.0
    }
    return -1.0
}

// A tree of itself, through a list: an expression and its evaluator.
enum Expr {
    Num(n: int)
    Add(items: []Expr)
    Mul(a: Expr, b: Expr)
    Neg(x: Expr)
}

fn eval(e: Expr) -> int {
    match e {
        Expr.Num(n) => return n
        Expr.Add(items) => {
            let sum = 0
            for it in items {
                sum += eval(it)
            }
            return sum
        }
        Expr.Mul(a, b) => return eval(a) * eval(b)
        Expr.Neg(x) => return -eval(x)
    }
    return 0
}

enum Option<T> {
    Some(v: T)
    None
}

enum Result2<T, E> {
    Ok(v: T)
    Err(e: E)
}

fn indexOf2(xs: []str, want: str) -> Option<int> {
    for i, x in xs {
        if x == want {
            return Option.Some(i)
        }
    }
    return Option.None
}

fn parseAge(s: str) -> Result2<int, str> {
    let n = toInt(s, -1)
    if n < 0 {
        return Result2<int, str>.Err("not an age: " + s)
    }
    return Result2<int, str>.Ok(n)
}

fn unwrapOr<T>(o: Option<T>, fallback: T) -> T {
    match o {
        Option.Some(v) => return v
        Option.None => return fallback
    }
    return fallback
}

enum Event {
    Key(code: int, down: bool)
    Click(x: int, y: int)
    Quit
}

let shapes = [Shape.Circle(2.0), Shape.Rect(3.0, 4.5), Shape.Empty]
for s in shapes {
    print("{s} has area {area(s)}")
}
print(shapes)

let e = Expr.Add([Expr.Num(1), Expr.Mul(Expr.Num(2), Expr.Num(3)), Expr.Neg(Expr.Num(4))])
print(e)
print(eval(e))

print(indexOf2(["a", "b", "c"], "c"))
print(indexOf2(["a"], "z"))
print(unwrapOr(indexOf2(["a", "b"], "b"), -1))
print(unwrapOr(Option.Some("hi"), "none"))
let nothing: Option<str> = Option.None
print(unwrapOr(nothing, "none"))

for s in ["36", "abc"] {
    match parseAge(s) {
        Result2.Ok(n) => print("age {n}")
        Result2.Err(msg) => print(msg)
    }
}

// Arms that share a body, _ for a value not needed, and an else.
let events = [Event.Key(65, true), Event.Click(3, 4), Event.Key(66, false), Event.Quit, Event.Click(0, 0)]
let clicks = 0
for ev in events {
    match ev {
        Event.Key(code, _) => print("key {code}")
        Event.Click(x, y) => {
            if x == 0 && y == 0 {
                continue
            }
            clicks += 1
        }
        Event.Quit => break
    }
}
print(clicks)

match Event.Quit {
    Event.Key, Event.Click => print("input")
    else => print("other")
}

// A closure made in an arm keeps the value it names.
let fs: []fn() -> int = []
for ev in events {
    match ev {
        Event.Click(x, y) => push(fs, fn() -> int { return x * 10 + y })
        else => print("skip {ev}")
    }
}
for f in fs {
    print(f())
}

// Equality compares variant and values.
print(Shape.Empty == Shape.Empty)
print(Shape.Circle(1.0) == Shape.Circle(1.0))
print(Shape.Circle(1.0) == Shape.Circle(2.0))
print(Shape.Circle(1.0) != Shape.Empty)

// Data enums in maps and structs.
struct Player {
    name: str
    held: Option<str>
}
let p = Player{name: "ada", held: Option.Some("lamp")}
print(p)
let byName = {"a": Shape.Circle(1.0), "b": Shape.Empty}
print(byName["a"])

// Types that hold themselves print and compare through a function of
// their own, however deep they go.
let e2 = Expr.Add([Expr.Num(1), Expr.Mul(Expr.Num(2), Expr.Num(3)), Expr.Neg(Expr.Num(4))])
print(e == e2)
print(e == Expr.Neg(e2))
struct Node {
    v: int
    kids: []Node
}
let tree = Node{v: 1, kids: [Node{v: 2}, Node{v: 3, kids: [Node{v: 4}]}]}
print(tree)
print(tree == Node{v: 1, kids: [Node{v: 2}, Node{v: 3, kids: [Node{v: 4}]}]})
print(tree == Node{v: 1})
print("{Expr.Neg(Expr.Num(5))}")
