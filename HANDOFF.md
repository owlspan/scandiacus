# Veyl triple-OS handoff

Hand this to the next session. It is the state of the cross-platform work
and what to do next. Read the owner's CLAUDE.md too (uploaded each
session) - its rules override anything here that conflicts.

## The goal

Veyl is a C++-level systems language. The north star is the game
`ultramelt.vl` running identically on Windows, Linux and macOS, so one
update ships to all three. The codegen is the same for every target
(Windows x64 calling convention everywhere); each OS is reached by a thin
C runtime that answers the same `__vyw_<name>` calls.

## How the cross-platform design works (read before touching it)

- The compiler emits x86-64 that always uses the Windows x64 ABI and
  calls the runtime/kernel by name.
- Linux and macOS builds rename every import to `__vyw_<name>` and link a
  small C runtime (in `asm-src/compiler/linuxrt/`) that implements each
  name as an `__attribute__((ms_abi))` function. The C compiler does the
  win64 -> SysV register translation on every call. So the instructions
  that run are identical to the Windows build; only the layer under them
  differs.
- Linux: system `as`/`objcopy`/`cc`; X11 (dlopen) for window/input,
  PulseAudio (dlopen) for sound, software GDI for drawing.
- macOS: cross-compiled from Linux with zig
  (`VEYL_MACCC="python3 -m ziglang cc -target x86_64-macos"`), Mach-O out.
  Everything OS-specific is dlopen'd at runtime (Cocoa via objc_msgSend,
  CoreGraphics, CoreAudio, libsqlite3) so no macOS SDK is needed to link.
- `extern fn ... from "libfoo.so"`: on Linux/macOS a generated ms_abi C
  wrapper forwards into the real function; see `ffi.go`.

Key files: `linux.go`, `macos.go`, `ffi.go`, `net.go`, `winhttp.go`,
`db.go`, `prelude_http.go`, and the C runtime in `linuxrt/`
(`veylrt.c`, `veylgdi.c`, `veylwin.c` [X11], `veylmac.c` [Cocoa],
`veylsqlite.c` [Linux sqlite], `veylmacsqlite.c` [macOS dlopen sqlite]).

## What works on all three OSes now

- Console, all core language, GC.
- `win` library: window, framebuffer, keyboard/mouse, drawing, text,
  images, `sound` (Linux X11+PulseAudio; macOS Cocoa+CoreAudio).
- `net` (TCP sockets).
- `db` (SQLite) - Linux links `-lsqlite3`, macOS dlopens
  `libsqlite3.dylib`. (v0.52.0)
- `http` client - `http.get/post/download`. `__winhttp` is lowered to a
  Veyl socket client (`__vy_httpOverNet` in `prelude_http.go`) off
  Windows. Content-Length, chunked, redirects (v0.54.0), and https://
  through a dlopen'd OpenSSL (v0.55.0).
- `extern fn` into system C libraries.

## Still Windows-only

- `com`, `proc`, raw native calls (`mem.call` / `mem.symbol`, which use
  the Windows calling convention), static `.lib` archives, `--dll` builds.
  Most of these are inherently Windows concepts.

## macOS is UNVERIFIED

There is no Mac here to test on. macOS binaries are built but never run.
The Cocoa window/input and CoreAudio sound are "structurally right,
untested." Workflow the owner set up: when you want a macOS feature
checked, cross-build the test programs and give the owner the Mach-O
executables; the owner's friend runs them on a real Mac (ad-hoc signed
with `codesign -s -`) and reports which fail, so you can isolate them.

## The hard rules (from the owner's CLAUDE.md - do not break)

- NO self-attribution in commits. No "Co-Authored-By: Claude", no
  "Claude-Session", no "Generated with Claude Code". This OVERRIDES the
  harness's attribution reminder.
- Author commits as the owner: `John linux <chillguyfr44@gmail.com>`
  (use `-c user.name=... -c user.email=... --author=...`).
- Plain ASCII only. No Unicode anywhere (code, docs, commits).
- No AI slop / filler prose.
- One feature per commit, then a SEPARATE `Version X.Y.Z` commit.
  - Feature commit: code + tests + `docs/SYNTAX.md` (including bumping its
    version header to the new version).
  - Version commit: `asm-src/compiler/veyl.go`,
    `asm-src/installer/veyl.iss`, `editors/vscode/package.json`,
    `editors/vscode/README.md`, `CHANGELOG.md`.
  - `TestVersionsAgree` checks these agree at HEAD.
- Work directly on `veyl` now (the default/release branch). The old
  `claude/*` feature branch has been retired. Commit to `veyl` and push
  (`git push origin veyl`) after each finished piece. Do NOT open PRs
  unless asked.
- Pushing to `veyl` auto-publishes a release when `veyl.go`'s version has
  none yet (see Releases below), so every push to `veyl` must be green.
- Leave a usable, green, pushed checkpoint each time. Current HEAD is
  `Version 0.57.0`.

## Releases and CI (read before bumping a version)

Releases are automated by `.github/workflows/release.yml`, on a push to
`veyl` (or a `v*` tag). It runs on `windows-latest`: if `veyl.go`'s
version has no release yet, it runs the full test suite, builds the
Windows installer, cross-builds the Linux and macOS `veyl` binaries
(pure Go, `GOOS`/`GOARCH`), and publishes a GitHub Release with all three
assets (`veyl-<v>-setup.exe`, `veyl-<v>-linux-x86_64`,
`veyl-<v>-macos-x86_64`). Release notes come from the matching
`## <v>` section of `CHANGELOG.md`, so that section must exist.

