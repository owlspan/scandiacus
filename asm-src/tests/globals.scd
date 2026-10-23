// Top-level var: a mutable global every function can see and change.

var score = 0
var names: []str = []
var best = 1.5
const LIMIT = 3

fn award(points: int) {
    score += points
    push(names, "p" + str(points))
}

fn reset() {
    score = 0
    best = 2
}

award(5)
award(10)
print(score)
print(names)
reset()
print(score)
print(best)

let score2 = score + LIMIT
print(score2)

// A local shadows the global inside its own scope only.
fn shadow() -> int {
    let score = 99
    return score
}
print(shadow())
print(score)

struct Player { name: str, hp: int }
var hero = Player{name: "ada", hp: 10}
fn hurt() {
    hero.hp -= 3
}
hurt()
hurt()
print(hero)
var var = 7
print(var)
