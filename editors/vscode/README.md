# Veyl for VS Code

Syntax highlighting for `.vl` files: keywords, types, strings with
interpolation, the dotted libraries, the bare builtins, and the `?`/`!`
type markers.

The Windows installer puts this in place for you if the VS Code
component is ticked. The steps below are for doing it by hand.

---

## Installing it

There is no marketplace listing. Copy the folder into your extensions
directory and restart VS Code:

**Windows**

```
xcopy /E /I "editors\vscode" "%USERPROFILE%\.vscode\extensions\veyl.veyl-lang-0.53.0"
```

**macOS and Linux**

```
cp -r editors/vscode ~/.vscode/extensions/veyl.veyl-lang-0.53.0
```

The folder name matters: VS Code expects `publisher.name-version`
matching `package.json`, and a folder it does not recognise can end up
ignored with no error to say why. Restart VS Code, open a `.vl` file, and the language
indicator in the status bar should read **Veyl**.

If it does not, run **Developer: Inspect Editor Tokens and Scopes** from
the command palette and put the cursor on a keyword. The scope should
start with `source.veyl`.

---

## What it does and does not do

**Does:** highlighting, comment toggling, bracket matching and
auto-closing, 4-space indentation, and it strips trailing whitespace and
adds a final newline on save.

**Does not:** completion, go-to-definition or inline errors. Those need
a language server, which does not exist yet.

Reserved-but-unimplemented words - `defer`, `own`, `unsafe` - are
highlighted as errors on purpose. The compiler will refuse them, and
finding that out from the editor beats finding out from a build.

---

## Keeping the builtin list honest

The grammar hard-codes the builtin names, and a hard-coded list drifts.
The real list is the `sigs` table in `asm-src/compiler/library.go`
plus the prelude names in `asm-src/compiler/prelude.go`. After adding a
builtin, update the `builtins` and `libraries` patterns in
`syntaxes/veyl.tmLanguage.json` to match.
