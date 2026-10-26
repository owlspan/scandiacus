// The library, for ranges and map keys take the fixed-width types.
let big: u64 = 18446744073709551000
let small: u64 = 5
print(max(big, small))
print(min(big, small))
print(max(small, 9))
let n: i8 = -3
print(min(n, -100))
for i in u8(250)..=255 { write(str(i) + " ") }
print("")
for j in i8(-3)..2 { write(str(j) + " ") }
print("")
let e: i32 = 3
for k in 0..e { write(str(k * i32(1000000000)) + " ") }
print("")
let m: {u8: str} = {}
m[u8(200)] = "a"
m[u8(7)] = "b"
m[u8(255)] = "c"
print(m)
print(keys(m))
print(has(m, u8(7)))
print(m[u8(200)])
let lit: {i16: int} = {-5: 1, 300: 2}
print(lit)
let f: f32 = 2.0
print(pow(f, f))
print(clamp(u16(70), 0, 50))
print(sign(i32(-9)))
print(divf(u32(7), u32(2)))
