// A struct the program declares keeps its own fields even when the
// library has a struct of the same name for calls this program never
// makes.

struct ProcModule {
    id: int
}

struct Request {
    id: int
}

let m = ProcModule{id: 3}
let q = Request{id: 4}
print(m.id + q.id)
print(sin(0.0))
