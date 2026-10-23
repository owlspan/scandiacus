// The db library against the system sqlite, on Linux and macOS. On
// Windows the same library loads sqlite3.dll from a package, so this
// program is skipped there and runs on the Unix targets only.
let none: []str = []
let conn = must(db.open(":memory:"))
must(db.exec(conn, "create table notes (id integer primary key, body text)", none))
must(db.exec(conn, "insert into notes (body) values (?)", ["hello"]))
must(db.exec(conn, "insert into notes (body) values (?)", ["world"]))
print("changes: {db.changes(conn)}")
print("lastId: {db.lastId(conn)}")
for row in must(db.query(conn, "select id, body from notes where body like ?", ["%o%"])) {
    print("{row[0]}: {row[1]}")
}
let empty = must(db.query(conn, "select body from notes where body = ?", ["nope"]))
print("rows: {len(empty)}")
db.close(conn)
