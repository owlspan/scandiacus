package main

// The instructions an asm line may write, and in which forms.
//
// The byte encoder was written for the compiler's own output and trusts
// it: given an operand combination it was never meant to see, it can
// produce some instruction rather than refuse. An asm line is anybody's
// text, so before it reaches the encoder its operands are matched
// against this table, and only a listed form gets through. Every form
// here is held to GNU as by asmset_test.go, so a line the Windows build
// accepts means the same bytes the Linux build gets from the assembler.
// Anything else can still be written with .byte.
//
// A form is its operands, comma-separated, one letter each:
//
//	r  a general register of the instruction's size
//	m  memory of that size: an explicit size, or none when a register
//	   operand gives it
//	i  an immediate that fits 32 bits signed
//	I  any 64-bit immediate (mov into a 64-bit register)
//	b  an immediate byte
//	1  the immediate 1
//	c  cl
//	B  a byte register or byte memory, W the same for a word, D for a
//	   dword
//	x  an xmm register
//	X  an xmm register or memory
//	M  memory, of any size or none
//	S  memory with its size written, as m
//	l  a label
//
// and sizes is which of 8, 16, 32 and 64 the r and m letters may be.

import (
	"fmt"
	"strings"
)

type asmForm struct {
	ops   string
	sizes []int
}

var (
	s3264 = []int{32, 64}
	s64   = []int{64}
	sAll  = []int{8, 16, 32, 64}
	sNone = []int{0}
)

func forms(sizes []int, ops ...string) []asmForm {
	out := make([]asmForm, len(ops))
	for i, o := range ops {
		out[i] = asmForm{o, sizes}
	}
	return out
}

var asmSet = map[string][]asmForm{}

// asmAlias are the other names GNU as takes for a conditional, turned
// into the ones the encoder knows before either sees the line.
var asmAlias = map[string]string{
	"z": "e", "nz": "ne", "c": "b", "nc": "ae", "nae": "b", "nb": "ae",
	"na": "be", "nbe": "a", "pe": "p", "po": "np", "nge": "l", "nl": "ge",
	"ng": "le", "nle": "g",
}

func init() {
	add := func(names string, f []asmForm) {
		for _, n := range strings.Fields(names) {
			asmSet[n] = append(asmSet[n], f...)
		}
	}
	add("pause lfence mfence sfence rdtsc rdtscp int3 ud2 cdq cdqe cqo cpuid xgetbv nop",
		forms(sNone, ""))
	add("add adc sub sbb and or xor cmp", forms(s3264, "r,r", "r,m", "m,r", "r,i", "m,i"))
	add("mov", forms(s3264, "r,r", "r,i", "m,i"))
	add("mov", forms(sAll, "r,m", "m,r"))
	add("mov", forms(s64, "r,I"))
	add("test", forms(s3264, "r,r", "m,r", "r,i", "m,i"))
	add("xchg", forms(s64, "r,r", "m,r"))
	add("xadd cmpxchg", forms(s64, "m,r"))
	add("lea", forms(s3264, "r,m"))
	add("push pop", forms(s64, "r"))
	add("inc dec neg not mul div idiv", forms(s3264, "r", "m"))
	add("imul", forms(s3264, "r", "m", "r,r", "r,m", "r,r,i", "r,m,i"))
	add("shl shr sar rol ror", forms(s3264, "r,c", "r,1", "r,b"))
	add("movzx movsx", forms(s3264, "r,B", "r,W"))
	add("movsxd", forms(s64, "r,D"))
	add("bsf bsr popcnt lzcnt tzcnt", forms(s3264, "r,r", "r,m"))
	add("crc32", forms(s3264, "r,r", "r,S"))
	add("bswap", forms(s3264, "r"))
	add("bt", forms(s3264, "r,r", "r,b"))
	for cc := range jcc {
		add(cc, forms(sNone, "l"))
		add("set"+cc[1:], forms(sNone, "B"))
		add("cmov"+cc[1:], forms(s3264, "r,r", "r,m"))
	}
	add("jmp", forms(sNone, "l"))
	add("jmp call", forms(s64, "r"))

	add("addsd subsd mulsd divsd sqrtsd minsd maxsd comisd ucomisd "+
		"addss subss mulss divss sqrtss minss maxss comiss ucomiss "+
		"addps subps mulps divps sqrtps minps maxps andps andnps orps xorps unpcklps unpckhps "+
		"addpd subpd mulpd divpd sqrtpd minpd maxpd andpd andnpd orpd xorpd unpcklpd unpckhpd "+
		"haddps haddpd paddb paddw paddd paddq psubb psubw psubd psubq pand pandn por pxor "+
		"pcmpeqb pcmpeqd pmullw pmuludq pmulld cvtss2sd cvtsd2ss",
		forms(sNone, "x,X"))
	add("shufps shufpd pshufd roundsd roundss", forms(sNone, "x,X,b"))
	add("movsd movss movaps movups movapd movupd movdqa movdqu", forms(sNone, "x,X", "M,x"))
	add("cvtsi2sd cvtsi2ss", forms(s3264, "x,r", "x,m"))
	add("cvttsd2si cvtsd2si cvttss2si cvtss2si", forms(s3264, "r,X"))
	add("movd", forms([]int{32}, "x,r", "r,x"))
	add("movq", forms(s64, "x,r", "r,x"))
}

