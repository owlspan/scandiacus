package main

// The clock, and the three things only the operating system can answer.
//
// Everything above these - formatting, parsing, the calendar - is in the
// prelude, in Scandiacus. What is here is what has to be a call: reading the
// wall clock, turning a Unix second into a local calendar, and turning a
// local calendar back into a Unix second.
//
// Local, not UTC, and that is not a detail. The Go backend formats with
// time.Unix(...).Format, which renders in the process's own time zone,
// so a backend that rendered UTC would disagree with it by hours for
// most of the world and agree in London in winter - the worst kind of
// bug to find. localtime and mktime read the same zone Windows gives Go,
// so the two agree by construction rather than by coincidence.

// The fields of C's struct tm, in order. Nine ints, four bytes each.
const (
	tmSec = iota
	tmMin
	tmHour
	tmMday
	tmMon  // 0 = January
	tmYear // years since 1900
	tmWday // 0 = Sunday
	tmYday
	tmIsdst

	tmFields
)

func (l *lowerer) timeBuiltin(c *Call, name string) (Reg, bool) {
	arity := func(n int) bool {
		if len(c.Args) != n {
			l.errorAt(c, "%s takes %d argument(s), got %d", name, n, len(c.Args))
			return false
		}
		return true
	}

	switch name {
	case "__tmField":
		if !arity(2) {
			return l.junk(), true
		}
		return l.tmField(l.intArg(c, 0), l.intArg(c, 1)), true

	case "__mktime":
		if !arity(6) {
			return l.junk(), true
		}
		args := make([]Reg, 6)
		for i := range args {
			args[i] = l.intArg(c, i)
		}
		return l.mktime(args), true

	case "__millis", "time.millis":
		if !arity(0) {
			return l.junk(), true
		}
		return l.millis(), true

	case "time.nanos":
		if !arity(0) {
			return l.junk(), true
		}
		// GetSystemTimeAsFileTime ticks at a hundred nanoseconds, so the
		// last two digits here are always zero. The Go backend's
		// UnixNano has real nanosecond resolution and this does not,
		// which no program can depend on without depending on the clock.
		return l.arith(OpMul, l.millisTicks(), l.constant(100)), true

	case "time.sleep":
		if !arity(1) {
			return l.junk(), true
		}
		l.blockingCall("Sleep", []Reg{l.intArg(c, 0)}, []vty{vInt},
			vty{k: kVoid}, false)
		return l.junk(), true
	}

	return NoReg, false
}

// intArg lowers argument i and insists it is an int.
func (l *lowerer) intArg(c *Call, i int) Reg {
	v := l.expr(c.Args[i])
	if l.regTy[v].k != kInt {
		l.errorAt(c.Args[i], "argument %d must be an int", i+1)
		return l.constant(0)
	}
	return v
}

// The C runtime's local calendar covers 1970 to 3000 (_localtime64 on
// Windows gives a null pointer outside it, _mktime64 -1). The Gregorian
// calendar repeats exactly every 400 years - 146097 days, weekdays and
// leap days included - so a second outside the range is moved by whole
// cycles into it, converted there, and its year moved back. The local
// rules are then applied to the same date in the moved year, which is
// what both the C runtime and Go on Windows do: they use today's rules
// for every year. Only a second the C runtime refuses is moved - on
// Linux it covers every year, with the zone's history, and that answer
// is kept. It used to read through the null pointer, so any date before
// 1970 crashed the program on Windows.
const (
	cycleSecs  = 146097 * 86400 // 400 years
	tmLastSafe = 32503680000    // 3000-01-01, comfortably inside
)

