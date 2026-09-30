package main

// Images, text measurement, off-screen canvases and sound.
//
// Images are BMP files, loaded by Windows itself into a device-
// independent bitmap and drawn into a window's back buffer with BitBlt,
// StretchBlt, or TransparentBlt for a sprite whose background is one
// colour. An image is a handle, an int, like a window.
//
// A canvas is a window block with no window: the same back buffer, so
// every drawing function works on it, but nothing on screen. It is for
// drawing off-screen and reading the result back with win.pixel, which
// is also what lets the drawing functions be tested with no display.
//
// Sound is PlaySound: one WAV at a time, played in the background.

const (
	imageBitmap      = 0
	lrLoadFromFile   = 0x0010
	lrCreateDIB      = 0x2000
	bitmapStructSize = 32 // BITMAP on x64
	bmWidthAt        = 4
	bmHeightAt       = 8

	sndAsync     = 0x0001
	sndNoDefault = 0x0002
	sndLoop      = 0x0008
	sndFilename  = 0x00020000
)

var msimg32Syms = []string{"TransparentBlt", "AlphaBlend"}
var winmmSyms = []string{"PlaySoundA"}

func (l *lowerer) mediaBuiltin(c *Call, name string) (Reg, bool) {
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
	i3 := []vty{vInt, vInt, vInt}

	switch name {
	case "win.canvas":
		if !arity(2) {
			return l.junk(), true
		}
		a := args(2)
		return l.winCanvas(a[0], a[1]), true

	case "win.pixels":
		// GDI batches its drawing, so anything drawn with win.line and
		// friends is flushed into the bits before they are handed over.
		if !arity(1) {
			return l.junk(), true
		}
		w := l.expr(c.Args[0])
		l.ccall("GdiFlush", nil, nil, vInt, true, false)
		p := l.field(w, winBitsAt, vInt)
		return l.retype(p, vPtr("*u32")), true

	case "win.pixel":
		// GetPixel answers CLR_INVALID, all ones, outside the surface;
		// read as a C int that is -1.
		if !arity(3) {
			return l.junk(), true
		}
		a := args(3)
		return l.ccall("GetPixel", []Reg{l.field(a[0], winMemDCAt, vInt), a[1], a[2]},
			i3, vInt, true, false), true

	case "__imageFromBMP":
		// A .bmp through Windows' own loader, or 0.
		if !arity(1) {
			return l.junk(), true
		}
		path := l.expr(c.Args[0])
		h := l.ccall("LoadImageA",
			[]Reg{l.constant(0), path, l.constant(imageBitmap), l.constant(0), l.constant(0),
				l.constant(lrLoadFromFile | lrCreateDIB)},
			[]vty{vInt, vStr, vInt, vInt, vInt, vInt}, vInt, false, false)
		out := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: out})
		bad := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, h, l.constant(0)), Dst: NoReg, Imm: bad})
		l.emit(Instr{Op: OpStore, A: l.imageBlock(h, l.bitmapDim(h, bmWidthAt), l.bitmapDim(h, bmHeightAt), 0),
			Dst: NoReg, Imm: out})
		l.mark(bad)
		return l.load(out, vInt), true

	case "__imageFromPixels":
		// Decoded pixels, as prelude_png.go lays them out, into a
		// 32-bit bitmap that is drawn with its alpha.
		if !arity(1) {
			return l.junk(), true
		}
		px := l.bytesArg(c, 0)
		w := l.loadWidth(px, memU32)
		h := l.loadWidth(l.arith(OpAdd, px, l.constant(4)), memU32)
		bits := l.ptrPair()
		bmp := l.ccall("CreateDIBSection",
			[]Reg{l.constant(0), l.bitmapInfo32(w, h), l.constant(0), bits, l.constant(0), l.constant(0)},
			[]vty{vInt, vInt, vInt, vInt, vInt, vInt}, vInt, false, false)
		out := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: out})
		bad := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, bmp, l.constant(0)), Dst: NoReg, Imm: bad})
		l.ccall("memmove", []Reg{l.loadPtr(bits), l.arith(OpAdd, px, l.constant(8)),
			l.arith(OpMul, l.arith(OpMul, w, h), l.constant(4))},
			[]vty{vInt, vInt, vInt}, vInt, false, false)
		l.emit(Instr{Op: OpStore, A: l.imageBlock(bmp, w, h, 1), Dst: NoReg, Imm: out})
		l.mark(bad)
		return l.load(out, vInt), true

	case "win.imageWidth", "win.imageHeight":
		if !arity(1) {
			return l.junk(), true
		}
		img := l.expr(c.Args[0])
		off := int64(imgWidthAt)
		if name == "win.imageHeight" {
			off = imgHeightAt
		}
		return l.field(img, off, vInt), true

	case "win.freeImage":
		if !arity(1) {
			return l.junk(), true
		}
		img := l.expr(c.Args[0])
		l.ccall("DeleteObject", []Reg{l.field(img, imgBitmapAt, vInt)}, []vty{vInt}, vInt, true, false)
		l.ccall("free", []Reg{img}, []vty{vInt}, vVoid, false, false)
		return l.void(), true

	case "win.draw":
		if !arity(4) {
			return l.junk(), true
		}
		a := args(4)
		l.drawImage(a[0], a[1], a[2], a[3], NoReg, NoReg, NoReg)
		return l.void(), true

	case "win.drawScaled":
		if !arity(6) {
			return l.junk(), true
		}
		a := args(6)
		l.drawImage(a[0], a[1], a[2], a[3], a[4], a[5], NoReg)
		return l.void(), true

	case "win.drawKeyed":
		if !arity(5) {
			return l.junk(), true
		}
		a := args(5)
		l.drawImage(a[0], a[1], a[2], a[3], NoReg, NoReg, a[4])
		return l.void(), true

	case "win.textWidth", "win.textHeight":
		// GetTextExtentPoint32A fills a SIZE: cx then cy, four bytes
		// each. The height is measured on a string with an ascender
		// and a descender, so it is the full line height.
		if name == "win.textHeight" && !arity(1) || name == "win.textWidth" && !arity(2) {
			return l.junk(), true
		}
		w := l.expr(c.Args[0])
		s := l.strLit("Ag")
		if name == "win.textWidth" {
			s = l.expr(c.Args[1])
		}
		size := l.ptrPair()
		l.ccall("GetTextExtentPoint32A", []Reg{l.field(w, winMemDCAt, vInt), s, l.strLen(s), size},
			[]vty{vInt, vStr, vInt, vInt}, vInt, true, false)
		off := int64(0)
		if name == "win.textHeight" {
			off = 4
		}
		return l.loadWidth(l.arith(OpAdd, size, l.constant(off)), memI32), true

	case "sound.play", "sound.loop":
		if !arity(1) {
			return l.junk(), true
		}
		flags := int64(sndAsync | sndNoDefault | sndFilename)
		if name == "sound.loop" {
			flags |= sndLoop
		}
		// An asynchronous PlaySound can report success before it has
		// opened the file, so a missing one is caught here first.
		path := l.expr(c.Args[0])
		out := l.temp(vBool)
		l.emit(Instr{Op: OpStore, A: l.boolConst(false), Dst: NoReg, Imm: out})
		missing := l.newLabel()
		attrs := l.ccall("GetFileAttributesA", []Reg{path}, []vty{vStr}, vInt, true, false)
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, attrs, l.constant(-1)), Dst: NoReg, Imm: missing})
		ok := l.ccall("PlaySoundA", []Reg{path, l.constant(0), l.constant(flags)},
			[]vty{vStr, vInt, vInt}, vInt, true, false)
		l.emit(Instr{Op: OpStore, A: l.compare(OpNe, ok, l.constant(0)), Dst: NoReg, Imm: out})
		l.mark(missing)
		return l.load(out, vBool), true

	case "sound.stop":
		if !arity(0) {
			return l.junk(), true
		}
		l.ccall("PlaySoundA", []Reg{l.constant(0), l.constant(0), l.constant(0)},
			[]vty{vInt, vInt, vInt}, vInt, true, false)
		return l.void(), true
	}
	return NoReg, false
}

