// A tour of what Scandiacus gained in 0.26 to 0.35, in one program.
//
//   scandiacus run tour.scd

// Generics: one function, any list.
fn largest<T>(xs: []T) -> T {
    let best = xs[0]
    for x in xs {
        if x > best {
            best = x
        }
    }
    return best
}

// An enum whose variants carry values, taken apart by a match.
enum Shape {
    Circle(r: float)
    Rect(w: float, h: float)
}

fn area(s: Shape) -> float {
    match s {
        Shape.Circle(r) => return 3.14159 * r * r
        Shape.Rect(w, h) => return w * h
    }
    return 0.0
}

// An interface: any struct with the method is one.
interface Named {
    fn name(self) -> str
}

struct Robot { id: int }
impl Robot {
    fn name(self) -> str { return "robot-{self.id}" }
}

struct Cat { called: str }
impl Cat {
    fn name(self) -> str { return self.called }
}

// A default parameter value.
fn greet(who: Named, greeting: str = "hello") -> str {
    return "{greeting}, {who.name()}"
}

print(largest([3, 9, 2]))
print(largest(["pear", "zoo", "apple"]))

for s in [Shape.Circle(1.0), Shape.Rect(2.0, 3.0)] {
    print("{s} has area {area(s)}")
}

let crowd: []Named = [Robot{id: 7}, Cat{called: "tom"}]
for n in crowd {
    print(greet(n))
}
print(greet(Cat{called: "felix"}, "welcome"))

// Threads and a channel: four workers square numbers, main sums them.
let jobs = channel<int>()
let results = channel<int>()
let workers: []int = []
for w in 0..4 {
    push(workers, thread.spawn(fn() {
        for j in jobs {
            results.send(j * j)
        }
    }))
}
for n in 1..=100 {
    jobs.send(n)
}
jobs.close()
for t in workers {
    thread.join(t)
}
results.close()
let total = 0
for r in results {
    total += r
}
print("sum of squares 1..100 = {total}")

// defer, and a Builder for a long string.
fn report(parts: []str) -> str {
    let sb = Builder{}
    defer print("report built from {len(parts)} parts")
    for p in parts {
        sb.add(p)
        sb.add(" ")
    }
    return sb.str()
}
print(report(["scandiacus", "is", "a", "real", "language", "now"]))
