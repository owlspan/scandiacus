package main

// csv, in Veyl. Go's encoding/csv, with FieldsPerRecord at -1 so a
// ragged file is read rather than refused.
//
// The reader's error text is Go's, down to the line and column, because
// a program that prints why a file would not parse has to print the
// same sentence on both backends.

const preludeCsv = `
fn __vy_csvBareQuote() -> str { return "bare \" in non-quoted-field" }
fn __vy_csvExtraQuote() -> str { return "extraneous or missing \" in quoted-field" }

// Go's ParseError.Error, for the two errors this reader can raise.
fn __vy_csvErr(startLine: int, line: int, col: int, why: str) -> str {
    if startLine != line {
        return "cannot read csv: record on line {startLine}; parse error on line {line}, column {col}: {why}"
    }
    return "cannot read csv: parse error on line {line}, column {col}: {why}"
}

fn __vy_csvParse(text: str) -> [][]str! {
    let rows: [][]str = []
    let n = len(text)
    // A carriage return as the very last byte is dropped before parsing,
    // as Go's reader does for backwards compatibility.
    if n > 0 && __strAt(text, n - 1) == 13 {
        n = n - 1
    }
    let i = 0
    let line = 1

    while i < n {
        // A blank line makes no record. A lone \r\n counts as blank.
        if __strAt(text, i) == 10 {
            i = i + 1
            line = line + 1
            continue
        }
        if __strAt(text, i) == 13 && i + 1 < n && __strAt(text, i + 1) == 10 {
            i = i + 2
            line = line + 1
            continue
        }

        let startLine = line
        let row: []str = []
        let done = false

        while !done {
            let field = ""

            if __strAt(text, i) == 34 {
                // Quoted. Two quotes inside are one quote.
                i = i + 1
                let closed = false
                while !closed {
                    if i >= n {
                        // Go points one past the end for a quote that
                        // never closes.
                        return fail(__vy_csvErr(startLine, line, __vy_csvCol(text, n), __vy_csvExtraQuote()))
                    }
                    let c = __strAt(text, i)
                    if c == 34 {
                        if i + 1 < n && __strAt(text, i + 1) == 34 {
                            field = field + "\""
                            i = i + 2
                        } else {
                            i = i + 1
                            closed = true
                        }
                    } else if c == 13 && i + 1 < n && __strAt(text, i + 1) == 10 {
                        // A CR LF inside quotes is a plain line feed, however the
                        // file ends its lines, as Go reads it.
                        i = i + 1
                    } else {
                        if c == 10 { line = line + 1 }
                        field = field + __charAtB(text, i)
                        i = i + 1
                    }
                }
                // Only a comma, a line end or the end of the text may
                // follow the closing quote.
                if i < n {
                    let c = __strAt(text, i)
                    // A carriage return counts only as half of CR LF.
                    let lineEnd = c == 10 || c == 13 && i + 1 < n && __strAt(text, i + 1) == 10
                    if c != 44 && !lineEnd {
                        // the column of the closing quote, not of what
                        // wrongly follows it
                        return fail(__vy_csvErr(startLine, line, __vy_csvCol(text, i - 1), __vy_csvExtraQuote()))
                    }
                }
            } else {
                let start = i
                while i < n {
                    let c = __strAt(text, i)
                    if c == 44 || c == 10 { break }
                    if c == 13 && i + 1 < n && __strAt(text, i + 1) == 10 { break }
                    if c == 34 {
                        return fail(__vy_csvErr(startLine, line, __vy_csvCol(text, i), __vy_csvBareQuote()))
                    }
                    i = i + 1
                }
                field = __substrB(text, start, i)
            }

            push(row, field)

            if i >= n {
                done = true
            } else {
                let c = __strAt(text, i)
                if c == 44 {
                    i = i + 1
                } else {
                    if c == 13 && i + 1 < n && __strAt(text, i + 1) == 10 {
                        i = i + 2
                    } else {
                        i = i + 1
                    }
                    line = line + 1
                    done = true
                }
            }
        }

        push(rows, row)
    }

    return rows
}

// The column Go would report: one based, counted from the last line
// start rather than from the front of the text.
fn __vy_csvCol(text: str, at: int) -> int {
    let start = 0
    let i = 0
    while i < at {
        if __strAt(text, i) == 10 { start = i + 1 }
        i = i + 1
    }
    return at - start + 1
}

// Go quotes a field holding a comma, a quote or a line end, one that
// starts with a space, and the single field \.
fn __vy_csvNeedsQuotes(f: str) -> bool {
    if f == "" { return false }
    if f == "\\." { return true }
    let i = 0
    while i < len(f) {
        let c = __strAt(f, i)
        if c == 44 || c == 34 || c == 10 || c == 13 { return true }
        i = i + 1
    }
    let c0 = __strAt(f, 0)
    return c0 == 32 || c0 == 9 || c0 == 10 || c0 == 13 || c0 == 11 || c0 == 12
}

fn __vy_csvField(f: str) -> str {
    if !__vy_csvNeedsQuotes(f) { return f }
    let out = "\""
    let i = 0
    while i < len(f) {
        if __strAt(f, i) == 34 {
            out = out + "\"\""
        } else {
            out = out + __charAtB(f, i)
        }
        i = i + 1
    }
    return out + "\""
}

fn __vy_csvWrite(rows: [][]str) -> str {
    let out = ""
    let r = 0
    while r < len(rows) {
        let row = rows[r]
        let c = 0
        while c < len(row) {
            if c > 0 { out = out + "," }
            out = out + __vy_csvField(row[c])
            c = c + 1
        }
        out = out + "\n"
        r = r + 1
    }
    return out
}

fn __vy_csvRead(path: str) -> [][]str! {
    let text = os.file.read(path)?
    return __vy_csvParse(text)
}

fn __vy_csvSave(path: str, rows: [][]str) -> bool {
    return isOk(os.file.write(path, __vy_csvWrite(rows)))
}
`
