let s = 0
for i in 0..400000000 {
    s += (i * i) % 7 + (i >> 3)
}
print(s)
