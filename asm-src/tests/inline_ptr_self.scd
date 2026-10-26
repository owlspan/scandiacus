// A method whose closure captures self, called on a list element, a
// local and a loop variable over the list: the closure and the caller
// both see every change the method makes.

struct R {
    name: str
    n: int
}
impl R {
    fn bump(self) -> fn() -> int {
        self.n += 10
        let f = fn() -> int { return self.n }
        self.n += 5
        return f
    }
}
let rs: []R = []
push(rs, R{name: "a", n: 1})
let f = rs[0].bump()
print(rs[0].n)
print(f())
let p = R{name: "b", n: 2}
let g = p.bump()
print(p.n)
print(g())
for q in rs {
    let h = q.bump()
    print(h())
}
print(rs[0].n)
