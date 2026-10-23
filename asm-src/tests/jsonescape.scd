// JSON strings: every \u escape decodes to UTF-8, a surrogate pair
// to its code point and a lone one to U+FFFD; encoding writes invalid
// UTF-8 as \ufffd and escapes U+2028, as Go does. Shown as hex.
fn h(s: str) -> str {
    return bytes.hex(bytes.of(s))
}
print(h(json.get(`{"k": "\u0041\u00e9\u4e2d"}`, "k")))
print(h(json.get(`{"k": "\ud83d\ude00 pair"}`, "k")))
print(h(json.get(`{"k": "\ud83d lone high"}`, "k")))
print(h(json.get(`{"k": "\ude00 lone low"}`, "k")))
print(h(json.get(`{"k": "\uD83D\uDE00\u0041"}`, "k")))
print(h(json.get(`{"k": "\b\f\n\/\\"}`, "k")))
print(json.encode("a\xffb"))
print(json.encode("\xe2\x80\xa8x"))
print(json.encode("\x08\x0c\x01<&>"))
print(json.encode("\xe4\xb8"))
