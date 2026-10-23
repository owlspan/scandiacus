package main

// The bytes library, in Scandiacus.
//
// Everything here is built on three primitives from bytes.go:
// __bytesMake(n), __byteAt(b, i) and __bytePut(b, i, v). The rest is
// ordinary code, which is the point of having the prelude.

const preludeBytes = `
fn __vy_hexDigits() -> str { return "0123456789abcdef" }

fn __vy_bytesHex(b: bytes) -> str {
    let digits = __vy_hexDigits()
    let out = ""
    let i = 0
    while i < len(b) {
        let v = __byteAt(b, i)
        out = out + __substrB(digits, (v >> 4) & 15, ((v >> 4) & 15) + 1)
        out = out + __substrB(digits, v & 15, (v & 15) + 1)
        i = i + 1
    }
    return out
}

fn __vy_hexVal2(c: str) -> int {
    let digits = "0123456789abcdef"
    let low = lower(c)
    let i = 0
    while i < 16 {
        if __substrB(digits, i, i + 1) == low { return i }
        i = i + 1
    }
    return -1
}

fn __vy_bytesFromHex(s: str) -> bytes! {
    let t = trim(s)
    if len(t) % 2 != 0 { return fail("not hexadecimal: " + t) }

    let out = __bytesMake(len(t) / 2)
    let i = 0
    while i < len(t) {
        let hi = __vy_hexVal2(__substrB(t, i, i + 1))
        let lo = __vy_hexVal2(__substrB(t, i + 1, i + 2))
        if hi < 0 || lo < 0 { return fail("not hexadecimal: " + t) }
        __bytePut(out, i / 2, hi * 16 + lo)
        i = i + 2
    }
    return out
}

fn __vy_b64Alphabet() -> str {
    return "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
}

fn __vy_bytesBase64(b: bytes) -> str {
    let alpha = __vy_b64Alphabet()
    let out = ""
    let i = 0
    while i + 2 < len(b) {
        let n = (__byteAt(b, i) << 16) | (__byteAt(b, i + 1) << 8) | __byteAt(b, i + 2)
        out = out + __vy_b64Char(alpha, (n >> 18) & 63)
        out = out + __vy_b64Char(alpha, (n >> 12) & 63)
        out = out + __vy_b64Char(alpha, (n >> 6) & 63)
        out = out + __vy_b64Char(alpha, n & 63)
        i = i + 3
    }

    let left = len(b) - i
    if left == 1 {
        let n1 = __byteAt(b, i) << 16
        out = out + __vy_b64Char(alpha, (n1 >> 18) & 63)
        out = out + __vy_b64Char(alpha, (n1 >> 12) & 63)
        out = out + "=="
    }
    if left == 2 {
        let n2 = (__byteAt(b, i) << 16) | (__byteAt(b, i + 1) << 8)
        out = out + __vy_b64Char(alpha, (n2 >> 18) & 63)
        out = out + __vy_b64Char(alpha, (n2 >> 12) & 63)
        out = out + __vy_b64Char(alpha, (n2 >> 6) & 63)
        out = out + "="
    }
    return out
}

fn __vy_b64Char(alpha: str, v: int) -> str {
    return __substrB(alpha, v, v + 1)
}

fn __vy_b64Val(c: str) -> int {
    let alpha = __vy_b64Alphabet()
    let i = 0
    while i < 64 {
        if __substrB(alpha, i, i + 1) == c { return i }
        i = i + 1
    }
    return -1
}

fn __vy_bytesFromBase64(s: str) -> bytes! {
    let t = trim(s)
    if len(t) % 4 != 0 { return fail("not valid base64") }
    if len(t) == 0 { return __bytesMake(0) }

    // Padding decides how many bytes the last group carries.
    let pad = 0
    if __charAtB(t, len(t) - 1) == "=" { pad = 1 }
    if len(t) > 1 && __charAtB(t, len(t) - 2) == "=" { pad = 2 }

    let out = __bytesMake((len(t) / 4) * 3 - pad)
    let at = 0
    let i = 0
    while i < len(t) {
        let n = 0
        let k = 0
        while k < 4 {
            let c = __charAtB(t, i + k)
            let v = 0
            if c != "=" {
                v = __vy_b64Val(c)
                if v < 0 { return fail("not valid base64") }
            }
            n = (n << 6) | v
            k = k + 1
        }
        if at < len(out) { __bytePut(out, at, (n >> 16) & 255) }
        if at + 1 < len(out) { __bytePut(out, at + 1, (n >> 8) & 255) }
        if at + 2 < len(out) { __bytePut(out, at + 2, n & 255) }
        at = at + 3
        i = i + 4
    }
    return out
}

// Slicing clamps rather than failing. Reading past the end of a buffer
// is ordinary when parsing.
fn __vy_bytesSlice(b: bytes, from: int, to: int) -> bytes {
    let start = from
    let end = to
    if start < 0 { start = 0 }
    if end > len(b) { end = len(b) }
    if start >= end { return __bytesMake(0) }

    let out = __bytesMake(end - start)
    let i = 0
    while i < end - start {
        __bytePut(out, i, __byteAt(b, start + i))
        i = i + 1
    }
    return out
}

fn __vy_bytesFind(hay: bytes, needle: bytes) -> int {
    if len(needle) == 0 { return 0 }
    if len(needle) > len(hay) { return -1 }

    let i = 0
    while i + len(needle) <= len(hay) {
        let k = 0
        let same = true
        while k < len(needle) && same {
            if __byteAt(hay, i + k) != __byteAt(needle, k) { same = false }
            k = k + 1
        }
        if same { return i }
        i = i + 1
    }
    return -1
}

fn __vy_bytesList(b: bytes) -> []int {
    let out: []int = []
    let i = 0
    while i < len(b) {
        push(out, __byteAt(b, i))
        i = i + 1
    }
    return out
}

fn __vy_bytesOfList(ns: []int) -> bytes! {
    let out = __bytesMake(len(ns))
    let i = 0
    while i < len(ns) {
        if ns[i] < 0 || ns[i] > 255 {
            return fail(str(ns[i]) + " does not fit in a byte")
        }
        __bytePut(out, i, ns[i])
        i = i + 1
    }
    return out
}

fn __vy_bytesFill(n: int, value: int) -> bytes! {
    if n < 0 { return fail("cannot make a negative number of bytes") }
    if value < 0 || value > 255 {
        return fail(str(value) + " does not fit in a byte")
    }
    let out = __bytesMake(n)
    let i = 0
    while i < n {
        __bytePut(out, i, value)
        i = i + 1
    }
    return out
}

fn __vy_sizeOk(size: int) -> bool {
    return size == 1 || size == 2 || size == 4 || size == 8
}

fn __vy_bytesPutInt(n: int, size: int, big: bool) -> bytes! {
    if !__vy_sizeOk(size) {
        return fail("a size of " + str(size) + " is not 1, 2, 4 or 8")
    }
    let out = __bytesMake(size)
    let i = 0
    while i < size {
        let shift = 8 * i
        if big { shift = 8 * (size - 1 - i) }
        __bytePut(out, i, (n >> shift) & 255)
        i = i + 1
    }
    return out
}

fn __vy_bytesGetInt(b: bytes, offset: int, size: int, big: bool) -> int! {
    if !__vy_sizeOk(size) {
        return fail("a size of " + str(size) + " is not 1, 2, 4 or 8")
    }
    if offset < 0 || offset + size > len(b) {
        return fail("reading " + str(size) + " bytes at " + str(offset) + " runs past the end of " + str(len(b)) + " bytes")
    }
    let n = 0
    let i = 0
    while i < size {
        if big {
            n = (n << 8) | __byteAt(b, offset + i)
        } else {
            n = n | (__byteAt(b, offset + i) << (8 * i))
        }
        i = i + 1
    }
    return n
}

fn __vy_bytesHash(b: bytes, algorithm: str) -> str! {
    if algorithm == "sha256" { return __vy_bytesHex(__vy_sha256(b)) }
    if algorithm == "sha1" { return __vy_bytesHex(__vy_sha1(b)) }
    if algorithm == "md5" { return __vy_bytesHex(__vy_md5(b)) }
    if algorithm == "sha512" {
        return fail("sha512 is not on the assembly backend yet")
    }
    return fail("no such algorithm: " + algorithm + " (md5, sha1, sha256, sha512)")
}

`

// preludeBytesWrap gives the public builtins the arity a user writes,
// on top of the versions that take the endianness as an argument.
const preludeBytesWrap = `
fn __vy_getIntLE(b: bytes, offset: int, size: int) -> int! {
    return __vy_bytesGetInt(b, offset, size, false)
}

fn __vy_getIntBE(b: bytes, offset: int, size: int) -> int! {
    return __vy_bytesGetInt(b, offset, size, true)
}

fn __vy_putIntLE(n: int, size: int) -> bytes! {
    return __vy_bytesPutInt(n, size, false)
}

fn __vy_putIntBE(n: int, size: int) -> bytes! {
    return __vy_bytesPutInt(n, size, true)
}

fn __vy_hashSha256(s: str) -> str {
    return __vy_bytesHex(__vy_sha256(bytes.of(s)))
}

fn __vy_hashSha1(s: str) -> str {
    return __vy_bytesHex(__vy_sha1(bytes.of(s)))
}

fn __vy_hashMd5(s: str) -> str {
    return __vy_bytesHex(__vy_md5(bytes.of(s)))
}
`
