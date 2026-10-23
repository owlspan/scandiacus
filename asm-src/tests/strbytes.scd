// A string knows its length, so a zero byte inside one is just a byte:
// len, +, ==, ordering, indexing, files and bytes all see all of it.
let s = "ab\0cd"
print(len(s))
print(len(s + s))
print(s == "ab\0cd")
print(s == "ab\0ce")
print(s == "ab")
print("ab" < "ab\0")
print(__strAt(s, 2))
print(__strAt(s, 4))
print(len(__chr(0)))
print(indexOf(s, "cd"))
print(len(split(s, "\0")))
let b = bytes.of(s)
print(len(b))
print(bytes.str(b) == s)
let path = os.dir.temp() + "/scandiacus-strbytes.bin"
must(os.file.write(path, s + "\0end"))
let back = must(os.file.read(path))
print(len(back))
print(back == s + "\0end")
must(os.file.delete(path))
let m: {str: int} = {}
m["k\0a"] = 1
m["k\0b"] = 2
print(len(m))
print(m["k\0b"])
print(sort(["b\0", "b", "a\0z"]) == ["a\0z", "b", "b\0"])
print(upper("x\0y") == "X\0Y")
print(len("{s}{s}"))
print(s)
print("after")
