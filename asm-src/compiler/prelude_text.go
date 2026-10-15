package main

// Text builtins, in Veyl: reading a line, parsing a float, padding,
// counting, and indexing a string by character rather than by byte.
//
// substr, charAt and chars count characters on the Go backend, which
// converts the string to []rune first, and they counted bytes here, so
// anything outside ASCII came out cut in half. They walk the UTF-8 now,
// with Go's decoding rules: a byte that does not start a valid sequence
// is one character, U+FFFD, exactly as []rune(s) makes it.
//
// The prelude's own parsers - url, time, http, csv, re - keep calling
// the byte-indexed originals, __substrB and __charAtB. They only ever
// look for ASCII delimiters, and walking from the start of the string on
// every call would make them quadratic.

const preludeText = `
// How many bytes the character starting at byte p takes, or 0 when the
// byte there does not start a valid UTF-8 sequence. The ranges are
// Go's: no overlong forms, no surrogates, nothing past U+10FFFF. A read
// past the end lands on the terminator, which is never a continuation
// byte, so no length check is needed.
fn __vy_runeWidth(s: str, p: int) -> int {
    let b0 = __strByte(s, p)
    if b0 < 128 {
        return 1
    }
    if b0 < 194 || b0 > 244 {
        return 0
    }
    let b1 = __strByte(s, p + 1)
    if b0 < 224 {
        if b1 < 128 || b1 > 191 {
            return 0
        }
        return 2
    }
    let lo = 128
    let hi = 191
    if b0 == 224 {
        lo = 160
    }
    if b0 == 237 {
        hi = 159
    }
    if b0 == 240 {
        lo = 144
    }
    if b0 == 244 {
        hi = 143
    }
    if b1 < lo || b1 > hi {
        return 0
    }
    let b2 = __strByte(s, p + 2)
    if b2 < 128 || b2 > 191 {
        return 0
    }
    if b0 < 240 {
        return 3
    }
    let b3 = __strByte(s, p + 3)
    if b3 < 128 || b3 > 191 {
        return 0
    }
    return 4
}

fn __vy_runeCount(s: str) -> int {
    let n = len(s)
    let p = 0
    let c = 0
    while p < n {
        let w = __vy_runeWidth(s, p)
        if w == 0 {
            w = 1
        }
        p += w
        c += 1
    }
    return c
}

// upper and lower: ASCII through the byte loop, then the letters of
// Latin-1, Latin Extended-A, Greek and Cyrillic, which are two bytes in
// UTF-8 and stay two bytes when their case changes, so the result is
// fixed up in place. The mappings are Go's unicode.ToUpper / ToLower for
// those ranges, minus the dotted and dotless i (their partner is one
// byte). A string with no lead byte from C2 to D1 is returned as the
// ASCII pass left it.
fn __vy_upper(s: str) -> str {
    return __vy_caseFix(__upperA(s), true)
}

fn __vy_lower(s: str) -> str {
    return __vy_caseFix(__lowerA(s), false)
}

fn __vy_caseFix(s: str, up: bool) -> str {
    let n = len(s)
    let p = 0
    let found = false
    while p < n {
        let b0 = __strByte(s, p)
        if b0 >= 194 && b0 <= 209 {
            found = true
            break
        }
        p += 1
    }
    if !found {
        return s
    }
    let b = bytes.of(s)
    while p + 1 < n {
        let b0 = __byteAt(b, p)
        let b1 = __byteAt(b, p + 1)
        if b0 >= 194 && b0 <= 209 && b1 >= 128 && b1 <= 191 {
            let c = (b0 - 192) * 64 + (b1 - 128)
            let m = c
            if up {
                m = __vy_upperRune(c)
            } else {
                m = __vy_lowerRune(c)
            }
            if m != c {
                __bytePut(b, p, 192 + m / 64)
                __bytePut(b, p + 1, 128 + m % 64)
            }
            p += 2
        } else {
            p += 1
        }
    }
    return bytes.str(b)
}

fn __vy_upperRune(c: int) -> int {
    if c == 181 {
        return 924
    }
    if c >= 224 && c <= 254 && c != 247 {
        return c - 32
    }
    if c == 255 {
        return 376
    }
    if c >= 256 && c <= 311 && c % 2 == 1 && c != 305 {
        return c - 1
    }
    if c >= 313 && c <= 328 && c % 2 == 0 {
        return c - 1
    }
    if c >= 330 && c <= 375 && c % 2 == 1 {
        return c - 1
    }
    if c >= 378 && c <= 382 && c % 2 == 0 {
        return c - 1
    }
    if c == 940 {
        return 902
    }
    if c >= 941 && c <= 943 {
        return c - 37
    }
    if c == 962 {
        return 931
    }
    if c >= 945 && c <= 971 {
        return c - 32
    }
    if c == 972 {
        return 908
    }
    if c == 973 || c == 974 {
        return c - 63
    }
    if c >= 1072 && c <= 1103 {
        return c - 32
    }
    if c >= 1104 && c <= 1119 {
        return c - 80
    }
    return c
}

fn __vy_lowerRune(c: int) -> int {
    if c >= 192 && c <= 222 && c != 215 {
        return c + 32
    }
    if c == 376 {
        return 255
    }
    if c >= 256 && c <= 310 && c % 2 == 0 && c != 304 {
        return c + 1
    }
    if c >= 313 && c <= 327 && c % 2 == 1 {
        return c + 1
    }
    if c >= 330 && c <= 374 && c % 2 == 0 {
        return c + 1
    }
    if c >= 377 && c <= 381 && c % 2 == 1 {
        return c + 1
    }
    if c == 902 {
        return 940
    }
    if c >= 904 && c <= 906 {
        return c + 37
    }
    if c == 908 {
        return 972
    }
    if c == 910 || c == 911 {
        return c + 63
    }
    if c >= 913 && c <= 939 && c != 930 {
        return c + 32
    }
    if c >= 1040 && c <= 1071 {
        return c + 32
    }
    if c >= 1024 && c <= 1039 {
        return c + 80
    }
    return c
}

// fixed(x, places). The places are held to 0..40, and an infinity or a
// NaN is written the way str writes it rather than as the C runtime's.
//
// The rounding is done here, half to even, on the exact decimal
// expansion: a double has at most 1074 digits after the point, and every
// C runtime prints them exactly when asked, but they round an exact tie
// differently - UCRT writes -0.5 to no places as -1, glibc and Go as -0.
fn __vy_fixed(x: float, places: int) -> str {
    if ((__bits(x) >> 52) & 2047) == 2047 {
        return str(x)
    }
    let p = places
    if p < 0 {
        p = 0
    }
    if p > 40 {
        p = 40
    }
    let raw = __fixedRaw(__bits(x), p + 1100)
    let sign = ""
    if startsWith(raw, "-") {
        sign = "-"
        raw = __substrB(raw, 1, len(raw))
    }
    let dot = indexOf(raw, ".")
    let digits = __substrB(raw, 0, dot) + __substrB(raw, dot + 1, dot + 1 + p)
    let rest = __substrB(raw, dot + 1 + p, len(raw))
    let up = false
    let first = __strByte(rest, 0)
    if first > 53 {
        up = true
    } else if first == 53 {
        let i = 1
        while i < len(rest) && __strByte(rest, i) == 48 {
            i += 1
        }
        if i < len(rest) {
            up = true
        } else {
            up = (__strByte(digits, len(digits) - 1) - 48) % 2 == 1
        }
    }
    if up {
        digits = __vy_decIncrement(digits)
    }
    let whole = __substrB(digits, 0, len(digits) - p)
    if p == 0 {
        return sign + whole
    }
    return sign + whole + "." + __substrB(digits, len(digits) - p, len(digits))
}

// One more in the last place of a string of decimal digits.
fn __vy_decIncrement(d: str) -> str {
    let b = bytes.of(d)
    let i = len(d) - 1
    while i >= 0 {
        let c = __byteAt(b, i)
        if c < 57 {
            __bytePut(b, i, c + 1)
            return bytes.str(b)
        }
        __bytePut(b, i, 48)
        i -= 1
    }
    return "1" + bytes.str(b)
}

// The width part of an interpolation's format spec: "{x:8}" pads on the
// left, "{x:<8}" on the right, and "{x:08}" with zeros after any sign.
fn __vy_fmtPad(s: str, width: int, left: bool, zero: bool) -> str {
    let n = len(chars(s))
    if n >= width {
        return s
    }
    if left {
        return s + repeat(" ", width - n)
    }
    if zero {
        if startsWith(s, "-") || startsWith(s, "+") {
            return __substrB(s, 0, 1) + repeat("0", width - n) + __substrB(s, 1, len(s))
        }
        return repeat("0", width - n) + s
    }
    return repeat(" ", width - n) + s
}

fn __vy_charAt(s: str, i: int) -> str {
    if i < 0 {
        return ""
    }
    let n = len(s)
    let p = 0
    let c = 0
    while p < n {
        let w = __vy_runeWidth(s, p)
        if c == i {
            if w == 0 {
                return "\u{FFFD}"
            }
            return __substrB(s, p, p + w)
        }
        if w == 0 {
            w = 1
        }
        p += w
        c += 1
    }
    return ""
}

// Characters start to end, with both clamped. When the range holds no
// invalid bytes, which is nearly always, it is one byte slice.
fn __vy_substr(s: str, start: int, end: int) -> str {
    let a = start
    if a < 0 {
        a = 0
    }
    if end <= a {
        return ""
    }
    let n = len(s)
    let p = 0
    let c = 0
    let from = -1
    let bad = false
    while p < n && c < end {
        if c == a {
            from = p
        }
        let w = __vy_runeWidth(s, p)
        if w == 0 {
            if from >= 0 {
                bad = true
            }
            w = 1
        }
        p += w
        c += 1
    }
    if from < 0 {
        return ""
    }
    if !bad {
        return __substrB(s, from, p)
    }
    let out = ""
    let q = from
    while q < p {
        let w = __vy_runeWidth(s, q)
        if w == 0 {
            out = out + "\u{FFFD}"
            q += 1
        } else {
            out = out + __substrB(s, q, q + w)
            q += w
        }
    }
    return out
}

fn __vy_chars(s: str) -> []str {
    let out: []str = []
    let n = len(s)
    let p = 0
    while p < n {
        let w = __vy_runeWidth(s, p)
        if w == 0 {
            push(out, "\u{FFFD}")
            p += 1
        } else {
            push(out, __substrB(s, p, p + w))
            p += w
        }
    }
    return out
}

// strings.Count: non-overlapping, and an empty needle matches between
// every pair of characters and at both ends.
fn __vy_count(s: str, sub: str) -> int {
    let m = len(sub)
    if m == 0 {
        return __vy_runeCount(s) + 1
    }
    let n = len(s)
    let found = 0
    let i = 0
    while i + m <= n {
        let j = 0
        while j < m {
            if __strByte(s, i + j) != __strByte(sub, j) {
                break
            }
            j += 1
        }
        if j == m {
            found += 1
            i += m
        } else {
            i += 1
        }
    }
    return found
}

// Width is in characters, and a fill longer than one character is laid
// down whole until the width is reached, possibly overshooting it, the
// same as the Go backend's loop.
fn __vy_padLeft(s: str, width: int, fill: str) -> str {
    let f = fill
    if f == "" {
        f = " "
    }
    let fillLen = __vy_runeCount(f)
    let have = __vy_runeCount(s)
    let out = s
    while have < width {
        out = f + out
        have += fillLen
    }
    return out
}

fn __vy_padRight(s: str, width: int, fill: str) -> str {
    let f = fill
    if f == "" {
        f = " "
    }
    let fillLen = __vy_runeCount(f)
    let have = __vy_runeCount(s)
    let out = s
    while have < width {
        out = out + f
        have += fillLen
    }
    return out
}

// One line of standard input, without its line ending, or "" at the
// end of input. bufio's ReadString('\n') then TrimRight("\r\n"), which
// is what the Go backend does.
fn __vy_readLine() -> str {
    let buf = __bytesMake(16)
    let got: []int = []
    while true {
        let b = __readByte(buf)
        if b < 0 || b == 10 {
            break
        }
        push(got, b)
    }
    let n = len(got)
    while n > 0 {
        let last = got[n - 1]
        if last != 13 && last != 10 {
            break
        }
        n -= 1
    }
    let out = __bytesMake(n)
    let i = 0
    while i < n {
        __bytePut(out, i, got[i])
        i += 1
    }
    return bytes.str(out)
}

fn __vy_input(prompt: str) -> str {
    write(prompt)
    return __vy_readLine()
}

fn __vy_pause() {
    write("Press Enter to continue...")
    __vy_readLine()
}

fn __vy_isDigitByte(b: int) -> bool {
    return b >= 48 && b <= 57
}

// The end of a run of digits starting at p. An underscore may separate
// two digits, as strconv allows, and nowhere else.
fn __vy_skipDigits(t: str, p: int, n: int) -> int {
    let q = p
    while q < n {
        let b = __strByte(t, q)
        if __vy_isDigitByte(b) {
            q += 1
        } else if b == 95 && q > p && __vy_isDigitByte(__strByte(t, q + 1)) {
            q += 1
        } else {
            break
        }
    }
    return q
}

// What strconv.ParseFloat would make of already-trimmed text: 0 if it
// is not a number, 1 for a decimal, 2 and 3 for positive and negative
// infinity, 4 for NaN. A hexadecimal mantissa with a p exponent, like
// 0x1.8p3, is a 1 too, as in Go; strtod reads that form as well.
//
// A number that has an exponent but not the digits of one, like "1e",
// is not a number, and neither is anything with text after it.
fn __vy_floatForm(t: str) -> int {
    let n = len(t)
    if n == 0 {
        return 0
    }
    let p = 0
    let neg = false
    let signed = false
    let first = __strByte(t, 0)
    if first == 43 || first == 45 {
        signed = true
        neg = first == 45
        p = 1
    }
    let word = lower(__substrB(t, p, n))
    if word == "inf" || word == "infinity" {
        if neg {
            return 3
        }
        return 2
    }
    if word == "nan" && !signed {
        return 4
    }
    if p + 1 < n && __strByte(t, p) == 48 && (__strByte(t, p + 1) == 120 || __strByte(t, p + 1) == 88) {
        return __vy_hexFloatForm(t, p + 2, n)
    }
    let start = p
    p = __vy_skipDigits(t, p, n)
    let digits = p - start
    if p < n && __strByte(t, p) == 46 {
        p += 1
        let frac = p
        p = __vy_skipDigits(t, p, n)
        digits += p - frac
    }
    if digits == 0 {
        return 0
    }
    if p < n {
        let e = __strByte(t, p)
        if e == 101 || e == 69 {
            p += 1
            if p < n {
                let sign = __strByte(t, p)
                if sign == 43 || sign == 45 {
                    p += 1
                }
            }
            let expStart = p
            p = __vy_skipDigits(t, p, n)
            if p == expStart {
                return 0
            }
        }
    }
    if p != n {
        return 0
    }
    return 1
}

// The rest of a hexadecimal float after its 0x: hex digits with an
// optional point, then the p exponent Go requires of that form.
fn __vy_hexFloatForm(t: str, p: int, n: int) -> int {
    let start = p
    p = __vy_skipHex(t, p, n)
    let digits = p - start
    if p < n && __strByte(t, p) == 46 {
        p += 1
        let frac = p
        p = __vy_skipHex(t, p, n)
        digits += p - frac
    }
    if digits == 0 || p >= n {
        return 0
    }
    let e = __strByte(t, p)
    if e != 112 && e != 80 {
        return 0
    }
    p += 1
    if p < n {
        let sign = __strByte(t, p)
        if sign == 43 || sign == 45 {
            p += 1
        }
    }
    let expStart = p
    p = __vy_skipDigits(t, p, n)
    if p == expStart || p != n {
        return 0
    }
    return 1
}

fn __vy_isHexByte(b: int) -> bool {
    return __vy_isDigitByte(b) || (b >= 97 && b <= 102) || (b >= 65 && b <= 70)
}

// skipDigits for hex digits, underscores allowed the same way.
fn __vy_skipHex(t: str, p: int, n: int) -> int {
    let q = p
    while q < n {
        let b = __strByte(t, q)
        if __vy_isHexByte(b) {
            q += 1
        } else if b == 95 && q > p && __vy_isHexByte(__strByte(t, q + 1)) {
            q += 1
        } else {
            break
        }
    }
    return q
}

// A decimal too large for a float64 is a range error in Go, so it takes
// the fallback rather than coming back as an infinity.
fn __vy_toFloat(s: str, fallback: float) -> float {
    let t = trim(s)
    let form = __vy_floatForm(t)
    if form == 0 {
        return fallback
    }
    if form == 2 {
        return __frombits(9218868437227405312)
    }
    if form == 3 {
        return __frombits(-4503599627370496)
    }
    if form == 4 {
        return __frombits(9221120237041090561)
    }
    let f = __strtod(replace(t, "_", ""))
    if ((__bits(f) >> 52) & 2047) == 2047 {
        return fallback
    }
    return f
}

fn __vy_isFloat(s: str) -> bool {
    let t = trim(s)
    let form = __vy_floatForm(t)
    if form == 0 {
        return false
    }
    if form == 1 {
        return ((__bits(__strtod(replace(t, "_", ""))) >> 52) & 2047) != 2047
    }
    return true
}

// mem.scan: the first address in [start, start+size) where the pattern
// matches, or -1. A pattern is hex bytes separated by spaces, "??" or
// "?" matching any byte - the form every disassembler and memory tool
// prints, so one copied from them works as it is.
fn __vy_memScan(start: int, size: int, pattern: str) -> int {
    return __vy_scanFrom(start, size, __vy_scanPattern(pattern))
}

// A pattern as one int a byte, -1 for a wildcard. Parsed apart from the
// search so that a caller scanning many ranges parses it once.
fn __vy_scanPattern(pattern: str) -> []int {
    let want: []int = []
    for part in split(trim(pattern), " ") {
        if part == "" {
            continue
        }
        if part == "?" || part == "??" {
            push(want, -1)
            continue
        }
        let b = bits.fromBase(part, 16)
        if failed(b) || len(part) > 2 {
            __abortStr("mem.scan: " + part + " in the pattern is not a hex byte or ??")
        }
        push(want, valueOr(b, 0))
    }
    return want
}

fn __vy_scanFrom(start: int, size: int, want: []int) -> int {
    let m = len(want)
    if m == 0 {
        return start
    }
    let i = 0
    while i + m <= size {
        let j = 0
        while j < m {
            let w = want[j]
            if w >= 0 && mem.readU8(start + i + j) != w {
                break
            }
            j += 1
        }
        if j == m {
            return start + i
        }
        i += 1
    }
    return -1
}

fn __vy_floorInt(x: float) -> int {
    return int(__vy_floor(x))
}

fn __vy_ceilInt(x: float) -> int {
    return int(__vy_ceil(x))
}

fn __vy_roundInt(x: float) -> int {
    return int(__vy_round(x))
}

fn __vy_truncInt(x: float) -> int {
    return int(__vy_trunc(x))
}
`
