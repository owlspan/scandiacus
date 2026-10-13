// toFloat and isFloat take hexadecimal floats, as Go does: a 0x
// mantissa needs a p exponent.
let cases = ["0x1p-2", "0x1.8p3", "-0X1P+4", "0xA_Bp0", "0x1.p1", "0x.8p1", "0x1", "0x1p", "0xp1", "0x1.8", "1e3", "0x1p-1074", "0x1p1024"]
for c in cases {
    print("{c} {isFloat(c)} {toFloat(c, -1.0)}")
}
