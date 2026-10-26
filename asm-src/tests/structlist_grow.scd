// Inline struct lists and code that grows the list it is working on:
// a for-in body that pushes, and a method on an element that pushes
// onto the same list. Both must write where the element now is.
struct P {
    x: int
    y: float
}

var ps: []P = []

fn addOne() {
    push(ps, P{x: 100, y: 0.5})
}

impl P {
    fn bump(self) {
        self.x += 1
        addOne()
        self.x += 10
    }
}

for i in 0..3 {
    push(ps, P{x: i, y: float(i)})
}
// The body grows the list it walks: writes through p after the push must
// land in the element, wherever the block now is.
for p in ps {
    push(ps, P{x: 0, y: 0.0})
    p.x = p.x * 7
    p.y += 1.5
}
for p in ps { print("{p.x} {p.y}") }
let q = ps
q[1].bump()
print(ps[1].x)
print(len(ps))
// A loop that does not grow anything keeps the address.
for p in ps { p.x += 1 }
print(ps[0].x)
