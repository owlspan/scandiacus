// Sprites: an image drawn with one colour left transparent, scaled up,
// and text centred by measuring it.
//
//     scandiacus run sprites.scd
//
// Images are .bmp files. This one is made at startup, so the example
// needs nothing beside it: an 8x8 smiley on a magenta background, and
// magenta is the colour drawKeyed leaves out.

fn le(n: int, width: int) -> []int {
    let out: []int = []
    let i = 0
    while i < width {
        push(out, (n >> (8 * i)) & 255)
        i += 1
    }
    return out
}

// Rows top to bottom; 1 is yellow, 2 is black, 0 is background.
const face = [
    "00111100",
    "01111110",
    "11211211",
    "11111111",
    "12111121",
    "11222211",
    "01111110",
    "00111100",
]

fn writeSprite(path: str) -> void! {
    let px: []int = []
    // A BMP stores rows bottom up, three bytes per pixel as blue, green,
    // red; eight pixels is 24 bytes, already a multiple of four.
    let row = 7
    while row >= 0 {
        for ch in chars(face[row]) {
            match ch {
                "1" => push(px, 0, 220, 255)
                "2" => push(px, 20, 20, 20)
                else => push(px, 255, 0, 255)
            }
        }
        row -= 1
    }
    let data: []int = [66, 77]
    for part in [le(54 + len(px), 4), le(0, 4), le(54, 4), le(40, 4), le(8, 4), le(8, 4),
                 le(1, 2), le(24, 2), le(0, 24), px] {
        for b in part {
            push(data, b)
        }
    }
    return bytes.write(path, bytes.ofList(data)?)
}

let path = os.path.join(os.dir.temp(), "scandiacus_sprite.bmp")
must(writeSprite(path))
let sprite = must(win.image(path))

let w = must(win.open("sprites", 640, 400))
let magenta = win.rgb(255, 0, 255)
let x = 40
let dx = 3
while win.poll(w) {
    if win.pressed(w, "esc") {
        break
    }
    win.clear(w, win.rgb(24, 26, 36))

    // The sprite at its own size, bouncing, with the background left out.
    win.drawKeyed(w, sprite, x, 60, magenta)
    x += dx
    if x < 0 || x > win.width(w) - win.imageWidth(sprite) {
        dx = -dx
    }

    // The same image scaled up eight times. drawScaled copies every
    // pixel, background included.
    win.drawScaled(w, sprite, 40, 120, 64, 64)

    let title = "press esc to quit"
    let tw = win.textWidth(w, title)
    win.text(w, (win.width(w) - tw) / 2, win.height(w) - 40, title, win.rgb(235, 238, 245))

    win.present(w)
    sleep(16)
}
win.close(w)
win.freeImage(sprite)
