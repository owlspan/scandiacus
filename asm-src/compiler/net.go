package main

// TCP sockets, through WinSock.
//
// This is the first thing here that lives in neither msvcrt nor
// kernel32, which is why importOverride in pe.go exists: the naming
// rule that routes a PascalCase symbol to kernel32 and everything else
// to msvcrt has no way to know that `socket` and `recv` are in
// ws2_32.dll. They are listed there by name.
//
// A socket is a plain int here, the SOCKET handle Windows hands back.
// That keeps the whole http layer writable in Veyl in the prelude,
// where a socket is just a number to pass around.

// WinSock constants, all from winsock2.h.
const (
	afInet      = 2
	sockStream  = 1
	ipprotoTCP  = 6
	solSocket   = 0xffff
	soReuseAddr = 4
	somaxconn   = 0x7fffffff

	// The version WSAStartup asks for, 2.2, low byte first.
	winsockVersion = 0x0202

	// WSADATA is 408 bytes on x64. Rounded up; nothing reads it.
	wsaDataSize = 512

	// sockaddr_in: family, port, address, then eight bytes of padding
	// that must be zero.
	sockaddrSize   = 16
	sinFamilyOff   = 0
	sinPortOff     = 2
	sinAddrOff     = 4
	sinZeroOff     = 8
	defaultRecvMax = 65536
)

// winsockSyms are the ws2_32 imports. pe.go routes them by this list.
var winsockSyms = []string{
	"WSAStartup", "WSACleanup", "WSAGetLastError",
	"socket", "bind", "listen", "accept", "connect",
	"recv", "send", "closesocket", "setsockopt", "gethostbyname",
}

// ensureWinsock calls WSAStartup. It is called at the top of every
// entry point rather than once, because WSAStartup is reference
// counted and nothing here ever calls WSACleanup, so the second and
// later calls just bump a counter. One call is cheaper than the global
// and the guard branch that avoiding it would need.
func (l *lowerer) ensureWinsock() {
	data := l.allocObj(l.constant(wsaDataSize), tagBytes)
	l.ccall("WSAStartup", []Reg{l.constant(winsockVersion), data},
		[]vty{vInt, vInt}, vInt, true, false)
}

// sockaddr builds a sockaddr_in for a port and an address already in
// host order. The port goes out big-endian, which is what "network
// byte order" means and the one place a socket API still insists on it.
func (l *lowerer) sockaddr(port, addr Reg) Reg {
	sa := l.allocObj(l.constant(sockaddrSize), tagBytes)

	i := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: i})
	top := l.newLabel()
	done := l.newLabel()
	l.mark(top)
	cur := l.load(i, vInt)
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, cur, l.constant(sockaddrSize)),
		Dst: NoReg, Imm: done})
	l.storeByte(sa, cur, l.constant(0))
	l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, cur, l.constant(1)), Dst: NoReg, Imm: i})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
	l.mark(done)

	l.storeByte(sa, l.constant(sinFamilyOff), l.constant(afInet))
	l.storeByte(sa, l.constant(sinPortOff),
		l.arith(OpBAnd, l.arith(OpShr, port, l.constant(8)), l.constant(255)))
	l.storeByte(sa, l.constant(sinPortOff+1),
		l.arith(OpBAnd, port, l.constant(255)))

	// The address is already the four bytes in the order they go on the
	// wire, packed into an int by ipv4Bytes or left zero for ANY.
	for k := int64(0); k < 4; k++ {
		l.storeByte(sa, l.constant(sinAddrOff+k),
			l.arith(OpBAnd, l.arith(OpShr, addr, l.constant(8*k)), l.constant(255)))
	}
	return sa
}

// sockFail is the failure a socket call produces: what was being
// attempted, then the system's own sentence for why it did not.
func (l *lowerer) sockFail(op string, t vty) Reg {
	return l.resFail(l.concatAll(l.strLit(op+": "), l.sysMessage()), t)
}

// checkSock branches on a call that reports failure as a negative
// number, which is every WinSock call here: INVALID_SOCKET and
// SOCKET_ERROR are both all-ones.
func (l *lowerer) failIfNegative(v Reg, op string, t vty) {
	ok := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, v, l.constant(0)),
		Dst: NoReg, Imm: ok})
	l.emit(Instr{Op: OpRet, A: l.sockFail(op, t), Dst: NoReg})
	l.mark(ok)
}

