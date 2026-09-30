// mem: native blocks, reads and writes at every width, and extern
// structs laid over them.

let p = mem.alloc(64)
print(mem.readI64(p))
mem.write8(p, 300)
print(mem.readU8(p))
mem.write8(p, -1)
print("{mem.readU8(p)} {mem.readI8(p)}")
mem.write16(p + 2, 65535)
print("{mem.readU16(p + 2)} {mem.readI16(p + 2)}")
mem.write32(p + 4, -5)
print("{mem.readU32(p + 4)} {mem.readI32(p + 4)}")
mem.write64(p + 8, 1234567890123)
print(mem.readI64(p + 8))
mem.writeF32(p + 16, 1.5)
mem.writeF64(p + 24, 2.25)
print("{mem.readF32(p + 16)} {mem.readF64(p + 24)}")
mem.writeF32(p + 16, 0.1)
print(mem.readF32(p + 16))
mem.fill(p + 32, 65, 5)
print(mem.strN(p + 32, 5))
print(mem.str(p + 32))
mem.copy(p + 40, p + 32, 3)
print(mem.strN(p + 40, 10))
let b = mem.bytes(p + 32, 4)
print(bytes.hex(b))
let q = mem.resize(p, 4096)
print(mem.readI64(q + 8))
mem.free(q)
mem.free(0)
let s = "hello"
print(mem.str(mem.addr(s) + 1))
print(mem.str(0) == "")

extern struct Vec3 { x: f32, y: f32, z: f32 }
extern struct Player {
    hp: i32
    alive: bool
    pos: Vec3
    name: [8]u8
    ammo: u16 at 40
}
print(Player.size)
let raw = mem.alloc(Player.size)
let pl = Player(raw)
pl.hp = 100
pl.hp -= 25
pl.alive = true
pl.pos.x = 1.5
pl.pos.z = -2.0
pl.ammo = u16(70000)
mem.fill(pl.name, 66, 3)
print(pl.hp)
print(pl.ammo)
print(mem.str(pl.name))
print(pl.pos.x + pl.pos.z)
print(pl.pos)
print(str(pl.pos))
let same = Player(raw)
same.hp = 1
print(pl.hp)
print(pl == same)
print(mem.addr(pl) == raw)
let v = Vec3{x: 1.0, y: 2.0, z: 3.0}
pl.pos = v
print(pl.pos)
impl Vec3 {
    fn len2(self) -> f32 {
        return self.x * self.x + self.y * self.y + self.z * self.z
    }
}
print(pl.pos.len2())
fn heal(target: Player, amount: int) {
    target.hp += i32(amount)
}
heal(pl, 9)
print(pl.hp)
mem.free(raw)
