# Changelog

Each release on GitHub carries the installer and the section below for
its version.

## 1.84.0

**A faster heap.** Objects up to 2 KB, header included, now live in
64 KB pages of one size class each, taken from a free list per class
instead of one malloc each. A collection tests whether a word is an
object with arithmetic on the page it falls in, rather than through a
hash table of every object built for the occasion, and sweeps the pages
in order. Objects bigger than that are malloc blocks as before.

**Small functions are inlined.** A function of up to 40 instructions
is copied into the places that call it, so a getter or a two-line
helper in a loop costs nothing, and the optimiser sees through the
call. A function that can crash outside a bounds check - raw memory, a
native call, a division by a variable - keeps its call, so a crash
still names it.

**Struct arguments without copies.** A struct passed to a function is
a copy, so the function cannot change the caller's value. When the
function only reads the parameter's fields, writes no fields at all and
calls nothing that could, the copy is skipped: it is the same value
either way. `tests/structargs.vl` covers both sides.

**Also faster:** string map keys (hashed a word at a time, compared by
length first), `for` loops over a list or a map (the loop test moves to
the bottom like any other loop), and stores to locals nothing reads
are dropped.

Best of three, against 1.83.0:

| | 1.83.0 | 1.84.0 |
| --- | ---: | ---: |
| a loop calling a struct-taking function 3M times | 1015 ms | 55 ms |
| one collection with a million strings alive | 200 ms | 35 ms |
| a million short-lived strings beside them | 238 ms | 148 ms |
| a million `"k{i}"` strings built | 373 ms | 141 ms |
| a million inserts into a `{str: int}` | 707 ms | 423 ms |
| a million lookups in it | 524 ms | 354 ms |
| a million inserts into an `{int: int}` | 686 ms | 238 ms |

**Callbacks on Linux and macOS.** Native code there calls with the
System V convention, so a callback stub that only jumped to the Veyl
function handed it its arguments in the wrong registers. Off Windows
the stub now moves them where a Veyl function takes them:
`qsort`'s comparator and `mem.callback` addresses work on all three
systems (`tests/callbacks_c.vl`).

**Fix: float arguments to `mem.call`.** A call through an address with
integer arguments in r8 or r9 and a float after them lost an integer
argument.

**Fix: errors in imported files.** An error found while lowering a
function from an imported file was reported under the file being built.

Checked against the unoptimised build, with the collector eager, off
and normal, on about 1,500 randomly generated programs - loops, lists
and maps of every size, strings past the page limit, structs that point
at each other, collections in the middle - with identical output.

## 1.83.0

**Interfaces in HTML and CSS.** The new `ui` library opens a window
whose contents are a web page, so an interface can have the layout,
fonts and look of anything a browser shows, driven by a Veyl program:

```veyl
let app = must(ui.open("To do", 560, 640))
ui.html(app, page)
while ui.poll(app) {
    for m in ui.messages(app) {
        // what the page sent with veyl.send(...)
    }
}
```

The page gets a `veyl` object: `veyl.send(x)` reaches `ui.messages`,
and `veyl.onmessage` receives `ui.send`. `ui.file` shows an HTML file
with its own stylesheets and scripts beside it, `ui.eval` runs
JavaScript in the page. On Windows it is WebView2, the Edge engine
every updated Windows 10 and 11 already has, so nothing ships with the
program; the window follows dark mode and is sharp on high-DPI
screens. `examples/ui/todo.vl` is a complete to-do list. Linux and
macOS are not done yet.

**`mem.callback(f)`** gives the address of a stub that native code can
call to run `f` - a window procedure, or the methods of a COM object
the program builds itself. The `ui` library is built on it: WebView2's
completion and message handlers are COM objects made in Veyl.

**Empty `[]` and `{}` take their type from where they go.** Assigning
to a variable whose type is known (`xs = []`), pushing into a list of
lists, passing to a parameter, filling a field and the fallback of
`json.decodeOr` no longer ask for an annotation.

**Faster code.** Several new optimisations, measured against C built
with `zig cc -O2` on the same programs:

| best of three | 1.82.1 | 1.83.0 | C |
| --- | ---: | ---: | ---: |
| 1000 structs moved 20,000 times | 556 ms | 93 ms | 86 ms |
| 400x400 float matrix multiply over lists | 355 ms | 134 ms | 69 ms |
| fib(37), recursive calls | 291 ms | 251 ms | 148 ms |
| 400M iterations of integer arithmetic | 675 ms | 668 ms | 536 ms |
| sieve over a 50M-element list | 1620 ms | 1433 ms | 470 ms |

- A bounds check is one unsigned compare, with the failure out of line.
- Within a block, the same list header, element, field or bounds check
  is read once (`cse.go`); a store only forgets loads of its own kind
  of object.
- A list's length and data pointer, or a field, that a loop cannot
  change is read once before the loop (`licm.go`).
- A loop's test is copied to the bottom: one jump a trip instead of two.
- Pointers get scratch registers between calls, spare callee-saved
  registers join the pools, a result can take the register of an
  operand that dies there, and frames only hold the values that are not
  in registers.

The sieve's remaining distance is memory: a `[]bool` keeps eight bytes
per element, where C keeps one.

**Fix: the assembly peephole removed a load.** Two identical lines
`mov r11, qword ptr [r11]` in a row are two loads through a chain, not
one load twice; the peephole dropped the second. Nothing produced that
shape until the allocator started reusing an operand's register, but it
now only drops a repeated move whose destination is not also read.

