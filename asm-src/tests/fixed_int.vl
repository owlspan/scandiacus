// Fixed-width integers: i8, u8, i16, u16, i32, u32, u64. Each wraps at
// its own width, unsigned ones never go negative, and nothing converts
// implicitly: an untyped literal fits when its value is in range, and
// i32(...) and the rest convert, wrapping like a C cast.

// Wrapping at each width.
let a: u8 = 250
print(a + 10)
let b: i8 = 127
print(b + 1)
let c: i16 = -32768
print(c - 1)
let d: u16 = 0
print(d - 1)
let e: i32 = 2147483647
print(e + 1)
let f: u32 = 4000000000
print(f * 2)
let g: u64 = 0
print(g - 1)

// Multiplication keeps the low bits.
let m: u8 = 16
print(m * m)
let n: i32 = 65536
print(n * n)
print(i32(100000) * i32(100000))

// Negation and complement.
let s: i8 = -128
print(-s)
let u: u8 = 1
print(-u)
print(~u)
let w: i16 = 0
print(~w)

// Division and remainder truncate toward zero, and the one signed
// overflow wraps rather than trapping.
let p: i32 = -7
print(p / 2)
print(p % 2)
let q: i8 = -128
print(q / -1)
let r: u32 = 4000000000
print(r / 3)
print(r % 7)

// Shifts: left wraps, right is arithmetic for signed and logical for
// unsigned.
let sh: u8 = 0x81
print(sh << 1)
print(sh >> 1)
let sn: i8 = -64
print(sn >> 2)
print(sn << 1)
let count = 3
print(sh >> count)

// Bitwise operators stay in range.
let x: u16 = 0xF0F0
let y: u16 = 0x0FF0
print(x & y)
print(x | y)
print(x ^ y)

// Comparisons are by value in the type.
let lo: u32 = 1
let hi: u32 = 4000000000
print(lo < hi)
let neg: i32 = -1
print(neg < 0)

// Compound assignment wraps too.
let acc: u8 = 200
acc += 100
print(acc)
acc -= 50
print(acc)
acc *= 3
print(acc)
acc /= 2
print(acc)
acc %= 7
print(acc)
acc <<= 7
print(acc)
acc >>= 3
print(acc)
acc |= 1
acc &= 0x0F
acc ^= 0xFF
print(acc)

// Conversions wrap like a C cast, both at run time and on a constant.
let big = 300
print(u8(big))
print(i8(big))
print(i8(200))
print(u16(-1))
print(u32(-1))
print(i16(40000))
print(int(u8(255)) + 1)
let back: i32 = -5
print(int(back))
print(u32(back))
print(i32(u32(back)))
print(i32(3.99))
print(i32(-3.99))
print(u8(258.7))
print(float(i16(-300)))

// Literals the type holds, in hex, binary and with separators.
let h1: u32 = 0xDEADBEEF
let h2: i16 = -0x8000
let h3: u8 = 0b1010_1010
let h4: u32 = 1_000_000
print("{h1} {h2} {h3} {h4}")

// In interpolation, lists, maps and structs.
let xs: []u8 = [1, 2, 255]
xs[2] += 1
print(xs)
push(xs, 7)
print(len(xs))
let ys = [i16(1), -2, 3]
print(ys)
let bytes: {str: u8} = {"a": 200}
bytes["a"] += 100
print(bytes)
struct Pixel {
    r: u8
    g: u8
    b: u8
}
let px = Pixel{r: 255, g: 128, b: 0}
px.r += 1
print(px)
print("{px.g} {px.b}")

// Functions take and return them.
fn wrapAdd(a: u8, b: u8) -> u8 {
    return a + b
}
print(wrapAdd(200, 100))
fn sum32(xs: []i32) -> i32 {
    let total: i32 = 0
    for v in xs {
        total += v
    }
    return total
}
print(sum32([2147483647, 1, 1]))

// As nullable values and in a match.
let maybe: ?u16 = 65535
if maybe != nil {
    print(maybe + 1)
}
let code: u8 = 3
match code {
    1 => print("one")
    3 => print("three")
    else => print("other")
}

// Generics carry the type through.
fn twice<T>(v: T) -> T {
    return v + v
}
print(twice(u8(200)))
print(twice(i32(2000000000)))

// A checksum in the style C code writes one: each step wraps at 32
// bits, with no masking by hand.
fn fnv1a(data: []u8) -> u32 {
    let hash: u32 = 2166136261
    for byte in data {
        hash ^= u32(byte)
        hash *= 16777619
    }
    return hash
}
let hello: []u8 = [104, 101, 108, 108, 111]
print(fnv1a(hello))
