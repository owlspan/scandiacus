// Default parameter values, and generic methods with their types named.

enum Level { Low, High }
fn greet(name: str, greeting: str = "hello", times: int = 1, loud: bool = false) -> str {
    let s = ""
    for i in 0..times {
        s = s + greeting + ", " + name + "! "
    }
    if loud {
        return upper(s)
    }
    return s
}
fn scale(x: float, by: float = 2.0, shift: int = -1) -> float {
    return x * by + float(shift)
}
fn pick(l: Level = Level.High, note: ?str = nil) -> str {
    if note != nil {
        return "{l} {note}"
    }
    return "{l}"
}
struct Counter { n: int }
impl Counter {
    fn bump(self, by: int = 1) -> int {
        self.n += by
        return self.n
    }
}
print(greet("ada"))
print(greet("ada", "hi"))
print(greet("ada", "hi", 2))
print(greet("ada", "hi", 1, true))
print(scale(3.0))
print(scale(3.0, 10.0))
print(pick())
print(pick(Level.Low, "set"))
let c = Counter{}
c.bump()
c.bump(5)
print(c.n)

struct Bag { items: []int }
impl Bag {
    fn empty<U>(self) -> []U {
        let out: []U = []
        return out
    }
    fn mapAll<U>(self, f: fn(int) -> U) -> []U {
        let out: []U = []
        for x in self.items {
            push(out, f(x))
        }
        return out
    }
}
let bag2 = Bag{items: [1, 2]}
print(len(bag2.empty<str>()))
print(bag2.mapAll<str>(fn(x: int) -> str { return "#{x}" }))
let one = 1
let two = 2
print(one < two)
