package main

// The math prelude, in Scandiacus.
//
// Every function here is Go's own algorithm from its math package,
// transcribed. That is the point: see the header of prelude.go for why
// calling msvcrt instead is not an option, and note that the coefficient
// blocks are copied rather than derived - they are the published minimax
// constants for each approximation, and a digit changed anywhere in them
// is a wrong answer that still looks plausible.
//
// The special cases Go handles with Inf and NaN are handled here as far
// as they can be. Neither value has a spelling on this backend - see
// ConstType in library.go - so where Go returns one, this returns the
// arithmetic that produces it (0.0 / 0.0, 1.0 / 0.0) rather than
// pretending the case does not arise. A program that prints the result
// disagrees with the Go backend, which is the documented INF and NAN
// gap and not a new one.

const preludeBits = `
// The IEEE 754 layout, as Go's math package names it: 52 bits of
// fraction, an 11-bit exponent biased by 1023.

fn __vy_isnan(x: float) -> bool {
    return !(x == x)
}

// Frexp splits x into a fraction in [0.5, 1) and a power of two.
// Returned as a pair through two functions rather than one, because
// Scandiacus has no multiple return and a struct here would allocate.

fn __vy_normmul(x: float) -> float {
    // A subnormal has leading zeros in its fraction, so its exponent
    // field lies. Scaling it up by 2**52 makes it normal, and the
    // exponent is corrected by the matching 52 in __vy_frexp_exp.
    if __vy_fabs(x) < 2.2250738585072014e-308 {
        return x * 4503599627370496.0
    }
    return x
}

fn __vy_frexp_frac(f: float) -> float {
    if f == 0.0 { return f }
    if __vy_isnan(f) { return f }
    let x = __bits(__vy_normmul(f))
    x = x & ~(2047 << 52)
    x = x | (1022 << 52)
    return __frombits(x)
}

fn __vy_frexp_exp(f: float) -> int {
    if f == 0.0 { return 0 }
    if __vy_isnan(f) { return 0 }
    let extra = 0
    if __vy_fabs(f) < 2.2250738585072014e-308 { extra = -52 }
    let x = __bits(__vy_normmul(f))
    return extra + ((x >> 52) & 2047) - 1023 + 1
}

fn __vy_ldexp(frac: float, e: int) -> float {
    if frac == 0.0 { return frac }
    if __vy_isnan(frac) { return frac }
    let exp = e
    if __vy_fabs(frac) < 2.2250738585072014e-308 { exp = exp - 52 }
    let f = __vy_normmul(frac)
    let x = __bits(f)
    exp = exp + ((x >> 52) & 2047) - 1023
    if exp < -1075 {
        return 0.0
    }
    if exp > 1023 {
        // Overflow. Go returns an infinity here and this produces one
        // the only way the language can.
        if frac < 0.0 { return -1.0 / 0.0 }
        return 1.0 / 0.0
    }
    let m = 1.0
    if exp < -1022 {
        exp = exp + 53
        m = 1.1102230246251565e-16
    }
    x = x & ~(2047 << 52)
    x = x | ((exp + 1023) << 52)
    return m * __frombits(x)
}

fn __vy_fabs(x: float) -> float {
    if x < 0.0 { return -x }
    return x
}
`

const preludeLog = `
// Go's math.Log. Reduce to a fraction near one with Frexp, then a
// seven-term odd polynomial in s = f/(2+f), which is the standard
// atanh-series form: it converges fastest where the reduction leaves us.

fn __vy_log(x: float) -> float {
    if __vy_isnan(x) { return x }
    if x < 0.0 { return 0.0 / 0.0 }
    if x == 0.0 { return -1.0 / 0.0 }

    let f1 = __vy_frexp_frac(x)
    let ki = __vy_frexp_exp(x)
    // Sqrt2/2, the point where multiplying by two makes the fraction
    // closer to one than leaving it alone does.
    if f1 < 0.7071067811865476 {
        f1 = f1 * 2.0
        ki = ki - 1
    }
    let f = f1 - 1.0
    let k = float(ki)

    let s = f / (2.0 + f)
    let s2 = s * s
    let s4 = s2 * s2
    let t1 = s2 * (0.6666666666666735130 + s4 * (0.2857142874366239149 + s4 * (0.1818357216161805012 + s4 * 0.1479819860511658591)))
    let t2 = s4 * (0.3999999999940941908 + s4 * (0.2222219843214978396 + s4 * 0.1531383769920937332))
    let R = t1 + t2
    let hfsq = 0.5 * f * f
    // Ln2 split into a high and a low half so that k*Ln2 carries more
    // than 53 bits of the product before it is added in.
    return k * 6.93147180369123816490e-01 - ((hfsq - (s * (hfsq + R) + k * 1.90821492927058770002e-10)) - f)
}

fn __vy_log10(x: float) -> float {
    return __vy_log(x) * 0.4342944819032518
}

fn __vy_log2(x: float) -> float {
    let frac = __vy_frexp_frac(x)
    let exp = __vy_frexp_exp(x)
    // An exact power of two has a fraction of exactly one half, and
    // its log2 is a whole number that no polynomial should be allowed
    // to round.
    if frac == 0.5 {
        return float(exp - 1)
    }
    return __vy_log(frac) * 1.4426950408889634 + float(exp)
}
`