// asmShape checks a line's operands against the table, with the
// mnemonic already normalised. lock goes only before a form that writes
// memory.
func asmShape(line string) error {
	mn, rest, _ := strings.Cut(line, " ")
	lock := false
	if mn == "lock" {
		lock = true
		mn, rest, _ = strings.Cut(rest, " ")
	}
	fs, ok := asmSet[mn]
	if !ok {
		return fmt.Errorf("%s is not an instruction asm can encode - write it with .byte", mn)
	}
	var texts []string
	if rest = strings.TrimSpace(rest); rest != "" {
		texts = strings.Split(rest, ",")
	}
	ops := make([]operand, len(texts))
	sized := make([]bool, len(texts))
	for i, t := range texts {
		op, err := parseOperand(t)
		if err != nil {
			return err
		}
		if op.kind == opRipSym {
			return fmt.Errorf("asm cannot name a symbol")
		}
		ops[i] = op
		sized[i] = strings.Contains(t, " ptr ")
	}
	for _, f := range fs {
		if asmMatches(f, ops, sized) {
			if lock && !(strings.HasPrefix(f.ops, "m") && (mn == "xadd" || mn == "cmpxchg" ||
				mn == "xchg" || arith[mn].rmReg != 0 && mn != "cmp" || mn == "inc" || mn == "dec")) {
				return fmt.Errorf("lock goes only before an instruction that writes memory")
			}
			return nil
		}
	}
	return fmt.Errorf("%s does not take these operands here", mn)
}

func asmMatches(f asmForm, ops []operand, sized []bool) bool {
	var letters []string
	if f.ops != "" {
		letters = strings.Split(f.ops, ",")
	}
	if len(letters) != len(ops) {
		return false
	}
	for _, size := range f.sizes {
		if asmMatchesAt(letters, ops, sized, size) {
			return true
		}
	}
	return false
}

func asmMatchesAt(letters []string, ops []operand, sized []bool, size int) bool {
	hasReg := false
	for _, l := range letters {
		if l == "r" {
			hasReg = true
		}
	}
	for i, l := range letters {
		op := ops[i]
		switch l {
		case "r":
			if op.kind != opReg || op.size != size {
				return false
			}
		case "m":
			if op.kind != opMem || sized[i] && op.size != size || !sized[i] && !hasReg {
				return false
			}
		case "i":
			if op.kind != opImm || op.disp < -1<<31 || op.disp >= 1<<31 {
				return false
			}
		case "I":
			if op.kind != opImm {
				return false
			}
		case "b":
			if op.kind != opImm || op.disp < -128 || op.disp > 255 {
				return false
			}
		case "1":
			if op.kind != opImm || op.disp != 1 {
				return false
			}
		case "c":
			if op.kind != opReg || op.size != 8 || op.reg != 1 {
				return false
			}
		case "B", "W", "D":
			want := map[string]int{"B": 8, "W": 16, "D": 32}[l]
			if op.kind == opReg && op.size == want {
				continue
			}
			if op.kind != opMem || !sized[i] || op.size != want {
				return false
			}
		case "x":
			if op.kind != opXmm || op.size != 64 {
				return false
			}
		case "X":
			if op.kind == opXmm && op.size == 64 {
				continue
			}
			if op.kind != opMem {
				return false
			}
		case "S":
			if op.kind != opMem || !sized[i] || op.size != size {
				return false
			}
		case "M":
			if op.kind != opMem {
				return false
			}
		case "l":
			if op.kind != opSym {
				return false
			}
		}
	}
	return true
}

// asmNormalize spaces a line the way the encoder reads it and gives a
// conditional its canonical name.
func asmNormalize(line string) string {
	line = strings.Join(strings.Fields(line), " ")
	mn, rest, _ := strings.Cut(line, " ")
	for _, pre := range []string{"j", "set", "cmov"} {
		if a, ok := asmAlias[strings.TrimPrefix(mn, pre)]; ok && strings.HasPrefix(mn, pre) {
			mn = pre + a
		}
	}
	if rest == "" {
		return mn
	}
	parts := strings.Split(rest, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return mn + " " + strings.Join(parts, ", ")
}
