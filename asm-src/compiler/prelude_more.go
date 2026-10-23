package main

// bits, args and url, in Scandiacus.
//
// All three are pure computation over ints and strings, which is exactly
// what the prelude is for. The only thing here that needs the operating
// system is the command line, and that is one call.
//
// The bit functions are written against Go's math/bits, which works on
// uint64 where Scandiacus's int is signed. Every place that matters, the shift
// is followed by a mask: `>>` here is arithmetic, so a negative value
// shifted right brings ones down from the top, and the mask is what
// turns that back into the logical shift these functions mean.

const preludeBits2 = `
// Go's bits.OnesCount64, the SWAR version: add adjacent pairs, then
// nibbles, then bytes, and read the total out of the top byte with one
// multiply. The first shift is the only one that can see a sign bit,
// and the mask beside it removes it.
fn __vy_bitsCount(n: int) -> int {
    let x = n
    x = (x & 0x5555555555555555) + ((x >> 1) & 0x5555555555555555)
    x = (x & 0x3333333333333333) + ((x >> 2) & 0x3333333333333333)
    x = (x + (x >> 4)) & 0x0F0F0F0F0F0F0F0F
    x = x * 0x0101010101010101
    return (x >> 56) & 0xFF
}

// bits.Len64: how many bits are needed to hold the value, reading it as
// unsigned. A negative int has bit 63 set, so it needs all 64.
fn __vy_bitsLength(n: int) -> int {
    if n < 0 { return 64 }
    let x = n
    let count = 0
    while x != 0 {
        count = count + 1
        x = x >> 1
    }
    return count
}

fn __vy_bitsLeading(n: int) -> int {
    return 64 - __vy_bitsLength(n)
}

fn __vy_bitsTrailing(n: int) -> int {
    if n == 0 { return 64 }
    let x = n
    let count = 0
    while (x & 1) == 0 {
        count = count + 1
        x = x >> 1
    }
    return count
}

// bits.Reverse32, then widened back to an int - so the answer is always
// in 0 .. 2**32-1 and never negative, matching Go's uint32-to-int.
fn __vy_bitsReverse(n: int) -> int {
    let x = n & 0xFFFFFFFF
    let out = 0
    let i = 0
    while i < 32 {
        out = (out << 1) | ((x >> i) & 1)
        i = i + 1
    }
    return out
}

// bits.RotateLeft32. The count is taken modulo 32 the way Go takes it,
// by masking, so a negative rotation is a rotation the other way.
fn __vy_bitsRotate(n: int, by: int) -> int {
    let x = n & 0xFFFFFFFF
    let s = by & 31
    if s == 0 { return x }
    return ((x << s) | (x >> (32 - s))) & 0xFFFFFFFF
}

fn __vy_baseDigits() -> str {
    return "0123456789abcdefghijklmnopqrstuvwxyz"
}

// strconv.FormatInt. A base outside 2..36 gives the empty string rather
// than a panic, which is what the Go backend's wrapper does.
fn __vy_toBase(n: int, base: int) -> str {
    if base < 2 || base > 36 { return "" }
    if n == 0 { return "0" }

    let digits = __vy_baseDigits()
    let neg = n < 0
    let x = n
    if neg { x = -x }

    let out = ""
    while x != 0 {
        let d = x % base
        out = __substrB(digits, d, d + 1) + out
        x = x / base
    }
    if neg { return "-" + out }
    return out
}

fn __vy_digitValue(c: str, base: int) -> int {
    let digits = __vy_baseDigits()
    let i = 0
    while i < base {
        if __substrB(digits, i, i + 1) == lower(c) { return i }
        i = i + 1
    }
    return -1
}

// strconv.ParseInt, with the Go backend's own two messages. Every way of
// failing gives the same one, so the reasons never have to agree beyond
// these two strings.
fn __vy_fromBase(s: str, base: int) -> int! {
    if base < 2 || base > 36 {
        return fail("base must be between 2 and 36")
    }
    let bad = s + " is not a base-" + str(base) + " number"

    let t = trim(s)
    if len(t) == 0 { return fail(bad) }

    let i = 0
    let neg = false
    if __charAtB(t, 0) == "-" {
        neg = true
        i = 1
    }
    if __charAtB(t, 0) == "+" { i = 1 }
    if i >= len(t) { return fail(bad) }

    let n = 0
    while i < len(t) {
        let d = __vy_digitValue(__charAtB(t, i), base)
        if d < 0 { return fail(bad) }
        n = n * base + d
        i = i + 1
    }
    if neg { return -n }
    return n
}
`

