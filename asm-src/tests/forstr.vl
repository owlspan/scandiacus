// for c in s walks the characters of a string, as chars(s) gives
// them; two names are the character index and the character.
for c in "h\u{e9}y" {
    print(bytes.hex(bytes.of(c)))
}
for i, c in "ab" {
    print("{i}={c}")
}
let n = 0
for c in repeat("abcdefghij", 200) {
    if c == "a" {
        n += 1
    }
}
print(n)
let s = "x"
for c in s + "yz" {
    print(c)
}

// str() and json.encode of long collections build their text in place.
let xs: []int = []
for i in 0..3000 {
    push(xs, i)
}
let text = str(xs)
print("{len(text)} {substr(text, 0, 10)} {substr(text, len(text) - 8, len(text))}")
let copy = text
copy += "!"
print("{len(text)} {len(copy)}")
print(len(json.encode(xs)))