The generated code was checked against the unoptimised build on about
600 randomly generated programs - loops, aliased lists, struct fields,
`break` and `continue`, calls that change lists - with identical output.

## 1.82.1

**Fix: floats stored into struct fields.** Storing a float into a field
could stop the build with "the generated assembly could not be
encoded" - setting a field of a list element from a variable
(`ws[0].speed = v`), from a builtin such as `max()`, or from a value
clamped in an `if`. Those now compile; `tests/floatfields.vl` covers
them.

## 1.82.0

**Audio on macOS.** `audio.input` and `audio.output` work on a Mac,
through an AudioQueue in the runtime; checked on GitHub's Apple Silicon
runner, where half a second plays and records in half a second.

**Pick the device by name.** `audio.input` and `audio.output` take an
optional fourth argument, any part of a device's name, and
`audio.devices(output)` lists them - so a voice changer can send its
sound straight to a virtual cable:

```veyl
let out = must(audio.output(48000, 1, 480, "cable"))
```

`examples/audio/robot.vl` takes `--in` and `--out`, and the new
`examples/audio/check.vl` lists the devices and times a stream each
way. On Windows and macOS a stream keeps time from the start; on Linux
PulseAudio may take a second or two to start the first one if it had
an idle device suspended, then keeps time.

## 1.81.0

**Live audio.** `audio.input` and `audio.output` open the microphone
and the speakers as streams of float samples, read and written a block
at a time, which is what a voice changer, a tuner or a synthesizer
needs:

```veyl
let mic = must(audio.input(48000, 1, 480))
let out = must(audio.output(48000, 1, 480))
while true {
    audio.write(out, audio.read(mic))
}
```

`examples/audio/robot.vl` is a working voice changer - a robot voice or
an echo. To use it in Discord or a game, route the output through a
virtual audio cable such as VB-Audio Cable. Windows uses winmm; Linux
uses PulseAudio and works, but its delay has not been measured on real
hardware yet. macOS is not done.

## 1.80.0

**Dates before 1970 work on Windows.** `time.format` of any date before
1970 crashed the program, and `time.parse` said such a date did not
match, because the C runtime's local calendar stops at 1970 and 3000.
A time outside that is moved by whole 400-year cycles, over which the
calendar repeats exactly, and moved back. Checked against Go's local
time from 1900 to 9999 on Windows and Linux.

**`url.join` resolves as Go does.** `.`, `..` and `g/` lost their
trailing slash, an empty reference gave something other than the base,
and escapes in the path were decoded away. It is now a port of Go's
`ResolveReference`, and agrees with it on the RFC 3986 examples and 800
random references. The http client follows redirects with it.

## 1.79.0

**JSON text in any script.** Decoding kept only the low byte of a
`\u` escape, so an accented letter came out as an invalid byte, a CJK
character as an unrelated ASCII one, and an emoji - which every JSON
encoder sends as a surrogate pair - as junk. Escapes now decode to
UTF-8, a pair to its code point, a lone surrogate to U+FFFD. Encoding
copied invalid UTF-8 through, which is not JSON; it writes `\ufffd`
for a bad byte now, and escapes U+2028 and U+2029 as Go does. Both
directions agree with Go's `encoding/json` on 900 random strings.

**CSV from Windows reads the same as from anywhere.** A CR LF inside a
quoted field is a line feed, a carriage return as the last byte is
dropped, and a stray one after a closing quote is an error, all as in
Go's `encoding/csv`, checked on 800 random documents.

**No warnings from the library.** Every program using the new
`http.serveWorkers` was told a variable inside it was never used; a
test now fails if any part of the library warns about itself.

## 1.78.0

**`re` finds what Go's `regexp` finds.** Compared on 27000 random
patterns and texts, it now agrees on every match, every capture and
every split. On the way it stopped crashing and stopped hanging:

- A loop whose body can match nothing, like `(a*)*`, went round until
  the stack ran out.
- `(a+)+b` against a line of `a`s took exponential time, as it does in
  most backtracking engines. No pattern takes longer now than its
  length times the text's; that one answers at once.
- Matching recursed once per character, so a few thousand characters
  overflowed the stack. It is a loop now; a megabyte is fine.
- `re.split` gave an extra empty piece after a match at the end.

**`proc` reads names in any script.** Process names, and module names
and paths, came through the system code page and lost the characters
it lacks.

**`abs(-0.0)` is `0`**, as `math.Abs` and C give it. Found by comparing
random float expressions against Go; integer and fixed-width arithmetic
compared the same way, 9000 expressions, all agreed already.

## 1.77.0

**Format specs.** `"{price:.2}"`, `"{name:<10}"`, `"{n:5}"`,
`"{minutes:02}"`: a colon after an interpolated expression gives places
after the point, a width, left alignment, or zero padding. `fixed(x,
places)` does the rounding as a function. Exact ties round half to
even from the exact binary value, so the result is the same on every
system - checked against Go on 2000 values.

**`for c in s`** walks a string's characters, UTF-8 decoded, the same
as `chars(s)`; `for i, c in s` gives the index too.

**Nothing common is quadratic any more.** A benchmark of everyday
operations found several that were, and two that crashed:

- `sort` and `sortBy` were insertion sorts: a million ints took
  minutes. They are stable merge sorts now - 113 ms.
- `repeat`, `str()` of a long list, and `json.encode` of one appended
  piece by piece; with nothing collected inside a builtin, a big enough
  one ran out of memory and crashed. All three are linear now.
- `split` and `replace` cut out a substring at every byte to compare
  it, and `replace` appended once per match. Both compare in place
  now, and `replace` sizes its result once.
