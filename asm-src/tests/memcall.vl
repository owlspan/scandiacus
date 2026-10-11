// mem.symbol and mem.call into the C library every system has: a word
// argument, two doubles, and a double beside an int.
let libc = "msvcrt.dll"
let libm = "msvcrt.dll"
if os.name() == "linux" {
    libc = "libc.so.6"
    libm = "libm.so.6"
}
if os.name() == "macos" {
    libc = "/usr/lib/libSystem.B.dylib"
    libm = libc
}
let strlen = mem.symbol(libc, "strlen")
print(strlen != 0)
print(mem.call(strlen, "hello"))
print(mem.callF(mem.symbol(libm, "pow"), 2.0, 10.0))
print(mem.callF(mem.symbol(libm, "ldexp"), 0.75, 4))
print(mem.call(mem.symbol(libc, "abs"), -7) & 0xFFFFFFFF)
print(mem.symbol(libc, "no_such_function_here"))
