// Generic functions and structs: each set of types a generic is used
// with gets its own copy, checked and compiled like hand-written code.

fn largest<T>(xs: []T) -> T {
    let best = xs[0]
    for x in xs {
        if x > best {
            best = x
        }
    }
    return best
}

fn mapList<T, U>(xs: []T, f: fn(T) -> U) -> []U {
    let out: []U = []
    for x in xs {
        push(out, f(x))
    }
    return out
}

fn filter<T>(xs: []T, keep: fn(T) -> bool) -> []T {
    let out: []T = []
    for x in xs {
        if keep(x) {
            push(out, x)
        }
    }
    return out
}

fn reduce<T, A>(xs: []T, start: A, f: fn(A, T) -> A) -> A {
    let acc = start
    for x in xs {
        acc = f(acc, x)
    }
    return acc
}

fn orDefault<T>(x: ?T, fallback: T) -> T {
    if x != nil {
        return x
    }
    return fallback
}

fn lookup<K, V>(m: {K: V}, k: K, fallback: V) -> V {
    if has(m, k) {
        return m[k]
    }
    return fallback
}

fn swap<A, B>(p: Pair<A, B>) -> Pair<B, A> {
    return Pair<B, A>{first: p.second, second: p.first}
}

struct Pair<A, B> {
    first: A
    second: B
}

struct Stack<T> {
    items: []T
}

impl Stack<T> {
    fn push(self, x: T) {
        push(self.items, x)
    }
    fn pop(self) -> T {
        return pop(self.items)
    }
    fn peek(self) -> ?T {
        if len(self.items) == 0 {
            return nil
        }
        return self.items[len(self.items) - 1]
    }
    fn size(self) -> int {
        return len(self.items)
    }
    fn self_(self) -> Stack<T> {
        return self
    }
}

// A tree whose children are more of itself, through a list.
struct Tree<T> {
    value: T
    kids: []Tree<T>
}

fn total(t: Tree<int>) -> int {
    let sum = t.value
    for k in t.kids {
        sum += total(k)
    }
    return sum
}

enum Suit { Hearts, Spades }

struct Card {
    rank: int
    suit: Suit
}

print(largest([3, 9, 2]))
print(largest(["pear", "apple", "zoo"]))
print(largest([1.5, 0.5]))

let squares = mapList([1, 2, 3, 4], fn(x: int) -> int { return x * x })
print(squares)
print(mapList(squares, fn(x: int) -> str { return "#" + str(x) }))
print(filter(["a", "bb", "ccc"], fn(s: str) -> bool { return len(s) > 1 }))
print(reduce([1, 2, 3], "", fn(acc: str, x: int) -> str { return acc + str(x) }))
print(reduce([1.5, 2.5], 0.0, fn(acc: float, x: float) -> float { return acc + x }))

let none: ?int = nil
print(orDefault(none, 7))
print(orDefault("set", "unset"))

let ages = {"ada": 36}
print(lookup(ages, "ada", 0))
print(lookup(ages, "bob", -1))

let p = Pair<str, int>{first: "age", second: 36}
let q = swap(p)
print(q.first + 1)
print(q.second)

let s = Stack<int>{}
s.push(1)
s.push(2)
s.push(3)
print(s.size())
print(s.pop())
print(s.peek())
print(s.self_().size())
let empty = Stack<str>{}
print(empty.peek() == nil)

let deck = Stack<Card>{}
deck.push(Card{rank: 12, suit: Suit.Spades})
print(deck.pop().rank)

let nested = Stack<Stack<int>>{}
nested.push(s)
print(nested.pop().size())

let suits = Stack<Suit>{}
suits.push(Suit.Hearts)
print(suits.size())

let t = Tree<int>{value: 1, kids: [Tree<int>{value: 2}, Tree<int>{value: 3, kids: [Tree<int>{value: 4}]}]}
print(total(t))

// Named types where the arguments cannot say.
let none2: []int = []
print(len(mapList<int, str>(none2, fn(x: int) -> str { return str(x) })))

// Comparisons still read as comparisons.
let a = 1
let b = 2
let c = 3
print(a < b)
print(b > c)
if a < b {
    print("less")
}

// Methods with type parameters of their own.
struct Box2<T> { v: T }
impl Box2<T> {
    fn map<U>(self, f: fn(T) -> U) -> Box2<U> {
        return Box2<U>{v: f(self.v)}
    }
    fn get(self) -> T { return self.v }
}
struct Bag { items: []int }
impl Bag {
    fn mapAll<U>(self, f: fn(int) -> U) -> []U {
        let out: []U = []
        for x in self.items {
            push(out, f(x))
        }
        return out
    }
}
let bx = Box2<int>{v: 21}
let sx = bx.map(fn(x: int) -> str { return "n={x * 2}" })
print(sx.get())
print(bx.map(fn(x: int) -> float { return float(x) / 2.0 }).get())
let bag = Bag{items: [1, 2, 3]}
print(bag.mapAll(fn(x: int) -> str { return "<{x}>" }))
print(bag.mapAll(fn(x: int) -> bool { return x > 1 }))
