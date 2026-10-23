// time.format and time.parse before 1970 and after 3000. The C runtime
// on Windows covers neither, and format used to crash on any date before
// 1970; such a time is moved by whole 400-year cycles, over which the
// calendar repeats exactly. Mid-year times, so no time zone moves them
// to another day.
let f = "YYYY-MM-DD HH:mm:ss"
for t in [-2208945600, -1000000000, 951825600, 2147483648, 32503723200, 253386446400] {
    let back = time.parse(time.format(t, f), f)
    print("{time.format(t, "YYYY")} {back == t}")
}
// Either side of second 0, the round trip only: which year they fall in
// depends on the zone.
for t in [-86400, -1, 0, 1] {
    print(time.parse(time.format(t, f), f) == t)
}
print(time.format(-5000000000, "YYYY MMMM"))
print(time.format(17000000000, "YYYY MMMM"))
print(time.parse("1066-10-14 12:00:00", f) < 0)
print(time.format(time.parse("1066-10-14 12:00:00", f), "YYYY-MM-DD DDDD"))
