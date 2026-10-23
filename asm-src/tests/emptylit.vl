// An empty [] or {} takes its type from where it goes, not only from a
// let annotation.
struct Bag {
    tags: []str
    counts: {str: int}
}

fn none() -> []int {
    return []
}

fn size(xs: []float) -> int {
    return len(xs)
}

let xs: []int = [1, 2]
xs = []
push(xs, 3)
print(xs)

let ys: []str = json.decodeOr("not json", [])
print(len(ys))
let zs: []int = json.decodeOr("[4, 5]", [])
print(zs)

let b = Bag{tags: [], counts: {}}
push(b.tags, "a")
b.counts["k"] = 1
print(b.tags)
print(b.counts)

let groups: {str: []int} = {}
groups["odd"] = []
push(groups["odd"], 1)
print(groups)

let grid: [][]int = []
push(grid, [])
insert(grid, 0, [])
print(grid)

let m: {str: int} = {"a": 1}
m = {}
print(len(m))

print(none())
print(size([]))
