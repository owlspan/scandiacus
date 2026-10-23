// extern fn: calling native libraries straight from Scandiacus.
//
// An extern declares a function that lives in a DLL rather than in this
// file. There is no wrapper source and no import system - the
// declaration is the whole bridge, and Windows loads each named library
// when the program starts.
//
// This one makes sound, so run it where speakers are welcome:
//
//     scandiacus run extern.scd

// A name in PascalCase lands in kernel32.dll by the compiler's rule:
// functions spelled like the Windows API are assumed to be it.
extern fn Beep(freq: int, ms: int) -> bool

// Any other name says where it lives with `from`.
extern fn MessageBeep(kind: int) -> bool from "user32.dll"

// The C runtime is reachable the same way. printf is variadic: ...
// means arguments beyond the format string pass through as written.
extern fn printf(fmt: str, ...) -> int

// Beep waits for the tone to finish, so these play in order.
print(Beep(440, 200))
print(Beep(660, 200))

// One of the standard Windows sounds - kind 0 is the default beep.
print(MessageBeep(0))

// str crosses as a pointer to its bytes, int as a 64-bit word, so the
// C side sees ordinary char* and long long arguments.
let n = printf("hello from C: %d + %d = %d\n", 2, 3, 2 + 3)
print(n > 0)
