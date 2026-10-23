// Canvases, images and text measurement, with no window: everything
// is drawn off-screen and checked with win.pixel. The BMP is built here
// byte by byte so the test needs no files of its own.

// A 4x2 24-bit BMP, bottom-up rows padded to four bytes: the top row
// is red, green, blue, magenta; the bottom row all white.
fn le(n: int, width: int) -> []int {
    let out: []int = []
    let i = 0
    while i < width {
        push(out, (n >> (8 * i)) & 255)
        i += 1
    }
    return out
}
let px: []int = []
// bottom row first (BGR)
for i in 0..4 {
    push(px, 255, 255, 255)
}
push(px, 0, 0, 255)
push(px, 0, 255, 0)
push(px, 255, 0, 0)
push(px, 255, 0, 255)
let head: []int = [66, 77]
for b in le(54 + len(px), 4) { push(head, b) }
for b in le(0, 4) { push(head, b) }
for b in le(54, 4) { push(head, b) }
for b in le(40, 4) { push(head, b) }
for b in le(4, 4) { push(head, b) }
for b in le(2, 4) { push(head, b) }
for b in le(1, 2) { push(head, b) }
for b in le(24, 2) { push(head, b) }
for b in le(0, 24) { push(head, b) }
for b in px { push(head, b) }
let path = os.path.join(os.dir.temp(), "scandiacus_media_test.bmp")
must(bytes.write(path, must(bytes.ofList(head))))

let c = win.canvas(64, 48)
print("{win.width(c)}x{win.height(c)}")
print(win.poll(c))
win.clear(c, win.rgb(10, 20, 30))
print(win.pixel(c, 5, 5) == win.rgb(10, 20, 30))
win.rect(c, 10, 10, 5, 5, win.rgb(200, 100, 50))
print(win.pixel(c, 12, 12) == win.rgb(200, 100, 50))
print(win.pixel(c, 16, 16) == win.rgb(10, 20, 30))
print(win.pixel(c, 100, 100))

let img = must(win.image(path))
print("{win.imageWidth(img)}x{win.imageHeight(img)}")
win.draw(c, img, 0, 0)
print(win.pixel(c, 0, 0) == win.rgb(255, 0, 0))
print(win.pixel(c, 1, 0) == win.rgb(0, 255, 0))
print(win.pixel(c, 2, 0) == win.rgb(0, 0, 255))
print(win.pixel(c, 3, 1) == win.rgb(255, 255, 255))

win.clear(c, win.rgb(0, 0, 0))
win.drawScaled(c, img, 0, 0, 8, 4)
print(win.pixel(c, 7, 0) == win.rgb(255, 0, 255))
print(win.pixel(c, 6, 3) == win.rgb(255, 255, 255))

win.clear(c, win.rgb(1, 2, 3))
win.drawKeyed(c, img, 20, 20, win.rgb(255, 0, 255))
print(win.pixel(c, 23, 20) == win.rgb(1, 2, 3))
print(win.pixel(c, 22, 20) == win.rgb(0, 0, 255))
win.freeImage(img)

print(win.textWidth(c, "hello") > win.textWidth(c, "hi"))
print(win.textHeight(c) > 0)
print(failed(win.image("nope.bmp")))
print(errorOf(win.image("nope.bmp")))
print(sound.play("no_such_sound.wav"))
sound.stop()
must(os.file.delete(path))
