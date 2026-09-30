// win.pixels: a window's or canvas's back buffer as memory, one u32 a
// pixel in 0x00RRGGBB, rows top to bottom. Pointer writes and GDI
// drawing share the same pixels.

let c = win.canvas(8, 4)
win.clear(c, win.rgb(0, 0, 255))
let p = win.pixels(c)
print(p[0] == 0x0000FF)

// Written through the pointer, read back through GDI.
p[1] = (255 << 16) | (128 << 8) | 7
p[3 * 8 + 7] = 0x00FF00
print(win.pixel(c, 1, 0) == win.rgb(255, 128, 7))
print(win.pixel(c, 7, 3) == win.rgb(0, 255, 0))
print(win.pixel(c, 2, 0) == win.rgb(0, 0, 255))

// Drawn through GDI, read through the pointer: win.pixels flushes
// what GDI still has batched first.
win.rect(c, 0, 1, 8, 1, win.rgb(10, 20, 30))
let q = win.pixels(c)
print(q[8 + 4] == ((10 << 16) | (20 << 8) | 30))

// A whole row filled by hand.
for x in 0..8 {
    q[2 * 8 + x] = 0xFFFFFF
}
let white = 0
for x in 0..8 {
    if win.pixel(c, x, 2) == win.rgb(255, 255, 255) {
        white += 1
    }
}
print(white)
