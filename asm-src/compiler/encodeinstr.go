package main

// One assembly line to bytes.
//
// The pieces of an x86-64 instruction, in the order they go out: an
// optional mandatory prefix, REX, the opcode, ModRM, SIB, the
// displacement, then the immediate. Everything below builds those in
// that order and nothing reorders them, which is most of what makes the
// encoding readable.

import (
	"fmt"
	"strings"
)

// emitByte and friends append to a block.
func (b *block) put(bs ...byte) { b.code = append(b.code, bs...) }

func (b *block) put32(v int32) {
	b.put(byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func (b *block) put64(v int64) {
	for i := 0; i < 8; i++ {
		b.put(byte(v >> (8 * i)))
	}
}

// rex works out the prefix byte, if one is needed at all.
//
// W is set for a 64-bit operation, R extends the register field, X the
// index and B the base. A byte operation on spl, bpl, sil or dil also
// needs an empty REX to reach the new register names rather than the
// legacy ah/ch/dh/bh.
func rexByte(w bool, r, x, bse int) (byte, bool) {
	v := byte(0x40)
	need := false
	if w {
		v |= 0x08
		need = true
	}
	if r >= 8 {
		v |= 0x04
		need = true
	}
	if x >= 8 {
		v |= 0x02
		need = true
	}
	if bse >= 8 {
		v |= 0x01
		need = true
	}
	return v, need
}

// modrm writes the ModRM byte, and the SIB and displacement that go with
// it, for a register field and a memory or register operand.
func (b *block) modrm(regField int, rm operand) {
	switch rm.kind {
	case opReg, opXmm:
		b.put(0xC0 | byte((regField&7)<<3) | byte(rm.reg&7))

	case opMem:
		base := rm.reg & 7
		if rm.index >= 0 {
			// [base + index*scale]: mod is 00 unless the base is rbp or
			// r13, which the encoding steals for rip-relative.
			mod := byte(0)
			if base == 5 {
				mod = 0x40
			}
			ss := byte(0)
			switch rm.scale {
			case 2:
				ss = 1
			case 4:
				ss = 2
			case 8:
				ss = 3
			}
			b.put(mod | byte((regField&7)<<3) | 4) // rm 100 means SIB
			b.put(ss<<6 | byte((rm.index&7)<<3) | byte(base))
			if base == 5 {
				b.put(0)
			}
			return
		}
		// rsp and r12 need a SIB byte even with no index, because rm 100
		// means "there is a SIB".
		needSIB := base == 4
		switch {
		case rm.disp == 0 && base != 5:
			b.put(byte((regField&7)<<3) | byte(base))
			if needSIB {
				b.put(0x24)
			}
		case rm.disp >= -128 && rm.disp <= 127:
			b.put(0x40 | byte((regField&7)<<3) | byte(base))
			if needSIB {
				b.put(0x24)
			}
			b.put(byte(rm.disp))
		default:
			b.put(0x80 | byte((regField&7)<<3) | byte(base))
			if needSIB {
				b.put(0x24)
			}
			b.put32(int32(rm.disp))
		}

	case opRipSym:
		// mod 00, rm 101 is rip-relative. The displacement is filled in
		// once the symbol has an address.
		b.put(byte((regField&7)<<3) | 5)
		b.rel = append(b.rel, reloc{at: len(b.code), sym: rm.sym})
		b.put32(int32(rm.disp))
	}
}

// prefix writes the operand-size and REX prefixes for an instruction
// whose register field is reg and whose rm operand is rm.
func (b *block) prefix(size int, reg int, rm operand, mandatory ...byte) {
	// The operand-size override comes before any mandatory prefix and
	// before REX, which has to be the last byte ahead of the opcode.
	if size == 16 {
		b.put(0x66)
	}
	b.put(mandatory...)
	x, bse := 0, 0
	switch rm.kind {
	case opReg, opXmm:
		bse = rm.reg
	case opMem:
		bse = rm.reg
		if rm.index >= 0 {
			x = rm.index
		}
	}
	byteRegs := size == 8 && (reg >= 4 && reg <= 7)
	if v, need := rexByte(size == 64, reg, x, bse); need || byteRegs {
		b.put(v)
	}
}

// encodeLine turns one line of assembly into bytes.
func (b *block) encodeLine(line string) error {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	if strings.HasSuffix(line, ":") {
		if b.at == nil {
			b.at = map[string]int{}
		}
		b.at[strings.TrimSuffix(line, ":")] = len(b.items)
		return nil
	}
	if strings.HasPrefix(line, ".") {
		return nil // a directive; the section writer deals with those
	}

	mnemonic := line
	rest := ""
	if sp := strings.IndexByte(line, ' '); sp >= 0 {
		mnemonic = line[:sp]
		rest = strings.TrimSpace(line[sp+1:])
	}
	// `lock xadd ...`: the prefix is a word of its own ahead of the
	// instruction it makes atomic.
	lock := false
	if mnemonic == "lock" {
		lock = true
		mnemonic, rest = rest, ""
		if sp := strings.IndexByte(mnemonic, ' '); sp >= 0 {
			mnemonic, rest = mnemonic[:sp], strings.TrimSpace(mnemonic[sp+1:])
		}
	}

	var ops []operand
	if rest != "" {
		for _, part := range strings.Split(rest, ",") {
			op, err := parseOperand(part)
			if err != nil {
				return fmt.Errorf("%s: %w", line, err)
			}
			ops = append(ops, op)
		}
	}

	b.code = b.code[:0]
	b.rel = b.rel[:0]
	if lock {
		b.put(0xF0)
	}
	if err := b.encode(mnemonic, ops); err != nil {
		return fmt.Errorf("%s: %w", line, err)
	}
	if len(b.code) > 0 || len(b.rel) > 0 {
		// A rip-relative displacement counts from the end of the whole
		// instruction, which is only known now: an immediate can follow
		// the displacement.
		for i := range b.rel {
			if b.rel[i].next == 0 {
				b.rel[i].next = len(b.code)
			}
		}
		b.items = append(b.items, item{
			bytes: append([]byte(nil), b.code...),
			rel:   append([]reloc(nil), b.rel...),
		})
	}
	return nil
}

// branchTo records a jump whose width is still open.
func (b *block) branchTo(esc, code byte, target string) {
	b.items = append(b.items, item{branch: true, esc: esc, code: code,
		target: target, short: true})
}

// settle lays the instructions out, choosing a width for every branch,
// and produces the final bytes.
//
// Branches start optimistic - assumed short - and any that turns out not
// to reach is widened. Widening only ever pushes targets further apart,
// so a branch never goes back to short and the loop terminates.
func (b *block) settle() {
	for {
		offsets := make([]int, len(b.items)+1)
		at := 0
		for i, it := range b.items {
			offsets[i] = at
			at += it.size()
		}
		offsets[len(b.items)] = at

		changed := false
		for i := range b.items {
			it := &b.items[i]
			if !it.branch || !it.short {
				continue
			}
			target, known := b.at[it.target]
			if !known {
				// Not a label in this block, so it is a call or a jump
				// to a symbol and has to be the wide form.
				it.short = false
				changed = true
				continue
			}
			delta := offsets[target] - (offsets[i] + 2)
			if delta < -128 || delta > 127 {
				it.short = false
				changed = true
			}
		}
		if !changed {
			b.finish(offsets)
			return
		}
	}
}

// size is how many bytes an item takes at its current width.
func (it item) size() int {
	if !it.branch {
		return len(it.bytes)
	}
	if it.short {
		return 2
	}
	if it.esc != 0 {
		return 6
	}
	return 5
}

// finish writes the settled bytes out, filling in every branch that
// stays inside this block and leaving a relocation for the rest.
func (b *block) finish(offsets []int) {
	b.code = nil
	b.relocs = nil
	b.labels = map[string]int{}
	for name, idx := range b.at {
		b.labels[name] = offsets[idx]
	}

	for i, it := range b.items {
		base := offsets[i]
		if !it.branch {
			bytes := append([]byte(nil), it.bytes...)
			for _, r := range it.rel {
				// A reference to a label in this same text is resolved
				// here. Anything else - a string in .rdata, a function
				// in another section, an imported symbol - keeps its
				// relocation for whatever links this.
				if idx, known := b.at[r.sym]; known && !r.isCall {
					// Whatever is in the field is the addend: sym[rip+8]
					// parses as a reference to sym with an eight added,
					// and overwriting rather than adding would silently
					// point at sym itself.
					addend := int32(uint32(bytes[r.at]) |
						uint32(bytes[r.at+1])<<8 |
						uint32(bytes[r.at+2])<<16 |
						uint32(bytes[r.at+3])<<24)
					delta := offsets[idx] - (base + r.next) + int(addend)
					bytes[r.at] = byte(delta)
					bytes[r.at+1] = byte(delta >> 8)
					bytes[r.at+2] = byte(delta >> 16)
					bytes[r.at+3] = byte(delta >> 24)
					continue
				}
				r.at += base
				r.next += base
				b.relocs = append(b.relocs, r)
			}
			b.code = append(b.code, bytes...)
			continue
		}

		target, known := b.at[it.target]
		if it.short {
			delta := offsets[target] - (base + 2)
			// The short conditional is 7x, which is the near 0F 8x with
			// the escape dropped and 0x10 taken off.
			op := it.code
			if it.esc != 0 {
				op = it.code - 0x10
			} else {
				op = 0xEB
			}
			b.code = append(b.code, op, byte(delta))
			continue
		}

		if it.esc != 0 {
			b.code = append(b.code, it.esc)
		}
		b.code = append(b.code, it.code)
		if known {
			delta := offsets[target] - (base + it.size())
			b.code = append(b.code, byte(delta), byte(delta>>8),
				byte(delta>>16), byte(delta>>24))
			continue
		}
		b.relocs = append(b.relocs, reloc{at: len(b.code), sym: it.target,
			next: len(b.code) + 4, isCall: true})
		b.code = append(b.code, 0, 0, 0, 0)
	}
}

// The arithmetic instructions share one encoding shape, differing only
// in an opcode base and the /digit that goes in the ModRM register field
// for the immediate form.
var arith = map[string]struct {
	rmReg byte // op rm, reg
	regRM byte // op reg, rm
	digit int  // for the immediate form
}{
	"add": {0x01, 0x03, 0},
	"or":  {0x09, 0x0B, 1},
	"and": {0x21, 0x23, 4},
	"sub": {0x29, 0x2B, 5},
	"xor": {0x31, 0x33, 6},
	"cmp": {0x39, 0x3B, 7},
}

var setcc = map[string]byte{
	"seto": 0x90, "setno": 0x91, "setb": 0x92, "setae": 0x93,
	"sete": 0x94, "setne": 0x95, "setbe": 0x96, "seta": 0x97,
	"sets": 0x98, "setns": 0x99, "setp": 0x9A, "setnp": 0x9B,
	"setl": 0x9C, "setge": 0x9D, "setle": 0x9E, "setg": 0x9F,
}

var jcc = map[string]byte{
	"jo": 0x80, "jno": 0x81, "jb": 0x82, "jae": 0x83,
	"je": 0x84, "jne": 0x85, "jbe": 0x86, "ja": 0x87,
	"js": 0x88, "jns": 0x89, "jp": 0x8A, "jnp": 0x8B,
	"jl": 0x8C, "jge": 0x8D, "jle": 0x8E, "jg": 0x8F,
}

// The scalar-double instructions are all F2 0F xx with the same shape.
var sseD = map[string]byte{
	"addsd": 0x58, "mulsd": 0x59, "subsd": 0x5C, "divsd": 0x5E,
	"sqrtsd": 0x51,
}

func (b *block) encode(m string, ops []operand) error {
	switch m {
	case "ret":
		b.put(0xC3)
		return nil
	case "cqo":
		b.put(0x48, 0x99)
		return nil
	case "nop":
		b.put(0x90)
		return nil
	}

	if op, ok := arith[m]; ok {
		return b.encodeArith(op.rmReg, op.regRM, op.digit, ops)
	}
	if code, ok := setcc[m]; ok {
		if len(ops) != 1 || ops[0].kind != opReg {
			return fmt.Errorf("setcc wants one register")
		}
		b.prefix(8, 0, ops[0])
		b.put(0x0F, code)
		b.modrm(0, ops[0])
		return nil
	}
	if code, ok := jcc[m]; ok {
		return b.encodeJump(0x0F, code, ops)
	}
	if code, ok := sseD[m]; ok {
		return b.encodeSSE(code, ops)
	}

	switch m {
	case "mov":
		return b.encodeMov(ops)
	case "lea":
		return b.encodeLea(ops)
	case "push", "pop":
		return b.encodePushPop(m, ops)
	case "jmp":
		return b.encodeJump(0, 0xE9, ops)
	case "call":
		return b.encodeCall(ops)
	case "test":
		return b.encodeTest(ops)
	case "imul":
		return b.encodeImul(ops)
	case "idiv", "neg", "not":
		return b.encodeUnary(m, ops)
	case "shl", "sar", "shr":
		return b.encodeShift(m, ops)
	case "movzx":
		return b.encodeMovzx(ops)
	case "movsxd":
		return b.encodeMovsxd(ops)
	case "movsx":
		return b.encodeMovsx(ops)
	case "cvtss2sd", "cvtsd2ss", "movss":
		return b.encodeSingle(m, ops)
	case "cmovne":
		return b.encodeCmov(0x45, ops)
	case "movsd":
		return b.encodeMovsd(ops)
	case "movups":
		return b.encodeMovups(ops)
	case "comisd", "ucomisd":
		return b.encodeComisd(m, ops)
	case "cvtsi2sd":
		return b.encodeCvtsi2sd(ops)
	case "cvttsd2si":
		return b.encodeCvttsd2si(ops)
	case "xadd":
		return b.encodeRMReg([]byte{0x0F, 0xC1}, ops)
	case "cmpxchg":
		return b.encodeRMReg([]byte{0x0F, 0xB1}, ops)
	case "xchg":
		return b.encodeRMReg([]byte{0x87}, ops)
	}
	return fmt.Errorf("no encoding for %q", m)
}

// encodeRMReg is an instruction of the form op r/m64, r64: xadd,
// cmpxchg and xchg, the three the atomics use.
func (b *block) encodeRMReg(opcode []byte, ops []operand) error {
	if len(ops) != 2 || ops[1].kind != opReg || (ops[0].kind != opReg && ops[0].kind != opMem) {
		return fmt.Errorf("wants a register or memory, then a register")
	}
	b.prefix(64, ops[1].reg, ops[0])
	b.put(opcode...)
	b.modrm(ops[1].reg, ops[0])
	return nil
}

func (b *block) encodeArith(rmReg, regRM byte, digit int, ops []operand) error {
	if len(ops) != 2 {
		return fmt.Errorf("wants two operands")
	}
	dst, src := ops[0], ops[1]

	if src.kind == opImm {
		size := dst.size
		if dst.kind == opMem && size == 0 {
			size = 64
		}
		if dst.kind == opReg && dst.reg == 0 && (size == 64 || size == 32) &&
			(src.disp < -128 || src.disp > 127) {
			// rax and eax have a form of their own with no ModRM byte,
			// one shorter, which the assembler always picks for an
			// immediate too big for a byte.
			if size == 64 {
				b.put(0x48)
			}
			b.put(byte(digit<<3 | 5))
			b.put32(int32(src.disp))
			return nil
		}
		b.prefix(size, digit, dst)
		if src.disp >= -128 && src.disp <= 127 {
			b.put(0x83)
			b.modrm(digit, dst)
			b.put(byte(src.disp))
			return nil
		}
		b.put(0x81)
		b.modrm(digit, dst)
		b.put32(int32(src.disp))
		return nil
	}

	// reg, mem goes the other way round from mem, reg: the register is
	// the ModRM register field either way, and only the opcode says
	// which one is the destination.
	if dst.kind == opReg && (src.kind == opMem || src.kind == opRipSym) {
		b.prefix(dst.size, dst.reg, src)
		b.put(regRM)
		b.modrm(dst.reg, src)
		return nil
	}
	if src.kind != opReg {
		return fmt.Errorf("unsupported operand combination")
	}
	b.prefix(src.size, src.reg, dst)
	b.put(rmReg)
	b.modrm(src.reg, dst)
	return nil
}

func (b *block) encodeMov(ops []operand) error {
	if len(ops) != 2 {
		return fmt.Errorf("mov wants two operands")
	}
	dst, src := ops[0], ops[1]

	if src.kind == opImm {
		if dst.kind == opReg {
			// A 64-bit register taking a value that does not fit in a
			// sign-extended 32 bits needs the ten-byte form.
			if dst.size == 64 && (src.disp > 0x7FFFFFFF || src.disp < -0x80000000) {
				if v, need := rexByte(true, 0, 0, dst.reg); need {
					b.put(v)
				}
				b.put(0xB8 + byte(dst.reg&7))
				b.put64(src.disp)
				return nil
			}
			if dst.size == 32 {
				if v, need := rexByte(false, 0, 0, dst.reg); need {
					b.put(v)
				}
				b.put(0xB8 + byte(dst.reg&7))
				b.put32(int32(src.disp))
				return nil
			}
			b.prefix(dst.size, 0, dst)
			b.put(0xC7)
			b.modrm(0, dst)
			b.put32(int32(src.disp))
			return nil
		}
		// Into memory.
		size := dst.size
		b.prefix(size, 0, dst)
		if size == 8 {
			b.put(0xC6)
			b.modrm(0, dst)
			b.put(byte(src.disp))
			return nil
		}
		b.put(0xC7)
		b.modrm(0, dst)
		b.put32(int32(src.disp))
		return nil
	}

	if dst.kind == opReg && (src.kind == opMem || src.kind == opRipSym) {
		size := dst.size
		b.prefix(size, dst.reg, src)
		if size == 8 {
			b.put(0x8A)
		} else {
			b.put(0x8B)
		}
		b.modrm(dst.reg, src)
		return nil
	}

	if src.kind == opReg && (dst.kind == opMem || dst.kind == opRipSym) {
		size := src.size
		b.prefix(size, src.reg, dst)
		if size == 8 {
			b.put(0x88)
		} else {
			b.put(0x89)
		}
		b.modrm(src.reg, dst)
		return nil
	}

	if dst.kind == opReg && src.kind == opReg {
		b.prefix(src.size, src.reg, dst)
		b.put(0x89)
		b.modrm(src.reg, dst)
		return nil
	}
	return fmt.Errorf("unsupported mov")
}

func (b *block) encodeLea(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("lea wants a register and a memory operand")
	}
	b.prefix(ops[0].size, ops[0].reg, ops[1])
	b.put(0x8D)
	b.modrm(ops[0].reg, ops[1])
	return nil
}

func (b *block) encodePushPop(m string, ops []operand) error {
	if len(ops) != 1 || ops[0].kind != opReg {
		return fmt.Errorf("%s wants a register", m)
	}
	if ops[0].reg >= 8 {
		b.put(0x41)
	}
	base := byte(0x50)
	if m == "pop" {
		base = 0x58
	}
	b.put(base + byte(ops[0].reg&7))
	return nil
}

// encodeJump writes a jump or a conditional jump. Both use a 32-bit
// displacement always, never the short form: the target is not known
// yet, and a fixed width means the offsets never move.
func (b *block) encodeJump(esc, code byte, ops []operand) error {
	if len(ops) != 1 || (ops[0].kind != opSym && ops[0].kind != opRipSym) {
		return fmt.Errorf("jump wants a label")
	}
	b.branchTo(esc, code, ops[0].sym)
	return nil
}

func (b *block) encodeCall(ops []operand) error {
	if len(ops) != 1 {
		return fmt.Errorf("call wants a target")
	}
	if ops[0].kind == opReg {
		b.prefix(32, 2, ops[0])
		b.put(0xFF)
		b.modrm(2, ops[0])
		return nil
	}
	// A call goes through the branch machinery so that a target defined
	// in this same text - every helper written in x64.go is one - is
	// resolved here rather than left for a linker. It never shortens:
	// there is no two-byte call.
	b.items = append(b.items, item{branch: true, esc: 0, code: 0xE8,
		target: ops[0].sym, short: false})
	return nil
}

func (b *block) encodeTest(ops []operand) error {
	if len(ops) != 2 || ops[1].kind != opReg {
		return fmt.Errorf("test wants two registers")
	}
	b.prefix(ops[1].size, ops[1].reg, ops[0])
	b.put(0x85)
	b.modrm(ops[1].reg, ops[0])
	return nil
}

func (b *block) encodeImul(ops []operand) error {
	if len(ops) == 3 && ops[0].kind == opReg && ops[2].kind == opImm {
		// imul r, r/m, imm: a multiply by a constant into a register.
		b.prefix(ops[0].size, ops[0].reg, ops[1])
		if ops[2].disp >= -128 && ops[2].disp <= 127 {
			b.put(0x6B)
			b.modrm(ops[0].reg, ops[1])
			b.put(byte(ops[2].disp))
			return nil
		}
		b.put(0x69)
		b.modrm(ops[0].reg, ops[1])
		b.put32(int32(ops[2].disp))
		return nil
	}
	if len(ops) == 1 {
		// The one-operand form: rdx:rax = rax * r/m, signed. The high
		// word is what division by a constant needs; see divconst.go.
		b.prefix(ops[0].size, 5, ops[0])
		b.put(0xF7)
		b.modrm(5, ops[0])
		return nil
	}
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("imul wants a register destination")
	}
	b.prefix(ops[0].size, ops[0].reg, ops[1])
	b.put(0x0F, 0xAF)
	b.modrm(ops[0].reg, ops[1])
	return nil
}

