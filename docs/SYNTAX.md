# Veyl Language Reference

**Version 0.41.0** - the language as currently implemented.

Veyl compiles straight to x86-64 and writes the Windows executable
itself. A finished program is a single self-contained `.exe` with no
runtime to install, and building one needs nothing but `veyl.exe`.

> Anything marked **planned** is not implemented yet. If you write it,
> the compiler will reject it.

---

## Contents

1. [Hello, world](#hello-world)
2. [Comments](#comments)
3. [Statements and lines](#statements-and-lines)
4. [Variables](#variables)
5. [Types](#types)
6. [Enums](#enums)
7. [Generics](#generics)
8. [Interfaces](#interfaces)
9. [Operators](#operators)
10. [Strings and interpolation](#strings-and-interpolation)
11. [Control flow](#control-flow)
12. [Functions](#functions)
13. [Builtin library](#builtin-library)
14. [Native functions (`extern`)](#native-functions-extern)
15. [`proc` - process memory](#proc---process-memory)
16. [`win` - windows, drawing and input](#win---windows-drawing-and-input)
17. [Reserved words](#reserved-words)
18. [Compiler commands](#compiler-commands)
19. [Known limitations](#known-limitations)

---

## Hello, world

```veyl
print("Hello, world!")
```

```
veyl run hello.vl
```

Source files use the `.vl` extension. There is no required `main`
function - statements at the top level of a file run in order, and
declaring a `main` yourself is an error, because the compiler writes
its own around those statements.

---

## Comments

```veyl
// a line comment

/* a block comment
   /* which can nest */
   all the way to here */
```

Nesting matters in practice: you can comment out a region that already
contains a block comment without the inner `*/` ending it early.

---

## Statements and lines

Statements end at a line break. There are no semicolons.

```veyl
let a = 1
let b = 2
```

Blocks use braces, not indentation. Indentation is style only - the
compiler ignores it.

```veyl
if a < b {
    print("smaller")
}
```

Inside brackets - `(...)` in a call or a grouped expression - line
breaks are ignored, so long argument lists can wrap:

```veyl
print(
    "this",
    "spans",
    "lines"
)
```

---

## Variables

`let` declares a variable. `const` declares one that cannot be reassigned.

```veyl
let count = 0
const limit = 10

count = 5          // fine
count += 1         // fine
limit = 11         // error: cannot assign to "limit" because it was declared const
```

The type is inferred from the value. Annotate it explicitly with `: Type`
when you want to be specific:

```veyl
let name: str = "Veyl"
let ratio: float = 0.5
```

A name may be declared only once per scope, but an inner block may shadow
an outer name:

```veyl
let x = 1
{
    let x = 2       // a different variable
    print(x)        // 2
}
print(x)            // 1
```

Reading an undeclared variable is an error, not an implicit declaration:

```veyl
total = 5           // error: undefined variable "total"
```

---

## Types

| Veyl  | Holds                        | Example        |
| ------- | ---------------------------- | -------------- |
| `int`     | whole numbers                | `42`, `-7`          |
| `float`   | numbers with a decimal point | `3.14`, `0.5`       |
| `str`     | text                         | `"hello"`           |
| `bool`    | `true` or `false`            | `true`              |
| `[]T`     | a list of `T`                | `[1, 2, 3]`         |
| `{K: V}`  | a map from `K` to `V`        | `{"a": 1}`          |
| `?T`      | a `T`, or nothing            | `nil`               |
| `T!`      | a `T`, or a reason it failed | `fail("bad input")` |
| `fn(A) -> B` | a function                | `fn(n: int) -> int { ... }` |
| `i8` ... `u64`, `f32` | a number of a fixed width | see [Fixed-width numbers](#fixed-width-numbers) |
| `*T`      | the address of a `T` in C memory | see [Pointers](#pointers) |

A number literal is an `int` unless it contains a `.`, in which case it
is a `float`.

Integers can be written in decimal, hex or binary, and `_` may be used
to group digits anywhere:

```veyl
let mask  = 0xFF          // 255
let bits  = 0b1010_1010   // 170
let big   = 1_000_000
let ratio = 1_234.5
```

Veyl does not convert between types automatically. Mixing them is an
error - use `str()`, `int()`, or `float()` to convert explicitly.

```veyl
let n = 5
print("count: " + n)        // error: cannot add str and int
print("count: " + str(n))   // fine
print("count: {n}")         // better - interpolation handles it
```

### The rules

The compiler knows the type of every expression and checks it before
generating any code. The rules are short:

| Construct | Rule |
| --- | --- |
| `+` | two numbers of the same type, or two `str` |
| `- * /` | two numbers of the same type |
| `%` | two `int` - use `mod()` for floats |
| `< <= > >=` | two numbers of the same type, or two `str` |
| `== !=` | two values of the same type; lists, maps and structs compare by contents |
| `&& \|\| !` | `bool` only |
| `if` / `while` | the condition must be `bool` - `if 5` is an error |
| `let x: T = v` | `v` must be a `T` |
| `x = v` | `v` must match the type `x` was declared with |
| `return v` | `v` must match the function's return type |
| `f(a, b)` | each argument must match the parameter's type |
| `for i in a..b` | the bounds and the `step` must be `int` |

There is **no implicit conversion between two values**. This is an
error, and the message says so in Veyl's words:

```veyl
let a = 1
let b = 2.5
let c = a + b   // error: cannot mix int and float in '+'
                //        convert one with int(...) or float(...)
```

### Integer literals are flexible

The one exception is a plain integer literal, which will happily become
a float where one is expected. This follows Go's untyped-constant rule
and keeps ordinary arithmetic readable:

```veyl
let radius = 2.5
let area = PI * radius * radius   // fine
let wide  = radius * 2            // fine - 2 becomes 2.0
let ratio: float = 1              // fine
```

The flexibility belongs to the *literal*, not to the type. Once a value
is in a variable, its type is fixed:

```veyl
let two = 2                       // an int variable
let nope = radius * two           // error: cannot mix float and int
let ok   = radius * float(two)    // fine
```

### Division

`/` between two `int` values is integer division, so `7 / 2` is `3`.
That is now a stated rule rather than a leaked backend detail - the
compiler knows both operands are `int` and emits integer division
deliberately.

For a fractional result, use `divf()` or make a side a float:

```veyl
print(7 / 2)          // 3
print(divf(7, 2))     // 3.5
print(7.0 / 2.0)      // 3.5
```

### Fixed-width numbers

`int` and `float` are 64 bits. When a program needs the widths C code,
file formats and other programs' memory use, there are these:

| Type | Holds |
| --- | --- |
| `i8` `i16` `i32` | signed, -128 to 127, -32768 to 32767, and so on |
| `u8` `u16` `u32` `u64` | unsigned, 0 to 255, 0 to 65535, and so on |
| `f32` | a single-precision float, what C calls `float` |

`i64` and `f64` are other names for `int` and `float`.

They are ordinary types: a variable, a parameter, a return, a field, a
list element or a map value can be one. Arithmetic wraps at the type's
width, the way C's does:

```veyl
let a: u8 = 250
print(a + 10)             // 4
let b: i8 = 127
print(b + 1)              // -128
let c: u32 = 0
print(c - 1)              // 4294967295
let h: u32 = 2166136261
h *= 16777619             // wraps at 32 bits, no masking needed
```

Unsigned values divide, shift right and compare as unsigned: a `u64`
above 2^63 is a large number, not a negative one, and `>>` on any
unsigned type fills with zeros. A signed division that overflows,
`i32(-2147483648) / -1`, wraps instead of stopping the program. An
`f32` rounds to single precision after every operation, so it gives
exactly the answers C's `float` does, and prints as the shortest
decimal that reads back to the same `f32`:

```veyl
let x: f32 = 0.1
print(x + 0.2)            // 0.3
print(x + 0.2 == 0.3)     // true, in single precision
print(float(x))           // 0.10000000149011612
```

There are still no implicit conversions. `i8(x)`, `u8(x)`, `i16(x)`,
`u16(x)`, `i32(x)`, `u32(x)`, `u64(x)`, `f32(x)`, `int(x)` and
`float(x)` convert between any two number types. An integer narrows by
keeping its low bits, like a C cast, and a float truncates toward zero:

```veyl
let big = 300
print(u8(big))            // 44
print(i8(200))            // -56
print(u32(-1))            // 4294967295
print(i32(-3.99))         // -3
let n: i32 = 5
let total = n + 1         // fine: 1 is a literal
let m = 1
let bad = n + m           // error: cannot mix i32 and int
let good = n + i32(m)     // fine
```

A literal, or an expression made only of literals, fits any of them
when its value is in range, and is an error when it is not:

```veyl
let mask: u32 = (255 << 16) | 7    // fine
let full: u64 = 0xFFFFFFFFFFFFFFFF // fine
let over: u8 = 256                 // error: 256 does not fit in u8
```

A shift's count can be any integer type. `%`, `&`, `|`, `^`, `~`, `<<`
and `>>` work on the integer types, not on `f32`. Reading memory at a
width gives that width's type: a `*u16` reads a `u16`, and an extern
struct's `i32` field reads an `i32`.

A value of one is still a single machine word, sign- or zero-extended
from its width, so it costs what an `int` does; the extra instruction
that puts a result back into range is one `movzx`, `movsx` or 32-bit
`mov`.

### Lists and maps

`[]T` is a list of `T`. `{K: V}` is a map from `K` to `V`, where the key
must be `str` or `int`.

```veyl
let nums  = [3, 1, 2]                  // []int
let words = ["beta", "alpha"]          // []str
let ages  = {"ada": 36, "alan": 41}    // {str: int}
```

They nest without any special rules:

```veyl
let grid:   [][]int      = [[1, 2], [3, 4]]
let groups: {str: []str} = {}
```

An **empty literal carries no element type**, so it needs an annotation.
The compiler says exactly that if you forget:

```veyl
let xs: []int = []          // fine
let m: {str: int} = {}      // fine
let oops = []               // error: cannot tell what kind of list
                            //        this is - annotate it
```

Printing uses Veyl's own notation, so a printed value is something you
could paste back into your program:

```veyl
print([1, 2, 3])                   // [1, 2, 3]
print({"a": 1})                    // {"a": 1}
print(["x", "y"])                  // ["x", "y"]
```

### Nullable types

A plain type can **never** be nil. `?T` is the type that can.

```veyl
let name: str  = "ada"    // always holds a string
let note: ?str = nil      // holds a string, or nothing
```

This is the main thing Veyl offers over its own backend: there is no
such thing as a nil `str`, so there is no such thing as a nil-dereference
crash. The cost is that a `?T` has to be checked before it can be used.

**Checking narrows the type.** Inside a proven `!= nil`, the value is a
plain `T`:

```veyl
let note: ?str = maybeLoad()

if note != nil {
    print(upper(note))     // note is a str in here
}
print(upper(note))         // error: ?str might be nil
```

It works in either direction, and through `&&`:

```veyl
if note == nil {
    print("nothing")
} else {
    print(note)            // narrowed in the else branch
}

if note != nil && len(note) > 3 {
    print("long enough")
}
```

The narrowing is deliberately literal-minded: it understands
`x != nil`, `x == nil`, and `&&` chains of them. Anything cleverer would
be harder to predict than it is worth.

**Widening is automatic.** A `T` goes into a `?T` without ceremony:

```veyl
let n: ?int = 5           // fine
```

Going the other way needs the check - that is the whole point.

**A bare `nil` needs a type to aim at**, so a binding that starts empty
must say what it will hold:

```veyl
let x: ?int = nil         // fine
let y = nil               // error: cannot tell what "y" can hold
```

Nullables work anywhere a type does - struct fields, list elements, map
values, parameters and return types:

```veyl
struct Config {
    name:    str
    timeout: ?int
}

fn lookup(m: {str: int}, key: str) -> ?int {
    if has(m, key) {
        return m[key]
    }
    return nil
}
```

`find(m, k)` is the nil-safe counterpart to `m[k]`: it returns `?V`, so
a missing key is distinguishable from a key holding zero.

### Results - things that can fail

`T!` is either a `T`, or a reason it is missing. Where `?T` says
"there might be nothing here", `T!` says "this might not have worked,
and here is why".

```veyl
fn parsePort(text: str) -> int! {
    if !isInt(text) {
        return fail("{text} is not a number")
    }
    return toInt(text)
}
```

Inside a function returning `T!`, `return value` succeeds and
`return fail("...")` does not. There is nothing else to write.

**A result is not a value until you unwrap it.** Using one directly is
an error, which is the whole point:

```veyl
let port = parsePort("8080")
print(port + 1)         // error: int! might have failed
```

Four ways to get at it:

| Way | Meaning |
| --- | --- |
| `must(r)` | the value, or stop the program with the reason |
| `valueOr(r, alt)` | the value, or `alt` if it failed |
| `isOk(r)` / `failed(r)` | test it first |
| `errorOf(r)` | the reason, or `""` if it worked |
| `r?` | the value, or return the failure from *this* function |

### The `?` operator

`?` is the reason the error type is worth having. It unwraps a result,
or returns its failure from the enclosing function:

```veyl
fn addressFor(host: str, port: str) -> str! {
    let n = parsePort(port)?      // on failure, addressFor returns it
    return "{host}:{n}"
}
```

That is Go's four-line `if err != nil` dance in one character.

It works mid-expression, and chains - the first failure wins and
nothing after it runs:

```veyl
fn sumPorts(a: str, b: str) -> int! {
    return parsePort(a)? + parsePort(b)?
}
```

`?` only makes sense in a function that can itself fail, so the
enclosing function must return a `T!`. The compiler says so if not.

### Composing with nullables

`?T` and `T!` stack, and the order means different things:

- `?int!` - it might have failed; if it worked, there might be no value.
- `int!` inside a `?` - not a thing; wrap the other way round.

```veyl
fn maybePort(text: str) -> ?int! {
    if text == "" {
        return nil        // worked, and there is nothing
    }
    return parsePort(text)?
}
```

The standard library uses all of this: `os.file.read` returns `str!`,
`http.get` returns `str!`, and so on. See [Libraries](#libraries).

---

## Structs

A `struct` groups named values into a new type.

```veyl
struct Point {
    x: float
    y: float
}
```

Fields go one per line, or separated by commas - whichever reads better.

### Making one

Name the struct, then give the fields in any order. **Fields you leave
out take their zero value**, so `Point{}` is a valid origin.

```veyl
let origin = Point{}
let p = Point{x: 3.0, y: 4.0}
let q = Point{y: 1.0, x: 2.0}    // order does not matter
```

Read and write fields with `.`:

```veyl
print(p.x)
p.x = 6.0
```

### Methods

Methods go in an `impl` block. The first parameter is always `self`.

```veyl
impl Point {
    fn length(self) -> float {
        return sqrt(self.x * self.x + self.y * self.y)
    }

    fn scale(self, by: float) {
        self.x *= by
        self.y *= by
    }
}

print(p.length())
p.scale(0.5)
```

A method may change `self`, as `scale` does. A method and a field
cannot share a name.

### Copying

**Assigning a struct copies it.** The two values are independent
afterwards - there are no references and no aliasing to reason about.

```veyl
let a = Point{x: 1.0, y: 1.0}
let b = a
b.x = 99.0
print(a.x)      // still 1
```

The exception is a method call, which acts on the original rather than a
copy - otherwise `scale` could not work at all.

### Structs and collections

They nest in both directions:

```veyl
struct User {
    name: str
    tags: []str
}

let people: []User = []
push(people, User{name: "ada", tags: ["maths"]})
push(people[0].tags, "computing")     // changes the element in place

let byName: {str: Point} = {}
byName["origin"] = Point{}
```

A struct cannot contain **itself** by value - the type would need
infinite space. Through a list it is fine:

```veyl
struct Node {
    value:    int
    children: []Node    // fine
}
```

### One syntax wrinkle

`if p {` is ambiguous: `p` could be a variable, or the start of a struct
literal `p{...}`. Veyl resolves it the way Go does - a struct literal
cannot appear unparenthesised in an `if`, `while` or `for` header:

```veyl
if ready { }                                  // fine
if (Point{x: 1.0, y: 0.0}).length() > 0.5 { } // parens needed
```

### Printing

Structs print in the same notation you would write them in:

```veyl
print(Point{x: 3.0, y: 4.0})    // Point{x: 3, y: 4}
```

---

## Enums

An enum is a type with a fixed set of named values:

```veyl
enum State { Idle, Running, Done }

enum Dir {
    North
    East
    South
    West
}

var state = State.Idle
```

A value is written `State.Idle`. Two values of the same enum compare
with `==` and `!=`; there is no arithmetic on them, and an enum is never
mixed with an `int` or with another enum. They print by name:

```veyl
print(state)                  // Idle
print("now {Dir.West}")       // now West
print([Dir.North, Dir.East])  // [North, East]
```

A `match` on an enum with no `else` has to handle every value. Adding a
value to the enum later then makes each match that forgot it an error,
naming what is missing:

```veyl
fn next(s: State) -> State {
    match s {
        State.Idle => return State.Running
        State.Running => return State.Done
        State.Done => return State.Idle
    }
    return s
}
```

`enum` is only a keyword at the top of a file, before a name and a
brace, so a variable called `enum` still works.

### Variants that carry values

A variant can hold values, listed like a function's parameters:

```veyl
enum Shape {
    Circle(r: float)
    Rect(w: float, h: float)
    Empty
}

let shapes = [Shape.Circle(2.0), Shape.Rect(3.0, 4.5), Shape.Empty]
print(shapes)          // [Circle(2), Rect(3, 4.5), Empty]
```

A `match` names the values each variant holds, `_` for one it does not
need, and without an `else` it has to handle every variant:

```veyl
fn area(s: Shape) -> float {
    match s {
        Shape.Circle(r) => return 3.14159 * r * r
        Shape.Rect(w, h) => return w * h
        Shape.Empty => return 0.0
    }
    return 0.0
}
```

That is the only way to reach what a variant holds: `s.r` is an error,
because `s` might not be a circle. Two values are equal when they are
the same variant holding equal values. A value never changes once it
is built.

A variant can hold the enum itself, which makes trees easy:

```veyl
enum Expr {
    Num(n: int)
    Add(a: Expr, b: Expr)
    Neg(x: Expr)
}

fn eval(e: Expr) -> int {
    match e {
        Expr.Num(n) => return n
        Expr.Add(a, b) => return eval(a) + eval(b)
        Expr.Neg(x) => return -eval(x)
    }
    return 0
}
```

An enum can be generic, as a struct can. The type arguments come from
what a variant is given, or from where the value is going:

```veyl
enum Option<T> {
    Some(v: T)
    None
}

fn indexOf(xs: []str, want: str) -> Option<int> {
    for i, x in xs {
        if x == want {
            return Option.Some(i)
        }
    }
    return Option.None
}

match indexOf(["a", "b"], "b") {
    Option.Some(i) => print("at {i}")
    Option.None => print("not there")
}
```

A value of `Option<str>.None` with nothing to say what it holds is
written with its type: `let none = Option<str>.None`.

---

## Generics

A function or struct can take types as parameters, written in angle
brackets after its name:

```veyl
fn largest<T>(xs: []T) -> T {
    let best = xs[0]
    for x in xs {
        if x > best {
            best = x
        }
    }
    return best
}

print(largest([3, 9, 2]))          // 9
print(largest(["pear", "zoo"]))    // zoo
```

The types come from the arguments, so a call looks like any other.
Where the arguments cannot say - an empty list, or a type that appears
only in the result - name them:

```veyl
let none: []int = []
let words = mapList<int, str>(none, fn(x: int) -> str { return str(x) })
```

A struct names its types the same way, and so does every use of it.
Methods go in `impl Name<T>`, where `T` means whatever the struct was
made with:

```veyl
struct Stack<T> {
    items: []T
}

impl Stack<T> {
    fn push(self, x: T) {
        push(self.items, x)
    }
    fn pop(self) -> T {
        return pop(self.items)
    }
}

let s = Stack<int>{}
s.push(1)
let pairs = Stack<Pair<str, int>>{}
```

Any type can be a type argument: a number, `str`, a list or map, a
function type, a nullable, another struct, an enum or another generic.

### How it works

Each set of types a generic is used with gets its own copy, checked
and compiled like code written out by hand: `largest` called on a
`[]int` and on a `[]str` is two functions, `largest<int>` and
`largest<str>`, and each is as fast as if it had been written for its
type. This is how C++ templates work, and it has the same two
consequences:

- **A generic is checked when it is used, per use.** `x > best` in
  `largest` is fine for `int` and `str`. With a struct it is an error,
  reported at the line in `largest` with `(in largest<Point>)` after
  it. A generic nothing uses is never checked at all.
- **Every instance is code of its own**, so a generic used with twenty
  types is twenty copies in the executable.

A method can have type parameters of its own, besides its struct's.
Theirs come from the arguments, as a function's do, or are named at
the call, as in `bag.empty<str>()`:

```veyl
impl Box<T> {
    fn map<U>(self, f: fn(T) -> U) -> Box<U> {
        return Box<U>{v: f(self.v)}
    }
}

let n = Box<int>{v: 21}
let s = n.map(fn(x: int) -> str { return "{x * 2}" })   // a Box<str>
```

`f<A, B>(x)` in an expression is read as a call naming its types, so a
pair of comparisons written as `f(a < b, c > (d))` needs brackets:
`f((a < b), c > (d))`.

---

## Interfaces

An interface lists methods. Any struct that has them, with the same
parameter and result types, can be used as one - nothing on the struct
says so, the way Go does it:

```veyl
interface Shape {
    fn area(self) -> float
    fn name(self) -> str
}

struct Square { side: float }
impl Square {
    fn area(self) -> float { return self.side * self.side }
    fn name(self) -> str { return "square" }
}

struct Circle { r: float }
impl Circle {
    fn area(self) -> float { return 3.14159 * self.r * self.r }
    fn name(self) -> str { return "circle" }
}

let shapes: []Shape = [Square{side: 2.0}, Circle{r: 1.0}]
push(shapes, Square{side: 1.0})
for s in shapes {
    print("{s.name()}: {s.area()}")
}
```

A struct becomes the interface wherever one is wanted: a parameter, a
`let` with the type, a list or map element, a return, `push`, `==`.
One missing a method, or with a method of the wrong type, is an error
saying which:

```
Box is not a Shape: area is fn(self) -> int where Shape needs fn(self) -> float
```

Calls go to the struct inside, and a method that changes `self`
changes the value the interface holds. Printing one prints that
struct. Its fields are not reachable through the interface; a `match`
names the structs it could be and gets the one it is:

```veyl
match s {
    Shape.Circle(c) => print("radius {c.r}")
    Shape.Square(sq) => print("side {sq.side}")
    else => print("something else")
}
```

The zero value of an interface - a struct field of one that a literal
left out - holds no struct, and calling a method on it stops the
program with a message naming the interface.

`interface` is only a keyword at the top of a file, before a name and a
brace.

### How it works

The whole program is compiled at once, so every struct ever used as a
`Shape` is known by the end. A `Shape` is kept as a tagged value, like
an enum whose variants are those structs, and each of its methods is a
branch on the tag that calls the struct's own. There is no vtable to
look up and nothing allocated beyond the value itself.

---

## Operators

Highest precedence first:

| Precedence | Operators           | Meaning                       |
| ---------- | ------------------- | ----------------------------- |
| 11         | `!x` `-x` `~x`      | not, negation, bitwise not    |
| 10         | `*` `/` `%`         | multiply, divide, remainder   |
| 9          | `+` `-`             | add, subtract                 |
| 8          | `<<` `>>`           | bit shifts                    |
| 7          | `<` `<=` `>` `>=`   | comparison                    |
| 6          | `==` `!=`           | equality                      |
| 5          | `&`                 | bitwise and                   |
| 4          | `^`                 | bitwise xor                   |
| 3          | `\|`                | bitwise or                    |
| 2          | `&&`                | logical and                   |
| 1          | `\|\|`              | logical or                    |

All binary operators are left-associative: `a - b - c` means `(a - b) - c`.

Use `( )` to group explicitly:

```veyl
let a = 2 + 3 * 4        // 14
let b = (2 + 3) * 4      // 20
```

`+` also joins strings. `&&` and `||` short-circuit - the right side is
not evaluated if the left already decides the result.

Compound assignment: `+=` `-=` `*=` `/=` `%=` `&=` `|=` `^=` `<<=` `>>=`.

### Bitwise operators

`&` `|` `^` `~` `<<` `>>` work on `int` only. Masks read better in hex
or binary - `0xFF`, `0b1010` - and `_` can group the digits.

```veyl
let flags = 0
flags |= 4          // set a bit
flags &= ~4         // clear it
print(flags & 1)    // test it
```

**The precedence ladder is C's**, which means comparison binds *tighter*
than `&`, `^` and `|`. So this does not do what it looks like:

```veyl
if flags & 4 == 4 { }      // parses as flags & (4 == 4)
if (flags & 4) == 4 { }    // what you meant
```

Veyl keeps C's ordering so the operators behave the way people expect
from elsewhere, and the type checker catches the mistake with a message
that says to add parentheses.

**Note:** `int / int` truncates toward zero, so `7 / 2` is `3`. For a
fractional result, make one side a `float`.

---

## Strings and interpolation

String literals use double quotes. Escapes: `\n`, `\t`, `\r`, `\\`,
`\"`, `\0`, and two for writing any byte or character in plain ASCII
source:

| Escape | Means |
| --- | --- |
| `\xHH` | the byte with that hex value, exactly two digits |
| `\u{H...}` | the Unicode code point, one to six hex digits, as UTF-8 |

```veyl
print("caf\u{e9}")          // cafe with an acute accent
print(len("\u{e9}"))        // 2 - len counts bytes
print("\x41\x42")           // AB
```

A string is UTF-8 bytes. `len` and `indexOf` count bytes; `charAt`,
`substr`, `chars`, `padLeft` and `padRight` count characters. A byte
that does not begin a valid character reads as one character, U+FFFD.

### Raw strings

Backticks give a string with **no escapes and no interpolation**, which
may span lines:

```veyl
const pattern = `\d{4}-\d{2}-\d{2}`
const table = `name,age
ada,36`
```

Use them for text that is already full of backslashes and braces - a
regular expression, a block of CSV, a chunk of JSON. Quoting that twice
is what goes wrong: in an ordinary string `\d` is an unknown escape and
`{4}` is an interpolation.

There is no way to put a backtick inside one. Use an ordinary string
for that.

Any expression inside `{ }` is evaluated and inserted:

```veyl
let name = "world"
let n = 3

print("hello, {name}")
print("n squared is {n * n}")
print("big: {n > 2 && name != ""}")
```

String literals may appear inside an interpolation, so this works:

```veyl
print("shouting: {upper("hello")}")
print("fixed: {replace(path, "\\", "/")}")
```

For a literal brace, double it:

```veyl
print("{{literal braces}}")     // {literal braces}
```

Interpolation compiles to a formatting call, so a bare `%` in a string
is safe - it needs no escaping.

---

## Control flow

### if / else if / else

```veyl
if score >= 90 {
    print("A")
} else if score >= 80 {
    print("B")
} else {
    print("C")
}
```

The condition needs no parentheses. Braces are required even for a
single statement.

### match

A multi-way branch on one value. Arms may list several values, and
**they do not fall through** - there is no `break` to forget.

```veyl
match code {
    200      => print("ok")
    301, 302 => print("redirect")
    404, 410 => print("gone")
    else     => print("something else")
}
```

An arm's body is a single statement, or a block:

```veyl
match n % 3 {
    0 => {
        print("divisible by three")
        total += n
    }
    else => print("not")
}
```

The subject must be an `int`, `float`, `str`, `bool` or an enum - match
compares values, so lists, maps and structs cannot be matched on. A
match on an enum without an `else` has to cover every value; see
[Enums](#enums). Every arm has
to have the same type as the subject, and repeating a value is an error
rather than dead code.

`else` is optional. Without one, a value that matches nothing simply
does nothing. With one, and if every arm returns, the compiler counts
the match as returning on every path.

### while

```veyl
let i = 0
while i < 10 {
    print(i)
    i += 1
}
```

### for

Counted loops use a range. `..` excludes the end, `..=` includes it.

```veyl
for i in 0..5 { print(i) }      // 0 1 2 3 4
for i in 1..=5 { print(i) }     // 1 2 3 4 5
```

`step` sets the increment. A negative step counts down.

```veyl
for i in 0..=100 step 25 { print(i) }   // 0 25 50 75 100
for i in 10..0 step -2 { print(i) }     // 10 8 6 4 2
```

The bounds are evaluated once, before the loop starts, so a function
call in the range is not re-run on every iteration.

The loop variable is scoped to the loop and may shadow an outer name
without disturbing it.

### defer

`defer` puts a statement off until the block it is in is left, however
it is left: its end, a `return`, or a `break` or `continue`. Several
run latest first:

```veyl
fn save(m: int, path: str) -> bool {
    thread.lock(m)
    defer thread.unlock(m)      // on every way out below
    if !os.file.exists(path) {
        return false
    }
    ...
    return true
}
```

It belongs to the block, not the function, so inside a loop it runs at
the end of each trip - which is what makes `defer delete(xs)` in a
`gc off` loop free each trip's list on that trip. The statement is run
as it is on the way out, so `defer print(x)` prints `x`'s value then.
It cannot itself `return`, `break` or `continue`.

### break and continue

```veyl
for i in 0..100 {
    if i % 2 == 0 { continue }   // skip to the next iteration
    if i > 20 { break }          // leave the loop
    print(i)
}
```

Both work in `for` and `while`, and both apply to the innermost loop.
Using either outside a loop is a compile error.

### Iterating a collection

`for x in list` walks the elements. A second name gives the index too.

```veyl
for w in words { print(w) }
for i, w in words { print("{i}: {w}") }
```

A map binds two names, key and value:

```veyl
for name, age in ages { print("{name} is {age}") }
```

**Map iteration is in sorted key order**, not Go's randomised order. A
loop whose output changes between runs is a bad thing to hand a
beginner, so the keys are sorted first. `keys()` and `values()` are
sorted for the same reason.

A `str` is not directly iterable - use `chars(s)` or `split(s, sep)`.

---

## Functions

```veyl
fn add(a: int, b: int) -> int {
    return a + b
}
```

- Parameter types are **required**.
- `-> Type` is the return type; omit it for a function that returns nothing.
- Functions must be declared at the top level, not nested.
- Declaration order does not matter - a function may call one defined
  later in the file.
- Recursion works.

```veyl
fn fib(n: int) -> int {
    if n < 2 {
        return n
    }
    return fib(n - 1) + fib(n - 2)
}

fn greet(name: str) {
    print("hello, {name}")
    return              // bare return is optional here
}
```

A function with a return type must return on **every** path. This is an
error:

```veyl
fn bad(n: int) -> int {
    if n > 0 {
        return 1
    }
    // error: function "bad" must return a value of type int on every path
}
```

### Default values

A parameter can have a default, used when a call leaves it off:

```veyl
fn greet(name: str, greeting: str = "hello", times: int = 1) -> str {
    ...
}

greet("ada")                 // greeting "hello", times 1
greet("ada", "welcome", 3)
```

A default is a constant: a number, a string, `true`, `false`, `nil` or
an enum's variant. Once one parameter has one, every parameter after it
needs one too. Methods take defaults the same way.

### Functions are values

A declared function can be handed around like anything else, and
written down anonymously where it is needed:

```veyl
fn double(n: int) -> int {
    return n * 2
}

let f = double                                       // a named function
let triple = fn(n: int) -> int { return n * 3 }      // an anonymous one
print(f(21), triple(5))
```

The type is written the way the signature is:

```veyl
let op: fn(int) -> int = double
let show: fn(str) = fn(s: str) { print(s) }     // returns nothing
```

Parameter types are always required, even in a literal - Veyl does
not infer them.

**Taking and returning them** is what makes callbacks work:

```veyl
fn applyTwice(g: fn(int) -> int, start: int) -> int {
    return g(g(start))
}

fn adder(by: int) -> fn(int) -> int {
    return fn(n: int) -> int { return n + by }
}

print(applyTwice(adder(10), 1))     // 21
```

**Literals close over what is around them**, and can change it:

```veyl
let seen = 0
let tally = fn(n: int) { seen += n }
tally(3)
tally(4)
print(seen)     // 7
```

They go in lists, maps and struct fields like any other value:

```veyl
struct Rule {
    label: str
    test:  fn(int) -> bool
}
```

**A builtin is not a value.** `let p = print` is an error - wrap it:
`fn(s: str) { print(s) }`. Builtins are compiler-known shapes rather
than real functions, and several are variadic or polymorphic in ways no
single signature describes.

### Scope

Each function has its own scope. A top-level `let` is local to the
implicit `main`, so functions cannot see it - pass it in as a
parameter, or make it a `const`, which is global.

```veyl
let total = 10
const LIMIT = 100

fn show() {
    print(LIMIT)        // fine - a top-level const is global
    print(total)        // error: "total" belongs to the program body
}
```

See [Globals](#globals) for the rule and why it works that way.

**Planned:** default parameters, multiple return values.

---

## Builtin library

Available everywhere; no import needed.

### Output

| Function     | Description                              |
| ------------ | ---------------------------------------- |
| `print(...)` | writes its arguments, then a line break   |
| `write(...)` | writes its arguments with no line break   |

```veyl
write("Loading")
write("...")
print("done")           // Loading...done
```

### Input

| Function          | Returns | Description                                    |
| ----------------- | ------- | ---------------------------------------------- |
| `input()`         | `str`   | reads one line                                  |
| `input(prompt)`   | `str`   | writes `prompt`, then reads one line            |
| `pause()`         | -       | waits for Enter; useful before a program exits  |

```veyl
let name = input("Your name: ")
print("hi, {name}")
pause()
```

The line comes back without its ending, `\n` or `\r\n`. At the end of
input - a file piped in has run out, or Ctrl+Z then Enter at a console -
`input` returns `""`, so a loop reading until an empty line stops there
too. `pause` writes `Press Enter to continue...` first.

### Conversion

| Function              | Returns | Description                                     |
| --------------------- | ------- | ----------------------------------------------- |
| `str(x)`              | `str`   | any value as text                                |
| `toInt(s)`            | `int`   | parses text; `0` if it isn't a number            |
| `toInt(s, fallback)`  | `int`   | parses text; `fallback` if it isn't a number     |
| `toFloat(s)`          | `float` | parses text; `0` if it isn't a number            |
| `toFloat(s, fallback)`| `float` | parses text; `fallback` if it isn't a number     |
| `isInt(s)`            | `bool`  | whether the text parses as a whole number        |
| `isFloat(s)`          | `bool`  | whether the text parses as a number              |
| `int(x)` `float(x)`   | `int`, `float` | any number, converted                     |
| `i8(x)` ... `u64(x)`, `f32(x)` | that type | any number, wrapped like a C cast - see [Fixed-width numbers](#fixed-width-numbers) |

Surrounding whitespace is ignored, so `toInt(" 7 ")` gives `7`.

**Careful:** `toInt` never fails - it returns the fallback instead. If bad
input should not be silently accepted, check it first:

```veyl
let raw = input("Age: ")
while !isInt(raw) {
    print("that isn't a number")
    raw = input("Age: ")
}
let age = toInt(raw)
```

Or pick a fallback that cannot be mistaken for a real answer:

```veyl
let age = toInt(input("Age: "), -1)
if age < 0 {
    print("I'll take that as a no.")
}
```

### Math

Every numeric builtin accepts `int` or `float` arguments.

| Function                 | Returns | Description                          |
| ------------------------ | ------- | ------------------------------------ |
| `sqrt(x)` `cbrt(x)`      | `float` | square and cube roots                 |
| `pow(x, y)`              | `float` | x to the power of y                   |
| `exp(x)`                 | `float` | e to the power of x                   |
| `hypot(x, y)`            | `float` | length of the hypotenuse              |
| `log(x)` `log2(x)` `log10(x)` | `float` | logarithms                     |
| `abs(x)`                 | `float` | absolute value                        |
| `mod(x, y)`              | `float` | remainder that works on floats        |
| `floor(x)` `ceil(x)`     | `int`   | round down / up                       |
| `round(x)` `trunc(x)`    | `int`   | round to nearest / toward zero        |
| `clamp(x, lo, hi)`       | `float` | constrain to a range                  |
| `sign(x)`                | `int`   | `-1`, `0` or `1`                      |
| `isNan(x)`               | `bool`  | whether x is not-a-number             |
| `sin` `cos` `tan`        | `float` | trigonometry, in radians              |
| `asin` `acos` `atan`     | `float` | inverse trigonometry                  |
| `atan2(y, x)`            | `float` | angle of the point (x, y)             |

Constants: `PI`, `E`, `INF`, `NAN`.

```veyl
print("sqrt(2) is {sqrt(2)}")
print("a full turn is {2 * PI} radians")
print("{floor(3.7)} {ceil(3.2)} {round(2.6)}")
```

### Numbers and conversion

| Function        | Returns | Description                              |
| --------------- | ------- | ---------------------------------------- |
| `int(x)`        | `int`   | drops the fractional part                 |
| `float(x)`      | `float` | converts to a float                       |
| `divf(a, b)`    | `float` | true division, even for two ints          |

**Important:** `/` between two `int` values truncates, so `7 / 2` is `3`.
Use `divf(7, 2)` for `3.5`, or make one side a float.

### Randomness

| Function            | Returns | Description                             |
| ------------------- | ------- | --------------------------------------- |
| `random()`          | `float` | between 0 and 1                          |
| `randomInt(lo, hi)` | `int`   | between `lo` and `hi`, both included     |

### Strings

`s = s + piece` in a loop copies all of `s` every time. A `Builder`
keeps the pieces and joins them once, so building a long string is
linear:

```veyl
let sb = Builder{}
for i in 0..1000 {
    sb.add("{i},")
}
print(sb.str())
```

`add(s)` and `addLine(s)` append, `str()` gives the string, `len()` its
length, and `clear()` empties it.

| Function                        | Returns | Description                      |
| ------------------------------- | ------- | -------------------------------- |
| `upper(s)` `lower(s)`           | `str`   | change case of ASCII letters      |
| `trim(s)`                       | `str`   | remove surrounding whitespace     |
| `contains(s, sub)`              | `bool`  | whether `sub` occurs in `s`       |
| `startsWith(s, p)` `endsWith(s, p)` | `bool` | prefix / suffix test         |
| `indexOf(s, sub)`               | `int`   | byte position of `sub`, or `-1`   |
| `count(s, sub)`                 | `int`   | how many times `sub` occurs       |
| `replace(s, old, new)`          | `str`   | replace every occurrence          |
| `repeat(s, n)`                  | `str`   | `s` joined to itself `n` times    |
| `charAt(s, i)`                  | `str`   | character `i`; `""` if out of range |
| `substr(s, start, end)`         | `str`   | characters `start` to `end`; indexes are clamped |
| `padLeft(s, width)` `padRight(s, width)` | `str` | pad with spaces        |
| `padLeft(s, width, fill)`       | `str`   | pad with a chosen character       |

Index-based functions never crash - an out-of-range index gives `""`
rather than stopping the program.

```veyl
print(padLeft(str(7), 3, "0"))    // 007
print(substr("Hello, world", 7, 12))
```

### Lists

`xs[i]` reads an element and `xs[i] = v` writes one. An out-of-range
index stops the program with a plain message rather than a Go stack
trace.

The functions that **change** a list - `push`, `pop`, `insert`,
`removeAt`, `clear` - take the list itself as their first argument, and
that argument has to be a variable or an element, not a temporary. The
functions that **read** a list return a new one and leave the original
alone.

| Function                | Returns  | Description                                |
| ----------------------- | -------- | ------------------------------------------ |
| `push(xs, v, ...)`      | -        | append one or more values                   |
| `pop(xs)`               | element  | remove and return the last element          |
| `insert(xs, i, v)`      | -        | insert `v` at position `i`                  |
| `removeAt(xs, i)`       | element  | remove and return the element at `i`        |
| `clear(xs)`             | -        | remove everything                           |
| `first(xs)` `last(xs)`  | element  | the first or last element                   |
| `slice(xs, a, b)`       | list     | a copy of the range; indexes are clamped    |
| `reverse(xs)`           | list     | a reversed copy                             |
| `sort(xs)`              | list     | a sorted copy; numbers or strings           |
| `sum(xs)`               | number   | the total of a list of numbers              |
| `join(xs, sep)`         | `str`    | every element joined into one string        |
| `contains(xs, v)`       | `bool`   | whether `v` is in the list                  |
| `indexOf(xs, v)`        | `int`    | position of `v`, or `-1`                    |
| `len(xs)`               | `int`    | how many elements                           |

### Working over a list

These take a function, which is why they only became possible once
functions were values. Like `sort` and `reverse`, they return a new
list and leave the original alone.

| Function                | Returns  | Description                                |
| ----------------------- | -------- | ------------------------------------------ |
| `map(xs, f)`            | list     | `f` applied to each element; the type may change |
| `filter(xs, keep)`      | list     | the elements `keep` says yes to             |
| `reduce(xs, start, f)`  | any      | fold into one value, starting from `start`  |
| `sortBy(xs, less)`      | list     | sorted by your own comparison               |
| `any(xs, test)` `all(xs, test)` | `bool` | whether some or every element passes  |
| `each(xs, do)`          | -        | run `do` for each element                   |

```veyl
let nums = [5, 3, 8, 1]

print(map(nums, fn(n: int) -> int { return n * 2 }))
print(filter(nums, fn(n: int) -> bool { return n > 4 }))
print(reduce(nums, 0, fn(acc: int, n: int) -> int { return acc + n }))
print(sortBy(nums, fn(a: int, b: int) -> bool { return a > b }))
```

`reduce` starts from a value of the type you want back, so it can build
something other than a list of the same thing:

```veyl
let words = ["fig", "pear"]
print(reduce(words, "", fn(acc: str, w: str) -> str { return acc + charAt(w, 0) }))
```

```veyl
let xs: []int = []
push(xs, 3, 1, 2)
print(sort(xs))        // [1, 2, 3]
print(xs)              // [3, 1, 2] - sort returned a copy
```

### Maps

`m[k]` reads and `m[k] = v` writes. **A missing key reads as the zero
value** - `0`, `""`, `false` - so use `has()` when the difference
matters.

| Function        | Returns | Description                            |
| --------------- | ------- | -------------------------------------- |
| `has(m, k)`     | `bool`  | whether the key is present              |
| `find(m, k)`    | `?V`    | the value, or nil if the key is absent  |
| `remove(m, k)`  | -       | delete a key                            |
| `keys(m)`       | list    | the keys, sorted                        |
| `values(m)`     | list    | the values, in sorted key order         |
| `clear(m)`      | -       | remove everything                       |
| `len(m)`        | `int`   | how many entries                        |

```veyl
let counts: {str: int} = {}
for w in split("a b a", " ") {
    counts[w] += 1        // a missing key starts at 0
}
print(counts)             // {"a": 2, "b": 1}
```

### Splitting strings

| Function          | Returns  | Description                          |
| ----------------- | -------- | ------------------------------------ |
| `split(s, sep)`   | `[]str`  | split on a separator                  |
| `chars(s)`        | `[]str`  | one entry per character               |
| `lines(s)`        | `[]str`  | split on line breaks                  |

### Utility

| Function        | Returns | Description                             |
| --------------- | ------- | --------------------------------------- |
| `len(x)`        | `int`   | length of a `str`, list, or map          |
| `min(a, b, ...)`| -       | smallest of its arguments                |
| `max(a, b, ...)`| -       | largest of its arguments                 |
| `sleep(ms)`     | -       | pauses for `ms` milliseconds             |
| `exit(code)`    | -       | ends the program with an exit code       |

Builtin names cannot be redefined.

---

## Multiple files

`import` loads another `.vl` file and folds its declarations into your
program. The path is relative to the file that writes it - there is no
search path, no registry, and no package names to learn.

```veyl
import "geometry.vl"
import "shapes/circle.vl"
```

### `pub` decides what escapes

A declaration is private to its own file unless it is marked `pub`:

```veyl
// geometry.vl
pub const TAU = 6.283185307179586

pub struct Vec {
    x: float
    y: float
}

pub fn circleArea(radius: float) -> float {
    return (TAU / 2.0) * radius * radius
}

fn helper() -> int {      // no pub: this file only
    return 1
}
```

Using something private from another file is an error that says so:

```
error: "helper" is private to geometry.vl
       - mark it 'pub fn helper' to use it from another file
```

**Methods are as visible as their struct.** `pub` inside an `impl`
block is an error - a `pub struct` brings its methods with it.

### What an imported file may contain

Declarations only: `import`, `const`, `struct`, `fn` and `impl`. A
loose statement is refused, because there is no sensible moment for it
to run:

```veyl
// in an imported file
print("hello")     // error: an imported file can only declare things
```

Importing the same file twice is harmless - it is folded in once. An
import cycle is an error rather than a hang.

### Globals

A top-level `const` and a top-level `var` are globals: visible inside
every function, and across files if they are `pub`. A `const` cannot
change; a `var` can, from anywhere.

```veyl
const LIMIT = 100
var score = 0

fn award(points: int) {
    score += points           // changes the one global
}

award(5)
print(score)                  // 5
```

A top-level `let` is not a global: it belongs to the program body, and
functions cannot see it. The compiler says so and points at `var`:

```veyl
let count = 10

fn broken() -> bool {
    return count > 0          // error: "count" belongs to the program body
}
```

Globals are computed in the order they are written, before the program
body runs - so a `const` or `var` cannot use a top-level `let`, and one
that uses another global has to come after it.

---

## Libraries

Everything above is a bare name. The rest of the standard library lives
under a dotted path:

```veyl
let text = os.file.read("notes.txt")
let page = http.get("https://example.com")
print(time.stamp())
```

These need no `import` - the names are always available, and the dots
group them rather than loading anything. `import` is for your own files
and for packages other people wrote; see [PACKAGES.md](PACKAGES.md).

If you get a path wrong the compiler suggests the near misses:

```
error: there is no builtin called os.file.slurp
       - did you mean one of: os.file.append, os.file.delete,
         os.file.exists, os.file.lines, os.file.read, ...
```

### When a program stops

A runtime error - an index past the end of a list, a `must` on a
failure - says what went wrong and where:

```
runtime error: index 5 is out of range for a list of length 3
    at game.vl:42 in update
```

A crash that is not one of Veyl's own errors - an address given to
`mem.*` that is not valid, native code misbehaving - is reported too,
with what kind it was and which of the program's functions it happened
in, after everything the program printed has been written out:

```
crash: access violation - an address that is not valid was read or written
    in poke
```

Neither costs anything while the program runs normally.

### How failure is reported

**Nothing in the library stops your program.** Anything that can fail
says so in its type:

| Shape | On failure |
| --- | --- |
| `os.file.read(p)` -> `str!` | a failure carrying the reason |
| `os.file.readOr(p, fallback)` -> `str` | returns the fallback |
| `os.file.write(p, text)` -> `bool` | returns `false` |

An operation that **produces a value** returns `T!`, so you unwrap it
with `?`, `must()`, `valueOr()` or a check - see
[Results](#results--things-that-can-fail).

```veyl
let text = must(os.file.read("notes.txt"))    // or stop, saying why

fn wordCount(path: str) -> int! {
    let text = os.file.read(path)?            // or hand the reason up
    return len(split(trim(text), " "))
}
```

An operation that **only acts** returns a `bool`. There is no unit type
to put inside a result, so the reason is lost in that one case - a real
gap, and the last thing left to tidy here.

---

### `void!` - an action that can fail but returns nothing

Most fallible things hand back a value, so `T!` covers them. Writing a
file does not: it either worked, or it did not. That is `void!`.

```veyl
fn save(path: str, text: str) -> void! {
    os.file.write(path, text)?
    os.file.append(path, "!")?
    return ok()
}
```

`ok()` is the counterpart to `fail("...")`: it says the action
succeeded. `?`, `isOk`, `errorOf` and `must` all work as they do on any
other result.

Used as a plain statement, nothing changes - the result is simply
ignored:

```veyl
os.file.write("notes.txt", "hello")
```

That is deliberate. Reaching for the reason should be easy, not
mandatory.

**Why this exists.** These operations used to return a plain `bool`, so
"permission denied", "no such directory" and "the disk is full" were
all just `false`. There was no way to say "this failed and here is why"
for something with no value to carry. Now there is:

```veyl
let r = os.file.write("/nowhere/a.txt", "x")
if !isOk(r) {
    print(errorOf(r))   // open /nowhere/a.txt: no such file or directory
}
```

The operations that return `void!`: `os.file.write`, `os.file.append`,
`os.file.delete`, `os.file.rename`, `os.dir.make`, `os.dir.delete`,
`os.dir.change`, `os.env.set`.

`os.file.exists`, `os.dir.is` and `os.env.has` are still `bool`, because
asking a question is not the same as performing an action - there is
nothing for them to fail at.

### `os` - files, directories, paths, processes

| Function | Returns | Description |
| --- | --- | --- |
| `os.file.read(p)` | `str!` | the whole file |
| `os.file.readOr(p, alt)` | `str` | the file, or `alt` if it cannot be read |
| `os.file.lines(p)` | `[]str!` | the file split into lines |
| `os.file.write(p, text)` | `bool` | write, replacing what was there |
| `os.file.append(p, text)` | `bool` | add to the end |
| `os.file.size(p)` | `int!` | size in bytes |
| `os.file.exists(p)` | `bool` | whether it is there |
| `os.file.delete(p)` | `bool` | remove it |
| `os.file.rename(a, b)` | `bool` | move or rename |
| `os.dir.list(p)` | `[]str!` | entry names, sorted |
| `os.dir.make(p)` | `bool` | create, including parents |
| `os.dir.delete(p)` | `bool` | remove a directory and its contents |
| `os.dir.is(p)` | `bool` | whether it is a directory |
| `os.dir.current()` | `str` | the working directory |
| `os.dir.change(p)` | `bool` | change directory |
| `os.dir.home()` `os.dir.temp()` | `str` | well-known directories |
| `os.path.join(a, b, ...)` | `str` | join with the right separator |
| `os.path.base(p)` `os.path.dir(p)` `os.path.ext(p)` | `str` | split a path |
| `os.path.clean(p)` `os.path.absolute(p)` | `str` | tidy or resolve |
| `os.env.get(n)` | `str` | an environment variable, `""` if unset |
| `os.env.set(n, v)` | `bool` | set one |
| `os.env.has(n)` | `bool` | whether it is set at all |
| `os.args()` | `[]str` | command-line arguments |
| `os.run(cmd, args)` | `str!` | run a program, return its output |
| `os.name()` `os.arch()` | `str` | the OS and CPU architecture |
| `os.cpus()` `os.pid()` | `int` | processor count, process id |
| `os.hostname()` | `str` | this machine's name |

The path functions never touch the disk - they are string manipulation.

```veyl
os.file.write("notes.txt", "hello")
for line in os.file.lines("notes.txt") {
    print(line)
}
```

**Shorthand.** `os.read.file(p)`, `os.write.file(p, text)` and a few
others are aliases for the noun-first spellings. Both compile to the
same call; the noun-first form is documented because it groups better as
the library grows.

---

### `http` - a web server, and fetching

Writing a server is the main thing here. `http.serve` takes a port and
a function, calls it once per request, and sends back whatever it
returns.

```veyl
http.serve(8080, fn(req: Request) -> Response {
    if req.path == "/" {
        return http.ok("<h1>hello from Veyl</h1>")
    }
    if req.path == "/time" {
        return http.text(time.stamp())
    }
    return http.notFound()
})
```

That is a complete program. Run it and open `http://localhost:8080`.

**What you are given.** A `Request` is a struct:

| Field | Type | |
| --- | --- | --- |
| `method` | `str` | `"GET"`, `"POST"`, ... |
| `path` | `str` | `"/users"`, with no query string |
| `query` | `str` | everything after `?`, empty if none |
| `body` | `str` | the request body |
| `headers` | `{str: str}` | header names lowercased |

`http.header(req, "user-agent")` reads one header and gives `""` when
it is absent, which saves checking the map first.

**What you send back.** A `Response` is `status`, `contentType` and
`body`, and these build one for you:

| Function | Sends |
| --- | --- |
| `http.ok(body)` | 200, `text/html` |
| `http.text(body)` | 200, `text/plain` |
| `http.json(body)` | 200, `application/json` |
| `http.status(code, body)` | any code, `text/plain` |
| `http.notFound()` | 404 |

You can also build one yourself: `Response{status: 302, contentType:
"text/plain", body: ""}`.

**Fetching.** `http.get(url)` returns the body as `str!`.

```veyl
let page = http.get("http://example.com")?
```

Both `http://` and `https://` work: requests go through WinHTTP, which
does TLS, the certificate chain, redirects and chunked bodies. A 4xx
or 5xx is a failure carrying the code.

`http.post(url, body)` and `http.download(url, path)` are there too.

**One request at a time.** The server handles a request, finishes it,
then accepts the next. That is fine for a tool, a local dashboard or
something on your own network. It is not a production web server, and a
slow handler blocks everything behind it.

---

### `net` - TCP sockets

`http` is written on top of these, and they are there when you want the
socket itself. A socket is an `int` handle.

| Function | Returns | Description |
| --- | --- | --- |
| `net.listen(port)` | `int!` | a listening socket |
| `net.accept(sock)` | `int!` | blocks until someone connects |
| `net.recv(sock)` | `str!` | read once; `""` when the peer closes |
| `net.send(sock, data)` | `int!` | writes all of it, returns the length |
| `net.connect(host, port)` | `int!` | connect out, by name or address |
| `net.close(sock)` | | |

An echo server:

```veyl
let server = must(net.listen(9000))
print("listening on 9000")

while true {
    let conn = must(net.accept(server))
    let line = must(net.recv(conn))
    must(net.send(conn, "you said: {line}"))
    net.close(conn)
}
```

Two things to know. `net.recv` reads **once** and gives you whatever
had arrived - TCP is a stream, so a message may come back in pieces and
deciding where one ends is yours to do. And a string here is
NUL-terminated, so binary data with a zero byte in it reads back short;
that is what `bytes` is for.

---

### `json` - reading and writing JSON

Two ways to use it, because two different things are usually wanted.

**Whole values.** `json.encode` turns any Veyl value into text, and
`json.decode` turns text back into a declared type. The type comes from
the annotation on the binding - Veyl has no type arguments, so this is
how the decoder is told what to build:

```veyl
struct Point {
    x: float
    y: float
}

let text = json.encode(Point{x: 1.0, y: 2.0})   // {"x":1,"y":2}
let p: Point! = json.decode(text)               // decoding can fail
print(must(p).x)
```

The annotation names the shape *and* says it can fail. It travels
through a wrapper too, so this reads the way you would expect:

```veyl
let p: Point = must(json.decode(text))
```

Field names are encoded exactly as you wrote them. Lists, maps, nested
structs and lists of structs all work.

**Single values.** When you only want one field out of a response,
reach in with a dotted path. Numbers step into arrays:

```veyl
let name  = json.get(body, "user.name")
let age   = json.int(body, "user.age")
let first = json.get(body, "members.0.name")
```

| Function | Returns | Description |
| --- | --- | --- |
| `json.encode(v)` | `str` | any value as JSON |
| `json.pretty(v)` | `str` | the same, indented |
| `json.decode(text)` | the annotated type | decode; fails if the text is bad |
| `json.decodeOr(text, alt)` | `alt`'s type | decode, or `alt` if the text is bad |
| `json.get(text, path)` | `str` | a string field; other values come back as JSON |
| `json.int(text, path)` | `int` | a whole number |
| `json.num(text, path)` | `float` | a number |
| `json.bool(text, path)` | `bool` | a boolean |
| `json.has(text, path)` | `bool` | whether the path exists |
| `json.keys(text, path)` | `[]str` | the keys of an object, sorted |
| `json.count(text, path)` | `int` | length of an array, object or string |
| `json.valid(text)` | `bool` | whether it parses at all |

The `path` is optional everywhere - leave it out to act on the whole
document. A path that does not exist reads as `""`, `0` or `false`
rather than failing; use `json.has` when the difference matters.

**Writing JSON by hand needs doubled braces.** `{` starts an
interpolation inside a string, so a literal one is written `{{`:

```veyl
let text = "{{"x": 1}}"        // the string {"x": 1}
```

Usually it is easier to build the value and encode it than to write the
text out.

---

### `task` - doing several things at once

`task.map` is `map`, run concurrently. Same arguments, same ordered
results - switching between them is a one-word edit.

```veyl
let pages = task.map(urls, fn(u: str) -> str {
    return valueOr(http.get(u), "")
})
```

| Function | Returns | Description |
| --- | --- | --- |
| `task.map(xs, f)` | list | `f` over each element, at once; results stay in order |
| `task.mapLimit(xs, n, f)` | list | the same, at most `n` running at a time |
| `task.each(xs, do)` | - | run `do` for each element, at once |
| `task.all(fns)` | - | run a list of `fn()` at once and wait for the last |

Everything has finished by the time the call returns. There is no way
to start work that outlives the statement that started it.

**The one thing it cannot check is what your function touches.** A
function passed to `task.map` runs on several threads at once, so it
should compute a value from its argument rather than change something
outside itself:

```veyl
// Fine: each call produces a value.
let sizes = task.map(paths, fn(p: str) -> int {
    return valueOr(os.file.size(p), 0)
})

// Not fine: every call writes to the same list.
let out: []int = []
task.each(paths, fn(p: str) {
    push(out, 1)        // a race, and nothing will tell you
})
```

That is a real limit of this design, not an oversight - enforcing it
needs an ownership system Veyl does not have. A mutex, below, is how to
share something safely.

### `thread` and `atomic` - threads of your own

`thread.spawn` starts a function on a new thread and returns straight
away; `thread.join` waits for it. The function can be a closure, and
sees what it captured:

```veyl
let hits = atomic.new(0)
let workers: []int = []
for id in 0..8 {
    push(workers, thread.spawn(fn() {
        for i in 0..10000 {
            atomic.add(hits, 1)
        }
    }))
}
for t in workers {
    thread.join(t)
}
print(atomic.get(hits))     // 80000
```

| Function | Returns | Description |
| --- | --- | --- |
| `thread.spawn(f)` | `int` | run `f`, a `fn()`, on a new thread; the result is its handle |
| `thread.join(t)` | - | wait for a thread to finish |
| `thread.id()` / `thread.cores()` | `int` | this thread's id / how many cores there are |
| `thread.mutex()` | `int` | a new mutex |
| `thread.lock(m)` / `thread.unlock(m)` | - | take and release it; one thread at a time |
| `thread.cond()` | `int` | a condition variable |
| `thread.wait(c, m)` | - | release `m`, sleep until notified, take `m` back |
| `thread.notify(c)` / `thread.notifyAll(c)` | - | wake one waiter / all of them |
| `atomic.new(v)` | `int` | the address of a new word holding `v` |
| `atomic.add(p, n)` | `int` | add `n`, giving the new value |
| `atomic.get(p)` / `atomic.set(p, v)` | `int` / - | read, write |
| `atomic.swap(p, v)` | `int` | write `v`, giving the old value |
| `atomic.cas(p, old, new)` | `bool` | write `new` only if the word holds `old` |

The `atomic` functions work on any word-aligned address, including one
from `mem.alloc`. Each is a single locked instruction.

### Channels

A `Channel<T>` carries values from one thread to another, in order:

```veyl
let jobs = channel<int>()
let done = channel<str>()

let worker = thread.spawn(fn() {
    while true {
        let j = jobs.recv()
        if j == nil {
            break           // closed, and everything taken
        }
        done.send("{j} squared is {j * j}")
    }
})

for n in 0..3 {
    jobs.send(n)
}
jobs.close()
thread.join(worker)
```

A `for` loop takes every value a channel carries, waiting for each, and
ends once the channel is closed and empty:

```veyl
for line in results {
    print(line)
}
```

`send` adds a value and wakes a receiver. `recv` gives the next one,
waiting if there is none, and `nil` once the channel is closed and
empty. `close` says no more are coming; sending after it stops the
program. `pending` is how many are waiting to be received. A channel
can be passed around and captured freely: every copy is the same
channel.

### Threads and the collector

The collector looks at one stack, the one it runs on, so it never runs
while any thread besides main is alive - not automatically, and not by
`mem.collect()`, which then does nothing. Allocation carries on as
normal, under a lock taken only while other threads are running, so a
program without threads pays nothing for them. A long-lived thread that
allocates heavily grows the heap until it finishes; a program that
needs threads and bounded memory both can say `gc off` and `delete`
what it is done with.

Nothing checks what a thread touches. Two threads changing the same
list without a mutex is a race, as in C++.

---

### `re` - regular expressions

Patterns belong in raw strings. In an ordinary string `{4}` is an
interpolation, so a quantifier would have to be written `{{4}}`, and
`\d` would be an unknown escape.

```veyl
const DATE = `\d{4}-\d{2}-\d{2}`

print(re.find(DATE, log))
print(re.findAll(DATE, log))
```

| Function | Returns | Description |
| --- | --- | --- |
| `re.matches(p, text)` | `bool` | whether the pattern occurs |
| `re.find(p, text)` | `str` | the first match, `""` if none |
| `re.findAll(p, text)` | `[]str` | every match |
| `re.groups(p, text)` | `[]str` | the first match's capture groups |
| `re.count(p, text)` | `int` | how many matches |
| `re.replace(p, text, with)` | `str` | replace every match |
| `re.split(p, text)` | `[]str` | split on the pattern |
| `re.escape(text)` | `str` | quote text to match it literally |
| `re.valid(p)` | `bool` | whether the pattern compiles |

**A pattern that does not compile stops the program.** That is the one
place the library still does this, and it is deliberate: patterns are
almost always literals the author wrote, so a bad one is a bug rather
than a runtime condition, and `must(...)` on every call would be noise.
Use `re.valid` for a pattern that came from input.

Compiled patterns are cached, so using one inside a loop does not
recompile it each time.

---

### `hash` - digests and encodings

| Function | Returns | Description |
| --- | --- | --- |
| `hash.md5(s)` `hash.sha1(s)` | `str` | lower-case hex digest |
| `hash.sha256(s)` `hash.sha512(s)` | `str` | lower-case hex digest |
| `hash.crc32(s)` | `int` | checksum |
| `hash.file(path)` | `str!` | sha256 of a file, read in chunks |
| `hash.base64(s)` `hash.hex(s)` | `str` | encode |
| `hash.fromBase64(s)` `hash.fromHex(s)` | `str!` | decode |

Digests are hex because that is what every other tool prints - a
checksum you cannot compare by eye is not much use. Decoding can fail,
since the input comes from outside; encoding cannot.

---

### `db` - SQLite

Needs the `sqlite` package, which carries the library:

```
veyl get sqlite
```

Then:

```veyl
import "sqlite"

const NONE: []str = []

let conn = must(db.open("app.db"))
must(db.exec(conn, "create table if not exists notes (id integer primary key, body text)", NONE))
must(db.exec(conn, "insert into notes (body) values (?)", ["hello"]))

for row in must(db.query(conn, "select id, body from notes", NONE)) {
    print("{row[0]}: {row[1]}")
}
db.close(conn)
```

| Function | Returns | Description |
| --- | --- | --- |
| `db.open(path)` | `int!` | a connection; the file is created if missing |
| `db.close(conn)` | | |
| `db.exec(conn, sql, args)` | `void!` | a statement with no results |
| `db.query(conn, sql, args)` | `[][]str!` | rows, each a list of columns |
| `db.changes(conn)` | `int` | rows affected by the last statement |
| `db.lastId(conn)` | `int` | the id the last insert generated |
| `db.version()` | `str` | the SQLite version |

**Arguments are always bound, never pasted.** `args` is a separate
`[]str` and each `?` in the SQL takes one, in order. There is no
function here that builds SQL from a value, so an injection cannot be
written through this API even by accident:

```veyl
let typed = "a' or '1'='1"
db.query(conn, "select id from notes where tag = ?", [typed])   // matches nothing
```

**Everything comes back as `str`.** A SQLite column is dynamically
typed, so one type out is the honest answer. Use `toInt` and `float()`.

A `NULL` column reads as `""`. If you need to tell null from empty, ask
the database: `select body is null from ...`.

**An empty argument list needs a name.** A bare `[]` in an argument
position has nothing to infer its element type from, so declare one:

```veyl
const NONE: []str = []
must(db.exec(conn, "delete from notes", NONE))
```

#### The package's helpers

`import "sqlite"` also brings these, which are ordinary Veyl over the
above:

| Function | Returns | Description |
| --- | --- | --- |
| `dbScalar(conn, sql, args, fallback)` | `str!` | the first column of the first row |
| `dbScalarInt(conn, sql, args, fallback)` | `int!` | the same as an int, failing if it is not one |
| `dbRow(conn, sql, args)` | `[]str!` | the first row, or empty |
| `dbCount(conn, table)` | `int!` | `select count(*)` |
| `dbTables(conn)` | `[]str!` | table names |
| `dbColumns(conn, table)` | `[]str!` | column names |
| `dbHasTable(conn, name)` | `bool!` | |
| `dbQueryNamed(conn, table, sql, args)` | `[]{str: str}!` | rows as maps keyed by column |
| `dbBegin` `dbCommit` `dbRollback` | `void!` | transactions |

Transactions are worth using for speed as well as correctness. SQLite
commits every statement on its own otherwise, so a few thousand inserts
go from seconds to milliseconds inside one:

```veyl
must(dbBegin(conn))
for line in lines(text) {
    must(db.exec(conn, "insert into notes (body) values (?)", [line]))
}
must(dbCommit(conn))
```

**A table name cannot be bound.** SQLite binds values, not identifiers,
so anything taking a table name checks it is a plain identifier and
fails rather than pasting it in. That is why `dbCount(conn, "notes;
drop table notes")` is an error and not a disaster.

---

### `csv` - tables

| Function | Returns | Description |
| --- | --- | --- |
| `csv.parse(text)` | `[][]str!` | rows of fields |
| `csv.write(rows)` | `str` | back to text, quoting as needed |
| `csv.read(path)` | `[][]str!` | parse a file |
| `csv.save(path, rows)` | `bool` | write a file |

Rows may have different lengths. Real files are ragged, and refusing to
read one is less useful than handing it over.

---

### `time` - clocks and formatting

Format strings use readable tokens rather than a reference date:

| Token | Means | Token | Means |
| --- | --- | --- | --- |
| `YYYY` `YY` | year | `HH` | hour, 24-hour |
| `MMMM` `MMM` `MM` | month | `hh` | hour, 12-hour |
| `DDDD` `DDD` `DD` | day | `mm` `ss` | minute, second |

| Function | Returns | Description |
| --- | --- | --- |
| `time.now()` | `int` | seconds since 1970 |
| `time.millis()` `time.nanos()` | `int` | finer resolution, for timing |
| `time.format(unix, fmt)` | `str` | render a timestamp |
| `time.parse(text, fmt)` | `int` | read one back; `-1` if it does not match |
| `time.date()` `time.clock()` `time.stamp()` | `str` | now, preformatted |
| `time.since(unix)` | `int` | seconds elapsed |
| `time.year()` `time.month()` `time.day()` | `int` | parts of today |
| `time.weekday()` | `str` | the day's name |
| `time.sleep(ms)` | - | pause |

```veyl
let started = time.millis()
work()
print("took {time.millis() - started} ms")
```

---

### Collected, or by hand

A program is garbage collected unless it says otherwise: values are
freed once nothing refers to them, and there is nothing to think about.

A program that wants to manage its own memory, as in C++, says so on
a line of its own at the top of the file:

```veyl
gc off

let xs = [1, 2, 3]
push(xs, 4)
print(xs)
delete(xs)
```

Then nothing is ever collected - not in the background, and not by
`mem.collect()`, which becomes an error - and `delete(x)` frees a list,
map, struct, string or `bytes` the moment it is called. There are no
collection pauses, and memory is released exactly where the program
says.

`delete` frees the value itself and not what it refers to: deleting a
list of strings frees the list, and each string is deleted on its own.
Nothing checks that a deleted value is not used again, or deleted
twice, which is the same bargain C++ makes. A string written in the
program is part of the executable, and deleting one does nothing.

Strings that exist only in passing are freed as they are used, since
nothing could name them to delete them: the pieces of `"a" + str(i) +
"b"`, the parts of an interpolation, and a string built only to be
printed. A string that is kept - in a variable, a list or a field - is
the program's to delete.

`gc off` goes in the file that is built, not one it imports, and
`delete` is only allowed in a program that says `gc off`: under the
collector, an object freed by hand would still be on the list the next
collection walks.

### `mem` - memory

Veyl collects its own values. The collector runs by itself when the
live heap passes a threshold - 4 MB at first, then twice what survived
the last collection - so a short program never pays for one. These
report on it, and nudge it:

| Function | Returns | Description |
| --- | --- | --- |
| `mem.used()` | `int` | bytes currently allocated |
| `mem.total()` | `int` | bytes allocated over the whole run |
| `mem.system()` | `int` | bytes obtained from the OS |
| `mem.objects()` | `int` | live object count |
| `mem.collections()` | `int` | how many times the collector has run |
| `mem.collect()` | - | run the collector now |
| `mem.goroutines()` | `int` | concurrent tasks in flight |

And these work on raw memory, outside the collector. An address is an
`int`, the same as a `ptr` on an extern, so pointer arithmetic is
ordinary arithmetic:

| Function | Returns | Description |
| --- | --- | --- |
| `mem.alloc(n)` | `int` | `n` zeroed bytes the collector never touches |
| `mem.resize(p, n)` | `int` | grow or shrink a block; `0` allocates one |
| `mem.free(p)` | - | give a block back; `0` does nothing |
| `mem.readU8(p)` `mem.readI8(p)` | `int` | one byte, unsigned or signed |
| `mem.readU16(p)` `mem.readI16(p)` | `int` | two bytes |
| `mem.readU32(p)` `mem.readI32(p)` | `int` | four bytes |
| `mem.readI64(p)` | `int` | eight bytes |
| `mem.readF32(p)` `mem.readF64(p)` | `float` | a C float or double |
| `mem.write8(p, v)` ... `mem.write64(p, v)` | - | the low bytes of `v` |
| `mem.writeF32(p, x)` `mem.writeF64(p, x)` | - | a float at that width |
| `mem.copy(dst, src, n)` | - | `n` bytes; the ranges may overlap |
| `mem.fill(p, byte, n)` | - | `n` copies of one byte |
| `mem.str(p)` | `str` | the NUL-terminated text at `p`, copied |
| `mem.strN(p, n)` | `str` | at most `n` bytes, stopping at a NUL |
| `mem.bytes(p, n)` | `bytes` | `n` bytes at `p`, copied |
| `mem.addr(v)` | `int` | where a `bytes`, `str` or extern struct lives |
| `mem.protect(p, n, mode)` | `bool` | make pages `"r"`, `"rw"`, `"rx"`, `"rwx"`, `"x"` or `""` (no access) |
| `mem.scan(p, n, pattern)` | `int` | first match of a byte pattern such as `"48 8B ?? ?? 89"`, or `-1` |
| `mem.symbol(dll, name)` | `int` | where a DLL exports `name`, loading the DLL if need be, or `0` |
| `mem.call(p, args...)` | `int` | call native code at `p` and return what it leaves in `rax` |
| `mem.callF(p, args...)` | `float` | the same, for a function returning a `double` |

```veyl
let p = mem.alloc(16)
mem.write32(p, -5)
print(mem.readU32(p))          // 4294967291
print(mem.readI32(p))          // -5
mem.writeF32(p + 4, 0.1)
print(mem.readF32(p + 4))      // 0.10000000149011612
mem.free(p)
```

A block from `mem.alloc` is yours to free. Nothing checks that an
address is valid: reading or writing one that is not stops the
program, as it would in C.

`mem.call` is how an address becomes a call - one from `mem.symbol`,
from `mem.scan`, read out of a table in memory, or handed over by
native code. Arguments follow the Windows x64 convention: an `int`,
`bool`, `str`, `bytes` or extern struct is one word, and a `float` a
`double`. Nothing checks that the address is code or that it takes
what it is given.

```veyl
let pow = mem.symbol("msvcrt.dll", "pow")
print(mem.callF(pow, 2.0, 10.0))     // 1024

let fmt = mem.symbol("msvcrt.dll", "sprintf")
let buf = mem.alloc(64)
mem.call(fmt, buf, "%d-%s", 7, "up")
print(mem.str(buf))                   // 7-up
```

A C function returning a 32-bit `int` leaves the upper half of `rax`
undefined, so mask a result that can be negative: `mem.call(f) &
0xFFFFFFFF`, then sign-extend if needed.

### `bytes` - raw binary

`bytes` is a real type, alongside `int`, `str` and the rest. It holds
binary: what comes off a socket, out of a file, or into a hash.

```veyl
let b: bytes = bytes.of("hello")
print(len(b))        // 5
print(b[0])          // 104
print(bytes.hex(b))  // 68656c6c6f
print(b)             // bytes(68656c6c6f)
```

`len` and indexing work as they do on a list. **Indexing gives an `int`
from 0 to 255** - there is no separate `byte` type, because a language
that already has `int` has somewhere to put a small number, and a
second numeric type would infect every arithmetic rule to buy nothing.
`==` compares contents, not identity.

| Function | Returns | Description |
| --- | --- | --- |
| `bytes.of(s)` | `bytes` | encode text |
| `bytes.str(b)` | `str` | decode back to text |
| `bytes.list(b)` / `bytes.ofList(ns)` | `[]int` / `bytes!` | as numbers |
| `bytes.hex(b)` / `bytes.fromHex(s)` | `str` / `bytes!` | hexadecimal |
| `bytes.base64(b)` / `bytes.fromBase64(s)` | `str` / `bytes!` | base64 |
| `bytes.slice(b, a, z)` | `bytes` | a copy of part of it |
| `bytes.concat(a, b, ...)` | `bytes` | join |
| `bytes.find(hay, needle)` | `int` | position, or -1 |
| `bytes.fill(n, value)` | `bytes!` | `n` copies of one byte |
| `bytes.getInt(b, off, size)` | `int!` | read a number |
| `bytes.putInt(n, size)` | `bytes!` | write one |
| `bytes.getIntBE` / `bytes.putIntBE` | | big-endian versions |
| `bytes.read(path)` / `bytes.write(path, b)` | `bytes!` / `void!` | files |
| `bytes.hash(b, algorithm)` | `str!` | md5, sha1, sha256, sha512 |

Integers are little-endian by default, which is what x86 and ARM both
use; the `BE` variants exist because network protocols went the other
way. Sizes are 1, 2, 4 or 8 bytes, and anything else is an error rather
than a guess.

`bytes.slice` clamps instead of failing - reading past the end of a
buffer is ordinary when parsing, and a truncated result is more useful
than a crash. `bytes.concat` and `bytes.slice` both copy, so a later
write cannot reach backwards into something you already handed away.

**Why a separate type.** Binary used to live in a `str`, and the
conversion is in fact lossless. What breaks is everything that assumes
text. On four bytes that are not valid UTF-8:

| Operation | Result |
| --- | --- |
| `len`, `trim` | unchanged |
| `upper` | corrupted |
| `json.encode` | every invalid byte becomes U+FFFD, the replacement character |

The data survives right up until something touches it, and then it is
quietly wrong. A separate type makes that impossible rather than merely
unlikely.

### `sound` - playing a WAV

| Function | Returns | Description |
| --- | --- | --- |
| `sound.play(path)` | `bool` | start a `.wav` playing in the background |
| `sound.loop(path)` | `bool` | the same, repeating until stopped |
| `sound.stop()` | | stop whatever is playing |

Both return `false` when the file is missing or cannot be played. One
sound plays at a time: starting another stops the first, which suits
music or an alert but not a game's overlapping effects.

### `rand` - randomness

| Function | Returns | Description |
| --- | --- | --- |
| `rand.int(lo, hi)` | `int` | between `lo` and `hi`, both included |
| `rand.float()` | `float` | between 0 and 1 |
| `rand.bool()` | `bool` | a coin flip |
| `rand.hex(n)` | `str` | `n` random hex digits |
| `rand.uuid()` | `str` | a version 4 UUID |
| `rand.pick(list)` | `T` | one element |
| `rand.shuffle(list)` | `[]T` | a reordered copy |
| `rand.sample(list, n)` | `[]T` | `n` elements, no repeats |
| `rand.seed(n)` | - | fix the sequence |

Programs differ from run to run without seeding. `rand.seed` is for
when you want the same sequence every time - a test, or a puzzle with a
daily number.

```veyl
rand.seed(7)
print(rand.int(1, 100))       // 87, every time
print(rand.pick(["a", "b"]))
```

`rand.shuffle` returns a **new** list and leaves the original alone,
because assignment in Veyl copies and a function that quietly
reordered its argument would not fit that. `rand.pick` on an empty list
gives the zero value; `rand.int(9, 2)`, where the range is backwards,
gives `9` rather than failing.

### `stats` - summarising numbers

Every one of these takes `[]int` or `[]float` and returns `float`.

| Function | Description |
| --- | --- |
| `stats.mean(xs)` | the average |
| `stats.median(xs)` | the middle value, or the middle pair averaged |
| `stats.var(xs)` | the **sample** variance, dividing by n-1 |
| `stats.stdev(xs)` | the square root of that |
| `stats.percentile(xs, p)` | interpolated, `p` from 0 to 100 |

```veyl
let scores = [2, 4, 4, 4, 5, 5, 7, 9]
print(stats.mean(scores))            // 5
print(stats.median(scores))          // 4.5
print(stats.percentile(scores, 90))  // 7.6
```

`stats.var` is the sample variance, dividing by n-1 rather than n. That
is what you want when your numbers are measurements rather than an
entire population, which is the usual case and the one people get
wrong. An empty list gives 0, and so does a single value, which has no
spread to measure.

### `term` - colour and the console

| Function | Returns | Description |
| --- | --- | --- |
| `term.red(s)` and `green` `yellow` `blue` `magenta` `cyan` `grey` | `str` | coloured |
| `term.bold(s)` and `dim` `underline` `invert` | `str` | styled |
| `term.bar(done, total, width)` | `str` | a progress bar |
| `term.clear()` | - | clear the screen |
| `term.colour()` | `bool` | whether colour will actually show |

```veyl
print(term.green("passed") + " " + term.grey("0.4s"))
print(term.bar(7, 10, 20))           // [##############------]
```

**Colour turns itself off when it would be noise.** If output is being
piped into a file or another program, or `NO_COLOR` is set, the styling
functions return the text unchanged rather than filling the file with
escape sequences. `term.colour()` tells you which is happening, so a
program can pick a different layout rather than relying on colour that
is not there.

On Windows the console has to be switched into virtual-terminal mode or
the escape codes appear literally as `ESC[31m`. Veyl does that
automatically at startup for any program using `term`.

### `url` - taking a URL apart

| Function | Returns | Description |
| --- | --- | --- |
| `url.scheme(u)` `host` `port` `path` `fragment` | `str!` | one piece of it |
| `url.query(u)` | `{str: str}!` | the query string as a map |
| `url.build(base, params)` | `str` | append an encoded query |
| `url.join(base, ref)` | `str!` | resolve a relative reference |

```veyl
let u = "https://api.example.com:8443/v2/items?limit=10"
print(must(url.host(u)))               // api.example.com
print(must(url.port(u)))               // 8443
print(must(url.query(u))["limit"])     // 10
print(url.build("https://x.dev/s", {"q": "veyl"}))
```

A missing piece is an empty string, not a failure - a URL with no port
is perfectly valid. Failure is reserved for a URL that will not parse.
`url.build` sorts its parameters, so the same map always produces the
same URL; that matters for caching and for tests. A repeated query key
keeps its first value, since a map cannot hold both.

### `args` - the command line

| Function | Returns | Description |
| --- | --- | --- |
| `args.flag(name)` | `bool` | was `--name` given |
| `args.value(name, fallback)` | `str` | the value after `--name` |
| `args.rest()` | `[]str` | everything that is not a flag |

```veyl
if args.flag("verbose") {
    print("starting")
}
for file in args.rest() {
    print(must(os.file.read(file)))
}
```

```
veyl run tool.vl --verbose --out=result.txt notes.txt
```

Both `--name=value` and `--name value` work, and one dash or two are
the same. `args.rest()` skips a flag's value, so the list is only the
positional arguments. `os.args()` is still there for the raw list.

### `bits` - bit twiddling and bases

| Function | Returns | Description |
| --- | --- | --- |
| `bits.count(n)` | `int` | how many bits are set |
| `bits.length(n)` | `int` | bits needed to represent it |
| `bits.leading(n)` / `trailing(n)` | `int` | zeros at each end |
| `bits.reverse(n)` | `int` | reverse the low 32 bits |
| `bits.rotate(n, by)` | `int` | rotate the low 32 bits left |
| `bits.toBase(n, base)` | `str` | 2 to 36; `""` if out of range |
| `bits.fromBase(s, base)` | `int!` | parse in that base |

```veyl
print(bits.count(255))            // 8
print(bits.toBase(255, 16))       // ff
print(must(bits.fromBase("1010", 2)))  // 10
```

`& | ^ ~ << >>` are operators in the language already; these are the
operations that need a function.

---

## Native functions: `extern`

An `extern` declares a function that lives in a DLL instead of in your
program. The declaration is the whole bridge - nothing to install, no
wrapper to write - and Windows loads each named library when the
program starts.

```veyl
extern fn Beep(freq: int, ms: int) -> bool
extern fn MessageBoxA(hwnd: int, text: str, cap: str, kind: int) -> int

Beep(440, 200)
```

Call one like any other function.

### Where the library comes from

A name in PascalCase is assumed to be the Windows API and lands in
`kernel32.dll`; any other name falls back to `msvcrt.dll`, the C
runtime. To point somewhere else, add a `from` clause:

```veyl
extern fn MessageBeep(kind: int) -> bool from "user32.dll"
extern fn mz_extract(zip: str, dest: str) -> int from "miniz"
```

The `.dll` suffix is optional. `from` is only a keyword in that spot,
so an ordinary variable or function called `from` still works.

### Types at the boundary

| Veyl type | Passes as | Comes back as |
| --- | --- | --- |
| `int` | a 64-bit word | C `int`, sign-extended |
| `bool` | 0 or 1 | C `int`, sign-extended |
| `float` | a 64-bit double | a double |
| `str` | a pointer to its bytes | `char*`, copied into a fresh string |
| `ptr` | a raw machine word | the full 64-bit value |
| `bytes` | a pointer to its first byte | - |
| an extern struct | its address | its address, as a view |

Two rows need explaining.

**`int` versus `ptr` as a return.** A C function returning `int`
promises only the low 32 bits of the result register. Saying
`-> int` tells the compiler to sign-extend those; saying `-> ptr`
takes all 64 bits, which is what handles and pointers need. Guessing
wrong on a handle leaves garbage in its top half, so pointers get
their own spelling rather than sharing `int`.

**`str` as a return.** The C side hands back a pointer to memory Veyl
does not own. The compiler copies the bytes into a fresh string at
once - nothing to free, and the library cannot take the memory back
later. This relies on C strings being NUL-terminated, which they are
by convention.

**`bytes` as a parameter** is how a buffer is handed to a function
that fills one in. Make it the size you need with `bytes.fill`, pass
it, and read it afterwards.

Lists, maps, Veyl structs and `T!` cannot cross. A function can, as a
callback - see below. Anything else is an error pointing at the
declaration.

### Variadic

`...` marks a C variadic function such as printf. Arguments beyond the
named ones pass through as themselves, with floats duplicated into
integer registers because the x64 calling convention requires it:

```veyl
extern fn printf(fmt: str, ...) -> int
printf("%d %s\n", 7, "dots")
```

### Rules

- An extern has no body, cannot be a method, and cannot be used as a
  value - calling it is the only thing that works.
- Arity is checked like any function's; a variadic extern takes at
  least its named arguments.
- If the program needs a library that is not beside the source, in an
  installed package, in System32 or on PATH, `veyl build` and
  `veyl run` refuse before building and name it. Windows would
  otherwise abort the program at startup with an error number instead
  of a sentence.

Values passed to C are safe from the collector for as long as the call
lasts: the caller still holds each one, and the collector never moves
anything. If C keeps a pointer after the call returns, keep the value
too - in a const, or somewhere the program can still reach.

### `extern struct` - C layouts

An `extern struct` describes bytes laid out the way C lays them out: a
Win32 structure, a block from `mem.alloc`, a structure in another
program read into a buffer. A value of one is a **view**: the address
of its first byte. Reading a field loads it from memory at its own
width; writing one stores it there.

```veyl
extern struct Vec3 { x: f32, y: f32, z: f32 }

extern struct Player {
    hp: i32
    alive: bool
    pos: Vec3
    name: [16]u8
    ammo: u16 at 0x40
}
```

| Field type | Reads as |
| --- | --- |
| `i8` `u8` `i16` `u16` `i32` `u32` `u64` `f32` | that [fixed-width type](#fixed-width-numbers) |
| `i64` `f64` | `int`, `float` |
| `ptr` | `int`, all 64 bits |
| `bool` | `bool`, one byte, anything but 0 is true |
| another extern struct | a view of it, inside this one |
| `*T` | a pointer, all 64 bits - see [Pointers](#pointers) |
| `[N]T` | `*T`, a pointer to the first element |

Fields go where a C compiler would put them: each at the next multiple
of its own size, the whole padded to its largest field. `at` pins a
field to an exact offset instead, and the fields after it carry on from
its end - which is how a structure is described when only a few of its
offsets are known.

```veyl
let raw = mem.alloc(Player.size)    // Player.size is 68
let p = Player(raw)                 // a view at an address
p.hp = 100
p.hp -= 25                          // compound assignment works
p.pos.x = 1.5                       // a nested struct is a view too
print(mem.str(p.name))              // an array reads as a pointer to its start
print(p)                            // Player{hp: 75, alive: false, ...}
mem.free(raw)
```

A literal allocates zeroed memory the collector owns, for the common
case of filling in a structure and passing it to Windows:

```veyl
extern struct SYSTEMTIME { year: u16, month: u16, weekday: u16, day: u16,
                           hour: u16, minute: u16, second: u16, ms: u16 }
extern fn GetSystemTime(t: SYSTEMTIME)

let now = SYSTEMTIME{}
GetSystemTime(now)
print(now.year)
```

Assigning a view copies the address, never the bytes: two names for
the same memory, which is the point. `==` compares addresses.
`mem.addr(v)` gives the address as an `int`. Methods work on extern
structs the same as on any other.

Nothing checks that a view points somewhere valid. Reading a field of
one that does not stops the program the way it would in C.

`examples/ffi/memreader.vl` puts this together: it lists running
programs, finds where one is loaded, and follows a pointer chain
through its memory.

### Pointers

`*T` is an address with a known thing at it. `T` is a C scalar (`i8`
to `u64`, `f32`, `f64`, `bool`, `ptr`), an extern struct, or another
pointer.

```veyl
let raw = mem.alloc(64)
let p = raw as *i32           // an address becomes a pointer with `as`
*p = 7                        // write through it
p[1] = -3                     // p[i] is the i-th i32 after p
*p += 10
print(*p)                     // 17
print(p[1])                   // -3

let q = p + 2                 // + and - count in elements, like C
print(q - p)                  // 2
print((q as int) - (p as int))  // 8 bytes
```

The width, sign and stride all come from the type: a `*u8` reads one
byte from 0 to 255, a `*i8` the same byte as -128 to 127, a `*f32` a C
float. Nothing needs a `mem.readI32` or a `* 4`.

A pointer to an extern struct reads its fields the way C's `->` does,
and indexes an array of them:

```veyl
extern struct Vec3 { x: f32, y: f32, z: f32 }

let vs = mem.alloc(Vec3.size * 3) as *Vec3
vs[1].y = 2.5                 // the second Vec3
let second = vs + 1
print(second.y)               // 2.5
let v = vs[1]                 // a view of it, as before
```

A field can be a pointer, including to its own kind, so a list in
memory is written the way it is in C:

```veyl
extern struct Node {
    value: i32
    next: *Node
}

let n = head
while n != nil {
    print(n.value)
    n = n.next
}
```

`nil` is the null pointer: `p == nil`, `p != nil`, `let p: *Node = nil`.
Pointers of the same type compare with `==`, `<` and the rest.

`&` takes the address of memory - a field of an extern struct, `*p` or
`p[i]` - and gives back a pointer to it:

```veyl
let pz = &vs[2].z             // *f32
*pz = 8.0
```

`&` on a variable gives its address, which is what a native function
with an out-parameter wants:

```veyl
extern fn strtol(s: str, end: **u8, base: i32) -> i32 from "msvcrt"
extern fn frexp(x: float, exp: *i32) -> float from "msvcrt"

let end: *u8 = nil
let n = strtol("1234xyz", &end, 10)   // n is 1234, end points at "xyz"
let e: i32 = 0
let m = frexp(12.0, &e)               // m is 0.75, e is 4

fn bump(p: *i32) {
    *p += 1
}
let count: i32 = 41
bump(&count)                           // count is 42
```

A variable can be a number of any width, a `bool` or a pointer; the
pointer's type is the variable's own, so `&count` above is a `*i32`,
`&x` for an `int` is a `*i64` and for a `float` a `*f64`. While its
address is taken the variable is kept in memory at that width, so what
native code writes through the pointer, even into part of it, is what
the variable reads afterwards. For an extern struct variable, which is
already an address, `&v` is a pointer to the struct it views.

The variable lives until its function returns, and its address must
not outlive that. The compiler follows the address through the
function - into other variables, through `+`, `-` and `as` - and
refuses the ways out it can see:

```veyl
fn broken() -> *i32 {
    let x: i32 = 5
    return &x            // error: the address of a local variable
}                        //        cannot be returned - use new
```

Returning it, storing it in a global, a field, a list, a map or
through another pointer, and using it in a closure are all errors.
Passing it to a function you call is not, as in C: that function must
not keep it. A `for` loop's variable, a `const` and a global have no
address to give.

`as` changes what an address is seen as, never the address: between
`int`, any pointer type, and an extern struct view. A pointer goes
anywhere an address as an `int` goes - `mem.*`, an extern's `ptr`, a
function taking `int` - without `as`, but an `int` never becomes a
pointer by itself: `let p: *i32 = raw` is an error, `raw as *i32` is
not.

Across `extern`, a pointer is one 64-bit word both ways, like `ptr`,
and a callback can take pointers:

```veyl
extern fn qsort(base: *i32, n: int, size: int, cmp: fn(*i32, *i32) -> int) from "msvcrt"

fn ascending(x: *i32, y: *i32) -> int {
    return *x - *y
}
qsort(nums, 5, 4, ascending)
```

Nothing checks that a pointer points somewhere valid, the same as C:
reading through a bad one stops the program. `print(p)` prints the
address as a number.

### `new` and `delete`

`new` allocates zeroed memory from the C heap for one value and gives
back a pointer to it; `new [n]T` allocates `n` of them in a row, and
`new T{...}` fills fields in. `delete(p)` frees it.

```veyl
extern struct Vec3 { x: f32, y: f32, z: f32 }

let v = new Vec3{x: 1, y: 2}      // *Vec3
v.z = 3
let pts = new [100]Vec3           // *Vec3 to the first of 100
pts[5] = *v                       // copies the twelve bytes
let flags = new [64]u8            // *u8
let count = new i32               // *i32
*count += 1

delete(v)
delete(pts)
delete(flags)
delete(count)
```

`T` is a C scalar (`i8` to `u64`, `f32`, `f64`, `bool`, `ptr`, and
`int` and `float` as `i64` and `f64`), an extern struct or a pointer
type. A Veyl struct is made with a literal instead; the collector owns
it.

This memory is never collected and never moved. That is the
difference from a literal, `Vec3{...}`, whose bytes the collector owns
and frees once nothing refers to them. `new` memory is freed by
`delete` or not at all, and it comes from the same C runtime as
native code linked against `msvcrt`, so either side can free what the
other allocated. A pointer to it can be kept anywhere - a local, a
field of a Veyl struct, a list, a map, a global - and passed to native
code as it is. `delete` on `nil` does nothing; using memory after
`delete`, or deleting it twice, is undefined, as in C. When memory
runs out, `new` stops the program with a runtime error.

A pointer to an extern struct reaches its fields and calls its methods
the way C++'s `->` does:

```veyl
impl Vec3 {
    fn scale(self, k: f32) {
        self.x *= k
        self.y *= k
        self.z *= k
    }
}

let p = new Vec3{x: 1, y: 2, z: 3}
p.scale(2)
print(*p)                         // Vec3{x: 2, y: 4, z: 6}
```

Assigning a pointer or a view copies the address. `*dst = *src`, or
`dst[i] = src`, copies the bytes.

### Callbacks

A parameter with a function type takes a Veyl function that native
code will call back:

```veyl
extern fn qsort(base: ptr, n: int, size: int, cmp: fn(ptr, ptr) -> int)

fn byValue(a: int, b: int) -> int {
    return mem.readI32(a) - mem.readI32(b)
}

qsort(buf, count, 4, byValue)
```

```veyl
extern fn EnumWindows(each: fn(ptr, ptr) -> bool, data: ptr) -> bool from "user32"

fn onWindow(hwnd: int, data: int) -> bool {
    return true                     // keep going
}

EnumWindows(onWindow, 0)
```

The types in the callback's signature follow the rest of the boundary:
`int` and `bool` are the C types, 32 bits, and `ptr` is a full 64-bit
value - so a handle, a pointer, an `LPARAM` or an `LRESULT` is `ptr`. A
`float` is a double. The Veyl function itself takes plain `int`s.

A fixed-width type in the signature is exactly that C type: `u8`,
`i16`, `u32` and the rest arrive at their width, whatever the caller
left in the rest of the register, and `f32` is a C `float`, in and out.
The same holds for an extern declaration, whose `f32` parameters are
passed as singles and whose narrow results are read at their width,
and for `export fn`. A callback or an export returning `f32` can take
at most four arguments.

Only a function declared with `fn`, named directly, can be a callback.
A closure carries its captured variables somewhere native code does
not know about. A callback may allocate and may run the collector.

### Building a DLL

`veyl build --dll mod.vl` writes `mod.dll`. `export fn` puts a function
in its export table under its own name:

```veyl
print("mod loaded")               // runs once, as the DLL loads

export fn add(a: int, b: int) -> int {
    return a + b
}
```

Anything that loads DLLs can load it - a mod loader, a host program's
plugin folder, `LoadLibrary` and `GetProcAddress`, or another Veyl
program:

```veyl
extern fn add(a: ptr, b: ptr) -> ptr from "mod"
print(add(40, 2))
```

For code calling an export, a Veyl `int` is a 64-bit `int64_t`, a
`float` is a `double`, a `str` is a `const char*`, a `bool` is read as a
C `BOOL`, and an extern struct is a pointer to it.

The top-level statements run while Windows holds its loader lock, so
they should set up and return; the work belongs in what the host calls.
A DLL does not change the host's console, and a failed `must` or an
`exit` ends the whole host process, as it would in C.

---

## `proc` - process memory

The `proc` library reads and writes another process's memory through
the standard Windows debugging calls - the same ones a debugger, a
profiler or a memory-inspection tool uses. It is built on `mem.symbol`
and `mem.call`, so it needs nothing declared with `extern`.

| Function | Returns | Description |
| --- | --- | --- |
| `proc.current()` | `int` | a handle to the running process itself |
| `proc.pid()` | `int` | the running process's id |
| `proc.open(pid)` | `int` | a handle to another process for reading and writing, or `0` if it cannot be opened |
| `proc.close(h)` | `bool` | close a handle from `proc.open` |
| `proc.read(h, addr, n)` | `bytes` | `n` bytes at `addr`, or an empty `bytes` if the read failed |
| `proc.write(h, addr, data)` | `int` | write a `bytes`, returning how many bytes were written |
| `proc.readU8(h, addr)` | `int` | one byte, 0 to 255 |
| `proc.readI32(h, addr)` | `int` | four bytes, signed |
| `proc.readI64(h, addr)` | `int` | eight bytes |
| `proc.writeI32(h, addr, v)` | `bool` | write four bytes |
| `proc.writeI64(h, addr, v)` | `bool` | write eight bytes |
| `proc.scan(h, start, size, pattern)` | `int` | the address of the first match of a byte pattern in `[start, start+size)`, or `-1` |

`proc.open` needs the same rights the target runs with: a process
owned by another user, or one running as administrator, needs this
program to be as well, and returns `0` otherwise. A handle from
`proc.open` is closed with `proc.close`; the one from `proc.current`
is not.

```veyl
let h = proc.current()
let cell = mem.alloc(8)

proc.writeI32(h, cell, 1000)
print(proc.readI32(h, cell))         // 1000

proc.write(h, cell, must(bytes.fromHex("deadbeef")))
print(bytes.hex(proc.read(h, cell, 4)))   // deadbeef
mem.free(cell)
```

A read that fails - an address that is not mapped, or a handle without
the rights - comes back empty rather than stopping the program, so a
scan over a range does not have to know in advance which parts are
readable. `proc.scan` takes a byte pattern in hex separated by spaces,
with `??` matching any byte, the same form `mem.scan` takes:

```veyl
let at = proc.scan(h, region, 0x1000, "90 48 8B ?? ??")
if at != -1 {
    print(proc.readU8(h, at))         // 144, the 0x90
}
```

Opening another process is the same handle in every call:
`proc.open(pid)` for a pid, then `proc.read`, `proc.write` and
`proc.scan` on the handle it returns, and `proc.close` at the end.

### Listing processes, modules, threads and regions

| Function | Returns | Description |
| --- | --- | --- |
| `proc.pids()` | `[]int` | the id of every running process |
| `proc.name(pid)` | `str` | a process's executable name, `""` if it is not running |
| `proc.parent(pid)` | `int` | the pid of the process that started it, or `0` |
| `proc.find(name)` | `int` | the pid of the first process with that executable name, ignoring case, or `0` |
| `proc.modules(pid)` | `[]ProcModule` | the executable and DLLs loaded in a process, the executable first |
| `proc.base(pid, name)` | `int` | where a module is loaded, or `0`; `""` means the executable |
| `proc.threads(pid)` | `[]int` | the id of every thread of a process |
| `proc.regions(h)` | `[]ProcRegion` | the committed regions of a process's address space, lowest first |
| `proc.scanAll(h, pattern)` | `[]int` | the address of every match of a byte pattern in the readable memory of a process |

A pid of `0` in `proc.modules`, `proc.base` and `proc.threads` means
the running process. `proc.regions` and `proc.scanAll` take a handle,
from `proc.open` or `proc.current`, because they read the process
rather than ask the system about it.

A `ProcModule` has `name` (`"kernel32.dll"`), `path` (the full path of
the file), `base` and `size`. A `ProcRegion` has `base`, `size`,
`protect` (the Windows page protection, such as `0x04` for read-write),
`kind` (`0x1000000` an image, `0x40000` a mapped file, `0x20000`
private memory) and three bools worked out from the protection:
`readable`, `writable` and `executable`. Both are ordinary structs; a
program that uses these calls cannot declare a struct of its own with
either name.

```veyl
let pid = proc.find("notepad.exe")
if pid == 0 {
    print("notepad is not running")
    exit(1)
}
for m in proc.modules(pid) {
    print("{bits.toBase(m.base, 16)}  {m.size}  {m.name}")
}

let h = proc.open(pid)
let total = 0
for r in proc.regions(h) {
    if r.writable {
        total += r.size
    }
}
print("{total} bytes of writable memory in {len(proc.threads(pid))} threads")
proc.close(h)
```

A process that cannot be looked into gives an empty list or a `0`, the
same rights question as `proc.open`. A module list of a process that is
loading a DLL at that moment is retried rather than reported empty.

`proc.scanAll` walks the readable regions and reads each a megabyte at
a time, so it needs no start or size and a match lying across two
reads is still found. Scanning the running process itself also finds
the scanner's own working copy of whatever it just read; scanning
another process does not have that echo. Names are read in the system
code page, so a name with characters outside it comes back altered.

---

## `win` - windows, drawing and input

Veyl opens a real window you can draw in. The shape is a **game loop**:
open once, then poll, draw, present, repeat.

```veyl
let w = must(win.open("my game", 800, 500))

while win.poll(w) {
    if win.pressed(w, "esc") { break }

    win.clear(w, win.rgb(18, 20, 28))
    win.circle(w, 400, 250, 40, win.rgb(120, 200, 140))
    win.text(w, 20, 20, "hello", win.rgb(235, 238, 245))

    win.present(w)
    sleep(16)
}
win.close(w)
```

That is a complete program. `sleep(16)` is roughly 60 frames a second.

### The loop

| Function | Returns | Description |
| --- | --- | --- |
| `win.open(title, w, h)` | `int!` | a window handle; `w` and `h` are the **drawing area** |
| `win.poll(w)` | `bool` | handle input; `false` once the window is closed |
| `win.present(w)` | | show everything drawn since the last present |
| `win.close(w)` | | |

`win.poll` is what makes the window respond at all. Without calling it
the window freezes, because nothing is reading its messages.

Drawing goes into a back buffer and `win.present` copies it to the
screen in one go. That is why nothing flickers, and why you should draw
a whole frame before presenting rather than presenting as you go.

### Drawing

| Function | Description |
| --- | --- |
| `win.rgb(r, g, b)` | a colour, each 0 to 255 |
| `win.clear(w, colour)` | fill the whole window |
| `win.rect(w, x, y, width, height, colour)` | filled rectangle |
| `win.circle(w, x, y, radius, colour)` | filled circle, centred on `x, y` |
| `win.line(w, x1, y1, x2, y2, colour)` | one-pixel line |
| `win.text(w, x, y, s, colour)` | text, `x, y` is the top-left |
| `win.frame(w, x, y, width, height, colour)` | a one-pixel outline |

`0, 0` is the top-left corner and y grows downwards, which is the usual
convention for screens and the opposite of graph paper.

### Input

| Function | Returns | Description |
| --- | --- | --- |
| `win.pressed(w, name)` | `bool` | is this key held down |
| `win.key(w, code)` | `bool` | the same, by virtual key code |
| `win.mouseX(w)` `win.mouseY(w)` | `int` | pointer position |
| `win.mouseDown(w)` | `bool` | is the left button held |
| `win.clicked(w)` | `bool` | was it clicked **this frame** |

Key names: `"left"`, `"right"`, `"up"`, `"down"`, `"space"`, `"esc"`,
`"enter"`, `"tab"`, `"backspace"`, `"shift"`, `"ctrl"`, `"alt"`, and
any single character - `win.pressed(w, "a")`, `win.pressed(w, "7")`.

The difference between `mouseDown` and `clicked` matters. `mouseDown`
is true for every frame the button is held, so a button wired to it
fires sixty times a second. `clicked` is true for one frame per press.

### Widgets

Built on the above, and written in Veyl. They are **immediate mode**:
there is no widget tree and nothing to keep in sync, so a button draws
itself and tells you whether it was clicked in the same call.

| Function | Returns | Description |
| --- | --- | --- |
| `win.button(w, x, y, width, height, label)` | `bool` | draws a button, true when clicked |
| `win.bar(w, x, y, width, height, frac, colour)` | | a fill bar, `frac` from 0.0 to 1.0 |
| `win.hover(w, x, y, width, height)` | `bool` | is the pointer inside this box |

```veyl
let count = 0
while win.poll(w) {
    win.clear(w, win.rgb(22, 24, 32))
    if win.button(w, 24, 24, 130, 40, "count up") {
        count = count + 1
    }
    win.text(w, 24, 80, "count is {count}", win.rgb(235, 238, 245))
    win.present(w)
    sleep(16)
}
```

Because the button is drawn fresh every frame, moving it is just
changing the numbers - there is nothing to update or lay out again.

### Size and the title

| Function | Description |
| --- | --- |
| `win.width(w)` `win.height(w)` | the drawing area, in pixels |
| `win.title(w, s)` | change the title bar |
| `win.resizable(w, on)` | add or remove the drag border |

**Windows open at a fixed size.** No drag border, no maximise button.
That is almost always what a game wants, and it matches the back buffer
being made once at a known size.

`win.resizable(w, true)` turns it on, and the buffer is rebuilt when
the drawing area changes, so `win.width(w)` and `win.height(w)` are
always current. Draw in terms of them rather than the numbers you
passed to `win.open` and the layout follows the window:

```veyl
win.resizable(w, true)
while win.poll(w) {
    win.clear(w, bg)
    // stays centred however the window is dragged
    win.circle(w, win.width(w) / 2, win.height(w) / 2, 40, fg)
    win.present(w)
    sleep(16)
}
```

The size you pass to `win.open` is the **drawing area**, not the window
including its title bar, so `win.open("x", 640, 400)` gives you exactly
640 by 400 pixels to draw in.

### Images

Images are `.png` or `.bmp` files, drawn into the back buffer like
everything else. A PNG keeps its transparency: every pixel is blended
with what is already there by its own alpha.

| Function | Returns | Description |
| --- | --- | --- |
| `win.image(path)` | `int!` | load a `.png` or `.bmp`; the result is an image handle |
| `win.imageWidth(img)` `win.imageHeight(img)` | `int` | its size in pixels |
| `win.draw(w, img, x, y)` | | draw it at its own size |
| `win.drawScaled(w, img, x, y, width, height)` | | draw it stretched to a size |
| `win.drawKeyed(w, img, x, y, colour)` | | draw it with every pixel of one colour left out, ignoring alpha |
| `win.freeImage(img)` | | release it |

A PNG with a transparent background needs nothing more than `draw`:

```veyl
let player = must(win.image("player.png"))
while win.poll(w) {
    win.clear(w, bg)
    win.draw(w, player, x, y)
    win.present(w)
    sleep(16)
}
```

A `.bmp` has no alpha. `drawKeyed` is how one gets a transparent
background: paint the background one colour nobody uses - magenta,
`win.rgb(255, 0, 255)`, is the tradition - and name that colour when
drawing.

PNGs are decoded by Veyl itself. Every colour type is supported at 8
bits a sample, and palette images at 1, 2, 4 and 8 bits; interlaced and
16-bit PNGs are refused with a message saying so. Decoding is not fast
- about half a second for a large, noisy 512x512 image - so load images
once, before the game loop.

### Measuring text

| Function | Returns | Description |
| --- | --- | --- |
| `win.textWidth(w, s)` | `int` | how wide `s` is when drawn, in pixels |
| `win.textHeight(w)` | `int` | the height of a line of text |

```veyl
let label = "game over"
let x = (win.width(w) - win.textWidth(w, label)) / 2
win.text(w, x, 200, label, white)          // centred
```

### Canvases

`win.canvas(width, height)` is a back buffer with no window: every
drawing function works on it, nothing appears on screen, and
`win.pixel(c, x, y)` reads a colour back - `-1` outside it. It is for
drawing off-screen, and for checking what was drawn:

```veyl
let c = win.canvas(64, 64)
win.clear(c, win.rgb(0, 0, 0))
win.circle(c, 32, 32, 10, win.rgb(255, 255, 255))
print(win.pixel(c, 32, 32) == win.rgb(255, 255, 255))   // true
```

`win.pixel` works on a window too, on what has been drawn since the
last `present`.

### Pixels

`win.pixels(w)` is the back buffer of a window or canvas as memory: a
`*u32`, one per pixel, rows top to bottom, `width * y + x` for the pixel
at `x, y`. A pixel is `0x00RRGGBB` - note that this is not the order
`win.rgb` uses, which is Windows' own `0x00BBGGRR`.

```veyl
let px = win.pixels(w)
let W = win.width(w)
for x in 0..W {
    px[100 * W + x] = (255 << 16) | (200 << 8) | 0    // an orange line at y = 100
}
win.present(w)
```

Writing through the pointer is as fast as writing memory, which is
what a renderer, an emulator or anything drawing many pixels a frame
wants; every `win.line` or `win.rect` is a call into Windows. The two
mix freely: `win.text` and the rest draw into the same pixels. GDI
batches its drawing, so call `win.pixels` after the GDI calls whose
result you want to read, since it flushes them first.

Nothing checks the index. The buffer is replaced when a resizable
window changes size, so take the pointer again each frame rather than
keeping it. `examples/gui/render3d.vl` is a 3D renderer built on this,
with a depth buffer of its own from `mem.alloc`.

### What it does not do yet

No JPEG, and no interlaced or 16-bit PNG. One font, at the system
size. One window per program.

Working examples are in `examples/gui/`: `pong.vl` is a playable game
in about 130 lines, `widgets.vl` exercises the widget set, and
`sprites.vl` draws images and centres text.

## Reserved words

In use:

```
let const fn return if else while for in step break continue true false
struct impl self match nil import pub extern
```

Keywords only in one place, and ordinary names everywhere else: `var`
and `enum` at the top of a file, `export` before `fn`, `from` after an
extern, and `at` in an extern struct field.

Reserved but not yet implemented - the lexer recognises them, so they
cannot be used as names:

```
defer own unsafe
```

---

## Compiler commands

| Command | Effect |
| --- | --- |
| `veyl run f.vl` | compile and run |
| `veyl build f.vl` | write `f.exe` next to the source |
| `veyl build --dll f.vl` | write `f.dll`; see [Building a DLL](#building-a-dll) |
| `veyl asm f.vl` | print the generated assembly |
| `veyl ir f.vl` | print the intermediate representation |
| `veyl version` | print the version |
| `veyl f.vl` | same as `run` |

### Packages

| Command | Effect |
| --- | --- |
| `veyl get <name>` | install from the official registry |
| `veyl get <user/repo>` | install from any GitHub repository |
| `veyl get <url>` | install from a url |
| `veyl get officials` | install every official package |
| `veyl get officials --nodlls` | the same, minus the ones carrying a native library |
| `veyl get <user/repo> --all` | every package a registry lists |
| `veyl list` | what is installed here |
| `veyl remove <name>` | uninstall it |

Packages land in `./veyl_modules` next to your program rather than
machine-wide, so a project carries its own dependencies and deleting
the directory uninstalls everything. A bare import finds one:

```veyl
import "totp"
print(must(totpNow(secret)))
```

An import lands in a **flat namespace** - there is no `totp.`
qualifier - so packages prefix their exported names by convention.

A package may carry a native `.dll`, which `veyl build` copies next to
the executable. That is how [`sqlite`](#db---sqlite) works without
every install carrying three megabytes of database.

#### What is in the official registry

| Package | What it is |
| --- | --- |
| `dotenv` | `.env` files: quoting, `export` prefixes, typed reads |
| `ini` | `[sections]`, `key = value`, comments, stable round trip |
| `jwt` | JSON Web Tokens, HS256, signing and verifying |
| `querybuilder` | Building SQL without pasting values into it |
| `sqlite` | The `sqlite3.dll` that [`db`](#db---sqlite) needs |
| `toml` | TOML, flattened to dotted keys, typed reads |
| `totp` | TOTP and HOTP, the codes an authenticator app shows |
| `validator` | Checking data, collecting every problem rather than the first |

Each one documents itself at the top of its `.vl` file, including what
it deliberately does not do. `toml`, for instance, refuses dates,
inline tables and arrays of tables rather than guessing at them.

The registry is
[owlspan/veyl-packages](https://github.com/owlspan/veyl-packages), and
its README covers writing your own. You do not have to be in the
registry to publish one.

Anything after the `.vl` file goes to your program rather than to the
compiler, so this reaches `args.flag("verbose")`:

```
veyl run tool.vl --verbose
```

### Reading what it compiled to

`asm` and `ir` are the debugging tools, and they are the most useful
thing here when a program does something you did not expect. `ir` is
the readable one - three-address code over virtual registers, before
anything knows an x86 register exists.

```
veyl ir hello.vl
veyl asm hello.vl
```

### Right-clicking a .vl file

The installer puts these on the menu:

- **Run with Veyl**
- **Compile to .exe**
- **Veyl**, a submenu: the generated assembly, the IR, or a prompt in
  that folder

Right-clicking any folder gives **Open Veyl prompt here**. Double-
clicking a `.vl` file runs it. All of them open a console that stays
up, so you can read what it said whether that was output or errors.

### Errors

Every error names a file, a line and a column, and the compiler reports
all of them rather than stopping at the first:

```
hello.vl:3:1: "y" is declared as int but the value is str
hello.vl:4:9: '+' needs numbers, got int and bool
veyl: 2 error(s)
```

One gap: a misspelled name is found by a later stage than the type
checker, so it is only reported once every type error is fixed.

---

## Known limitations

Honest list of what v0.41.0 does not do yet.

**The language**

- **A callback cannot be a closure.** Native code can call a function
  declared with `fn`, but not one that captured variables.
- **A native call through an address is untyped.** `mem.call` passes
  words and takes back `rax`; an `extern fn` is still the way to give
  a native function a checked signature.
- **The library takes `int` and `float`.** `abs`, `min`, `max`, `sqrt`
  and the rest of the math functions, the `mem` functions and the
  bounds of a `for` range do not take the fixed-width types yet: convert
  with `int(x)` or `float(x)`. A map key cannot be one either.
- **`&` has no global to point at.** A local variable, a parameter, a
  field of an extern struct, `*p` and `p[i]` have addresses; a global
  and a loop variable do not.
- **An escaping address is caught within one function.** Passing
  `&x` to a function that keeps it, or smuggling it out as an `int`
  through something the compiler cannot follow, is not detected.
- **`proc` names are in the system code page.** A process or module
  name with characters outside it comes back altered. There is also no
  call yet to suspend a thread or read its registers; those still go
  through `extern`.
- **A missing map key is silent.** `m["absent"]` returns the zero
  value. `has()` and `find()` distinguish it; the bare index was left
  alone because making every map read return `?V` would mean a nil
  check on each one.
- **No namespacing on imports.** Everything `pub` in an imported file
  lands in one flat namespace, so two files exporting the same name
  collide. The error names both files.
- **A map cannot be keyed by an enum.** Keys are still `int` or `str`.
- **Garbage collected**, with raw memory beside it: `mem.alloc` and
  extern structs give manual memory and pointers, but nothing checks an
  address, and `unsafe` is still reserved.

**The library**

- **`net` is plain TCP, with no TLS.** `http.get` and friends do speak
  HTTPS - they go through WinHTTP, which handles the handshake - but a
  socket opened with `net.connect` is unencrypted.
- **SQL is SQLite only**, through the `sqlite` package. No Postgres,
  MySQL or anything over a network.
- **No `zip`.** It exists on the `veylgo` branch and has not been
  ported.
- **The web server handles one request at a time.** Fine for a tool or
  something on your own network, not a production server.
- **A string is NUL-terminated bytes.** Binary data containing a zero
  byte reads back short - use `bytes` for that - and building a string
  by repeated appending is quadratic: push the pieces onto a list and
  `join` them once, which is linear.
- **`upper` and `lower` change ASCII letters only**, where the Go
  backend also changes accented and non-Latin ones.
- **`toFloat` and `isFloat` take decimal numbers only.** Go's parser
  also accepts a hexadecimal mantissa with a `p` exponent, like
  `0x1p-2`.
- **Printing a float rounds through the C library**, which stops at
  seventeen significant digits, so a value needing sixteen can round
  the wrong way in the last place.

**Windows**

- **Windows-only.** The whole `win` library, and the compiler's output,
  target Windows on x86-64. There is no cross-compilation.
- **One window per program**, no images, no sound, one font at one
  size, and no way to measure text.

**Memory**

- **Collection pauses while tasks run.** The collector reads only the
  stack of the thread it runs on, so it waits for a `task` batch to
  finish rather than miss what another thread holds.
- **A DLL never collects by itself.** Its exports may be called on the
  host's threads, which the collector cannot see. Call `mem.collect()`
  from an export when the host is known to be single-threaded.




