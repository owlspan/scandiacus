# veyl - the Veyl compiler

Veyl compiled to x86-64, with no Go anywhere.

It used to work the other way. The old compiler translated Veyl to Go
source and handed that to the Go toolchain. It produced genuinely
native executables and it was the only thing that did for a long time.
What it cost was a runtime, a 2.4 MB floor on binary size, a 177 MB Go
toolchain inside the installer, and any hope of pointers or manual
memory.

This is how those got bought back. The old one is on the
[`veylgo`](../../../tree/veylgo) branch, discontinued. This one is
Veyl now.

```
hello.vl  ->  [veyl]  ->  hello.exe
```

Nothing in that arrow is another program. veyl encodes the
instructions, links, and writes the PE itself, so a build needs nothing
installed.

It also builds for Linux:

```
hello.vl  ->  [veyl --linux]  ->  hello
```

The Linux executable runs the same instructions as the Windows one. The
generated code is left exactly as it is; every library call it makes is
answered by a small runtime (`compiler/linuxrt/veylrt.c`) that does the
same job on POSIX under the Windows calling convention. On Linux that is
the default, and console programs - files, strings, maps, threads, the
crash handler - behave the same on both. The libraries that only exist
on Windows (windows and drawing, sound, COM, sockets, HTTP, sqlite,
`proc`) are reported by name when a program uses them. For now the
Linux build uses the system's `as` and `cc` to assemble and link; a
built-in ELF writer, like the PE one, is the next step.

## Status

Every one of the 24 programs in the Go backend's own test suite
compiles here, and every one prints the same bytes. So did every
program in `examples/` when the Go backend was retired; that output is
now frozen in the `.out` file beside each one. This compiler has since
gone past the Go backend and no longer has to match it.

Compiled all the way to a running `.exe` with no Go anywhere in the
pipeline:

- `int`, `float`, `bool`, `str`, and lists of any of them
- `let`, `const`, plain and compound assignment, block scoping with
  shadowing
- all arithmetic on ints and floats, comparisons, `&&`, `||`, `!`, and
  the bitwise operators including signed shifts
- `if` / `else`, `while`, `for` over a range with `step`, `for x in xs`,
  `break`, `continue`, `match`, nested to any depth
- functions with any number of arguments, recursion, order-independent
  declaration, and the full Windows x64 calling convention including
  mixed int/float argument lists
- string concatenation, comparison, and full `"{interpolation}"`
- lists: literals, indexing, index assignment, `push`, `len`,
  iteration, and bounds checks printing the Go backend's exact sentence
- `print`, `write`, `str`, `len`, `push`, `abs`, `min`, `max`, `int`,
  `float`, `divf`, `sqrt`, `mod`, `upper`, `lower`, `substr`, `charAt`,
  `indexOf`, `contains`, `startsWith`, `endsWith`, `repeat`
- the constants `PI` and `E`
- namespaced calls at any depth: a dotted name is looked up like any
  other, so a namespace is a naming convention rather than a scope
- maps `{K: V}` with int or str keys: literals, get, set, `len`,
  growth, a missing key reading the zero value, `keys`, `values`,
  `has`, `remove`, `clear`, and `for k, v in m`
- containers inside containers, to any depth: `[][]int`,
  `{str: []int}`, a list of maps, a struct holding a list
- the error type `T!`: `fail`, `ok`, `isOk`, `failed`, `errorOf`,
  `valueOr`, `must`, postfix `?` propagation, and `void!`
- structs: declarations, literals with zero values for omitted fields,
  nested structs, field read and write, compound assignment on a field,
  structs in lists and maps, value semantics, and printing
- `impl` methods, with the receiver passed by reference so a method that
  changes a field changes the value it was called on
- nullable `?T`: `nil`, narrowing through `if x != nil`, in a struct
  field, in a list, and `find(m, k)`
