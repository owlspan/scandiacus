// os.env, the second namespaced library function.
//
// The values are the machine's, so the test compares facts about them
// rather than the strings themselves - both backends read the same
// environment, so every line here matches.

// PATH exists everywhere this compiler runs.
print(os.env.has("PATH"))
print(len(os.env.get("PATH")) > 0)

// An unset variable: has() is false and get() is the empty string, not
// a null pointer. That distinction is the whole reason get() has a
// branch in it - C's getenv returns NULL and Scandiacus has no such value.
print(os.env.has("SCANDIACUS_DEFINITELY_NOT_SET_XYZ"))
print(os.env.get("SCANDIACUS_DEFINITELY_NOT_SET_XYZ"))
print(len(os.env.get("SCANDIACUS_DEFINITELY_NOT_SET_XYZ")))
print(os.env.get("SCANDIACUS_DEFINITELY_NOT_SET_XYZ") == "")

// It is an ordinary str, so the string builtins all apply.
let p = os.env.get("PATH")
print(contains(p, ":") || contains(p, "\\") || len(p) > 0)
print(len(upper(p)) == len(p))

// In a condition and in a function.
fn present(name: str) -> str {
    if os.env.has(name) {
        return "set"
    }
    return "unset"
}

print(present("PATH"))
print(present("SCANDIACUS_DEFINITELY_NOT_SET_XYZ"))
