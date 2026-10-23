package main

// ui: a window whose contents are a web page, so an interface can be
// written in HTML and CSS and driven from Veyl.
//
// On Windows the page is a WebView2 control, the Edge engine that ships
// with Windows 11 and with every updated Windows 10. Nothing is linked:
// the runtime's own EmbeddedBrowserWebView.dll is found through the
// registry, which is what Microsoft's WebView2Loader.dll does, and
// everything after that is COM. The completion handlers and the
// message handler WebView2 calls back through are COM objects built
// here out of mem.callback stubs - a two-word block, the table and the
// state, with QueryInterface, AddRef, Release and Invoke in the table.
//
// The shape is the one win has: open, then poll in a loop. A message
// the page sends with veyl.send(...) waits in a queue of the strings
// WebView2 handed over until ui.messages takes them; nothing runs on
// a thread of its own, so every handler runs inside ui.poll.
//
// The state block, from mem.alloc and so zeroed:
//
//	  0 window       8 environment   16 controller   24 webview
//	 32 error        40 closed       48 inbox        56 inbox count
//	 64 inbox room   72 env handler  80 ctl handler  88 msg handler
//	104 ready       112 event token  120 RECT (16 bytes)

const preludeWebUI = `
fn __vy_uiUser(name: str) -> int {
    return mem.symbol("user32.dll", name)
}

// IUnknown for the handler objects. They live as long as the program,
// so the count is not kept, and any interface asked for is this one.
fn __vy_uiQuery(this: int, iid: int, out: int) -> int {
    mem.write64(out, this)
    return 0
}

fn __vy_uiRef(this: int) -> int {
    return 1
}

fn __vy_uiHandler(s: int, invoke: int) -> int {
    let table = mem.alloc(32)
    mem.write64(table, mem.callback(__vy_uiQuery))
    mem.write64(table + 8, mem.callback(__vy_uiRef))
    mem.write64(table + 16, mem.callback(__vy_uiRef))
    mem.write64(table + 24, invoke)
    let obj = mem.alloc(16)
    mem.write64(obj, table)
    mem.write64(obj + 8, s)
    return obj
}

// An HRESULT arrives in the low 32 bits of its register.
fn __vy_uiFailed(hr: int) -> bool {
    return (hr & 0x80000000) != 0
}

fn __vy_uiGiveUp(s: int, hr: int) {
    mem.write64(s + 32, hr & 0xFFFFFFFF)
    mem.write64(s + 104, 1)
}

// ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler: the
// environment is up, so ask it for a controller in the window.
fn __vy_uiEnvDone(this: int, hr: int, env: int) -> int {
    let s = mem.readI64(this + 8)
    if __vy_uiFailed(hr) || env == 0 {
        __vy_uiGiveUp(s, hr)
        return 0
    }
    com.addRef(env)
    mem.write64(s + 8, env)
    let r = com.call(env, 3, mem.readI64(s), mem.readI64(s + 80))
    if r < 0 {
        __vy_uiGiveUp(s, r)
    }
    return 0
}

// ICoreWebView2CreateCoreWebView2ControllerCompletedHandler.
fn __vy_uiControllerDone(this: int, hr: int, ctl: int) -> int {
    let s = mem.readI64(this + 8)
    if __vy_uiFailed(hr) || ctl == 0 {
        __vy_uiGiveUp(s, hr)
        return 0
    }
    com.addRef(ctl)
    mem.write64(s + 16, ctl)
    com.call(ctl, 25, s + 24)
    mem.write64(s + 104, 1)
    return 0
}

// ICoreWebView2WebMessageReceivedEventHandler: keep the string
// (TryGetWebMessageAsString, slot 5) until ui.messages reads it.
fn __vy_uiMessage(this: int, sender: int, args: int) -> int {
    let s = mem.readI64(this + 8)
    let out = mem.alloc(8)
    if com.call(args, 5, out) >= 0 && mem.readI64(out) != 0 {
        let n = mem.readI64(s + 56)
        let room = mem.readI64(s + 64)
        if n == room {
            room = room * 2
            mem.write64(s + 48, mem.resize(mem.readI64(s + 48), room * 8))
            mem.write64(s + 64, room)
        }
        mem.write64(mem.readI64(s + 48) + n * 8, mem.readI64(out))
        mem.write64(s + 56, n + 1)
    }
    mem.free(out)
    return 0
}

// The page fills the window's client area.
fn __vy_uiFit(s: int) {
    let ctl = mem.readI64(s + 16)
    if ctl == 0 { return }
    mem.call(__vy_uiUser("GetClientRect"), mem.readI64(s), s + 120)
    // put_Bounds takes a RECT by value, which the x64 convention passes
    // as the address of a copy.
    com.call(ctl, 6, s + 120)
}

fn __vy_uiWndProc(h: int, msg: int, wp: int, lp: int) -> int {
    let m = msg & 0xFFFFFFFF
    let s = mem.call(__vy_uiUser("GetWindowLongPtrW"), h, -21)
    if s != 0 {
        if m == 5 {
            __vy_uiFit(s)
        } else if m == 7 && mem.readI64(s + 16) != 0 {
            com.call(mem.readI64(s + 16), 12, 0)
        } else if m == 16 {
            // WM_CLOSE: ui.poll says so and the program decides; the
            // window goes when it calls ui.close or ends.
            mem.write64(s + 40, 1)
            return 0
        }
    }
    return mem.call(__vy_uiUser("DefWindowProcW"), h, m, wp, lp)
}

// Handle what has arrived, after waiting up to wait ms for something
// to.
fn __vy_uiPump(wait: int) {
    mem.call(__vy_uiUser("MsgWaitForMultipleObjectsEx"), 0, 0, wait, 0x4FF, 4)
    let msg = mem.alloc(48)
    while (mem.call(__vy_uiUser("PeekMessageW"), msg, 0, 0, 0, 1) & 0xFFFFFFFF) != 0 {
        mem.call(__vy_uiUser("TranslateMessage"), msg)
        mem.call(__vy_uiUser("DispatchMessageW"), msg)
    }
    mem.free(msg)
}

fn __vy_uiReg(root: int, key: str, value: str) -> str {
    let buf = mem.alloc(1040)
    let size = mem.alloc(8)
    mem.write32(size, 1040)
    let text = ""
    // RRF_RT_REG_SZ
    let r = mem.call(mem.symbol("advapi32.dll", "RegGetValueW"), root, mem.wide(key), mem.wide(value), 2, 0, buf, size)
    if (r & 0xFFFFFFFF) == 0 {
        text = mem.wstr(buf)
    }
    mem.free(buf)
    mem.free(size)
    return text
}

fn __vy_uiRegDword(root: int, key: str, value: str) -> int {
    let buf = mem.alloc(8)
    let size = mem.alloc(8)
    mem.write32(size, 4)
    let v = -1
    // RRF_RT_REG_DWORD
    let r = mem.call(mem.symbol("advapi32.dll", "RegGetValueW"), root, mem.wide(key), mem.wide(value), 0x10, 0, buf, size)
    if (r & 0xFFFFFFFF) == 0 {
        v = mem.readU32(buf)
    }
    mem.free(buf)
    mem.free(size)
    return v
}

// Where the installed WebView2 runtime keeps the DLL that starts it:
// per machine, then per user. "" if it is not installed.
fn __vy_uiRuntime() -> str {
    let guid = "{{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}}"
    let keys = ["SOFTWARE\\WOW6432Node\\Microsoft\\EdgeUpdate\\Clients\\" + guid,
                "Software\\Microsoft\\EdgeUpdate\\Clients\\" + guid]
    // HKEY_LOCAL_MACHINE and HKEY_CURRENT_USER, sign-extended as
    // handles are.
    let roots = [-2147483646, -2147483647]
    for root in roots {
        for key in keys {
            let place = __vy_uiReg(root, key, "location")
            let version = __vy_uiReg(root, key, "pv")
            if place != "" && version != "" && version != "0.0.0.0" {
                let dll = place + "\\" + version + "\\EBWebView\\x64\\EmbeddedBrowserWebView.dll"
                if os.file.exists(dll) { return dll }
            }
        }
    }
    return ""
}

// What the page has: veyl.send(x) posts x, as text, to ui.messages,
// and veyl.onmessage receives what ui.send sends.
fn __vy_uiBridge() -> str {
    let js = "window.veyl = {{ onmessage: null, send: function (m) {{ "
    js += "window.chrome.webview.postMessage(typeof m === 'string' ? m : JSON.stringify(m)) }} }};"
    js += "window.chrome.webview.addEventListener('message', function (e) {{ "
    js += "if (window.veyl.onmessage) window.veyl.onmessage(e.data) }});"
    return js
}

fn __vy_uiOpen(title: str, width: int, height: int) -> int! {
    if width < 1 || height < 1 {
        return fail("ui.open: the size has to be positive, got " + str(width) + "x" + str(height))
    }
    if os.name() != "windows" {
        return fail("ui: windows with web pages are Windows-only for now")
    }
    let dll = __vy_uiRuntime()
    let start = 0
    if dll != "" {
        start = mem.symbol(dll, "CreateWebViewEnvironmentWithOptionsInternal")
    }
    let loader = 0
    if start == 0 {
        // A WebView2Loader.dll shipped next to the program, if there is
        // one, knows other ways to find a runtime.
        loader = mem.symbol("WebView2Loader.dll", "CreateCoreWebView2EnvironmentWithOptions")
        if loader == 0 {
            let get = "it comes with Windows 11 and updated Windows 10, or from https://developer.microsoft.com/microsoft-edge/webview2/"
            return fail("ui: the WebView2 runtime is not installed - " + get)
        }
    }

    // COINIT_APARTMENTTHREADED; a second call on the thread is harmless.
    mem.call(mem.symbol("ole32.dll", "CoInitializeEx"), 0, 2)
    // Per-monitor DPI awareness, so the page is drawn sharp rather than
    // scaled up as a bitmap. Windows older than 1703 has no such call.
    let aware = __vy_uiUser("SetProcessDpiAwarenessContext")
    if aware != 0 {
        mem.call(aware, -4)
    }

    let s = mem.alloc(144)
    mem.write64(s + 48, mem.alloc(64))
    mem.write64(s + 64, 8)

    let inst = mem.call(mem.symbol("kernel32.dll", "GetModuleHandleW"), 0)
    let cls = mem.wide("VeylWebView")
    let wc = mem.alloc(80)
    mem.write32(wc, 80)
    mem.write32(wc + 4, 3)
    mem.write64(wc + 8, mem.callback(__vy_uiWndProc))
    mem.write64(wc + 24, inst)
    // The program's own icon when it was built with one (--icon).
    mem.write64(wc + 32, mem.call(__vy_uiUser("LoadIconW"), inst, 1))
    mem.write64(wc + 40, mem.call(__vy_uiUser("LoadCursorW"), 0, 32512))
    mem.write64(wc + 48, 6)
    mem.write64(wc + 64, mem.addr(cls))
    // A second registration fails because the class exists, which is
    // fine.
    mem.call(__vy_uiUser("RegisterClassExW"), wc)
    mem.free(wc)

    // WS_OVERLAPPEDWINDOW | WS_CLIPCHILDREN, at CW_USEDEFAULT.
    let style = 0x02CF0000
    let h = mem.call(__vy_uiUser("CreateWindowExW"), 0, cls, mem.wide(title), style,
        0x80000000, 0x80000000, width, height, 0, 0, inst, 0)
    if h == 0 {
        mem.free(s)
        return fail("ui.open: the window could not be made")
    }
    mem.write64(s, h)

    // The size asked for is the page's, in the pixels CSS means.
    let dpi = 96
    let forWindow = __vy_uiUser("GetDpiForWindow")
    if forWindow != 0 {
        dpi = mem.call(forWindow, h) & 0xFFFFFFFF
    }
    let outer = mem.alloc(16)
    let inner = mem.alloc(16)
    mem.call(__vy_uiUser("GetWindowRect"), h, outer)
    mem.call(__vy_uiUser("GetClientRect"), h, inner)
    let dw = (mem.readI32(outer + 8) - mem.readI32(outer)) - mem.readI32(inner + 8)
    let dh = (mem.readI32(outer + 12) - mem.readI32(outer + 4)) - mem.readI32(inner + 12)
    mem.free(outer)
    mem.free(inner)
    // SWP_NOMOVE | SWP_NOZORDER
    mem.call(__vy_uiUser("SetWindowPos"), h, 0, 0, 0, width * dpi / 96 + dw, height * dpi / 96 + dh, 6)

    // A dark title bar when Windows is set to dark (DWMWA_USE_IMMERSIVE_DARK_MODE).
    if __vy_uiRegDword(-2147483647, "Software\\Microsoft\\Windows\\CurrentVersion\\Themes\\Personalize", "AppsUseLightTheme") == 0 {
        let on = mem.alloc(8)
        mem.write32(on, 1)
        mem.call(mem.symbol("dwmapi.dll", "DwmSetWindowAttribute"), h, 20, on, 4)
        mem.free(on)
    }

    mem.call(__vy_uiUser("SetWindowLongPtrW"), h, -21, s)
    mem.call(__vy_uiUser("ShowWindow"), h, 5)
    mem.call(__vy_uiUser("UpdateWindow"), h)

    mem.write64(s + 72, __vy_uiHandler(s, mem.callback(__vy_uiEnvDone)))
    mem.write64(s + 80, __vy_uiHandler(s, mem.callback(__vy_uiControllerDone)))
    mem.write64(s + 88, __vy_uiHandler(s, mem.callback(__vy_uiMessage)))

    // WebView2 keeps its profile - cache, storage - in a folder it can
    // write to; beside the program is often not one.
    let data = os.path.join(os.env.get("LOCALAPPDATA"), "veyl", "webview")
    let hr = 0
    if start != 0 {
        hr = mem.call(start, 1, 0, mem.wide(data), 0, mem.readI64(s + 72))
    } else {
        hr = mem.call(loader, 0, mem.wide(data), 0, mem.readI64(s + 72))
    }
    if __vy_uiFailed(hr) {
        __vy_uiGiveUp(s, hr)
    }
    let waited = 0
    while mem.readI64(s + 104) == 0 && waited < 30000 {
        __vy_uiPump(20)
        waited += 20
    }
    if mem.readI64(s + 16) == 0 || mem.readI64(s + 24) == 0 {
        let code = mem.readI64(s + 32)
        mem.call(__vy_uiUser("DestroyWindow"), h)
        if code == 0 {
            return fail("ui.open: WebView2 did not start within 30 seconds")
        }
        let why = com.message(code)
        if why != "" { why = " (" + trim(why) + ")" }
        return fail("ui.open: WebView2 did not start: " + com.hex(code) + why)
    }

    let wv = mem.readI64(s + 24)
    let settings = mem.alloc(8)
    if com.call(wv, 3, settings) >= 0 {
        // put_IsStatusBarEnabled: no URL popping up in the corner.
        com.call(mem.readI64(settings), 10, 0)
        com.release(mem.readI64(settings))
    }
    mem.free(settings)
    com.call(wv, 34, mem.readI64(s + 88), s + 112)
    com.call(wv, 27, mem.wide(__vy_uiBridge()), 0)
    __vy_uiFit(s)
    // Keys go to the page from the start (MoveFocus, programmatic).
    com.call(mem.readI64(s + 16), 12, 0)
    return s
}

fn __vy_uiLive(s: int) -> bool {
    return mem.readI64(s + 24) != 0
}

fn __vy_uiHtml(s: int, html: str) {
    if __vy_uiLive(s) { com.call(mem.readI64(s + 24), 6, mem.wide(html)) }
}

fn __vy_uiUrl(s: int, url: str) {
    if __vy_uiLive(s) { com.call(mem.readI64(s + 24), 5, mem.wide(url)) }
}

// A file on disk, so the page's own links - style.css, app.js, images
// - resolve beside it.
fn __vy_uiFile(s: int, path: str) {
    let full = replace(os.path.absolute(path), "\\", "/")
    let url = replace(replace(replace(full, "%", "%25"), " ", "%20"), "#", "%23")
    __vy_uiUrl(s, "file:///" + url)
}

fn __vy_uiEval(s: int, js: str) {
    if __vy_uiLive(s) { com.call(mem.readI64(s + 24), 29, mem.wide(js), 0) }
}

fn __vy_uiSend(s: int, text: str) {
    if __vy_uiLive(s) { com.call(mem.readI64(s + 24), 33, mem.wide(text)) }
}

fn __vy_uiTitle(s: int, title: str) {
    mem.call(__vy_uiUser("SetWindowTextW"), mem.readI64(s), mem.wide(title))
}

// Handle the window's events and the page's messages, waiting up to
// 15 ms for one to arrive. false once the window has been closed.
fn __vy_uiPoll(s: int) -> bool {
    if mem.readI64(s + 40) != 0 { return false }
    __vy_uiPump(15)
    return mem.readI64(s + 40) == 0
}

// What the page sent since the last call, oldest first.
fn __vy_uiMessages(s: int) -> []str {
    let out: []str = []
    let n = mem.readI64(s + 56)
    let box = mem.readI64(s + 48)
    let free = mem.symbol("ole32.dll", "CoTaskMemFree")
    let i = 0
    while i < n {
        let p = mem.readI64(box + i * 8)
        push(out, mem.wstr(p))
        mem.call(free, p)
        i += 1
    }
    mem.write64(s + 56, 0)
    return out
}

fn __vy_uiClose(s: int) {
    mem.write64(s + 40, 1)
    let ctl = mem.readI64(s + 16)
    if ctl != 0 {
        com.call(ctl, 24)
        com.release(mem.readI64(s + 24))
        com.release(ctl)
        mem.write64(s + 16, 0)
        mem.write64(s + 24, 0)
    }
    if mem.readI64(s) != 0 {
        mem.call(__vy_uiUser("SetWindowLongPtrW"), mem.readI64(s), -21, 0)
        mem.call(__vy_uiUser("DestroyWindow"), mem.readI64(s))
        mem.write64(s, 0)
    }
}
`
