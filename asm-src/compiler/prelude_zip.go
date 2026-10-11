package main

// zip archives, in Veyl: zip.make, zip.list and zip.extract.
//
// An archive is each file's local header and data, then a central
// directory listing them all, then a 22-byte end record pointing at the
// directory. A reader starts from the end. Data is DEFLATE (method 8)
// or stored as it is (method 0), whichever is smaller.
//
// The compressor here is LZ77 over a 32 KB window with hash chains,
// written with DEFLATE's fixed Huffman codes - one block, no code
// tables to send. It gets most of what zlib's default level does on
// text and source code. Inflating reuses the PNG decoder's inflater,
// which handles all three block types, so any zip tool's output reads
// back.
//
// No zip64: an archive or a file of 4 GB or more is refused, as are
// encrypted entries and compression methods other than 0 and 8.
//
// Extraction refuses a name that is absolute or climbs out of the
// destination with "..", before writing anything: an archive must not
// be able to overwrite files outside its own folder.

const preludeZip = `
// crc32 of bytes, with a table: the str version in hash is for short
// strings, and this one runs over whole files.
fn __vy_crc32Bytes(b: bytes) -> int {
    let table: []int = []
    for n in 0..256 {
        let c = n
        for k in 0..8 {
            if (c & 1) != 0 {
                c = (c >> 1) ^ 3988292384
            } else {
                c = c >> 1
            }
        }
        push(table, c)
    }
    let crc = 4294967295
    let n = len(b)
    for i in 0..n {
        crc = table[(crc ^ __byteAt(b, i)) & 255] ^ (crc >> 8)
    }
    return crc ^ 4294967295
}

fn __vy_zipU16(b: bytes, at: int) -> int {
    return __byteAt(b, at) | (__byteAt(b, at + 1) << 8)
}

fn __vy_zipU32(b: bytes, at: int) -> int {
    return __vy_zipU16(b, at) | (__vy_zipU16(b, at + 2) << 16)
}

fn __vy_zipPut16(b: bytes, at: int, v: int) {
    __bytePut(b, at, v & 255)
    __bytePut(b, at + 1, (v >> 8) & 255)
}

fn __vy_zipPut32(b: bytes, at: int, v: int) {
    __vy_zipPut16(b, at, v & 65535)
    __vy_zipPut16(b, at + 2, (v >> 16) & 65535)
}

// The bit writer: w[0] the next byte of out, w[1] the bits waiting,
// w[2] how many. DEFLATE packs bits from the low end of each byte.
fn __vy_zipBits(out: bytes, w: []int, value: int, n: int) {
    w[1] = w[1] | (value << w[2])
    w[2] += n
    while w[2] >= 8 {
        __bytePut(out, w[0], w[1] & 255)
        w[0] += 1
        w[1] = w[1] >> 8
        w[2] -= 8
    }
}

// A Huffman code is sent most significant bit first, so it goes into
// the low-first stream reversed.
fn __vy_zipRev(code: int, n: int) -> int {
    let r = 0
    for i in 0..n {
        r = (r << 1) | ((code >> i) & 1)
    }
    return r
}

// The fixed code for a literal or length symbol, as w writes it.
fn __vy_zipSym(out: bytes, w: []int, sym: int) {
    if sym < 144 {
        __vy_zipBits(out, w, __vy_zipRev(48 + sym, 8), 8)
    } else if sym < 256 {
        __vy_zipBits(out, w, __vy_zipRev(400 + sym - 144, 9), 9)
    } else if sym < 280 {
        __vy_zipBits(out, w, __vy_zipRev(sym - 256, 7), 7)
    } else {
        __vy_zipBits(out, w, __vy_zipRev(192 + sym - 280, 8), 8)
    }
}

// A match of length 3 to 258 at distance 1 to 32768: the length
// symbol and its extra bits, then the distance code and its extra bits.
fn __vy_zipMatch(out: bytes, w: []int, length: int, dist: int, t: [][]int) {
    let li = 28
    while t[0][li] > length {
        li -= 1
    }
    __vy_zipSym(out, w, 257 + li)
    __vy_zipBits(out, w, length - t[0][li], t[1][li])
    let di = 29
    while t[2][di] > dist {
        di -= 1
    }
    __vy_zipBits(out, w, __vy_zipRev(di, 5), 5)
    __vy_zipBits(out, w, dist - t[2][di], t[3][di])
}

// deflate compresses data into one fixed-code block. The answer is
// empty when it would not come out smaller than data.
fn __vy_zipDeflate(data: bytes) -> bytes {
    let n = len(data)
    let empty = bytes.of("")
    if n < 16 {
        return empty
    }
    let t = [
        [3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258],
        [0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0],
        [1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577],
        [0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13],
    ]
    // Worse than n bytes is given up on, so n plus room for the last
    // symbols and the end code is all the space it can need.
    let out = must(bytes.fill(n + 64, 0))
    let w = [0, 0, 0]
    __vy_zipBits(out, w, 1, 1)
    __vy_zipBits(out, w, 1, 2)
    // head[h] is the last position + 1 whose three bytes hash to h;
    // prev[i] the one before it with the same hash.
    let head: []int = []
    for i in 0..65536 {
        push(head, 0)
    }
    let prev: []int = []
    for i in 0..n {
        push(prev, 0)
    }
    let i = 0
    while i < n {
        if w[0] >= n {
            return empty
        }
        let bestLen = 0
        let bestDist = 0
        if i + 2 < n {
            let h = ((__byteAt(data, i) << 10) ^ (__byteAt(data, i + 1) << 5) ^ __byteAt(data, i + 2)) & 65535
            let cand = head[h] - 1
            prev[i] = head[h]
            head[h] = i + 1
            let tries = 0
            while cand >= 0 && i - cand <= 32768 && tries < 32 {
                let max = n - i
                if max > 258 { max = 258 }
                let k = 0
                while k < max && __byteAt(data, cand + k) == __byteAt(data, i + k) {
                    k += 1
                }
                if k > bestLen {
                    bestLen = k
                    bestDist = i - cand
                    if k == max { break }
                }
                cand = prev[cand] - 1
                tries += 1
            }
        }
        if bestLen >= 3 {
            __vy_zipMatch(out, w, bestLen, bestDist, t)
            // The skipped positions still go into the chains, so later
            // matches can start inside this one.
            let j = i + 1
            while j < i + bestLen && j + 2 < n {
                let h = ((__byteAt(data, j) << 10) ^ (__byteAt(data, j + 1) << 5) ^ __byteAt(data, j + 2)) & 65535
                prev[j] = head[h]
                head[h] = j + 1
                j += 1
            }
            i += bestLen
        } else {
            __vy_zipSym(out, w, __byteAt(data, i))
            i += 1
        }
    }
    __vy_zipSym(out, w, 256)
    if w[2] > 0 {
        __vy_zipBits(out, w, 0, 8 - w[2])
    }
    if w[0] >= n {
        return empty
    }
    return bytes.slice(out, 0, w[0])
}

// The last part of a path, by either kind of slash.
fn __vy_zipBase(path: str) -> str {
    let parts = split(replace(path, "\\", "/"), "/")
    let i = len(parts) - 1
    while i > 0 && parts[i] == "" {
        i -= 1
    }
    return parts[i]
}

// Adds path to names and files under the archive name inside: a file,
// or a folder with everything in it.
fn __vy_zipGather(path: str, inside: str, names: []str, files: []str) -> void! {
    if os.dir.is(path) {
        push(names, inside + "/")
        push(files, "")
        let entries = os.dir.list(path)?
        for e in sort(entries) {
            __vy_zipGather(path + "/" + e, inside + "/" + e, names, files)?
        }
        return
    }
    if !os.file.exists(path) {
        return fail("{path}: no such file or folder")
    }
    push(names, inside)
    push(files, path)
}

// The time now, as the DOS date and time words a zip header holds.
fn __vy_zipNow() -> []int {
    let clock = split(time.clock(), ":")
    let date = ((time.year() - 1980) << 9) | (time.month() << 5) | time.day()
    let tod = (toInt(clock[0]) << 11) | (toInt(clock[1]) << 5) | (toInt(clock[2]) / 2)
    return [date, tod]
}

fn __vy_zipMake(dest: str, paths: []str) -> void! {
    let names: []str = []
    let files: []str = []
    for p in paths {
        __vy_zipGather(p, __vy_zipBase(p), names, files)?
    }
    if len(names) > 65535 {
        return fail("{dest}: more than 65535 entries needs zip64, which is not supported")
    }
    let when = __vy_zipNow()
    let datas: []bytes = []
    let methods: []int = []
    let crcs: []int = []
    let sizes: []int = []
    let total = 22
    for i in 0..len(names) {
        let raw = bytes.of("")
        if files[i] != "" {
            raw = bytes.read(files[i])?
        }
        if len(raw) >= 4294967295 {
            return fail("{files[i]}: 4 GB or more needs zip64, which is not supported")
        }
        push(crcs, __vy_crc32Bytes(raw))
        push(sizes, len(raw))
        let packed = __vy_zipDeflate(raw)
        if len(packed) > 0 {
            push(datas, packed)
            push(methods, 8)
        } else {
            push(datas, raw)
            push(methods, 0)
        }
        let nameLen = len(bytes.of(names[i]))
        total += 30 + nameLen + len(datas[i]) + 46 + nameLen
    }
    if total >= 4294967295 {
        return fail("{dest}: an archive of 4 GB or more needs zip64, which is not supported")
    }

    let out = bytes.fill(total, 0)?
    let at = 0
    let offsets: []int = []
    for i in 0..len(names) {
        let name = bytes.of(names[i])
        push(offsets, at)
        __vy_zipPut32(out, at, 67324752)
        __vy_zipPut16(out, at + 4, 20)
        __vy_zipPut16(out, at + 6, 2048)
        __vy_zipPut16(out, at + 8, methods[i])
        __vy_zipPut16(out, at + 10, when[1])
        __vy_zipPut16(out, at + 12, when[0])
        __vy_zipPut32(out, at + 14, crcs[i])
        __vy_zipPut32(out, at + 18, len(datas[i]))
        __vy_zipPut32(out, at + 22, sizes[i])
        __vy_zipPut16(out, at + 26, len(name))
        mem.copy(mem.addr(out) + at + 30, mem.addr(name), len(name))
        at += 30 + len(name)
        mem.copy(mem.addr(out) + at, mem.addr(datas[i]), len(datas[i]))
        at += len(datas[i])
    }
    let dirAt = at
    for i in 0..len(names) {
        let name = bytes.of(names[i])
        __vy_zipPut32(out, at, 33639248)
        __vy_zipPut16(out, at + 4, 20)
        __vy_zipPut16(out, at + 6, 20)
        __vy_zipPut16(out, at + 8, 2048)
        __vy_zipPut16(out, at + 10, methods[i])
        __vy_zipPut16(out, at + 12, when[1])
        __vy_zipPut16(out, at + 14, when[0])
        __vy_zipPut32(out, at + 16, crcs[i])
        __vy_zipPut32(out, at + 20, len(datas[i]))
        __vy_zipPut32(out, at + 24, sizes[i])
        __vy_zipPut16(out, at + 28, len(name))
        // External attributes: the DOS directory bit for a folder.
        if files[i] == "" {
            __vy_zipPut32(out, at + 38, 16)
        }
        __vy_zipPut32(out, at + 42, offsets[i])
        mem.copy(mem.addr(out) + at + 46, mem.addr(name), len(name))
        at += 46 + len(name)
    }
    __vy_zipPut32(out, at, 101010256)
    __vy_zipPut16(out, at + 8, len(names))
    __vy_zipPut16(out, at + 10, len(names))
    __vy_zipPut32(out, at + 12, at - dirAt)
    __vy_zipPut32(out, at + 16, dirAt)
    bytes.write(dest, out)?
}

// The central directory: where it starts and how many entries, found
// from the end record, which is the last 22 bytes unless the archive
// has a comment after it.
fn __vy_zipDir(b: bytes, src: str) -> []int! {
    let i = len(b) - 22
    let stop = i - 65535
    if stop < 0 { stop = 0 }
    while i >= stop {
        if __vy_zipU32(b, i) == 101010256 {
            let count = __vy_zipU16(b, i + 10)
            let at = __vy_zipU32(b, i + 16)
            if at >= len(b) {
                return fail("{src}: the archive's directory is damaged")
            }
            return [at, count]
        }
        i -= 1
    }
    return fail("{src} is not a zip archive")
}

fn __vy_zipList(src: str) -> []str! {
    let b = bytes.read(src)?
    let d = __vy_zipDir(b, src)?
    let names: []str = []
    let at = d[0]
    for k in 0..d[1] {
        if at + 46 > len(b) || __vy_zipU32(b, at) != 33639248 {
            return fail("{src}: the archive's directory is damaged")
        }
        let nameLen = __vy_zipU16(b, at + 28)
        push(names, bytes.str(bytes.slice(b, at + 46, at + 46 + nameLen)))
        at += 46 + nameLen + __vy_zipU16(b, at + 30) + __vy_zipU16(b, at + 32)
    }
    return names
}

// A name is safe when it stays inside the folder it is extracted to:
// not absolute, no drive, no ".." part.
fn __vy_zipSafe(name: str) -> bool {
    let n = replace(name, "\\", "/")
    if indexOf(n, "/") == 0 || indexOf(n, ":") >= 0 {
        return false
    }
    for part in split(n, "/") {
        if part == ".." {
            return false
        }
    }
    return true
}

// Makes a folder and any missing above it. A leading slash and a drive
// like C: are kept as they are, not made.
fn __vy_zipMkdirs(path: str) -> void! {
    let parts = split(replace(path, "\\", "/"), "/")
    let cur = ""
    for i in 0..len(parts) {
        let p = parts[i]
        if i == 0 {
            cur = p
        } else {
            cur = cur + "/" + p
        }
        if p == "" || p == "." || (i == 0 && indexOf(p, ":") >= 0) || os.dir.is(cur) {
            continue
        }
        os.dir.make(cur)?
    }
}

fn __vy_zipExtract(src: str, dir: str) -> int! {
    let b = bytes.read(src)?
    let d = __vy_zipDir(b, src)?
    // Every name is checked before anything is written.
    let names = __vy_zipList(src)?
    for name in names {
        if !__vy_zipSafe(name) {
            return fail("archive contains an unsafe path: {name}")
        }
    }
    __vy_zipMkdirs(dir)?
    let files = 0
    let at = d[0]
    for k in 0..d[1] {
        let flags = __vy_zipU16(b, at + 8)
        let method = __vy_zipU16(b, at + 10)
        let crc = __vy_zipU32(b, at + 16)
        let packed = __vy_zipU32(b, at + 20)
        let size = __vy_zipU32(b, at + 24)
        let nameLen = __vy_zipU16(b, at + 28)
        let local = __vy_zipU32(b, at + 42)
        let name = replace(bytes.str(bytes.slice(b, at + 46, at + 46 + nameLen)), "\\", "/")
        at += 46 + nameLen + __vy_zipU16(b, at + 30) + __vy_zipU16(b, at + 32)

        let path = dir + "/" + name
        if name != "" && __strAt(name, len(name) - 1) == 47 {
            __vy_zipMkdirs(path)?
            continue
        }
        if (flags & 1) != 0 {
            return fail("{name}: encrypted entries are not supported")
        }
        if local + 30 > len(b) || __vy_zipU32(b, local) != 67324752 {
            return fail("{src}: the entry for {name} is damaged")
        }
        let start = local + 30 + __vy_zipU16(b, local + 26) + __vy_zipU16(b, local + 28)
        if start + packed > len(b) {
            return fail("{src}: the entry for {name} runs past the end of the archive")
        }
        let out = bytes.fill(size, 0)?
        if method == 0 {
            if packed != size {
                return fail("{src}: the entry for {name} is damaged")
            }
            mem.copy(mem.addr(out), mem.addr(b) + start, size)
        } else if method == 8 {
            let err = __vy_inflateAt(b, start, out)
            if err != "" {
                return fail("{name}: {err}")
            }
        } else {
            return fail("{name}: compression method {method} is not supported")
        }
        if __vy_crc32Bytes(out) != crc {
            return fail("{name}: the data does not match its checksum")
        }
        let slash = len(path) - len(__vy_zipBase(path)) - 1
        if slash > 0 {
            __vy_zipMkdirs(bytes.str(bytes.slice(bytes.of(path), 0, slash)))?
        }
        bytes.write(path, out)?
        files += 1
    }
    return files
}
`
