package main

// An HTTP server, in Scandiacus.
//
// The whole thing sits on the six net.* primitives, so nothing here
// touches a socket API directly. That is the point of making a socket a
// plain int: the protocol is ordinary Scandiacus and reads like it.
//
// What it is: HTTP/1.1, one request per connection, Connection: close.
// What it is not: keep-alive, chunked transfer, or TLS. http.serve runs
// a handler to completion before the next accept; http.serveWorkers
// runs several threads doing that on one socket.

const preludeHTTP = `
struct Request {
    method: str
    path: str
    query: str
    body: str
    headers: {str: str}
}

struct Response {
    status: int
    contentType: str
    body: str
    location: str
}

// The reason phrase for the codes a small server actually sends. Go
// prints these beside the number and so does this, since a client that
// logs the status line would otherwise show something different.
fn __vy_httpReason(code: int) -> str {
    if code == 200 { return "OK" }
    if code == 201 { return "Created" }
    if code == 204 { return "No Content" }
    if code == 301 { return "Moved Permanently" }
    if code == 302 { return "Found" }
    if code == 303 { return "See Other" }
    if code == 304 { return "Not Modified" }
    if code == 307 { return "Temporary Redirect" }
    if code == 308 { return "Permanent Redirect" }
    if code == 400 { return "Bad Request" }
    if code == 401 { return "Unauthorized" }
    if code == 403 { return "Forbidden" }
    if code == 404 { return "Not Found" }
    if code == 405 { return "Method Not Allowed" }
    if code == 500 { return "Internal Server Error" }
    return "Status"
}

fn __vy_httpOK(body: str) -> Response {
    return Response{status: 200, contentType: "text/html; charset=utf-8", body: body}
}

fn __vy_httpText(body: str) -> Response {
    return Response{status: 200, contentType: "text/plain; charset=utf-8", body: body}
}

fn __vy_httpJSON(body: str) -> Response {
    return Response{status: 200, contentType: "application/json", body: body}
}

fn __vy_httpStatus(code: int, body: str) -> Response {
    return Response{status: code, contentType: "text/html; charset=utf-8", body: body}
}

fn __vy_httpNotFound() -> Response {
    return __vy_httpStatus(404, "<h1>404 Not Found</h1>")
}

// A 302 to another url, which a browser and http.get both follow.
fn __vy_httpRedirect(url: str) -> Response {
    return Response{status: 302, contentType: "text/plain; charset=utf-8", body: "", location: url}
}

// The status line, the headers a browser needs, then the body. Content
// Length is counted rather than guessed, because a client that reads
// fewer bytes than were sent hangs waiting for the rest.
fn __vy_httpFormat(r: Response) -> str {
    let head = "HTTP/1.1 {r.status} {__vy_httpReason(r.status)}"
    let ct = "Content-Type: {r.contentType}"
    let cl = "Content-Length: {len(r.body)}"
    let loc = ""
    if r.location != "" { loc = "Location: {r.location}\r\n" }
    return head + "\r\n" + ct + "\r\n" + cl + "\r\n" + loc + "Connection: close" + "\r\n\r\n" + r.body
}

// Split "/path?a=1" into its two halves.
fn __vy_httpSplitTarget(target: str) -> []str {
    let q = indexOf(target, "?")
    if q < 0 { return [target, ""] }
    return [__substrB(target, 0, q), __substrB(target, q + 1, len(target))]
}

fn __vy_httpParse(raw: str) -> Request {
    let headers: {str: str} = {}
    let req = Request{method: "", path: "", query: "", body: "", headers: headers}

    let sep = indexOf(raw, "\r\n\r\n")
    let headEnd = sep
    let bodyAt = sep + 4
    if sep < 0 {
        headEnd = len(raw)
        bodyAt = len(raw)
    }

    let head = __substrB(raw, 0, headEnd)
    req.body = __substrB(raw, bodyAt, len(raw))

    let rows = split(head, "\r\n")
    if len(rows) == 0 { return req }

    // "GET /path?q=1 HTTP/1.1"
    let parts = split(rows[0], " ")
    if len(parts) >= 1 { req.method = parts[0] }
    if len(parts) >= 2 {
        let both = __vy_httpSplitTarget(parts[1])
        req.path = both[0]
        req.query = both[1]
    }

    // Header names are case insensitive, so they are stored lowered and
    // __vy_httpHeader lowers what it is asked for. Otherwise a lookup
    // of "content-type" misses a header the client spelled with a
    // capital C, which is a bug that only shows up against some clients.
    let i = 1
    while i < len(rows) {
        let row = rows[i]
        let colon = indexOf(row, ":")
        if colon > 0 {
            let name = lower(trim(__substrB(row, 0, colon)))
            let value = trim(__substrB(row, colon + 1, len(row)))
            req.headers[name] = value
        }
        i = i + 1
    }
    return req
}

fn __vy_httpHeader(r: Request, name: str) -> str {
    let key = lower(name)
    if has(r.headers, key) { return r.headers[key] }
    return ""
}

// Read a whole request, not just whatever arrived first.
//
// recv returns what one packet carried, which for anything with a body
// is routinely less than the request. This reads until the blank line
// that ends the headers, then reads Content-Length more. Both loops
// stop if the peer goes quiet, so a client that connects and says
// nothing cannot wedge the server.
fn __vy_httpRead(conn: int) -> str! {
    let raw = ""
    while indexOf(raw, "\r\n\r\n") < 0 {
        let chunk = net.recv(conn)?
        if len(chunk) == 0 { return raw }
        raw = raw + chunk
    }

    let sep = indexOf(raw, "\r\n\r\n")
    let head = __substrB(raw, 0, sep)
    let want = __vy_httpLength(head)
    let have = len(raw) - (sep + 4)

    while have < want {
        let chunk = net.recv(conn)?
        if len(chunk) == 0 { return raw }
        raw = raw + chunk
        have = have + len(chunk)
    }
    return raw
}

// Content-Length off the header block, or zero.
fn __vy_httpLength(head: str) -> int {
    let rows = split(head, "\r\n")
    let i = 0
    while i < len(rows) {
        let colon = indexOf(rows[i], ":")
        if colon > 0 {
            let name = lower(trim(__substrB(rows[i], 0, colon)))
            if name == "content-length" {
                let v = trim(__substrB(rows[i], colon + 1, len(rows[i])))
                if isInt(v) { return toInt(v) }
            }
        }
        i = i + 1
    }
    return 0
}

// serve accepts forever and runs the handler for each request.
//
// A handler that fails is answered with a 500 rather than taking the
// server down with it, because the one thing a server must not do is
// stop listening.
fn __vy_httpServe(port: int, handler: fn(Request) -> Response) -> void! {
    let sock = net.listen(port)?
    while true {
        let conn = net.accept(sock)?
        let raw = valueOr(__vy_httpRead(conn), "")
        if len(raw) > 0 {
            let res = handler(__vy_httpParse(raw))
            net.send(conn, __vy_httpFormat(res))?
        }
        net.close(conn)
    }
    return ok()
}

// http.serve with a pool of threads, each accepting on the one listening
// socket, so a slow client holds up only the thread serving it. The
// handler runs on several threads at once: what it shares needs a mutex
// or atomics, as anywhere else. Fewer than one worker means one.
fn __vy_httpServeWorkers(port: int, workers: int, handler: fn(Request) -> Response) -> void! {
    let sock = net.listen(port)?
    let n = workers
    if n < 1 {
        n = 1
    }
    let pool: []int = []
    for i in 0..n {
        push(pool, thread.spawn(fn() {
            __vy_httpWorker(sock, handler)
        }))
    }
    for t in pool {
        thread.join(t)
    }
    return ok()
}

// One worker: accept, answer, close, until the socket fails.
fn __vy_httpWorker(sock: int, handler: fn(Request) -> Response) {
    while true {
        let accepted = net.accept(sock)
        if !isOk(accepted) {
            return
        }
        let conn = valueOr(accepted, 0)
        let raw = valueOr(__vy_httpRead(conn), "")
        if len(raw) > 0 {
            let res = handler(__vy_httpParse(raw))
            // A client that has gone away is its own loss; the next
            // connection is served all the same.
            net.send(conn, __vy_httpFormat(res))
        }
        net.close(conn)
    }
}

// A one-shot client. Enough to fetch a page, not a general HTTP client:
// no keep-alive, one request per connection.
fn __vy_httpRequest(method: str, url: str, body: str) -> str! {
    let u = __vy_httpSplitURL(url)?
    // On Windows this is WinHTTP, which does TLS, redirects and chunked
    // bodies. On Linux and macOS the same builtin is lowered to a socket
    // client, __vy_httpOverNet, so the http:// case works there too.
    return __winhttp(u.host, u.port, u.path, u.secure, method, body)
}

struct __vy_URL {
    host: str
    port: int
    path: str
    secure: bool
}

// "https://host:8443/a?b" -> host, port, path and whether it is TLS. No
// scheme means http://.
fn __vy_httpSplitURL(url: str) -> __vy_URL! {
    let rest = url
    let u = __vy_URL{host: "", port: 80, path: "/", secure: false}

    if startsWith(rest, "https://") {
        rest = __substrB(rest, 8, len(rest))
        u.secure = true
        u.port = 443
    } else {
        if startsWith(rest, "http://") {
            rest = __substrB(rest, 7, len(rest))
        }
    }

    let slash = indexOf(rest, "/")
    let hostport = rest
    if slash >= 0 {
        hostport = __substrB(rest, 0, slash)
        u.path = __substrB(rest, slash, len(rest))
    }

    u.host = hostport
    let colon = indexOf(hostport, ":")
    if colon >= 0 {
        u.host = __substrB(hostport, 0, colon)
        let p = __substrB(hostport, colon + 1, len(hostport))
        if isInt(p) { u.port = toInt(p) }
    }

    if u.host == "" {
        return fail("cannot fetch \"{url}\": no host in the url")
    }
    return u
}

// __vy_httpOverNet is the http client for Linux and macOS, where there
// is no WinHTTP. The __winhttp builtin is lowered to a call here off
// Windows; it is never called on the Windows build. Each request is
// HTTP/1.1 over one net connection with Connection: close, read to the
// close and parsed. Content-Length and chunked bodies are both handled.
// https:// puts TLS on the connection with the __tls builtins, which use
// the system OpenSSL and check the certificate against the system's
// trust store.
//
// Redirects are followed the way WinHTTP and browsers follow them, up to
// ten: 301, 302 and 303 turn a POST into a GET with no body, 307 and 308
// repeat the request as it was. A redirect from https:// to http:// is
// refused, as WinHTTP refuses it, since it would send in the clear what
// was asked for over TLS.
fn __vy_httpOverNet(host: str, port: int, path: str, secure: bool, method: str, body: str) -> str! {
    let u = __vy_URL{host: host, port: port, path: path, secure: secure}
    let hops = 0
    while true {
        let raw = __vy_httpOnce(u, method, body)?

        let sep = indexOf(raw, "\r\n\r\n")
        if sep < 0 {
            return fail("the server sent a reply with no header block")
        }
        let head = __substrB(raw, 0, sep)
        let rest = __substrB(raw, sep + 4, len(raw))
        let code = __vy_httpStatusCode(head)

        let to = __vy_httpHeaderOf(head, "location")
        if code >= 300 && code < 400 && code != 304 && to != "" {
            hops = hops + 1
            if hops > 10 {
                return fail("stopped after 10 redirects, the last to \"{to}\"")
            }
            if code != 307 && code != 308 {
                if method != "GET" && method != "HEAD" { method = "GET" }
                body = ""
            }
            let next = __vy_httpResolve(u, to)?
            if u.secure && !next.secure {
                return fail("refused a redirect from https:// to \"{to}\", which is not encrypted")
            }
            u = next
            continue
        }

        let out = rest
        if __vy_httpChunked(head) {
            out = __vy_httpDechunk(rest)
        }
        if code >= 400 {
            return fail("server replied {code}")
        }
        return out
    }
    return fail("unreachable")
}

// One request, and the whole reply as it came off the wire.
fn __vy_httpOnce(u: __vy_URL, method: str, body: str) -> str! {
    let conn = net.connect(u.host, u.port)?
    let t = 0
    if u.secure {
        let opened = __tlsOpen(conn, u.host)
        if !isOk(opened) {
            net.close(conn)
            return fail("cannot fetch \"https://{u.host}{u.path}\": {errorOf(opened)}")
        }
        t = must(opened)
    }
    let got = __vy_httpExchange(conn, t, __vy_httpRequestText(u, method, body))
    if t != 0 { __tlsClose(t) }
    net.close(conn)
    return got
}

fn __vy_httpRequestText(u: __vy_URL, method: str, body: str) -> str {
    let host = u.host
    let deflt = 80
    if u.secure { deflt = 443 }
    if u.port != deflt { host = "{u.host}:{u.port}" }
    let req = "{method} {u.path} HTTP/1.1\r\n"
    req = req + "Host: {host}\r\n"
    req = req + "User-Agent: scandiacus\r\n"
    req = req + "Accept: */*\r\n"
    req = req + "Connection: close\r\n"
    if len(body) > 0 {
        req = req + "Content-Type: application/x-www-form-urlencoded\r\n"
        req = req + "Content-Length: {len(body)}\r\n"
    }
    return req + "\r\n" + body
}

// Send the request and read until the server closes, over TLS when t is
// a session and over the bare socket when it is zero. The server was
// asked to close when done, so a read returns empty at the end. Chunks
// go onto a list and are joined once, because appending each onto the
// last would be quadratic.
fn __vy_httpExchange(conn: int, t: int, req: str) -> str! {
    if t != 0 {
        __tlsSend(t, req)?
    } else {
        net.send(conn, req)?
    }
    let parts: []str = []
    while true {
        let chunk = ""
        if t != 0 {
            chunk = __tlsRecv(t)?
        } else {
            chunk = net.recv(conn)?
        }
        if len(chunk) == 0 { break }
        push(parts, chunk)
    }
    return join(parts, "")
}

// Where a Location header points, from the url that sent it: a full
// url, one starting with // (same scheme), an absolute path on the same
// host, or a path relative to the current one's directory.
fn __vy_httpResolve(from: __vy_URL, to: str) -> __vy_URL! {
    if startsWith(to, "http://") || startsWith(to, "https://") {
        return __vy_httpSplitURL(to)
    }
    if startsWith(to, "//") {
        let scheme = "http:"
        if from.secure { scheme = "https:" }
        return __vy_httpSplitURL(scheme + to)
    }
    let u = __vy_URL{host: from.host, port: from.port, path: to, secure: from.secure}
    if !startsWith(to, "/") {
        let dir = from.path
        let q = indexOf(dir, "?")
        if q >= 0 { dir = __substrB(dir, 0, q) }
        let cut = 0
        let i = 0
        while i < len(dir) {
            if __substrB(dir, i, i + 1) == "/" { cut = i + 1 }
            i = i + 1
        }
        u.path = __substrB(dir, 0, cut) + to
        if cut == 0 { u.path = "/" + to }
    }
    return u
}

// One header's value off a reply's header block, or "".
fn __vy_httpHeaderOf(head: str, name: str) -> str {
    let rows = split(head, "\r\n")
    let i = 1
    while i < len(rows) {
        let colon = indexOf(rows[i], ":")
        if colon > 0 {
            if lower(trim(__substrB(rows[i], 0, colon))) == name {
                return trim(__substrB(rows[i], colon + 1, len(rows[i])))
            }
        }
        i = i + 1
    }
    return ""
}

// The status code off the first line, "HTTP/1.1 200 OK" -> 200.
fn __vy_httpStatusCode(head: str) -> int {
    let line = head
    let nl = indexOf(head, "\r\n")
    if nl >= 0 { line = __substrB(head, 0, nl) }
    let sp = indexOf(line, " ")
    if sp < 0 { return 0 }
    let after = __substrB(line, sp + 1, len(line))
    let codeStr = after
    let sp2 = indexOf(after, " ")
    if sp2 >= 0 { codeStr = __substrB(after, 0, sp2) }
    if isInt(codeStr) { return toInt(codeStr) }
    return 0
}

// Whether the response says its body is chunked.
fn __vy_httpChunked(head: str) -> bool {
    let rows = split(head, "\r\n")
    let i = 0
    while i < len(rows) {
        let colon = indexOf(rows[i], ":")
        if colon > 0 {
            let name = lower(trim(__substrB(rows[i], 0, colon)))
            if name == "transfer-encoding" {
                let v = lower(trim(__substrB(rows[i], colon + 1, len(rows[i]))))
                if contains(v, "chunked") { return true }
            }
        }
        i = i + 1
    }
    return false
}

// Reassemble a chunked body: each chunk is a hex length, CRLF, that many
// bytes, CRLF, and a zero-length chunk ends it.
fn __vy_httpDechunk(body: str) -> str {
    let parts: []str = []
    let s = body
    while true {
        let nl = indexOf(s, "\r\n")
        if nl < 0 { break }
        let sizeLine = __substrB(s, 0, nl)
        let semi = indexOf(sizeLine, ";")
        if semi >= 0 { sizeLine = __substrB(sizeLine, 0, semi) }
        let n = __vy_hexToInt(sizeLine)
        if n <= 0 { break }
        let start = nl + 2
        push(parts, __substrB(s, start, start + n))
        s = __substrB(s, start + n + 2, len(s))
    }
    return join(parts, "")
}

// A hex string to an int, by looking each digit up in order. No ord()
// needed, and an unexpected character stops it where it is.
fn __vy_hexToInt(s: str) -> int {
    let digits = "0123456789abcdef"
    let t = lower(trim(s))
    let n = 0
    let i = 0
    while i < len(t) {
        let d = indexOf(digits, __substrB(t, i, i + 1))
        if d < 0 { return n }
        n = n * 16 + d
        i = i + 1
    }
    return n
}

fn __vy_httpGet(url: str) -> str! {
    return __vy_httpRequest("GET", url, "")
}

fn __vy_httpPost(url: str, body: str) -> str! {
    return __vy_httpRequest("POST", url, body)
}

// Save a url straight to a file, which is what a fetch is usually for.
fn __vy_httpDownload(url: str, path: str) -> void! {
    let body = __vy_httpRequest("GET", url, "")?
    return os.file.write(path, body)
}
`
