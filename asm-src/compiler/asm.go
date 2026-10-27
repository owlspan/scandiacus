package main

// Inline assembly. See frontend/asm.go for the rules; this is how it
// runs.
//
//	asm(rcx = n, rax -> count) {
//	    "popcnt rax, rcx"
//	}
//
// The values cross through a few words of the frame rather than through
// virtual registers. Each input is stored to its word, OpAsm loads every
// word into its machine register, runs the lines and stores each output
// register to its word, and the outputs are read back and assigned as
// any value is. So OpAsm has no operands and defines nothing, and no
// pass has to learn about an instruction with several results: to all
// of them it is an op they do not know, which they already treat as
// writing anywhere. The words are in the array area (arrays.go), which
// is addressed from rsp and stays put.
//
// The lines can use any register. A function with asm in it keeps
// nothing in a machine register across instructions (no pooled values,
// no promoted slots), and its prologue saves each callee-saved register
// the asm names; see asmClobbers.
//
// Every line is put through the byte encoder while lowering, whatever
// the target. That is what makes a program build everywhere or nowhere:
// the Linux and macOS builds hand the same text to GNU as, which takes
// far more, and an instruction only those would accept would be one
// the Windows build refused.

import (
	"fmt"
	"strings"
)

// An asmBlock is one asm statement, as OpAsm's Imm indexes Func.Asm.
type asmBlock struct {
	lines  []string
	labels map[string]bool
	base   int64 // its words in the array area
	in     []asmReg
	out    []asmReg
}

type asmReg struct {
	reg string
	xmm bool
	// f32 is a single in the register, which a variable holds as the
	// double of the same value (fixed.go).
	f32 bool
}

func (l *lowerer) asmStmt(st *AsmStmt) {
	blk := &asmBlock{labels: map[string]bool{}}
	for _, ln := range st.Lines {
		text := asmNormalize(ln.Text)
		if strings.HasSuffix(text, ":") {
			blk.labels[strings.TrimSuffix(text, ":")] = true
		}
		blk.lines = append(blk.lines, text)
	}
	for i, ln := range blk.lines {
		if err := asmEncodes(ln, blk.labels); err != "" {
			l.errorAt(st.Lines[i], "%s", err)
		}
	}

	words := int64(max(len(st.In), len(st.Out)))
	blk.base = l.fn.ArrayBytes
	l.fn.ArrayBytes += words * wordSize
	area := l.newReg()
	l.regTy[area] = vInt
	l.emit(Instr{Op: OpFrameAddr, Dst: area, A: NoReg, B: NoReg, Imm: blk.base})

	for i, op := range st.In {
		v := l.rvalue(op.X)
		t := l.regTy[v]
		blk.in = append(blk.in, asmRegFor(op.Reg, t))
		l.pokeWord(area, int64(i)*wordSize, v)
	}
	for _, op := range st.Out {
		t, _ := vtyOf(op.T)
		blk.out = append(blk.out, asmRegFor(op.Reg, t))
	}
	l.fn.Asm = append(l.fn.Asm, blk)
	l.emit(Instr{Op: OpAsm, Dst: NoReg, A: NoReg, B: NoReg, Imm: int64(len(l.fn.Asm) - 1),
		Comment: "asm"})

	if len(st.Out) > 0 {
		// Worked out again rather than kept, so no value lives across
		// the asm: it would have to wait in memory.
		area = l.newReg()
		l.regTy[area] = vInt
		l.emit(Instr{Op: OpFrameAddr, Dst: area, A: NoReg, B: NoReg, Imm: blk.base})
	}
	for i, op := range st.Out {
		t, _ := vtyOf(op.T)
		raw := l.newReg()
		l.regTy[raw] = vInt
		if t.k == kFloat {
			l.regTy[raw] = vFloat
		}
		l.emit(Instr{Op: OpLoadMem, Dst: raw, A: area, B: NoReg, Imm: int64(i) * wordSize})
		var v Reg
		switch {
		case t.k == kBool:
			v = l.compare(OpNe, raw, l.constant(0))
		case fixedOf(t) != "" && fixedOf(t) != "f32":
			v = l.wrapFixed(raw, t)
		default:
			v = l.retype(raw, t)
		}
		l.assignValue(op, op.X.(*Ident), v)
	}
}

func asmRegFor(reg string, t vty) asmReg {
	return asmReg{reg: reg, xmm: t.k == kFloat, f32: fixedOf(t) == "f32"}
}

// assignValue assigns a value already worked out to a name, by every
// route an assignment takes - a local, a captured one, a global - by
// way of a hidden local holding it.
func (l *lowerer) assignValue(n Node, id *Ident, v Reg) {
	const hidden = "asm output" // a space: no program can name it
	l.pushScope()
	slot := l.declare(hidden, l.regTy[v])
	l.storeLocal(slot, v)
	line, col := n.Pos()
	l.assign(&AssignStmt{Span: Span{Line: line, Col: col}, Target: id, Op: ASSIGN,
		Value: &Ident{Span: Span{Line: line, Col: col}, Name: hidden}})
	l.popScope()
}