const preludeExp = `
// Go's math.Exp. Take out the nearest multiple of ln2, leaving a small
// remainder, and use a degree-five odd rational on that.

fn __vy_exp(x: float) -> float {
    if __vy_isnan(x) { return x }
    if x > 7.09782712893383973096e+02 { return 1.0 / 0.0 }
    if x < -7.45133219101941108420e+02 { return 0.0 }
    // Below 2**-28 the polynomial cannot improve on 1+x, and the
    // reduction below would be pure rounding error.
    if -3.725290298461914e-09 < x && x < 3.725290298461914e-09 {
        return 1.0 + x
    }

    let k = 0
    if x < 0.0 { k = int(1.44269504088896338700e+00 * x - 0.5) }
    if x > 0.0 { k = int(1.44269504088896338700e+00 * x + 0.5) }
    let hi = x - float(k) * 6.93147180369123816490e-01
    let lo = float(k) * 1.90821492927058770002e-10
    return __vy_expmulti(hi, lo, k)
}

fn __vy_expmulti(hi: float, lo: float, k: int) -> float {
    let r = hi - lo
    let t = r * r
    let c = r - t * (1.66666666666666657415e-01 + t * (-2.77777777770155933842e-03 + t * (6.61375632143793436117e-05 + t * (-1.65339022054652515390e-06 + t * 4.13813679705723846039e-08))))
    let y = 1.0 - ((lo - (r * c) / (2.0 - c)) - hi)
    return __vy_ldexp(y, k)
}
`

const preludeAtan = `
// Go's math.Atan, and Asin and Acos built on it exactly as Go builds
// them. xatan is the rational approximation on [0, 0.66]; satan folds
// the rest of the line onto that interval with the arctangent addition
// formula.

fn __vy_xatan(x: float) -> float {
    let z = x * x
    let num = ((-8.750608600031904122785e-01 * z + -1.615753718733365076637e+01) * z + -7.500855792314704667340e+01) * z + -1.228866684490136173410e+02
    num = num * z + -6.485021904942025371773e+01
    let den = ((((z + 2.485846490142306297962e+01) * z + 1.650270098316988542046e+02) * z + 4.328810604912902668951e+02) * z + 4.853903996359136964868e+02) * z + 1.945506571482613964425e+02
    z = num * z / den
    return x * z + x
}

fn __vy_satan(x: float) -> float {
    // Morebits is the extra bits of Pi/2 that a double cannot hold, kept
    // separate so the subtraction below does not lose them. Tan3pio8 is
    // tan(3*pi/8), the second breakpoint.
    if x <= 0.66 {
        return __vy_xatan(x)
    }
    if x > 2.41421356237309504880 {
        return 1.5707963267948966 - __vy_xatan(1.0 / x) + 6.123233995736765886130e-17
    }
    return 0.7853981633974483 + __vy_xatan((x - 1.0) / (x + 1.0)) + 0.5 * 6.123233995736765886130e-17
}

fn __vy_atan(x: float) -> float {
    if x == 0.0 { return x }
    if x > 0.0 { return __vy_satan(x) }
    return -__vy_satan(-x)
}

fn __vy_atan2(y: float, x: float) -> float {
    if __vy_isnan(y) || __vy_isnan(x) { return 0.0 / 0.0 }
    if y == 0.0 {
        if x >= 0.0 && !__vy_signbit(x) { return __vy_copysign(0.0, y) }
        return __vy_copysign(3.141592653589793, y)
    }
    if x == 0.0 { return __vy_copysign(1.5707963267948966, y) }

    let q = __vy_atan(y / x)
    if x < 0.0 {
        if q <= 0.0 { return q + 3.141592653589793 }
        return q - 3.141592653589793
    }
    return q
}

fn __vy_signbit(x: float) -> bool {
    return __bits(x) < 0
}

fn __vy_copysign(x: float, sign: float) -> float {
    // The sign bit is bit 63 and nothing else moves, which is why this
    // is a bit operation rather than a comparison: it carries the sign
    // of a negative zero, and a comparison cannot see one.
    let signBit = 1 << 63
    return __frombits((__bits(x) & ~signBit) | (__bits(sign) & signBit))
}

fn __vy_asin(x: float) -> float {
    if x == 0.0 { return x }
    let sign = false
    let v = x
    if v < 0.0 {
        v = -v
        sign = true
    }
    if v > 1.0 { return 0.0 / 0.0 }

    let temp = __vy_sqrt(1.0 - v * v)
    if v > 0.7 {
        temp = 1.5707963267948966 - __vy_satan(temp / v)
    } else {
        temp = __vy_satan(v / temp)
    }
    if sign { temp = -temp }
    return temp
}

fn __vy_acos(x: float) -> float {
    return 1.5707963267948966 - __vy_asin(x)
}
`

