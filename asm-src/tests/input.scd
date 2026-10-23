// input reads a line and drops its ending, whether that was \n or
// \r\n. At the end of input it returns "".
let name = input("name? ")
print("hello, {name}")
let total = 0
let line = input()
while line != "" {
    total += toInt(line, 0)
    line = input()
}
print("total {total}")
pause()
print("[" + input() + "]")
