package main

// sha512, crc32, and the string-level encodings, in Scandiacus.
//
// sha512 is the same shape as sha256 next door but over 64-bit words,
// so it needs its own padding (128 byte blocks) and its own shift
// helper. A Scandiacus int is 64 bits and signed, so the top half of the K
// table is written as negative decimals, the same way the rand table
// is. Addition wraps on its own, which is the whole reason no mask
// function appears here the way __vy_m32 does over there.

const preludeHash2 = `
// A logical right shift. Scandiacus's >> sign-extends, so the mask puts the
// top n bits back to zero.
//
// n == 0 has to be its own case: a shift count is taken mod 64, so
// 1 << 64 is 1, and the mask would come out 0 instead of all ones.
fn __vy_shr64(x: int, n: int) -> int {
    if n == 0 { return x }
    return (x >> n) & ((1 << (64 - n)) - 1)
}

fn __vy_rotr64(x: int, n: int) -> int {
    return __vy_shr64(x, n) | (x << (64 - n))
}

// Pad to a multiple of 128: a 0x80 byte, zeros, then the bit length
// big-endian in 16 bytes. The top 8 are always zero here, no message
// is anywhere near 2^64 bits.
fn __vy_padMsg128(b: bytes) -> bytes {
    let bits = len(b) * 8
    let total = len(b) + 1
    while total % 128 != 112 { total = total + 1 }

    let out = __bytesMake(total + 16)
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
        __bytePut(out, total + k, 0)
        k = k + 1
    }
    k = 0
    while k < 8 {
        __bytePut(out, total + 8 + k, __vy_shr64(bits, 8 * (7 - k)) & 255)
        k = k + 1
    }
    return out
}

fn __vy_words64ToBytes(h: []int, n: int) -> bytes {
    let out = __bytesMake(n * 8)
    let i = 0
    while i < n {
        let k = 0
        while k < 8 {
            __bytePut(out, i * 8 + k, __vy_shr64(h[i], 8 * (7 - k)) & 255)
            k = k + 1
        }
        i = i + 1
    }
    return out
}

fn __vy_sha512K() -> []int {
    return [
        4794697086780616226, 8158064640168781261, -5349999486874862801, -1606136188198331460,
        4131703408338449720, 6480981068601479193, -7908458776815382629, -6116909921290321640,
        -2880145864133508542, 1334009975649890238, 2608012711638119052, 6128411473006802146,
        8268148722764581231, -9160688886553864527, -7215885187991268811, -4495734319001033068,
        -1973867731355612462, -1171420211273849373, 1135362057144423861, 2597628984639134821,
        3308224258029322869, 5365058923640841347, 6679025012923562964, 8573033837759648693,
        -7476448914759557205, -6327057829258317296, -5763719355590565569, -4658551843659510044,
        -4116276920077217854, -3051310485924567259, 489312712824947311, 1452737877330783856,
        2861767655752347644, 3322285676063803686, 5560940570517711597, 5996557281743188959,
        7280758554555802590, 8532644243296465576, -9096487096722542874, -7894198246740708037,
        -6719396339535248540, -6333637450476146687, -4446306890439682159, -4076793802049405392,
        -3345356375505022440, -2983346525034927856, -860691631967231958, 1182934255886127544,
        1847814050463011016, 2177327727835720531, 2830643537854262169, 3796741975233480872,
        4115178125766777443, 5681478168544905931, 6601373596472566643, 7507060721942968483,
        8399075790359081724, 8693463985226723168, -8878714635349349518, -8302665154208450068,
        -8016688836872298968, -6606660893046293015, -4685533653050689259, -4147400797238176981,
        -3880063495543823972, -3348786107499101689, -1523767162380948706, -757361751448694408,
        500013540394364858, 748580250866718886, 1242879168328830382, 1977374033974150939,
        2944078676154940804, 3659926193048069267, 4368137639120453308, 4836135668995329356,
        5532061633213252278, 6448918945643986474, 6902733635092675308, 7801388544844847127
    ]
}

fn __vy_sha512(input: bytes) -> bytes {
    let k = __vy_sha512K()
    let h: []int = [
        7640891576956012808, -4942790177534073029, 4354685564936845355, -6534734903238641935,
        5840696475078001361, -7276294671716946913, 2270897969802886507, 6620516959819538809
    ]

    let msg = __vy_padMsg128(input)
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
            let base = at + t * 8
            let v = 0
            let j = 0
            while j < 8 {
                v = (v << 8) | __byteAt(msg, base + j)
                j = j + 1
            }
            w[t] = v
            t = t + 1
        }
        t = 16
        while t < 80 {
            let x = w[t - 15]
            let y = w[t - 2]
            let s0 = __vy_rotr64(x, 1) ^ __vy_rotr64(x, 8) ^ __vy_shr64(x, 7)
            let s1 = __vy_rotr64(y, 19) ^ __vy_rotr64(y, 61) ^ __vy_shr64(y, 6)
            w[t] = w[t - 16] + s0 + w[t - 7] + s1
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
        while t < 80 {
            let S1 = __vy_rotr64(e, 14) ^ __vy_rotr64(e, 18) ^ __vy_rotr64(e, 41)
            let ch = (e & f) ^ (~e & g)
            let t1 = hh + S1 + ch + k[t] + w[t]
            let S0 = __vy_rotr64(a, 28) ^ __vy_rotr64(a, 34) ^ __vy_rotr64(a, 39)
            let maj = (a & b) ^ (a & c) ^ (b & c)
            let t2 = S0 + maj

            hh = g
            g = f
            f = e
            e = d + t1
            d = c
            c = b
            b = a
            a = t1 + t2
            t = t + 1
        }

        h[0] = h[0] + a
        h[1] = h[1] + b
        h[2] = h[2] + c
        h[3] = h[3] + d
        h[4] = h[4] + e
        h[5] = h[5] + f
        h[6] = h[6] + g
        h[7] = h[7] + hh
        at = at + 128
    }

    return __vy_words64ToBytes(h, 8)
}

// crc32, the IEEE polynomial reflected. No table: the bit at a time
// version is short and a table would be 256 entries of nothing.
fn __vy_crc32(s: str) -> int {
    let b = bytes.of(s)
    let crc = 4294967295
    let i = 0
    while i < len(b) {
        crc = crc ^ __byteAt(b, i)
        let j = 0
        while j < 8 {
            if (crc & 1) != 0 {
                crc = (crc >> 1) ^ 3988292384
            } else {
                crc = crc >> 1
            }
            j = j + 1
        }
        i = i + 1
    }
    return (crc ^ 4294967295) & 4294967295
}

// ---- the str level ----

fn __vy_hashSha512(s: str) -> str {
    return __vy_bytesHex(__vy_sha512(bytes.of(s)))
}

fn __vy_hashHex(s: str) -> str {
    return __vy_bytesHex(bytes.of(s))
}

fn __vy_hashFromHex(s: str) -> str! {
    let b = bytes.fromHex(s)?
    return bytes.str(b)
}

fn __vy_hashBase64(s: str) -> str {
    return __vy_bytesBase64(bytes.of(s))
}

fn __vy_hashFromBase64(s: str) -> str! {
    let b = bytes.fromBase64(s)?
    return bytes.str(b)
}

fn __vy_hashFile(path: str) -> str! {
    let b = bytes.read(path)?
    return __vy_bytesHex(__vy_sha256(b))
}
`
