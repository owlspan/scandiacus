// proc: reading and writing process memory. The test works on the
// running process itself, so it is deterministic and needs no other
// program: a buffer is written and read back through the same calls a
// debugger would use on another process.

let h = proc.current()
print(proc.pid() > 0)

let region = mem.alloc(64)

// Typed writes and reads.
print(proc.writeI32(h, region, 1234567))
print(proc.readI32(h, region))
print(proc.writeI64(h, region + 8, 0x1122334455667788))
print(proc.readI64(h, region + 8))

// A negative value keeps its bits through an i32 round trip.
print(proc.writeI32(h, region + 4, -2))
print(proc.readI32(h, region + 4))

// Raw bytes both ways.
let src = must(bytes.fromHex("deadbeef"))
print(proc.write(h, region + 16, src))
print(bytes.hex(proc.read(h, region + 16, 4)))
print(proc.readU8(h, region + 16))

// A byte pattern scan, with ?? as a wildcard, finds what was written.
mem.write8(region + 40, 0x90)
mem.write8(region + 41, 0x48)
mem.write8(region + 42, 0x8b)
mem.write8(region + 43, 0x05)
print(proc.scan(h, region, 64, "90 48 8B") == region + 40)
print(proc.scan(h, region, 64, "90 ?? 8B ??") == region + 40)
print(proc.scan(h, region, 64, "11 22 33 44 55 66") == -1)

// Opening the process by its own id and reading through that handle
// gives the same bytes.
let opened = proc.open(proc.pid())
print(opened != 0)
print(proc.readI32(opened, region))
print(proc.close(opened))

// Reading an address that is not mapped fails softly: an empty bytes,
// and a typed read of zero.
print(len(proc.read(h, 0, 8)))
print(proc.readI32(h, 0))

mem.free(region)
