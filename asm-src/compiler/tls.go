package main

// TLS for the http client on Linux and macOS.
//
// On Windows WinHTTP does https://. Off it, __vy_httpOverNet in the
// prelude is a socket client written in Scandiacus, and these four builtins
// let it put a TLS session on the socket it connected:
//
//	__tlsOpen(sock, host) -> int!     handshake, certificate checked
//	__tlsSend(t, data)    -> int!     everything written, like net.send
//	__tlsRecv(t)          -> str!     one read, "" at the end
//	__tlsClose(t)                     end the session, not the socket
//
// They are answered by linuxrt/scandiacustls.c, which loads the system
// OpenSSL at run time. They are internal - the prelude is their only
// caller - and the Windows build never runs that code, so there they
// compile to a failure instead of imports no Windows DLL has.

func (l *lowerer) tlsBuiltin(c *Call, name string) (Reg, bool) {
	arity := func(n int) bool {
		if len(c.Args) != n {
			l.errorAt(c, "%s takes %d argument(s), got %d", name, n, len(c.Args))
			return false
		}
		return true
	}
	off := !targetLinux() && !targetMac()

	switch name {
	case "__tlsOpen":
		if !arity(2) {
			return l.junk(), true
		}
		sock, host := l.expr(c.Args[0]), l.expr(c.Args[1])
		ret := vty{k: kInt, res: true}
		if off {
			return l.resFail(l.strLit("TLS here is WinHTTP's"), ret), true
		}
		sym := l.helperFunc("tlsopen", []vty{vInt, vStr}, ret, func(a []Reg) {
			t := l.ccall("scandiacus_tls_open", []Reg{a[0], a[1]}, []vty{vInt, vStr}, vInt, false, false)
			bad := l.newLabel()
			l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, t, l.constant(0)), Dst: NoReg, Imm: bad})
			l.emit(Instr{Op: OpRet, A: l.resOk(t, ret), Dst: NoReg})
			l.mark(bad)
			l.emit(Instr{Op: OpRet, A: l.tlsFail(ret), Dst: NoReg})
		})
		return l.callHelper(sym, []Reg{sock, host}, []vty{vInt, vStr}, ret), true

	case "__tlsSend":
		if !arity(2) {
			return l.junk(), true
		}
		t, data := l.expr(c.Args[0]), l.expr(c.Args[1])
		ret := vty{k: kInt, res: true}
		if off {
			return l.resFail(l.strLit("TLS here is WinHTTP's"), ret), true
		}
		// A loop, as netSend is: a write may take less than all of it.
		sym := l.helperFunc("tlssend", []vty{vInt, vStr}, ret, func(a []Reg) {
			total := l.strLen(a[1])
			sent := l.temp(vInt)
			l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: sent})
			top, done, bad := l.newLabel(), l.newLabel(), l.newLabel()
			l.mark(top)
			at := l.load(sent, vInt)
			l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, at, total), Dst: NoReg, Imm: done})
			n := l.ccall("scandiacus_tls_send", []Reg{a[0], l.arith(OpAdd, a[1], at), l.arith(OpSub, total, at)},
				[]vty{vInt, vInt, vInt}, vInt, false, false)
			l.emit(Instr{Op: OpJumpIf, A: l.compare(OpLt, n, l.constant(0)), Dst: NoReg, Imm: bad})
			l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, at, n), Dst: NoReg, Imm: sent})
			l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
			l.mark(done)
			l.emit(Instr{Op: OpRet, A: l.resOk(total, ret), Dst: NoReg})
			l.mark(bad)
			l.emit(Instr{Op: OpRet, A: l.tlsFail(ret), Dst: NoReg})
		})
		return l.callHelper(sym, []Reg{t, data}, []vty{vInt, vStr}, ret), true

	case "__tlsRecv":
		if !arity(1) {
			return l.junk(), true
		}
		t := l.expr(c.Args[0])
		ret := vty{k: kStr, res: true}
		if off {
			return l.resFail(l.strLit("TLS here is WinHTTP's"), ret), true
		}
		sym := l.helperFunc("tlsrecv", []vty{vInt}, ret, func(a []Reg) {
			max := l.constant(defaultRecvMax)
			buf := l.strAlloc(max)
			n := l.ccall("scandiacus_tls_recv", []Reg{a[0], buf, max}, []vty{vInt, vStr, vInt}, vInt, false, false)
			bad := l.newLabel()
			l.emit(Instr{Op: OpJumpIf, A: l.compare(OpLt, n, l.constant(0)), Dst: NoReg, Imm: bad})
			l.strEnd(buf, n)
			l.emit(Instr{Op: OpRet, A: l.resOk(buf, ret), Dst: NoReg})
			l.mark(bad)
			l.emit(Instr{Op: OpRet, A: l.tlsFail(ret), Dst: NoReg})
		})
		return l.callHelper(sym, []Reg{t}, []vty{vInt}, ret), true

	case "__tlsClose":
		if !arity(1) {
			return l.junk(), true
		}
		t := l.expr(c.Args[0])
		if !off {
			l.ccall("scandiacus_tls_close", []Reg{t}, []vty{vInt}, vVoid, false, false)
		}
		return l.void(), true
	}
	return NoReg, false
}

// tlsFail is the failure carrying the runtime's reason, which it keeps
// per thread so two threads fetching at once each get their own.
func (l *lowerer) tlsFail(t vty) Reg {
	msg := l.ccall("scandiacus_tls_error", nil, nil, vInt, false, false)
	// Copied, since the runtime's buffer is reused by the next failure.
	return l.resFail(l.cstr(msg), t)
}