func (b *block) encodeUnary(m string, ops []operand) error {
	digit := map[string]int{"not": 2, "neg": 3, "idiv": 7}[m]
	if len(ops) != 1 {
		return fmt.Errorf("%s wants one operand", m)
	}
	b.prefix(ops[0].size, digit, ops[0])
	b.put(0xF7)
	b.modrm(digit, ops[0])
	return nil
}

func (b *block) encodeShift(m string, ops []operand) error {
	digit := map[string]int{"shl": 4, "shr": 5, "sar": 7}[m]
	if len(ops) != 2 {
		return fmt.Errorf("%s wants two operands", m)
	}
	dst, amount := ops[0], ops[1]
	b.prefix(dst.size, digit, dst)
	if amount.kind == opReg {
		// Only cl is allowed, which is register 1 at byte size.
		b.put(0xD3)
		b.modrm(digit, dst)
		return nil
	}
	if amount.disp == 1 {
		// A shift by one has its own opcode with no immediate byte, and
		// the assembler always picks it.
		b.put(0xD1)
		b.modrm(digit, dst)
		return nil
	}
	b.put(0xC1)
	b.modrm(digit, dst)
	b.put(byte(amount.disp))
	return nil
}

func (b *block) encodeMovzx(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("movzx wants a register destination")
	}
	b.prefix(ops[0].size, ops[0].reg, ops[1])
	if ops[1].size == 16 {
		b.put(0x0F, 0xB7)
	} else {
		b.put(0x0F, 0xB6)
	}
	b.modrm(ops[0].reg, ops[1])
	return nil
}

