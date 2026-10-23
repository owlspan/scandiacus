// PNG decoding. Each fixture in tests/png was written by gen.py beside
// it, with known pixel content; the hash is of the exact 32-bit
// premultiplied pixels the decoder must produce. The paths are relative
// to where go test runs, asm-src/compiler.
//
// __vy_pngLoad is the decoder behind win.image, called directly so the
// whole buffer can be checked.

const cases = [
    ["../tests/png/rgba_f4_z0.png", "5363cb31d914eb264fe02089e3c6e136cf2e34d268d85d80f9db3ed8094e2d63"],
    ["../tests/png/rgba_f-1_z9.png", "5363cb31d914eb264fe02089e3c6e136cf2e34d268d85d80f9db3ed8094e2d63"],
    ["../tests/png/rgb.png", "5330c24aff9eac93bc01f0a54a2b133d5dba7cc7c81304c89ec879cabf621ad0"],
    ["../tests/png/grey.png", "aeba8adc9230249e6176c0c626ded51acc77819dabdd071a397b5ea596d06c2b"],
    ["../tests/png/greya.png", "a22de9642e836637b22c4c4a47db653d81b08e888a2236fc08be20bbb2834d3c"],
    ["../tests/png/pal1.png", "3dbc9ecb685ccf6f80a5884db6f8699ce711849a62c91e77f5049e5523cba6f7"],
    ["../tests/png/pal2.png", "f89b773d7696a2f5a2cdde84be8ffe5f0fa1b1332d4c8192f597189c8ec6fddc"],
    ["../tests/png/pal4.png", "5a41e7cfed98deef9b85a3b9f73051c5ae5a2f163c401bfea6a308d5bc468983"],
    ["../tests/png/pal8.png", "5ec13f982d3249150b12f5c7475f37793c14ca265a12819ca8788ac6f5da65fd"],
    ["../tests/png/pillow.png", "5363cb31d914eb264fe02089e3c6e136cf2e34d268d85d80f9db3ed8094e2d63"],
]
for c in cases {
    let px = __vy_pngLoad(c[0])
    if failed(px) {
        print("{c[0]}: {errorOf(px)}")
    } else if must(bytes.hash(must(px), "sha256")) == c[1] {
        print("{c[0]}: exact")
    } else {
        print("{c[0]}: wrong pixels")
    }
}
print(errorOf(__vy_pngLoad("../tests/png/deep.png")))

// Drawing a PNG with alpha: a half-transparent pixel is blended with
// what is under it, a fully transparent one leaves it alone.

fn be(n: int) -> []int {
    return [(n >> 24) & 255, (n >> 16) & 255, (n >> 8) & 255, n & 255]
}
fn chunk(tag: str, body: []int) -> []int {
    let out = be(len(body))
    for ch in chars(tag) {
        push(out, bytes.list(bytes.of(ch))[0])
    }
    for b in body {
        push(out, b)
    }
    for b in [0, 0, 0, 0] {
        push(out, b)
    }
    return out
}
// A 2x2 RGBA image: opaque red, half-transparent green, fully
// transparent blue, opaque white.
let raw = [0, 255, 0, 0, 255, 0, 255, 0, 128, 0, 0, 0, 255, 0, 255, 255, 255, 255]
let z: []int = [120, 1, 1, len(raw), 0, 255 - len(raw), 255]
for b in raw {
    push(z, b)
}
push(z, 0, 0, 0, 0)
let file: []int = [137, 80, 78, 71, 13, 10, 26, 10]
for part in [chunk("IHDR", [0, 0, 0, 2, 0, 0, 0, 2, 8, 6, 0, 0, 0]), chunk("IDAT", z), chunk("IEND", [])] {
    for b in part {
        push(file, b)
    }
}
let path = os.path.join(os.dir.temp(), "scandiacus_alpha_test.png")
must(bytes.write(path, must(bytes.ofList(file))))

let img = must(win.image(path))
print("{win.imageWidth(img)}x{win.imageHeight(img)}")
let c = win.canvas(8, 8)
win.clear(c, win.rgb(0, 0, 200))
win.draw(c, img, 2, 2)
print(win.pixel(c, 2, 2) == win.rgb(255, 0, 0))
let half = win.pixel(c, 3, 2)
print("{half & 255} {(half >> 8) & 255} {(half >> 16) & 255}")
print(win.pixel(c, 2, 3) == win.rgb(0, 0, 200))
print(win.pixel(c, 3, 3) == win.rgb(255, 255, 255))
win.clear(c, win.rgb(0, 0, 0))
win.drawScaled(c, img, 0, 0, 8, 8)
print(win.pixel(c, 1, 1) == win.rgb(255, 0, 0))
print(win.pixel(c, 6, 6) == win.rgb(255, 255, 255))
win.freeImage(img)
must(os.file.delete(path))
// Only the start: the rest is the operating system's own wording,
// which differs between Windows versions and wine.
let missing = errorOf(win.image("missing.png"))
print(startsWith(missing, "cannot read \"missing.png\": open missing.png: "))
