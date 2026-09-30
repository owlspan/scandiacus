package main

// Windows, drawing and input.
//
// The shape is a game loop rather than the old backend's blocking
// openWindow:
//
//	let w = must(win.open("game", 800, 600))
//	while win.poll(w) {
//	    win.clear(w, win.rgb(20, 20, 30))
//	    win.rect(w, x, y, 40, 40, win.rgb(220, 80, 80))
//	    win.present(w)
//	}
//
// Two decisions make this much smaller than it looks.
//
// The window class uses DefWindowProcA directly as its procedure, so
// there is no callback into generated code at all. Everything this
// needs to know is read out of the message queue in win.poll before
// the message is dispatched, and a closed window is noticed with
// IsWindow rather than by handling WM_DESTROY. The old backend needed
// syscall.NewCallback for this and had a fixed pool of them to worry
// about; there is nothing to run out of here.
//
// Drawing goes to a memory bitmap and win.present blits it in one go.
// Drawing straight to the window flickers, and the fix is the same one
// every graphics program reaches for.

// Win32 constants.
const (
	// WS_OVERLAPPEDWINDOW, and the same without WS_THICKFRAME and
	// WS_MAXIMIZEBOX, which are between them the whole of "resizable".
	wsOverlappedWindow = 0x00CF0000
	wsFixedWindow      = 0x00CA0000
	wsVisible          = 0x10000000

	gwlStyle       = -16
	swpFrameChange = 0x0020 | 0x0002 | 0x0001 | 0x0004 // FRAMECHANGED|NOMOVE|NOSIZE|NOZORDER
	cwUseDefault   = -2147483648                       // 0x80000000 as a signed int
	swShow         = 5
	pmRemove       = 0x0001
	srcCopy        = 0x00CC0020
	transparentBk  = 1
	idcArrow       = 32512
	whiteBrush     = 0

	wmQuit        = 0x0012
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmKeyDown     = 0x0100
	wmKeyUp       = 0x0101

	// MSG on x64: hwnd, message, wParam, lParam, time, point.
	msgSize      = 48
	msgMessageAt = 8
	msgWParamAt  = 16
	msgLParamAt  = 24

	// WNDCLASSA on x64.
	wndClassSize      = 72
	wcWndProcAt       = 8
	wcInstanceAt      = 24
	wcCursorAt        = 40
	wcBackgroundAt    = 48
	wcClassNameAt     = 64
	wndClassNameConst = "VeylWindow"
)

// The window block. One allocation holding everything about a window,
// handed to Veyl as an int so a window is a handle like a socket is.
//
// It comes from calloc, not the collected heap. A handle is an int, and
// the collector does not look inside ints - a window kept in a struct
// field or a list would otherwise be freed while still open.
const (
	winHwndAt   = 0
	winDCAt     = 8
	winMemDCAt  = 16
	winBitmapAt = 24
	winWidthAt  = 32
	winHeightAt = 40
	winMouseXAt = 48
	winMouseYAt = 56
	winDownAt   = 64
	winClickAt  = 72
	winClosedAt = 80
	winKeysAt   = 88              // 256 bytes, one per virtual key code
	winBitsAt   = winKeysAt + 256 // the back buffer's pixels; see dibBuffer
	winBlockLen = winBitsAt + 8
)

// user32 and gdi32, which the naming rule cannot place any better than
// it could place the sockets.
var user32Syms = []string{
	"RegisterClassA", "CreateWindowExA", "DefWindowProcA", "DestroyWindow",
	"PeekMessageA", "TranslateMessage", "DispatchMessageA", "ShowWindow",
	"UpdateWindow", "GetDC", "ReleaseDC", "IsWindow", "LoadCursorA",
	"GetClientRect", "FillRect", "SetWindowTextA", "GetAsyncKeyState",
	"AdjustWindowRect", "SetWindowLongPtrA", "GetWindowLongPtrA", "SetWindowPos",
	"LoadImageA",
}

