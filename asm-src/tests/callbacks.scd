// Callbacks: Scandiacus functions called from native code, through the C
// runtime's qsort and the Windows EnumWindows.

extern fn qsort(base: ptr, n: int, size: int, cmp: fn(ptr, ptr) -> int)

fn byValue(a: int, b: int) -> int {
    return mem.readI32(a) - mem.readI32(b)
}
fn backwards(a: int, b: int) -> int {
    return mem.readI32(b) - mem.readI32(a)
}

let data = [42, -7, 19, 0, 1000, 3, -250, 8]
let buf = mem.alloc(len(data) * 4)
let i = 0
for v in data {
    mem.write32(buf + i * 4, v)
    i += 1
}
fn dump(buf: int, n: int) -> []int {
    let out: []int = []
    let j = 0
    while j < n {
        push(out, mem.readI32(buf + j * 4))
        j += 1
    }
    return out
}
qsort(buf, len(data), 4, byValue)
print(dump(buf, len(data)))
qsort(buf, len(data), 4, backwards)
print(dump(buf, len(data)))

// A window enumerator: a handle and an LPARAM, both declared ptr since
// both are 64 bits, and a BOOL coming back.
extern fn EnumWindows(each: fn(ptr, ptr) -> bool, data: ptr) -> bool from "user32"
let seen = mem.alloc(8)
fn onWindow(hwnd: int, counter: int) -> bool {
    mem.write64(counter, mem.readI64(counter) + 1)
    return true
}
print(EnumWindows(onWindow, seen))
print(mem.readI64(seen) >= 0)

// A callback that allocates and runs the collector, with qsort's own
// frames on the stack between it and everything the program holds.
const names = ["pear", "fig", "banana", "kiwi", "apple", "cherry"]
fn byName(a: int, b: int) -> int {
    let x = names[mem.readI32(a)]
    let y = names[mem.readI32(b)]
    let noise = [x + "!", y + "?", str(len(x))]
    mem.collect()
    if noise[0] < noise[1] {
        return -1
    }
    if noise[0] > noise[1] {
        return 1
    }
    return 0
}
let keep = ["this", "list", "must", "survive"]
let nbuf = mem.alloc(len(names) * 4)
i = 0
while i < len(names) {
    mem.write32(nbuf + i * 4, i)
    i += 1
}
qsort(nbuf, len(names), 4, byName)
let out: []str = []
i = 0
while i < len(names) {
    push(out, names[mem.readI32(nbuf + i * 4)])
    i += 1
}
print(out)
print(keep)