- deep equality: `==` on a list, a map, a struct or a nullable compares
  contents, generated from the static type
- `str()` of any of those - the same renderer that prints one, with its
  output pointed at a buffer, so the two cannot disagree
- the list library: `first`, `last`, `pop`, `sum`, `reverse`, `sort`,
  `slice`, `join`, `insert`, `removeAt`, `contains`, `indexOf`, and
  `split`, `chars`, `lines` turning a string into a list
- multi-file programs: `import "other.vl"`, with `pub`, and top-level
  consts as real globals in static storage
- the `os` library: `file.read`, `write`, `append`, `lines`, `exists`,
  `size`, `delete`, `rename`, `readOr`; `dir.is`, `list`, `make`,
  `delete`; `path.join`, `dir`, `base`, `ext`; `env.get`, `has`, `set`;
  `run`, `pid`, `cpus`, `hostname`; and `time.now`

- closures and first-class functions, captured by reference, and the
  higher-order builtins `map`, `filter`, `reduce`, `sortBy`, `any`,
  `all`, `each`
- structured concurrency: `task.map`, `task.mapLimit`, `task.each` and
  `task.all`, on real Windows threads, results in list order
- the `bytes` type, and `hash`: md5, sha1, sha256, sha512, crc32, hex
  and base64 both ways, and `hash.file`
- `re`: a backtracking engine written in Veyl, matching Go's
  leftmost-first semantics
- `json` encode and decode, and `csv` parse, write, read and save
- the math, time, bits, args, url, stats, term and rand libraries, all
  written in Veyl in the prelude
- a mark-and-sweep collector, conservative over roots

- `net` TCP sockets and an `http` server and client, through WinSock
- `win`: a window with a game loop, drawing, keyboard and mouse,
  immediate-mode widgets, `.png` images with alpha and `.bmp` with a
  colour key, a PNG decoder written in Veyl, text
  measurement, and off-screen canvases; `sound` plays a WAV
- `extern fn`: declare a function that lives in a DLL and call it -
  the Windows API, the C runtime, or any library a package ships
- raw memory: `mem.alloc` and `mem.free`, reads and writes at every
  width from `u8` to `f64`, `mem.protect` and `mem.scan`, and
  `extern struct`, a C layout laid over an address, with natural
  alignment or offsets given by hand
- callbacks: a Veyl function handed to native code as a C function
  pointer
- `veyl build --dll`: a DLL whose top level runs on load, exporting
  every `export fn`
- `var` globals every function can change, and enums, with a `match`
  on one checked for every variant
- generics: `fn largest<T>(xs: []T) -> T`, `struct Stack<T>` and
  `impl Stack<T>`, each use compiled to its own copy as in C++
- enums whose variants carry values, `Circle(r: float)`, taken apart
  by a `match`; recursive ones for trees, generic ones like `Option<T>`
- `mem.call` and `mem.symbol`: call native code at any address
- threads: `thread.spawn`, mutexes, condition variables, atomics and
  `Channel<T>`
- `defer`, scoped to the block, and `Builder` for linear string
  building
- default parameter values: `fn greet(name: str, greeting: str = "hi")`
- runtime errors name the file, line and function they happened in,
  and a crash names its kind and function
- interfaces: `interface Shape { fn area(self) -> float }`, satisfied
  by any struct with the methods, as in Go
- garbage collected by default, or `gc off` at the top of a file to
  free memory by hand with `delete`, as in C++
- fixed-width numbers, `i8` to `u64` and `f32`, that wrap and round
  exactly as C's do, with `i32(x)`-style conversions
- `new T` and `new [n]T` for C-layout memory from the C heap, freed
  with `delete`, and methods called through a pointer
- `&x` on a local variable, for native out-parameters, with the
  compiler refusing an address that would outlive its function
- `proc`: read and write another process's memory - open, read, write,
  typed reads and writes, and a pattern scan - and list processes,
  their modules, threads and memory regions
