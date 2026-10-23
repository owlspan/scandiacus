package main

// Widgets and key names, in Scandiacus.
//
// Same arrangement as http on net: the lowerer provides the primitives
// that have to touch Win32, and everything built out of them is Scandiacus
// source compiled by this compiler.
//
// The widgets are immediate mode. There is no widget tree and nothing
// to keep in sync: a button is drawn and its answer returned in the
// same call, so the UI is whatever this frame's code says it is. That
// suits a game loop, which is what this is for.

const preludeUI = `
// Windows virtual key codes, by a name rather than a number. A single
// character is its own code, which is why "a" and "7" work without
// being listed.
fn __vy_winKeyCode(name: str) -> int {
    let k = lower(name)
    if k == "left"  { return 37 }
    if k == "up"    { return 38 }
    if k == "right" { return 39 }
    if k == "down"  { return 40 }
    if k == "space" { return 32 }
    if k == "esc" || k == "escape" { return 27 }
    if k == "enter" || k == "return" { return 13 }
    if k == "tab"   { return 9 }
    if k == "back" || k == "backspace" { return 8 }
    if k == "shift" { return 16 }
    if k == "ctrl" || k == "control" { return 17 }
    if k == "alt"   { return 18 }
    if len(k) == 1 { return __strAt(upper(k), 0) }
    return 0
}

fn __vy_winPressed(w: int, name: str) -> bool {
    return win.key(w, __vy_winKeyCode(name))
}

// Is the pointer inside this box?
fn __vy_winHover(w: int, x: int, y: int, bw: int, bh: int) -> bool {
    let mx = win.mouseX(w)
    let my = win.mouseY(w)
    return mx >= x && mx < x + bw && my >= y && my < y + bh
}

// A button. Draws itself and returns whether it was clicked this frame.
//
// Three faces, because a button that does not react to the pointer
// reads as a picture of a button rather than a control.
fn __vy_winButton(w: int, x: int, y: int, bw: int, bh: int, label: str) -> bool {
    let over = __vy_winHover(w, x, y, bw, bh)

    let face = win.rgb(58, 58, 68)
    if over { face = win.rgb(88, 88, 104) }
    if over && win.mouseDown(w) { face = win.rgb(38, 38, 46) }

    win.rect(w, x, y, bw, bh, win.rgb(18, 18, 24))
    win.rect(w, x + 1, y + 1, bw - 2, bh - 2, face)
    win.text(w, x + 10, y + bh / 2 - 8, label, win.rgb(236, 236, 242))

    return over && win.clicked(w)
}

// A filled bar, for health, loading, or anything from 0.0 to 1.0.
fn __vy_winBar(w: int, x: int, y: int, bw: int, bh: int, frac: float, colour: int) {
    let f = frac
    if f < 0.0 { f = 0.0 }
    if f > 1.0 { f = 1.0 }
    win.rect(w, x, y, bw, bh, win.rgb(30, 30, 38))
    let fill = int(float(bw - 2) * f)
    if fill > 0 { win.rect(w, x + 1, y + 1, fill, bh - 2, colour) }
}

// An outlined box, one pixel, drawn as four thin rectangles because
// there is no stroke-without-fill primitive.
fn __vy_winFrame(w: int, x: int, y: int, bw: int, bh: int, colour: int) {
    win.rect(w, x, y, bw, 1, colour)
    win.rect(w, x, y + bh - 1, bw, 1, colour)
    win.rect(w, x, y, 1, bh, colour)
    win.rect(w, x + bw - 1, y, 1, bh, colour)
}
`
