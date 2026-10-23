// A TCP client and server talking over the loopback, to exercise the net
// library. Uses the Linux/macOS socket runtime; skipped on Windows, which
// reaches the same net library through WinSock instead.
let srv = must(net.listen(18090))
thread.spawn(fn() {
    let c = must(net.accept(srv))
    let msg = must(net.recv(c))
    must(net.send(c, "echo:" + msg))
    net.close(c)
})
let s = must(net.connect("localhost", 18090))
must(net.send(s, "hello"))
print(must(net.recv(s)))
net.close(s)
