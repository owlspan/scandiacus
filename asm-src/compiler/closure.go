package main

// Closures: a function as a value.
//
// A function value is one word, a pointer to a two-word object:
//
//	[clo+0]  the environment, or 0 when nothing was captured
//	[clo+8]  the code address
//
// Both offsets are fixed, which is the whole reason the environment is
// a separate object rather than the captures sitting inline. A call site
// knows neither how many captures a value has nor whether it has any -
// `steps` in functions2.vl is a list holding two capture-free values and
// one that captured ten - so the code address has to be somewhere the
// call site can name without knowing.
//
// The object is tagged tagStruct with nptr 1, so the collector already
// handles it: it marks the first word, the environment, and steps over
// the second, which is an address in the text and not a heap pointer at
// all. No new tag and no change to gcmark.go.
//
// The environment is a block of pointers - every capture is a box - so
// it is tagPtrs and the collector already handles that too.
//
// Captures are by reference, because Go's are. `seen += n` inside a
// closure has to be visible outside it, and a closure returned from
// adder() has to keep reading a parameter whose frame is gone. So every
// captured variable lives in a one-word heap cell and the environment
// holds the cell, not the value. A variable that is never captured is
// untouched and stays in its stack slot.
//
// The environment reaches the body in a register rather than as an
// argument. That is what lets a plain declared function be used as a
// value with no wrapper: `let f = double` builds a closure whose code
// is double's own address, and double ignores the register it was
// handed. The alternative was a thunk per named function used this way.

// A capture is one name the environment carries, with the type of what
// its cell holds.
type capture struct {
	name string
	t    vty
}

const (
	cloEnvOff  = 0
	cloCodeOff = 8
	cloWords   = 2
)

// closureHeader is the object header for the two-word closure: sixteen
// bytes, one leading pointer, and the struct tag whose scanning rule
// already says "mark that many words and stop".
func closureHeader() int64 {
	return int64(cloWords*wordSize)<<structSizeShift | 1<<structNPtrShift | tagStruct
}

// funcLit lowers `fn(...) { ... }` used as a value.
func (l *lowerer) funcLit(x *FuncLit) Reg {
	t, ok := vtyOf(x.T)
	if !ok {
		l.errorAt(x, "a function of type %s is not on the assembly backend yet", x.T)
		return l.junk()
	}

	free := freeNames(x.Decl)

	// Only the names that are actually variables here are captured. A
	// reference to a declared function, a global or a library path is
	// reached the same way from inside the literal as from outside it.
	var captured []capture
	for _, name := range free {
		if slot, isLocal := l.lookup(name); isLocal {
			captured = append(captured, capture{name: name, t: l.slotTy[slot]})
			continue
		}
		// A name this function captured itself is captured again, one
		// level further out. The cell is the same one, so a variable two
		// closures deep is still one variable.
		if t, isCap := l.captureTy[name]; isCap {
			captured = append(captured, capture{name: name, t: t})
		}
	}

	code := l.liftFunc(x.Decl, t, captured)
	clo := l.makeClosure(code, captured)
	l.regTy[clo] = t
	return clo
}

// makeClosure builds the object: an environment holding one box per
// capture, and the code address beside it.
func (l *lowerer) makeClosure(code string, captured []capture) Reg {
	env := l.constant(0)
	if len(captured) > 0 {
		env = l.allocObj(l.constant(int64(len(captured)*wordSize)), tagPtrs)
		for i, c := range captured {
			// The box itself is what goes in, not what is in it. Two
			// closures capturing the same variable therefore see each
			// other's writes, which is what capturing by reference
			// means.
			l.emit(Instr{Op: OpStoreMem, A: env, B: l.boxOf(c.name), Dst: NoReg,
				Imm: int64(i * wordSize), Comment: "capture " + c.name})
		}
	}

	clo := l.allocTagged(l.constant(cloWords*wordSize), l.constant(closureHeader()))
	l.emit(Instr{Op: OpStoreMem, A: clo, B: env, Dst: NoReg, Imm: cloEnvOff})

	addr := l.newReg()
	l.regTy[addr] = vInt
	l.emit(Instr{Op: OpSymAddr, Dst: addr, A: NoReg, B: NoReg, Sym: fnSym(code),
		Comment: code})
	l.emit(Instr{Op: OpStoreMem, A: clo, B: addr, Dst: NoReg, Imm: cloCodeOff})
	return clo
}

