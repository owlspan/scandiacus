let name = "Scandiacus"
let greeting = "hello, " + name
print(greeting)
print(len(greeting))
print(name == "Scandiacus")
print(name == "scandiacus")
print(name != "other")

let n = 42
print("the answer is {n}")
print("{n} squared is {n * n}")
print("nested: {"inner"} done")

fn describe(x: int) -> str {
    if x < 0 { return "negative" }
    if x == 0 { return "zero" }
    return "positive"
}
print(describe(-5))
print(describe(0))
print(describe(7))

let acc = ""
for i in 1..=5 {
    acc += "{i},"
}
print(acc)
print(str(123) + "!")
print("bool: {true}")