const preludeTrig = `
// Go's math.Sin, Cos and Tan.
//
// All three reduce the argument modulo pi/4 and then evaluate a
// polynomial on the remainder, choosing which polynomial and which sign
// from how many eighths of a turn were taken out. PI4A, PI4B and PI4C
// are pi/4 split into three parts whose sum carries far more than 53
// bits, so that x - y*pi/4 keeps its precision for a large y.
//
// Above 2**29 that three-part split is no longer enough and Go switches
// to a reduction against a thousand-bit constant. That is not here: an
// argument that large is a compile-time constant nowhere and a runtime
// value rarely, and getting it wrong quietly is worse than the gap. See
// __vy_trigbig.

fn __vy_trigred(x: float) -> float {
    let j = int(x * 1.2732395447351628)
    if (j & 1) == 1 { j = j + 1 }
    let y = float(j)
    return ((x - y * 7.85398125648498535156e-01) - y * 3.77489470793079817668e-08) - y * 2.69515142907905952645e-15
}

fn __vy_trigoct(x: float) -> int {
    let j = int(x * 1.2732395447351628)
    if (j & 1) == 1 { j = j + 1 }
    return j & 7
}

fn __vy_sin(x: float) -> float {
    if x == 0.0 { return x }
    if __vy_isnan(x) { return x }

    let sign = false
    let v = x
    if v < 0.0 {
        v = -v
        sign = true
    }
    if v >= 536870912.0 { return __vy_trigbig(v) }

    let j = __vy_trigoct(v)
    if j > 3 {
        sign = !sign
        j = j - 4
    }
    let z = __vy_trigred(v)
    let zz = z * z

    let y = 0.0
    if j == 1 || j == 2 {
        y = 1.0 - 0.5 * zz + zz * zz * ((((((-1.13585365213876817300e-11 * zz) + 2.08757008419747316778e-09) * zz - 2.75573141792967388112e-07) * zz + 2.48015872888517045348e-05) * zz - 1.38888888888730564116e-03) * zz + 4.16666666666665929218e-02)
    } else {
        y = z + z * zz * ((((((1.58962301576546568060e-10 * zz) - 2.50507477628578072866e-08) * zz + 2.75573136213857245213e-06) * zz - 1.98412698295895385996e-04) * zz + 8.33333333332211858878e-03) * zz - 1.66666666666666307295e-01)
    }
    if sign { y = -y }
    return y
}

fn __vy_cos(x: float) -> float {
    if __vy_isnan(x) { return x }

    let sign = false
    let v = __vy_fabs(x)
    if v >= 536870912.0 { return __vy_trigbig(v) }

    let j = __vy_trigoct(v)
    if j > 3 {
        j = j - 4
        sign = !sign
    }
    if j > 1 { sign = !sign }

    let z = __vy_trigred(v)
    let zz = z * z

    let y = 0.0
    if j == 1 || j == 2 {
        y = z + z * zz * ((((((1.58962301576546568060e-10 * zz) - 2.50507477628578072866e-08) * zz + 2.75573136213857245213e-06) * zz - 1.98412698295895385996e-04) * zz + 8.33333333332211858878e-03) * zz - 1.66666666666666307295e-01)
    } else {
        y = 1.0 - 0.5 * zz + zz * zz * ((((((-1.13585365213876817300e-11 * zz) + 2.08757008419747316778e-09) * zz - 2.75573141792967388112e-07) * zz + 2.48015872888517045348e-05) * zz - 1.38888888888730564116e-03) * zz + 4.16666666666665929218e-02)
    }
    if sign { y = -y }
    return y
}

fn __vy_tan(x: float) -> float {
    if x == 0.0 { return x }
    if __vy_isnan(x) { return x }

    let sign = false
    let v = x
    if v < 0.0 {
        v = -v
        sign = true
    }
    if v >= 536870912.0 { return __vy_trigbig(v) }

    let j = __vy_trigoct(v)
    let z = __vy_trigred(v)
    let zz = z * z

    let y = z
    if zz > 1e-14 {
        y = z + z * (zz * (((-1.30936939181383777646e+04 * zz) + 1.15351664838587416140e+06) * zz + -1.79565251976484877988e+07) / ((((zz + 1.36812963470692954678e+04) * zz - 1.32089234440210967447e+06) * zz + 2.50083801823357915839e+07) * zz - 5.38695755929454629881e+07))
    }
    if (j & 2) == 2 { y = -1.0 / y }
    if sign { y = -y }
    return y
}

// An argument at or above 2**29 needs Go's Payne-Hanek reduction against
// a thousand-bit 4/pi, which is not implemented here. Rather than return
// a number that is confidently wrong, this stops with a message naming
// the gap - the same rule the rest of this backend follows.
fn __vy_trigbig(x: float) -> float {
    return __abort("sin, cos and tan of an argument at or above 2**29 are not on the assembly backend yet")
}
`