// tmField reads one field of the local calendar for a Unix second.
//
// _localtime64 wants a pointer to a 64-bit time and returns a pointer to
// a struct it owns, so the second goes into a frame slot to have an
// address at all. The struct is not copied: the fields are read straight
// out of it, which is safe because nothing else is called in between.
func (l *lowerer) tmField(secs, idx Reg) Reg {
	slot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: secs, Dst: NoReg, Imm: slot})
	idxS := l.temp(vInt)
	l.setSlot(idxS, idx)
	yearAdj := l.temp(vInt)
	l.setSlot(yearAdj, l.constant(0))
	tmS := l.temp(vInt)
	l.setSlot(tmS, l.ccall("_localtime64", []Reg{l.slotAddr(slot)}, []vty{vInt}, vInt, false, false))
	have := l.newLabel()
	l.jumpNot(l.compare(OpEq, l.load(tmS, vInt), l.constant(0)), have)

	// Refused: into the range by whole cycles, keeping count in years.
	inRange, low := l.newLabel(), l.newLabel()
	l.jumpIf(l.compare(OpLt, l.load(slot, vInt), l.constant(cycleSecs)), low)
	l.jumpNot(l.compare(OpGt, l.load(slot, vInt), l.constant(tmLastSafe)), inRange)
	k := l.arith(OpDiv, l.arith(OpAdd, l.arith(OpSub, l.load(slot, vInt), l.constant(tmLastSafe)), l.constant(cycleSecs-1)), l.constant(cycleSecs))
	kS := l.temp(vInt)
	l.setSlot(kS, k)
	l.setSlot(slot, l.arith(OpSub, l.load(slot, vInt), l.arith(OpMul, l.load(kS, vInt), l.constant(cycleSecs))))
	l.setSlot(yearAdj, l.arith(OpMul, l.load(kS, vInt), l.constant(400)))
	l.jump(inRange)
	l.mark(low)
	// Below one cycle up from 1970, so that a zone east of UTC, whose
	// local midnight of 1 January 1970 is before second 0, is inside
	// too. Seconds already in 1970-2370 are moved up one cycle and back,
	// which changes nothing.
	k2 := l.arith(OpDiv, l.arith(OpSub, l.constant(2*cycleSecs-1), l.load(slot, vInt)), l.constant(cycleSecs))
	l.setSlot(kS, k2)
	l.setSlot(slot, l.arith(OpAdd, l.load(slot, vInt), l.arith(OpMul, l.load(kS, vInt), l.constant(cycleSecs))))
	l.setSlot(yearAdj, l.arith(OpMul, l.load(kS, vInt), l.constant(-400)))
	l.mark(inRange)
	l.setSlot(tmS, l.ccall("_localtime64", []Reg{l.slotAddr(slot)}, []vty{vInt}, vInt, false, false))
	l.mark(have)

	out := l.temp(vInt)
	l.setSlot(out, l.constant(0))
	null := l.newLabel()
	l.jumpIf(l.compare(OpEq, l.load(tmS, vInt), l.constant(0)), null)

	// Two four-byte fields to a word, so field i lives in word i/2, in
	// its low half when i is even and its high half when it is odd.
	word := l.newReg()
	l.regTy[word] = vInt
	addr := l.newReg()
	l.regTy[addr] = vInt
	l.emit(Instr{Op: OpIndexAddr, Dst: addr, A: l.load(tmS, vInt), B: l.arith(OpShr, l.load(idxS, vInt), l.constant(1))})
	l.emit(Instr{Op: OpLoadMem, Dst: word, A: addr, B: NoReg, Imm: 0})

	shift := l.arith(OpShl, l.arith(OpBAnd, l.load(idxS, vInt), l.constant(1)), l.constant(5))
	half := l.arith(OpShr, word, shift)

	// A tm field is a C int, so the top half of the word is another
	// field or padding. Left then right by 32 with an arithmetic shift
	// keeps the sign, which tm_year and tm_isdst both need.
	field := l.arith(OpShr, l.arith(OpShl, half, l.constant(32)), l.constant(32))
	isYear := l.compare(OpEq, l.load(idxS, vInt), l.constant(5))
	l.setSlot(out, l.arith(OpAdd, field, l.pick(isYear, l.load(yearAdj, vInt), l.constant(0), vInt)))
	l.mark(null)
	return l.load(out, vInt)
}