// boxOf is the cell a captured variable lives in. The variable was
// boxed when it was declared, so its slot already holds the cell.
func (l *lowerer) boxOf(name string) Reg {
	slot, ok := l.lookup(name)
	if !ok {
		// Not a local here: it is something this function captured, so
		// its cell comes out of this function's own environment.
		if box, isCap := l.captureBox(name); isCap {
			return box
		}
		return l.constant(0)
	}
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: slot,
		Comment: "box " + name})
	return d
}

// newBox allocates a cell for a captured variable and returns it. The
// tag depends on what the variable holds, because that is what tells the
// collector whether to follow the word inside.
func (l *lowerer) newBox(t vty) Reg {
	tag := int64(tagWords)
	if t.holdsPointer() {
		tag = tagPtrs
	}
	return l.allocObj(l.constant(wordSize), tag)
}

// liftFunc compiles a function literal's body as a top-level function
// and returns its name.
//
// The lowerer has one function in progress at a time, so everything that
// belongs to the outer one is put aside and restored. That is the same
// shape helperFunc uses, and for the same reason: the alternative is a
// second lowerer that would have to share the module, the struct table
// and the signature table anyway.
func (l *lowerer) liftFunc(fd *FnDecl, t vty, captured []capture) string {
	name := l.nextClosureName()

	savedFn, savedSlots, savedRegs := l.fn, l.slotTy, l.regTy
	savedScopes, savedLoops, savedBuf := l.scopes, l.loops, l.buf
	savedCaptures, savedCapTy, savedBoxed := l.captures, l.captureTy, l.boxed
	savedDisplay := l.fnDisplay
	l.fnDisplay = "a function literal in " + savedDisplay
	defer func() { l.fnDisplay = savedDisplay }()

	params := make([]vty, len(fd.Params))
	if t.fn != nil {
		copy(params, t.fn.params)
	}
	ret := vVoid
	if t.fn != nil {
		ret = t.fn.ret
	}

	l.fn = &Func{Name: name, NParams: len(fd.Params), ParamTypes: params,
		Ret: ret, Env: true}
	l.slotTy = map[int64]vty{}
	l.regTy = map[Reg]vty{}
	l.scopes = nil
	l.loops = nil
	l.buf = -1
	l.captures = map[string]int{}
	l.captureTy = map[string]vty{}
	l.boxed = map[int64]bool{}

	// Slot zero is the environment. x64.go writes the register holding
	// it there in the prologue, before anything can clobber it.
	envSlot := l.temp(vInt)
	_ = envSlot

	for i, c := range captured {
		l.captures[c.name] = i
		l.captureTy[c.name] = c.t
	}

	l.pushScope()

	// Any of this literal's own names that a literal *inside* it takes
	// has to be boxed here too, exactly as in an ordinary function.
	inner := capturedIn(fd)
	savedBoxNames := l.boxNames
	l.boxNames = inner
	defer func() { l.boxNames = savedBoxNames }()

	for i, pa := range fd.Params {
		// Read before boxed, for the reason in ir.go's function().
		d := l.newReg()
		l.regTy[d] = params[i]
		l.emit(Instr{Op: OpParam, Dst: d, A: NoReg, B: NoReg, Imm: int64(i),
			Comment: pa.Name})
		slot := l.declareMaybeBoxed(pa.Name, params[i], inner[pa.Name])
		if pa.Addressed {
			l.keepInMemory(fd, pa.Name, slot)
		}
		l.storeLocal(slot, d)
	}

	savedDefers := l.defers
	l.defers = nil
	l.stmtList(fd.Body.Stmts)
	l.defers = savedDefers
	l.popScope()
	l.endFunction(ret)

	l.mod.Funcs = append(l.mod.Funcs, l.fn)

	l.fn, l.slotTy, l.regTy = savedFn, savedSlots, savedRegs
	l.scopes, l.loops, l.buf = savedScopes, savedLoops, savedBuf
	l.captures, l.captureTy, l.boxed = savedCaptures, savedCapTy, savedBoxed
	return name
}

func (l *lowerer) nextClosureName() string {
	l.nClosures++
	return "__clo" + itoaSmall(l.nClosures)
}

