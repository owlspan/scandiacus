// Closures as callbacks. One passed straight to an extern function holds
// a slot for that call only, so the 200 sorts below never run out of the
// 64 slots. mem.callback(closure) holds one until mem.freeCallback, the
// closure stays alive through a collection meanwhile, and the address
// returns 0 once freed.
extern fn qsort(base: ptr, n: int, size: int, cmp: fn(ptr, ptr) -> int)
extern fn bsearch(key: ptr, base: ptr, n: int, size: int, cmp: fn(ptr, ptr) -> int) -> ptr

var values = [42, -7, 19, 0, 1000, 3, -250, 8]
var buf = mem.alloc(len(values) * 8)

fn fill() {
    for i in 0..len(values) {
        mem.write64(buf + i * 8, values[i])
    }
}

fn dump() -> []int {
    let out: []int = []
    for i in 0..len(values) {
        push(out, mem.readI64(buf + i * 8))
    }
    return out
}

fn sortSigned(sign: int) {
    let calls: []int = []
    qsort(buf, len(values), 8, fn(a: int, b: int) -> int {
        push(calls, 1)
        let x = mem.readI64(a)
        let y = mem.readI64(b)
        if x < y {
            return -sign
        }
        if x > y {
            return sign
        }
        return 0
    })
    if len(calls) == 0 {
        print("comparator never called")
    }
}

fill()
sortSigned(1)
print(dump())
sortSigned(-1)
print(dump())

for round in 0..200 {
    fill()
    sortSigned(1 - (round % 2) * 2)
}
print(dump())

let mod = 30
fill()
qsort(buf, len(values), 8, fn(a: int, b: int) -> int {
    return (mem.readI64(a) % mod) - (mem.readI64(b) % mod)
})
print(dump())

fill()
sortSigned(1)
let key = mem.alloc(8)
for v in [19, 4, -250] {
    mem.write64(key, v)
    let hit = bsearch(key, buf, len(values), 8, fn(a: int, b: int) -> int {
        return mem.readI64(a) - mem.readI64(b)
    })
    if hit == 0 {
        print("{v} not found")
    } else {
        print("{v} at {(hit - buf) / 8}")
    }
}
mem.free(key)

let base = 1000
let scale = 2.5
let cb = mem.callback(fn(a: int, b: int) -> int { return base + a * 10 + b })
let fcb = mem.callback(fn(x: float, n: int) -> float { return x * scale + float(n) })
mem.collect()
print(mem.call(cb, 4, 2))
print(mem.callF(fcb, 2.0, 1))
mem.freeCallback(cb)
mem.freeCallback(fcb)
print(mem.call(cb, 4, 2))

for i in 0..100 {
    let k = mem.callback(fn(a: int) -> int { return a + i })
    if mem.call(k, 1) != i + 1 {
        print("wrong at {i}")
    }
    mem.freeCallback(k)
}
print("done")
mem.free(buf)
