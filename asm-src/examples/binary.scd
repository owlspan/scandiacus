// The bytes type: raw binary that no text operation can quietly mangle.

let b = bytes.of("hello")
print(b)
print(len(b))
print(b[0])
print(b[4])
print(bytes.hex(b))
print(bytes.base64(b))
print(bytes.str(b))
print(bytes.list(b))

// Round trips.
print(bytes.str(must(bytes.fromHex("68656c6c6f"))))
print(bytes.str(must(bytes.fromBase64("aGVsbG8="))))
print(isOk(bytes.fromHex("zz")))
print(errorOf(bytes.fromHex("zz")))
print(isOk(bytes.fromBase64("!!!")))

// Slicing clamps rather than failing, because reading past the end of
// a buffer is ordinary when parsing.
print(bytes.slice(b, 1, 3))
print(bytes.slice(b, 0, 999))
print(bytes.slice(b, 3, 1))
print(bytes.slice(b, -5, 2))

print(bytes.concat(bytes.of("ab"), bytes.of("cd")))
print(bytes.concat(bytes.of("a"), bytes.of("b"), bytes.of("c")))
print(bytes.find(b, bytes.of("ll")))
print(bytes.find(b, bytes.of("zz")))

print(must(bytes.ofList([104, 105])))
print(isOk(bytes.ofList([300])))
print(errorOf(bytes.ofList([-1])))
print(must(bytes.fill(3, 255)))
print(isOk(bytes.fill(-1, 0)))

// Integers, little-endian by default because x86 and ARM both are.
let le = must(bytes.putInt(258, 2))
let be = must(bytes.putIntBE(258, 2))
print(le)
print(be)
print(must(bytes.getInt(le, 0, 2)))
print(must(bytes.getIntBE(be, 0, 2)))
print(must(bytes.getInt(must(bytes.putInt(1000000, 4)), 0, 4)))
print(isOk(bytes.getInt(b, 4, 4)))
print(errorOf(bytes.getInt(b, 4, 4)))
print(isOk(bytes.putInt(1, 3)))

// Contents, not identity.
print(bytes.of("x") == bytes.of("x"))
print(bytes.of("x") == bytes.of("y"))

print(must(bytes.hash(bytes.of("abc"), "sha256")))
print(must(bytes.hash(bytes.of("abc"), "md5")))
print(isOk(bytes.hash(b, "sha3")))

// A file that is not text, written and read back unchanged.
let raw = must(bytes.ofList([0x89, 0x50, 0x4E, 0x47, 0x00, 0xFF, 0xFE]))
print(isOk(bytes.write("vy_blob.bin", raw)))
let back = must(bytes.read("vy_blob.bin"))
print(back == raw)
print(back)
print(must(bytes.getIntBE(back, 0, 4)))
print(isOk(bytes.read("vy_no_such_file.bin")))
print(isOk(os.file.delete("vy_blob.bin")))

// An annotation, and a function that threads a result through.
let empty: bytes = bytes.of("")
print(len(empty))

fn headerOf(path: str) -> int! {
    let data = bytes.read(path)?
    return bytes.getIntBE(data, 0, 2)?
}
print(isOk(headerOf("vy_definitely_missing.bin")))
