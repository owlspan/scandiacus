// AVX, AVX2 and FMA in inline asm. The instructions run only when the
// machine has them, and the results print the same either way: each
// function falls back to plain code that gives the same answer.

// A dot product of two [8]f32 with ymm: multiply, then add across.
fn dot8(a: [8]f32, b: [8]f32) -> f32 {
    let r: f32 = 0.0
    if cpu.avx2() {
        asm(rcx = a, rdx = b, xmm0 -> r) {
            "vmovups ymm0, [rcx]"
            "vmulps ymm0, ymm0, [rdx]"
            "vhaddps ymm0, ymm0, ymm0"
            "vhaddps ymm0, ymm0, ymm0"
            "vextractf128 xmm1, ymm0, 1"
            "vaddps xmm0, xmm0, xmm1"
        }
        return r
    }
    for i in 0..8 {
        r += a[i] * b[i]
    }
    return r
}

// A sum of [16]f64 with vaddpd on ymm, then the halves of the result.
fn sum16(a: [16]f64) -> f64 {
    let r = 0.0
    if cpu.avx2() {
        asm(rcx = a, xmm0 -> r) {
            "vmovupd ymm0, [rcx]"
            "vaddpd ymm0, ymm0, [rcx+32]"
            "vaddpd ymm0, ymm0, [rcx+64]"
            "vaddpd ymm0, ymm0, [rcx+96]"
            "vextractf128 xmm1, ymm0, 1"
            "vaddpd xmm0, xmm0, xmm1"
            "vhaddpd xmm0, xmm0, xmm0"
        }
        return r
    }
    for i in 0..16 {
        r += a[i]
    }
    return r
}

// a*b+c in one fused step on scalars; vfmadd213sd is xmm0 = xmm1*xmm0 + xmm2.
fn fused(a: float, b: float, c: float) -> float {
    let r = 0.0
    if cpu.fma() {
        asm(xmm0 = a, xmm1 = b, xmm2 = c, xmm0 -> r) {
            "vfmadd213sd xmm0, xmm1, xmm2"
        }
        return r
    }
    return a * b + c
}

// Integer add of two [8]i32 into a third, 256 bits at a time.
fn add8(a: [8]i32, b: [8]i32) -> [8]i32 {
    let out: [8]i32
    if cpu.avx2() {
        asm(rcx = a, rdx = b, r8 = out) {
            "vmovdqu ymm0, [rcx]"
            "vpaddd ymm0, ymm0, [rdx]"
            "vmovdqu [r8], ymm0"
        }
        return out
    }
    for i in 0..8 {
        out[i] = a[i] + b[i]
    }
    return out
}

// Reverse the bytes within each 16 of 32: vpshufb works in 128-bit lanes.
fn reverse32(src: [32]u8, mask: [32]u8) -> [32]u8 {
    let out: [32]u8
    if cpu.avx2() {
        asm(rcx = src, rdx = mask, r8 = out) {
            "vmovdqu ymm0, [rcx]"
            "vpshufb ymm0, ymm0, [rdx]"
            "vmovdqu [r8], ymm0"
        }
        return out
    }
    for i in 0..32 {
        out[i] = src[(i / 16) * 16 + 15 - i % 16]
    }
    return out
}

// A loop of its own in the block, with a ymm accumulator.
fn sum64(a: [64]f32) -> f32 {
    let r: f32 = 0.0
    if cpu.avx2() {
        asm(rcx = a, rdx = 8, xmm0 -> r) {
            "vxorps ymm0, ymm0, ymm0"
            "top:"
            "vaddps ymm0, ymm0, [rcx]"
            "add rcx, 32"
            "dec rdx"
            "jnz top"
            "vextractf128 xmm1, ymm0, 1"
            "vaddps xmm0, xmm0, xmm1"
            "vhaddps xmm0, xmm0, xmm0"
            "vhaddps xmm0, xmm0, xmm0"
        }
        return r
    }
    for i in 0..64 {
        r += a[i]
    }
    return r
}

let xs: [8]f32 = [1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0]
let ys: [8]f32 = [8.0, 7.0, 6.0, 5.0, 4.0, 3.0, 2.0, 1.0]
print(dot8(xs, ys))

let ds: [16]f64 = [0.5, 1.5, 2.5, 3.5, 4.5, 5.5, 6.5, 7.5, 8.5, 9.5, 10.5, 11.5, 12.5, 13.5, 14.5, 15.5]
print(sum16(ds))

let f = fused(3.0, 4.0, 0.5)
print(f)
print(f == 3.0 * 4.0 + 0.5)

let ia: [8]i32 = [1, 2, 3, 4, 5, 6, 7, 8]
let ib: [8]i32 = [10, 20, 30, 40, 50, 60, 70, 80]
print(add8(ia, ib))

let text: [32]u8 = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
                    16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31]
let mask: [32]u8 = [15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0,
                    15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0]
print(reverse32(text, mask))

let many: [64]f32
for i in 0..64 {
    many[i] = f32(i + 1)
}
print(sum64(many))
