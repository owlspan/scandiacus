// fixed(x, places) and format specs in interpolation: {x:.2} for
// places, {x:8} and {x:<8} for a width, {n:05} to pad with zeros.
// Exact ties round half to even on every system.
let pi = 3.14159265
print("{pi:.2}|{pi:.0}|{pi:8.3}|{pi:<8.1}|")
print(fixed(2.675, 2))
print(fixed(1.005, 2))
print(fixed(-0.5, 0))
print(fixed(1e20, 1))
print(fixed(7, 3))
let n = 42
print("[{n:5}] [{n:<5}] [{n:05}] [{-7:04}]")
let name = "ada"
print("[{name:6}] [{name:<6}]")
let m = {"a": 1}
print("{m["a"]} {len(m)}")
print("{fixed(0.1 + 0.2, 17)}")
print(fixed(1.0 / 0.0, 2))
let items = ["tea", "coffee"]
let prices = [1.5, 12.25]
for i in 0..2 {
    print("{items[i]:<8}{prices[i]:7.2}")
}
print("{fixed(0.125, 2)} {fixed(0.375, 2)} {fixed(2.5, 0)} {fixed(3.5, 0)} {fixed(9.995, 2)} {fixed(99.5, 0)} {fixed(-0.0001, 2)}")
print(fixed(0.5, 0))
print(fixed(1.5, 0))
print(fixed(5e-324, 3))
