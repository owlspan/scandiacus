// mem.scan finds a byte pattern with ?? wildcards; mem.protect changes
// what a range of pages may be used for.

let b = mem.alloc(64)
let i = 0
while i < 64 {
    mem.write8(b + i, i * 3)
    i += 1
}
print(mem.scan(b, 64, "0F 12 15") - b)
print(mem.scan(b, 64, "0f ?? 15 ? 1B") - b)
print(mem.scan(b, 64, "0F 13"))
print(mem.scan(b, 64, "") - b)
print(mem.scan(b + 60, 4, "BA BD") - b)
print(mem.scan(b + 60, 4, "BD C0"))

// Make a page read-only, then writable again. Protection is per page, so
// the page has to be one this program owns outright: a small block from
// mem.alloc shares its page with the C runtime's own heap data, and a
// read-only page under that made print fault, now and then, depending on
// where the heap happened to put things. Three pages always hold a whole
// one.
let big = mem.alloc(3 * 4096)
let page = (big + 4095) / 4096 * 4096
print(mem.protect(page, 4096, "r"))
print(mem.protect(page, 4096, "rw"))
mem.write8(page, 7)
print(mem.readU8(page))
print(mem.protect(page, 4096, "nonsense"))
