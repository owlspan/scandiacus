// Decoding a document with many strings. Each string's buffer is as
// long as the string; it used to be as long as the whole document,
// which made decoding quadratic (20000 records ran out of memory).
// Kept small: an eager-GC build collects on every allocation.
struct Item {
    id: int
    name: str
    tags: []str
}

let items: []Item = []
for i in 0..1000 {
    push(items, Item{id: i, name: "thing \"{i}\"", tags: ["a", "b{i % 7}\n"]})
}
let text = json.encode(items)
let back: []Item = must(json.decode(text))
let total = 0
for it in back {
    total += it.id + len(it.name) + len(it.tags[1])
}
print(len(back))
print(back[999].name)
print(total)