// ptrPair is two words of scratch for an out-parameter.
func (l *lowerer) ptrPair() Reg {
	p := l.allocObj(l.constant(2*wordSize), tagBytes)
	l.emit(Instr{Op: OpStoreMem, A: p, B: l.constant(0), Imm: 0})
	l.emit(Instr{Op: OpStoreMem, A: p, B: l.constant(0), Imm: wordSize})
	return p
}

// An image is a block outside the collector, for the same reason a
// window is: the handle is an int. It holds the bitmap, its size, and
// whether it carries alpha, which decides how it is drawn.
const (
	imgBitmapAt = 0
	imgWidthAt  = 8
	imgHeightAt = 16
	imgAlphaAt  = 24
	imgBlockLen = 32

	// BLENDFUNCTION, passed by value as one DWORD: AC_SRC_OVER, no
	// flags, full constant alpha, AC_SRC_ALPHA.
	blendPerPixel = 0x01FF0000
)

func (l *lowerer) imageBlock(bmp, w, h Reg, alpha int64) Reg {
	b := l.ccall("calloc", []Reg{l.constant(1), l.constant(imgBlockLen)},
		[]vty{vInt, vInt}, vInt, false, false)
	l.emit(Instr{Op: OpStoreMem, A: b, B: bmp, Imm: imgBitmapAt})
	l.emit(Instr{Op: OpStoreMem, A: b, B: w, Imm: imgWidthAt})
	l.emit(Instr{Op: OpStoreMem, A: b, B: h, Imm: imgHeightAt})
	l.emit(Instr{Op: OpStoreMem, A: b, B: l.constant(alpha), Imm: imgAlphaAt})
	return b
}

