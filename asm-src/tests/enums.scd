// Enums: named variants, compared, matched and printed by name.

enum State { Idle, Running, Done }

enum Dir {
    North
    East
    South
    West
}

var state = State.Idle

fn advance(s: State) -> State {
    match s {
        State.Idle => return State.Running
        State.Running => return State.Done
        State.Done => return State.Idle
    }
    return s
}

fn turn(d: Dir) -> Dir {
    match d {
        Dir.North => return Dir.East
        Dir.East => return Dir.South
        Dir.South => return Dir.West
        else => return Dir.North
    }
    return d
}

print(state)
state = advance(state)
print(state)
print(advance(advance(state)))
print(state == State.Running)
print(state != State.Done)
print("now {state}")
print(str(Dir.West) + "!")
let path = [Dir.North, turn(Dir.North), turn(turn(Dir.North))]
print(path)
struct Robot { name: str, facing: Dir }
let r = Robot{name: "r2", facing: Dir.South}
print(r)
let states: []State = []
push(states, State.Done)
print(states)
write(Dir.East)
print("")