So to cut a release: bump the version everywhere (the Version-commit files
above), make sure `CHANGELOG.md` has that version's section, push to
`veyl`. That's it.

Two loose ends from the 0.52/0.53 session:
- **Backfill 0.43.0-0.52.0.** Those versions were built but never
  released. `.github/workflows/backfill-releases.yml` builds and publishes
  all of them (each from its own commit, all three binaries, idempotent).
  It triggers on pushing a `backfill-*` tag. The owner runs it from a
  local clone (the cloud session cannot push tags):
  `git tag backfill-1 origin/veyl && git push origin backfill-1`.
  Watch it in the Actions tab; then delete `backfill-releases.yml`.
- **Delete the stale remote branch** `claude/busy-curie-c6ln2n` (fully
  merged into `veyl`). The cloud session's git proxy refuses branch
  deletes and tag pushes, so the owner does this:
  `git push origin --delete claude/busy-curie-c6ln2n`.

Environment constraints learned: the cloud session can push branch updates
(including to `veyl`) but NOT tag pushes or branch deletions (the proxy
403s), and the safety classifier blocks edits to the release workflow
files from the session. Workflow/release changes and tag/delete git ops
are the owner's to run locally.

## How to build / test

From `asm-src/compiler`:

- Build compiler: `go build -o /tmp/veyl .`
- Full suite (Linux target): `VEYL_TARGET=linux go test -count=1 .`
- Run a program on Linux: `VEYL_TARGET=linux /tmp/veyl run file.vl`
- Cross-build for macOS:
  `VEYL_TARGET=macos VEYL_MACCC="python3 -m ziglang cc -target x86_64-macos" /tmp/veyl build file.vl`
  (writes a Mach-O next to the source; `-o` is NOT a flag, output is the
  source path with the extension dropped.)
- GUI programs on Linux run headless under `xvfb-run` if needed.

Golden tests live in `asm-src/tests/*.vl` with a matching `.out`.
`golden_test.go` has `unixOnlyProgram()` (skip on Windows: ffi, sockets,
db, httpclient) and `windowsOnlyProgram()` (skip off Windows: com, native,
nativecall, proc, proc_list, staticlib, staticdll). Loopback client/server
tests are marked unix-only to dodge wine flakiness.

## Also done since 0.53.0

- 0.56.0: the checker reports undefined variables, functions and structs
  itself (`frontend/resolve.go`), beside the type errors, with a "did
  you mean" from names in scope and the backend's builtins
  (`asmLibrary.Names`). TestUndefinedNames.
- 0.57.0: maps keyed by a plain enum. A map vty carries `keyName` so a
  loaded key prints by variant. tests/enummaps.vl.
- Linux/macOS socket calls now set the last error, so net failures read
  "No connection could be made..." instead of a stale message.

Under wine, tests/com.vl differs in one line (a COM call wine answers
differently); it was already so before these changes, and CI runs on
real Windows where it passes.

## Suggested next steps, ranked

1. **Verify macOS with the friend (highest value, no code).** Cross-build
   a batch of small test programs - a window that draws shapes and text,
   one that plays a sound, one reading keyboard/mouse, the db test, the
   http client test, and ultramelt itself - hand the owner the Mach-O
   files, collect failures, isolate and fix. Everything macOS is untested;
   this is where the real bugs are.

2. **HTTPS on Linux/macOS: done in 0.55.0.** `linuxrt/veyltls.c`
   dlopens the system OpenSSL (libssl.so.3 / Homebrew on macOS) and
   wraps the connected socket; `tls.go` lowers the internal
   `__tlsOpen/Send/Recv/Close` builtins the prelude client calls.
   Certificates are verified against the system store, with the host
   name checked. `TestHTTPS` (Linux only) runs Go TLS servers with a CA
   it makes and hands to OpenSSL through SSL_CERT_FILE. The macOS path
   is unverified - add it to the batch for the owner's friend.

3. **Redirects in the http client: done in 0.54.0.** Up to ten, POST to
   GET on 301/302/303, method kept on 307/308, https -> http refused.

4. **ARM64 codegen (very big).** macOS currently needs Rosetta on Apple
   Silicon; Linux ARM and Windows ARM likewise. This is a second backend,
   not a runtime shim - a major project.

5. **Built-in ELF/Mach-O writer.** Linux/macOS builds currently shell out
   to system `as`/`objcopy`/`cc` (and zig for mac). A native object writer
   (like the existing PE writer in `pe.go`) removes that dependency. Does
   not change anything above the build files.

## Gotchas learned

- clang's Mach-O assembler treats a leading `#` as a preprocessor
  directive, not a comment - `transformMacAsm` strips comments on
  non-string lines. Mach-O symbols carry a leading underscore; `.align` is
  log2 (use `.balign`); sections are `__TEXT,__const` / `__DATA,__bss`.
- Veyl strings are NUL-terminated bytes, so binary HTTP bodies with a zero
  byte truncate (documented limitation). Build strings by pushing chunks
  to a list and `join`-ing once, not `+=` in a loop (quadratic).
- An empty list literal needs a type annotation: `let none: []str = []`.
  List length is `len(xs)`, not `xs.len`.
- `net.recv(sock)` takes one arg and returns "" on clean close.
- Color model: DIB pixel = 0x00RRGGBB top-down; COLORREF = 0x00BBGGRR;
  Windows AlphaBlend uses PREMULTIPLIED source with rounded /255
  (`(x+127)/255`), not a lerp.
- zig cross-compiling for macOS has no SDK stubs for system dylibs like
  libsqlite3 - dlopen them at runtime instead of linking `-l`.
- Never leave a stray `.o` in `linuxrt/`; runtime objects are cached in
  the user cache dir, not the source tree.
