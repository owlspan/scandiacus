// Data blocks with a descriptor (mem.gcblock, a hook for the compiler's
// tests): the collector follows the pointer words of every element and
// no others, and a pointer into the middle of a block keeps it alive.
//
// Elements are two words, a child block and a number. The number word
// holds the address of a victim block, which the collector must not
// follow.

fn child(v: int) -> int {
    let c = mem.gcblock(0, 1, 1)
    mem.write64(c, v)
    return c
}

fn build(n: int) -> int {
    let b = mem.gcblock(1, 2, n)
    let e = 0
    while e < n {
        mem.write64(b + e * 16, child(1000 + e))
        mem.write64(b + e * 16 + 8, child(-1))
        e += 1
    }
    return b
}

// Overwrites freed slots, so a block freed by mistake shows up as wrong
// contents below.
fn churn() {
    let k = 0
    while k < 400 {
        let j = mem.gcblock(0, 1, 1)
        mem.write64(j, 77777)
        let w = mem.gcblock(0, 2, 40)
        mem.write64(w, 77777)
        k += 1
    }
}

// Reads the child of every element back.
fn check(b: int, n: int) -> bool {
    let e = 0
    while e < n {
        if mem.readI64(mem.readI64(b + e * 16)) != 1000 + e {
            return false
        }
        e += 1
    }
    return true
}

// The only reference left is an address inside element `at`.
fn inside(n: int, at: int) -> int {
    let b = build(n)
    return b + at * 16 + 8
}

fn garbage(n: int) {
    let k = 0
    while k < n {
        build(2)
        k += 1
    }
}

// Unreferenced blocks are collected: 300 builds make 1800 objects.
mem.collect()
let before = mem.objects()
garbage(300)
mem.collect()
print(mem.objects() - before < 100)

// A kept block of 300 elements is 601 objects, but the 300 victims are
// reachable only through number words, so 301 survive.
mem.collect()
let base = mem.objects()
let kept = build(300)
mem.collect()
let grown = mem.objects() - base
print(grown >= 301)
print(grown < 400)
print(check(kept, 300))

// Small block (80 bytes: a page slot) and large (4800: malloc).
let small = build(4)
let big = build(300)
churn()
mem.collect()
churn()
print(check(small, 4))
print(check(big, 300))

// Interior pointers only.
let p1 = inside(4, 2)
let p2 = inside(300, 250)
churn()
mem.collect()
churn()
print(check(p1 - 2 * 16 - 8, 4))
print(check(p2 - 250 * 16 - 8, 300))

// The last byte of the block.
let p3 = inside(300, 299) + 7
churn()
mem.collect()
churn()
print(check(p3 - 299 * 16 - 8 - 7, 300))
