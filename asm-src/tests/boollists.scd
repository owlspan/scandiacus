// A []bool keeps one byte per element (elemWidth in list.go). Every
// list operation, checked against what word-sized bools printed.
let a: []bool = [true, false, true]
print(a)
print(len(a))
push(a, true)
a[1] = true
print(a)
print(a[1] && a[3])
var flips = 0
for b in a {
    if b { flips += 1 }
}
print(flips)
let big: []bool = []
for i in 0..1000 {
    push(big, i % 3 == 0)
}
var c = 0
for i in 0..len(big) {
    if big[i] { c += 1 }
}
print(c)
print("{big[999]} {big[998]}")
let b2: []bool = [true, true, false, true]
print(a == b2)
print(a == [true, true, true, true])
print(contains(b2, false))
let s = sortBy(b2, fn(x: bool, y: bool) -> bool { return !x && y })
print(s)
print(slice(b2, 1, 3))
let r = reverse(b2)
print(r)
print("{first(r)} {last(r)} {indexOf(r, false)}")
print(pop(b2))
print(b2)
insert(b2, 0, false)
print(removeAt(b2, 1))
let mapped = map(b2, fn(x: bool) -> bool { return !x })
print(mapped)
print(join(mapped, "-"))
clear(mapped)
print(len(mapped))
print(b2)
print(json.encode(b2))
let back: []bool = must(json.decode("[true,false,false,true]"))
print(back)
print(str(back))
struct S {
    flags: []bool
    n: int
}
let st = S{flags: [false, true], n: 3}
print(st)
