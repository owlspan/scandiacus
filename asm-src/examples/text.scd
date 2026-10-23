// Text: counting, padding, parsing numbers, and indexing by character.
//
// The accented letters are built from their UTF-8 bytes so this file
// stays plain ASCII. In a program of your own, "\u{e9}" says the same
// thing.

let e = bytes.str(must(bytes.ofList([195, 169])))
let word = "caf" + e + "s"

// len counts bytes; everything that takes an index counts characters.
print(len(word))
print(len(chars(word)))
print(chars(word))
print(charAt(word, 3))
print(substr(word, 2, 5))
print(substr(word, -3, 99))
print(charAt(word, 42) == "")

// A byte that does not start a character reads as U+FFFD, as it does
// when Go turns a string into runes.
let broken = "a" + bytes.str(must(bytes.ofList([200]))) + "b"
print(len(chars(broken)))
print(len(charAt(broken, 1)))

print(count("cheese", "e"))
print(count("aaaa", "aa"))
print(count(word, ""))

print(padLeft("7", 3, "0"))
print(padLeft("left", 8) + "|")
print(padRight("right", 8) + "|")
print(padRight(e, 3, ".") + "|")

// Numbers out of text. toFloat never fails - it takes the fallback -
// so isFloat is how to tell.
for s in ["3.25", " -1e3 ", "1_000.5", ".5", "5.", "1e", "abc", "1e400", "inf", "-Infinity", "nan"] {
    print("{s} -> {isFloat(s)} {toFloat(s, -1.0)}")
}
print(toFloat("2.5") + toFloat("x"))

// floor, ceil, round and trunc give ints.
let n: int = floor(3.7)
print(n)
print("{ceil(3.2)} {round(2.5)} {round(-2.5)} {trunc(-3.9)} {floor(-0.5)}")

// The special floats, and the comparisons that have to treat NaN right.
print("{INF} {-INF} {NAN}")
print(NAN == NAN)
print(NAN != NAN)
print(NAN < 1.0 || NAN >= 1.0)
print(isNan(NAN))
print(INF > 1e308)

// With no input attached, input reads the end of it and gets "".
print("[" + input() + "]")

// Strings order byte by byte, so "B" comes before "a".
let words = ["b", "a", "ab", "", "B", "abc", "abd"]
for x in words {
    let line = ""
    for y in words {
        let r = 0
        if x < y {
            r += 1
        }
        if x > y {
            r += 2
        }
        if x <= y {
            r += 4
        }
        if x >= y {
            r += 8
        }
        line = line + str(r)
    }
    print(line)
}