var gdi32Syms = []string{
	"CreateCompatibleDC", "CreateCompatibleBitmap", "SelectObject",
	"BitBlt", "CreateSolidBrush", "DeleteObject", "DeleteDC",
	"TextOutA", "SetTextColor", "SetBkMode", "Rectangle", "Ellipse",
	"MoveToEx", "LineTo", "CreatePen", "GetStockObject",
	"GetPixel", "GetObjectA", "StretchBlt", "CreateDIBSection", "GetTextExtentPoint32A",
	"GdiFlush",
}

func (l *lowerer) winBuiltin(c *Call, name string) (Reg, bool) {
	arity := func(n int) bool {
		if len(c.Args) != n {
			l.errorAt(c, "%s takes %d argument(s), got %d", name, n, len(c.Args))
			return false
		}
		return true
	}
	args := func(n int) []Reg {
		out := make([]Reg, n)
		for i := 0; i < n; i++ {
			out[i] = l.expr(c.Args[i])
		}
		return out
	}

	switch name {
	case "win.open":
		if !arity(3) {
			return l.junk(), true
		}
		a := args(3)
		return l.winOpen(a[0], a[1], a[2]), true

	case "win.poll":
		if !arity(1) {
			return l.junk(), true
		}
		return l.winPoll(l.expr(c.Args[0])), true

	case "win.present":
		if !arity(1) {
			return l.junk(), true
		}
		l.winPresent(l.expr(c.Args[0]))
		return l.junk(), true

	case "win.close":
		if !arity(1) {
			return l.junk(), true
		}
		w := l.expr(c.Args[0])
		l.ccall("DestroyWindow", []Reg{l.field(w, winHwndAt, vInt)},
			[]vty{vInt}, vInt, true, false)
		return l.junk(), true

	case "win.rgb":
		if !arity(3) {
			return l.junk(), true
		}
		a := args(3)
		// COLORREF is 0x00BBGGRR, which is the one place Windows puts
		// blue first and the reason this is a function rather than a
		// number the caller writes out.
		v := l.arith(OpBAnd, a[0], l.constant(255))
		v = l.arith(OpBOr, v, l.arith(OpShl, l.arith(OpBAnd, a[1], l.constant(255)), l.constant(8)))
		v = l.arith(OpBOr, v, l.arith(OpShl, l.arith(OpBAnd, a[2], l.constant(255)), l.constant(16)))
		return v, true

	case "win.clear":
		if !arity(2) {
			return l.junk(), true
		}
		a := args(2)
		l.winFillRect(a[0], l.constant(0), l.constant(0),
			l.field(a[0], winWidthAt, vInt), l.field(a[0], winHeightAt, vInt), a[1])
		return l.junk(), true

	case "win.rect":
		if !arity(6) {
			return l.junk(), true
		}
		a := args(6)
		l.winFillRect(a[0], a[1], a[2], a[3], a[4], a[5])
		return l.junk(), true

	case "win.text":
		if !arity(5) {
			return l.junk(), true
		}
		a := args(5)
		l.winText(a[0], a[1], a[2], a[3], a[4])
		return l.junk(), true

	case "win.line":
		if !arity(6) {
			return l.junk(), true
		}
		a := args(6)
		l.winLine(a[0], a[1], a[2], a[3], a[4], a[5])
		return l.junk(), true

	case "win.circle":
		if !arity(5) {
			return l.junk(), true
		}
		a := args(5)
		l.winEllipse(a[0], a[1], a[2], a[3], a[4])
		return l.junk(), true

	case "win.resizable":
		if !arity(2) {
			return l.junk(), true
		}
		a := args(2)
		l.winResizable(a[0], a[1])
		return l.junk(), true

	case "win.title":
		if !arity(2) {
			return l.junk(), true
		}
		a := args(2)
		l.ccall("SetWindowTextA", []Reg{l.field(a[0], winHwndAt, vInt), a[1]},
			[]vty{vInt, vStr}, vInt, true, false)
		return l.junk(), true

	case "win.mouseX":
		if !arity(1) {
			return l.junk(), true
		}
		return l.field(l.expr(c.Args[0]), winMouseXAt, vInt), true
	case "win.mouseY":
		if !arity(1) {
			return l.junk(), true
		}
		return l.field(l.expr(c.Args[0]), winMouseYAt, vInt), true
	case "win.width":
		if !arity(1) {
			return l.junk(), true
		}
		return l.field(l.expr(c.Args[0]), winWidthAt, vInt), true
	case "win.height":
		if !arity(1) {
			return l.junk(), true
		}
		return l.field(l.expr(c.Args[0]), winHeightAt, vInt), true

	case "win.mouseDown":
		if !arity(1) {
			return l.junk(), true
		}
		return l.asBool(l.field(l.expr(c.Args[0]), winDownAt, vInt)), true
	case "win.clicked":
		if !arity(1) {
			return l.junk(), true
		}
		return l.asBool(l.field(l.expr(c.Args[0]), winClickAt, vInt)), true

	case "win.key":
		if !arity(2) {
			return l.junk(), true
		}
		a := args(2)
		// Masked rather than bounds checked: a key code is a byte and
		// anything else would read past the block.
		code := l.arith(OpBAnd, a[1], l.constant(255))
		b := l.loadByte(l.arith(OpAdd, a[0], l.constant(winKeysAt)), code)
		return l.asBool(b), true
	}
	return NoReg, false
}

