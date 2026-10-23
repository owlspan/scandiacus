// Nullables, and equality by contents.
//
// A ?T is one word: zero for nil, otherwise a pointer to a box holding
// the value. The checker does the hard half - it proves `x != nil` and
// marks the uses inside that block, so the lowerer dereferences exactly
// the ones it was told about and never has to work out where that is
// safe.

let maybe: ?int = nil
let given: ?int = 7

print(maybe == nil)
print(given == nil)

// Reading one requires proving it is not nil first.
if given != nil {
    print(given * 2)
} else {
    print("nothing there")
}

// The narrowing lasts for the whole block, nested statements included.
if given != nil {
    let total = 0
    for i in 0..3 {
        total += given
    }
    print(total)
}

// Assignment moves between the two worlds.
let box: ?str = nil
box = "now full"
if box != nil {
    print(upper(box))
}
box = nil
print(box == nil)

// A function can return one, which is how a lookup says "absent"
// without picking a value to mean it.
fn lookup(m: {str: int}, key: str) -> ?int {
    if has(m, key) {
        return m[key]
    }
    return nil
}

let ages = {"ada": 36}
let found = lookup(ages, "ada")
let missing = lookup(ages, "nobody")
print(found != nil)
print(missing == nil)
if found != nil {
    print(found)
}

// find() is the same idea built in: it keeps absent and zero apart,
// where a bare index cannot.
print(find(ages, "ada") != nil)
print(find(ages, "zzz") == nil)

// In a struct, and in a list.
struct Config {
    name: str
    timeout: ?int
}

let a = Config{name: "a", timeout: 30}
let b = Config{name: "b"}
print(a.timeout != nil)
print(b.timeout != nil)

let slots: []?int = [1, nil, 3]
let present = 0
for slot in slots {
    if slot != nil {
        present += slot
    }
}
print(len(slots))
print(present)

// Printing shows the value, or the word nil.
print(str(given))
print(str(maybe))
print("interpolated {given} {maybe}")

// Equality is by contents, for nullables and for everything that holds
// contents. Two separately built lists with the same elements are equal.
print([1, 2, 3] == [1, 2, 3])
print([1, 2] == [2, 1])
print(["x", "y"] == ["x", "y"])
print({"a": 1} == {"a": 1})
print({"a": 1} == {"a": 2})
print([[1], [2]] == [[1], [2]])
print(a == Config{name: "a", timeout: 30})
print(a == b)

let some: ?int = 5
let same: ?int = 5
let none: ?int = nil
print(some == same)
print(some == none)
print(none == nil)
