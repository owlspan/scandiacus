// Inline assembly: values in and out through named registers.

fn popcount(x: int) -> int {
    let n = 0
    asm(rcx = x, rax -> n) {
        "popcnt rax, rcx"
    }
    return n
}

// A loop with labels of its own, used from a loop of the caller's.
fn sumTo(n: int) -> int {
    let total = 0
    asm(rcx = n, rax -> total) {
        "xor eax, eax"
        "test rcx, rcx"
        "jz done"
        "top:"
        "add rax, rcx"
        "dec rcx"
        "jnz top"
        "done:"
    }
    return total
}

// Two blocks in one function may use the same label name.
fn clampBoth(a: int, b: int) -> int {
    let x = 0
    asm(rax = a, rax -> x) {
        "cmp rax, 100"
        "jle ok"
        "mov eax, 100"
        "ok:"
    }
    let y = 0
    asm(rax = b, rax -> y) {
        "cmp rax, 100"
        "jle ok"
        "mov eax, 100"
        "ok:"
    }
    return x + y
}

// Writes every callee-saved register; the caller's values must survive.
fn clobber(seed: int) -> int {
    let out = 0
    asm(rax = seed, rax -> out) {
        "mov rbx, rax"
        "mov rsi, rax"
        "mov rdi, rax"
        "mov r12, rax"
        "mov r13, rax"
        "mov r14, rax"
        "mov r15, rax"
        "pxor xmm6, xmm6"
        "pxor xmm7, xmm7"
        "pxor xmm8, xmm8"
        "pxor xmm9, xmm9"
        "pxor xmm10, xmm10"
        "pxor xmm11, xmm11"
        "pxor xmm12, xmm12"
        "pxor xmm13, xmm13"
        "pxor xmm14, xmm14"
        "pxor xmm15, xmm15"
        "add rax, rbx"
    }
    return out
}

fn hypot(a: float, b: float) -> float {
    let r = 0.0
    asm(xmm0 = a, xmm1 = b, xmm0 -> r) {
        "mulsd xmm0, xmm0"
        "mulsd xmm1, xmm1"
        "addsd xmm0, xmm1"
        "sqrtsd xmm0, xmm0"
    }
    return r
}

print(popcount(255))
print(popcount(0x5555))

let total = 0
for i in 0..5 {
    total += sumTo(i)
}
print(total)
print(clampBoth(7, 500))

let keep = 0
let acc = 0.5
for i in 0..10 {
    keep += clobber(i)
    acc = acc * 2.0
}
print(keep)
print(acc)

print(hypot(3.0, 4.0))

// f32 in and out: a single in the register.
let s: f32 = 1.5
let doubled: f32 = 0.0
asm(xmm2 = s, xmm2 -> doubled) {
    "addss xmm2, xmm2"
}
print(doubled)

// An array goes in as its address; asm can write through it.
let v: [4]f32 = [1.0, 2.0, 3.0, 4.0]
let w: [4]f32 = [10.0, 20.0, 30.0, 40.0]
asm(rcx = v, rdx = w) {
    "movups xmm0, [rcx]"
    "movups xmm1, [rdx]"
    "addps xmm0, xmm1"
    "movups [rcx], xmm0"
}
print(v)

let counts: [2]i64 = [5, 0]
asm(rcx = counts, rax = 3) {
    "lock xadd qword ptr [rcx], rax"
    "mov qword ptr [rcx+8], rax"
}
print(counts)

// Narrow outputs wrap to their type; a bool is any nonzero.
let small: i8 = 0
let flag = false
asm(rax -> small, rdx -> flag) {
    "mov eax, 300"
    "mov edx, 2"
}
print(small)
print(flag)

// Outputs go to globals and to variables a closure captured.
var hits = 0
let seen = 1
let peek = fn() -> int { return seen }
asm(rax -> hits, rcx -> seen) {
    "mov eax, 11"
    "mov ecx, 22"
}
print(hits)
print(peek())

// An instruction the table lacks, written as its bytes: bswap rax.
let swapped = 0
asm(rax = 0x0102030405060708, rax -> swapped) {
    ".byte 0x48, 0x0f, 0xc8"
}
print(swapped == 0x0807060504030201)

let pick = 0
asm(rax = 1, rcx = 2, rdx = 3, rax -> pick) {
    "cmp rcx, rdx"
    "cmovl rax, rdx"
}
print(pick)

let t0 = 0
let t1 = 0
asm(rax -> t0) {
    "rdtsc"
    "shl rdx, 32"
    "or rax, rdx"
}
asm(rax -> t1) {
    "rdtsc"
    "shl rdx, 32"
    "or rax, rdx"
}
print(t1 >= t0)