const preludePow = `
// Go's math.Pow. The integer part of the exponent is done by repeated
// squaring, tracking the exponent separately so the intermediate never
// overflows, and the fractional part goes through Exp and Log.

fn __vy_pow(x: float, y: float) -> float {
    if y == 0.0 { return 1.0 }
    if y == 1.0 { return x }
    if __vy_isnan(x) || __vy_isnan(y) { return 0.0 / 0.0 }
    if x == 0.0 {
        if y < 0.0 {
            if __vy_oddint(y) { return __vy_copysign(1.0 / 0.0, x) }
            return 1.0 / 0.0
        }
        if __vy_oddint(y) { return x }
        return 0.0
    }
    if x == 1.0 { return 1.0 }

    let yi = __vy_trunc(__vy_fabs(y))
    let yf = __vy_fabs(y) - yi

    let a1 = 1.0
    let ae = 0
    if yf != 0.0 {
        if yf > 0.5 {
            yf = yf - 1.0
            yi = yi + 1.0
        }
        a1 = __vy_exp(yf * __vy_log(x))
    }

    let x1 = __vy_frexp_frac(x)
    let xe = __vy_frexp_exp(x)

    let i = int(yi)
    while i != 0 {
        if xe < -4096 || 4096 < xe {
            // The exponent has run away from what a double can hold, so
            // the answer is an overflow or an underflow and the loop can
            // stop pretending otherwise.
            ae = ae + xe
            i = 0
        } else {
            if (i & 1) == 1 {
                a1 = a1 * x1
                ae = ae + xe
            }
            x1 = x1 * x1
            xe = xe * 2
            if x1 < 0.5 {
                x1 = x1 + x1
                xe = xe - 1
            }
            i = i >> 1
        }
    }

    if y < 0.0 {
        a1 = 1.0 / a1
        ae = -ae
    }
    return __vy_ldexp(a1, ae)
}

fn __vy_oddint(x: float) -> bool {
    if __vy_fabs(x) >= 9007199254740992.0 {
        // Above 2**53 every float is even, and int() would overflow.
        return false
    }
    let t = __vy_trunc(x)
    if t != x { return false }
    return (int(t) & 1) == 1
}
`

