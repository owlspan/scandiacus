// win.handle gives the window's HWND, which Windows itself accepts,
// and win.display the module handle it was made with.

extern fn IsWindow(hwnd: ptr) -> bool from "user32"
extern fn GetClassNameA(hwnd: ptr, buf: bytes, n: int) -> int from "user32"

let w = must(win.open("handle", 200, 100))
let h = win.handle(w)
print(h != 0)
print(win.display(w) != 0)
print(IsWindow(h))
let buf = must(bytes.fill(64, 0))
print(GetClassNameA(h, buf, 64) > 0)
win.close(w)
print(IsWindow(h))
