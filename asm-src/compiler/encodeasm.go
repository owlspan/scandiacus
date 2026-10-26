package main

// Encodings that only asm statements use. The compiler's own output
// never needs them; asmset.go lists which forms of each an asm line may
// write, and asmset_test.go holds every one of those to GNU as.

import "fmt"

// asmNullary are the instructions with no operands.
var asmNullary = map[string][]byte{
	"pause":  {0xF3, 0x90},
	"lfence": {0x0F, 0xAE, 0xE8},
	"mfence": {0x0F, 0xAE, 0xF0},
	"sfence": {0x0F, 0xAE, 0xF8},
	"rdtsc":  {0x0F, 0x31},
	"rdtscp": {0x0F, 0x01, 0xF9},
	"int3":   {0xCC},
	"ud2":    {0x0F, 0x0B},
	"cdq":    {0x99},
	"cdqe":   {0x48, 0x98},
}

// asmGPOps are general-register instructions of the form op reg, r/m:
// a mandatory prefix (or 0) and the opcode after 0F.
var asmGPOps = map[string]struct {
	pfx  byte
	code []byte
}{
	"bsf":    {0, []byte{0x0F, 0xBC}},
	"bsr":    {0, []byte{0x0F, 0xBD}},
	"popcnt": {0xF3, []byte{0x0F, 0xB8}},
	"lzcnt":  {0xF3, []byte{0x0F, 0xBD}},
	"tzcnt":  {0xF3, []byte{0x0F, 0xBC}},
	"crc32":  {0xF2, []byte{0x0F, 0x38, 0xF1}},
}

var asmCmov = map[string]byte{
	"cmovo": 0x40, "cmovno": 0x41, "cmovb": 0x42, "cmovae": 0x43,
	"cmove": 0x44, "cmovne": 0x45, "cmovbe": 0x46, "cmova": 0x47,
	"cmovs": 0x48, "cmovns": 0x49, "cmovp": 0x4A, "cmovnp": 0x4B,
	"cmovl": 0x4C, "cmovge": 0x4D, "cmovle": 0x4E, "cmovg": 0x4F,
}

// asmXmmOps are SSE instructions of the form op xmm, xmm/m: a
// mandatory prefix (or 0), the opcode after 0F, and whether an
// immediate byte follows.
var asmXmmOps = map[string]struct {
	pfx  byte
	code []byte
	imm  bool
}{
	"minsd": {0xF2, []byte{0x5D}, false}, "maxsd": {0xF2, []byte{0x5F}, false},
	"addss": {0xF3, []byte{0x58}, false}, "subss": {0xF3, []byte{0x5C}, false},
	"mulss": {0xF3, []byte{0x59}, false}, "divss": {0xF3, []byte{0x5E}, false},
	"sqrtss": {0xF3, []byte{0x51}, false}, "minss": {0xF3, []byte{0x5D}, false},
	"maxss": {0xF3, []byte{0x5F}, false},

	"addps": {0, []byte{0x58}, false}, "subps": {0, []byte{0x5C}, false},
	"mulps": {0, []byte{0x59}, false}, "divps": {0, []byte{0x5E}, false},
	"sqrtps": {0, []byte{0x51}, false}, "minps": {0, []byte{0x5D}, false},
	"maxps": {0, []byte{0x5F}, false}, "andps": {0, []byte{0x54}, false},
	"andnps": {0, []byte{0x55}, false}, "orps": {0, []byte{0x56}, false},
	"xorps": {0, []byte{0x57}, false}, "unpcklps": {0, []byte{0x14}, false},
	"unpckhps": {0, []byte{0x15}, false}, "shufps": {0, []byte{0xC6}, true},
	"comiss": {0, []byte{0x2F}, false}, "ucomiss": {0, []byte{0x2E}, false},

	"divpd": {0x66, []byte{0x5E}, false}, "sqrtpd": {0x66, []byte{0x51}, false},
	"minpd": {0x66, []byte{0x5D}, false}, "maxpd": {0x66, []byte{0x5F}, false},
	"andpd": {0x66, []byte{0x54}, false}, "andnpd": {0x66, []byte{0x55}, false},
	"orpd": {0x66, []byte{0x56}, false}, "xorpd": {0x66, []byte{0x57}, false},
	"unpckhpd": {0x66, []byte{0x15}, false}, "shufpd": {0x66, []byte{0xC6}, true},
	"haddps": {0xF2, []byte{0x7C}, false}, "haddpd": {0x66, []byte{0x7C}, false},

	"paddb": {0x66, []byte{0xFC}, false}, "paddw": {0x66, []byte{0xFD}, false},
	"paddd": {0x66, []byte{0xFE}, false}, "paddq": {0x66, []byte{0xD4}, false},
	"psubb": {0x66, []byte{0xF8}, false}, "psubw": {0x66, []byte{0xF9}, false},
	"psubd": {0x66, []byte{0xFA}, false}, "psubq": {0x66, []byte{0xFB}, false},
	"pand": {0x66, []byte{0xDB}, false}, "pandn": {0x66, []byte{0xDF}, false},
	"por": {0x66, []byte{0xEB}, false}, "pxor": {0x66, []byte{0xEF}, false},
	"pcmpeqb": {0x66, []byte{0x74}, false}, "pcmpeqd": {0x66, []byte{0x76}, false},
	"pmullw": {0x66, []byte{0xD5}, false}, "pmuludq": {0x66, []byte{0xF4}, false},
	"pmulld": {0x66, []byte{0x38, 0x40}, false}, "pshufd": {0x66, []byte{0x70}, true},
	"roundsd": {0x66, []byte{0x3A, 0x0B}, true}, "roundss": {0x66, []byte{0x3A, 0x0A}, true},

	"cvtss2si": {0xF3, []byte{0x2D}, false}, "cvttss2si": {0xF3, []byte{0x2C}, false},
	"cvtsd2si": {0xF2, []byte{0x2D}, false},
}

