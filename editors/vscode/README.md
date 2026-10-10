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
veyl vscode --vsix veyl-lang-0.58.0.vsix editors/vscode
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

**Does not:** completion, go-to-definition or inline errors. Those need
a language server, which does not exist yet.

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
