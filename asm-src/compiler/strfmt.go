package main

// Private builtins for turning a float into digits.
//
// These exist because msvcrt's printf is not correctly rounded and Go's
// formatting is. Asking msvcrt for sixteen significant digits of
// 0.588625783462934148992928840016 gives 0.5886257834629342, where the
// correct answer is 0.5886257834629341 - it carries about seventeen
// digits internally and rounds that, so the last digit is rounded twice.
// Both strings read back as the same double, which is why the
// round-trip search that used to pick the shorter of them never noticed.
//
// The fix is not to stop using printf but to stop asking it to round.
// Asked for enough digits it emits the exact decimal expansion, which a
// binary float always has and which is what correct rounding needs; the
// rounding then happens in the prelude, in Scandiacus, where it can be read.

// fmtSpec picks which format string the call uses. The values are the
// numbers the prelude passes.
const (
	fmtSpecE = 0 // "%.*e", scientific, prec fraction digits
	fmtSpecF = 1 // "%.*f", fixed, prec fraction digits
)

// fmtBuf is how much room a rendering gets. The widest thing asked for
// here is the exact expansion of a subnormal in fixed notation, which
// runs to about 1080 characters, and 1200 covers it with room over.
const fmtBuf = 1200

func (l *lowerer) fmtBuiltin(c *Call, name string) (Reg, bool) {
	if name != "__fmtE" && name != "__fmtF" {
		return NoReg, false
	}
	if len(c.Args) != 2 {
		l.errorAt(c, "%s takes 2 arguments, got %d", name, len(c.Args))
		return l.junk(), true
	}

	spec := "__fmt_star_e"
	if name == "__fmtF" {
		spec = "__fmt_star_f"
	}
	l.mod.Helpers["floatfmt"] = true

	prec := l.expr(c.Args[0])
	if l.regTy[prec].k != kInt {
		l.errorAt(c.Args[0], "%s wants an int precision", name)
		return l.junk(), true
	}
	x := l.numeric(c.Args[1])

	buf := l.strAlloc(l.constant(fmtBuf))

	// _snprintf(buf, fmtBuf, spec, prec, x). The value is the fourth
	// argument, past the register window, so the variadic duplication
	// rule does not apply to it - the same reasoning the float printer
	// relies on.
	fmtStr := l.symbolRef(spec)
	l.ccall("_snprintf",
		[]Reg{buf, l.constant(fmtBuf), fmtStr, prec, x},
		[]vty{vStr, vInt, vStr, vInt, vFloat},
		vInt, true, false)

	return buf, true
}

// symbolRef is the address of a fixed .rdata label as a string-typed
// register. Only the format strings above use it.
func (l *lowerer) symbolRef(label string) Reg {
	d := l.newReg()
	l.regTy[d] = vStr
	l.emit(Instr{Op: OpSymAddr, Dst: d, A: NoReg, B: NoReg, Sym: label})
	return d
}
