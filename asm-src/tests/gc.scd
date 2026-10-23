// Collection runs by itself: a loop that makes a great deal of garbage
// keeps a bounded heap, and what it holds on to survives.

let keep: []str = []
let peak = 0
let i = 0
while i < 300000 {
    let junk = [str(i), "x" + str(i * 2), "padding padding padding padding"]
    let m = {"a": junk[0], "b": junk[1]}
    if i % 1000 == 0 {
        push(keep, m["a"])
    }
    if mem.used() > peak {
        peak = mem.used()
    }
    i += 1
}
print(len(keep))
print(keep[0] + " " + keep[299])
print(mem.collections() > 0)
print(peak < 64 * 1024 * 1024)