- The compiler itself: a 2000-element list literal took 13 seconds to
  compile, from two quadratic passes; 0.75 seconds now.

**8 MB of stack on Windows**, as on Linux, rather than 1 MB: a very
long function overflowed the old one, and recursion now goes as deep
on both.

**The language server** follows `import ... as` namespaces: go to
definition, hover and completion work on `geo.area`.

## 1.76.1

**HTTPS works on Apple Silicon Macs.** A Veyl program runs there as
x86-64 under Rosetta, and the OpenSSL Homebrew installs on those Macs
is arm64, so it could not be loaded and every `https://` fetch failed.
When no OpenSSL loads, the program now uses the system's own TLS,
SecureTransport, which every Mac has; nothing needs installing. The
certificate is checked against the system keychain and the host name.
CI fetches a good and an expired certificate on a Mac each run.

## 1.76.0

**`s += t` is linear.** Building a string by appending in a loop was
quadratic: each append made a new string and copied all of the old
one. An append to a local now grows the string in place, in a block
with room to spare, as long as nothing else has seen the string yet;
reading the variable (other than in `len`) hands the value out, and
the next append copies once before growing again. `s = s + t` takes
the same path. 200000 appends building a 1.3 MB string went from 22
seconds to 85 milliseconds.

**Namespaced imports.** `import "geometry.vl" as geo` puts everything
the file declares under `geo`: `geo.area(2.0)`, `geo.Vec{...}`,
`let v: geo.Vec`, `geo.Kind.Round`, `geo.TAU`. Two files that export
the same names no longer collide. Generics work across namespaces. A
plain import is unchanged.

**`http.serveWorkers(port, n, handler)`** answers on `n` threads, so a
slow request holds up only one of them. `http.serve` still answers one
at a time, which keeps a handler's plain variables safe.

**`&` on a global.** A `var` at the top level has an address, which,
since a global lives as long as the program, can be returned and kept.

## 1.75.0

**`upper` and `lower` past ASCII.** Latin-1, Latin Extended-A, Greek
and Cyrillic letters change case now, with Go's mappings for those
ranges (checked against Go for every code point from U+00A0 to
U+045F). Letters whose other case is a different length, like the
German sharp s, are left alone. ASCII text costs what it did.

**`toFloat` and `isFloat` read hexadecimal floats**, like `0x1.8p3`,
as Go does: a `0x` mantissa needs a `p` exponent.

The limitations list had two stale entries - windows have had images,
sound and text measuring for a while, and a Linux build no longer
needs the system tools when zig is set up - and now says so.

## 1.74.0

