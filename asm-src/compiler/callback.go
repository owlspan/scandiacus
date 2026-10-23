package main

// Callbacks: a Veyl function handed to native code as a C function
// pointer.
//
//	extern fn EnumWindows(each: fn(ptr, ptr) -> bool, data: ptr) -> bool from "user32"
//
//	fn onWindow(hwnd: int, data: int) -> bool { ... }
//	EnumWindows(onWindow, 0)
//
// A Veyl function already follows the Windows x64 convention - the
// arguments in rcx, rdx, r8, r9 or xmm0 to xmm3 by position, the rest on
// the stack, the result in rax or xmm0, and nothing the caller relies on
// left changed - so native code can call one directly. task.go starts
// threads on Veyl functions for the same reason.
//
// What does not line up is width. A C int or BOOL is 32 bits, and the
// top half of the 64-bit register it arrives in is whatever the caller
// left there, where Veyl reads every int as the whole register. So the
// pointer native code is given is not the function itself but a stub
// that sign-extends each argument the callback type declares as `int`,
// normalises each `bool` to 0 or 1, and jumps to the function. The
// function then returns straight to the native caller.
//
// The spellings follow the rest of the extern boundary: `int` and
// `bool` are the C types, `ptr` is a full 64-bit value, so a handle, a
// pointer or an LPARAM is declared `ptr`. A float is a double.
//
// Only a function declared with fn can be a callback. A closure carries
// its environment in a register native code knows nothing about.

import (
	"fmt"
	"strings"
)

// A widenArg is one callback argument the stub has to fix up.
type widenArg struct {
	pos    int
	isBool bool

	// kind, when set, is the mem* width of a fixed-width argument: an
	// integer is extended from it, an f32 (memF32) widened to the
	// double Veyl holds it as. A pos of -1 with memF32 is an f32
	// return, narrowed on the way back out.
	kind int64
}

// A Thunk is one stub: its own symbol, the Veyl function it enters, and
// the arguments to widen on the way.
type Thunk struct {
	Sym    string
	Target string
	Widen  []widenArg

	// Kinds is the target's parameters, 'f' for a float and 'i' for
	// anything else, for a callback that native code may call with the
	// System V convention; see sysvThunk.
	Kinds string
	Adapt bool
}

// callbackWidening reads a callback type as written, fn(ptr, int) ->
// bool, and lists the arguments that arrive as 32-bit C values. The
// checked type cannot answer this: ptr and int are the same type once
// checked, and the difference is exactly what matters here.
func callbackWidening(text string) []widenArg {
	s := strings.TrimSpace(text)
	if !strings.HasPrefix(s, "fn(") {
		return nil
	}
	depth, end := 0, -1
	for i := 2; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil
	}
	inner := strings.TrimSpace(s[3:end])
	if inner == "" {
		return nil
	}
	var out []widenArg
	for i, part := range strings.Split(inner, ",") {
		out = append(out, widenFor(i, part)...)
	}
	if rest := strings.TrimSpace(s[end+1:]); strings.HasPrefix(rest, "->") &&
		strings.TrimSpace(rest[2:]) == "f32" {
		out = append(out, widenArg{pos: -1, kind: memF32})
	}
	return out
}

// widenFor is what argument i, written as this type, needs on arrival.
func widenFor(i int, typ string) []widenArg {
	switch t := strings.TrimSpace(typ); {
	case t == "int":
		return []widenArg{{pos: i}}
	case t == "bool":
		return []widenArg{{pos: i, isBool: true}}
	case t == "f32":
		return []widenArg{{pos: i, kind: memF32}}
	case fixedExt[t] != 0:
		return []widenArg{{pos: i, kind: fixedExt[t]}}
	}
	return nil
}

// thunkShape names what a stub does, so two callbacks into the same
// function that widen differently get different stubs.
func thunkShape(widen []widenArg) string {
	shape := ""
	for _, w := range widen {
		kind := "i"
		switch {
		case w.isBool:
			kind = "b"
		case w.pos < 0:
			kind = "r"
		case w.kind != 0:
			kind = fmt.Sprintf("k%d_", w.kind)
		}
		shape += fmt.Sprintf("%s%d", kind, w.pos+1)
	}
	return shape
}

