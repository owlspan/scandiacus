# Changelog

Each release on GitHub carries the installer and the section below for
its version.

## 0.37.0

Smaller and faster executables. Nothing a program means has changed.

**Size.** hello, world is 3.5 KB, down from 11 KB, and a program using
JSON 87 KB instead of 610 KB. Most of what a program carried was the
same code written out wherever it was used - the allocator at every
allocation, a map's search at every read, a JSON parser at every
`json.get`, a type's printer at every `print` - and each of those is
now one function called from every site. A program that never makes
an object for the collector no longer carries the collector.

**Maps are hash tables.** Lookups and inserts are O(1); 300,000 inserts
took 34 seconds and now take a quarter of one. Printing, iterating,
`keys` and `values` still go in sorted key order.

**Generated code.** Operations happen in registers with constants as
immediates instead of going through rax and rcx every time; division
by a constant is a multiply; floats live in xmm registers; `xs[i]`,
`p[i]` and `x += y` are single instructions in a loop. Against gcc -O2,
integer loops and recursion now run within a few percent to 25%, and
float, list and pointer loops within 1.6x.

## 0.36.0

**Typed pointers.** `*T` is an address with a known thing at it: a C
scalar such as `i32` or `f32`, an extern struct, or another pointer.
`*p` reads and writes at T's width, `p[i]` and `p + n` count in
elements, `p - q` is how many elements apart two are, and `p.hp`
reads through a `*Player` the way C's `p->hp` does. `raw as *i32` makes
one from an address, `nil` is the null pointer, and `&` takes the
address of memory - a field, `*p` or `p[i]`. Extern struct fields can
be pointers, including to their own kind, and extern functions and
callbacks take and return them.

An array field of an extern struct, `name: [16]u8`, now reads as a
pointer to its first element rather than a bare int, so `p.name[3]`
is the fourth byte. It still goes anywhere an address did; `p.name + 1`
now counts in elements, which only differs for elements wider than a
byte.

## 0.35.0

**Looping over a channel.** `for v in ch` takes every value a channel
carries, waiting for each, and ends once the channel is closed and
drained - the loop a worker or a consumer is written as. Inside it `v`
is a plain value, not a nullable.

## 0.34.0

**Runtime errors say where.** An index out of range or a failed `must`
prints the same first line as before, then the file, line and function:
`at game.vl:42 in update`. Each place that can fail knows its statement
when it is compiled and hands the location over only when it fails, so
a program that runs normally pays nothing for it.

**Crashes are reported.** An access violation - a bad address given to
`mem.*`, native code writing where it should not - used to end the
program silently. An exception handler installed as `main` starts now
flushes what the program printed, says what kind of crash it was and
which function it happened in, or that it was in native code, and ends
the program as before.

## 0.33.0

**Default parameter values.** `fn greet(name: str, greeting: str =
"hello")` can be called as `greet("ada")`. A default is a constant - a
number, a string, `true`, `false`, `nil` or an enum's variant - and
every parameter after one with a default has one too. Methods take
them as well. A call that leaves arguments off gets a copy of each
default written into it, so nothing past the checker changed.

**Naming a generic method's types.** `bag.empty<str>()` or
`b.map<str>(f)`, for when the arguments cannot say.

## 0.32.0

**defer.** `defer stmt` runs stmt when its block is left - at its end,
on a `return`, or on a `break` or `continue` - latest first. It belongs
to the block, as in Zig and Swift, so `defer delete(xs)` in a `gc off`
loop frees each trip's list on that trip, and `defer thread.unlock(m)`
releases a lock on every way out of a function.

**Builder.** A string put together from pieces in linear time, where
`s = s + piece` in a loop is quadratic: `add`, `addLine`, `str`, `len`
and `clear`.

**Generic methods.** A method can have type parameters of its own,
`fn map<U>(self, f: fn(T) -> U) -> Box<U>`, on a generic struct or a
plain one, taken from the call's arguments.

**Also.** An error reported twice at the same place, as a call used as
a receiver could be, is reported once.

## 0.31.0

**Threads.** `thread.spawn(f)` runs a function - a closure, with what
it captured - on a new thread, and `thread.join` waits for it. With
them: `thread.mutex`, `lock` and `unlock`; `thread.cond`, `wait`,
`notify` and `notifyAll`; and `atomic.new`, `add`, `get`, `set`,
`swap` and `cas`, each a single locked instruction.

**Channels.** `channel<T>()` makes a `Channel<T>` that carries values
between threads in order: `send`, `recv` (a `?T`, `nil` once closed and
drained), `close` and `pending`. Copies of one are the same channel.