// winResizable adds or removes the drag border and the maximise box.
//
// A window is fixed when it is opened, because the back buffer is one
// bitmap made once and a window whose drawing area can change out from
// under it is the harder case. Turning this on makes win.poll notice
// the new size and rebuild the buffer.
//
// SetWindowPos with FRAMECHANGED afterwards is not optional: a style
// written with SetWindowLongPtr alone is stored and not drawn, so the
// border keeps working until something else happens to recalculate it.
func (l *lowerer) winResizable(w, on Reg) {
	hwnd := l.field(w, winHwndAt, vInt)
	style := l.pick(on, l.constant(wsOverlappedWindow|wsVisible),
		l.constant(wsFixedWindow|wsVisible), vInt)

	l.ccall("SetWindowLongPtrA", []Reg{hwnd, l.constant(gwlStyle), style},
		[]vty{vInt, vInt, vInt}, vInt, false, false)
	l.ccall("SetWindowPos",
		[]Reg{hwnd, l.constant(0), l.constant(0), l.constant(0),
			l.constant(0), l.constant(0), l.constant(swpFrameChange)},
		[]vty{vInt, vInt, vInt, vInt, vInt, vInt, vInt}, vInt, true, false)
}

// asBool turns a nonzero int into a bool.
func (l *lowerer) asBool(v Reg) Reg {
	return l.compare(OpNe, v, l.constant(0))
}

