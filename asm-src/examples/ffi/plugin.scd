// plugin: a DLL written in Scandiacus.
//
//     scandiacus build --dll plugin.scd       writes plugin.dll
//
// Anything that loads DLLs can load this one: a mod loader, a host
// program's plugin folder, LoadLibrary from C, or another Scandiacus program
// declaring the exports with extern fn ... from "plugin".
//
// The top-level statements run once, as the library is loaded. Windows
// holds a lock while they do, so keep them short: set things up here,
// and do real work when the host calls in.

print("plugin loaded")

// Exported ints are 64-bit (int64_t), floats are doubles, a str is a
// const char*, and a bool is read as a C BOOL.
export fn plugin_version() -> int {
    return 1
}

export fn plugin_greet(name: str) -> int {
    print("hello, {name}, from a Scandiacus plugin")
    return len(name)
}

export fn plugin_scale(value: float, by: float) -> float {
    return value * by
}
