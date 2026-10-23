package main

// PNG decoding, in Scandiacus.
//
// Windows has no plain call that turns a PNG file into pixels - that is
// WIC, a COM API - so the decoder is here: DEFLATE, the PNG chunks, and
// the scanline filters. What comes out is ready for a 32-bit bitmap:
//
//	bytes 0-3   width, little-endian
//	bytes 4-7   height
//	then        width * height pixels, four bytes each, blue, green,
//	            red, alpha, with the colours premultiplied by alpha,
//	            which is what AlphaBlend wants
//
// Supported: colour types 0 (grey), 2 (RGB), 3 (palette), 4 (grey and
// alpha) and 6 (RGBA), at bit depth 8, and palette images at 1, 2, 4 and
// 8, with transparency from a tRNS chunk. Not supported, and refused
// with a sentence saying so: interlacing and 16-bit samples.
//
// The inflater is the canonical-Huffman decoder of zlib's own puff.c:
// a code is read one bit at a time, compared against the first code of
// each length. It is not the fastest way to inflate, and it is short
// enough to read against RFC 1951.
//
// Its state is a list, because a list is shared by reference and a
// struct is copied into a function: st[0] is the input position,
// st[1] the bit buffer, st[2] how many bits are in it, st[3] the output
// position.

const preludePNG = `
fn __vy_pngBit(src: bytes, st: []int) -> int {
    if st[2] == 0 {
        if st[0] >= len(src) {
            return -1
        }
        st[1] = __byteAt(src, st[0])
        st[0] += 1
        st[2] = 8
    }
    let b = st[1] & 1
    st[1] = st[1] >> 1
    st[2] -= 1
    return b
}

fn __vy_pngBits(src: bytes, st: []int, n: int) -> int {
    let v = 0
    let i = 0
    while i < n {
        let b = __vy_pngBit(src, st)
        if b < 0 {
            return -1
        }
        v = v | (b << i)
        i += 1
    }
    return v
}

// A Huffman table as puff.c keeps one: how many codes there are of each
// length, then the symbols in code order.
fn __vy_pngTable(lengths: []int, n: int, counts: []int, symbols: []int) {
    let i = 0
    while i < 16 {
        counts[i] = 0
        i += 1
    }
    i = 0
    while i < n {
        counts[lengths[i]] += 1
        i += 1
    }
    let offs: []int = [0, 0]
    i = 1
    while i < 15 {
        push(offs, offs[i] + counts[i])
        i += 1
    }
    i = 0
    while i < n {
        let l = lengths[i]
        if l != 0 {
            symbols[offs[l]] = i
            offs[l] += 1
        }
        i += 1
    }
}

fn __vy_pngDecodeSym(src: bytes, st: []int, counts: []int, symbols: []int) -> int {
    let code = 0
    let first = 0
    let index = 0
    let l = 1
    while l < 16 {
        let b = __vy_pngBit(src, st)
        if b < 0 {
            return -1
        }
        code = code | b
        let count = counts[l]
        if code - count < first {
            return symbols[index + (code - first)]
        }
        index += count
        first += count
        first = first << 1
        code = code << 1
        l += 1
    }
    return -1
}

fn __vy_pngZeros(n: int) -> []int {
    let out: []int = []
    let i = 0
    while i < n {
        push(out, 0)
        i += 1
    }
    return out
}


// One compressed block's worth of symbols, into out.
fn __vy_pngCodes(src: bytes, st: []int, out: bytes, lc: []int, ls: []int, dc: []int, ds: []int, t: [][]int) -> str {
    while true {
        let sym = __vy_pngDecodeSym(src, st, lc, ls)
        if sym < 0 {
            return "the compressed data ends early"
        }
        if sym < 256 {
            if st[3] >= len(out) {
                return "the data is longer than its size says"
            }
            __bytePut(out, st[3], sym)
            st[3] += 1
        } else if sym == 256 {
            return ""
        } else {
            let li = sym - 257
            if li >= 29 {
                return "the compressed data is damaged"
            }
            let length = t[0][li] + __vy_pngBits(src, st, t[1][li])
            let dsym = __vy_pngDecodeSym(src, st, dc, ds)
            if dsym < 0 || dsym >= 30 {
                return "the compressed data is damaged"
            }
            let dist = t[2][dsym] + __vy_pngBits(src, st, t[3][dsym])
            if dist > st[3] {
                return "the compressed data refers back past its start"
            }
            if st[3] + length > len(out) {
                return "the data is longer than its size says"
            }
            let k = 0
            while k < length {
                __bytePut(out, st[3], __byteAt(out, st[3] - dist))
                st[3] += 1
                k += 1
            }
        }
    }
    return ""
}

// inflate decompresses a zlib stream into out, which is already the
// size the data has to be. It answers "" or what went wrong.
fn __vy_pngInflate(src: bytes, out: bytes) -> str {
    if len(src) < 2 || (__byteAt(src, 0) & 15) != 8 {
        return "the image data is not zlib-compressed"
    }
    return __vy_inflateAt(src, 2, out)
}

// inflateAt decompresses raw DEFLATE starting at byte at of src: a
// zlib stream's body, or a zip entry's data.
fn __vy_inflateAt(src: bytes, at: int, out: bytes) -> str {
    let st = [at, 0, 0, 0]
    // The length and distance tables of RFC 1951, section 3.2.5, and
    // the order code-length codes come in.
    let t = [
        [3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258],
        [0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0],
        [1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577],
        [0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13],
    ]
    let order = [16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15]
    let lc = __vy_pngZeros(16)
    let ls = __vy_pngZeros(288)
    let dc = __vy_pngZeros(16)
    let ds = __vy_pngZeros(30)
    let last = 0
    while last == 0 {
        last = __vy_pngBits(src, st, 1)
        let kind = __vy_pngBits(src, st, 2)
        if last < 0 || kind < 0 {
            return "the compressed data ends early"
        }
        if kind == 0 {
            // Stored: skip to a byte boundary, then LEN, NLEN, and LEN
            // bytes as they are.
            st[2] = 0
            if st[0] + 4 > len(src) {
                return "the compressed data ends early"
            }
            let n = __byteAt(src, st[0]) | (__byteAt(src, st[0] + 1) << 8)
            st[0] += 4
            if st[0] + n > len(src) || st[3] + n > len(out) {
                return "the compressed data is damaged"
            }
            let k = 0
            while k < n {
                __bytePut(out, st[3], __byteAt(src, st[0]))
                st[0] += 1
                st[3] += 1
                k += 1
            }
        } else if kind == 1 {
            let lens = __vy_pngZeros(288)
            let i = 0
            while i < 288 {
                if i < 144 {
                    lens[i] = 8
                } else if i < 256 {
                    lens[i] = 9
                } else if i < 280 {
                    lens[i] = 7
                } else {
                    lens[i] = 8
                }
                i += 1
            }
            __vy_pngTable(lens, 288, lc, ls)
            let dl = __vy_pngZeros(30)
            i = 0
            while i < 30 {
                dl[i] = 5
                i += 1
            }
            __vy_pngTable(dl, 30, dc, ds)
            let err = __vy_pngCodes(src, st, out, lc, ls, dc, ds, t)
            if err != "" {
                return err
            }
        } else if kind == 2 {
            let nlen = __vy_pngBits(src, st, 5) + 257
            let ndist = __vy_pngBits(src, st, 5) + 1
            let ncode = __vy_pngBits(src, st, 4) + 4
            if nlen > 286 || ndist > 30 {
                return "the compressed data is damaged"
            }
            let lens = __vy_pngZeros(320)
            let i = 0
            while i < ncode {
                lens[order[i]] = __vy_pngBits(src, st, 3)
                i += 1
            }
            let cc = __vy_pngZeros(16)
            let cs = __vy_pngZeros(19)
            __vy_pngTable(lens, 19, cc, cs)
            let all = __vy_pngZeros(320)
            i = 0
            while i < nlen + ndist {
                let sym = __vy_pngDecodeSym(src, st, cc, cs)
                if sym < 0 {
                    return "the compressed data is damaged"
                }
                if sym < 16 {
                    all[i] = sym
                    i += 1
                } else {
                    let prev = 0
                    let rep = 0
                    if sym == 16 {
                        if i == 0 {
                            return "the compressed data is damaged"
                        }
                        prev = all[i - 1]
                        rep = 3 + __vy_pngBits(src, st, 2)
                    } else if sym == 17 {
                        rep = 3 + __vy_pngBits(src, st, 3)
                    } else {
                        rep = 11 + __vy_pngBits(src, st, 7)
                    }
                    if i + rep > nlen + ndist {
                        return "the compressed data is damaged"
                    }
                    let k = 0
                    while k < rep {
                        all[i] = prev
                        i += 1
                        k += 1
                    }
                }
            }
            let litLens = __vy_pngZeros(288)
            i = 0
            while i < nlen {
                litLens[i] = all[i]
                i += 1
            }
            let distLens = __vy_pngZeros(30)
            i = 0
            while i < ndist {
                distLens[i] = all[nlen + i]
                i += 1
            }
            __vy_pngTable(litLens, nlen, lc, ls)
            __vy_pngTable(distLens, ndist, dc, ds)
            let err = __vy_pngCodes(src, st, out, lc, ls, dc, ds, t)
            if err != "" {
                return err
            }
        } else {
            return "the compressed data is damaged"
        }
    }
    return ""
}

fn __vy_pngU32(b: bytes, at: int) -> int {
    return (__byteAt(b, at) << 24) | (__byteAt(b, at + 1) << 16) | (__byteAt(b, at + 2) << 8) | __byteAt(b, at + 3)
}

fn __vy_pngPaeth(a: int, b: int, c: int) -> int {
    let p = a + b - c
    let pa = p - a
    if pa < 0 {
        pa = -pa
    }
    let pb = p - b
    if pb < 0 {
        pb = -pb
    }
    let pc = p - c
    if pc < 0 {
        pc = -pc
    }
    if pa <= pb && pa <= pc {
        return a
    }
    if pb <= pc {
        return b
    }
    return c
}

// win.image: a .png through the decoder above, anything else through
// Windows' own .bmp loader.
fn __vy_winImage(path: str) -> int! {
    if endsWith(lower(path), ".png") {
        let px = __vy_pngLoad(path)?
        let img = __imageFromPixels(px)
        if img == 0 {
            return fail("cannot make a bitmap for {path}")
        }
        return img
    }
    let img = __imageFromBMP(path)
    if img == 0 {
        return fail("cannot load {path} - an image has to be a readable .bmp or .png file")
    }
    return img
}

// __vy_pngLoad reads and decodes a PNG file into the layout described
// at the top of this file.
fn __vy_pngLoad(path: str) -> bytes! {
    let file = bytes.read(path)?
    let n = len(file)
    if n < 8 || __vy_pngU32(file, 0) != 2303741511 || __vy_pngU32(file, 4) != 218765834 {
        return fail("{path} is not a PNG file")
    }
    let width = 0
    let height = 0
    let depth = 0
    let kind = 0
    let palette: []int = []
    let alphas: []int = []
    let idat = 0
    let pos = 8
    // First pass: the header, the palette, and how much image data
    // there is, so it can be gathered into one block.
    while pos + 8 <= n {
        let size = __vy_pngU32(file, pos)
        let tag = __vy_pngU32(file, pos + 4)
        let body = pos + 8
        if body + size > n {
            return fail("{path} is cut short")
        }
        if tag == 1229472850 {
            width = __vy_pngU32(file, body)
            height = __vy_pngU32(file, body + 4)
            depth = __byteAt(file, body + 8)
            kind = __byteAt(file, body + 9)
            if __byteAt(file, body + 12) != 0 {
                return fail("{path} is interlaced, which is not supported")
            }
        } else if tag == 1347179589 {
            let i = 0
            while i < size {
                push(palette, __byteAt(file, body + i))
                i += 1
            }
        } else if tag == 1951551059 {
            let i = 0
            while i < size {
                push(alphas, __byteAt(file, body + i))
                i += 1
            }
        } else if tag == 1229209940 {
            idat += size
        }
        pos = body + size + 4
    }
    if width <= 0 || height <= 0 {
        return fail("{path} has no image header")
    }
    let channels = 0
    if kind == 0 {
        channels = 1
    } else if kind == 2 {
        channels = 3
    } else if kind == 3 {
        channels = 1
    } else if kind == 4 {
        channels = 2
    } else if kind == 6 {
        channels = 4
    } else {
        return fail("{path} has an unknown colour type")
    }
    if depth != 8 && !(kind == 3 && (depth == 1 || depth == 2 || depth == 4)) {
        return fail("{path} uses {depth}-bit samples; only 8-bit images, and palette images at 1, 2, 4 or 8, are supported")
    }

    let packed = __bytesMake(idat)
    let at = 0
    pos = 8
    while pos + 8 <= n {
        let size = __vy_pngU32(file, pos)
        if __vy_pngU32(file, pos + 4) == 1229209940 {
            let i = 0
            while i < size {
                __bytePut(packed, at, __byteAt(file, pos + 8 + i))
                at += 1
                i += 1
            }
        }
        pos = pos + 12 + size
    }

    // Bytes per pixel for the filters, and bytes per row.
    let bpp = channels
    let stride = width * channels
    if depth < 8 {
        bpp = 1
        stride = (width * depth + 7) / 8
    }
    let raw = __bytesMake(height * (stride + 1))
    let err = __vy_pngInflate(packed, raw)
    if err != "" {
        return fail("{path}: " + err)
    }

    // Undo the filters in place, row by row. Each row starts with its
    // filter byte; the unfiltered bytes overwrite the filtered ones.
    let y = 0
    while y < height {
        let row = y * (stride + 1) + 1
        let up = row - (stride + 1)
        let f = __byteAt(raw, row - 1)
        let x = 0
        if f == 0 {
            x = stride
        }
        while x < stride {
            let a = 0
            if x >= bpp {
                a = __byteAt(raw, row + x - bpp)
            }
            let b = 0
            let c = 0
            if y > 0 {
                b = __byteAt(raw, up + x)
                if x >= bpp {
                    c = __byteAt(raw, up + x - bpp)
                }
            }
            let v = __byteAt(raw, row + x)
            if f == 1 {
                v = v + a
            } else if f == 2 {
                v = v + b
            } else if f == 3 {
                v = v + ((a + b) >> 1)
            } else if f == 4 {
                v = v + __vy_pngPaeth(a, b, c)
            } else if f != 0 {
                return fail("{path} has an unknown row filter")
            }
            __bytePut(raw, row + x, v & 255)
            x += 1
        }
        y += 1
    }

    // Out to premultiplied BGRA.
    let out = __bytesMake(8 + width * height * 4)
    let k = 0
    while k < 4 {
        __bytePut(out, k, (width >> (8 * k)) & 255)
        __bytePut(out, 4 + k, (height >> (8 * k)) & 255)
        k += 1
    }
    let o = 8
    y = 0
    while y < height {
        let row = y * (stride + 1) + 1
        let x = 0
        while x < width {
            let r = 0
            let g = 0
            let bl = 0
            let al = 255
            if kind == 3 {
                let idx = 0
                if depth == 8 {
                    idx = __byteAt(raw, row + x)
                } else {
                    let perByte = 8 / depth
                    let byte = __byteAt(raw, row + x / perByte)
                    let shift = 8 - depth * (x % perByte + 1)
                    idx = (byte >> shift) & ((1 << depth) - 1)
                }
                if idx * 3 + 2 < len(palette) {
                    r = palette[idx * 3]
                    g = palette[idx * 3 + 1]
                    bl = palette[idx * 3 + 2]
                }
                if idx < len(alphas) {
                    al = alphas[idx]
                }
            } else {
                let p = row + x * channels
                if kind == 0 || kind == 4 {
                    r = __byteAt(raw, p)
                    g = r
                    bl = r
                    if kind == 4 {
                        al = __byteAt(raw, p + 1)
                    }
                } else {
                    r = __byteAt(raw, p)
                    g = __byteAt(raw, p + 1)
                    bl = __byteAt(raw, p + 2)
                    if kind == 6 {
                        al = __byteAt(raw, p + 3)
                    }
                }
            }
            if al == 255 {
                __bytePut(out, o, bl)
                __bytePut(out, o + 1, g)
                __bytePut(out, o + 2, r)
            } else {
                __bytePut(out, o, (bl * al + 127) / 255)
                __bytePut(out, o + 1, (g * al + 127) / 255)
                __bytePut(out, o + 2, (r * al + 127) / 255)
            }
            __bytePut(out, o + 3, al)
            o += 4
            x += 1
        }
        y += 1
    }
    return out
}
`
