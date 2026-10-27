package main

// The AVX, AVX2 and FMA instructions an asm block may write. The
// compiler's own wide loops use a handful of them (vecloop.go, fma.go);
// they go through the same table, so there is one place that knows how
// each is laid out. asmset.go lists the operand forms each one takes
// and asmset_test.go holds every form to GNU as.

import "fmt"

// Where the operands of a VEX instruction go.
type vexShape byte

const (
	vRVM   vexShape = iota // op reg, vvvv, r/m
	vRM                    // op reg, r/m
	vMR                    // op r/m, reg (a store)
	vVMI                   // op vvvv, r/m, imm, with a /digit in reg (shift by immediate)
	vRVMR                  // op reg, vvvv, r/m, reg4 (the fourth in imm[7:4])
	vMove                  // a load with op, a store with x; vmovss and vmovsd also take three registers
	vGpr                   // op xmm, general register, or x the other way round
	vNoReg                 // no operands
)

// Implied prefixes and opcode maps, as VEX numbers them.
const (
	pNone, p66, pF3, pF2 = 0, 1, 2, 3
	m0F, m0F38, m0F3A    = 1, 2, 3
)

// A vexOp is one instruction: its implied prefix, opcode map and
// opcode, VEX.W, the shape, whether an immediate byte follows, and x,
// which is the store opcode of a move, the /digit of a shift by
// immediate, or the opcode of the register-to-xmm direction of vmovd.
type vexOp struct {
	pp, mp byte
	op     byte
	w      bool
	shape  vexShape
	imm    bool
	x      byte
}

