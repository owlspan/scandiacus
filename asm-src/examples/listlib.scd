// The list library.
//
// Every one of these is a loop over an index, written in the IR rather
// than as an assembly helper, so the byte writer inherits them all.

let nums = [5, 3, 9, 1]
let words = ["pear", "apple", "fig"]

// The ones that produce a list produce a new one. sort and reverse do
// not touch what they were given, which is what the Go backend does and
// is invisible until a program prints the original afterwards.
print(str(sort(nums)))
print(str(nums))
print(str(reverse(words)))
print(str(sort(words)))

print(first(nums))
print(last(nums))
print(sum(nums))
print(join(words, ", "))
print(join(nums, "-"))

// A mixed literal is a list of floats, so the sum is one too.
let mixed = [1, 2.5, 3]
print(str(mixed))
print(sum(mixed))
print(str(sort(mixed)))

// Searching. indexOf answers -1 when there is no match, the same as
// slices.Index.
print(contains(words, "fig"))
print(contains(words, "durian"))
print(indexOf(words, "apple"))
print(indexOf(words, "durian"))
print(contains("hello", "ell"))
print(indexOf("hello", "llo"))

// Slicing clamps rather than failing, matching substr.
print(str(slice(nums, 1, 3)))
print(str(slice(nums, 0, 99)))
print(str(slice(nums, 3, 1)))

// Mutating in place: insert opens a gap, removeAt closes one and hands
// back what was there, pop shortens from the end.
let queue = [10, 20, 30]
insert(queue, 1, 15)
print(str(queue))
print(removeAt(queue, 0))
print(str(queue))
print(pop(queue))
print(str(queue))
push(queue, 40, 50, 60)
print(str(queue))

// Both loop forms.
for i, v in words {
    print(str(i) + " " + v)
}
let total = 0
for v in nums {
    total += v
}
print(total)

// Strings into lists.
print(str(split("a,b,c", ",")))
print(str(split("a,b,", ",")))
print(str(split("one--two", "--")))
print(str(chars("hey")))
print(str(lines("first\nsecond\n")))
print(str(lines("crlf\r\nhandled")))

// And back again.
print(join(split("2026-08-20", "-"), "/"))
print(len(chars("scandiacus")))

// join renders anything that is not already a string, and an empty
// list joins to nothing.
print(join([1.5, 2.25], " | "))
print(join([true, false], "/"))
print(join(["", "", ""], "x"))
let none: []str = []
print("[" + join(none, ",") + "]")