- `com`: call COM interfaces through their method tables, which is how
  DirectX, WMI and the shell are reached, with GUIDs, `CoCreateInstance`
  and wide strings

- `input`, `pause`, `toFloat`, `isFloat`, `count`, `padLeft` and
  `padRight`, and the constants `INF` and `NAN`, with comparisons that
  treat a NaN the way Go does

Missing against the Go backend: `zip`. There is also no resolver on
this side, so a name that is neither a local, a function nor a builtin
is caught by the lowerer rather than the checker. Everything absent is
a compile error naming it, never wrong output.

What the Go backend does not have: `extern fn` and callbacks, raw
memory and `extern struct`, DLLs, `var` globals, enums, generics, interfaces, and the `\xHH`
and `\u{...}` string escapes. The differential suite
compares programs both backends can run; the extern demos sit in
`examples/ffi/`, and programs that need the escapes or feed standard
input sit in `tests/` with the output they must print beside them.

Byte-identical means error messages too, which is why the `os` library
is written against Win32 rather than the C runtime: Go's message for a
missing file is FormatMessage's sentence, and strerror's is a
different one.

```
$ veyl run examples/floats.vl
5.5
-2.5
6
0.375
```

`examples/collatz.vl` is the benchmark - nested loops, 10,000 iterations
of real integer work - through both backends:

The two assembly columns are the same compiler. Only the linking
differs: the middle one goes through gcc, the last is the PE this
compiler writes itself.

| | via Go | asm, via gcc | asm, self-linked |
| --- | ---: | ---: | ---: |
| runtime, best of 7 | 33 ms | 36 ms | 38 ms |
| executable size | 2,524,160 | 123,102 | 2,048 |

**Within about 15% of Go on this little program, and over a thousand
times smaller.** The 123 KB column is mostly MinGW's C runtime rather
than anything this compiler produced, which is what the last column
removes.

collatz is too small to say much about code quality - at forty
milliseconds a run, most of it is process startup - so the honest scale
numbers come from a bigger loop: fifteen million iterations of
straight-line integer arithmetic run 1.12 s compiled by the compiler as
it was before any of the passes landed, 0.60 s with folding,
dead-code elimination, load forwarding and slot packing alone, and
0.42 s with the register allocator on top. Across every example
program, executables average 36% smaller than before the passes.

## What it does not have

**Collection is statement-grained and single-threaded.** The collector
runs by itself, at the start of a statement, once the live heap passes
a threshold: 4 MB, then twice what survived the last collection. A
statement boundary is safe because every pointer is in a stack slot or
a global there - the register allocator never takes one - and the test
suite runs with `VEYL_GC=eager`, which collects at nearly every
statement, to hold it to that. It does not run while `task` threads are
working, since it scans only its own stack, and it does not run in a
DLL, whose exports the host may call on threads of its own.
`VEYL_GC=off` turns it off.

**No resolver.** A name that is neither a local, a function nor a
builtin is caught by the lowerer rather than the checker, so it is
missed when the checker has already failed on something else. Sharing
`resolve.go` the way `check.go` is now shared is the obvious fix.

**A string is NUL-terminated bytes**, with no length beside it. So a
file holding a zero byte reads back short where the Go backend reads it
whole, which is what the `bytes` type is for, and building a string by
repeated appending is quadratic - push the pieces and `join` them.

**Printing a float rounds through msvcrt**, which stops at seventeen
significant digits, so a value needing sixteen can round the wrong way.
The fix is Go's `strconv/decimal.go` in the prelude.

## Why assembly text and not machine code

Assembly and machine code are one to one. Every decision that is hard -
instruction selection, register allocation, the calling convention,
stack frame layout - is identical either way. Everything that assembly
text defers is mechanical: byte encoding, jump offset backpatching,
COFF object layout, relocations, PE headers, the import table.

