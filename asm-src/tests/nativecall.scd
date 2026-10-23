// Calling native code at an address: mem.symbol finds one, mem.call and
// mem.callF call it.
let strlen = mem.symbol("msvcrt.dll", "strlen")
print(strlen != 0)
print(mem.call(strlen, "hello"))
let pow = mem.symbol("msvcrt.dll", "pow")
print(mem.callF(pow, 2.0, 10.0))
let abs = mem.symbol("msvcrt.dll", "labs")
print(mem.call(abs, -42))
print(mem.symbol("nope.dll", "x"))
print(mem.symbol("kernel32.dll", "NoSuchExport"))
let add = mem.symbol("kernel32.dll", "GetTickCount")
print(mem.call(add) > 0)
// Six arguments, two past the register window.
let fmt = mem.symbol("msvcrt.dll", "sprintf")
let buf = mem.alloc(64)
mem.call(fmt, buf, "%d %d %d %s", 1, 2, 3, "four")
print(mem.str(buf))
mem.free(buf)

// Function values with arguments past the four registers, some of which
// the register allocator keeps in r10.
fn six(a: int, b: int, c: int, d: int, e: int, f: int) -> str {
    return "{a} {b} {c} {d} {e} {f}"
}
let g = six
let one = 1
let k = 7
print(g(one, 2, 3, 4, k + 1, one + k))
let h = fn(a: int, b: int, c: int, d: int, e: int) -> int { return a * 10000 + b * 1000 + c * 100 + d * 10 + e }
print(h(one, 2, 3, 4, 5))