func (l *lowerer) netBuiltin(c *Call, name string) (Reg, bool) {
	arity := func(n int) bool {
		if len(c.Args) != n {
			l.errorAt(c, "%s takes %d argument(s), got %d", name, n, len(c.Args))
			return false
		}
		return true
	}

	switch name {
	case "net.listen":
		if !arity(1) {
			return l.junk(), true
		}
		return l.netListen(l.expr(c.Args[0])), true

	case "net.accept":
		if !arity(1) {
			return l.junk(), true
		}
		return l.netAccept(l.expr(c.Args[0])), true

	case "net.recv":
		if !arity(1) && !arity(2) {
			return l.junk(), true
		}
		max := l.constant(defaultRecvMax)
		if len(c.Args) == 2 {
			max = l.expr(c.Args[1])
		}
		return l.netRecv(l.expr(c.Args[0]), max), true

	case "net.send":
		if !arity(2) {
			return l.junk(), true
		}
		return l.netSend(l.expr(c.Args[0]), l.expr(c.Args[1])), true

	case "net.close":
		if !arity(1) {
			return l.junk(), true
		}
		l.ccall("closesocket", []Reg{l.expr(c.Args[0])}, []vty{vInt}, vInt, true, false)
		return l.junk(), true

	case "net.connect":
		if !arity(2) {
			return l.junk(), true
		}
		return l.netConnect(l.expr(c.Args[0]), l.expr(c.Args[1])), true

	case "__winhttp":
		if !arity(6) {
			return l.junk(), true
		}
		a := make([]Reg, 6)
		for i := range a {
			a[i] = l.expr(c.Args[i])
		}
		// WinHTTP on Windows; a plain socket client on Linux and macOS,
		// written in Veyl and folded in from the prelude. The two share a
		// signature so the caller cannot tell them apart.
		if targetLinux() || targetMac() {
			return l.callPrelude("__vy_httpOverNet", a), true
		}
		return l.winhttpFetch(a), true
	}
	return NoReg, false
}

// netListen is the four calls that turn a port into a socket somebody
// can accept on, in a helper function so the early failures can return.
func (l *lowerer) netListen(port Reg) Reg {
	ret := vty{k: kInt, res: true}
	sym := l.helperFunc("netlisten", []vty{vInt}, ret, func(args []Reg) {
		l.ensureWinsock()

		s := l.ccall("socket",
			[]Reg{l.constant(afInet), l.constant(sockStream), l.constant(ipprotoTCP)},
			[]vty{vInt, vInt, vInt}, vInt, false, false)
		l.failIfNegative(s, "socket", ret)

		// SO_REUSEADDR, so restarting a server does not fail for the
		// couple of minutes the old socket spends in TIME_WAIT.
		one := l.allocObj(l.constant(4), tagBytes)
		l.storeByte(one, l.constant(0), l.constant(1))
		for k := int64(1); k < 4; k++ {
			l.storeByte(one, l.constant(k), l.constant(0))
		}
		l.ccall("setsockopt",
			[]Reg{s, l.constant(solSocket), l.constant(soReuseAddr), one, l.constant(4)},
			[]vty{vInt, vInt, vInt, vInt, vInt}, vInt, true, false)

		sa := l.sockaddr(args[0], l.constant(0))
		b := l.ccall("bind", []Reg{s, sa, l.constant(sockaddrSize)},
			[]vty{vInt, vInt, vInt}, vInt, true, false)
		l.failIfNegative(b, "bind", ret)

		ln := l.ccall("listen", []Reg{s, l.constant(somaxconn)},
			[]vty{vInt, vInt}, vInt, true, false)
		l.failIfNegative(ln, "listen", ret)

		l.emit(Instr{Op: OpRet, A: l.resOk(s, ret), Dst: NoReg})
	})
	return l.callHelper(sym, []Reg{port}, []vty{vInt}, ret)
}

func (l *lowerer) netAccept(sock Reg) Reg {
	ret := vty{k: kInt, res: true}
	sym := l.helperFunc("netaccept", []vty{vInt}, ret, func(args []Reg) {
		c := l.ccall("accept", []Reg{args[0], l.constant(0), l.constant(0)},
			[]vty{vInt, vInt, vInt}, vInt, false, false)
		l.failIfNegative(c, "accept", ret)
		l.emit(Instr{Op: OpRet, A: l.resOk(c, ret), Dst: NoReg})
	})
	return l.callHelper(sym, []Reg{sock}, []vty{vInt}, ret)
}

// callPrelude emits a call to a prelude function the lowerer reaches by
// name, using the signature the prelude declared it with. It is how a
// builtin the lowerer handles itself can still delegate to Veyl code -
// __winhttp routes here to __vy_httpOverNet off Windows.
func (l *lowerer) callPrelude(fn string, args []Reg) Reg {
	s, ok := l.sigs[fn]
	if !ok {
		fail("the prelude did not supply %s", fn)
	}
	if len(args) > l.fn.MaxCallArgs {
		l.fn.MaxCallArgs = len(args)
	}
	d := l.newReg()
	l.regTy[d] = s.ret
	l.emit(Instr{Op: OpCall, Dst: d, A: NoReg, B: NoReg, Args: args,
		ArgTypes: s.params, RetType: s.ret, Sym: fn, Comment: fn + "()"})
	return d
}