// movsx sign-extends a byte or a word; movsxd is the dword form.
func (b *block) encodeMovsx(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("movsx wants a register destination")
	}
	b.prefix(ops[0].size, ops[0].reg, ops[1])
	if ops[1].size == 16 {
		b.put(0x0F, 0xBF)
	} else {
		b.put(0x0F, 0xBE)
	}
	b.modrm(ops[0].reg, ops[1])
	return nil
}

// The single-precision instructions, which are only ever used to cross
// between a float in memory and the double every Veyl float is:
// cvtss2sd widens, cvtsd2ss narrows, movss loads or stores four bytes.
func (b *block) encodeSingle(m string, ops []operand) error {
	if len(ops) != 2 {
		return fmt.Errorf("%s wants two operands", m)
	}
	dst, src := ops[0], ops[1]
	switch m {
	case "cvtss2sd", "cvtsd2ss":
		if dst.kind != opXmm {
			return fmt.Errorf("%s wants an xmm destination", m)
		}
		pfx := byte(0xF3)
		if m == "cvtsd2ss" {
			pfx = 0xF2
		}
		b.prefix(32, dst.reg, src, pfx)
		b.put(0x0F, 0x5A)
		b.modrm(dst.reg, src)
		return nil
	}
	if dst.kind == opXmm {
		b.prefix(32, dst.reg, src, 0xF3)
		b.put(0x0F, 0x10)
		b.modrm(dst.reg, src)
		return nil
	}
	if src.kind != opXmm {
		return fmt.Errorf("movss needs an xmm register")
	}
	b.prefix(32, src.reg, dst, 0xF3)
	b.put(0x0F, 0x11)
	b.modrm(src.reg, dst)
	return nil
}

