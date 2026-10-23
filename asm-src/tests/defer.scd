// defer: a statement run when its block is left, however it is left.

fn work(n: int) -> int {
    defer print("leaving work({n})")
    print("start")
    if n > 5 {
        defer print("big")
        return n * 2
    }
    defer print("small first")
    defer print("small second")
    return n
}
print(work(10))
print(work(1))
for i in 0..4 {
    defer print("end of trip {i}")
    if i == 1 {
        continue
    }
    if i == 3 {
        break
    }
    print("body {i}")
}
{
    let x = 1
    defer print("x was {x}")
    x = 2
}

// A deferred unlock releases the lock on every way out.
let m = thread.mutex()
fn guarded(m: int, fail: bool) -> str {
    thread.lock(m)
    defer thread.unlock(m)
    if fail {
        return "failed"
    }
    return "done"
}
print(guarded(m, true))
print(guarded(m, false))
print(guarded(m, false))