// asmVexOps are the VEX instructions of asm blocks. The length bit
// comes from the registers, so one line serves the xmm and the ymm form.
var asmVexOps = map[string]vexOp{
	// Arithmetic, packed and scalar.
	"vaddps": {pNone, m0F, 0x58, false, vRVM, false, 0}, "vaddpd": {p66, m0F, 0x58, false, vRVM, false, 0},
	"vaddss": {pF3, m0F, 0x58, false, vRVM, false, 0}, "vaddsd": {pF2, m0F, 0x58, false, vRVM, false, 0},
	"vmulps": {pNone, m0F, 0x59, false, vRVM, false, 0}, "vmulpd": {p66, m0F, 0x59, false, vRVM, false, 0},
	"vmulss": {pF3, m0F, 0x59, false, vRVM, false, 0}, "vmulsd": {pF2, m0F, 0x59, false, vRVM, false, 0},
	"vsubps": {pNone, m0F, 0x5C, false, vRVM, false, 0}, "vsubpd": {p66, m0F, 0x5C, false, vRVM, false, 0},
	"vsubss": {pF3, m0F, 0x5C, false, vRVM, false, 0}, "vsubsd": {pF2, m0F, 0x5C, false, vRVM, false, 0},
	"vdivps": {pNone, m0F, 0x5E, false, vRVM, false, 0}, "vdivpd": {p66, m0F, 0x5E, false, vRVM, false, 0},
	"vdivss": {pF3, m0F, 0x5E, false, vRVM, false, 0}, "vdivsd": {pF2, m0F, 0x5E, false, vRVM, false, 0},
	"vminps": {pNone, m0F, 0x5D, false, vRVM, false, 0}, "vminpd": {p66, m0F, 0x5D, false, vRVM, false, 0},
	"vminss": {pF3, m0F, 0x5D, false, vRVM, false, 0}, "vminsd": {pF2, m0F, 0x5D, false, vRVM, false, 0},
	"vmaxps": {pNone, m0F, 0x5F, false, vRVM, false, 0}, "vmaxpd": {p66, m0F, 0x5F, false, vRVM, false, 0},
	"vmaxss": {pF3, m0F, 0x5F, false, vRVM, false, 0}, "vmaxsd": {pF2, m0F, 0x5F, false, vRVM, false, 0},
	"vsqrtss": {pF3, m0F, 0x51, false, vRVM, false, 0}, "vsqrtsd": {pF2, m0F, 0x51, false, vRVM, false, 0},
	"vandps": {pNone, m0F, 0x54, false, vRVM, false, 0}, "vandpd": {p66, m0F, 0x54, false, vRVM, false, 0},
	"vandnps": {pNone, m0F, 0x55, false, vRVM, false, 0}, "vandnpd": {p66, m0F, 0x55, false, vRVM, false, 0},
	"vorps": {pNone, m0F, 0x56, false, vRVM, false, 0}, "vorpd": {p66, m0F, 0x56, false, vRVM, false, 0},
	"vxorps": {pNone, m0F, 0x57, false, vRVM, false, 0}, "vxorpd": {p66, m0F, 0x57, false, vRVM, false, 0},
	"vunpcklps": {pNone, m0F, 0x14, false, vRVM, false, 0}, "vunpcklpd": {p66, m0F, 0x14, false, vRVM, false, 0},
	"vunpckhps": {pNone, m0F, 0x15, false, vRVM, false, 0}, "vunpckhpd": {p66, m0F, 0x15, false, vRVM, false, 0},
	"vhaddps": {pF2, m0F, 0x7C, false, vRVM, false, 0}, "vhaddpd": {p66, m0F, 0x7C, false, vRVM, false, 0},
	"vaddsubps": {pF2, m0F, 0xD0, false, vRVM, false, 0}, "vaddsubpd": {p66, m0F, 0xD0, false, vRVM, false, 0},

	// One source: roots, reciprocals, conversions.
	"vsqrtps": {pNone, m0F, 0x51, false, vRM, false, 0}, "vsqrtpd": {p66, m0F, 0x51, false, vRM, false, 0},
	"vrcpps": {pNone, m0F, 0x53, false, vRM, false, 0}, "vrsqrtps": {pNone, m0F, 0x52, false, vRM, false, 0},
	"vcvtdq2ps": {pNone, m0F, 0x5B, false, vRM, false, 0}, "vcvtps2dq": {p66, m0F, 0x5B, false, vRM, false, 0},
	"vcvttps2dq": {pF3, m0F, 0x5B, false, vRM, false, 0},
	"vcvtps2pd":  {pNone, m0F, 0x5A, false, vRM, false, 0}, "vcvtpd2ps": {p66, m0F, 0x5A, false, vRM, false, 0},
	"vptest": {p66, m0F38, 0x17, false, vRM, false, 0}, "vtestps": {p66, m0F38, 0x0E, false, vRM, false, 0},
	"vtestpd": {p66, m0F38, 0x0F, false, vRM, false, 0},

	// Moves: load opcode, store opcode in x.
	"vmovaps": {pNone, m0F, 0x28, false, vMove, false, 0x29}, "vmovups": {pNone, m0F, 0x10, false, vMove, false, 0x11},
	"vmovapd": {p66, m0F, 0x28, false, vMove, false, 0x29}, "vmovupd": {p66, m0F, 0x10, false, vMove, false, 0x11},
	"vmovdqa": {p66, m0F, 0x6F, false, vMove, false, 0x7F}, "vmovdqu": {pF3, m0F, 0x6F, false, vMove, false, 0x7F},
	"vmovss": {pF3, m0F, 0x10, false, vMove, false, 0x11}, "vmovsd": {pF2, m0F, 0x10, false, vMove, false, 0x11},
	"vmovd": {p66, m0F, 0x6E, false, vGpr, false, 0x7E}, "vmovq": {p66, m0F, 0x6E, true, vGpr, false, 0x7E},
	"vmovmskps": {pNone, m0F, 0x50, false, vRM, false, 0}, "vmovmskpd": {p66, m0F, 0x50, false, vRM, false, 0},
	"vpmovmskb": {p66, m0F, 0xD7, false, vRM, false, 0},

	// Broadcasts and lane moves.
	"vbroadcastss": {p66, m0F38, 0x18, false, vRM, false, 0}, "vbroadcastsd": {p66, m0F38, 0x19, false, vRM, false, 0},
	"vbroadcastf128": {p66, m0F38, 0x1A, false, vRM, false, 0},
	"vpbroadcastb":   {p66, m0F38, 0x78, false, vRM, false, 0}, "vpbroadcastw": {p66, m0F38, 0x79, false, vRM, false, 0},
	"vpbroadcastd": {p66, m0F38, 0x58, false, vRM, false, 0}, "vpbroadcastq": {p66, m0F38, 0x59, false, vRM, false, 0},
	"vinsertf128": {p66, m0F3A, 0x18, false, vRVM, true, 0}, "vextractf128": {p66, m0F3A, 0x19, false, vMR, true, 0},
	"vinserti128": {p66, m0F3A, 0x38, false, vRVM, true, 0}, "vextracti128": {p66, m0F3A, 0x39, false, vMR, true, 0},
	"vperm2f128": {p66, m0F3A, 0x06, false, vRVM, true, 0}, "vperm2i128": {p66, m0F3A, 0x46, false, vRVM, true, 0},
	"vpermd": {p66, m0F38, 0x36, false, vRVM, false, 0}, "vpermps": {p66, m0F38, 0x16, false, vRVM, false, 0},
	"vpermq": {p66, m0F3A, 0x00, true, vRM, true, 0}, "vpermpd": {p66, m0F3A, 0x01, true, vRM, true, 0},
	"vpermilps": {p66, m0F3A, 0x04, false, vRM, true, 0}, "vpermilpd": {p66, m0F3A, 0x05, false, vRM, true, 0},
	"vpmovzxbw": {p66, m0F38, 0x30, false, vRM, false, 0}, "vpmovzxbd": {p66, m0F38, 0x31, false, vRM, false, 0},
	"vpmovsxbd": {p66, m0F38, 0x21, false, vRM, false, 0},

	// With an immediate byte.
	"vshufps": {pNone, m0F, 0xC6, false, vRVM, true, 0}, "vshufpd": {p66, m0F, 0xC6, false, vRVM, true, 0},
	"vblendps": {p66, m0F3A, 0x0C, false, vRVM, true, 0}, "vblendpd": {p66, m0F3A, 0x0D, false, vRVM, true, 0},
	"vdpps":    {p66, m0F3A, 0x40, false, vRVM, true, 0},
	"vroundps": {p66, m0F3A, 0x08, false, vRM, true, 0}, "vroundpd": {p66, m0F3A, 0x09, false, vRM, true, 0},
	"vroundss": {p66, m0F3A, 0x0A, false, vRVM, true, 0}, "vroundsd": {p66, m0F3A, 0x0B, false, vRVM, true, 0},
	"vcmpps": {pNone, m0F, 0xC2, false, vRVM, true, 0}, "vcmppd": {p66, m0F, 0xC2, false, vRVM, true, 0},
	"vcmpss": {pF3, m0F, 0xC2, false, vRVM, true, 0}, "vcmpsd": {pF2, m0F, 0xC2, false, vRVM, true, 0},
	"vpshufd":   {p66, m0F, 0x70, false, vRM, true, 0},
	"vblendvps": {p66, m0F3A, 0x4A, false, vRVMR, false, 0}, "vblendvpd": {p66, m0F3A, 0x4B, false, vRVMR, false, 0},

	// Integer, the 66 0F map.
	"vpaddb": {p66, m0F, 0xFC, false, vRVM, false, 0}, "vpaddw": {p66, m0F, 0xFD, false, vRVM, false, 0},
	"vpaddd": {p66, m0F, 0xFE, false, vRVM, false, 0}, "vpaddq": {p66, m0F, 0xD4, false, vRVM, false, 0},
	"vpsubb": {p66, m0F, 0xF8, false, vRVM, false, 0}, "vpsubw": {p66, m0F, 0xF9, false, vRVM, false, 0},
	"vpsubd": {p66, m0F, 0xFA, false, vRVM, false, 0}, "vpsubq": {p66, m0F, 0xFB, false, vRVM, false, 0},
	"vpand": {p66, m0F, 0xDB, false, vRVM, false, 0}, "vpandn": {p66, m0F, 0xDF, false, vRVM, false, 0},
	"vpor": {p66, m0F, 0xEB, false, vRVM, false, 0}, "vpxor": {p66, m0F, 0xEF, false, vRVM, false, 0},
	"vpcmpeqb": {p66, m0F, 0x74, false, vRVM, false, 0}, "vpcmpeqw": {p66, m0F, 0x75, false, vRVM, false, 0},
	"vpcmpeqd": {p66, m0F, 0x76, false, vRVM, false, 0}, "vpcmpeqq": {p66, m0F38, 0x29, false, vRVM, false, 0},
	"vpcmpgtb": {p66, m0F, 0x64, false, vRVM, false, 0}, "vpcmpgtw": {p66, m0F, 0x65, false, vRVM, false, 0},
	"vpcmpgtd": {p66, m0F, 0x66, false, vRVM, false, 0}, "vpcmpgtq": {p66, m0F38, 0x37, false, vRVM, false, 0},
	"vpmullw": {p66, m0F, 0xD5, false, vRVM, false, 0}, "vpmulld": {p66, m0F38, 0x40, false, vRVM, false, 0},
	"vpmuludq": {p66, m0F, 0xF4, false, vRVM, false, 0},
	"vpminsd":  {p66, m0F38, 0x39, false, vRVM, false, 0}, "vpmaxsd": {p66, m0F38, 0x3D, false, vRVM, false, 0},
	"vpminud": {p66, m0F38, 0x3B, false, vRVM, false, 0}, "vpmaxud": {p66, m0F38, 0x3F, false, vRVM, false, 0},
	"vpshufb":    {p66, m0F38, 0x00, false, vRVM, false, 0},
	"vpunpcklbw": {p66, m0F, 0x60, false, vRVM, false, 0}, "vpunpckhbw": {p66, m0F, 0x68, false, vRVM, false, 0},
	"vpunpckldq": {p66, m0F, 0x62, false, vRVM, false, 0}, "vpunpckhdq": {p66, m0F, 0x6A, false, vRVM, false, 0},
	"vpunpcklqdq": {p66, m0F, 0x6C, false, vRVM, false, 0}, "vpunpckhqdq": {p66, m0F, 0x6D, false, vRVM, false, 0},
	"vpackssdw": {p66, m0F, 0x6B, false, vRVM, false, 0}, "vpackusdw": {p66, m0F38, 0x2B, false, vRVM, false, 0},
	"vpacksswb": {p66, m0F, 0x63, false, vRVM, false, 0}, "vpackuswb": {p66, m0F, 0x67, false, vRVM, false, 0},

	// Shifts by an immediate: the opcode picks the element size and the
	// /digit the direction, in x.
	"vpsllw": {p66, m0F, 0x71, false, vVMI, true, 6}, "vpsrlw": {p66, m0F, 0x71, false, vVMI, true, 2},
	"vpsraw": {p66, m0F, 0x71, false, vVMI, true, 4},
	"vpslld": {p66, m0F, 0x72, false, vVMI, true, 6}, "vpsrld": {p66, m0F, 0x72, false, vVMI, true, 2},
	"vpsrad": {p66, m0F, 0x72, false, vVMI, true, 4},
	"vpsllq": {p66, m0F, 0x73, false, vVMI, true, 6}, "vpsrlq": {p66, m0F, 0x73, false, vVMI, true, 2},

	// Shifts by a vector of counts.
	"vpsllvd": {p66, m0F38, 0x47, false, vRVM, false, 0}, "vpsllvq": {p66, m0F38, 0x47, true, vRVM, false, 0},
	"vpsrlvd": {p66, m0F38, 0x45, false, vRVM, false, 0}, "vpsrlvq": {p66, m0F38, 0x45, true, vRVM, false, 0},
	"vpsravd": {p66, m0F38, 0x46, false, vRVM, false, 0},

	// No operands, and no ModRM byte either.
	"vzeroupper": {pNone, m0F, 0x77, false, vNoReg, false, 0},
	"vzeroall":   {pNone, m0F, 0x77, false, vNoReg, false, 0},
}

