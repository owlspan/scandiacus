// extern union, and bit fields laid out as MSVC and MinGW lay them out.
extern union Num { i: i32, f: f32, b: [4]u8, w: u64 }
extern struct Flags {
    ready: u8 bits 1
    mode: u8 bits 3
    level: i8 bits 4
    count: u16 bits 10
    big: u32 bits 20
    sign: i32 bits 5
    on: bool bits 1
    tail: u8
}
extern struct Packet { kind: u8, val: Num }
print(Num.size)
print(Flags.size)
print(Packet.size)
let n = Num{}
n.f = 1.0
print(n.i)
print(n.b[3])
n.i = -1
print(n.w)
let f = Flags{}
f.ready = 1
f.mode = 5
f.level = -3
f.count = 1023
f.big = 1048575
f.sign = -16
f.on = true
f.tail = 77
print(f.ready)
print(f.mode)
print(f.level)
print(f.count)
print(f.big)
print(f.sign)
print(f.on)
print(f.tail)
f.mode = 9
print(f.mode)
print(f.ready)
print(f.level)
f.level += 1
print(f.level)
print(mem.readU8(mem.addr(f)))
print(f)
let p = Packet{}
p.val.i = 7
print(p.val.f > 0.0)
// The raw bytes, which match what MinGW's gcc stores for the same fields.
let a = mem.addr(f)
let bytes = ""
for i in 0..12 { bytes = bytes + str(mem.readU8(a + i)) + " " }
print(bytes)
