// A quote that never closes, at the end of the text. When the text ends
// with a line break, Go's csv reader puts the error at the end of the
// last line - a CR LF counting as one byte - not at the start of a line
// after it.
let cases: []str = ["\"\n", "\"\r\n", " ,\",a\r\n", "\"\n\n\r\n", "\"a", "x,\"b\nc"]
for c in cases {
    let r = csv.parse(c)
    if failed(r) {
        print(errorOf(r))
    } else {
        print(len(must(r)))
    }
}
