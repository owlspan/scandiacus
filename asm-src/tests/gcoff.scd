// gc off: nothing is collected, and delete frees a value by hand.
gc off

struct Particle {
    x: float
    y: float
    tag: str
}

let start = mem.objects()

// Lists, maps, structs, strings and bytes, made and freed in a loop:
// the heap ends where it started.
let i = 0
let checksum = 0
while i < 200000 {
    let xs = [1, 2, 3]
    push(xs, i)
    push(xs, i)
    checksum += xs[4]
    let m = {"a": i}
    checksum += m["a"]
    let p = Particle{x: 1.0, y: 2.0, tag: "p"}
    let s = "n" + str(i) + "!"
    checksum += len(s)
    let b = bytes.of("abc")
    delete(xs)
    delete(m)
    delete(p)
    delete(s)
    delete(b)
    i += 1
}
print(checksum)
print(mem.objects() - start < 5)

// Temporaries made by + and interpolation are freed as they are used,
// so a loop that only prints does not grow either.
i = 0
let before = mem.objects()
while i < 1000 {
    if i % 250 == 0 {
        print("step {i} of " + str(1000) + " done")
    }
    i += 1
}
print(mem.objects() - before < 5)

// A literal lives in the program itself, and deleting it is harmless.
let lit = "still here"
delete(lit)
print(lit)

// Values that outlive the loop are kept until deleted.
let keep: []str = []
i = 0
while i < 5 {
    push(keep, "item {i}")
    i += 1
}
print(keep)
for k in keep {
    delete(k)
}
delete(keep)