// netRecv reads once and returns what arrived. One recv, not a loop to
// fill the buffer: TCP is a stream and the caller is the only one who
// knows where a message ends.
func (l *lowerer) netRecv(sock, max Reg) Reg {
	ret := vty{k: kStr, res: true}
	sym := l.helperFunc("netrecv", []vty{vInt, vInt}, ret, func(args []Reg) {
		buf := l.strAlloc(args[1])
		n := l.ccall("recv", []Reg{args[0], buf, args[1], l.constant(0)},
			[]vty{vInt, vStr, vInt, vInt}, vInt, true, false)
		l.failIfNegative(n, "recv", ret)
		// A zero return is the peer closing cleanly, which is an empty
		// string rather than a failure.
		l.storeByte(buf, n, l.constant(0))
		l.emit(Instr{Op: OpRet, A: l.resOk(buf, ret), Dst: NoReg})
	})
	return l.callHelper(sym, []Reg{sock, max}, []vty{vInt, vInt}, ret)
}

// netSend loops until everything is written. send is allowed to take
// less than it was given, and a partial write that nobody noticed is a
// truncated response that looks like a server bug.
func (l *lowerer) netSend(sock, data Reg) Reg {
	ret := vty{k: kInt, res: true}
	sym := l.helperFunc("netsend", []vty{vInt, vStr}, ret, func(args []Reg) {
		total := l.strLen(args[1])
		sent := l.temp(vInt)
		l.emit(Instr{Op: OpStore, A: l.constant(0), Dst: NoReg, Imm: sent})

		top := l.newLabel()
		done := l.newLabel()
		l.mark(top)
		at := l.load(sent, vInt)
		l.emit(Instr{Op: OpJumpNot, A: l.compare(OpLt, at, total), Dst: NoReg, Imm: done})

		n := l.ccall("send",
			[]Reg{args[0], l.arith(OpAdd, args[1], at),
				l.arith(OpSub, total, at), l.constant(0)},
			[]vty{vInt, vInt, vInt, vInt}, vInt, true, false)
		l.failIfNegative(n, "send", ret)
		l.emit(Instr{Op: OpStore, A: l.arith(OpAdd, at, n), Dst: NoReg, Imm: sent})
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

		l.mark(done)
		l.emit(Instr{Op: OpRet, A: l.resOk(total, ret), Dst: NoReg})
	})
	return l.callHelper(sym, []Reg{sock, data}, []vty{vInt, vStr}, ret)
}

// netConnect resolves a name with gethostbyname and connects to it.
//
// gethostbyname rather than getaddrinfo: it is IPv4 only and long
// deprecated, but it returns a struct whose first address is four bytes
// at a fixed offset, where getaddrinfo hands back a linked list of
// sockaddrs that this would have to walk. IPv6 is the reason to change
// that, and it is a real one.
func (l *lowerer) netConnect(host, port Reg) Reg {
	ret := vty{k: kInt, res: true}
	sym := l.helperFunc("netconnect", []vty{vStr, vInt}, ret, func(args []Reg) {
		l.ensureWinsock()

		he := l.ccall("gethostbyname", []Reg{args[0]}, []vty{vStr}, vInt, false, false)
		bad := l.newLabel()
		l.emit(Instr{Op: OpJumpIf, A: l.compare(OpEq, he, l.constant(0)),
			Dst: NoReg, Imm: bad})

		// struct hostent on x64: h_name, h_aliases, h_addrtype (int,
		// padded), h_length (short), then h_addr_list at offset 24. The
		// first entry of that list points at the four address bytes.
		list := l.field(he, 24, vInt)
		first := l.field(list, 0, vInt)
		addr := l.constant(0)
		for k := int64(0); k < 4; k++ {
			b := l.loadByte(first, l.constant(k))
			addr = l.arith(OpBOr, addr, l.arith(OpShl, b, l.constant(8*k)))
		}

		s := l.ccall("socket",
			[]Reg{l.constant(afInet), l.constant(sockStream), l.constant(ipprotoTCP)},
			[]vty{vInt, vInt, vInt}, vInt, false, false)
		l.failIfNegative(s, "socket", ret)

		sa := l.sockaddr(args[1], addr)
		rc := l.ccall("connect", []Reg{s, sa, l.constant(sockaddrSize)},
			[]vty{vInt, vInt, vInt}, vInt, true, false)
		l.failIfNegative(rc, "connect", ret)
		l.emit(Instr{Op: OpRet, A: l.resOk(s, ret), Dst: NoReg})

		l.mark(bad)
		l.emit(Instr{Op: OpRet,
			A:   l.resFail(l.concatAll(l.strLit("cannot resolve \""), args[0], l.strLit("\"")), ret),
			Dst: NoReg})
	})
	return l.callHelper(sym, []Reg{host, port}, []vty{vStr, vInt}, ret)
}