**macOS is tested.** Until now a macOS executable was built but never
run anywhere. CI now runs every golden program that is not Windows-only
on a Mac (GitHub's Apple Silicon runner, under Rosetta) and compares
its output, the same way Linux and Windows are checked. They all pass.
The window, input and sound backends are still unrun, since a CI
machine has no screen.

**Building on an Apple Silicon Mac works.** The system compiler was
handed x86-64 assembly without being told the architecture, and on an
ARM Mac it assembles for ARM by default, so every native build failed
there. It now gets `-arch x86_64`.

## 1.73.0

**Cross compiling.** Each of the three systems now builds for the other
two: `--linux` and `--macos` from Windows, `--windows` and `--linux`
from macOS, `--windows` and `--macos` from Linux.

A Windows executable already needed nothing but veyl, which writes the
PE itself. A Linux or macOS one is linked by a C toolchain for that
system, so from another system veyl uses zig's: `veyl cross install`
downloads zig 0.13.0 once (about 80 MB, checked against a pinned hash)
into the user cache folder, and the Windows installer has an optional
"Cross compiling" component that does the same. `veyl cross` says
whether it is set up. The symbol renames objcopy does in a native Linux
build are made in the assembly text instead, and sqlite is loaded at
run time, since there is no libsqlite3 to link against.

Checked: every golden program that runs on Linux, built on Windows and
run on Linux, prints what it should; a Windows build from Linux runs on
Windows. CI now builds a test on each system for the other two and runs
each result on the system it is for.

## 1.72.0

**Strings know their length.** Until now a string was a bare pointer to
bytes ending in a zero byte, with its length found by scanning for that
zero. That one decision was behind three limitations: a string could
not hold a zero byte, so binary data read as text came back cut short;
`len` was a scan of the whole string, so a loop like
`while i < len(s)` was quadratic without looking it; and every
concatenation and comparison scanned both sides first.

Now the length is in the header every heap object already has in front
of it, and string literals carry the same header in the executable. A
string still ends in a zero byte past its length, so it is passed to C
functions as it is. What changed:

- `len(s)` is one load. A loop over a 200 KB string with `len` in its
  condition went from 4.8 seconds to 2 milliseconds.
- A string can hold any byte. `"a\0b"` is three bytes; a file, a socket
  read or an HTTP body with zeros in it comes back whole; `+`, `==`,
  `<`, sorting, map keys, `split`, `indexOf` and the rest count every
  byte, and `print` writes them all.
- Concatenation and comparison copy and compare by length.

Building a string with `+=` in a loop is still quadratic, since each
`+` makes a new string: push the pieces and `join` them.

## 0.72.0

**A DLL collects its garbage.** It used to keep everything it ever
allocated, since its exports run on the host's threads and the
collector could not see them; a mod or plugin left running leaked
until it was unloaded. Each export is now entered through a wrapper
that tells the collector about the calling thread on the way in and
parks it on the way out, so a collection stops whichever host threads
are inside the DLL and reads their stacks, as in a program.

What an export returns, a `str` or an extern struct, stays alive until
the same thread calls into the DLL again, so the host can read it in
the meantime. A thread that ends is forgotten when Windows says so.

## 0.71.0

**Fewer antivirus false positives.** A Windows executable from Veyl
used to stand out from what mainstream linkers write: no DOS stub, no
address space layout randomisation, a zero checksum and timestamp, no
version information and no manifest. Engines that score a file on its
shape flagged it for that alone. Now it has all of them:

- the standard DOS stub, a real checksum, and the build time as its
  timestamp (`SOURCE_DATE_EPOCH` when set);
- ASLR, with a relocation table for the absolute addresses a static
  library brings;
- version information, set with `--product`, `--description`,
  `--company`, `--copyright` and `--file-version`, defaulting to the
  file's name and 1.0.0.0;
- a manifest asking for no elevation, so a program called `setup.exe`
  no longer makes Windows ask for administrator rights.

It costs about 2.5 KB per executable. Signing is still up to you; see
Antivirus in SYNTAX.md.

**Floats print exactly.** The digits came from msvcrt, which rounds
some values wrong: in 30,000 random doubles, 433 printed a wrong last
digit and 18 printed a different number. They come from the Universal
C Runtime now, with ties going to the even digit as Go and glibc do.
Checked against Go on 300,000 doubles on Windows and 100,000 on Linux.

**`zip`**: `zip.make`, `zip.list` and `zip.extract`, the last library
the Go backend had. Archives are compressed, read by any zip tool, and
read what any zip tool writes. Extraction refuses an entry that would
land outside the destination, before writing anything.

## 0.70.0

**A new logo.** A cube with its front corner carved out, in the
installer, the file icon for `.vl`, the VS Code extension, the README,
and now `veyl.exe` itself, which had no icon before. `icons/veyl.svg` is
the source and every size is drawn from the same shape.

**`veyl build --icon game.ico`** gives a Windows program its own icon:
the one Explorer, the taskbar and shortcuts show. Every size in the
`.ico` is kept. A Linux or macOS build says it left the icon out, since
those executables carry none.

**`veyl build -o path`** writes the executable where it says, making the
folder if need be. It used to be ignored, and every build landed next
to its source. Build's options can now come after the file too:
`veyl build game.vl -o dist/game.exe --icon game.ico`.

**The vulkan package draws indexed meshes**: `vkIndices` and
`vkDrawIndexed`, so a vertex shared by several triangles is stored once.

## 0.67.0

**OpenGL and Vulkan on Linux.** The `gl` and `vulkan` packages now run
on Linux as well as Windows, from the same source: `gl` makes a GLX
context on the win window, `vulkan` an Xlib surface. Checked under Xvfb
on Mesa's software renderers, where both read back the same pixels the
Windows builds do on a real GPU. What made it possible:

- **`mem.call` off Windows.** It used to call with the Windows
  convention everywhere; on Linux and macOS the build now generates a
  small C bridge for each shape of call, so `mem.symbol` and `mem.call`
  reach any shared library.
- **`win.display(w)`**, the X11 `Display*` the window is on (the
  `HINSTANCE` on Windows), and **`win.handle(w)`** now the real X11
  `Window` on Linux and `NSWindow` on macOS. The runtime's emulated
  user32 calls accept either.
- **`from "opengl32"` and `from "vulkan-1"`** name `libGL.so.1` and
  `libvulkan.so.1` on Linux, opened at the first call, so nothing needs
  installing until a program uses them.
- **Windows-only externs in packages.** A package may declare a
  function from `user32`, `gdi32` and the other Windows libraries and
  build on Linux; calling it there stops the program with a sentence.
  The program's own file still gets a build error.

**`os.name()` was always `"windows"`.** It now says what the program
was built for: `"windows"`, `"linux"` or `"macos"`.

## 0.66.0

**`win.handle(w)`.** The window's `HWND`, so code outside the compiler
can attach something of its own to a win window and keep `win.poll`
and the input functions. The new `gl` package uses it for OpenGL:

```veyl
import "gl"

let w = must(win.open("cube", 800, 600))
let g = must(glOpen(w))
while win.poll(w) {
    glClearColor(0.1, 0.1, 0.15, 1.0)
    glClear(GL_COLOR_BUFFER_BIT)
    glSwap(g)
}
```

`veyl get gl` installs it, and `veyl get vulkan` the same for Vulkan:
a swapchain with vsync, depth, pipelines from SPIR-V and push
constants, with every Vulkan function also callable directly. Both
are Windows only so far; off Windows the handle is the runtime's own
window record.

**Run from the editor.** VS Code shows a run button on a `.vl` file,
as it does for Python, with Build in its menu and Ctrl+F5 for Run. The
program runs in a terminal called Veyl in the file's folder. Sublime
gets a build system (Ctrl+B builds, Run as a variant) and Vim an
ftplugin (`:make`, F5 to run).

**Warnings.** Taking a variable's address with `&` now counts as using
it, so `let id: u32 = 0` passed as `&id` to a native call no longer
warns that `id` is never used.

## 0.65.0

**`veyl doc`.** Writes a reference for a file from its declarations and
the comments directly above them:

```veyl
// The area of a w by h rectangle.
pub fn area(w: int, h: int) -> int {
    return w * h
}
```

```
veyl doc shapes.vl                  Markdown on the console
veyl doc shapes.vl -o shapes.md     into a file
veyl doc shapes.vl -o shapes.html   a web page
```

Functions, structs with their methods, enums, interfaces, consts and
vars each get their signature and comment; the file's opening comment
introduces the page. A file that marks anything `pub` documents only
what it exports.

**Doc comments on hover.** In VS Code, hovering a name shows the comment
above its declaration under its signature.

Test: TestDoc.

## 0.64.0

**Warnings, and `veyl check`.** The compiler now warns about things that
are allowed but almost always a mistake:

```
calc.vl:2:5: warning: "spare" is declared but never used
calc.vl:4:5: warning: this can never run - it comes after a return
```

A `let` nothing reads is reported (assigning to it does not count),
unless its name starts with `_`; so is code after a `return`, `break` or
`continue`. Warnings never stop a build. `veyl build` prints them,
`veyl run` does not, and the new `veyl check` prints errors and
warnings without building anything. In VS Code they show as warnings,
not errors.

Test: TestWarnings.

## 0.63.0

**`veyl test`.** Tests are written in Veyl, in `test` blocks beside the
code:

```veyl
test "adds" {
    expect(add(1, 2) == 3)
    expectEq(add(2, 2), 4)
}
```

```
veyl test calc.vl
PASS  calc.vl: adds (0.00s)
1 passed, 0 failed
```

`expect(cond)` checks a condition and `expectEq(got, want)` two values,
by contents for lists, maps and structs. A failure is reported with its
line - `expectEq` says what both sides were - and the test carries on, so
one run shows everything wrong. Each test runs in its own process, so a
crash fails that test alone. `veyl test` exits 1 when anything failed;
`--run text` picks tests by name, and a folder runs every file with
tests. `veyl run` and `veyl build` leave test blocks out.

**Benchmarks.** `bench "name" { ... }` blocks are timed by
`veyl test --bench`, which repeats each until a round takes half a
second and reports the time per run.

Test: TestVeylTest.

## 0.62.0

**`veyl fmt`.** Lays a file out the standard way: four-space indents,
one space around operators and after commas, at most one blank line in
a row, no trailing whitespace. Comments are kept, nothing is reordered
or reflowed, and a file that does not lex is left alone. Give it files
or folders; `veyl fmt --check` changes nothing, lists what would change
and fails if anything would, for a CI step.

```
veyl fmt game.vl
veyl fmt .
veyl fmt --check src
```

In VS Code, **Format Document** (Shift+Alt+F) does the same through the
language server.

Tests: every golden program and example is formatted and run again
against its expected output, and formatted twice to check the layout is
stable.

## 0.61.0

**A language server, and VS Code uses it.** `veyl lsp` speaks the
Language Server Protocol, and the VS Code extension now starts it, so
editing a `.vl` file gets:

- errors as you type, the same ones `veyl build` reports;
- completion of names in scope and the library, and after a dot a
  struct's fields and methods, a library's functions, or an enum's
  variants;
- go to definition (F12), into imported files too;
- hover, for a variable's type or a function's signature;
- the outline of the file (Ctrl+Shift+O).

The extension runs the veyl that installed it, so nothing needs to be on
PATH; the new **Veyl: Path** setting picks another. Any other editor with
an LSP client can start `veyl lsp` itself.

Tests: TestLanguageServer, and TestVSCodeClient, which runs the
extension's client under node against a real `veyl lsp`.

## 0.60.0

**Unwind tables.** Windows executables now carry a `.pdata` section, so
Windows can walk the stack through Veyl functions. A native debugger
(Visual Studio, WinDbg) shows the whole call stack of a Veyl program
instead of one frame, a crash dump has a stack, and
`RtlCaptureStackBackTrace` finds every caller - before, it lost its way
at the first Veyl function. The debugger shows the calls, not Veyl
variables or source lines; there are no debug symbols yet.

A function that calls itself without end is reported as
`crash: stack overflow - probably a function that calls itself without
end`, now covered by a test.

Tests: TestUnwindTable, tests/unwind.vl, and a stack overflow in
TestErrorLocations.

## 0.59.0

**The collector runs while threads do.** Before, nothing was collected
while any thread besides main was alive, so a long-running thread that
allocated grew the heap until it finished. Now a collection stops every
thread, reads every thread's stack, and lets them all go. A thread
stops at the next statement it starts, or at once if it is waiting in
`thread.join`, `thread.lock`, `thread.wait`, a channel, `sleep`, a
socket, `input`, an HTTP request or a database query - so a thread
blocked on another never holds a collection up. In a program that
starts a thread every loop checks once a time round, so even
`while atomic.get(flag) == 0 {}` stops. `mem.collect()` works with
threads running too, where it used to do nothing.

A program without threads pays nothing for any of this.

Test: tests/gcthreads.vl runs four workers making garbage while each
keeps a list alive, a thread blocked on a channel and one spinning on
an atomic, with main collecting, and checks every kept value survived,
collections ran while the threads did, and the heap stayed bounded.

## 0.58.0

**VS Code highlighting works again.** The installer used to copy the
extension into `.vscode\extensions`, and current VS Code ignores a
folder there unless it is listed in its `extensions.json` registry -
which exists as soon as any other extension is installed - so `.vl`
files stopped being colored, with no error. The installer now installs
the extension through VS Code itself, with the new `veyl vscode`
command: it packs the extension as a `.vsix` and runs
`code --install-extension`, then removes the old copies that were never
loaded. Reload VS Code after installing.

If VS Code's `code` command is not found, `veyl vscode` leaves the
`.vsix` in the temp folder and says how to install it with
**Extensions: Install from VSIX...**. `veyl vscode --vsix FILE` writes
the package anywhere, and `veyl vscode --uninstall` removes it.

## 0.57.0

**Maps keyed by an enum.** `{Color: int}` works: index, assign, `has`,
`remove`, `keys`, equality and `for k, v in m`, with the keys printing
by name in the order the enum declares them.

```veyl
let seen: {Dir: int} = {}
seen[Dir.West] += 1
seen[Dir.North] += 2
print(seen)                   // {North: 2, West: 1}
```

A struct, a float or an enum whose variants carry values is still
refused as a key, now with "a map key must be str, int or a plain enum".
An annotation that is wrong is reported once, not again as an untyped
`{}` or `[]`.

Test: tests/enummaps.vl.

## 0.56.0

**Undefined names are reported with the type errors.** A misspelled
variable, function or struct used to be found only after every type
error was fixed, and an unknown function was reported as "not on the
assembly backend yet". The checker now reports them itself, in the
same list, and offers the closest real name when one is a likely slip:

```
hello.vl:5:7: undefined variable "totl" - did you mean "total"?
hello.vl:6:7: undefined function "lenn" - did you mean "len"?
```

A const or var computed from a `let` in the program body now gets the
sentence explaining why it cannot see it, instead of "a global of type
unknown".

Test: TestUndefinedNames.

## 0.55.0

**HTTPS on Linux and macOS.** `http.get`, `http.post` and
`http.download` now fetch `https://` urls off Windows. The socket
client puts TLS on its connection with the system's OpenSSL, loaded
when the first https url is fetched, so a program that never uses https
needs no OpenSSL where it runs. The certificate is checked against the
system's trust store and must name the host; a failure says which check
failed, as in `the certificate of localhost was not accepted: hostname
mismatch`. A redirect from `https://` down to `http://` is refused.
On macOS this needs `brew install openssl@3`, and is untested so far.

**Socket errors name the real reason on Linux and macOS.** A refused
connection said "A device attached to the system is not functioning";
it now says "No connection could be made because the target machine
actively refused it.", the sentence Windows prints.

Test: TestHTTPS runs TLS servers with a CA the test makes and checks a
name, an address, POST, chunked and large bodies, a redirect, a refused
downgrade, a wrong name and an unknown CA.

## 0.54.0

**Redirects in the HTTP client on Linux and macOS.** `http.get`,
`http.post` and `http.download` now follow a 3xx off Windows the way
WinHTTP does on it, up to ten deep. A 301, 302 or 303 turns a POST into
a GET with no body, as browsers do; a 307 or 308 repeats the request
unchanged. The Location can be a full url, a path on the same host, or
a path relative to the current one. A redirect to `https://` fails with
the same reason a direct `https://` url does off Windows.

**`http.redirect(url)`** builds a 302 for a Veyl server, and `Response`
has a `location` field, sent as the `Location` header when it is set.

Test: tests/httpclient.vl covers each redirect kind, the loop limit and
a redirect to https.

## 0.53.0

**The HTTP client on Linux and macOS.** `http.get`, `http.post` and
`http.download` now work off Windows for `http://` urls. On Windows the
calls still go through WinHTTP (which does TLS, redirects and chunked
bodies); on Linux and macOS the same `__winhttp` builtin is lowered to a
socket client written in Veyl over the `net` library, so the plain-HTTP
case behaves the same - same return value, same "server replied 404" on
a 4xx. Content-Length and chunked response bodies are both handled.
Redirects are not followed yet, and an `https://` url fails with a clear
message rather than a wrong answer, because TLS is not written by hand.

Test: tests/httpclient.vl runs the Veyl http server in a thread and
fetches from it over the loopback; skipped on Windows, which reaches the
same library through WinHTTP.

## 0.52.0

**SQLite on Linux and macOS.** The `db` library - open, exec, query,
changes, lastId, close - now works off Windows. On Windows it still loads
the `sqlite3.dll` the `sqlite` package ships; on Linux the build links the
system `-lsqlite3`, and on macOS it loads the `libsqlite3.dylib` that
comes with the OS at run time (so the cross toolchain needs no macOS SDK
stub for it). Each `sqlite3_` call the library makes is answered by an
ms_abi wrapper that forwards to the real one, the same bridge every other
runtime call uses, and sqlite is linked only when a program actually uses
`db`.

Test: tests/db.vl opens an in-memory database, inserts and queries rows,
and checks changes and lastId; skipped on Windows, which reaches the same
library through the packaged DLL.

## 0.51.0

**TCP sockets on Linux and macOS.** The `net` library - listen, accept,
connect, send, recv, close - now works off Windows, not just on it. The
WinSock calls it makes are BSD sockets with a few Windows spellings, so
the runtime answers them with POSIX sockets: no startup to do, close
instead of closesocket, and a translation of the SOL_SOCKET /
SO_REUSEADDR constants; sockaddr_in and struct hostent already have the
layout the library builds by hand.

Test: tests/sockets.vl runs a client and a server over the loopback;
skipped on Windows, which reaches the same library through WinSock.

## 0.50.0

**Calling C libraries on Linux and macOS.** An `extern fn ... from
"libfoo.so"` now reaches that library's function directly, not just the
names the runtime happens to wrap. For each such symbol the build
generates a tiny ms_abi wrapper and lets the C compiler bridge the
calling convention (correct for any mix of integer, pointer and float
arguments), then links the named library. So `extern fn sqrt(x: float)
-> float from "libm.so"` just works, the same way DLL externs work on
Windows. Variadic C functions still need the Windows target.

## 0.49.0

**macOS sound (CoreAudio).** The `sound` library and the MCI music calls
now play on macOS, through an AudioQueue from the AudioToolbox framework
(loaded with dlopen, like the rest of the macOS backend). The mixer is the
same as on Linux - a set of voices summed each frame - with CoreAudio
pulling the mixed samples through a callback. So a macOS build now has the
window, input and sound a game needs. Still experimental and built
without a Mac to test on.

## 0.48.0

**macOS windows and input (Cocoa).** A windowed program now builds for
macOS with a Cocoa backend (compiler/linuxrt/veylmac.c), the counterpart
to the X11 one on Linux. It is plain C: the Objective-C runtime and the
AppKit and CoreGraphics frameworks are loaded with dlopen and called
through objc_msgSend, so it needs no Objective-C compiler, no linked
framework and no macOS SDK to build - a Mac has all of them at run time.
A window's pixels (drawn by the shared software GDI, veylgdi.c) are shown
by handing them to a CoreGraphics image set as the view's layer contents;
mouse and keyboard come from the Cocoa event queue, mapped to the same
WM_* messages win.poll reads.

This is new and was built without a Mac to test on, so it is marked
experimental; sound on macOS is not wired up yet (CoreAudio is next) and
its calls succeed silently. Console and off-screen programs are
unaffected.

## 0.47.0

**macOS target.** `veyl build --macos` produces an x86-64 Mach-O
executable, which runs natively on Intel Macs and through Rosetta on
Apple Silicon. It works the same way as the Linux target: the generated
code is unchanged and the same runtime answers its calls under the
Windows calling convention. A new file, compiler/macos.go, rewrites the
assembly for the Mach-O assembler - its section names, byte alignment,
and the leading underscore on the symbols that cross into the runtime -
and the runtime (veylrt.c) grew the handful of macOS spellings it needed
(thread id, stack bounds, the crash context). The build runs the system
`cc` on a Mac, or a cross compiler named in VEYL_MACCC elsewhere (zig
cross-compiles it from Linux).

Console and off-screen-drawing programs work on macOS. The on-screen
window layer is X11, which macOS does not ship, so windowed programs wait
on a native Cocoa backend. The same seven Windows-only programs skipped
on Linux are Windows-only on macOS too.

## 0.46.0

**Graphics, input and sound on Linux.** The `win` library now works on
Linux, not just Windows: opening a window, the frame buffer, the keyboard
and mouse, `win.rect`/`line`/`circle`/`text`, images, and the `sound`
library. Drawing is done in software (`linuxrt/veylgdi.c`); the window
and input go through X11 and sound through PulseAudio, both loaded at run
time with dlopen (`linuxrt/veylwin.c`), so a program that opens no window
needs neither installed. A graphical game built with `--linux` runs the
same as on Windows.

Of the test programs, 63 of 70 now pass byte-for-byte on Linux; the other
seven test Windows-only facilities (COM, the PE image layout, raw native
calls through `mem.call`/`mem.symbol`, the Windows process APIs, and COFF
static libraries) and are skipped there. CI runs the Linux suite too.

## 0.45.0

**Linux.** veyl builds Linux executables as well as Windows ones.
`--linux` and `--windows` choose; the default is the system veyl runs
on, so on Linux `veyl run app.vl` just runs.

The program's code is the same on both systems. Every library call it
makes is answered on Linux by a small runtime that does the same job on
POSIX under the Windows calling convention: printing and formatting,
files and directories, memory, threads, locks and condition variables,
time, the environment and the command line, and the crash handler.
Console programs behave the same on both; 59 of the 70 test programs
print the same bytes on Linux as on Windows.

The libraries that only exist on Windows - windows and drawing, sound,
COM, `proc`, sockets and HTTP, sqlite, `mem.symbol`, static `.lib`
archives and `--dll` builds - are reported by name when a Linux build
uses them. For now a Linux build assembles and links with the system's
`as`, `objcopy` and `cc`; a built-in ELF writer is the next step.

## 0.44.0

**Static libraries can call into DLLs.** Object code pulled from a static
library usually calls the C runtime or a Win32 function - `printf`,
`strlen`, and the like. Those symbols are now imported from their DLL
automatically, the same way a program's own `extern` declarations are, so
a static library that uses the C runtime links and runs.

Still not linked: the small C runtime helper symbols a C compiler expects
a linker to supply itself (stack probes, the constructor shim, the
stack-guard cookie), COMDAT sections, weak externals, common symbols and
the `.pdata`/`.xdata` unwind tables. Each is reported as a clear error.

## 0.43.0

**Static libraries.** A `from` clause ending in `.lib`, `.a`, `.obj` or
`.o` names a static library instead of a DLL:

```veyl
extern fn vy_add(a: int, b: int) -> int from "mathlib.a"
```

The object code that defines the symbol is pulled out of the archive at
build time and linked into the program, rather than imported from a DLL
at run time. The compiler reads `!<arch>` archives and the COFF objects
inside them, pulls the members a program refers to, and applies their
ADDR64, ADDR32NB and REL32 relocations.

What links so far is self-contained object code: functions and the data
they use, calling each other and nothing outside the libraries named.
Object code that calls into a DLL, the C runtime helper symbols, COMDAT
sections, weak externals, common symbols and the `.pdata`/`.xdata`
unwind tables are reported as clear errors rather than a wrong image;
they come next.

## 0.42.0

**com: COM interfaces.** DirectX, WIC, WMI and the shell are reached
through COM objects, not flat functions. `com.call(obj, slot, args...)`
calls a method by its slot in the interface and returns the `HRESULT`;
`com.call64` and `com.callF` are for the methods that return a pointer
or a `double`.

Around it: `com.init()` / `com.done()`, `com.create(clsid, iid)` and
`com.query(obj, iid)` (both return a result carrying the `HRESULT` on
failure), `com.addRef` / `com.release`, `com.guid(text)` and
`com.guidText(p)`, `com.bstr` / `com.bstrFree`, `com.hex(hr)` and
`com.message(hr)`.

**Wide strings.** `mem.wide(s)` gives a `str` as zero-terminated UTF-16
and `mem.wstr(p)` reads one back, for COM and for any Windows function
with a W in its name.

`examples/ffi/adapters.vl` lists the display adapters through DXGI.
Implementing an interface in Veyl, for an API that calls back through
one, is not here yet.

## 0.41.0

**proc: listing.** The other half of the `proc` library - finding
things, where 0.40.0 was reading and writing them.

- `proc.pids()`, `proc.name(pid)`, `proc.parent(pid)` and
  `proc.find(name)` for the running processes.
- `proc.modules(pid)` lists the executable and DLLs loaded in a
  process as `ProcModule` structs (`name`, `path`, `base`, `size`), and
  `proc.base(pid, name)` gives where one is loaded.
- `proc.threads(pid)` lists a process's thread ids.
- `proc.regions(h)` lists the committed regions of an address space as
  `ProcRegion` structs with `base`, `size`, `protect`, `kind` and
  `readable` / `writable` / `executable`.
- `proc.scanAll(h, pattern)` returns every match of a byte pattern in
  the readable memory of a process. It walks the regions itself, so it
  needs no start or size, and reads a megabyte at a time.

All of it is still Veyl over `mem.symbol` and `mem.call`; nothing needs
`extern`. A process that cannot be looked into gives an empty list or
`0`.

**Fixed:** a program that declared its own struct called `Request` or
`Response` silently got the `http` library's fields instead of its own.
A struct the program declares is now the program's.

## 0.40.0

**proc: process memory.** A library over the standard Windows debugging
calls - OpenProcess, ReadProcessMemory, WriteProcessMemory - the
surface a debugger or profiler uses. `proc.current()` and `proc.pid()`
for the running process, `proc.open(pid)` / `proc.close(h)` for another
by id, `proc.read(h, addr, n)` and `proc.write(h, addr, data)` for raw
bytes, `proc.readU8` / `readI32` / `readI64` and `writeI32` / `writeI64`
for typed access, and `proc.scan(h, start, size, pattern)` for a byte
pattern with `??` wildcards.

It is written in Veyl on top of `mem.symbol` and `mem.call`, so it adds
no import-table plumbing. A call the operating system refuses comes back
`0`, an empty `bytes`, or `-1`, never a crash, so a scan over a range
does not have to know in advance which parts are readable.

Listing a process's modules and threads, and finding one by name, are
not here yet; the toolhelp calls still go through `extern`, as in
`examples/ffi/memreader.vl`.

## 0.39.0

Memory and types, the ground C++-style programs stand on.

**Fixed-width numbers.** `i8`, `u8`, `i16`, `u16`, `i32`, `u32`, `u64`
and `f32` are real variable types: locals, parameters, returns, fields,
list and map elements. Each wraps at its own width, unsigned ones
divide, shift and compare unsigned, and an `f32` rounds to single
precision after every operation, so the answers match C's exactly.
There are still no implicit conversions: `i32(x)`, `u8(x)`, `f32(x)`
and the rest convert, wrapping like a C cast, and a literal fits any of
them when its value is in range. `i64` and `f64` name `int` and
`float`.

Reading memory at a width now gives that width's type: an extern
struct's `u32` field reads as `u32` and a `*u16` as `u16`, where both
used to read as `int`. Code that mixed them with `int` converts
explicitly now, as in `int(entry.pid)` or `u32(colour)`.

At the native boundary an `f32` argument is passed as a C `float`, a
narrow or `f32` result is read at its width, and callback and export
stubs extend each fixed-width argument whatever native code left in
the rest of the register.

**new and delete.** `new T`, `new [n]T` and `new T{x: 1}` allocate
zeroed C-layout memory from the C heap and give back a `*T`;
`delete(p)` frees it. It is never collected or moved, and native code
can free what Veyl allocated and the other way round. Methods can be
called through a pointer to an extern struct.

**&local.** `&x` gives the address of a local variable or parameter -
a number of any width, a `bool` or a pointer - for native
out-parameters with no `mem.alloc` first. The compiler refuses an
address that would outlive its function: returned, stored in a global,
a field, a list or a map, or held by a closure.

Fixed along the way: the encoder dropped the REX prefix `sil` and
`dil` need as a source operand, a function with a float slot in an xmm
register but nothing else pooled could emit an instruction the
encoder refused, and `let p: *T = nil` failed to compile.

## 0.38.0

**Pixels as memory.** `win.pixels(w)` returns a window's or canvas's
back buffer as a `*u32`, one `0x00RRGGBB` per pixel, so a program can
draw pixel by pixel at the speed of writing memory rather than one
call into Windows per line. GDI drawing - text, lines, images - still
works on the same pixels.

`examples/gui/render3d.vl` is a software 3D renderer built on it: four
shapes, flat shading, a depth buffer, delta time, several hundred
frames a second.

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

What it took: width-specific loads and stores in the IR, and the
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
