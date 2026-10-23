// Functions as values.
//
// A function value is a pointer to two words: the environment and the
// code. Captures are by reference, so a closure sees later writes to
// what it captured and its own writes are seen outside.

fn double(n: int) -> int { return n * 2 }

// A declared function used as a value needs no wrapper: the closure
// carries double's own address and an empty environment.
let f = double
print(f(21))

let triple = fn(n: int) -> int { return n * 3 }
print(triple(5))

// Capturing a parameter of a frame that is gone by the time the closure
// runs. This is why captures live in the heap and not in a stack slot.
fn adder(by: int) -> fn(int) -> int {
    return fn(n: int) -> int { return n + by }
}
let add10 = adder(10)
print(add10(5))
print(adder(100)(1))

// Two closures over one variable see each other, and the enclosing
// scope sees both.
let seen = 0
let tally = fn(n: int) { seen += n }
let reset = fn() { seen = 0 }
tally(3)
tally(4)
print(seen)
reset()
tally(9)
print(seen)

// A captured variable written after the closure was made.
let cutoff = 4
let over = fn(n: int) -> bool { return n > cutoff }
cutoff = 7
print(over(5))

// The higher-order list functions, which are what closures were in
// front of.
let nums = [5, 3, 8, 1, 9, 2]
print(map(nums, fn(n: int) -> int { return n * n }))
print(filter(nums, fn(n: int) -> bool { return n > 4 }))
print(reduce(nums, 0, fn(acc: int, n: int) -> int { return acc + n }))
print(any(nums, fn(n: int) -> bool { return n > 8 }))
print(all(nums, fn(n: int) -> bool { return n > 0 }))
each(nums, fn(n: int) { write("{n} ") })
print("")

// sortBy is stable, so equal keys keep their order.
let words = ["pear", "fig", "kiwi", "plum", "banana"]
print(sortBy(words, fn(a: str, b: str) -> bool { return len(a) < len(b) }))
print(words)

// In containers and in a struct field.
let steps: []fn(int) -> int = [double, triple, add10]
let n = 1
for stage in steps {
    n = stage(n)
}
print(n)

struct Rule {
    label: str
    test: fn(int) -> bool
}
let rules: []Rule = [
    Rule{label: "even", test: fn(k: int) -> bool { return k % 2 == 0 }},
    Rule{label: "big", test: fn(k: int) -> bool { return k > 100 }}
]
for rule in rules {
    print("{rule.label} of 42: {rule.test(42)}")
}

let named: {str: fn(int) -> int} = {"twice": double}
print(named["twice"](9))

// Applied on the spot, and a closure that can fail.
print(fn(a: int, b: int) -> int { return a * b }(6, 7))

fn parse(text: str) -> int! {
    if !isInt(text) { return fail("{text} is not a number") }
    return toInt(text)
}
let p: fn(str) -> int! = parse
print(valueOr(p("12"), -1))
print(valueOr(p("x"), -1))
