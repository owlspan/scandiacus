// A file that touches everything the grammar highlights, so a change
// to it can be eyeballed in one screen.

import "helpers.scd"

pub const LIMIT = 100
const RATIO = 1.5

pub struct Server {
    host: str
    port: int
    tags: []str
    timeout: ?int
}

impl Server {
    fn address(self) -> str {
        return "{self.host}:{self.port}"
    }

    fn isSecure(self) -> bool {
        return self.port == 443 || self.port == 8443
    }
}

fn parsePort(text: str) -> int! {
    if !isInt(text) {
        return fail("{text} is not a number")
    }
    let n = toInt(text)
    if n < 1 || n > 65535 {
        return fail("port {n} out of range")
    }
    return n
}

fn classify(code: int) -> str {
    match code {
        200 => return "ok"
        301, 302 => return "redirect"
        else => return "other"
    }
}

let s = Server{host: "example.com", port: 443, tags: ["web", "tls"]}
print("address {s.address()} secure {s.isSecure()}")

// bitwise, ranges, and the reserved words the compiler refuses
let flags = 0
flags |= 1 << 3
flags &= ~1
// Parens needed: comparison binds tighter than & in C's ladder, and
// Scandiacus keeps C's ladder.
print("flags {flags} masked {(flags & 0xff) == flags}")

for i in 0..=10 step 2 {
    if i % 4 == 0 {
        continue
    }
    write("{i} ")
}
print("")

let ages = {"ada": 36, "alan": 41}
for name, age in ages {
    print("  {padRight(name, 6)} {age}")
}

let port = valueOr(parsePort("8080"), 80)
print("port {port} class {classify(200)} pi {PI} limit {LIMIT} ratio {RATIO}")

let text = must(os.file.read("notes.txt"))
let page = valueOr(http.get("https://example.com"), "")
print("read {len(text)} bytes, fetched {len(page)}, at {time.stamp()}")
print("mem {mem.used()} local {net.localIP()} json {json.encode(ages)}")
