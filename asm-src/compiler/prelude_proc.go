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

// ---- enumeration ----
//
// Processes, modules and threads come from a toolhelp snapshot, and the
// layout of a process's address space from VirtualQueryEx. The entry
// structures are laid out by hand in a scratch buffer at their x64
// offsets, which is what lets this stay on mem.call with nothing
// declared:
//
//   PROCESSENTRY32  304 bytes: pid at 8, parent pid at 32, name at 44
//   MODULEENTRY32   568 bytes: base at 24, size at 32, name at 48,
//                              path at 304
//   THREADENTRY32    28 bytes: thread id at 8, owner pid at 12
//   MEMORY_BASIC_INFORMATION 48 bytes: base at 0, size at 24,
//                              state at 32, protect at 36, type at 40
//
// The names are the ANSI ones, so a name outside the system code page
// comes back with its odd characters replaced.

struct ProcModule {
    name: str
    path: str
    base: int
    size: int
}

struct ProcRegion {
    base: int
    size: int
    protect: int
    kind: int
    readable: bool
    writable: bool
    executable: bool
}

// A Windows BOOL is 32 bits wide and the top half of the register it
// comes back in is not promised to be clear.
fn __vy_procTrue(v: int) -> bool {
    return (v & 0xFFFFFFFF) != 0
}

// A snapshot handle, or -1. A module snapshot of a process that is
// loading or unloading a DLL at that moment fails with ERROR_BAD_LENGTH
// (24), and the documented answer is to ask again.
fn __vy_procSnap(flags: int, pid: int) -> int {
    let lastError = mem.symbol("kernel32.dll", "GetLastError")
    let create = mem.symbol("kernel32.dll", "CreateToolhelp32Snapshot")
    let tries = 0
    while tries < 16 {
        let snap = mem.call(create, flags, pid)
        if snap != -1 {
            return snap
        }
        if (mem.call(lastError) & 0xFFFFFFFF) != 24 {
            return -1
        }
        tries += 1
    }
    return -1
}

// The id of every running process, in the order the system lists them.
fn __vy_procPids() -> []int {
    let out: []int = []
    let snap = __vy_procSnap(2, 0)
    if snap == -1 {
        return out
    }
    let next = mem.symbol("kernel32.dll", "Process32Next")
    let e = mem.alloc(304)
    mem.write32(e, 304)
    let more = __vy_procTrue(mem.call(mem.symbol("kernel32.dll", "Process32First"), snap, e))
    while more {
        push(out, mem.readU32(e + 8))
        more = __vy_procTrue(mem.call(next, snap, e))
    }
    mem.free(e)
    __vy_procClose(snap)
    return out
}

// The executable name of a process, "" if there is no such process.
fn __vy_procName(pid: int) -> str {
    let snap = __vy_procSnap(2, 0)
    if snap == -1 {
        return ""
    }
    let next = mem.symbol("kernel32.dll", "Process32Next")
    let e = mem.alloc(304)
    mem.write32(e, 304)
    let found = ""
    let more = __vy_procTrue(mem.call(mem.symbol("kernel32.dll", "Process32First"), snap, e))
    while more {
        if mem.readU32(e + 8) == pid {
            found = mem.str(e + 44)
            break
        }
        more = __vy_procTrue(mem.call(next, snap, e))
    }
    mem.free(e)
    __vy_procClose(snap)
    return found
}

// One walk of the process list serves parent and find: with byName
// false, the parent's pid of the process pid; with it true, the pid of
// the first process whose executable is called name. 0 if there is none.
fn __vy_procWalk(byName: bool, pid: int, name: str) -> int {
    let snap = __vy_procSnap(2, 0)
    if snap == -1 {
        return 0
    }
    let want = lower(name)
    let next = mem.symbol("kernel32.dll", "Process32Next")
    let e = mem.alloc(304)
    mem.write32(e, 304)
    let found = 0
    let more = __vy_procTrue(mem.call(mem.symbol("kernel32.dll", "Process32First"), snap, e))
    while more {
        let id = mem.readU32(e + 8)
        if byName {
            if lower(mem.str(e + 44)) == want {
                found = id
                break
            }
        } else if id == pid {
            found = mem.readU32(e + 32)
            break
        }
        more = __vy_procTrue(mem.call(next, snap, e))
    }
    mem.free(e)
    __vy_procClose(snap)
    return found
}

// The pid of the process that started this one, 0 if pid is not
// running. The parent itself may have exited since.
fn __vy_procParent(pid: int) -> int {
    return __vy_procWalk(false, pid, "")
}

// The pid of the first process with this executable name, compared
// without regard to case, or 0.
fn __vy_procFind(name: str) -> int {
    return __vy_procWalk(true, 0, name)
}

