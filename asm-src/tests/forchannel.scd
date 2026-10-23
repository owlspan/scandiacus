// for v in ch: every value, until the channel is closed and drained.

let ch = channel<int>()
let producer = thread.spawn(fn() {
    for i in 1..=5 {
        ch.send(i * i)
    }
    ch.close()
})
let sum = 0
for v in ch {
    if v == 9 {
        continue
    }
    sum += v
    print(v)
}
thread.join(producer)
print(sum)