The collector scans one stack, so it waits while any thread besides
main is alive. Allocation takes a lock only then, so a program without
threads pays nothing for it - and two `task.map` threads allocating at
the same moment, which could corrupt the heap before, cannot now.

**Faster code.** The busiest integer locals live in callee-saved
registers for their whole function, the collection check at each
statement is dropped wherever nothing can have allocated since the last
one, and a compare feeding a branch becomes one compare and one jump.
A nested arithmetic loop runs about 45% faster.

**Also.** After `if x == nil { return }`, a nullable `x` is known not to
be nil for the rest of the block. A method can be called on a variable
a closure captured.

## 0.30.0

**Memory by hand, when you want it.** A program is still garbage
collected by default. One that starts with

    gc off

is never collected, and frees what it is done with itself:

    let xs = [1, 2, 3]
    ...
    delete(xs)

`delete` frees a list, map, struct, string or `bytes` at once, with no
collector running and no pauses, the way C++ does. It frees the value,
not what the value refers to, and nothing checks for a use after
delete. Strings made only in passing - the pieces of `a + str(i)`, the
parts of an interpolation, a string built only to be printed - are
freed as they are used, and a list that outgrows its space frees the
old one, so a loop of made-and-deleted values runs in constant memory.

## 0.29.0

**Interfaces.** An interface lists methods, and any struct that has
them, with the same types, is one - nothing on the struct says so:

    interface Shape {
        fn area(self) -> float
        fn name(self) -> str
    }

    let shapes: []Shape = [Square{side: 2.0}, Circle{r: 1.0}]
    for s in shapes {
        print("{s.name()}: {s.area()}")
    }

A struct becomes the interface wherever one is wanted: an argument, a
typed `let`, a list or map element, a return, `push` or `==`. A struct
missing a method is an error naming the method, or the types that
differ. A method that changes `self` changes the value the interface
holds, and a `match` gets the struct back: `Shape.Circle(c) => ...`.

Since the whole program is compiled at once, an interface is kept as a
tagged value over the structs used as it, and a call is a branch on
the tag - no vtable, and nothing allocated beyond the value.

## 0.28.0

**Enums that carry values.** A variant lists what it holds, and a
`match` names it:

    enum Shape {
        Circle(r: float)
        Rect(w: float, h: float)
        Empty
    }

    match s {
        Shape.Circle(r) => print(3.14159 * r * r)
        Shape.Rect(w, h) => print(w * h)
        Shape.Empty => print(0)
    }

A match without an `else` has to handle every variant, `_` skips a
value, and a variant can hold the enum itself, so a tree is an enum.
Generic enums work too: `enum Option<T> { Some(v: T), None }`, with the
type taken from the value given, `Option.Some(3)`, or from where it is
going, `return Option.None`. Values print as their variant,
`Circle(2)`, and compare equal when they are the same variant holding
the same values.

**Calling an address.** `mem.symbol(dll, name)` finds an export, and
`mem.call(p, args...)` calls native code at any address, with
`mem.callF` for one returning a `double`: something found by
`mem.scan`, read from a vtable or handed over by a host.

**Fixes.** Printing or comparing a type that holds itself, such as
`struct Node { kids: []Node }`, sent the compiler into a loop until it
ran out of memory; such types now print and compare through a function
of their own. A call through a function value with more than four
arguments could pass the wrong value in the third, when the register
allocator had kept that value in r10.

## 0.27.0

**Generics.** Functions, structs and their methods take type
parameters:

    fn largest<T>(xs: []T) -> T { ... }
    struct Stack<T> { items: []T }
    impl Stack<T> { fn push(self, x: T) { ... } }

The types come from a call's arguments, or are named where they cannot
be, as in `mapList<int, str>(none, f)` or `Stack<str>{}`. Any type can
be one: numbers, strings, lists, maps, functions, nullables, structs,
enums and other generics, `Stack<Pair<str, int>>` included.

Each set of types a generic is used with is its own copy, compiled as
if written out by hand, which is how C++ templates work: no boxing and
no cost at run time. A generic is checked per use, and an error inside
one names the instance, as in `(in largest<Point>)`. A method cannot
yet have type parameters of its own.

**Fixes.** `print` and `write` of a nullable showed the address of its
box; they show the value or `nil`. A method called on another method's
result, `a.me().get()`, compiles on the assembly backend.

## 0.26.0

**PNG images, with transparency.** `win.image` loads `.png` as well as
`.bmp`, and `win.draw` blends a PNG by each pixel's alpha, so a sprite
with a transparent background draws the way it looks in an editor.
`win.drawScaled` blends too.

