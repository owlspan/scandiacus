# Veyl for VS Code

Syntax highlighting for `.vl` files: keywords, types, strings with
interpolation, the dotted libraries, the bare builtins, and the `?`/`!`
type markers.

The Windows installer puts this in place for you if the VS Code
component is ticked. The steps below are for doing it by hand.

---

## Installing it

There is no marketplace listing. veyl installs it through VS Code's own
command line:

```
veyl vscode editors/vscode
```

That packs this folder as a `.vsix` and runs `code --install-extension`
on it. When `code` is not on PATH it says so and leaves the package in
the temp folder; or write it somewhere yourself and install it from VS
Code with **Extensions: Install from VSIX...**:

```
veyl vscode --vsix veyl-lang-0.64.0.vsix editors/vscode
```

Copying the folder into `~/.vscode/extensions` no longer works. VS Code
keeps a registry, `extensions.json`, and does not load a folder that is
not in it, which is why highlighting disappeared for anyone with another
extension installed. `veyl vscode` also removes those old copies.

Reload VS Code, open a `.vl` file, and the language indicator in the
status bar should read **Veyl**. If it does not, run **Developer:
Inspect Editor Tokens and Scopes** and put the cursor on a keyword; the
scope should start with `source.veyl`.

`veyl vscode --uninstall` removes it.

---

## What it does and does not do

**Does:** highlighting, comment toggling, bracket matching and
auto-closing, 4-space indentation, and it strips trailing whitespace and
adds a final newline on save.

Through `veyl lsp`, the language server built into veyl:

- **errors and warnings as you type**, the same ones `veyl build`
  reports;
- **completion** of names in scope and the library, and after a dot a
  struct's fields and methods, a library's functions or an enum's
  variants;
- **go to definition** (F12) for functions, methods, structs, enums,
  fields, globals and locals, into imported files too;
- **hover** for the type of a variable or the signature of a function;
- **the outline** of a file (Ctrl+Shift+O);
- **Format Document** (Shift+Alt+F), the same layout as `veyl fmt`.

The extension starts the veyl that installed it. To use another, set
**Veyl: Path** (`veyl.path`) in the settings. If veyl cannot be started
the highlighting still works, and a message says so.

The client is `extension.js`, plain JavaScript with no npm packages;
`test/` drives it against a real `veyl lsp` (TestVSCodeClient).

**Does not yet:** rename, find references, or signature help.

Reserved-but-unimplemented words - `own` and `unsafe` - are
highlighted as errors on purpose. The compiler will refuse them, and
finding that out from the editor beats finding out from a build.

---

## Keeping the builtin list honest

The grammar hard-codes the builtin names, and a hard-coded list drifts.
The real list is the `sigs` table in `asm-src/compiler/library.go`
plus the prelude names in `asm-src/compiler/prelude.go`. After adding a
builtin, update the `builtins` and `libraries` patterns in
`syntaxes/veyl.tmLanguage.json` to match.
