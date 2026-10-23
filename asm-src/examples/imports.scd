// Multi-file programs.
//
// An import folds the other file's declarations into this one before
// anything is checked, so from the checker down there is a single
// program. Importing the same file twice is harmless.

import "mod/shapes.scd"
import "mod/shapes.scd"

// A top-level const is a real global: it lives in static storage and is
// visible inside functions, unlike a top-level `let`, which stays a
// local of the implicit main.
const LABEL = "shape"

fn tag(b: Box) -> str {
    // LABEL is from this file, UNIT from the import, and both reach in
    // here the same way.
    return LABEL + ": " + describe(b) + " (unit " + str(UNIT) + ")"
}

let a = Box{w: 3.0, h: 4.0}
let b = Box{w: 1.0, h: 2.0}

print(a.area())
print(describe(a))
print(tag(a))
print(circleArea(1.0))
print(UNIT)
print(LABEL)

// A struct from the import works everywhere a local one would.
let all: []Box = [a, b]
let total = 0.0
for x in all {
    total += x.area()
}
print(total)

// And a method on it mutates the original, imported or not.
a.grow(1.0)
print(describe(a))

let byName: {str: Box} = {}
byName["first"] = b
print(describe(byName["first"]))