The decoder is written in Veyl, in the prelude: DEFLATE, the PNG
chunks and the five row filters. Every colour type is supported at 8
bits, and palette images at 1, 2, 4 and 8 bits with a tRNS chunk.
Interlaced and 16-bit images are refused with a message. Fixtures for
each case are in `tests/png`, checked against the exact pixels.

An image handle is now a block holding the bitmap, its size and
whether it has alpha, outside the collector like a window.

## 0.25.0

**Images.** `win.image` loads a `.bmp`; `win.draw`, `win.drawScaled`
and `win.drawKeyed` put it in the window - at its own size, stretched,
or with one colour left out so a sprite has a transparent background.

**Measuring text.** `win.textWidth` and `win.textHeight`, so a label
can be centred instead of guessed at.

**Canvases.** `win.canvas(width, height)` is a back buffer with no
window, and `win.pixel` reads a colour back from it or from a window.
Every drawing function works on one.

**Sound.** `sound.play`, `sound.loop` and `sound.stop` play a WAV in
the background.

`examples/gui/sprites.vl` uses all of it.

Fixed: a window's state lived on the collected heap behind an `int`
handle, so since automatic collection a window kept in a struct or a
list could have been freed while still open.

## 0.24.0

**Global variables.** A top-level `var` is visible inside every
function and can be changed from any of them:

```veyl
var score = 0
fn award(points: int) { score += points }
```

A function reaching for a top-level `let` now gets an error that says
so and suggests `var`, instead of "undefined variable".

**Enums.**

```veyl
enum State { Idle, Running, Done }
var state = State.Idle
print(state)                // Idle
```

Values compare with `==`, print by name, and cannot be mixed with ints
or other enums. A `match` on an enum without an `else` has to handle
every value, and names the ones it misses.

Fixed:

- `pub` was never enforced on this backend: private functions,
  structs and constants of an imported file could be used freely.
- Every type error in an imported file was reported against the main
  file's name. It names the file it is in now.
- Assigning to a `const` reported "undefined variable"; it says the
  name was declared const.

## 0.23.0

**Automatic garbage collection.** The collector runs by itself at the
start of a statement once the live heap passes 4 MB, and after that
once it doubles what survived the last collection. A loop that makes
garbage forever now runs in bounded memory. It waits while `task`
threads run, since it only scans its own stack, and it stays off in a
DLL. `VEYL_GC=off` turns it off; `VEYL_GC=eager` collects at nearly
every statement, and CI runs the whole suite that way.

**Maps are fast.** Lookups are a binary search instead of a scan, and
inserts and removals move entries with one `memmove`. 100,000 inserts
and lookups: 35 s before, 1.5 s now. Integer keys at opposite ends of
the range compared by subtraction, which overflowed and sorted them
wrong; they compare directly now.

**`join` is linear.** It measured the result, allocated once and copied
each piece in, instead of concatenating: 200,000 pieces went from 121 s
to under one. Every byte copy - substrings, file reads, `bytes` - is a
`memmove` instead of a loop.

`mem.goroutines()` counts running task batches instead of always
answering 1.

## 0.22.0

**Callbacks.** A function type on an `extern fn` parameter takes a Veyl
function that native code calls back:

```veyl
extern fn EnumWindows(each: fn(ptr, ptr) -> bool, data: ptr) -> bool from "user32"
fn onWindow(hwnd: int, data: int) -> bool { return true }
EnumWindows(onWindow, 0)
```

Native code is handed a stub that sign-extends the arguments the
callback type declares as C `int`, normalises each `bool`, and jumps to
the function, which then returns straight to its caller. A callback
may allocate and run the collector.

**DLLs.** `veyl build --dll mod.vl` writes `mod.dll`. The top level
runs once, on load; every `export fn` is in the export table. The image
is relocatable with no fix-ups, since nothing in it holds an absolute
address, so a DLL loads wherever Windows puts it.

**`mem.protect`** changes what pages may be used for, and **`mem.scan`**
finds a byte pattern such as `"48 8B ?? ?? 89"` - the two things a mod
needs to find and patch code.

**Strings order**: `<`, `>`, `<=` and `>=` on two strings compare byte
by byte, as on the Go backend. The checker accepted them and the
backend refused them.

`examples/ffi/plugin.vl` is a DLL to start from.

## 0.21.0

