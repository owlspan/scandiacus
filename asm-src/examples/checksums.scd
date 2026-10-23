// Three checksums over bytes, written the way C writes them: u32
// arithmetic that wraps on its own, u8 values straight out of a buffer,
// no masking with & 0xFFFFFFFF after every step.

// CRC-32 (the zlib and PNG one), with its table built once.
fn crcTable() -> []u32 {
    let table: []u32 = []
    for n in 0..256 {
        let c = u32(n)
        for k in 0..8 {
            if (c & 1) == 1 {
                c = 0xEDB88320 ^ (c >> 1)
            } else {
                c = c >> 1
            }
        }
        push(table, c)
    }
    return table
}

fn crc32(table: []u32, data: []u8) -> u32 {
    let c: u32 = 0xFFFFFFFF
    for b in data {
        c = table[int((c ^ u32(b)) & 0xFF)] ^ (c >> 8)
    }
    return c ^ 0xFFFFFFFF
}

// Adler-32, as zlib appends it.
fn adler32(data: []u8) -> u32 {
    let a: u32 = 1
    let b: u32 = 0
    for x in data {
        a = (a + u32(x)) % 65521
        b = (b + a) % 65521
    }
    return (b << 16) | a
}

// FNV-1a, 32 and 64 bits.
fn fnv32(data: []u8) -> u32 {
    let h: u32 = 2166136261
    for x in data {
        h = (h ^ u32(x)) * 16777619
    }
    return h
}

fn fnv64(data: []u8) -> u64 {
    let h: u64 = 14695981039346656037
    for x in data {
        h = (h ^ u64(x)) * 1099511628211
    }
    return h
}

fn bytesOf(s: str) -> []u8 {
    let out: []u8 = []
    for b in bytes.list(bytes.of(s)) {
        push(out, u8(b))
    }
    return out
}

fn hex32(v: u32) -> str {
    let digits = "0123456789abcdef"
    let out = ""
    for i in 0..8 {
        let nib = int((v >> u32(28 - 4 * i)) & 0xF)
        out = out + substr(digits, nib, nib + 1)
    }
    return out
}

let table = crcTable()
for text in ["", "a", "hello", "The quick brown fox jumps over the lazy dog"] {
    let data = bytesOf(text)
    print("\"{text}\"")
    print("  crc32   {hex32(crc32(table, data))}")
    print("  adler32 {hex32(adler32(data))}")
    print("  fnv32   {hex32(fnv32(data))}")
    print("  fnv64   {fnv64(data)}")
}
