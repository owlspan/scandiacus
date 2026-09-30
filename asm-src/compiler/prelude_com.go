package main

// com: calling COM interfaces, in Veyl.
//
// A COM object is a pointer to a pointer to a table of function
// addresses, and calling a method is calling the address in one slot of
// that table with the object as the first argument. DirectX, WIC, WMI,
// the shell and most Windows APIs newer than the flat Win32 ones are
// reached this way and no other.
//
// The call itself, com.call, is lowered in memlib.go beside mem.call,
// because it takes whatever arguments the method does. Everything
// around it is here and is ordinary Veyl: GUIDs from their text form,
// CoCreateInstance, the three IUnknown methods every interface starts
// with, and the UTF-16 strings COM passes.
//
// mem.wide and mem.wstr live here too. They are not COM's alone - every
// W function in the Windows API wants them - but COM is where a program
// first cannot do without.

const preludeCom = `
// A str as UTF-16 with its terminating zero, which is what a Windows
// function taking a wide string expects to be pointed at.
fn __vy_memWide(s: str) -> bytes {
    let conv = mem.symbol("kernel32.dll", "MultiByteToWideChar")
    // A length of -1 means up to and including the terminator, so the
    // count that comes back has room for it.
    let n = mem.call(conv, 65001, 0, s, -1, 0, 0) & 0xFFFFFFFF
    if n <= 0 {
        return __bytesMake(2)
    }
    let out = __bytesMake(n * 2)
    mem.call(conv, 65001, 0, s, -1, out, n)
    return out
}

// The zero-terminated UTF-16 text at an address, as a str. A null
// address reads as "", like mem.str.
fn __vy_memWstr(p: int) -> str {
    if p == 0 {
        return ""
    }
    let conv = mem.symbol("kernel32.dll", "WideCharToMultiByte")
    let n = mem.call(conv, 65001, 0, p, -1, 0, 0, 0, 0) & 0xFFFFFFFF
    if n <= 1 {
        return ""
    }
    let buf = mem.alloc(n)
    mem.call(conv, 65001, 0, p, -1, buf, n, 0, 0)
    let out = mem.str(buf)
    mem.free(buf)
    return out
}

// An HRESULT the way the documentation and every search engine spell
// it: 0x80004002.
fn __vy_comHex(hr: int) -> str {
    return "0x" + upper(padLeft(bits.toBase(hr & 0xFFFFFFFF, 16), 8, "0"))
}

// What the system has to say about an HRESULT, "" when it has nothing.
fn __vy_comMessage(hr: int) -> str {
    let buf = mem.alloc(1024)
    // FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS
    let n = mem.call(mem.symbol("kernel32.dll", "FormatMessageA"), 0x1200, 0, hr & 0xFFFFFFFF, 0, buf, 1024, 0) & 0xFFFFFFFF
    let out = ""
    if n > 0 {
        out = trim(mem.str(buf))
    }
    mem.free(buf)
    return out
}

// Start COM on this thread, in a single-threaded apartment. True also
// when it was already started; false when the thread is already in the
// other kind of apartment.
fn __vy_comInit() -> bool {
    let hr = int(i32(mem.call(mem.symbol("ole32.dll", "CoInitializeEx"), 0, 2)))
    return hr >= 0
}

fn __vy_comDone() {
    mem.call(mem.symbol("ole32.dll", "CoUninitialize"))
}

// A GUID from its text form, with or without the braces, as the 16
// bytes a COM call takes a pointer to. The first three groups are
// stored as little-endian numbers and the rest as written, which is why
// this is not just the hex digits in order.
fn __vy_comGuid(text: str) -> bytes {
    let t = trim(text)
    if startsWith(t, "{{") && endsWith(t, "}}") {
        t = __substrB(t, 1, len(t) - 1)
    }
    let shape = len(t) == 36 && __substrB(t, 8, 9) == "-" && __substrB(t, 13, 14) == "-" && __substrB(t, 18, 19) == "-" && __substrB(t, 23, 24) == "-"
    let raw = __vy_bytesFromHex(replace(t, "-", ""))
    if !shape || failed(raw) || len(valueOr(raw, bytes.of(""))) != 16 {
        __abortStr("com.guid: " + text + " is not a GUID such as 00000000-0000-0000-C000-000000000046")
    }
    let b = must(raw)
    let order = [3, 2, 1, 0, 5, 4, 7, 6, 8, 9, 10, 11, 12, 13, 14, 15]
    let out = __bytesMake(16)
    let i = 0
    while i < 16 {
        __bytePut(out, i, __byteAt(b, order[i]))
        i += 1
    }
    return out
}

// The text form of the GUID stored at an address, for printing one
// that a call handed back.
fn __vy_comGuidText(p: int) -> str {
    let order = [3, 2, 1, 0, 5, 4, 7, 6, 8, 9, 10, 11, 12, 13, 14, 15]
    let digits = "0123456789ABCDEF"
    let out = ""
    let i = 0
    while i < 16 {
        if i == 4 || i == 6 || i == 8 || i == 10 {
            out = out + "-"
        }
        let v = mem.readU8(p + order[i])
        out = out + __substrB(digits, v >> 4, (v >> 4) + 1) + __substrB(digits, v & 15, (v & 15) + 1)
        i += 1
    }
    return out
}

// CoCreateInstance: a new object of a class, through one of its
// interfaces. The class may live in a DLL loaded into this process or
// in a server of its own; any will do.
fn __vy_comCreate(clsid: str, iid: str) -> int! {
    let out = mem.alloc(8)
    let hr = int(i32(mem.call(mem.symbol("ole32.dll", "CoCreateInstance"), __vy_comGuid(clsid), 0, 23, __vy_comGuid(iid), out)))
    let obj = mem.readI64(out)
    mem.free(out)
    if hr < 0 || obj == 0 {
        // CO_E_NOTINITIALIZED has one cause and it is worth saying.
        if (hr & 0xFFFFFFFF) == 0x800401F0 {
            return fail("com.create: " + __vy_comHex(hr) + ", COM is not started on this thread - call com.init() first")
        }
        return fail("com.create: " + __vy_comHex(hr))
    }
    return obj
}

// QueryInterface, slot 0 of every interface: the same object through
// another of its interfaces. The result is a new reference and is
// released on its own.
fn __vy_comQuery(obj: int, iid: str) -> int! {
    let out = mem.alloc(8)
    let hr = com.call(obj, 0, __vy_comGuid(iid), out)
    let got = mem.readI64(out)
    mem.free(out)
    if hr < 0 || got == 0 {
        return fail("com.query: " + __vy_comHex(hr))
    }
    return got
}

// AddRef and Release, slots 1 and 2. Both give the new count, which is
// meant for debugging only. Releasing null does nothing.
fn __vy_comAddRef(obj: int) -> int {
    return com.call64(obj, 1) & 0xFFFFFFFF
}

fn __vy_comRelease(obj: int) -> int {
    if obj == 0 {
        return 0
    }
    return com.call64(obj, 2) & 0xFFFFFFFF
}

// A BSTR: the string type of automation interfaces such as WMI's. It is
// a wide string with its length stored in front, owned by the system
// allocator, so it is made and freed through these two and read with
// mem.wstr.
fn __vy_comBstr(s: str) -> int {
    return mem.call(mem.symbol("oleaut32.dll", "SysAllocString"), __vy_memWide(s))
}

fn __vy_comBstrFree(p: int) {
    if p != 0 {
        mem.call(mem.symbol("oleaut32.dll", "SysFreeString"), p)
    }
}
`
