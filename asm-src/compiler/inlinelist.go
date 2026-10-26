package main

import "os"

// Structs stored inside a list's element block.
//
// A []P of structs used to be a block of pointers to one heap object each.
// With inline storage the element block holds the fields themselves,
// element i at data + i*stride, and indexing yields the address of the
// element, which is also what a struct value is everywhere else: a
// pointer to its fields. Field reads and writes, method calls and
// copyStruct therefore work on an element address unchanged.
//
// Only structs with no pointer-holding field are stored this way for
// now. Their element block is an ordinary pointer-free block (tagWords)
// the collector never looks inside; a struct with a str, list or
// nested struct field would need a block that says which words of each
// element to follow.
//
// SCANDIACUS_INLINESTRUCTS=1 turns it on while it is being built.

var inlineOn = os.Getenv("SCANDIACUS_INLINESTRUCTS") == "1"

// inlineStride maps the name of a struct stored inline to its size in
// bytes. Filled by collectStructs.
var inlineStride = map[string]int64{}

// inlineStrideOf is the element size of a list of t, or 0 when the
// elements of such a list are not stored inline.
func inlineStrideOf(t vty) int64 {
	if t.k != kStruct || t.res || t.null {
		return 0
	}
	return inlineStride[t.name]
}

// noteInline records whether a laid-out struct is stored inline in lists.
func noteInline(lay *structLayout) {
	if inlineOn && !lay.view && lay.nptr == 0 && lay.bytes > 0 && dataEnums[lay.name] == nil {
		inlineStride[lay.name] = lay.bytes
	}
}

// copyWords copies a struct's fields from the struct at src to dst,
// word by word.
func (l *lowerer) copyWords(dst, src Reg, bytes int64) {
	for off := int64(0); off < bytes; off += wordSize {
		w := l.newReg()
		l.regTy[w] = vInt
		l.emit(Instr{Op: OpLoadMem, Dst: w, A: src, B: NoReg, Imm: off})
		l.emit(Instr{Op: OpStoreMem, A: dst, B: w, Imm: off})
	}
}

// inlineAddr is the address of element idx of an element block whose
// elements are stride bytes. The stride is multiplied out here because an
// address mode only scales by 1, 2, 4 or 8.
func (l *lowerer) inlineAddr(data, idx Reg, stride int64, elem vty) Reg {
	addr := l.newReg()
	l.regTy[addr] = elem
	switch stride {
	case 1, 2, 4, 8:
		l.emit(Instr{Op: OpIndexAddr, Dst: addr, A: data, B: idx, Imm: stride})
	default:
		off := l.arith(OpMul, idx, l.constant(stride))
		l.emit(Instr{Op: OpIndexAddr, Dst: addr, A: data, B: off, Imm: 1})
	}
	return addr
}

// freshCopy is a heap copy of the struct at v, for a value that must
// outlive the slot it was read from: what pop and removeAt hand back,
// and what first and last return.
func (l *lowerer) freshCopy(v Reg, t vty) Reg {
	stride := inlineStrideOf(t)
	if stride == 0 {
		return v
	}
	dup := l.allocTagged(l.constant(stride), l.constant(stride<<structSizeShift|tagStruct))
	l.regTy[dup] = t
	l.copyWords(dup, v, stride)
	return dup
}

// structAddr reports whether a register that an IndexAddr defines holds
// the address of a struct, which is how an inline element is addressed.
func structAddr(fn *Func, r Reg) bool {
	return int(r) < len(fn.RegTypes) && fn.RegTypes[r].k == kStruct
}
