// A web server.
//
// Not in the parent directory with the other examples on purpose: the
// differential test runs everything in examples/*.scd through both
// backends and compares the output, and a program that listens forever
// has none.
//
// Run it, then open http://localhost:8080 in a browser.

fn page(title: str, body: str) -> str {
    let head = "<!doctype html><meta charset=utf-8><title>{title}</title>"
    let open = "<body style='font-family:system-ui;max-width:40em;margin:3em auto'>"
    return head + open + body + "</body>"
}

// A top-level `let` belongs to the program body rather than being a
// global, so a named function cannot see these. The handler can: a
// closure captures by reference, which is also what makes `hits += 1`
// visible out here afterwards.
let hits = 0
let notes: []str = []

print("serving on http://localhost:8080")
print("ctrl-c to stop")

must(http.serve(8080, fn(req: Request) -> Response {
    hits += 1
    print("{req.method} {req.path}")

    if req.path == "/api/notes" {
        return http.json(json.encode(notes))
    }

    if req.path == "/api/stats" {
        let stats = {"hits": hits, "notes": len(notes)}
        return http.json(json.encode(stats))
    }

    // A form post arrives as text=whatever in the body.
    if req.path == "/note" && req.method == "POST" {
        let text = req.body
        if startsWith(text, "text=") {
            text = substr(text, 5, len(text))
        }
        text = trim(replace(text, "+", " "))
        if len(text) > 0 {
            push(notes, text)
        }
    }

    if req.path != "/" && req.path != "/note" {
        return http.notFound()
    }

    let list = ""
    for n in notes {
        list = list + "<li>" + n + "</li>"
    }
    if len(notes) == 0 {
        list = "<li><i>nothing yet</i></li>"
    }

    let form = "<form method=post action=/note><input name=text style='padding:.4em'><button style='padding:.4em'>add</button></form>"
    let body = "<h1>Scandiacus</h1>"
    body = body + "<p>Served by a compiler that writes its own executables.</p>"
    body = body + "<p>{hits} requests so far.</p>"
    body = body + "<h2>Notes</h2><ul>" + list + "</ul>"
    body = body + form
    return http.ok(page("scandiacus", body))
}))
