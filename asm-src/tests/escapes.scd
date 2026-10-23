// \xHH is one byte and \u{...} is a code point written out as UTF-8.
print(bytes.hex(bytes.of("caf\u{e9}")))
print(len("\u{e9}"))
print(len("\u{1F600}"))
print(len(chars("\u{1F600}!")))
print("\x41\x42\x43")
print("tab[\x09]")

// A brace from an escape is text, never the start of an interpolation.
let x = 1
print("\u{7B}x\u{7D} is {x}")
