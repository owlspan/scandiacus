// The collector.
//
// Mark and sweep. Roots are conservative - the stack is read as a flat
// array of words and anything that is the address of a known object is
// treated as live - because there is no stack map to say which slots
// hold pointers. What makes that sound rather than hopeful is that
// every virtual register lives in a stack slot: with no register
// allocator, there is no pointer that exists only in a machine register
// and could be missed.
//
// Tracing is precise. Every object carries a header saying what it is,
// so nothing has to guess which of its words are pointers.

struct Point { x: int  y: int }

// Things that must survive any number of collections.
let keep = ["alpha", "beta", "gamma"]
let scores = {"ada": 36, "alan": 41}
let grid = [[1, 2], [3, 4]]
let here = Point{x: 3, y: 4}
let maybe: ?str = "present"

// Something that makes a great deal of garbage and keeps none of it.
fn churn(rounds: int) -> int {
    let total = 0
    for i in 0..rounds {
        let s = "item-" + str(i)
        let parts = split(s, "-")
        total += len(parts)
    }
    return total
}

print(churn(300))
print(mem.objects() > 0)
print(mem.total() >= mem.used())

// Collecting drops what nothing points at and keeps what something
// does. Written as claims that hold whatever the cadence: normally the
// garbage from churn() is still sitting there and this collect frees
// it, so the count strictly falls; under SCANDIACUS_GC_STRESS=1 every
// statement collected on the way in, it is already gone, and all that
// is left to claim is that collecting did not grow anything and did
// run.
let before = mem.objects()
mem.collect()
print(mem.objects() <= before)
print(mem.collections() > 0)

print(str(keep))
print(str(scores))
print(str(grid))
print(here.x + here.y)
print(maybe == nil)

// Collecting repeatedly, with live data being changed between cycles,
// is where a collector that frees one live object shows itself.
for r in 0..20 {
    push(keep, "extra" + str(r))
    scores["n" + str(r)] = r
    mem.collect()
}
print(len(keep))
print(len(scores))
print(str(grid))
print(keep[0])
print(keep[len(keep) - 1])
print(scores["ada"])

// A collection with nothing live to find still works.
mem.collect()
mem.collect()
print(mem.collections() > 20)
print(str(keep) == str(keep))