So this did the thinking first and the typing later. The encoder, the
linker and the PE writer came afterwards and nothing above `x64.go`
changed when they did. That is the point of the split, and it is why
`x64.go` is forbidden from using assembler conveniences: no macros, no
pseudo-instructions, nothing the assembler has to evaluate. Every line
must be one instruction whose bytes we could write ourselves, and
`encode_test.go` checks every one of them against GNU `as`.

## Layout

```
frontend/           lexer, parser, AST, types and the type checker,
                    shared with the Go backend on the veylgo branch
asm-src/
  go.mod            requires ../frontend
  compiler/
    veyl.go         the driver and the command line
    frontend.go     type aliases onto the shared front end
    library.go      asmLibrary - the builtin table, for the checker
    ir.go           AST -> three-address IR over virtual registers
    opt.go          folding, dead code, load forwarding, slot packing
    regalloc.go     the register allocator
    x64.go          IR  -> x86-64, GNU as syntax with Intel operands
    peephole.go     the assembly peephole
    textlib.go      the primitives under input and the text builtins
    memlib.go       raw memory: alloc, free, reads and writes by width
    views.go        extern structs, C layouts at an address
    fixed.go        i8 to u64 and f32: wrapping, unsigned ops, rounding
    cstruct.go      new and delete, and variables kept in memory for &x
    winmedia.go     images, canvases, text measurement and sound
    prelude_png.go  the PNG decoder and DEFLATE, in Veyl
    callback.go     Veyl functions called from native code
    dll.go          DLL output: the entry point and the export table
    encode*.go      x86-64 text -> machine code
    link.go, pe.go  the linker and the PE writer
    gc*.go          the collector
    prelude*.go     library code written in Veyl, compiled with the program
    *.go            one file per built-in library: list, map, os, net,
                    http, json, task, db, win, and the rest
  examples/         top-level ones are tests, with their output beside them
  tests/            test programs, with their output
  installer/        the Inno Setup script and its build script
  scripts/          make-installer.bat and .sh, saferun.ps1, metrics.ps1
docs/               SYNTAX.md and TUTORIAL.md
editors/            VS Code, Sublime, Vim and Notepad++ highlighting
```

The lexer, parser, AST and types live in `../frontend` and are shared
with the Go backend, so both compile the same language from one
definition. `frontend.go` is nothing but Go type aliases, which is what
lets the rest of this package write `Expr` and `PLUS` unqualified.

`ir.go` is the boundary. Nothing in it knows an x86 register exists.

**The type checker is shared too.** What
unblocked it was the builtin table: the two backends do not have the
same set of builtins, so the checker could not assume either one.
`frontend/library.go` is the interface it asks instead, and
`library.go` here implements it as `asmLibrary`. A builtin this backend
lacks comes back as a clean "not on the assembly backend yet" error
rather than a checker crash.

So a wrong program is now caught here, with the same message the Go
backend would give. The remaining gap is the **resolver**, described
above, which only the Go backend has.

## Commands

```
veyl f.vl          compile and run
veyl run   f.vl    the same thing, spelled out
veyl build f.vl    write an executable next to the source
veyl asm   f.vl    print the generated assembly
veyl ir    f.vl    print the intermediate representation
```

`asm` and `ir` matter more here than `veyl emit` does on the Go
backend. When a register allocator produces something wrong, reading
the instruction stream is the only way to see it.

## Testing

```
cd asm-src
go test ./...
```

Every program in `tests/` and the top level of `examples/` is run and
its output compared byte for byte with the `.out` file beside it. On
Linux the programs are built as Linux executables and run directly,
against the same `.out` files; the few that need a Windows-only library
are skipped. `VEYL_TARGET=windows` runs the Windows build instead,
through `wine`.

Until the Go backend was retired, the examples were compared against it
directly, and their `.out` files are its output at that point. That
comparison earned its place on the first program ever compiled. The
numbers were correct and the line endings were not: the C runtime
translates `\n` to `\r\n` on stdout and Go does not. In a terminal the
two were indistinguishable. In bytes they were not. `x64.go` now puts
stdout in binary mode in the prologue.

