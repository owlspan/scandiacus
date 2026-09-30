package main

// proc: read and write another process's memory, in Veyl.
//
// This is the standard Windows debugging surface - OpenProcess,
// ReadProcessMemory, WriteProcessMemory - the same calls a debugger,
// a profiler or a memory-inspection tool makes. Every function here is
// reachable through extern already; the point of wrapping it is that a
// buffer, a length and an out-parameter turn into an ordinary call that
// returns a value.
//
// It is built on mem.symbol and mem.call rather than extern, so it
// needs no import-table plumbing of its own: mem.symbol loads
// kernel32 the first time and hands back the address, and mem.call
// makes the call by the Windows x64 convention. A bytes value passes
// as the address of its data, which is what the read and write calls
// want.
//
// Nothing here checks that an address is valid or that the caller has
// the rights the target needs. A call that the operating system
// refuses comes back as 0, an empty bytes, or -1, never a crash.

const preludeProc = `
// PROCESS_VM_OPERATION | PROCESS_VM_READ | PROCESS_VM_WRITE |
// PROCESS_QUERY_INFORMATION: enough to read, write and ask about a
// process, and no more.
fn __vy_procAccess() -> int {
    return 0x438
}

// The current process. GetCurrentProcess returns a pseudo-handle, -1,
// that every process memory call accepts as itself.
fn __vy_procSelf() -> int {
    return mem.call(mem.symbol("kernel32.dll", "GetCurrentProcess"))
}

fn __vy_procId() -> int {
    return mem.call(mem.symbol("kernel32.dll", "GetCurrentProcessId"))
}

// Open a process by id for reading and writing. 0 means it could not
// be opened - usually a process owned by another user, or one that
// needs rights this program was not started with.
fn __vy_procOpen(pid: int) -> int {
    return mem.call(mem.symbol("kernel32.dll", "OpenProcess"), __vy_procAccess(), 0, pid)
}

fn __vy_procClose(h: int) -> bool {
    return mem.call(mem.symbol("kernel32.dll", "CloseHandle"), h) != 0
}

// Read n bytes at addr. An empty bytes means the read failed: the
// address is not mapped, or not readable with the handle's rights.
fn __vy_procRead(h: int, addr: int, n: int) -> bytes {
    if n <= 0 {
        return bytes.of("")
    }
    let buf = mem.alloc(n)
    let got = mem.alloc(8)
    let ok = mem.call(mem.symbol("kernel32.dll", "ReadProcessMemory"), h, addr, buf, n, got)
    let read = mem.readI64(got)
    let out = bytes.of("")
    if ok != 0 {
        out = mem.bytes(buf, read)
    }
    mem.free(buf)
    mem.free(got)
    return out
}

// Write data at addr. Returns how many bytes were written, 0 on
// failure.
fn __vy_procWrite(h: int, addr: int, data: bytes) -> int {
    let n = len(data)
    if n <= 0 {
        return 0
    }
    let got = mem.alloc(8)
    let ok = mem.call(mem.symbol("kernel32.dll", "WriteProcessMemory"), h, addr, data, n, got)
    let wrote = 0
    if ok != 0 {
        wrote = mem.readI64(got)
    }
    mem.free(got)
    return wrote
}

// Typed reads. Each reads the width at addr and gives it back as an
// int or a float; a failed read gives 0.
fn __vy_procReadU8(h: int, addr: int) -> int {
    let b = __vy_procRead(h, addr, 1)
    if len(b) < 1 {
        return 0
    }
    return b[0]
}

fn __vy_procReadI32(h: int, addr: int) -> int {
    let b = __vy_procRead(h, addr, 4)
    if len(b) < 4 {
        return 0
    }
    let v = b[0] | (b[1] << 8) | (b[2] << 16) | (b[3] << 24)
    // v holds the low 32 bits; read them back as a signed i32 so a
    // negative value comes out negative rather than as a large unsigned.
    return int(i32(v))
}

fn __vy_procReadI64(h: int, addr: int) -> int {
    let b = __vy_procRead(h, addr, 8)
    if len(b) < 8 {
        return 0
    }
    let lo = b[0] | (b[1] << 8) | (b[2] << 16) | (b[3] << 24)
    let hi = b[4] | (b[5] << 8) | (b[6] << 16) | (b[7] << 24)
    return (lo & 0xFFFFFFFF) | (hi << 32)
}

// A typed write goes through a small scratch buffer so the bytes are
// laid out little-endian the way memory is.
fn __vy_procWriteI32(h: int, addr: int, v: int) -> bool {
    let buf = mem.alloc(4)
    mem.write32(buf, v)
    let ok = __vy_procWrite(h, addr, mem.bytes(buf, 4)) == 4
    mem.free(buf)
    return ok
}

fn __vy_procWriteI64(h: int, addr: int, v: int) -> bool {
    let buf = mem.alloc(8)
    mem.write64(buf, v)
    let ok = __vy_procWrite(h, addr, mem.bytes(buf, 8)) == 8
    mem.free(buf)
    return ok
}

// Scan a region for a byte pattern such as "48 8B ?? ?? 89", where ??
// matches any byte. Returns the address of the first match, or -1.
// The region is read into this process first, so a part of it that is
// not readable ends the scan there.
fn __vy_procScan(h: int, start: int, size: int, pattern: str) -> int {
    if size <= 0 {
        return -1
    }
    let buf = mem.alloc(size)
    let got = mem.alloc(8)
    let ok = mem.call(mem.symbol("kernel32.dll", "ReadProcessMemory"), h, start, buf, size, got)
    let read = mem.readI64(got)
    let at = -1
    if ok != 0 && read > 0 {
        // mem.scan gives the address of the match inside the local
        // buffer; the same match in the target is that far past start.
        let hit = mem.scan(buf, read, pattern)
        if hit >= 0 {
            at = start + (hit - buf)
        }
    }
    mem.free(buf)
    mem.free(got)
    return at
}
`