// winOpen registers the class, makes the window, and builds the back
// buffer it will be drawn into.
func (l *lowerer) winOpen(title, width, height Reg) Reg {
	ret := vty{k: kInt, res: true}
	sym := l.helperFunc("winopen", []vty{vStr, vInt, vInt}, ret, func(a []Reg) {
		inst := l.ccall("GetModuleHandleA", []Reg{l.constant(0)},
			[]vty{vInt}, vInt, false, false)

		// The class is registered every time. A second RegisterClassA
		// with the same name fails, and that failure is fine: the class
		// it wanted already exists, which is the state it was trying to
		// reach. Checking would mean a global.
		wc := l.allocObj(l.constant(wndClassSize), tagBytes)
		l.zeroBlock(wc, wndClassSize)

		proc := l.ccall("GetProcAddress",
			[]Reg{l.ccall("GetModuleHandleA", []Reg{l.strLit("user32.dll")},
				[]vty{vStr}, vInt, false, false), l.strLit("DefWindowProcA")},
			[]vty{vInt, vStr}, vInt, false, false)
		l.emit(Instr{Op: OpStoreMem, A: wc, B: proc, Imm: wcWndProcAt})
		l.emit(Instr{Op: OpStoreMem, A: wc, B: inst, Imm: wcInstanceAt})

		cursor := l.ccall("LoadCursorA", []Reg{l.constant(0), l.constant(idcArrow)},
			[]vty{vInt, vInt}, vInt, false, false)
		l.emit(Instr{Op: OpStoreMem, A: wc, B: cursor, Imm: wcCursorAt})

		brush := l.ccall("GetStockObject", []Reg{l.constant(whiteBrush)},
			[]vty{vInt}, vInt, false, false)
		l.emit(Instr{Op: OpStoreMem, A: wc, B: brush, Imm: wcBackgroundAt})

		cls := l.strLit(wndClassNameConst)
		l.emit(Instr{Op: OpStoreMem, A: wc, B: cls, Imm: wcClassNameAt})
		l.ccall("RegisterClassA", []Reg{wc}, []vty{vInt}, vInt, true, false)

		// The size asked for is the drawing area, not the window.
		// CreateWindowEx measures the whole window including the title
		// bar and the border, so passing the wanted size straight
		// through makes the client area smaller than the back buffer
		// and clips the bottom and right of everything drawn.
		frame := l.allocObj(l.constant(16), tagBytes)
		l.putInt32(frame, 0, l.constant(0))
		l.putInt32(frame, 4, l.constant(0))
		l.putInt32(frame, 8, a[1])
		l.putInt32(frame, 12, a[2])
		l.ccall("AdjustWindowRect",
			[]Reg{frame, l.constant(wsFixedWindow), l.constant(0)},
			[]vty{vInt, vInt, vInt}, vInt, true, false)
		outerW := l.arith(OpSub, l.getInt32(frame, 8), l.getInt32(frame, 0))
		outerH := l.arith(OpSub, l.getInt32(frame, 12), l.getInt32(frame, 4))

		hwnd := l.ccall("CreateWindowExA",
			[]Reg{
				l.constant(0), cls, a[0],
				l.constant(wsFixedWindow | wsVisible),
				l.constant(cwUseDefault), l.constant(cwUseDefault),
				outerW, outerH,
				l.constant(0), l.constant(0), inst, l.constant(0),
			},
			[]vty{vInt, vStr, vStr, vInt, vInt, vInt, vInt, vInt, vInt, vInt, vInt, vInt},
			vInt, false, false)

		bad := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, hwnd, l.constant(0)),
			Dst: NoReg, Imm: bad})

		w := l.winBlock()
		l.emit(Instr{Op: OpStoreMem, A: w, B: hwnd, Imm: winHwndAt})
		l.emit(Instr{Op: OpStoreMem, A: w, B: a[1], Imm: winWidthAt})
		l.emit(Instr{Op: OpStoreMem, A: w, B: a[2], Imm: winHeightAt})

		dc := l.ccall("GetDC", []Reg{hwnd}, []vty{vInt}, vInt, false, false)
		l.emit(Instr{Op: OpStoreMem, A: w, B: dc, Imm: winDCAt})

		mem := l.ccall("CreateCompatibleDC", []Reg{dc}, []vty{vInt}, vInt, false, false)
		l.emit(Instr{Op: OpStoreMem, A: w, B: mem, Imm: winMemDCAt})

		l.dibBuffer(w, mem, a[1], a[2])

		l.ccall("ShowWindow", []Reg{hwnd, l.constant(swShow)},
			[]vty{vInt, vInt}, vInt, true, false)
		l.ccall("UpdateWindow", []Reg{hwnd}, []vty{vInt}, vInt, true, false)

		l.emit(Instr{Op: OpRet, A: l.resOk(w, ret), Dst: NoReg})

		l.mark(bad)
		l.emit(Instr{Op: OpRet,
			A:   l.resFail(l.concatAll(l.strLit("cannot open a window: "), l.sysMessage()), ret),
			Dst: NoReg})
	})
	return l.callHelper(sym, []Reg{title, width, height}, []vty{vStr, vInt, vInt}, ret)
}

// zeroBlock writes n zero bytes. Win32 structs have fields nothing here
// sets, and a stale word in one of them is read as a real value.
func (l *lowerer) zeroBlock(p Reg, n int64) {
	i := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: i})
	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)
	cur := l.load(i, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, cur, l.constant(n)), Dst: NoReg, Imm: done})
	l.storeByte(p, cur, l.constant(0))
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, cur, l.constant(1)), Dst: NoReg, Imm: i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(done)
}

