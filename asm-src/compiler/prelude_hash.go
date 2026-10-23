package main

// sha256, sha1 and md5, in Scandiacus.
//
// Standard implementations over 32-bit words. Scandiacus ints are signed and
// 64-bit, so every step masks back to 32 bits. The right shifts are safe
// because a masked value is never negative.

const preludeHash = `
fn __vy_m32(x: int) -> int { return x & 0xFFFFFFFF }

fn __vy_rotr32(x: int, n: int) -> int {
    return __vy_m32((x >> n) | (x << (32 - n)))
}

fn __vy_rotl32(x: int, n: int) -> int {
    return __vy_m32((x << n) | (x >> (32 - n)))
}

// Pad to a multiple of 64 bytes: a 0x80 byte, zeros, then the bit length.
// sha writes that length big-endian, md5 little.
fn __vy_padMsg(b: bytes, big: bool) -> bytes {
    let bits = len(b) * 8
    let total = len(b) + 1
    while total % 64 != 56 { total = total + 1 }

    let out = __bytesMake(total + 8)
    let i = 0
    while i < len(b) {
        __bytePut(out, i, __byteAt(b, i))
        i = i + 1
    }
    __bytePut(out, len(b), 128)
    i = len(b) + 1
    while i < total {
        __bytePut(out, i, 0)
        i = i + 1
    }

    let k = 0
    while k < 8 {
        let shift = 8 * (7 - k)
        if !big { shift = 8 * k }
        __bytePut(out, total + k, (bits >> shift) & 255)
        k = k + 1
    }
    return out
}

// The state words out as bytes.
fn __vy_wordsToBytes(h: []int, n: int, big: bool) -> bytes {
    let out = __bytesMake(n * 4)
    let i = 0
    while i < n {
        let k = 0
        while k < 4 {
            let shift = 8 * (3 - k)
            if !big { shift = 8 * k }
            __bytePut(out, i * 4 + k, (h[i] >> shift) & 255)
            k = k + 1
        }
        i = i + 1
    }
    return out
}

fn __vy_sha256K() -> []int {
    return [
        1116352408, 1899447441, 3049323471, 3921009573, 961987163, 1508970993, 2453635748, 2870763221,
        3624381080, 310598401, 607225278, 1426881987, 1925078388, 2162078206, 2614888103, 3248222580,
        3835390401, 4022224774, 264347078, 604807628, 770255983, 1249150122, 1555081692, 1996064986,
        2554220882, 2821834349, 2952996808, 3210313671, 3336571891, 3584528711, 113926993, 338241895,
        666307205, 773529912, 1294757372, 1396182291, 1695183700, 1986661051, 2177026350, 2456956037,
        2730485921, 2820302411, 3259730800, 3345764771, 3516065817, 3600352804, 4094571909, 275423344,
        430227734, 506948616, 659060556, 883997877, 958139571, 1322822218, 1537002063, 1747873779,
        1955562222, 2024104815, 2227730452, 2361852424, 2428436474, 2756734187, 3204031479, 3329325298
    ]
}

fn __vy_sha256(input: bytes) -> bytes {
    let k = __vy_sha256K()
    let h: []int = [
        1779033703, 3144134277, 1013904242, 2773480762, 1359893119, 2600822924, 528734635, 1541459225
    ]

    let msg = __vy_padMsg(input, true)
    let w: []int = []
    let i = 0
    while i < 64 {
        push(w, 0)
        i = i + 1
    }

    let at = 0
    while at < len(msg) {
        let t = 0
        while t < 16 {
            w[t] = (__byteAt(msg, at + t * 4) << 24) | (__byteAt(msg, at + t * 4 + 1) << 16) | (__byteAt(msg, at + t * 4 + 2) << 8) | __byteAt(msg, at + t * 4 + 3)
            t = t + 1
        }
        t = 16
        while t < 64 {
            let s0 = __vy_rotr32(w[t - 15], 7) ^ __vy_rotr32(w[t - 15], 18) ^ (w[t - 15] >> 3)
            let s1 = __vy_rotr32(w[t - 2], 17) ^ __vy_rotr32(w[t - 2], 19) ^ (w[t - 2] >> 10)
            w[t] = __vy_m32(w[t - 16] + s0 + w[t - 7] + s1)
            t = t + 1
        }

        let a = h[0]
        let b = h[1]
        let c = h[2]
        let d = h[3]
        let e = h[4]
        let f = h[5]
        let g = h[6]
        let hh = h[7]

        t = 0
        while t < 64 {
            let S1 = __vy_rotr32(e, 6) ^ __vy_rotr32(e, 11) ^ __vy_rotr32(e, 25)
            let ch = (e & f) ^ (__vy_m32(~e) & g)
            let t1 = __vy_m32(hh + S1 + ch + k[t] + w[t])
            let S0 = __vy_rotr32(a, 2) ^ __vy_rotr32(a, 13) ^ __vy_rotr32(a, 22)
            let maj = (a & b) ^ (a & c) ^ (b & c)
            let t2 = __vy_m32(S0 + maj)

            hh = g
            g = f
            f = e
            e = __vy_m32(d + t1)
            d = c
            c = b
            b = a
            a = __vy_m32(t1 + t2)
            t = t + 1
        }

        h[0] = __vy_m32(h[0] + a)
        h[1] = __vy_m32(h[1] + b)
        h[2] = __vy_m32(h[2] + c)
        h[3] = __vy_m32(h[3] + d)
        h[4] = __vy_m32(h[4] + e)
        h[5] = __vy_m32(h[5] + f)
        h[6] = __vy_m32(h[6] + g)
        h[7] = __vy_m32(h[7] + hh)
        at = at + 64
    }

    return __vy_wordsToBytes(h, 8, true)
}

fn __vy_sha1(input: bytes) -> bytes {
    let h: []int = [1732584193, 4023233417, 2562383102, 271733878, 3285377520]
    let msg = __vy_padMsg(input, true)

    let w: []int = []
    let i = 0
    while i < 80 {
        push(w, 0)
        i = i + 1
    }

    let at = 0
    while at < len(msg) {
        let t = 0
        while t < 16 {
            w[t] = (__byteAt(msg, at + t * 4) << 24) | (__byteAt(msg, at + t * 4 + 1) << 16) | (__byteAt(msg, at + t * 4 + 2) << 8) | __byteAt(msg, at + t * 4 + 3)
            t = t + 1
        }
        t = 16
        while t < 80 {
            w[t] = __vy_rotl32(w[t - 3] ^ w[t - 8] ^ w[t - 14] ^ w[t - 16], 1)
            t = t + 1
        }

        let a = h[0]
        let b = h[1]
        let c = h[2]
        let d = h[3]
        let e = h[4]

        t = 0
        while t < 80 {
            let f = 0
            let kk = 0
            if t < 20 {
                f = (b & c) | (__vy_m32(~b) & d)
                kk = 1518500249
            } else {
                if t < 40 {
                    f = b ^ c ^ d
                    kk = 1859775393
                } else {
                    if t < 60 {
                        f = (b & c) | (b & d) | (c & d)
                        kk = 2400959708
                    } else {
                        f = b ^ c ^ d
                        kk = 3395469782
                    }
                }
            }
            let tmp = __vy_m32(__vy_rotl32(a, 5) + f + e + kk + w[t])
            e = d
            d = c
            c = __vy_rotl32(b, 30)
            b = a
            a = tmp
            t = t + 1
        }

        h[0] = __vy_m32(h[0] + a)
        h[1] = __vy_m32(h[1] + b)
        h[2] = __vy_m32(h[2] + c)
        h[3] = __vy_m32(h[3] + d)
        h[4] = __vy_m32(h[4] + e)
        at = at + 64
    }

    return __vy_wordsToBytes(h, 5, true)
}

fn __vy_md5K() -> []int {
    return [
        3614090360, 3905402710, 606105819, 3250441966, 4118548399, 1200080426, 2821735955, 4249261313,
        1770035416, 2336552879, 4294925233, 2304563134, 1804603682, 4254626195, 2792965006, 1236535329,
        4129170786, 3225465664, 643717713, 3921069994, 3593408605, 38016083, 3634488961, 3889429448,
        568446438, 3275163606, 4107603335, 1163531501, 2850285829, 4243563512, 1735328473, 2368359562,
        4294588738, 2272392833, 1839030562, 4259657740, 2763975236, 1272893353, 4139469664, 3200236656,
        681279174, 3936430074, 3572445317, 76029189, 3654602809, 3873151461, 530742520, 3299628645,
        4096336452, 1126891415, 2878612391, 4237533241, 1700485571, 2399980690, 4293915773, 2240044497,
        1873313359, 4264355552, 2734768916, 1309151649, 4149444226, 3174756917, 718787259, 3951481745
    ]
}

fn __vy_md5Shift() -> []int {
    return [
        7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22,
        5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20,
        4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23,
        6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21
    ]
}

fn __vy_md5(input: bytes) -> bytes {
    let k = __vy_md5K()
    let s = __vy_md5Shift()
    let h: []int = [1732584193, 4023233417, 2562383102, 271733878]
    let msg = __vy_padMsg(input, false)

    let m: []int = []
    let i = 0
    while i < 16 {
        push(m, 0)
        i = i + 1
    }

    let at = 0
    while at < len(msg) {
        let t = 0
        while t < 16 {
            m[t] = __byteAt(msg, at + t * 4) | (__byteAt(msg, at + t * 4 + 1) << 8) | (__byteAt(msg, at + t * 4 + 2) << 16) | (__byteAt(msg, at + t * 4 + 3) << 24)
            t = t + 1
        }

        let a = h[0]
        let b = h[1]
        let c = h[2]
        let d = h[3]

        t = 0
        while t < 64 {
            let f = 0
            let g = 0
            if t < 16 {
                f = (b & c) | (__vy_m32(~b) & d)
                g = t
            } else {
                if t < 32 {
                    f = (d & b) | (__vy_m32(~d) & c)
                    g = (5 * t + 1) % 16
                } else {
                    if t < 48 {
                        f = b ^ c ^ d
                        g = (3 * t + 5) % 16
                    } else {
                        f = c ^ (b | __vy_m32(~d))
                        g = (7 * t) % 16
                    }
                }
            }
            let tmp = d
            d = c
            c = b
            let sum = __vy_m32(a + f + k[t] + m[g])
            b = __vy_m32(b + __vy_rotl32(sum, s[t]))
            a = tmp
            t = t + 1
        }

        h[0] = __vy_m32(h[0] + a)
        h[1] = __vy_m32(h[1] + b)
        h[2] = __vy_m32(h[2] + c)
        h[3] = __vy_m32(h[3] + d)
        at = at + 64
    }

    return __vy_wordsToBytes(h, 4, false)
}
`
