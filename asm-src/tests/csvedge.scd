// csv.parse reads as Go's encoding/csv does: CR LF inside quotes is a
// line feed, a carriage return at the very end is dropped, and after a
// closing quote only a comma or a line end may follow.
fn show(rows: [][]str) -> str {
    let out: []str = []
    for row in rows {
        let fs: []str = []
        for f in row {
            push(fs, bytes.hex(bytes.of(f)))
        }
        push(out, join(fs, "."))
    }
    return join(out, "/")
}
fn try(t: str) {
    let r = csv.parse(t)
    if isOk(r) {
        print(show(must(r)))
    } else {
        print("error")
    }
}
try("\x22a\x0d\x0ab\x22,c\x0d\x0a")
try("x,y\x0d")
try("\x22\x22\x0d\x0d bb")
try("\x22q\x22\x0d\x0anext")
try("a,\x22b\x22\x22c\x22")