func (b *block) encodeMovsxd(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("movsxd wants a register destination")
	}
	b.prefix(64, ops[0].reg, ops[1])
	b.put(0x63)
	b.modrm(ops[0].reg, ops[1])
	return nil
}

func (b *block) encodeCmov(code byte, ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("cmov wants a register destination")
	}
	b.prefix(ops[0].size, ops[0].reg, ops[1])
	b.put(0x0F, code)
	b.modrm(ops[0].reg, ops[1])
	return nil
}

// movsd moves a double either way, so which operand is the register
// decides the opcode: 10 loads, 11 stores.
func (b *block) encodeMovsd(ops []operand) error {
	if len(ops) != 2 {
		return fmt.Errorf("movsd wants two operands")
	}
	dst, src := ops[0], ops[1]
	if dst.kind == opXmm {
		b.prefix(32, dst.reg, src, 0xF2)
		b.put(0x0F, 0x10)
		b.modrm(dst.reg, src)
		return nil
	}
	if src.kind != opXmm {
		return fmt.Errorf("movsd needs an xmm register")
	}
	b.prefix(32, src.reg, dst, 0xF2)
	b.put(0x0F, 0x11)
	b.modrm(src.reg, dst)
	return nil
}

// encodeMovups moves all 128 bits of an xmm register to or from memory,
// which saving a callee-saved one needs; movsd would move only the low
// half and zero the rest on the way back.
func (b *block) encodeMovups(ops []operand) error {
	if len(ops) != 2 {
		return fmt.Errorf("movups wants two operands")
	}
	dst, src := ops[0], ops[1]
	if dst.kind == opXmm {
		b.prefix(32, dst.reg, src)
		b.put(0x0F, 0x10)
		b.modrm(dst.reg, src)
		return nil
	}
	if src.kind != opXmm {
		return fmt.Errorf("movups needs an xmm register")
	}
	b.prefix(32, src.reg, dst)
	b.put(0x0F, 0x11)
	b.modrm(src.reg, dst)
	return nil
}