// winPoll drains the message queue and reports whether the window is
// still open.
func (l *lowerer) winPoll(w Reg) Reg {
	sym := l.helperFunc("winpoll", []vty{vInt}, vBool, func(a []Reg) {
		win := a[0]
		msg := l.allocObj(l.constant(msgSize), tagBytes)

		// The click edge is per frame, so it is cleared here and set by
		// a button-down seen below. A caller polling twice without
		// drawing would otherwise see the same click again.
		l.emit(Instr{Op: OpStoreMem, A: win, B: l.constant(0), Imm: winClickAt})

		top := l.newLabel()
		drained := l.newLabel()
		l.mark(top)
		got := l.ccall("PeekMessageA",
			[]Reg{msg, l.constant(0), l.constant(0), l.constant(0), l.constant(pmRemove)},
			[]vty{vInt, vInt, vInt, vInt, vInt}, vInt, true, false)
		l.emit(Instr{Op: OpJumpNot, A: l.asBool(got), Dst: NoReg, Imm: drained})

		m := l.field(msg, msgMessageAt, vInt)
		m = l.arith(OpBAnd, m, l.constant(0xffffffff))
		wp := l.field(msg, msgWParamAt, vInt)
		lp := l.field(msg, msgLParamAt, vInt)

		quit := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, m, l.constant(wmQuit)),
			Dst: NoReg, Imm: quit})

		// Mouse position rides in lParam as two 16-bit halves. The
		// low half is signed, so a pointer dragged off the left edge
		// reports a negative x rather than 65000.
		notMove := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, m, l.constant(wmMouseMove)),
			Dst: NoReg, Imm: notMove})
		l.emit(Instr{Op: OpStoreMem, A: win, B: l.signed16(lp), Imm: winMouseXAt})
		l.emit(Instr{Op: OpStoreMem, A: win,
			B: l.signed16(l.arith(OpShr, lp, l.constant(16))), Imm: winMouseYAt})
		l.mark(notMove)

		notDown := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, m, l.constant(wmLButtonDown)),
			Dst: NoReg, Imm: notDown})
		l.emit(Instr{Op: OpStoreMem, A: win, B: l.constant(1), Imm: winDownAt})
		l.emit(Instr{Op: OpStoreMem, A: win, B: l.constant(1), Imm: winClickAt})
		l.mark(notDown)

		notUp := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, m, l.constant(wmLButtonUp)),
			Dst: NoReg, Imm: notUp})
		l.emit(Instr{Op: OpStoreMem, A: win, B: l.constant(0), Imm: winDownAt})
		l.mark(notUp)

		keys := l.arith(OpAdd, win, l.constant(winKeysAt))
		code := l.arith(OpBAnd, wp, l.constant(255))

		notKeyDown := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, m, l.constant(wmKeyDown)),
			Dst: NoReg, Imm: notKeyDown})
		l.storeByte(keys, code, l.constant(1))
		l.mark(notKeyDown)

		notKeyUp := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpEq, m, l.constant(wmKeyUp)),
			Dst: NoReg, Imm: notKeyUp})
		l.storeByte(keys, code, l.constant(0))
		l.mark(notKeyUp)

		l.ccall("TranslateMessage", []Reg{msg}, []vty{vInt}, vInt, true, false)
		l.ccall("DispatchMessageA", []Reg{msg}, []vty{vInt}, vInt, false, false)
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

		l.mark(drained)
		// A window closed by its own title bar is gone by now, and
		// IsWindow is how that is noticed without a window procedure of
		// our own to catch WM_DESTROY in.
		alive := l.ccall("IsWindow", []Reg{l.field(win, winHwndAt, vInt)},
			[]vty{vInt}, vInt, true, false)
		l.emit(Instr{Op: OpJumpNot, A: l.asBool(alive), Dst: NoReg, Imm: quit})

		// If the drawing area has changed, rebuild the back buffer to
		// match. Without this a resizable window draws through a bitmap
		// the wrong size and the picture is clipped or padded for the
		// rest of the run.
		rect := l.allocObj(l.constant(16), tagBytes)
		l.ccall("GetClientRect", []Reg{l.field(win, winHwndAt, vInt), rect},
			[]vty{vInt, vInt}, vInt, true, false)
		cw := l.getInt32(rect, 8)
		ch := l.getInt32(rect, 12)

		same := l.newLabel()
		changed := l.logicalOr(
			l.compare(OpNe, cw, l.field(win, winWidthAt, vInt)),
			l.compare(OpNe, ch, l.field(win, winHeightAt, vInt)))
		l.emit(Instr{Op: OpJumpNot, A: changed, Dst: NoReg, Imm: same})
		// A zero-sized client area is a minimised window. Rebuilding at
		// that size would give a bitmap nothing can be drawn into and
		// the picture would not come back when it is restored.
		l.emit(Instr{Op: OpJumpNot, A: l.logicalAnd(
			l.compare(OpGt, cw, l.constant(0)),
			l.compare(OpGt, ch, l.constant(0))), Dst: NoReg, Imm: same})

		// Out of the DC before it is deleted; a bitmap still selected
		// into one is not freed.
		old := l.dibBuffer(win, l.field(win, winMemDCAt, vInt), cw, ch)
		l.ccall("DeleteObject", []Reg{old}, []vty{vInt}, vInt, true, false)
		l.emit(Instr{Op: OpStoreMem, A: win, B: cw, Imm: winWidthAt})
		l.emit(Instr{Op: OpStoreMem, A: win, B: ch, Imm: winHeightAt})

		l.mark(same)
		l.emit(Instr{Op: OpRet, A: l.constant(1), Dst: NoReg})

		l.mark(quit)
		l.emit(Instr{Op: OpStoreMem, A: win, B: l.constant(1), Imm: winClosedAt})
		l.emit(Instr{Op: OpRet, A: l.constant(0), Dst: NoReg})
	})
	return l.callHelper(sym, []Reg{w}, []vty{vInt}, vBool)
}

