// Containers inside containers.
//
// A vty used to carry the element as a bare kind, which could say "list
// of int" but not "list of list of int". It carries a whole type now,
// so the nesting goes as deep as the program wants.

let grid = [[1, 2, 3], [4, 5], [6]]
print(str(grid))
print(len(grid))
print(len(grid[1]))
print(grid[0][2])

// Writing through two levels reaches the element, not a copy.
grid[1][0] = 40
push(grid, [7, 8])
push(grid[2], 60)
print(str(grid))

// A map whose values are lists: what json.decode needs.
let byLetter: {str: []int} = {}
push(byLetter["a"], 1)
push(byLetter["a"], 2)
push(byLetter["b"], 3)
print(str(byLetter))
print(len(byLetter["a"]))

// Reading a key that was never set gives an empty list, not a fault.
print(len(byLetter["zz"]))
print(str(byLetter["zz"]))

// A map of maps.
let nest = {"outer": {"inner": 5}}
print(nest["outer"]["inner"])
print(str(nest))

// A list of maps.
let rows = [{"n": 1}, {"n": 2}]
print(str(rows))
print(rows[1]["n"])

// A struct holding a list, and a list holding those structs.
struct Row {
    cells: []str
    n: int
}

let r = Row{cells: ["a"], n: 1}
push(r.cells, "b")
print(str(r))

let table = [Row{cells: ["x"], n: 0}, Row{cells: [], n: 1}]
push(table[1].cells, "y")
table[0].cells[0] = "z"
print(str(table))

// Iterating a list of lists.
let total = 0
for row in grid {
    for v in row {
        total += v
    }
}
print(total)