func (b *block) encodeSSE(code byte, ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opXmm {
		return fmt.Errorf("wants an xmm destination")
	}
	b.prefix(32, ops[0].reg, ops[1], 0xF2)
	b.put(0x0F, code)
	b.modrm(ops[0].reg, ops[1])
	return nil
}

// comisd and ucomisd carry the 66 prefix rather than F2, being the
// ordered and unordered compares of a packed-double encoding.
func (b *block) encodeComisd(m string, ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opXmm {
		return fmt.Errorf("%s wants an xmm destination", m)
	}
	code := byte(0x2F)
	if m == "ucomisd" {
		code = 0x2E
	}
	b.prefix(32, ops[0].reg, ops[1], 0x66)
	b.put(0x0F, code)
	b.modrm(ops[0].reg, ops[1])
	return nil
}

func (b *block) encodeCvtsi2sd(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opXmm {
		return fmt.Errorf("cvtsi2sd wants an xmm destination")
	}
	b.prefix(ops[1].size, ops[0].reg, ops[1], 0xF2)
	b.put(0x0F, 0x2A)
	b.modrm(ops[0].reg, ops[1])
	return nil
}

func (b *block) encodeCvttsd2si(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("cvttsd2si wants a register destination")
	}
	b.prefix(ops[0].size, ops[0].reg, ops[1], 0xF2)
	b.put(0x0F, 0x2C)
	b.modrm(ops[0].reg, ops[1])
	return nil
}
