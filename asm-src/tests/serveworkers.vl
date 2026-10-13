// http.serveWorkers answers on several threads: a fast request is
// answered while a slow one is still being handled.
thread.spawn(fn() {
    must(http.serveWorkers(18093, 4, fn(req: Request) -> Response {
        if req.path == "/slow" {
            sleep(1500)
        }
        return http.ok("done " + req.path)
    }))
})
sleep(300)
let slowDone = atomic.new(0)
let slow = thread.spawn(fn() {
    print(must(http.get("http://localhost:18093/slow")))
    atomic.set(slowDone, 1)
})
sleep(200)
print(must(http.get("http://localhost:18093/fast")))
print("fast answered while slow ran: {atomic.get(slowDone) == 0}")
thread.join(slow)
