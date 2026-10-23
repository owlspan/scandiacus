// Threads: spawn and join, atomics, a mutex, and channels.

let counter = atomic.new(0)
let m = thread.mutex()
let shared: []int = []
let ts: []int = []
let i = 0
while i < 8 {
    let id = i
    push(ts, thread.spawn(fn() {
        let k = 0
        while k < 10000 {
            atomic.add(counter, 1)
            k += 1
        }
        let s = "worker " + str(id)
        thread.lock(m)
        push(shared, len(s))
        thread.unlock(m)
    }))
    i += 1
}
for t in ts {
    thread.join(t)
}
print(atomic.get(counter))
print(len(shared))
print(thread.cores() > 0)
print(atomic.cas(counter, 80000, 5))
print(atomic.get(counter))
print(atomic.cas(counter, 1, 2))

// Eight threads allocating at once, beside main: the allocation lock.
let unused = thread.mutex()
let total2 = atomic.new(0)
let ts2: []int = []
let i2 = 0
while i2 < 8 {
    let id = i2
    push(ts2, thread.spawn(fn() {
        let k2 = 0
        let acc: []str = []
        while k2 < 20000 {
            push(acc, "t{id}-{k2}")
            if len(acc) > 100 {
                let fresh: []str = []
                acc = fresh
            }
            k2 += 1
        }
        atomic.add(total2, len(acc))
    }))
    i2 += 1
}
let mine: []str = []
let k2 = 0
while k2 < 20000 {
    push(mine, str(k2))
    k2 += 1
}
for t in ts2 {
    thread.join(t)
}
print(atomic.get(total2))
print(len(mine))
mem.collect()
print(mem.objects() < 100000)

// A worker pool fed and drained through channels.
let jobs = channel<int>()
let results = channel<str>()
let workers: []int = []
let w3 = 0
while w3 < 4 {
    let id = w3
    push(workers, thread.spawn(fn() {
        while true {
            let j = jobs.recv()
            if j == nil {
                break
            }
            results.send("{j * j}")
        }
    }))
    w3 += 1
}
let n3 = 0
while n3 < 1000 {
    jobs.send(n3)
    n3 += 1
}
jobs.close()
for t in workers {
    thread.join(t)
}
results.close()
let sum3 = 0
let count3 = 0
while true {
    let r = results.recv()
    if r == nil {
        break
    }
    sum3 += toInt(r, 0)
    count3 += 1
}
print(count3)
print(sum3)
