// The widget helpers: buttons, bars and frames.
//
// Immediate mode, so there is no widget tree and nothing to keep in
// sync. A button draws itself and returns whether it was clicked, in
// the same call, so the UI is whatever this frame's code says it is.

let w = must(win.open("scandiacus widgets", 520, 360))

let bg = win.rgb(22, 24, 32)
let ink = win.rgb(235, 238, 245)
let dim = win.rgb(120, 126, 140)
let good = win.rgb(110, 200, 140)
let warn = win.rgb(230, 150, 70)

let count = 0
let health = 1.0

while win.poll(w) {
    if win.pressed(w, "esc") {
        break
    }

    win.clear(w, bg)
    win.text(w, 24, 20, "Scandiacus widgets", ink)
    win.text(w, 24, 44, "immediate mode, no widget tree", dim)
    win.frame(w, 16, 12, 488, 60, win.rgb(60, 64, 78))

    if win.button(w, 24, 96, 130, 40, "count up") {
        count = count + 1
    }
    if win.button(w, 170, 96, 130, 40, "count down") {
        count = count - 1
    }
    if win.button(w, 316, 96, 130, 40, "reset") {
        count = 0
        health = 1.0
    }

    win.text(w, 24, 156, "count is {count}", ink)

    if win.button(w, 24, 190, 130, 40, "take a hit") {
        health = health - 0.15
        if health < 0.0 {
            health = 0.0
        }
    }

    win.text(w, 24, 246, "health", dim)
    let colour = good
    if health < 0.35 {
        colour = warn
    }
    win.bar(w, 24, 268, 460, 22, health, colour)

    if win.hover(w, 24, 268, 460, 22) {
        win.text(w, 24, 300, "hovering the bar", dim)
    }

    win.text(w, 24, 326, "mouse {win.mouseX(w)},{win.mouseY(w)}  esc to quit", dim)
    win.present(w)
    sleep(16)
}

print("count ended at {count}")
win.close(w)