const preludeRoot = `
// Sqrt is the hardware instruction, which IEEE 754 specifies exactly, so
// every implementation agrees on it and there is nothing to transcribe.
// It is wrapped only so the prelude can call it like anything else.

fn __vy_sqrt(x: float) -> float {
    return sqrt(x)
}

fn __vy_hypot(p: float, q: float) -> float {
    let a = __vy_fabs(p)
    let b = __vy_fabs(q)
    if __vy_isnan(a) || __vy_isnan(b) { return 0.0 / 0.0 }
    if a < b {
        let t = a
        a = b
        b = t
    }
    if a == 0.0 { return 0.0 }
    b = b / a
    return a * __vy_sqrt(1.0 + b * b)
}

// Go's math.Cbrt, which is Kahan's: a five-bit estimate straight out of
// the exponent field, a rational step to twenty-three bits, and one
// Newton step to fifty-three.
//
// Dividing the bit pattern by three is the estimate. It works because
// the exponent occupies the high bits, so dividing the whole word
// divides the exponent; B1 is the bias correction that keeps the
// mantissa bits from poisoning it. That is also why the chop before the
// last step matters: it forces t slightly above the true root, which is
// what makes the final error one-sided and under one ulp.
fn __vy_cbrt(x: float) -> float {
    if x == 0.0 { return x }
    if __vy_isnan(x) { return x }

    let sign = false
    let v = x
    if v < 0.0 {
        v = -v
        sign = true
    }

    // B1 = (682 - 0.03306235651) * 2**20, shifted into the exponent.
    let t = __frombits(__bits(v) / 3 + (715094163 << 32))
    if v < 2.22507385850720138309e-308 {
        // A subnormal has no usable exponent, so it is scaled into the
        // normal range first and B2 is the bias for that shift.
        t = 18014398509481984.0 * v
        t = __frombits(__bits(t) / 3 + (696219795 << 32))
    }

    let r = t * t / v
    let s = 5.42857142857142815906e-01 + r * t
    t = t * (3.57142857142857150787e-01 + 1.60714285714285720630e+00 / (s + 1.41428571428571436819e+00 + -7.05306122448979611050e-01 / s))

    // Chop to 22 bits and nudge up. 0xFFFFFFFFC0000000 as a signed
    // word is -1073741824, and adding 1<<30 is the nudge.
    t = __frombits((__bits(t) & -1073741824) + 1073741824)

    s = t * t
    r = v / s
    let w = t + t
    r = (r - t) / (w + r)
    t = t + t * r

    if sign { t = -t }
    return t
}
`

const preludeRound = `
// floor, ceil, round and trunc.
//
// These were msvcrt calls until the PE writer arrived, and two of them
// stopped working the moment it did: msvcrt.dll has floor and ceil but
// not round or trunc, which are C99, and MinGW had been quietly
// supplying its own. They are here now, which is better anyway - all
// four are exactly specified operations with no approximation in them,
// so a bit-level implementation is not an approximation of libm's, it
// is the same answer arrived at without the call.

fn __vy_trunc(x: float) -> float {
    let bits = __bits(x)
    let e = ((bits >> 52) & 2047) - 1023
    if e < 0 {
        // Magnitude below one truncates to a zero of the same sign.
        return __frombits(bits & (1 << 63))
    }
    if e < 52 {
        return __frombits(bits & ~(4503599627370495 >> e))
    }
    // No fraction bits left to drop: already whole, or infinite, or NaN.
    return x
}

fn __vy_floor(x: float) -> float {
    let t = __vy_trunc(x)
    if x < 0.0 && t != x {
        return t - 1.0
    }
    return t
}

fn __vy_ceil(x: float) -> float {
    let t = __vy_trunc(x)
    if x > 0.0 && t != x {
        return t + 1.0
    }
    return t
}

// Go's math.Round: add a half at the right binary place and mask the
// fraction off, which rounds half away from zero without a comparison
// and without ever leaving the exponent it started in.
fn __vy_round(x: float) -> float {
    let bits = __bits(x)
    let e = (bits >> 52) & 2047
    if e < 1023 {
        // Below one: the answer is a signed zero, or a signed one when
        // the magnitude is at least a half.
        bits = bits & (1 << 63)
        if e == 1022 {
            bits = bits | 4607182418800017408
        }
    } else {
        if e < 1075 {
            let f = e - 1023
            bits = bits + (2251799813685248 >> f)
            bits = bits & ~(4503599627370495 >> f)
        }
    }
    return __frombits(bits)
}
`