// bitmapDim reads one dimension of a bitmap out of GetObject's BITMAP.
func (l *lowerer) bitmapDim(bmp Reg, off int64) Reg {
	buf := l.allocObj(l.constant(bitmapStructSize), tagBytes)
	l.ccall("GetObjectA", []Reg{bmp, l.constant(bitmapStructSize), buf},
		[]vty{vInt, vInt, vInt}, vInt, true, false)
	return l.loadWidth(l.arith(OpAdd, buf, l.constant(off)), memI32)
}

// bitmapInfo32 is a BITMAPINFOHEADER for a 32-bit image, rows top down.
func (l *lowerer) bitmapInfo32(width, height Reg) Reg {
	bmi := l.allocObj(l.constant(48), tagBytes)
	l.ccall("memset", []Reg{bmi, l.constant(0), l.constant(48)}, []vty{vInt, vInt, vInt}, vInt, false, false)
	l.storeWidth(bmi, l.constant(40), memI32)
	l.storeWidth(l.arith(OpAdd, bmi, l.constant(4)), width, memI32)
	l.storeWidth(l.arith(OpAdd, bmi, l.constant(8)), l.arith(OpSub, l.constant(0), height), memI32)
	l.storeWidth(l.arith(OpAdd, bmi, l.constant(12)), l.constant(1), memU16)
	l.storeWidth(l.arith(OpAdd, bmi, l.constant(14)), l.constant(32), memU16)
	return bmi
}

