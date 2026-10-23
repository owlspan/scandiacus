// str() of a container.
//
// The same code that prints a list, a map or a struct, with its output
// pointed at a buffer instead of at stdout. That is why the rendering
// cannot drift: there is only one of it.

struct Point {
    x: int
    y: str
}

let nums = [3, 1, 2]
let words = ["beta", "alpha"]
let flags = [true, false]
let sizes = [1.5, 2.0]
let ages = {"ana": 31, "bo": 24}
let ranks = {2: "silver", 1: "gold"}
let p = Point{x: 7, y: "seven"}

print(str(nums))
print(str(words))
print(str(flags))
print(str(sizes))
print(str(ages))
print(str(ranks))
print(str(p))

// The point of having it as a string rather than only as output: it
// can be compared, interpolated and concatenated like any other.
print(str(nums) == "[3, 1, 2]")
print("ages are {str(ages)}")
print(str(p) + " and " + str(nums))

let empty: []int = []
print(str(empty))
let none: {str: int} = {}
print(str(none))

// Inside a function, where the buffer is a slot in that frame.
fn describe(who: Point) -> str {
    return "<" + str(who) + ">"
}
print(describe(p))

// A list of structs: the element renderer is the same one again.
let corners = [Point{x: 0, y: "origin"}, Point{x: 1, y: "unit"}]
print(str(corners))