// callbackArg lowers the argument passed for a function-typed extern
// parameter: the address of a stub for the named function.
func (l *lowerer) callbackArg(e Expr, want vty, widen []widenArg) Reg {
	id, ok := e.(*Ident)
	if ok {
		if _, local := l.lookup(id.Name); local {
			ok = false
		}
	}
	if !ok {
		l.errorAt(e, "a callback has to be a function declared with fn, named directly - "+
			"a closure or a variable holding a function cannot be called from native code")
		return l.junk()
	}
	if _, isFn := l.sigs[id.Name]; !isFn {
		l.errorAt(e, "%s is not a function declared with fn, so it cannot be a callback", id.Name)
		return l.junk()
	}

	sym := "__vy_cb_" + sanitizeSym(id.Name) + "_" + thunkShape(widen)
	known := false
	for _, t := range l.mod.Thunks {
		if t.Sym == sym {
			known = true
			break
		}
	}
	if !known {
		kinds := ""
		for _, p := range l.sigs[id.Name].params {
			if p.k == kFloat || fixedOf(p) == "f32" {
				kinds += "f"
			} else {
				kinds += "i"
			}
		}
		l.mod.Thunks = append(l.mod.Thunks, Thunk{Sym: sym, Target: fnSym(id.Name), Widen: widen,
			Kinds: kinds, Adapt: true})
	}

	d := l.newReg()
	l.regTy[d] = want
	l.emit(Instr{Op: OpSymAddr, Dst: d, A: NoReg, B: NoReg, Sym: sym, Comment: "callback " + id.Name})
	return d
}

// The registers an argument arrives in, by position, at each width.
var (
	argRegs32 = [4]string{"ecx", "edx", "r8d", "r9d"}
	argRegs8  = [4]string{"cl", "dl", "r8b", "r9b"}
)

// thunk writes one callback stub. At entry the return address is at
// [rsp] and argument i from the fifth on is at [rsp+8+8*i], which is
// where the caller put it and where the Veyl function will look.
func (e *Emitter) thunk(t Thunk) {
	if t.Adapt && (targetLinux() || targetMac()) {
		e.sysvThunk(t)
		return
	}
	e.b.WriteString("\n" + t.Sym + ":\n")
	retF32 := false
	for _, w := range t.Widen {
		if w.pos < 0 {
			retF32 = true
			continue
		}
		if w.kind != 0 {
			e.thunkFixed(w)
			continue
		}
		if w.pos < 4 {
			r64, r32, r8 := argRegs[w.pos], argRegs32[w.pos], argRegs8[w.pos]
			if w.isBool {
				e.line("test %s, %s", r32, r32)
				e.line("setne %s", r8)
				e.line("movzx %s, %s", r32, r8)
			} else {
				e.line("movsxd %s, %s", r64, r32)
			}
			continue
		}
		at := fmt.Sprintf("qword ptr [rsp+%d]", 8+8*w.pos)
		at32 := fmt.Sprintf("dword ptr [rsp+%d]", 8+8*w.pos)
		if w.isBool {
			e.line("mov eax, %s", at32)
			e.line("test eax, eax")
			e.line("setne al")
			e.line("movzx eax, al")
		} else {
			e.line("movsxd rax, %s", at32)
		}
		e.line("mov %s, rax", at)
	}
	if retF32 {
		// The Veyl function returns a double; native code wants a
		// single. Called rather than jumped to, so there is a way back
		// through here. The checker allows this with register arguments
		// only, whose places do not move when this frame is pushed.
		e.line("sub rsp, 40")
		e.line("call %s", t.Target)
		e.line("cvtsd2ss xmm0, xmm0")
		e.line("add rsp, 40")
		e.line("ret")
		return
	}
	e.line("jmp %s", t.Target)
}