The second test checks that everything outside the subset is a compile
error rather than wrong output. A backend that quietly mis-compiles what
it does not understand is worse than one that refuses.

## Requirements

To build a program: nothing on Windows. veyl encodes, links and writes
the PE itself. On Linux: `as`, `objcopy` and a C compiler, which any
machine that builds software has (`apt install build-essential`).

To run the tests: Go, and MinGW's `as` and `gcc` (on Linux, `x86_64-w64-mingw32-as`
and `wine`). `encode_test.go` checks every byte
this compiler emits against GNU `as`, and `VEYL_LINK=mingw` takes the
old route through `gcc` so a program that runs one way and not the
other localises the bug to the half that changed. Both are found on
`PATH`, at the usual MSYS2 and MinGW locations, or via `VEYL_MINGW`.
Without them those two checks skip and everything else runs.

## Installing

```
scripts\make-installer.bat
```

Double-click it. It builds `asm-src\dist\veyl-<version>-setup.exe`,
which is about 5 MB because there is no toolchain to bundle. The Go
backend's installer is roughly 90, most of it a trimmed copy of Go. It
needs Go and Inno Setup (`winget install JRSoftware.InnoSetup`).
`scripts/make-installer.sh` does the same on Linux through wine, with
Inno Setup installed into the wine prefix.

Every release on GitHub carries the same installer, built by
`.github/workflows/release.yml` on a Windows runner. A push to `veyl`
that bumps the version releases it, tag included; so does pushing a
`v*` tag by hand. The version lives in four places - `Version` in
`asm-src/compiler/veyl.go`, `AppVersion` and `ExtVersion` in
`installer/veyl.iss`, and `editors/vscode/package.json` - and
`version_test.go` fails when they disagree.

## How it got here