const preludeArgs = `
// The command line.
//
// A program built here has no C runtime startup, so there is no argv to
// read - __cmdline asks Windows for the raw string and this splits it
// the way the runtime would have: double quotes group, and a quote
// inside a quoted run ends it.

fn __vy_osArgs() -> []str {
    let raw = __cmdline()
    let out: []str = []
    let cur = ""
    let inQuotes = false
    let started = false

    let i = 0
    while i < len(raw) {
        let c = __charAtB(raw, i)
        if c == "\"" {
            inQuotes = !inQuotes
            started = true
        } else {
            if c == " " && !inQuotes {
                if started { push(out, cur) }
                cur = ""
                started = false
            } else {
                cur = cur + c
                started = true
            }
        }
        i = i + 1
    }
    if started { push(out, cur) }

    // Drop the program's own name, which is what os.Args[1:] means.
    let rest: []str = []
    let k = 1
    while k < len(out) {
        push(rest, out[k])
        k = k + 1
    }
    return rest
}

fn __vy_argsFlag(name: str) -> bool {
    let args = __vy_osArgs()
    for a in args {
        if a == "--" + name || a == "-" + name { return true }
    }
    return false
}

fn __vy_argsValue(name: str, fallback: str) -> str {
    let args = __vy_osArgs()
    let i = 0
    while i < len(args) {
        let a = args[i]
        if a == "--" + name || a == "-" + name {
            if i + 1 < len(args) { return args[i + 1] }
            return fallback
        }
        if startsWith(a, "--" + name + "=") {
            return __substrB(a, len(name) + 3, len(a))
        }
        i = i + 1
    }
    return fallback
}

fn __vy_argsRest() -> []str {
    let args = __vy_osArgs()
    let out: []str = []
    let skipNext = false
    for a in args {
        if skipNext {
            skipNext = false
        } else {
            if startsWith(a, "-") {
                if !contains(a, "=") { skipNext = true }
            } else {
                push(out, a)
            }
        }
    }
    return out
}
`

