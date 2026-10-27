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
//	y  a ymm register
//	Y  a ymm register or memory
//	M  memory, of any size or none
//	S  memory with its size written, as m
//	l  a label
//
// The AVX forms are written once for each width, as "x,x,X" and
// "y,y,Y", so that an xmm and a ymm register never meet in one line by
// accident; a form that does mix them, like vcvtps2pd's "y,X", says so.
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

	addAVX()
}

// addAVX lists the forms of the AVX, AVX2 and FMA instructions; how
// each is laid out is in encodevex.go.
func addAVX() {
	add := func(names string, f []asmForm) {
		for _, n := range strings.Fields(names) {
			asmSet[n] = append(asmSet[n], f...)
		}
	}
	both3 := forms(sNone, "x,x,X", "y,y,Y")
	both2 := forms(sNone, "x,X", "y,Y")
	bothImm := forms(sNone, "x,x,X,b", "y,y,Y,b")
	both2Imm := forms(sNone, "x,X,b", "y,Y,b")

	add("vzeroupper vzeroall", forms(sNone, ""))

	// Float arithmetic and logic, packed in both widths.
	add("vaddps vaddpd vsubps vsubpd vmulps vmulpd vdivps vdivpd vminps vminpd vmaxps vmaxpd "+
		"vandps vandpd vandnps vandnpd vorps vorpd vxorps vxorpd vunpcklps vunpcklpd "+
		"vunpckhps vunpckhpd vhaddps vhaddpd vaddsubps vaddsubpd", both3)
	add("vaddss vaddsd vsubss vsubsd vmulss vmulsd vdivss vdivsd vminss vminsd vmaxss vmaxsd "+
		"vsqrtss vsqrtsd", forms(sNone, "x,x,X"))
	add("vsqrtps vsqrtpd vrcpps vrsqrtps vcvtdq2ps vcvtps2dq vcvttps2dq vptest vtestps vtestpd", both2)
	add("vcvtps2pd", forms(sNone, "x,X", "y,X"))
	// Memory is left out of vcvtpd2ps: as wants the size written to tell
	// the two widths apart, and ymmword ptr is not a size asm knows.
	add("vcvtpd2ps", forms(sNone, "x,x", "x,y"))

	add("vmovaps vmovups vmovapd vmovupd vmovdqa vmovdqu", forms(sNone, "x,X", "M,x", "y,Y", "M,y"))
	add("vmovss vmovsd", forms(sNone, "x,M", "M,x", "x,x,x"))
	add("vmovd", forms([]int{32}, "x,r", "r,x"))
	add("vmovq", forms(s64, "x,r", "r,x"))
	add("vmovmskps vmovmskpd vpmovmskb", forms(s3264, "r,x", "r,y"))

	add("vshufps vshufpd vblendps vblendpd vdpps vcmpps vcmppd", bothImm)
	add("vroundss vroundsd vcmpss vcmpsd", forms(sNone, "x,x,X,b"))
	add("vroundps vroundpd vpermilps vpermilpd vpshufd", both2Imm)
	add("vperm2f128 vperm2i128", forms(sNone, "y,y,Y,b"))
	add("vinsertf128 vinserti128", forms(sNone, "y,y,X,b"))
	add("vextractf128 vextracti128", forms(sNone, "X,y,b"))
	add("vblendvps vblendvpd", forms(sNone, "x,x,X,x", "y,y,Y,y"))

	add("vbroadcastss", forms(sNone, "x,M", "y,M", "x,x", "y,x"))
	add("vbroadcastsd", forms(sNone, "y,M", "y,x"))
	add("vbroadcastf128", forms(sNone, "y,M"))
	add("vpbroadcastb vpbroadcastw vpbroadcastd vpbroadcastq", forms(sNone, "x,x", "y,x", "x,M", "y,M"))

	// AVX2 integer.
	add("vpaddb vpaddw vpaddd vpaddq vpsubb vpsubw vpsubd vpsubq vpand vpandn vpor vpxor "+
		"vpcmpeqb vpcmpeqw vpcmpeqd vpcmpeqq vpcmpgtb vpcmpgtw vpcmpgtd vpcmpgtq "+
		"vpmullw vpmulld vpmuludq vpminsd vpmaxsd vpminud vpmaxud vpshufb "+
		"vpunpcklbw vpunpckhbw vpunpckldq vpunpckhdq vpunpcklqdq vpunpckhqdq "+
		"vpackssdw vpackusdw vpacksswb vpackuswb vpsllvd vpsllvq vpsrlvd vpsrlvq vpsravd", both3)
	add("vpsllw vpslld vpsllq vpsrlw vpsrld vpsrlq vpsraw vpsrad", forms(sNone, "x,x,b", "y,y,b"))
	add("vpermd vpermps", forms(sNone, "y,y,Y"))
	add("vpermq vpermpd", forms(sNone, "y,Y,b"))
	add("vpmovzxbd vpmovzxbw vpmovsxbd", forms(sNone, "x,X", "y,X"))

	// Fused multiply-add: every order, add or subtract, negated or not.
	for _, k := range []string{"vfmadd", "vfmsub", "vfnmadd", "vfnmsub"} {
		for _, ord := range []string{"132", "213", "231"} {
			add(k+ord+"ps "+k+ord+"pd", both3)
			add(k+ord+"ss "+k+ord+"sd", forms(sNone, "x,x,X"))
		}
	}
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
	return fmt.Errorf("%s", asmWhyNot(mn, ops, texts))
}

// asmWhyNot is the message for a line no form matches, with a closer
// reason where there is one: the three things a first AVX line gets
// wrong are the widths, a register that needs EVEX, and an immediate.
func asmWhyNot(mn string, ops []operand, texts []string) string {
	wide, narrow := false, false
	for i, op := range ops {
		t := strings.TrimSpace(texts[i])
		if strings.HasPrefix(t, "zmm") || len(t) == 2 && t[0] == 'k' && t[1] >= '0' && t[1] <= '7' {
			return fmt.Sprintf("%s: %s needs EVEX encoding (AVX-512), which asm does not do", mn, t)
		}
		if op.kind == opXmm {
			wide = wide || op.size == 256
			narrow = narrow || op.size != 256
		}
		if op.kind == opImm && (op.disp < -128 || op.disp > 255) && i == len(ops)-1 && strings.HasPrefix(mn, "v") {
			return fmt.Sprintf("%s: the immediate %d does not fit in a byte", mn, op.disp)
		}
	}
	if wide && narrow && strings.HasPrefix(mn, "v") {
		return fmt.Sprintf("%s: xmm and ymm registers cannot be mixed in this form", mn)
	}
	return fmt.Sprintf("%s does not take these operands here", mn)
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
		case "y":
			if op.kind != opXmm || op.size != 256 {
				return false
			}
		case "Y":
			if op.kind == opXmm && op.size == 256 {
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