**Raw memory.** `mem.alloc`, `mem.resize` and `mem.free` give blocks
outside the collector. `mem.readU8` through `mem.readI64`,
`mem.readF32` and `mem.readF64` read at every width, signed or not;
`mem.write8` through `mem.write64`, `mem.writeF32` and `mem.writeF64`
write. `mem.copy`, `mem.fill`, `mem.str`, `mem.strN`, `mem.bytes` and
`mem.addr` round it out. An address is an `int`, so pointer arithmetic
is ordinary arithmetic.

**`extern struct`**, a C layout over an address:

```veyl
extern struct Player {
    hp: i32
    pos: Vec3
    name: [16]u8
    ammo: u16 at 0x40
}
let p = Player(address)
p.hp -= 10
```

Fields are laid out the way a C compiler would, or pinned with `at`.
They read and write at their own width, nest, and print. An extern
struct can be passed to and returned from an `extern fn`, and a
literal allocates zeroed memory for filling in a Win32 structure.

**`bytes` crosses into `extern fn`** as a pointer to its data, for
functions that fill in a buffer.

**`examples/ffi/memreader.vl`**: lists running programs, finds where
one is loaded, and follows a pointer chain through its memory.

Under the hood: width-specific loads and stores in the IR, and the
byte encoder learned `movsx`, word operands, and the single-precision
conversions, each checked against GNU as.

## 0.20.0

**Reading input.** `input()`, `input(prompt)` and `pause()`. A line
comes back without its ending, and the end of input reads as `""`.

```veyl
let name = input("Your name: ")
print("hi, {name}")
```

**Text builtins** the Go backend had and this one did not: `toFloat`
and `isFloat`, which follow Go's `ParseFloat` including underscores,
`inf` and `nan`; `count`; and `padLeft` and `padRight`, with an
optional fill.

**Characters, not bytes.** `charAt`, `substr` and `chars` count
characters, as they always did on the Go backend. They counted bytes
here, so anything outside ASCII came out cut in half.

**`\xHH` and `\u{...}` string escapes**, so any byte or character can
be written in plain ASCII source: `"caf\u{e9}"`.

**`INF` and `NAN`.** Float comparisons are NaN-correct now - every
comparison with a NaN is false except `!=` - and infinities and NaN
print as `+Inf`, `-Inf` and `NaN` rather than msvcrt's `1.#INF`.

Fixed:

- `floor`, `ceil`, `round` and `trunc` returned 0 for most arguments
  since 0.18.1: load forwarding turned a float bitcast into a plain
  copy, and once the register allocator put the int in a register the
  float return read the wrong register file. They also return `int`
  now, as the docs and the Go backend always said.
- `isNan` was false for a NaN.
- Compile errors whose message contained a colon were sorted into the
  wrong order.
- A float constant equal to another under `==` shared its storage, so
  `-0.0` could become `0.0`.

Tests:

- `tests/` holds programs the Go backend cannot run - the new escapes,
  and programs fed standard input - with the output they must print.

## 0.19.0

**`extern fn`: native functions declared in Veyl source.**

```veyl
extern fn Beep(freq: int, ms: int) -> bool
extern fn mz_extract(zip: str, dest: str) -> int from "miniz"
```

The declaration compiles to a real import-table entry and an x64 call.
There is no wrapper to write and nothing to install, which turns a
package into a plain `.vl` file of declarations. See
[Native functions](docs/SYNTAX.md#native-functions-extern).

Fixed:

- `veyl run app.vl --verbose` now passes `--verbose` to the program.
  The usage text promised it; `os.args()` came back empty.
- The installer now ships `examples/mod`, which `examples/imports.vl`
  needs, and `examples/ffi`.
- Every version string agrees. The installer and the VS Code extension
  said 0.18.1 and the extension's install instructions said 0.10.0.
  A test now fails when they drift.
- The docs no longer describe the Go backend as the compiler, or
  mention `veyl fmt`, `veyl emit`, `veyl builtins` and warnings, none
  of which this compiler has.

Tooling:

- `veyl run` starts the program through `wine` on a non-Windows host,
  so the whole test suite runs on Linux.
- CI runs every test on Windows against the reference backend, and a
  version tag builds the installer and publishes the release.

## 0.18.1

Optimisation passes: constant and branch folding, dead code
elimination, redundant load elimination, slot packing, an assembly
peephole, merging `.rdata` into `.text`, and a register allocator for
short-lived integer values. Executables average 36% smaller, and a
straight-line arithmetic loop runs 2.7x faster.

## 0.18.0

The first release that compiles straight to x86-64 with no Go, no
assembler and no linker. Windows you can draw in, a package manager,
SQLite, HTTPS, a web server, sockets, and editor highlighting.
