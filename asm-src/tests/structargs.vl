// A struct argument is a copy, whether or not the compiler makes one.
// A function that only reads its parameter's fields gets the caller's
// object itself; one that writes a field, calls something that could,
// or keeps the value gets a real copy. Each case here would print
// something different if the wrong one were chosen.
struct V {
    x: int
    inner: W
}

struct W {
    y: int
}

var g = V{x: 1, inner: W{y: 2}}

fn readAfterCall(p: V) -> int {
    let before = p.x
    bump()
    return before * 100 + p.x
}

fn bump() {
    g.x = g.x + 10
}

fn onlyReads(p: V) -> int {
    return p.x * 2 + p.inner.y
}

fn writesField(p: V) -> int {
    p.x = 99
    return p.x
}

fn writesNested(p: V) -> int {
    p.inner.y = 77
    return p.inner.y
}

fn keeps(p: V) -> V {
    return p
}

fn total(xs: []V) -> int {
    let t = 0
    for v in xs {
        t += onlyReads(v)
    }
    return t
}

print(readAfterCall(g))
print(g.x)
let a = V{x: 5, inner: W{y: 3}}
print(onlyReads(a))
print(writesField(a))
print(a.x)
print(writesNested(a))
print(a.inner.y)
let b = keeps(a)
b.x = 1000
print(a.x)
print(total([a, b, g]))
