// Calling C library functions directly, on Linux and macOS. On Windows
// these libraries have other names, so this program is skipped there.
extern fn sqrt(x: float) -> float from "libm.so"
extern fn pow(x: float, y: float) -> float from "libm.so"
extern fn abs(n: i32) -> i32 from "libc.so.6"
extern fn strlen(s: str) -> i64 from "libc.so.6"
print("{sqrt(16.0)}")
print("{pow(2.0, 10.0)}")
print("{abs(-5)}")
print("{strlen("hello")}")