// asmEncodes reports why the encoder cannot take a line, or "". A jump
// has to land on a label of the same block and a call has to go through
// a register: a symbol is spelled differently on each system, and the
// code has no business with the program's own.
func asmEncodes(line string, labels map[string]bool) string {
	if line == "" || strings.HasSuffix(line, ":") {
		return ""
	}
	if strings.HasPrefix(line, ".byte ") {
		return "" // checked by the frontend
	}
	if err := asmShape(line); err != nil {
		return fmt.Sprintf("%q: %s", line, err)
	}
	var b block
	if err := b.encodeLine(line); err != nil {
		msg := err.Error()
		if i := strings.LastIndex(msg, ": "); i >= 0 {
			msg = msg[i+2:]
		}
		return fmt.Sprintf("cannot encode %q: %s", line, msg)
	}
	for _, it := range b.items {
		if it.branch && it.code == 0xE8 {
			return fmt.Sprintf("%q: asm can call only through a register, as in call rax", line)
		}
		if it.branch && !labels[it.target] {
			return fmt.Sprintf("%q: %s is not a label in this asm block", line, it.target)
		}
		if len(it.rel) > 0 {
			return fmt.Sprintf("%q: asm cannot name a symbol", line)
		}
	}
	return ""
}

// asm writes one asm statement. Labels are renamed apart, since the
// statement can be written out more than once (an unrolled loop) and
// two blocks can use the same name.
func (e *Emitter) asm(blk *asmBlock) {
	e.nAsm++
	prefix := fmt.Sprintf(".Lasm%d_", e.nAsm)
	word := func(i int) string {
		return fmt.Sprintf("qword ptr [rsp+%d]", int64(e.outgoing())+blk.base+int64(i)*wordSize)
	}
	for i, r := range blk.in {
		switch {
		case r.xmm:
			e.line("movsd %s, %s", r.reg, word(i))
			if r.f32 {
				e.line("cvtsd2ss %s, %s", r.reg, r.reg)
			}
		default:
			e.line("mov %s, %s", r.reg, word(i))
		}
	}
	for _, ln := range blk.lines {
		if name, isLabel := strings.CutSuffix(ln, ":"); isLabel {
			e.label(prefix + name)
			continue
		}
		e.line("%s", asmRename(ln, blk.labels, prefix))
	}
	for i, r := range blk.out {
		switch {
		case r.xmm:
			if r.f32 {
				e.line("cvtss2sd %s, %s", r.reg, r.reg)
			}
			e.line("movsd %s, %s", word(i), r.reg)
		default:
			e.line("mov %s, %s", word(i), r.reg)
		}
	}
	if asmUsesYmm(blk) {
		// The compiler's own float code is SSE, and on many CPUs an SSE
		// instruction that runs while a ymm register's upper half is
		// dirty pays a transition penalty. Not added to a block that
		// names no ymm register, so those still run without AVX.
		e.line("vzeroupper")
	}
}

// asmUsesYmm reports a block with a line that names a ymm register.
func asmUsesYmm(blk *asmBlock) bool {
	for _, ln := range blk.lines {
		if strings.HasPrefix(ln, ".byte ") {
			continue
		}
		for _, w := range strings.FieldsFunc(ln, func(c rune) bool {
			return !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9')
		}) {
			if strings.HasPrefix(w, "ymm") {
				return true
			}
		}
	}
	return false
}

// asmRename points the operands that name a label at its renamed form.
func asmRename(line string, labels map[string]bool, prefix string) string {
	sp := strings.IndexByte(line, ' ')
	if sp < 0 || len(labels) == 0 {
		return line
	}
	ops := strings.Split(line[sp+1:], ",")
	for i, op := range ops {
		if labels[strings.TrimSpace(op)] {
			ops[i] = prefix + strings.TrimSpace(op)
		}
	}
	return line[:sp+1] + strings.Join(ops, ",")
}

// asmClobbers is the callee-saved registers a function's asm may write:
// each one a line names, in any width, and rbx for cpuid, the one
// instruction in asmset.go that writes a callee-saved register without
// naming it. A line of .byte could be anything, so it counts as all.
func asmClobbers(f *Func) map[string]bool {
	used := map[string]bool{}
	all := append(append([]string{}, calleeSaved...), calleeSavedXmm...)
	for _, blk := range f.Asm {
		for _, r := range append(append([]asmReg{}, blk.in...), blk.out...) {
			used[r.reg] = true
		}
		for _, ln := range blk.lines {
			if strings.HasPrefix(ln, ".byte ") {
				for _, r := range all {
					used[r] = true
				}
				continue
			}
			for _, w := range strings.FieldsFunc(ln, func(c rune) bool {
				return !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9')
			}) {
				if w == "cpuid" {
					used["rbx"] = true
				}
				used[asmFullReg(w)] = true
			}
		}
	}
	return used
}

// asmFullReg is the whole register a name is part of: rbx for bl, r12
// for r12d, xmm3 for ymm3. Anything else comes back as it is.
func asmFullReg(w string) string {
	for _, table := range [][]string{reg32, reg16, reg8} {
		if n, ok := regNum(w, table); ok {
			return reg64[n]
		}
	}
	if n, ok := xmmNum("xmm" + strings.TrimPrefix(w, "ymm")); ok && strings.HasPrefix(w, "ymm") {
		return fmt.Sprintf("xmm%d", n)
	}
	return w
}

// hasAsm reports a function with an asm statement in it.
func hasAsm(f *Func) bool { return len(f.Asm) > 0 }