const preludeURL = `
// A URL parser, covering what net/url reports for the parts this
// library exposes. It is not a general implementation of RFC 3986: it
// handles the shapes a program actually writes, and says so by failing
// rather than guessing on anything else.

fn __vy_schemeOf(raw: str) -> str {
    let i = 0
    while i < len(raw) {
        let c = __charAtB(raw, i)
        if c == ":" {
            if i == 0 { return "" }
            return lower(__substrB(raw, 0, i))
        }
        if c == "/" || c == "?" || c == "#" { return "" }
        i = i + 1
    }
    return ""
}

// Everything between the // and the next /, ? or #.
fn __vy_urlAuthority(raw: str) -> str {
    let scheme = __vy_schemeOf(raw)
    let at = 0
    if len(scheme) > 0 { at = len(scheme) + 1 }
    if at + 2 > len(raw) { return "" }
    if __substrB(raw, at, at + 2) != "//" { return "" }
    at = at + 2

    let i = at
    while i < len(raw) {
        let c = __charAtB(raw, i)
        if c == "/" || c == "?" || c == "#" { return __substrB(raw, at, i) }
        i = i + 1
    }
    return __substrB(raw, at, len(raw))
}

// Userinfo is stripped, as Hostname() strips it.
fn __vy_urlHostPort(raw: str) -> str {
    let a = __vy_urlAuthority(raw)
    let at = indexOf(a, "@")
    if at >= 0 { return __substrB(a, at + 1, len(a)) }
    return a
}

fn __vy_hostOf(raw: str) -> str {
    let hp = __vy_urlHostPort(raw)
    let colon = __vy_lastColon(hp)
    if colon < 0 { return hp }
    if !__vy_allDigits(__substrB(hp, colon + 1, len(hp))) { return hp }
    return __substrB(hp, 0, colon)
}

fn __vy_portOf(raw: str) -> str {
    let hp = __vy_urlHostPort(raw)
    let colon = __vy_lastColon(hp)
    if colon < 0 { return "" }
    let tail = __substrB(hp, colon + 1, len(hp))
    if !__vy_allDigits(tail) { return "" }
    return tail
}

fn __vy_lastColon(s: str) -> int {
    let i = len(s) - 1
    while i >= 0 {
        if __charAtB(s, i) == ":" { return i }
        i = i - 1
    }
    return -1
}

fn __vy_allDigits(s: str) -> bool {
    if len(s) == 0 { return false }
    let i = 0
    while i < len(s) {
        if __vy_digitOf(__charAtB(s, i)) < 0 { return false }
        i = i + 1
    }
    return true
}

fn __vy_pathOf(raw: str) -> str {
    let scheme = __vy_schemeOf(raw)
    let at = 0
    if len(scheme) > 0 { at = len(scheme) + 1 }
    if at + 2 <= len(raw) && __substrB(raw, at, at + 2) == "//" {
        at = at + 2 + len(__vy_urlAuthority(raw))
    }
    let i = at
    while i < len(raw) {
        let c = __charAtB(raw, i)
        if c == "?" || c == "#" { return __vy_unescape(__substrB(raw, at, i)) }
        i = i + 1
    }
    return __vy_unescape(__substrB(raw, at, len(raw)))
}

fn __vy_urlRawQuery(raw: str) -> str {
    let q = indexOf(raw, "?")
    if q < 0 { return "" }
    let h = indexOf(raw, "#")
    if h >= 0 && h > q { return __substrB(raw, q + 1, h) }
    if h >= 0 { return "" }
    return __substrB(raw, q + 1, len(raw))
}

fn __vy_fragmentOf(raw: str) -> str {
    let h = indexOf(raw, "#")
    if h < 0 { return "" }
    return __vy_unescape(__substrB(raw, h + 1, len(raw)))
}

// Percent-decoding, with + meaning a space inside a query. Anything that
// is not a valid escape is left alone, which is what Go does for a path
// and close enough for the rest.
fn __vy_unescape(s: str) -> str {
    let out = ""
    let i = 0
    while i < len(s) {
        let c = __charAtB(s, i)
        if c == "%" && i + 2 < len(s) {
            let hi = __vy_hexVal(__charAtB(s, i + 1))
            let lo = __vy_hexVal(__charAtB(s, i + 2))
            if hi >= 0 && lo >= 0 {
                out = out + __chr(hi * 16 + lo)
                i = i + 3
            } else {
                out = out + c
                i = i + 1
            }
        } else {
            out = out + c
            i = i + 1
        }
    }
    return out
}

fn __vy_unescapeQuery(s: str) -> str {
    let swapped = replace(s, "+", " ")
    return __vy_unescape(swapped)
}

fn __vy_hexVal(c: str) -> int {
    let digits = "0123456789abcdef"
    let i = 0
    let low = lower(c)
    while i < 16 {
        if __substrB(digits, i, i + 1) == low { return i }
        i = i + 1
    }
    return -1
}


// The public spellings. net/url can fail on a malformed URL, so the Go
// backend returns a T! and a program written against it says must(...);
// these keep that shape even though this parser reports the parts of
// anything it is given.

fn __vy_urlScheme(raw: str) -> str! {
    return __vy_schemeOf(raw)
}

fn __vy_urlHost(raw: str) -> str! {
    return __vy_hostOf(raw)
}

fn __vy_urlPort(raw: str) -> str! {
    return __vy_portOf(raw)
}

fn __vy_urlPath(raw: str) -> str! {
    return __vy_pathOf(raw)
}

fn __vy_urlFragment(raw: str) -> str! {
    return __vy_fragmentOf(raw)
}

fn __vy_urlQuery(raw: str) -> {str: str}! {
    let out: {str: str} = {}
    let q = __vy_urlRawQuery(raw)
    if len(q) == 0 { return out }

    let parts = split(q, "&")
    for p in parts {
        if len(p) > 0 {
            let eq = indexOf(p, "=")
            let k = p
            let v = ""
            if eq >= 0 {
                k = __substrB(p, 0, eq)
                v = __substrB(p, eq + 1, len(p))
            }
            let key = __vy_unescapeQuery(k)
            // Repeated keys keep the first value, which is what the Go
            // backend does when it flattens the multi-valued map.
            if !has(out, key) { out[key] = __vy_unescapeQuery(v) }
        }
    }
    return out
}

// Reference resolution. Enough of RFC 3986 for the shapes that come up:
// an absolute reference, a network-relative one, a rooted path and a
// relative path, with dot segments removed.
// url.join is Go's URL.ResolveReference, step for step, since that is
// what the http client's redirects and every Go program agree on: the
// reference's parts replace the base's from the first one it has, and
// the path is merged and its dot segments removed by resolvePath. The
// old version lost the trailing slash of ".", ".." and "g/", and gave
// something else for an empty reference.
fn __vy_urlJoin(base: str, ref: str) -> str! {
    let b = __vy_urlParts(base)
    let r = __vy_urlParts(ref)
    if r[0] != "" {
        // An opaque reference, mailto:x or g:h, is whole as it is.
        if r[2] == "" && !startsWith(r[3], "/") && r[3] != "" { return ref }
        return __vy_urlFormat(r[0], r[2], r[1], __vy_resolvePath(__vy_escapedPath(r[3]), ""), r[4], r[5], r[6], r[7])
    }
    if b[0] == "" { return fail("cannot resolve against a URL with no scheme") }
    if r[2] != "" {
        return __vy_urlFormat(b[0], r[2], r[1], __vy_resolvePath(__vy_escapedPath(r[3]), ""), r[4], r[5], r[6], r[7])
    }
    let hasQuery = r[4]
    let query = r[5]
    let hasFrag = r[6]
    let frag = r[7]
    if r[3] == "" && r[4] == "" {
        hasQuery = b[4]
        query = b[5]
        if r[6] == "" {
            hasFrag = b[6]
            frag = b[7]
        }
    }
    let path = __vy_resolvePath(__vy_escapedPath(b[3]), __vy_escapedPath(r[3]))
    return __vy_urlFormat(b[0], b[2], b[1], path, hasQuery, query, hasFrag, frag)
}

// A URL's parts as written, nothing decoded: scheme, authority, whether
// there is one, path, whether there is a query, the query, whether
// there is a fragment, the fragment. Flags are "1" or "".
fn __vy_urlParts(raw: str) -> []str {
    let out: []str = ["", "", "", "", "", "", "", ""]
    let rest = raw
    let h = indexOf(rest, "#")
    if h >= 0 {
        out[6] = "1"
        out[7] = __substrB(rest, h + 1, len(rest))
        rest = __substrB(rest, 0, h)
    }
    let q = indexOf(rest, "?")
    if q >= 0 {
        out[4] = "1"
        out[5] = __substrB(rest, q + 1, len(rest))
        rest = __substrB(rest, 0, q)
    }
    let scheme = __vy_schemeOf(rest)
    if scheme != "" {
        out[0] = scheme
        rest = __substrB(rest, len(scheme) + 1, len(rest))
    }
    // As in Go, a reference with no scheme that starts with three
    // slashes is all path rather than an empty authority.
    if startsWith(rest, "//") && (scheme != "" || !startsWith(rest, "///")) {
        rest = __substrB(rest, 2, len(rest))
        let slash = indexOf(rest, "/")
        out[2] = "1"
        if slash < 0 {
            out[1] = rest
            rest = ""
        } else {
            out[1] = __substrB(rest, 0, slash)
            rest = __substrB(rest, slash, len(rest))
        }
    }
    out[3] = rest
    return out
}

fn __vy_urlFormat(scheme: str, hasAuth: str, auth: str, path: str, hasQuery: str, query: str, hasFrag: str, frag: str) -> str {
    let out = scheme + ":"
    if hasAuth != "" {
        out = out + "//" + auth
        if path != "" && !startsWith(path, "/") { out = out + "/" }
    }
    out = out + path
    if hasQuery != "" { out = out + "?" + query }
    if hasFrag != "" && frag != "" { out = out + "#" + frag }
    return out
}

// The bytes a path may hold as they are: Go's shouldEscape for a path.
fn __vy_pathSafe(c: int) -> bool {
    if (c >= 97 && c <= 122) || (c >= 65 && c <= 90) || (c >= 48 && c <= 57) { return true }
    return c == 45 || c == 95 || c == 46 || c == 126 || c == 36 || c == 38 || c == 43 || c == 44 || c == 47 || c == 58 || c == 59 || c == 61 || c == 64
}

// A path as Go's EscapedPath gives it: as written when it is validly
// encoded, otherwise decoded and encoded again.
fn __vy_escapedPath(p: str) -> str {
    let valid = true
    let i = 0
    while i < len(p) {
        let c = __strByte(p, i)
        let extra = c == 33 || c == 39 || c == 40 || c == 41 || c == 42 || c == 91 || c == 93 || c == 37
        if !__vy_pathSafe(c) && !extra { valid = false }
        i = i + 1
    }
    if valid { return p }
    let raw = __vy_unescape(p)
    let out = ""
    i = 0
    while i < len(raw) {
        let c = __strByte(raw, i)
        if __vy_pathSafe(c) {
            out = out + __charAtB(raw, i)
        } else {
            out = out + "%" + upper(padLeft(bits.toBase(c, 16), 2, "0"))
        }
        i = i + 1
    }
    return out
}

// Go's resolvePath: base's directory and ref merged, dot segments
// removed, and a trailing . or .. leaving its slash.
fn __vy_resolvePath(base: str, ref: str) -> str {
    let full = ""
    if ref == "" {
        full = base
    } else if !startsWith(ref, "/") {
        let i = len(base) - 1
        while i >= 0 && __strByte(base, i) != 47 {
            i = i - 1
        }
        full = __substrB(base, 0, i + 1) + ref
    } else {
        full = ref
    }
    if full == "" { return "" }
    let dst = "/"
    let first = true
    let elem = ""
    for e in split(full, "/") {
        elem = e
        if e == "." {
            first = false
        } else if e == ".." {
            let str0 = __substrB(dst, 1, len(dst))
            let at = len(str0) - 1
            while at >= 0 && __strByte(str0, at) != 47 {
                at = at - 1
            }
            if at < 0 {
                dst = "/"
                first = true
            } else {
                dst = "/" + __substrB(str0, 0, at)
            }
        } else {
            if !first { dst = dst + "/" }
            dst = dst + e
            first = false
        }
    }
    if elem == "." || elem == ".." { dst = dst + "/" }
    if len(dst) > 1 && __strByte(dst, 1) == 47 { return __substrB(dst, 1, len(dst)) }
    return dst
}
`

