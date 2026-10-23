// The ui library's checks, which need no window: a size that cannot be
// shown is refused before anything is opened.
print(errorOf(ui.open("x", 0, 400)))
print(errorOf(ui.open("x", 640, -1)))
print("done")