// signed16 sign-extends the low half of a word.
func (l *lowerer) signed16(v Reg) Reg {
	low := l.arith(OpBAnd, v, l.constant(0xffff))
	big := l.compare(OpGt, low, l.constant(0x7fff))
	return l.pick(big, l.arith(OpSub, low, l.constant(0x10000)), low, vInt)
}

func (l *lowerer) winPresent(w Reg) {
	l.ccall("BitBlt",
		[]Reg{
			l.field(w, winDCAt, vInt), l.constant(0), l.constant(0),
			l.field(w, winWidthAt, vInt), l.field(w, winHeightAt, vInt),
			l.field(w, winMemDCAt, vInt), l.constant(0), l.constant(0),
			l.constant(srcCopy),
		},
		[]vty{vInt, vInt, vInt, vInt, vInt, vInt, vInt, vInt, vInt},
		vInt, true, false)
}

// winFillRect paints a solid rectangle. FillRect wants a RECT, which is
// four 32-bit values, and it excludes the right and bottom edges.
func (l *lowerer) winFillRect(w, x, y, width, height, color Reg) {
	rect := l.allocObj(l.constant(16), tagBytes)
	l.putInt32(rect, 0, x)
	l.putInt32(rect, 4, y)
	l.putInt32(rect, 8, l.arith(OpAdd, x, width))
	l.putInt32(rect, 12, l.arith(OpAdd, y, height))

	brush := l.ccall("CreateSolidBrush", []Reg{color}, []vty{vInt}, vInt, false, false)
	l.ccall("FillRect", []Reg{l.field(w, winMemDCAt, vInt), rect, brush},
		[]vty{vInt, vInt, vInt}, vInt, true, false)
	l.ccall("DeleteObject", []Reg{brush}, []vty{vInt}, vInt, true, false)
}

