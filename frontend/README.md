# frontend - the shared front end

The lexer, parser, AST, types and the type checker. Everything up to
but not including a decision about what to emit.

Both backends import this. `../asm-src` compiles Veyl to x86-64 and
writes the executable itself; the discontinued Go backend, which
translates Veyl to Go, lives in `src/` on the `veylgo` branch. They read
the same definition of what the language *is* rather than two copies
that drift apart.

```
frontend/          token, lexer, ast, types, parser, check
  |
  +--> asm-src/    -> x86-64 -> encoder, linker, PE writer -> .exe
  +--> src/        -> Go source -> go build -> .exe   (veylgo branch)
```

## Why it could be lifted out unchanged

None of these files ever knew which backend they fed. Moving them took
three changes:

- `pos` became `Span`, with exported fields, because the type checker
  constructs one `Widen` node and now does so from another package.
- `qual`, `keywords`, `isAlpha`, `isDigit` and `isHexDigit` were
  exported, because the formatter and the editor generator use them.
- Nothing else. Every AST node type and field was already exported.

## How the backends use it

Each backend has a `frontend.go` holding Go type aliases:

```go
type Expr = front.Expr
const PLUS = front.PLUS
```

Aliases, not new types, which is why every other file in those packages
still writes `Expr` and `PLUS` unqualified. Adding a name to the front
end means adding one line to each alias file and changing nothing else.

Regenerate them after adding an exported name; the alias files are
sorted and mechanical.

## Testing

```
go test ./...
```

`lexer_test.go` and `types_test.go` live here, with the code they test.
The backends test their own halves: the Go backend on `veylgo` runs a
golden-file suite, and so does `../asm-src`, over its `tests/` and
`examples/`.
