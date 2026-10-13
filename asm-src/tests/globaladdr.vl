// & on a global: the address is the global's own word, valid for the
// whole program, so it can be returned and kept.
var counter = 5
var small: i32 = -1
var flag = false
var ratio = 1.5

fn counterPtr() -> *i64 {
    return &counter
}

let p = counterPtr()
*p = 41
*p += 1
print(counter)

let q = &small
*q = 7
print(small)
*q = -300
print(small + 1)

let b = &flag
*b = true
print(flag)

let r = &ratio
*r = *r * 2.0
print(ratio)