// drawImage copies an image into a window's back buffer at x, y: at its
// own size, stretched to dw by dh when those are given, or with every
// pixel of the key colour left out when key is. An image with alpha is
// blended instead, unless it is being colour-keyed.
func (l *lowerer) drawImage(w, img, x, y, dw, dh, key Reg) {
	dst := l.field(w, winMemDCAt, vInt)
	src := l.ccall("CreateCompatibleDC", []Reg{dst}, []vty{vInt}, vInt, false, false)
	old := l.ccall("SelectObject", []Reg{src, l.field(img, imgBitmapAt, vInt)},
		[]vty{vInt, vInt}, vInt, false, false)
	iw := l.field(img, imgWidthAt, vInt)
	ih := l.field(img, imgHeightAt, vInt)
	if dw == NoReg {
		dw, dh = iw, ih
	}
	nine := []vty{vInt, vInt, vInt, vInt, vInt, vInt, vInt, vInt, vInt}
	eleven := append(append([]vty{}, nine...), vInt, vInt)
	switch {
	case key != NoReg:
		l.ccall("TransparentBlt",
			[]Reg{dst, x, y, iw, ih, src, l.constant(0), l.constant(0), iw, ih, key},
			eleven, vInt, true, false)
	default:
		plain, done := l.newLabel(), l.newLabel()
		alpha := l.compare(OpNe, l.field(img, imgAlphaAt, vInt), l.constant(0))
		l.emit(Instr{Op: OpJumpNot, A: alpha, Dst: NoReg, Imm: plain})
		l.ccall("AlphaBlend",
			[]Reg{dst, x, y, dw, dh, src, l.constant(0), l.constant(0), iw, ih, l.constant(blendPerPixel)},
			eleven, vInt, true, false)
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
		l.mark(plain)
		l.ccall("StretchBlt",
			[]Reg{dst, x, y, dw, dh, src, l.constant(0), l.constant(0), iw, ih, l.constant(srcCopy)},
			eleven, vInt, true, false)
		l.mark(done)
	}
	l.ccall("SelectObject", []Reg{src, old}, []vty{vInt, vInt}, vInt, false, false)
	l.ccall("DeleteDC", []Reg{src}, []vty{vInt}, vInt, true, false)
}

// winCanvas makes a window block with a back buffer and no window. The
// buffer is a 32-bit DIB section rather than a bitmap compatible with
// the screen, which needs no display to exist.
func (l *lowerer) winCanvas(width, height Reg) Reg {
	w := l.winBlock()
	l.emit(Instr{Op: OpStoreMem, A: w, B: width, Imm: winWidthAt})
	l.emit(Instr{Op: OpStoreMem, A: w, B: height, Imm: winHeightAt})

	mem := l.ccall("CreateCompatibleDC", []Reg{l.constant(0)}, []vty{vInt}, vInt, false, false)
	l.emit(Instr{Op: OpStoreMem, A: w, B: mem, Imm: winMemDCAt})

	l.dibBuffer(w, mem, width, height)
	// A closed flag, so win.poll on a canvas answers false at once.
	l.emit(Instr{Op: OpStoreMem, A: w, B: l.constant(1), Imm: winClosedAt})
	return w
}

// dibBuffer makes a window's or a canvas's back buffer: a 32-bit
// top-down DIB section selected into its memory DC. GDI draws into it
// like any bitmap, and the pixels are also plain memory - one u32 a
// pixel, 0x00RRGGBB, rows top to bottom - which win.pixels hands to the
// program. It records the bitmap and the pixel address in the block,
// and returns the bitmap that was selected before, for the caller to
// delete when this replaces one.
func (l *lowerer) dibBuffer(w, mem, width, height Reg) Reg {
	bits := l.ptrPair()
	bmp := l.ccall("CreateDIBSection",
		[]Reg{mem, l.bitmapInfo32(width, height), l.constant(0), bits, l.constant(0), l.constant(0)},
		[]vty{vInt, vInt, vInt, vInt, vInt, vInt}, vInt, false, false)
	l.emit(Instr{Op: OpStoreMem, A: w, B: bmp, Imm: winBitmapAt})
	l.emit(Instr{Op: OpStoreMem, A: w, B: l.loadPtr(bits), Imm: winBitsAt})
	old := l.ccall("SelectObject", []Reg{mem, bmp}, []vty{vInt, vInt}, vInt, false, false)
	l.ccall("SetBkMode", []Reg{mem, l.constant(transparentBk)}, []vty{vInt, vInt}, vInt, true, false)
	return old
}