func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// envReg is the environment this function was called with, which lives
// in slot zero.
func (l *lowerer) envReg() Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: 0, Comment: "env"})
	return d
}

// captureBox is the cell holding captured variable name, read out of the
// environment.
func (l *lowerer) captureBox(name string) (Reg, bool) {
	idx, ok := l.captures[name]
	if !ok {
		return NoReg, false
	}
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: l.envReg(), B: NoReg,
		Imm: int64(idx * wordSize), Comment: "captured " + name})
	return d, true
}

// capturedRead reads a captured variable: find its cell in the
// environment, then read through the cell.
//
// Two loads rather than one, and both are the point. The first is what
// makes a closure that outlives its frame work at all; the second is
// what makes two closures over the same variable see each other.
func (l *lowerer) capturedRead(x *Ident) (Reg, bool) {
	t, known := l.captureTy[x.Name]
	if !known {
		return NoReg, false
	}
	box, ok := l.captureBox(x.Name)
	if !ok {
		return NoReg, false
	}
	d := l.newReg()
	l.regTy[d] = t
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: box, B: NoReg, Imm: 0,
		Comment: x.Name})
	if x.Narrowed && t.null {
		return l.nullValue(d, t), true
	}
	return d, true
}

// assignCaptured writes to a captured variable, through its cell.
// Reported as handled so the caller stops looking for a local slot.
func (l *lowerer) assignCaptured(st *AssignStmt, id *Ident) bool {
	t, known := l.captureTy[id.Name]
	if !known {
		return false
	}

	v := l.rvalue(st.Value)
	if st.Op == ASSIGN && t.k == kFloat && l.regTy[v].k == kInt {
		v = l.toFloat(v)
	}
	if st.Op != ASSIGN {
		cur, _ := l.capturedRead(&Ident{Name: id.Name})
		out, applied := l.compound(st, st.Op, t, cur, v)
		if !applied {
			return true
		}
		v = out
	}

	// The cell is read again rather than reused from the load above,
	// because the value between them may have called something that
	// collected - and a moving collector is not ruled out by anything
	// written here.
	box, ok := l.captureBox(id.Name)
	if !ok {
		return true
	}
	l.emit(Instr{Op: OpStoreMem, A: box, B: v, Dst: NoReg, Imm: 0,
		Comment: id.Name + " " + st.Op.String()})
	return true
}

// namedFuncValue is a declared function used as a value: a closure with
// no environment, pointing straight at the function's own code.
func (l *lowerer) namedFuncValue(name string) (Reg, vty, bool) {
	s, ok := l.sigs[name]
	if !ok {
		return NoReg, vVoid, false
	}
	t := vFuncOf(s.params, s.ret)
	clo := l.makeClosure(name, nil)
	l.regTy[clo] = t
	return clo, t, true
}

// callClosure calls a function value.
func (l *lowerer) callClosure(f Reg, args []Reg, argTypes []vty, ret vty) Reg {
	if len(args) > l.fn.MaxCallArgs {
		l.fn.MaxCallArgs = len(args)
	}
	d := NoReg
	if ret.k != kVoid || ret.res || ret.null {
		d = l.newReg()
		l.regTy[d] = ret
	}
	l.emit(Instr{Op: OpCallClosure, Dst: d, A: f, B: NoReg, Args: args,
		ArgTypes: argTypes, RetType: ret, Comment: "call through a value"})
	return d
}

// callValue lowers a call whose callee is an expression producing a
// function value.
func (l *lowerer) callValue(c *Call) Reg {
	f := l.expr(c.Callee)
	t := l.regTy[f]
	if t.k != kFunc || t.fn == nil {
		l.errorAt(c, "this is not something that can be called")
		return l.junk()
	}
	if len(c.Args) != len(t.fn.params) {
		l.errorAt(c, "this function takes %d argument(s), got %d",
			len(t.fn.params), len(c.Args))
		return l.junk()
	}

	args := make([]Reg, len(c.Args))
	for i, a := range c.Args {
		v := l.rvalueAs(a, t.fn.params[i])
		if t.fn.params[i].k == kFloat && l.regTy[v].k == kInt {
			v = l.toFloat(v)
		}
		args[i] = v
	}
	return l.callClosure(f, args, t.fn.params, t.fn.ret)
}
