package main

// HTTPS, through WinHTTP.
//
// The socket layer in net.go speaks TCP and nothing else, so it can
// reach a plain HTTP server and cannot reach anything real - almost
// every host worth fetching from is HTTPS only, and TLS is not
// something to write by hand.
//
// Windows already has one. WinHTTP does the handshake, the certificate
// chain, redirects, chunked transfer encoding and content decoding, so
// this is six calls and a read loop rather than a TLS implementation.
//
// It is used for http:// as well as https://, not only for the secure
// case. Two transports for one library function would mean two sets of
// behaviour around redirects and chunked bodies, and the differences
// would surface as a bug somewhere far from here.
//
// The one real friction: WinHTTP is a wide API. Every string handed to
// it is UTF-16, and every string here is NUL-terminated bytes, so they
// go through MultiByteToWideChar on the way in.

const (
	// WinHttpOpen
	winhttpAccessDefaultProxy = 0
	// WinHttpOpenRequest
	winhttpFlagSecure = 0x00800000
	// WinHttpQueryHeaders
	winhttpQueryStatusCode = 19
	winhttpQueryFlagNumber = 0x20000000

	cpUTF8 = 65001

	// The response buffer starts here and doubles as needed.
	httpBufStart = 1 << 16
)

var winhttpSyms = []string{
	"WinHttpOpen", "WinHttpConnect", "WinHttpOpenRequest",
	"WinHttpSendRequest", "WinHttpReceiveResponse", "WinHttpQueryDataAvailable",
	"WinHttpReadData", "WinHttpQueryHeaders", "WinHttpCloseHandle",
}

// toWide converts a byte string to the UTF-16 WinHTTP wants.
//
// Two calls: the first with a null buffer asks how many characters it
// will need, the second does it. Guessing the size instead works until
// the first non-ASCII host name.
func (l *lowerer) toWide(s Reg) Reg {
	n := l.ccall("MultiByteToWideChar",
		[]Reg{l.constant(cpUTF8), l.constant(0), s, l.constant(-1),
			l.constant(0), l.constant(0)},
		[]vty{vInt, vInt, vStr, vInt, vInt, vInt}, vInt, true, false)

	// Two bytes per character, and n already counts the terminator.
	buf := l.allocObj(l.arith(OpMul, n, l.constant(2)), tagBytes)
	l.ccall("MultiByteToWideChar",
		[]Reg{l.constant(cpUTF8), l.constant(0), s, l.constant(-1), buf, n},
		[]vty{vInt, vInt, vStr, vInt, vInt, vInt}, vInt, true, false)
	return buf
}

// winhttpFetch is the whole request, as one helper function so the
// failures can return early and the handles can be closed on the way.
//
// Signature: host, port, path, secure, method, body -> the response
// body, or a failure.
func (l *lowerer) winhttpFetch(args []Reg) Reg {
	ret := vty{k: kStr, res: true}
	sym := l.helperFunc("winhttpfetch",
		[]vty{vStr, vInt, vStr, vBool, vStr, vStr}, ret, func(a []Reg) {
			host, port, path := a[0], a[1], a[2]
			secure, method, body := a[3], a[4], a[5]

			fail := func(what string) {
				l.emit(Instr{Op: OpRet,
					A:   l.resFail(l.concatAll(l.strLit(what+": "), l.sysMessage()), ret),
					Dst: NoReg})
			}

			session := l.ccall("WinHttpOpen",
				[]Reg{l.toWide(l.strLit("scandiacus")),
					l.constant(winhttpAccessDefaultProxy),
					l.constant(0), l.constant(0), l.constant(0)},
				[]vty{vInt, vInt, vInt, vInt, vInt}, vInt, false, false)
			okSession := l.newLabel()
			l.emit(Instr{Op: OpJumpIf, A: l.asBool(session), Dst: NoReg, Imm: okSession})
			fail("cannot start WinHTTP")
			l.mark(okSession)

			conn := l.ccall("WinHttpConnect",
				[]Reg{session, l.toWide(host), port, l.constant(0)},
				[]vty{vInt, vInt, vInt, vInt}, vInt, false, false)
			okConn := l.newLabel()
			l.emit(Instr{Op: OpJumpIf, A: l.asBool(conn), Dst: NoReg, Imm: okConn})
			fail("cannot connect")
			l.mark(okConn)

			flags := l.pick(secure, l.constant(winhttpFlagSecure), l.constant(0), vInt)
			req := l.ccall("WinHttpOpenRequest",
				[]Reg{conn, l.toWide(method), l.toWide(path),
					l.constant(0), l.constant(0), l.constant(0), flags},
				[]vty{vInt, vInt, vInt, vInt, vInt, vInt, vInt}, vInt, false, false)
			okReq := l.newLabel()
			l.emit(Instr{Op: OpJumpIf, A: l.asBool(req), Dst: NoReg, Imm: okReq})
			fail("cannot open the request")
			l.mark(okReq)

			// A body means a content type, or servers reject it.
			//
			// No branch around this. pick already chooses, and jumping
			// over the code that computes a register leaves it holding
			// whatever was in that stack slot before - which is what
			// the first version of this did, and it segfaulted on the
			// first GET.
			bodyLen := l.strLen(body)
			hasBody := l.compare(OpGt, bodyLen, l.constant(0))
			ct := l.toWide(l.strLit("Content-Type: application/x-www-form-urlencoded\r\n"))
			headers := l.pick(hasBody, ct, l.constant(0), vInt)
			headerLen := l.pick(hasBody, l.constant(-1), l.constant(0), vInt)

			sent := l.blockingCall("WinHttpSendRequest",
				[]Reg{req, headers, headerLen, body, bodyLen, bodyLen, l.constant(0)},
				[]vty{vInt, vInt, vInt, vStr, vInt, vInt, vInt}, vInt, true)
			okSent := l.newLabel()
			l.emit(Instr{Op: OpJumpIf, A: l.asBool(sent), Dst: NoReg, Imm: okSent})
			fail("cannot send the request")
			l.mark(okSent)

			got := l.blockingCall("WinHttpReceiveResponse", []Reg{req, l.constant(0)},
				[]vty{vInt, vInt}, vInt, true)
			okGot := l.newLabel()
			l.emit(Instr{Op: OpJumpIf, A: l.asBool(got), Dst: NoReg, Imm: okGot})
			fail("no response")
			l.mark(okGot)

			// The status code, so 404 is a failure with a reason rather
			// than a page of HTML nobody asked for.
			codeBuf := l.allocObj(l.constant(4), tagBytes)
			sizeBuf := l.allocObj(l.constant(4), tagBytes)
			l.putInt32(sizeBuf, 0, l.constant(4))
			l.ccall("WinHttpQueryHeaders",
				[]Reg{req, l.constant(winhttpQueryStatusCode | winhttpQueryFlagNumber),
					l.constant(0), codeBuf, sizeBuf, l.constant(0)},
				[]vty{vInt, vInt, vInt, vInt, vInt, vInt}, vInt, true, false)
			code := l.getInt32(codeBuf, 0)

			// Read the body whatever the status: a server's own error
			// page is often the only explanation there is.
			text := l.winhttpRead(req)

			l.ccall("WinHttpCloseHandle", []Reg{req}, []vty{vInt}, vInt, true, false)
			l.ccall("WinHttpCloseHandle", []Reg{conn}, []vty{vInt}, vInt, true, false)
			l.ccall("WinHttpCloseHandle", []Reg{session}, []vty{vInt}, vInt, true, false)

			bad := l.newLabel()
			l.emit(Instr{Op: OpJumpIf, A: l.compare(OpGe, code, l.constant(400)),
				Dst: NoReg, Imm: bad})
			l.emit(Instr{Op: OpRet, A: l.resOk(text, ret), Dst: NoReg})

			l.mark(bad)
			l.emit(Instr{Op: OpRet,
				A: l.resFail(l.concatAll(l.strLit("server replied "),
					l.intToStr(code)), ret),
				Dst: NoReg})
		})
	return l.callHelper(sym,
		args, []vty{vStr, vInt, vStr, vBool, vStr, vStr}, ret)
}

