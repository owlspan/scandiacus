// Collection while threads run. Workers make garbage fast and each
// keeps one list alive the whole time; main collects while they work.
// If a collection missed a root on another thread's stack, a kept list
// would be freed under its owner and its check would come out wrong.

let before = mem.collections()
let go = atomic.new(0)
let ch = channel<str>()

// One thread spins on an atomic and allocates nothing: it can only be
// stopped at the check in its loop.
let spinner = thread.spawn(fn() {
    while atomic.get(go) == 0 {
    }
})

// One thread is blocked in a channel the whole time.
let waiter = thread.spawn(fn() {
    let got = ch.recv()
    print("waiter got {got}")
})

let ts: []int = []
let sums = atomic.new(0)
let w = 0
while w < 4 {
    let id = w
    push(ts, thread.spawn(fn() {
        let kept: []str = []
        let k = 0
        while k < 200 {
            push(kept, "w{id}-{k}")
            k += 1
        }
        let round = 0
        while round < 300 {
            let junk: []str = []
            let j = 0
            while j < 200 {
                push(junk, "garbage {round} {j} from {id}")
                j += 1
            }
            round += 1
        }
        let ok = 0
        let i = 0
        while i < 200 {
            if kept[i] == "w{id}-{i}" {
                ok += 1
            }
            i += 1
        }
        atomic.add(sums, ok)
    }))
    w += 1
}

// Main makes garbage too, and asks for collections outright.
let r = 0
while r < 50 {
    let junk: []str = []
    let j = 0
    while j < 1000 {
        push(junk, "main {r} {j}")
        j += 1
    }
    mem.collect()
    r += 1
}

for t in ts {
    thread.join(t)
}
let during = mem.collections() - before
ch.send("done")
thread.join(waiter)
atomic.set(go, 1)
thread.join(spinner)

print(atomic.get(sums))
print(during >= 50)
print(mem.used() < 20000000)
print(mem.total() > 4 * mem.used())
