package main

// The macOS half of the audio library: five calls into the AudioQueue
// streams in linuxrt/veylmac.c, which the prelude's audio functions
// make on macOS only. On every other target they lower to constants,
// so a Windows or Linux program does not import functions its runtime
// does not have.

func (l *lowerer) audioMacBuiltin(c *Call, name string) (Reg, bool) {
	var sym string
	var types []vty
	ret := vInt
	switch name {
	case "__audioMacOpen":
		sym, types = "veyl_audio_open", []vty{vInt, vInt, vInt, vInt}
	case "__audioMacRead":
		sym, types = "veyl_audio_read", []vty{vInt, vInt, vInt}
	case "__audioMacWrite":
		sym, types = "veyl_audio_write", []vty{vInt, vInt, vInt}
	case "__audioMacClose":
		sym, types, ret = "veyl_audio_close", []vty{vInt}, vVoid
	case "__audioMacError":
		sym, types = "veyl_audio_error", nil
	default:
		return NoReg, false
	}
	if len(c.Args) != len(types) {
		l.errorAt(c, "%s takes %d arguments, got %d", name, len(types), len(c.Args))
		return l.junk(), true
	}
	args := make([]Reg, len(c.Args))
	for i, a := range c.Args {
		args[i] = l.expr(a)
	}
	if !targetMac() {
		if name == "__audioMacError" {
			return l.strLit(""), true
		}
		return l.constant(0), true
	}
	// Read, write and close wait on the device, so they go through
	// blockingCall: a collection does not wait for them.
	var r Reg
	switch name {
	case "__audioMacRead", "__audioMacWrite", "__audioMacClose":
		r = l.blockingCall(sym, args, types, ret, false)
	default:
		r = l.ccall(sym, args, types, ret, false, false)
	}
	if name == "__audioMacError" {
		// The runtime's buffer is reused by the next failure.
		return l.cstr(r), true
	}
	if ret.k == kVoid {
		return l.constant(0), true
	}
	return r, true
}