// winhttpRead drains the response into one string.
//
// The buffer doubles rather than concatenating each chunk onto the last.
// Concatenating would be quadratic, and a megabyte response arrives in
// about a hundred and thirty pieces, so it is the difference between
// copying a megabyte and copying sixty.
func (l *lowerer) winhttpRead(req Reg) Reg {
	capSlot := l.temp(vInt)
	lenSlot := l.temp(vInt)
	bufSlot := l.temp(vInt)

	l.emit(Instr{Op: OpStore, A: l.constant(httpBufStart), Dst: NoReg, Imm: capSlot})
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: lenSlot})
	l.emit(Instr{Op: OpStore, A: l.strAlloc(l.constant(httpBufStart)),
		Dst: NoReg, Imm: bufSlot})

	avail := l.allocObj(l.constant(4), tagBytes)
	read := l.allocObj(l.constant(4), tagBytes)

	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	l.putInt32(avail, 0, l.constant(0))
	q := l.blockingCall("WinHttpQueryDataAvailable", []Reg{req, avail},
		[]vty{vInt, vInt}, vInt, true)
	l.emit(Instr{Op: OpJumpNot, A: l.asBool(q), Dst: NoReg, Imm: done})
	n := l.getInt32(avail, 0)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpGt, n, l.constant(0)),
		Dst: NoReg, Imm: done})

	// Grow while the chunk plus what is already held plus the
	// terminator does not fit.
	grow := l.newLabel()
	fits := l.newLabel()
	l.mark(grow)
	need := l.arith(OpAdd, l.arith(OpAdd, l.load(lenSlot, vInt), n), l.constant(1))
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpGt, need, l.load(capSlot, vInt)),
		Dst: NoReg, Imm: fits})
	newCap := l.arith(OpMul, l.load(capSlot, vInt), l.constant(2))
	bigger := l.strAlloc(newCap)
	l.copyBytes(bigger, l.load(bufSlot, vInt), l.load(lenSlot, vInt))
	l.emit(Instr{Op: OpStore, A: bigger, Dst: NoReg, Imm: bufSlot})
	l.emit(Instr{Op: OpStore, A: newCap, Dst: NoReg, Imm: capSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: grow})
	l.mark(fits)

	at := l.arith(OpAdd, l.load(bufSlot, vInt), l.load(lenSlot, vInt))
	l.blockingCall("WinHttpReadData", []Reg{req, at, n, read},
		[]vty{vInt, vInt, vInt, vInt}, vInt, true)
	l.emit(Instr{Op: OpStore,
		A:   l.arith(OpAdd, l.load(lenSlot, vInt), l.getInt32(read, 0)),
		Dst: NoReg, Imm: lenSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	// Copied at the length read, so a body with zero bytes in it - an
	// image, an archive - comes back whole.
	return l.strFromMem(l.load(bufSlot, vInt), l.load(lenSlot, vInt))
}

// intToStr renders an int, for the status code in a failure message.
func (l *lowerer) intToStr(v Reg) Reg {
	l.mod.needs("inttostr")
	d := l.newReg()
	l.regTy[d] = vStr
	l.emit(Instr{Op: OpIntToStr, Dst: d, A: v, B: NoReg})
	return d
}
