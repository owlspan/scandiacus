// JSON, both directions.
//
// Encoding is generated from the type, the same way str() is. Decoding
// is two halves that do not know about each other: a parser that walks
// the characters at runtime, because what is in the text is only known
// once it has been read, and a conversion generated from the annotation.

struct Address {
    city: str
    zip: str
}

struct Person {
    name: str
    age: int
    height: float
    active: bool
    tags: []str
    address: Address
}

let ada = Person{
    name: "ada",
    age: 36,
    height: 1.7,
    active: true,
    tags: ["maths", "computing"],
    address: Address{city: "London", zip: "NW1"}
}

// Field names as written in Scandiacus, in declaration order.
let text = json.encode(ada)
print(text)

// And it comes back.
let back: Person = must(json.decode(text))
print(back.name)
print(back.address.city)
print(str(back.tags))
print(json.encode(back) == text)

// The indented form is the same content over more lines.
print(len(lines(json.pretty(ada))))

// Containers encode too, and a map comes out sorted because that is how
// it is stored.
print(json.encode([1, 2, 3]))
print(json.encode({"b": 2, "a": 1}))
print(json.encode([Address{city: "Paris", zip: "75"}]))

// Escaping follows Go's, which escapes the HTML-unsafe three as well as
// what JSON requires.
print(json.encode("quote \" backslash \\ newline \n tag <b>&"))

// Decoding into a container needs the annotation, which is where the
// shape comes from.
let nums: []int = must(json.decode("[4, 5, 6]"))
print(str(nums))
print(sum(nums))

let scores: {str: int} = must(json.decode(json.encode({"x": 1, "y": 2})))
print(str(scores))

// A document that is not JSON fails rather than returning something
// wrong.
let broken: Person! = json.decode("this is not json")
print(isOk(broken))
print(startsWith(errorOf(broken), "cannot decode"))
print(json.valid(text))
print(json.valid("not json"))

// decodeOr takes its shape from the fallback, so it needs no
// annotation and cannot fail.
let safe: Person = json.decodeOr("broken", Person{name: "default"})
print(safe.name)

// Reading single values by path, with no struct declared at the far end.
print(json.get(text, "name"))
print(json.int(text, "age"))
print(json.num(text, "height"))
print(json.bool(text, "active"))
print(json.get(text, "address.city"))
print(json.get(text, "tags.0"))
print(json.count(text, "tags"))
print(str(json.keys(text)))

// A nested object comes back as JSON, normalised.
print(json.get(text, "address"))

// A path that goes nowhere is quiet.
print(json.get(text, "nope") == "")
print(json.int(text, "nope"))
print(json.has(text, "name"))
print(json.has(text, "nope"))

// The shape real APIs have: a struct holding a list of structs, and a
// path reaching into it.
struct Team {
    label: str
    members: []Person
}

let team = Team{label: "founders", members: [ada, Person{name: "alan", age: 41}]}
let teamText = json.encode(team)
let sameTeam: Team = must(json.decode(teamText))
print(sameTeam.label)
print(len(sameTeam.members))
print(sameTeam.members[1].name)
print(json.get(teamText, "members.1.name"))
print(json.get(teamText, "members.9.name") == "")

// Whitespace between tokens is skipped, so a hand-written document
// decodes the same as a generated one.
let spaced: {str: int} = must(json.decode(`  {  "a" : 1 ,  "b" : 2  }  `))
print(str(spaced))