// asmXmmMoves load with the first opcode and store with the second.
var asmXmmMoves = map[string]struct {
	pfx         byte
	load, store byte
}{
	"movaps": {0, 0x28, 0x29},
	"movdqa": {0x66, 0x6F, 0x7F},
	"movdqu": {0xF3, 0x6F, 0x7F},
}

// encodeMore writes the asm-only instructions, reporting whether m is
// one of them.
func (b *block) encodeMore(m string, ops []operand) (bool, error) {
	if code, ok := asmNullary[m]; ok {
		b.put(code...)
		return true, nil
	}
	if op, ok := asmGPOps[m]; ok {
		if len(ops) != 2 || ops[0].kind != opReg {
			return true, fmt.Errorf("%s wants a register destination", m)
		}
		if op.pfx != 0 {
			b.prefix(ops[0].size, ops[0].reg, ops[1], op.pfx)
		} else {
			b.prefix(ops[0].size, ops[0].reg, ops[1])
		}
		b.put(op.code...)
		b.modrm(ops[0].reg, ops[1])
		return true, nil
	}
	if code, ok := asmCmov[m]; ok {
		return true, b.encodeCmov(code, ops)
	}
	if op, ok := asmXmmOps[m]; ok {
		want := 2
		if op.imm {
			want = 3
		}
		if len(ops) != want || (ops[0].kind != opXmm && ops[0].kind != opReg) {
			return true, fmt.Errorf("%s wants a register destination", m)
		}
		size := 32
		if ops[0].kind == opReg {
			size = ops[0].size // cvtss2si rax: W from the destination
		}
		if op.pfx != 0 {
			b.prefix(size, ops[0].reg, ops[1], op.pfx)
		} else {
			b.prefix(size, ops[0].reg, ops[1])
		}
		b.put(0x0F)
		b.put(op.code...)
		b.modrm(ops[0].reg, ops[1])
		if op.imm {
			b.put(byte(ops[2].disp))
		}
		return true, nil
	}
	if op, ok := asmXmmMoves[m]; ok {
		if len(ops) != 2 {
			return true, fmt.Errorf("%s wants two operands", m)
		}
		code, x, rm := op.load, ops[0], ops[1]
		if x.kind != opXmm {
			code, x, rm = op.store, ops[1], ops[0]
		}
		if op.pfx != 0 {
			b.prefix(32, x.reg, rm, op.pfx)
		} else {
			b.prefix(32, x.reg, rm)
		}
		b.put(0x0F, code)
		b.modrm(x.reg, rm)
		return true, nil
	}

	switch m {
	case "inc", "dec":
		digit := 0
		if m == "dec" {
			digit = 1
		}
		if len(ops) != 1 {
			return true, fmt.Errorf("%s wants one operand", m)
		}
		b.prefix(ops[0].size, digit, ops[0])
		b.put(0xFF)
		b.modrm(digit, ops[0])
		return true, nil
	case "bswap":
		if len(ops) != 1 || ops[0].kind != opReg {
			return true, fmt.Errorf("bswap wants a register")
		}
		if v, need := rexByte(ops[0].size == 64, 0, 0, ops[0].reg); need {
			b.put(v)
		}
		b.put(0x0F, 0xC8+byte(ops[0].reg&7))
		return true, nil
	case "bt":
		if len(ops) != 2 {
			return true, fmt.Errorf("bt wants two operands")
		}
		if ops[1].kind == opImm {
			b.prefix(ops[0].size, 4, ops[0])
			b.put(0x0F, 0xBA)
			b.modrm(4, ops[0])
			b.put(byte(ops[1].disp))
			return true, nil
		}
		b.prefix(ops[1].size, ops[1].reg, ops[0])
		b.put(0x0F, 0xA3)
		b.modrm(ops[1].reg, ops[0])
		return true, nil
	case "movd", "movq":
		// Between a general register and an xmm one: 6E into the xmm,
		// 7E out of it, W for the 64-bit form.
		if len(ops) != 2 {
			return true, fmt.Errorf("%s wants two operands", m)
		}
		code, x, g := byte(0x6E), ops[0], ops[1]
		if x.kind != opXmm {
			code, x, g = 0x7E, ops[1], ops[0]
		}
		size := 32
		if m == "movq" {
			size = 64
		}
		b.prefix(size, x.reg, g, 0x66)
		b.put(0x0F, code)
		b.modrm(x.reg, g)
		return true, nil
	case "movapd":
		if len(ops) == 2 && ops[0].kind != opXmm {
			b.prefix(32, ops[1].reg, ops[0], 0x66)
			b.put(0x0F, 0x29)
			b.modrm(ops[1].reg, ops[0])
			return true, nil
		}
	}
	return false, nil
}
