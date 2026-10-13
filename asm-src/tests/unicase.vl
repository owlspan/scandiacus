// upper and lower past ASCII: Latin-1, Latin Extended-A, Greek and
// Cyrillic letters change case; other text is left alone. Printed as
// UTF-8 hex so the expected output stays ASCII.
let words = ["\u{c9}cole \u{e9}t\u{e9}", "Stra\u{df}e", "\u{391}\u{3b8}\u{3ae}\u{3bd}\u{3b1}", "\u{41c}\u{43e}\u{441}\u{43a}\u{432}\u{430}", "\u{141}\u{f3}d\u{17a}", "\u{4e2d}\u{6587} abc", "plain ASCII"]
for w in words {
    print("{bytes.hex(bytes.of(upper(w)))} {bytes.hex(bytes.of(lower(w)))}")
}