func init() {
	// Fused multiply-add: three orders of the operands, add or subtract,
	// the product negated or not, and the four element kinds. The order
	// gives the opcode's base, the kind of operation steps it by two,
	// and the scalar forms are one past the packed ones; the d kinds set
	// VEX.W.
	for _, ord := range []struct {
		name string
		base byte
	}{{"132", 0x98}, {"213", 0xA8}, {"231", 0xB8}} {
		for i, k := range []string{"vfmadd", "vfmsub", "vfnmadd", "vfnmsub"} {
			for j, t := range []string{"ps", "pd", "ss", "sd"} {
				op := ord.base + byte(i*2) + byte(j/2)
				asmVexOps[k+ord.name+t] = vexOp{p66, m0F38, op, j%2 == 1, vRVM, false, 0}
			}
		}
	}
}

// encodeVex writes one of the VEX instructions in asmVexOps, reporting
// whether m is one of them.
func (b *block) encodeVex(m string, ops []operand) (bool, error) {
	op, ok := asmVexOps[m]
	if !ok {
		return false, nil
	}
	if op.shape == vNoReg {
		if len(ops) != 0 {
			return true, fmt.Errorf("%s takes no operands", m)
		}
		// vzeroall is the same opcode with the length bit set.
		l := byte(0xF8)
		if m == "vzeroall" {
			l = 0xFC
		}
		b.put(0xC5, l, op.op)
		return true, nil
	}
	want := map[vexShape]int{vRVM: 3, vRM: 2, vMR: 2, vVMI: 2, vRVMR: 4, vGpr: 2, vMove: 2}[op.shape]
	if op.imm {
		want++
	}
	if op.shape == vMove && len(ops) == 3 {
		want = 3 // vmovss xmm, xmm, xmm
	}
	if len(ops) != want {
		return true, fmt.Errorf("%s wants %d operands", m, want)
	}
	// The length bit follows the registers: any ymm makes it 256 bits.
	wide := false
	for _, o := range ops {
		if o.kind == opXmm && o.size == 256 {
			wide = true
		}
	}
	var v, reg int
	var rm operand
	opcode := op.op
	imm := byte(0)
	switch op.shape {
	case vRVM:
		reg, v, rm = ops[0].reg, ops[1].reg, ops[2]
	case vRM:
		reg, rm = ops[0].reg, ops[1]
	case vMR:
		reg, rm = ops[1].reg, ops[0]
	case vVMI:
		v, rm, reg = ops[0].reg, ops[1], int(op.x)
	case vRVMR:
		reg, v, rm = ops[0].reg, ops[1].reg, ops[2]
		imm = byte(ops[3].reg << 4)
	case vMove:
		switch {
		case len(ops) == 3:
			reg, v, rm = ops[0].reg, ops[1].reg, ops[2]
		case ops[0].kind == opXmm && ops[1].kind == opXmm && ops[0].reg < 8 && ops[1].reg >= 8:
			// Register to register can go either way. GNU as takes the
			// store opcode when that lets a high source ride in the
			// ModRM reg field and keeps the two-byte prefix.
			reg, rm, opcode = ops[1].reg, ops[0], op.x
		case ops[0].kind == opXmm:
			reg, rm = ops[0].reg, ops[1]
		default:
			reg, rm, opcode = ops[1].reg, ops[0], op.x
		}
	case vGpr:
		reg, rm = ops[0].reg, ops[1]
		if ops[0].kind != opXmm {
			reg, rm, opcode = ops[1].reg, ops[0], op.x
		}
	}
	b.vexW(op.pp, op.mp, op.w, wide, v, reg, rm, opcode)
	if op.imm {
		imm = byte(ops[len(ops)-1].disp)
	}
	if op.imm || op.shape == vRVMR {
		b.put(imm)
	}
	return true, nil
}
