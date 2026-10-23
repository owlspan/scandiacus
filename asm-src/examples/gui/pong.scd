// Pong.
//
// Not in examples/ with the rest because the differential test runs
// everything there through both backends and compares the output, and
// this opens a window instead of printing.
//
// Arrow keys or W and S. Space serves. Escape quits.

const W = 800
const H = 500
const PADDLE_H = 90
const PADDLE_W = 12

let w = must(win.open("scandiacus pong", W, H))

let bg = win.rgb(18, 20, 28)
let fg = win.rgb(235, 238, 245)
let dim = win.rgb(60, 64, 78)
let accent = win.rgb(120, 200, 140)
let warn = win.rgb(220, 90, 90)

let py = float(H / 2 - PADDLE_H / 2)
let ay = py
let bx = float(W / 2)
let by = float(H / 2)
let vx = 0.0
let vy = 0.0
let you = 0
let them = 0
let serving = true

while win.poll(w) {
    if win.pressed(w, "esc") {
        break
    }

    // ---- input ----
    if win.pressed(w, "up") || win.pressed(w, "w") {
        py = py - 7.0
    }
    if win.pressed(w, "down") || win.pressed(w, "s") {
        py = py + 7.0
    }
    if py < 0.0 {
        py = 0.0
    }
    if py > float(H - PADDLE_H) {
        py = float(H - PADDLE_H)
    }

    if serving && win.pressed(w, "space") {
        serving = false
        vx = 6.0
        vy = 3.5
        if rand.bool() {
            vy = -3.5
        }
    }

    // ---- the ball ----
    if !serving {
        bx = bx + vx
        by = by + vy

        if by < 8.0 {
            by = 8.0
            vy = -vy
        }
        if by > float(H) - 8.0 {
            by = float(H) - 8.0
            vy = -vy
        }

        // Your paddle, on the left.
        if bx < 30.0 && bx > 18.0 && by > py && by < py + float(PADDLE_H) && vx < 0.0 {
            vx = -vx
            // Where it hits the paddle bends the angle, which is the
            // whole game: a flat bounce has no play in it.
            let off = (by - (py + float(PADDLE_H) / 2.0)) / float(PADDLE_H / 2)
            vy = vy + off * 3.0
        }

        // The computer's paddle, on the right.
        let rx = float(W - 30)
        if bx > rx - 12.0 && bx < rx && by > ay && by < ay + float(PADDLE_H) && vx > 0.0 {
            vx = -vx
            let off = (by - (ay + float(PADDLE_H) / 2.0)) / float(PADDLE_H / 2)
            vy = vy + off * 3.0
        }

        if bx < 0.0 {
            them = them + 1
            serving = true
            bx = float(W / 2)
            by = float(H / 2)
        }
        if bx > float(W) {
            you = you + 1
            serving = true
            bx = float(W / 2)
            by = float(H / 2)
        }

        // The opponent tracks the ball, but slowly enough to be beaten.
        let target = by - float(PADDLE_H) / 2.0
        if ay < target - 4.0 {
            ay = ay + 4.6
        }
        if ay > target + 4.0 {
            ay = ay - 4.6
        }
        if ay < 0.0 {
            ay = 0.0
        }
        if ay > float(H - PADDLE_H) {
            ay = float(H - PADDLE_H)
        }
    }

    // ---- draw ----
    win.clear(w, bg)

    let y = 0
    while y < H {
        win.rect(w, W / 2 - 1, y, 2, 12, dim)
        y = y + 26
    }

    win.rect(w, 18, int(py), PADDLE_W, PADDLE_H, accent)
    win.rect(w, W - 30, int(ay), PADDLE_W, PADDLE_H, warn)
    win.circle(w, int(bx), int(by), 8, fg)

    win.text(w, W / 2 - 90, 20, "you {you}", fg)
    win.text(w, W / 2 + 50, 20, "cpu {them}", fg)

    if serving {
        win.text(w, W / 2 - 100, H / 2 + 40, "press space to serve", fg)
    }

    win.present(w)
    sleep(16)
}

print("final score  you {you}  cpu {them}")
win.close(w)
