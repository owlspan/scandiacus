// Extern structs and bytes crossing into Win32: a structure filled in
// by the system, and ReadProcessMemory reading this program's own
// memory the way a memory reader reads another's.

extern struct SYSTEMTIME {
    year: u16
    month: u16
    weekday: u16
    day: u16
    hour: u16
    minute: u16
    second: u16
    ms: u16
}
extern fn GetSystemTime(t: SYSTEMTIME)
extern fn GetCurrentProcessId() -> int
extern fn OpenProcess(access: int, inherit: bool, pid: int) -> ptr
extern fn ReadProcessMemory(h: ptr, addr: ptr, buf: bytes, n: int, got: ptr) -> bool
extern fn CloseHandle(h: ptr) -> bool

let now = SYSTEMTIME{}
GetSystemTime(now)
print(now.year >= 2024)
print(SYSTEMTIME.size)

extern struct Stats { hp: i32, speed: f32, level: u8 }
let secret = Stats{hp: 42, speed: 3.5, level: 7}

// Read our own memory the way a memory reader reads another program's.
let h = OpenProcess(0x0010, false, GetCurrentProcessId())
print(h != 0)
let buf = must(bytes.fill(Stats.size, 0))
let ok = ReadProcessMemory(h, mem.addr(secret), buf, Stats.size, 0)
print(ok)
let copy = Stats(mem.addr(buf))
print(copy)
CloseHandle(h)

// An extern can hand back a view: the executable's own image starts
// with the DOS header, whose first two bytes are "MZ".
extern struct DosHeader { magic: u16, lastPage: u16 }
extern fn GetModuleHandleA(name: ptr) -> DosHeader
let image = GetModuleHandleA(0)
print(image.magic == 0x5A4D)