The order the backend was built in, with the reasoning rather than just
the list. Everything here is done; what is left is under
[What comes next](#what-comes-next).

**1. The object header. Done.** Every heap allocation carries one word
in front of it: `size << 8 | tag`, where the tag says whether the block
is raw bytes, word slots holding no pointers, word slots that are all
pointers, or a list header. `allocObj` in `list.go`.

It buys nothing today. A collector has to tell a pointer from an
integer at runtime, and every value here is an anonymous eight bytes,
so nothing in the heap could say. The tag says it for a whole block at
once. Done before structs and closures multiplied the number of places
that allocate, because retrofitting it across all of them costs far
more than adding it to four sites.

One thing it does not cover: **string literals are static rather than
heap-allocated, so they have no header.** A collector will meet
pointers that do not point into the heap at all. That is a constraint
to design around, not an oversight.

**2. Namespaced calls. Done.** The lowerer required a plain identifier
as the callee, so nothing under `os.`, `time.`, `mem.` and the rest
could be called at all. It now flattens the callee with `DottedName`
and looks that string up like any other builtin, at any depth:
`os.file.write` arrives as one name. A namespace is a naming
convention, not a scope, exactly as on the Go backend.

Adding another is two edits - a signature in `library.go` and a case in
`builtin` in `ir.go` - but it is not the Go backend's one-liner. There
`emit` returns a line of Go and the standard library does the work;
here you write IR and usually a libc call. A function absent from
`library.go` is a type error naming it, so the subset stays honest
without anyone maintaining a list of what is missing.

The one that had to come before writing many of them was the **error
type `T!`**, which is now done - see below. Every library function in
Veyl reports failure as `T!` rather than panicking, so without results
here they could only have been ported in a lying form.

**3. Maps. Done.** Keys and values in two parallel arrays, found
through an open-addressed hash table, so a lookup and an insert are
O(1). They started as one sorted array, which printed and iterated in
key order for free but moved half the map on every insert; the order
the language promises is now restored by a sort only when something
walks the map and the entries have fallen out of it. See `maphash.go`.

It did not move parity, which is worth knowing: all seven programs that
reported a map as their first error had something else behind it.

**4. The error type `T!`. Done.** A result is a two-word heap object:
the failure reason, then the value. The layout does not depend on what
it carries, which is what lets `?` propagate a failure out of a `str!`
and into an `int!` by handing back the object it was given rather than
unpacking and rebuilding one - a failure crosses any number of frames
as a single pointer.

Boxing rather than returning a pair is what keeps the IR
three-address and the calling convention unchanged. The Go backend
returns a two-field struct by value; doing that here would have meant
classifying an aggregate return, which is the corner of the Windows x64
ABI with the most rules and the least payoff. The cost is an allocation
per fallible call, unbounded while nothing is freed.

Boxing a plain value into the `T!` a function promises is not decided
by the lowerer. The checker already inserts a `Widen` node at every
such site, the way it does for nullables, so there is one place that
can forget rather than five.

**5. Structs. Done.** Fixed field offsets, so every access is a load or
a store at an offset known at compile time.

The header is where the interest is. A struct is the first thing here
that is genuinely mixed - some words pointers, some not - so the
block-at-a-time tags could not describe one. Fields are reordered to
put every pointer first and the count goes in the eight spare bits the
header already had, which keeps "is this word a pointer" a single
number at no per-instance cost. The reordering is invisible: fields are
reached by name through a compile-time offset.

Value semantics were the part needing care. A struct is a Go struct on
the Go backend, so it copies on assignment, on being passed and on being
returned; here it is a pointer, so the copy is written out. `rvalue`
copies only when the source is a place - a literal or a call result is
already a fresh object nobody else holds. Getting this wrong would not
have crashed, it would have been `let b = a` quietly aliasing.

`impl` methods and `str()` of a struct came afterwards.

**6. A foreign call op. Done.** `OpCall` emitted `call __vy_<Sym>` and
so could only reach functions this compiler wrote. Anything wanting a
libc or Win32 function had to become its own opcode with its own case
in `x64.go` - which meant the cost of porting the library was one
*opcode* per function, not one edit.

`Instr` now carries `Extern`, `Ret32` and `Variadic`. `Ret32`
sign-extends `eax`, which the many C functions returning an `int` need
because the top half of `rax` is undefined on such a return.
`Variadic` puts a float in both the xmm and the integer register, which
the convention requires when the callee reads a `va_list` and has no
prototype telling it which file to look in. Both are silent when wrong,
which is why they are flags rather than something to remember at each
call site. The three ops that existed only to reach C are gone.

**7. Containers inside containers. Done.** A `vty` carried its element
as a bare kind, so it could say "list of int" but not "list of list of
int". It carries a whole type now. The catch: a vty holds a pointer, so
`==` is no longer type equality, and every place meaning "the same
type" has to go through `eq()`.

**8. `str()` of a container. Done.** The printing code with its output
redirected into a buffer, so `print(xs)` and `str(xs)` cannot disagree
about a character - there is one renderer, not two kept in step. It
allocates per piece appended, so a long list is quadratic and, with no
collector, permanent. That is an argument for a growable buffer, not
for a second renderer.

**9. The os library. Done.** Files, directories and the environment, in
`os.go` and `osdir.go`, entirely through the foreign call op - no new
opcode. Written against Win32 rather than the C runtime because the
failure text has to match Go's, and Go's comes from FormatMessage.

`os.env.set` is the exception that goes through the CRT: a process has
two environments, the block Win32 keeps and the copy the CRT made at
startup, and `SetEnvironmentVariable` updates only the one `getenv`
does not read.

**10. A register allocator. Done.** It hands r8 through r11 to
non-pointer integer values whose whole life fits between calls and the
other barriers, with no label inside the span. Frames stay as slot
packing made them - every value keeps its slot - but straight-line
arithmetic stops round-tripping through memory, which is most of the 30%
the allocator adds to the optimisation stack's runtime win. Off with
`VEYL_NORA=1`, which reproduces the previous output byte for byte.

Stack frames were large here because nothing was reused: a 60-line
program could need 3,000 virtual registers. That was a size problem, not
a correctness one - a frame bigger than a page has to touch every page
on the way down, or Windows never moves the guard page and the first
write below it faults. `reserve()` in `x64.go` does the probing, and has
to keep doing it however small frames get.

**11. The collector. Done.** Mark and sweep, conservative over the
roots and precise over the heap, because the object header from step 1
says which words of a block are pointers. It runs on its own once the
heap has doubled since the last collection, checked at statement
boundaries, and whenever `mem.collect()` asks. It never runs while a
thread besides main is alive, since it scans one stack; a program can
also turn it off entirely with `gc off`.

**12. The byte writer and PE emitter. Done.** `encode.go` turns each
line of assembly into machine code, checked byte for byte against GNU
`as`, and `pe.go` writes the executable with its import table. This is
what removed the MinGW dependency; `x64.go` did not change when it
landed. `VEYL_LINK=mingw` still takes the old route through `gcc`.

**13. Optimisation.** Constant and branch folding, dead code
elimination, redundant load elimination, slot packing, an assembly
peephole and the register allocator from step 10. On top of those:

- **Slot promotion.** The busiest integer and bool locals of each
  function live in rbx, rsi, rdi and r12-r15 for its whole life, across
  calls and loops, instead of in frame slots. Off with
  `VEYL_NOPROMOTE=1`.
- **Collection checks where they can matter.** Each statement's check
  of the heap is dropped when no path reaches it having allocated since
  the last one, so a loop that only does arithmetic has none. Off with
  `VEYL_NOPOLLS=1`.
- **Fused branches.** A compare whose only reader is the branch after
  it becomes one `cmp` and one conditional jump.
- **Instruction selection.** An operation is done in its result's own
  register, with a constant as an immediate and a memory operand where
  x86 takes one; `xs[i]` and `p[i]` fold into one `[base+index*scale]`
  operand, and `x += y` is one instruction on `x`'s home. See
  `x64sel.go`.
- **Division by a constant** is a multiply and shifts rather than
  `idiv`; see `divconst.go`.
- **Floats in registers.** Float locals live in xmm6-xmm15 and short
  temporaries in xmm2-xmm5.
- **No collector where nothing is collected.** A program that never
  makes an object for the collector does not carry it; hello, world is
  3.5 KB.

`VEYL_NOOPT=1` turns all of it off. Against `gcc -O2` on the same
programs, best of three on one machine:

| | Veyl | gcc -O2 |
| --- | ---: | ---: |
| fib(32), recursive calls | 122 ms | 99 ms |
| 200M iterations of `total += (i * i) % 7` | 417 ms | 406 ms |
| sieve over a 20M-element list | 917 ms | 584 ms |
| 100M float multiply-adds | 506 ms | 367 ms |
| summing a `*i32` of 10M elements, ten times | 195 ms | 132 ms |
| 300k int map inserts and lookups | 249 ms | 133 ms |

The C map is a hand-written open-addressed table. The Veyl list is
bounds-checked on every access, and the C array is not.

## What comes next

- **A resolver**, so a misspelled name is reported alongside type
  errors rather than after them.
- **Collection across threads**, so it can run while threads work and
  inside a DLL - which needs every thread's stack, not only the
  collector's own.
- **Unwind tables** (`.pdata`), so native debuggers and Windows' own
  stack walking understand Veyl frames - which would also let a stack
  overflow be reported reliably.
- **A language server**, for completion and go-to-definition in
  editors.
- **Closures as callbacks.**
- **`zip`**, the last library the Go backend has and this one does not.