// os.dir.home, os.dir.temp and os.path.clean, in Scandiacus. None of them
// needs Win32: two are an environment variable and the third is string
// work.
const preludeOsMore = `
fn __vy_dirHome() -> str {
    let h = os.env.get("USERPROFILE")
    if h != "" { return h }
    // A service account may have neither, in which case there is no
    // home directory to report rather than a wrong one.
    return os.env.get("HOME")
}

fn __vy_dirTemp() -> str {
    let t = os.env.get("TEMP")
    if t != "" { return t }
    t = os.env.get("TMP")
    if t != "" { return t }
    return "C:\\Windows\\Temp"
}

// Collapse separators, drop "." segments, and resolve ".." against what
// came before it. Purely textual: it never touches the disk, so it does
// not follow links and does not care whether the path exists.
fn __vy_pathClean(p: str) -> str {
    if p == "" { return "." }

    let rooted = false
    let prefix = ""
    let rest = p

    // Keep a drive letter or a leading separator, so cleaning does not
    // turn an absolute path into a relative one.
    if len(p) >= 2 && __strAt(p, 1) == 58 {
        prefix = __substrB(p, 0, 2)
        rest = __substrB(p, 2, len(p))
    }
    if len(rest) > 0 && (__strAt(rest, 0) == 47 || __strAt(rest, 0) == 92) {
        rooted = true
    }

    let parts: []str = []
    let cur = ""
    let i = 0
    while i <= len(rest) {
        let atEnd = i == len(rest)
        let c = 0
        if !atEnd { c = __strAt(rest, i) }
        if atEnd || c == 47 || c == 92 {
            if cur == "." || cur == "" {
                // nothing to add
            } else {
                if cur == ".." {
                    if len(parts) > 0 && parts[len(parts) - 1] != ".." {
                        removeAt(parts, len(parts) - 1)
                    } else {
                        if !rooted { push(parts, "..") }
                    }
                } else {
                    push(parts, cur)
                }
            }
            cur = ""
        } else {
            cur = cur + __charAtB(rest, i)
        }
        i = i + 1
    }

    let out = join(parts, "/")
    if rooted { out = "/" + out }
    out = prefix + out
    if out == "" { return "." }
    return out
}
`