// getInt32 reads a signed four-byte value at an offset. AdjustWindowRect
// puts negative numbers in left and top - the frame grows outwards from
// the client area - so this has to sign-extend or the width comes back
// four billion short.
func (l *lowerer) getInt32(p Reg, off int64) Reg {
	v := l.constant(0)
	for k := int64(0); k < 4; k++ {
		b := l.loadByte(p, l.constant(off+k))
		v = l.arith(OpBOr, v, l.arith(OpShl, b, l.constant(8*k)))
	}
	big := l.compare(OpGt, v, l.constant(0x7fffffff))
	return l.pick(big, l.arith(OpSub, v, l.constant(0x100000000)), v, vInt)
}

// putInt32 writes a four-byte little-endian value at an offset.
func (l *lowerer) putInt32(p Reg, off int64, v Reg) {
	for k := int64(0); k < 4; k++ {
		l.storeByte(p, l.constant(off+k),
			l.arith(OpBAnd, l.arith(OpShr, v, l.constant(8*k)), l.constant(255)))
	}
}

func (l *lowerer) winText(w, x, y, s, color Reg) {
	dc := l.field(w, winMemDCAt, vInt)
	l.ccall("SetTextColor", []Reg{dc, color}, []vty{vInt, vInt}, vInt, true, false)
	l.ccall("TextOutA", []Reg{dc, x, y, s, l.strLen(s)},
		[]vty{vInt, vInt, vInt, vStr, vInt}, vInt, true, false)
}

func (l *lowerer) winLine(w, x1, y1, x2, y2, color Reg) {
	dc := l.field(w, winMemDCAt, vInt)
	pen := l.ccall("CreatePen", []Reg{l.constant(0), l.constant(1), color},
		[]vty{vInt, vInt, vInt}, vInt, false, false)
	old := l.ccall("SelectObject", []Reg{dc, pen}, []vty{vInt, vInt}, vInt, false, false)
	l.ccall("MoveToEx", []Reg{dc, x1, y1, l.constant(0)},
		[]vty{vInt, vInt, vInt, vInt}, vInt, true, false)
	l.ccall("LineTo", []Reg{dc, x2, y2}, []vty{vInt, vInt, vInt}, vInt, true, false)
	l.ccall("SelectObject", []Reg{dc, old}, []vty{vInt, vInt}, vInt, false, false)
	l.ccall("DeleteObject", []Reg{pen}, []vty{vInt}, vInt, true, false)
}

// winEllipse draws a filled circle of radius r centred on x, y.
func (l *lowerer) winEllipse(w, x, y, r, color Reg) {
	dc := l.field(w, winMemDCAt, vInt)
	brush := l.ccall("CreateSolidBrush", []Reg{color}, []vty{vInt}, vInt, false, false)
	pen := l.ccall("CreatePen", []Reg{l.constant(0), l.constant(1), color},
		[]vty{vInt, vInt, vInt}, vInt, false, false)
	oldB := l.ccall("SelectObject", []Reg{dc, brush}, []vty{vInt, vInt}, vInt, false, false)
	oldP := l.ccall("SelectObject", []Reg{dc, pen}, []vty{vInt, vInt}, vInt, false, false)

	l.ccall("Ellipse",
		[]Reg{dc, l.arith(OpSub, x, r), l.arith(OpSub, y, r),
			l.arith(OpAdd, x, r), l.arith(OpAdd, y, r)},
		[]vty{vInt, vInt, vInt, vInt, vInt}, vInt, true, false)

	l.ccall("SelectObject", []Reg{dc, oldB}, []vty{vInt, vInt}, vInt, false, false)
	l.ccall("SelectObject", []Reg{dc, oldP}, []vty{vInt, vInt}, vInt, false, false)
	l.ccall("DeleteObject", []Reg{brush}, []vty{vInt}, vInt, true, false)
	l.ccall("DeleteObject", []Reg{pen}, []vty{vInt}, vInt, true, false)
}

// winBlock allocates a zeroed window block outside the collector.
func (l *lowerer) winBlock() Reg {
	w := l.ccall("calloc", []Reg{l.constant(1), l.constant(winBlockLen)},
		[]vty{vInt, vInt}, vInt, false, false)
	l.regTy[w] = vInt
	return w
}