// sysvThunk is the stub on Linux and macOS, where native code calls
// with the System V convention: integers in rdi, rsi, rdx, rcx, r8 and
// r9 and floats in xmm0 to xmm7, each counted separately, the rest on
// the stack, and no shadow space. A Veyl function takes the Windows x64
// convention, which every Veyl call on every system uses, so the stub
// saves the incoming registers, lays the arguments out the Windows way
// - positions 0 to 3 in registers, the rest above 32 bytes of shadow
// space - widening each as thunk does, and calls the function. The
// registers System V wants kept, rbx, rbp and r12 to r15, are ones the
// Windows convention keeps too.
func (e *Emitter) sysvThunk(t Thunk) {
	e.b.WriteString("\n" + t.Sym + ":\n")
	n := len(t.Kinds)
	stackArgs := 0
	if n > 4 {
		stackArgs = n - 4
	}
	frame := 112 + 32 + 8*stackArgs
	if frame%16 != 0 {
		frame += 16 - frame%16
	}
	e.line("push rbp")
	e.line("mov rbp, rsp")
	e.line("sub rsp, %d", frame)
	for i, r := range []string{"rdi", "rsi", "rdx", "rcx", "r8", "r9"} {
		e.line("mov qword ptr [rbp-%d], %s", 8+8*i, r)
	}
	for i := 0; i < 8; i++ {
		e.line("movsd qword ptr [rbp-%d], xmm%d", 56+8*i, i)
	}
	widenAt := map[int]widenArg{}
	retF32 := false
	for _, w := range t.Widen {
		if w.pos < 0 {
			retF32 = true
			continue
		}
		widenAt[w.pos] = w
	}
	ints, floats, spilled := 0, 0, 0
	for i, k := range t.Kinds {
		var src string
		if k == 'f' {
			if floats < 8 {
				src = fmt.Sprintf("[rbp-%d]", 56+8*floats)
			} else {
				src = fmt.Sprintf("[rbp+%d]", 16+8*spilled)
				spilled++
			}
			floats++
			x := "xmm5"
			if i < 4 {
				x = xmmArgs[i]
			}
			if w, ok := widenAt[i]; ok && w.kind == memF32 {
				e.line("cvtss2sd %s, dword ptr %s", x, src)
			} else {
				e.line("movsd %s, qword ptr %s", x, src)
			}
			if i >= 4 {
				e.line("movsd qword ptr [rsp+%d], xmm5", 32+8*(i-4))
			}
			continue
		}
		if ints < 6 {
			src = fmt.Sprintf("[rbp-%d]", 8+8*ints)
		} else {
			src = fmt.Sprintf("[rbp+%d]", 16+8*spilled)
			spilled++
		}
		ints++
		w, ok := widenAt[i]
		switch {
		case !ok:
			e.line("mov rax, qword ptr %s", src)
		case w.isBool:
			e.line("mov eax, dword ptr %s", src)
			e.line("test eax, eax")
			e.line("setne al")
			e.line("movzx eax, al")
		case w.kind == memI8:
			e.line("movsx rax, byte ptr %s", src)
		case w.kind == memU8:
			e.line("movzx eax, byte ptr %s", src)
		case w.kind == memI16:
			e.line("movsx rax, word ptr %s", src)
		case w.kind == memU16:
			e.line("movzx eax, word ptr %s", src)
		case w.kind == memU32:
			e.line("mov eax, dword ptr %s", src)
		default:
			e.line("movsxd rax, dword ptr %s", src)
		}
		if i < 4 {
			e.line("mov %s, rax", argRegs[i])
		} else {
			e.line("mov qword ptr [rsp+%d], rax", 32+8*(i-4))
		}
	}
	e.line("call %s", t.Target)
	if retF32 {
		e.line("cvtsd2ss xmm0, xmm0")
	}
	e.line("mov rsp, rbp")
	e.line("pop rbp")
	e.line("ret")
}

// The 16-bit names of the argument registers.
var argRegs16 = [4]string{"cx", "dx", "r8w", "r9w"}

// thunkFixed widens one fixed-width argument into the form Veyl holds
// it in: an integer extended from its width, an f32 made a double.
func (e *Emitter) thunkFixed(w widenArg) {
	if w.kind == memF32 {
		if w.pos < 4 {
			x := xmmArgs[w.pos]
			e.line("cvtss2sd %s, %s", x, x)
			return
		}
		at := fmt.Sprintf("[rsp+%d]", 8+8*w.pos)
		e.line("cvtss2sd xmm5, dword ptr %s", at)
		e.line("movsd qword ptr %s, xmm5", at)
		return
	}
	if w.pos < 4 {
		r64, r32 := argRegs[w.pos], argRegs32[w.pos]
		switch w.kind {
		case memI8:
			e.line("movsx %s, %s", r64, argRegs8[w.pos])
		case memU8:
			e.line("movzx %s, %s", r32, argRegs8[w.pos])
		case memI16:
			e.line("movsx %s, %s", r64, argRegs16[w.pos])
		case memU16:
			e.line("movzx %s, %s", r32, argRegs16[w.pos])
		case memI32:
			e.line("movsxd %s, %s", r64, r32)
		case memU32:
			e.line("mov %s, %s", r32, r32)
		}
		return
	}
	at := fmt.Sprintf("[rsp+%d]", 8+8*w.pos)
	switch w.kind {
	case memI8:
		e.line("movsx rax, byte ptr %s", at)
	case memU8:
		e.line("movzx eax, byte ptr %s", at)
	case memI16:
		e.line("movsx rax, word ptr %s", at)
	case memU16:
		e.line("movzx eax, word ptr %s", at)
	case memI32:
		e.line("movsxd rax, dword ptr %s", at)
	case memU32:
		e.line("mov eax, dword ptr %s", at)
	}
	e.line("mov qword ptr %s, rax", at)
}