// The modules loaded in a process: its executable first, then its
// DLLs. pid 0 is the running process. An empty list means the process
// could not be looked into, which is the same rights question as
// proc.open.
fn __vy_procModules(pid: int) -> []ProcModule {
    let out: []ProcModule = []
    let snap = __vy_procSnap(0x18, pid)
    if snap == -1 {
        return out
    }
    let next = mem.symbol("kernel32.dll", "Module32Next")
    let e = mem.alloc(568)
    mem.write32(e, 568)
    let more = __vy_procTrue(mem.call(mem.symbol("kernel32.dll", "Module32First"), snap, e))
    while more {
        push(out, ProcModule{name: mem.str(e + 48), path: mem.str(e + 304), base: mem.readI64(e + 24), size: mem.readU32(e + 32)})
        more = __vy_procTrue(mem.call(next, snap, e))
    }
    mem.free(e)
    __vy_procClose(snap)
    return out
}

// Where a module is loaded in a process, or 0. An empty name means the
// executable itself.
fn __vy_procBase(pid: int, name: str) -> int {
    let want = lower(name)
    for m in __vy_procModules(pid) {
        if want == "" || lower(m.name) == want {
            return m.base
        }
    }
    return 0
}

// The id of every thread of a process. pid 0 is the running process.
// A thread snapshot is always of the whole system, so the owner is
// checked here.
fn __vy_procThreads(pid: int) -> []int {
    let out: []int = []
    let owner = pid
    if owner == 0 {
        owner = __vy_procId()
    }
    let snap = __vy_procSnap(4, 0)
    if snap == -1 {
        return out
    }
    let next = mem.symbol("kernel32.dll", "Thread32Next")
    let e = mem.alloc(28)
    mem.write32(e, 28)
    let more = __vy_procTrue(mem.call(mem.symbol("kernel32.dll", "Thread32First"), snap, e))
    while more {
        if mem.readU32(e + 12) == owner {
            push(out, mem.readU32(e + 8))
        }
        more = __vy_procTrue(mem.call(next, snap, e))
    }
    mem.free(e)
    __vy_procClose(snap)
    return out
}

// The committed regions of a process's address space, lowest first.
// Free and merely reserved ranges are left out: there is nothing in
// them to read. readable is false for a guard page and for a region
// with no access, whatever else its protection says.
fn __vy_procRegions(h: int) -> []ProcRegion {
    let out: []ProcRegion = []
    let query = mem.symbol("kernel32.dll", "VirtualQueryEx")
    let info = mem.alloc(48)
    let addr = 0
    while mem.call(query, h, addr, info, 48) != 0 {
        let base = mem.readI64(info)
        let size = mem.readI64(info + 24)
        if size <= 0 {
            break
        }
        if mem.readU32(info + 32) == 0x1000 {
            let p = mem.readU32(info + 36)
            let low = p & 0xFF
            let open = (p & 0x100) == 0 && low != 1 && low != 0
            let r = ProcRegion{base: base, size: size, protect: p, kind: mem.readU32(info + 40), readable: open, writable: false, executable: false}
            r.writable = open && (low == 0x04 || low == 0x08 || low == 0x40 || low == 0x80)
            r.executable = open && low >= 0x10
            push(out, r)
        }
        let after = base + size
        if after <= addr {
            break
        }
        addr = after
    }
    mem.free(info)
    return out
}

// Every match of a byte pattern in the readable memory of a process.
// Each region is read a megabyte at a time into one buffer, stepping
// back by a pattern's length less one so a match across the seam is
// still found, and found once.
fn __vy_procScanAll(h: int, pattern: str) -> []int {
    let out: []int = []
    let want = __vy_scanPattern(pattern)
    let m = len(want)
    let chunk = 0x100000
    if m == 0 || m >= chunk {
        return out
    }
    let reader = mem.symbol("kernel32.dll", "ReadProcessMemory")
    let buf = mem.alloc(chunk)
    let got = mem.alloc(8)
    for r in __vy_procRegions(h) {
        if !r.readable {
            continue
        }
        let off = 0
        while off < r.size {
            let n = r.size - off
            if n > chunk {
                n = chunk
            }
            mem.write64(got, 0)
            let ok = __vy_procTrue(mem.call(reader, h, r.base + off, buf, n, got))
            let have = mem.readI64(got)
            if ok {
                let at = 0
                while at + m <= have {
                    let hit = __vy_scanFrom(buf + at, have - at, want)
                    if hit < 0 {
                        break
                    }
                    push(out, r.base + off + (hit - buf))
                    at = hit - buf + 1
                }
            }
            if off + n >= r.size {
                break
            }
            off = off + n - (m - 1)
        }
    }
    mem.free(buf)
    mem.free(got)
    return out
}
`
