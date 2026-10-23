const LIMIT = 3

fn name(n: int) -> str {
    match n {
        1 => return "one"
        2 => return "two"
        3, 4 => return "a few"
        else => return "many"
    }
}

for i in 1..=6 { print(name(i)) }
print(LIMIT)

let word = "yes"
match word {
    "yes" => print(1)
    "no" => print(0)
    else => print(-1)
}

let k = 9
match k {
    1 => print(100)
    else => print(200)
}