// mktime turns a local calendar back into a Unix second, or -1.
//
// The struct is built here rather than passed field by field because
// _mktime64 takes a pointer to one. tm_isdst is set to -1, which is the
// C way of saying "work out for yourself whether daylight saving was in
// force" - the same question ParseInLocation answers on the Go side, and
// getting it wrong shifts an hour twice a year. A year outside what the
// C runtime covers is moved by whole 400-year cycles, as in tmField.
func (l *lowerer) mktime(f []Reg) Reg {
	yearS := l.temp(vInt)
	l.setSlot(yearS, f[0])
	secsAdj := l.temp(vInt)
	l.setSlot(secsAdj, l.constant(0))
	fs := make([]int64, 6)
	for i := 1; i < 6; i++ {
		fs[i] = l.temp(vInt)
		l.setSlot(fs[i], f[i])
	}
	rS := l.temp(vInt)
	l.setSlot(rS, l.mktimeOnce(l.load(yearS, vInt), fs))
	done := l.newLabel()
	l.jumpNot(l.compare(OpEq, l.load(rS, vInt), l.constant(-1)), done)

	// Refused: the year moved by whole cycles, and the answer back.
	ok, low := l.newLabel(), l.newLabel()
	l.jumpIf(l.compare(OpLt, l.load(yearS, vInt), l.constant(1971)), low)
	l.jumpNot(l.compare(OpGt, l.load(yearS, vInt), l.constant(2999)), ok)
	k := l.temp(vInt)
	l.setSlot(k, l.arith(OpDiv, l.arith(OpAdd, l.arith(OpSub, l.load(yearS, vInt), l.constant(2999)), l.constant(399)), l.constant(400)))
	l.setSlot(yearS, l.arith(OpSub, l.load(yearS, vInt), l.arith(OpMul, l.load(k, vInt), l.constant(400))))
	l.setSlot(secsAdj, l.arith(OpMul, l.load(k, vInt), l.constant(cycleSecs)))
	l.jump(ok)
	l.mark(low)
	l.setSlot(k, l.arith(OpDiv, l.arith(OpAdd, l.arith(OpSub, l.constant(1971), l.load(yearS, vInt)), l.constant(399)), l.constant(400)))
	l.setSlot(yearS, l.arith(OpAdd, l.load(yearS, vInt), l.arith(OpMul, l.load(k, vInt), l.constant(400))))
	l.setSlot(secsAdj, l.arith(OpMul, l.load(k, vInt), l.constant(-cycleSecs)))
	l.mark(ok)
	l.setSlot(rS, l.mktimeOnce(l.load(yearS, vInt), fs))
	l.jumpIf(l.compare(OpEq, l.load(rS, vInt), l.constant(-1)), done)
	l.setSlot(rS, l.arith(OpAdd, l.load(rS, vInt), l.load(secsAdj, vInt)))
	l.mark(done)
	return l.load(rS, vInt)
}

// mktimeOnce is one call of _mktime64 on these fields.
func (l *lowerer) mktimeOnce(yearR Reg, fs []int64) Reg {
	yearS := l.temp(vInt)
	l.setSlot(yearS, yearR)
	buf := l.allocObj(l.constant(tmFields*4+8), tagBytes)
	bufS := l.temp(vInt)
	l.setSlot(bufS, buf)

	// Packed two fields to a word, in the order C declares them.
	pack := func(lo, hi Reg) Reg {
		low := l.arith(OpBAnd, lo, l.constant(0xFFFFFFFF))
		return l.arith(OpBOr, low, l.arith(OpShl, hi, l.constant(32)))
	}
	ld := func(i int) Reg { return l.load(fs[i], vInt) }
	year := l.arith(OpSub, l.load(yearS, vInt), l.constant(1900))
	mon := l.arith(OpSub, ld(1), l.constant(1))

	words := []Reg{
		pack(ld(5), ld(4)),                  // tm_sec, tm_min
		pack(ld(3), ld(2)),                  // tm_hour, tm_mday
		pack(mon, year),                     // tm_mon, tm_year
		pack(l.constant(0), l.constant(0)),  // tm_wday, tm_yday
		pack(l.constant(-1), l.constant(0)), // tm_isdst, padding
	}
	for i, w := range words {
		l.emit(Instr{Op: OpStoreMem, A: l.load(bufS, vInt), B: w, Dst: NoReg, Imm: int64(i * 8)})
	}

	return l.ccall("_mktime64", []Reg{l.load(bufS, vInt)}, []vty{vInt}, vInt, false, false)
}

// millis is the wall clock in milliseconds since the Unix epoch.
//
// GetSystemTimeAsFileTime rather than a C call, because the C ones that
// give sub-second resolution differ between runtimes and this one is the
// same everywhere Windows is. It counts hundred-nanosecond ticks from
// 1601, so the epoch shift is a constant.
func (l *lowerer) millis() Reg {
	// Ten thousand ticks to the millisecond. The epoch shift is already
	// out, in millisTicks.
	return l.arith(OpDiv, l.millisTicks(), l.constant(10000))
}

// millisTicks is the raw FILETIME, already shifted to the Unix epoch but
// still counting hundred-nanosecond ticks.
func (l *lowerer) millisTicks() Reg {
	slot := l.temp(vInt)
	l.ccall("GetSystemTimeAsFileTime", []Reg{l.slotAddr(slot)}, []vty{vInt},
		vty{k: kVoid}, false, false)
	ticks := l.newReg()
	l.regTy[ticks] = vInt
	l.emit(Instr{Op: OpLoad, Dst: ticks, A: NoReg, B: NoReg, Imm: slot})
	return l.arith(OpSub, ticks, l.constant(116444736000000000))
}
