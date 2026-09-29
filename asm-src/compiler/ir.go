package main

// The intermediate representation, and the lowering from AST into it.
//
// Three-address code over an unlimited supply of virtual registers. Every
// instruction reads two operands and writes one, which is the shape most
// machine instructions already have, so instruction selection stays a
// local decision rather than a tree walk.
//
// Nothing in this file knows what an x86 register is. That separation is
// the whole point: the register allocator and the emitter are what change
// when a second architecture arrives, and lowering is not.
//
// Deliberately not SSA. SSA earns its keep when there are optimisation
// passes to run, and there are none yet. Adding it later means inserting
// a pass between here and the emitter, not rewriting either.

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// A vty is the type information this backend tracks.
//
// The real type checker lives in ../../frontend/check.go and runs before
// this, shared with the Go backend, so this is not type checking. It is
// the subset of what the checker already worked out that the lowerer
// needs to keep around while it emits.
//
// What this does buy is knowing which of print, concat or compare to
// emit, which is not optional: `print` of a comparison has to write
// "true" and `a + b` on two strings has to concatenate rather than add.
type vkind int

const (
	kVoid vkind = iota
	kInt
	kFloat
	kBool
	kStr
	kList
	kMap
	kStruct

	// A function value: one word, a pointer to a closure object. See
	// closure.go for what is behind it.
	kFunc

	// Raw binary. A pointer to a tagBytes block, with the length in the
	// object header, so a zero byte inside it is just a byte.
	kBytes
)

// A vty is a kind plus, for a list, the type of its elements, and for a
// map, the type of its keys and values.
//
// el is a *vty rather than a vkind, which is what makes [][]int and
// {str: []str} expressible. It was a kind when lists were new, and the
// cost of that was paid twice: json needs a map of lists, and every
// error message about a nested type had to lie about a level it could
// not represent. A pointer rather than a value because a type cannot
// contain itself by value.
//
// name carries the one thing a bare kind cannot: which struct. With el
// holding a whole type, it names only the struct itself, never an
// element's - that was a workaround for the missing level.
//
// res is the `!` suffix. It is a flag rather than a kind because a
// result's layout does not depend on what it carries, so int! and str!
// differ only in how the value word is read back out.
type vty struct {
	k    vkind
	el   *vty  // list: the element type. map: the value type.
	key  vkind // map only: the key kind, still only int or str
	res  bool  // T!: the value is boxed alongside a failure reason
	null bool  // ?T: one word, zero for nil, else a pointer to the value
	name string

	// fn carries a function value's signature. A pointer for the same
	// reason el is one: a type cannot contain itself by value, and
	// fn(fn(int) -> int) -> int is a type somebody will write.
	fn *vsig
}

// A vsig is what a function value can be called with and what comes
// back. It is separate from `sig`, which is a declared function's
// signature in the lowerer's table: this one is part of a type.
type vsig struct {
	params []vty
	ret    vty
}

func vFuncOf(params []vty, ret vty) vty {
	return vty{k: kFunc, fn: &vsig{params: params, ret: ret}}
}

var (
	vVoid = vty{k: kVoid}
	// vNil is the type of the bare literal `nil`: no kind of its own,
	// and a nullable wrapper with nothing inside. Only Widen ever sees
	// it, which turns it into the zero word of whatever ?T was wanted.
	vNil   = vty{k: kVoid, null: true}
	vInt   = vty{k: kInt}
	vFloat = vty{k: kFloat}
	vBool  = vty{k: kBool}
	vStr   = vty{k: kStr}
	vBytes = vty{k: kBytes}
)

func vListOf(e vty) vty          { return vty{k: kList, el: &e} }
func vMapOf(kk vkind, v vty) vty { return vty{k: kMap, el: &v, key: kk} }

func vStructOf(name string) vty { return vty{k: kStruct, name: name} }

// vResultOf is T!. Wrapping twice is refused rather than flattened: the
// checker has already ruled out T!!, so a second wrap here would be a
// lowering bug worth seeing.
func vResultOf(t vty) vty { t.res = true; return t }

// inner is the T inside a T!.
func (t vty) inner() vty { t.res = false; return t }

// vNullOf is ?T. Like a result, the wrapper is a flag rather than a kind
// because the layout does not depend on what is inside: one word, zero
// for nil and otherwise a pointer to a box holding the value.
//
// Boxing even a str or a list, which are already pointers, costs an
// allocation that a null-pointer representation would not - but a ?int
// has no spare value to mean nil, and one representation for every ?T
// is what keeps the lowering to a single case.
func vNullOf(t vty) vty { t.null = true; return t }

// notNull is the T inside a ?T.
func (t vty) notNull() vty { t.null = false; return t }

// elemType is the type of what comes out of indexing this list or map.
//
// A list or map with no element type is one that was never built - the
// zero vty, which reads as void. Returning that rather than crashing
// keeps a lowering bug reporting a type error instead of a panic.
func (t vty) elemType() vty {
	if t.el == nil {
		return vVoid
	}
	return *t.el
}

// elemKind is the kind of the element, for the many places that only
// need to know int from str from pointer.
func (t vty) elemKind() vkind { return t.elemType().k }

// eq is type equality. It has to be a method now that a vty holds a
// pointer: `a == b` compares the pointer, so two separately built
// []int values would come out unequal. Every place that means "the
// same type" must go through here.
func (t vty) eq(o vty) bool {
	if t.k != o.k || t.key != o.key || t.res != o.res || t.null != o.null || t.name != o.name {
		return false
	}
	if (t.fn == nil) != (o.fn == nil) {
		return false
	}
	if t.fn != nil {
		if len(t.fn.params) != len(o.fn.params) || !t.fn.ret.eq(o.fn.ret) {
			return false
		}
		for i := range t.fn.params {
			if !t.fn.params[i].eq(o.fn.params[i]) {
				return false
			}
		}
	}
	if (t.el == nil) != (o.el == nil) {
		return false
	}
	if t.el == nil {
		return true
	}
	return t.el.eq(*o.el)
}

// keyType is the type of a map's keys.
func (t vty) keyType() vty { return vty{k: t.key} }

// holdsPointer reports whether a value of this type is a pointer into
// the heap, which is what a collector needs to know about any word it
// finds. A result is a pointer to its box whatever it carries.
func (t vty) holdsPointer() bool {
	if t.res || t.null {
		return true
	}
	switch t.k {
	case kStr, kList, kMap, kStruct, kFunc, kBytes:
		return true
	}
	return false
}

func (t vty) String() string {
	if t.res {
		return t.inner().String() + "!"
	}
	if t.null {
		return "?" + t.notNull().String()
	}
	switch t.k {
	case kInt:
		if t.name != "" {
			return t.name
		}
		return "int"
	case kFloat:
		return "float"
	case kBool:
		return "bool"
	case kStr:
		return "str"
	case kBytes:
		return "bytes"
	case kStruct:
		return t.name
	case kList:
		return "[]" + t.elemType().String()
	case kMap:
		return "{" + t.keyType().String() + ": " + t.elemType().String() + "}"
	case kFunc:
		out := "fn("
		if t.fn != nil {
			for i, p := range t.fn.params {
				if i > 0 {
					out += ", "
				}
				out += p.String()
			}
		}
		out += ")"
		if t.fn != nil && t.fn.ret.k != kVoid {
			out += " -> " + t.fn.ret.String()
		}
		return out
	}
	return "void"
}

// typeOfName resolves the written text of a type annotation to the vty
// this backend tracks.
//
// The grammar is not restated here. front.ParseType is the same
// function the checker runs, so the two cannot drift on what `[]int!`
// or `{str: User}` mean. What is local is which of those types this
// backend can actually build, and that is what the second return value
// answers.
func typeOfName(s string) (vty, bool) {
	if strings.TrimSpace(s) == "" {
		return vVoid, true
	}
	t := ParseType(s)
	if t == nil {
		return vVoid, false
	}
	return vtyOf(t)
}

// vtyOf narrows a checked Type down to the subset the lowerer can
// build, reporting false for the rest. Everything it refuses is a
// feature that is not here yet rather than a program that is wrong, so
// the caller turns it into "not on the assembly backend yet" naming the
// type as it was written.
func vtyOf(t *Type) (vty, bool) {
	if t == nil {
		return vVoid, false
	}
	switch t.Kind {
	case KVoid:
		return vVoid, true
	case KInt:
		return vInt, true
	case KFloat:
		return vFloat, true
	case KBool:
		return vBool, true
	case KStr:
		return vStr, true
	case KStruct:
		if _, isEnum := enumVariants[t.Name]; isEnum {
			return vEnumOf(t.Name), true
		}
		return vStructOf(t.Name), true
	case KEnum:
		return vEnumOf(t.Name), true
	case KPtr:
		return vPtr(t.String()), true

	case KBytes:
		return vBytes, true

	case KFunc:
		params := make([]vty, 0, len(t.Params))
		for _, p := range t.Params {
			v, ok := vtyOf(p)
			if !ok {
				return vVoid, false
			}
			params = append(params, v)
		}
		ret, ok := vtyOf(t.Elem)
		if !ok {
			return vVoid, false
		}
		return vFuncOf(params, ret), true

	case KResult:
		inner, ok := vtyOf(t.Elem)
		if !ok || inner.res {
			return vVoid, false
		}
		return vResultOf(inner), true

	case KNullable:
		inner, ok := vtyOf(t.Elem)
		// ?T! and ?? are refused rather than flattened: the checker has
		// already ruled them out, so one here would be a lowering bug
		// worth seeing rather than something to paper over.
		if !ok || inner.res || inner.null || inner.k == kVoid {
			return vVoid, false
		}
		return vNullOf(inner), true

	case KList:
		e, ok := vtyOf(t.Elem)
		// A list of results is still refused. Every element would be a
		// separate box, and nothing here unboxes one out of a container.
		if !ok || e.res || e.k == kVoid {
			return vVoid, false
		}
		return vListOf(e), true

	case KMap:
		kt, kok := vtyOf(t.Key)
		vt, vok := vtyOf(t.Elem)
		if !kok || !vok {
			return vVoid, false
		}
		if kt.k != kInt && kt.k != kStr {
			return vVoid, false
		}
		if vt.res || vt.k == kVoid {
			return vVoid, false
		}
		return vMapOf(kt.k, vt), true
	}
	return vVoid, false
}

// A Reg is a virtual register. There are as many as the program needs;
// mapping them onto the sixteen real ones is the allocator's problem.
type Reg int

// NoReg marks an instruction that writes nothing, or an absent operand.
const NoReg Reg = -1

type Op int

const (
	OpConst Op = iota // Dst = Imm
	OpStr             // Dst = address of string constant Imm
	OpLoad            // Dst = slot[Imm]
	OpStore           // slot[Imm] = A
	OpAdd             // Dst = A + B
	OpSub             // Dst = A - B
	OpMul             // Dst = A * B
	OpDiv             // Dst = A / B   (truncating, as the language specifies)
	OpMod             // Dst = A % B
	OpNeg             // Dst = -A

	// Floats. A separate set rather than overloading the integer ops,
	// because they run on a different register file (xmm0-15) and the
	// emitter has to know which one at the point it picks an
	// instruction - regTy is a lowering-time concept, gone by then.
	OpFConst // Dst = the float constant pool entry Imm
	OpFAdd
	OpFSub
	OpFMul
	OpFDiv
	OpFNeg
	OpFEq
	OpFNe
	OpFLt
	OpFLe
	OpFGt
	OpFGe
	OpIntToFloat // Dst = float(A), exact
	OpFloatToInt // Dst = int(A), truncated toward zero
	OpSqrt       // Dst = sqrt(A)
	OpFMod       // Dst = fmod(A, B), the C library's floating remainder

	// Bitwise. All int-only, matching the language.
	OpBAnd // Dst = A & B
	OpBOr  // Dst = A | B
	OpBXor // Dst = A ^ B
	OpBNot // Dst = ~A
	OpShl  // Dst = A << B
	OpShr  // Dst = A >> B

	// Comparisons produce 0 or 1, so a bool is just an int and needs no
	// separate representation. Branching then only needs one conditional
	// jump form rather than one per comparison.
	OpEq
	OpNe
	OpLt
	OpLe
	OpGt
	OpGe

	OpNot // Dst = !A

	// Control flow. Labels are integer targets rather than pointers, so
	// the byte writer can backpatch them the same way later without a
	// different IR.
	OpLabel   // Imm names this point
	OpJump    // goto Imm
	OpJumpIf  // if A != 0 goto Imm
	OpJumpNot // if A == 0 goto Imm

	// Functions.
	OpParam // Dst = incoming parameter number Imm
	OpCall  // Dst = Sym(Args...)
	OpRet   // return A, or nothing when A is NoReg

	// Anything needing the C runtime goes through a named helper, which
	// keeps the emitter free of open-coded sequences that the byte
	// writer would have to reproduce exactly.
	OpPrintInt
	OpPrintFloat
	OpPrintBool
	OpPrintStr
	OpConcat // Dst = A ++ B
	OpStrEq  // Dst = A == B, as strings
	OpStrLen // Dst = len(A)
	OpIntToStr
	OpFloatToStr
	OpBoolToStr

	// Raw memory. These exist so that lists can be built in the IR
	// rather than as hand-written assembly: a bounds check becomes an
	// ordinary compare and branch, and the byte writer inherits all of
	// it without a second implementation. They are also the ops that
	// `unsafe` and manual memory will eventually be written in terms of.
	OpAlloc     // Dst = allocate A bytes
	OpIndexAddr // Dst = A + B*Imm, where an Imm of 0 means 8
	OpLoadMem   // Dst = qword at [A + Imm]
	OpStoreMem  // qword at [A + Imm] = B

	// Unbuffered output, for building up a line without a newline after
	// each piece. print() is these plus a newline.
	OpWriteStr
	OpWriteInt
	OpWriteFloat
	OpBoundsFail // A is the index, B the length; does not return

	// Byte-level memory, for strings. A Veyl string is a pointer to
	// NUL-terminated bytes, so anything that inspects or builds one
	// needs to read and write single bytes rather than words.
	OpLoadByte  // Dst = zero-extended byte at [A + B]
	OpStoreByte // byte at [A + B] = low byte of the value in Imm's slot

	// must() failed: write the reason to stderr and stop. Its own op
	// rather than a call because it does not return, so the emitter must
	// not be free to schedule anything after it.
	OpMustFail // A is the reason; does not return

	// Dst = the address of global slot Imm. Globals live in static
	// storage rather than in main's frame, because a function has to be
	// able to reach one and a function does not have main's frame.
	//
	// Last in this block on purpose: opNames is positional, so a new op
	// added in the middle renames every op after it.
	OpGlobalAddr

	// Dst = rsp. The collector needs somewhere to start reading the
	// stack from, and the stack is where every live pointer is.
	OpStackPtr

	// Dst = the address of the .rdata label Sym. Only the format strings
	// the float printer uses need this; everything else that names a
	// static address is a string constant and goes through OpStr.
	OpSymAddr

	// Dst = the address of frame slot Imm. A C function with an
	// out-parameter - frexp is the first - needs somewhere to be told to
	// write, and a virtual register is not a place until it is a slot.
	//
	// Appended, not inserted: opNames is positional.
	OpSlotAddr

	// Dst = A(Args...), where A is a function value. The code address
	// and the environment both come out of the object A points at, so
	// unlike OpCall there is no symbol: what runs is not known until the
	// value is in hand.
	OpCallClosure

	// OpCallAddr calls native code at the address in A, with Args, by the
	// Windows x64 convention: mem.call. Like OpCall to an extern, but
	// what is called is a number the program worked out.
	OpCallAddr

	// The atomics, each one locked instruction. OpAtomicAdd adds B to the
	// word at A and gives what was there; OpAtomicSwap stores B and gives
	// what was there; OpAtomicCAS stores Args[2] if the word at Args[0]
	// holds Args[1], and says whether it did.
	OpAtomicAdd
	OpAtomicSwap
	OpAtomicCAS

	// OpGCPoll is a statement's check of the heap against the next
	// collection's threshold, calling Sym, the gcmaybe helper, when it is
	// over.
	OpGCPoll

	// Raw memory at a width. Imm is one of the mem* kinds below, which
	// says how many bytes, whether a narrow integer is sign-extended,
	// and whether the value is a float. Dst is an int or a float to
	// match. Appended, not inserted: opNames is positional.
	OpPeek // Dst = the value at address A
	OpPoke // the value B is written at address A
)

// The widths OpPeek and OpPoke move. A narrow store keeps the low bits,
// so a poke never needs the signed kinds, but it accepts them.
const (
	memU8 = iota + 1
	memI8
	memU16
	memI16
	memU32
	memI32
	memI64
	memF32
	memF64
)

var opNames = [...]string{
	"const", "str", "load", "store", "add", "sub", "mul", "div", "mod", "neg",
	"fconst", "fadd", "fsub", "fmul", "fdiv", "fneg",
	"feq", "fne", "flt", "fle", "fgt", "fge",
	"inttofloat", "floattoint", "sqrt", "fmod",
	"band", "bor", "bxor", "bnot", "shl", "shr",
	"eq", "ne", "lt", "le", "gt", "ge", "not",
	"label", "jump", "jumpif", "jumpnot",
	"param", "call", "ret",
	"printint", "printfloat", "printbool", "printstr",
	"concat", "streq", "strlen", "inttostr", "floattostr", "booltostr",
	"alloc", "indexaddr", "loadmem", "storemem",
	"writestr", "writeint", "writefloat", "boundsfail",
	"loadbyte", "storebyte",
	"mustfail", "globaladdr", "stackptr", "symaddr", "slotaddr",
	"callclosure", "peek", "poke",
}

func (o Op) String() string {
	if int(o) < len(opNames) {
		return opNames[o]
	}
	return "op?"
}

type Instr struct {
	Op       Op
	Dst      Reg
	A, B     Reg
	Imm      int64
	Args     []Reg // OpCall only
	ArgTypes []vty // OpCall only, parallel to Args: the emitter needs to
	// know which of the outgoing slots is a float, since
	// the Windows x64 convention routes a float argument
	// to xmm0-3 while an int in the same position goes
	// to rcx/rdx/r8/r9 - the choice depends on the type
	// in that position, not on how many of each kind
	// came before it.
	RetType vty    // OpCall only: whether the result comes back in rax or xmm0
	Sym     string // OpCall only: the function being called
	Extern  bool   // OpCall only: Sym is a raw symbol, not a Veyl function.
	// Veyl functions are emitted as __vy_<name> so that a
	// program calling its own fn strlen cannot collide with
	// libc; a foreign symbol has to be called by the name the
	// linker knows, so the emitter must be told which it is.
	Ret32 bool // OpCall only: Sym returns a C int, so only eax is
	// meaningful and the emitter must sign-extend it. The
	// upper half of rax is undefined on such a return, and
	// reading it whole gives a value that is usually right.
	Variadic bool // OpCall only: Sym is a C variadic. The Windows x64
	// convention says a float passed in the variadic part
	// goes in both the xmm and the matching integer
	// register, because the callee reading a va_list has
	// no prototype telling it which file to look in.
	Comment string // the Veyl this came from, carried into the .s file
}

// A Func is one lowered function.
type Func struct {
	Name       string
	NParams    int
	ParamTypes []vty // parallel to the incoming OpParam instructions;
	// the emitter needs this for the same reason OpCall
	// carries ArgTypes - which physical register an
	// incoming argument arrives in depends on its type.
	Ret  vty
	Code []Instr

	NRegs   int // virtual registers used
	NSlots  int // stack slots for locals and spilled temporaries
	NLabels int

	// RegTypes is the type of every virtual register, parallel to the
	// register numbers. The register allocator reads it to decide which
	// values may sit in a machine register: only non-pointers, because a
	// pointer the collector cannot see is a pointer it can free.
	RegTypes []vty

	// MaxCallArgs is the widest call this function makes. The outgoing
	// argument area has to be big enough for it, and is reserved once in
	// the prologue rather than pushed per call, which is what keeps rsp
	// 16-byte aligned without any per-call arithmetic.
	MaxCallArgs int

	// Env marks a lifted function literal. Its environment arrives in a
	// register rather than as an argument, and the prologue writes that
	// register into slot zero before anything can clobber it. A plain
	// declared function has this false and ignores the register, which
	// is what lets one be used as a function value with no wrapper.
	Env bool
}

// A Module is the whole program: its functions and its string pool.
type Module struct {
	Funcs   []*Func
	Strings []string
	Floats  []float64
	Helpers map[string]bool // runtime helpers actually used
	Thunks  []Thunk         // callback stubs; see callback.go
	Exports []Export        // `export fn`s, for a DLL build; see dll.go

	// DLL makes the program a library: an entry point Windows calls on
	// load instead of one that runs and exits, and an export table.
	DLL     bool
	Externs map[string]bool // foreign symbols called directly, declared
	// as .extern at the top of the .s file
	NGlobals int // words of static storage the program needs
}

func (m *Module) needs(h string) { m.Helpers[h] = true }

// needsExtern records a foreign symbol so the emitter declares it.
func (m *Module) needsExtern(sym string) { m.Externs[sym] = true }

// intern adds a string constant to the pool, reusing an existing entry
// when the same text appears twice.
func (m *Module) intern(s string) int64 {
	for i, existing := range m.Strings {
		if existing == s {
			return int64(i)
		}
	}
	m.Strings = append(m.Strings, s)
	return int64(len(m.Strings) - 1)
}

// internFloat is intern's counterpart for float literals, so the same
// bit pattern is not written into .rdata twice.
func (m *Module) internFloat(v float64) int64 {
	// By bit pattern: == would merge -0.0 into 0.0 and never match a NaN.
	for i, existing := range m.Floats {
		if math.Float64bits(existing) == math.Float64bits(v) {
			return int64(i)
		}
	}
	m.Floats = append(m.Floats, v)
	return int64(len(m.Floats) - 1)
}

// ---- function signatures ----

// A sig is what the lowerer knows about a function before it lowers a
// call to it. Built from the written annotations rather than from the
// checker, which is not shared yet.
type sig struct {
	params []vty
	ret    vty
}

// externSig is what the lowerer knows about a declared native function.
// It is a sig plus what a foreign call needs that a Veyl call does not:
// how wide the return register was when the C side left it there
// (ret32), whether the caller may pass extra arguments (variadic), and
// which library exports the symbol when the declaration said `from`.
type externSig struct {
	params []vty
	// widen is, for a function-typed parameter, which of the
	// callback's arguments arrive as a 32-bit C int or bool. See
	// callback.go.
	widen    map[int][]widenArg
	ret      vty
	ret32    bool
	variadic bool
	sym      string
	dll      string
}

// collectExtern records one extern declaration. The checker has already
// refused non-scalar parameters and returns; this pass works out what a
// call to the symbol will need at emission time.
//
// ret32 deserves its own word. A C function returning something that
// fits in 32 bits leaves the upper half of rax holding whatever was
// there before, so reading it as a full 64-bit value would be garbage
// half the time. `-> int` and `-> bool` therefore sign-extend from eax;
// `-> ptr` takes the whole register, because handles and pointers are
// genuine 64-bit values; `-> str` likewise, since what comes back is a
// char* to be copied; and `-> float` arrives in xmm0, untouched by all
// of this.
func (l *lowerer) collectExtern(fd *FnDecl) {
	es := externSig{sym: fd.Name, dll: fd.DLL, variadic: fd.Variadic}
	for _, pa := range fd.Params {
		t, good := typeOfName(pa.Type)
		// A function is a callback: the address of a stub that makes
		// the native call look like a Veyl one.
		if good && t.k == kFunc {
			if es.widen == nil {
				es.widen = map[int][]widenArg{}
			}
			es.widen[len(es.params)] = callbackWidening(pa.Type)
			es.params = append(es.params, t)
			continue
		}
		// A bytes buffer and an extern struct both cross as the address
		// of their first byte.
		if good && (t.k == kBytes || l.isView(t)) {
			es.params = append(es.params, t)
			continue
		}
		if !good || !scalarVty(t) {
			l.errorAt(fd, "parameter %q has type %q, which cannot cross into native code",
				pa.Name, pa.Type)
			continue
		}
		es.params = append(es.params, t)
	}
	ret, good := typeOfName(fd.Ret)
	if good && l.isView(ret) {
		es.ret = ret
		l.externFns[es.sym] = es
		return
	}
	if !good || !scalarVty(ret) {
		l.errorAt(fd, "return type %q cannot come back from native code", fd.Ret)
		return
	}
	switch r := strings.TrimSpace(fd.Ret); {
	case strings.HasPrefix(r, "*"):
		// A pointer comes back whole, like ptr.
	case r == "int" || r == "bool":
		es.ret32 = true
	case r == "" || r == "float" || r == "str" || r == "ptr":
	default:
		l.errorAt(fd, "return type %q cannot come back from native code", fd.Ret)
		return
	}
	es.ret = ret
	if es.dll != "" && !strings.HasSuffix(strings.ToLower(es.dll), ".dll") &&
		!strings.HasSuffix(strings.ToLower(es.dll), ".exe") {
		// Windows loads "miniz" and "miniz.dll" identically, but the import
		// table stores one canonical spelling and the missing-DLL check
		// stats for exactly that one.
		es.dll += ".dll"
	}
	if _, dup := l.externFns[es.sym]; dup {
		l.errorAt(fd, "%s is declared twice", es.sym)
		return
	}
	l.externFns[es.sym] = es
}

// scalarVty reports whether t can cross the native boundary at all.
func scalarVty(t vty) bool {
	switch t.k {
	case kInt, kFloat, kBool, kStr, kVoid:
		return true
	}
	return false
}

// ---- lowering ----

type lowerer struct {
	mod  *Module
	fn   *Func
	file string
	errs []string

	// Scopes, innermost last. A block introduces one, so an inner name
	// can shadow an outer one and stops existing at the closing brace.
	scopes []map[string]int64

	// boxed marks the slots that hold a one-word heap cell rather than
	// the value itself, because some closure captures the variable. See
	// closure.go.
	boxed map[int64]bool

	// captures maps a captured name to its index in the environment,
	// while a lifted function literal is being lowered, and captureTy to
	// what its cell holds.
	captures  map[string]int
	captureTy map[string]vty

	nClosures int

	// boxNames is the set of this function's own names that some literal
	// inside it captures, worked out before the body is lowered because
	// the decision has to be made where the name is declared.
	boxNames map[string]bool
	slotTy   map[int64]vty
	regTy    map[Reg]vty

	loops []loopTarget

	// defers holds, for each block being lowered, the statements it has
	// deferred so far; see defer.go.
	defers [][]Stmt
	sigs  map[string]sig

	// externFns holds the native functions the program declared with
	// `extern fn`. Keyed by plain name, the same way sigs is, because an
	// import lands in one flat namespace.
	externFns map[string]externSig

	// Helper functions written directly in the IR, by symbol, so each
	// is emitted once however many times it is called.
	helpers map[string]bool

	// gcStress makes every statement collect first. It exists to be
	// turned on for the whole test suite: a collector that frees a live
	// object is otherwise a bug that shows up somewhere else entirely,
	// hours later, and running every program with collection at every
	// statement turns that into a failure at the statement responsible.
	gcStress bool
	inHelper bool

	// inlineOnce names the rendering function being built, so the next
	// writeValue of its type is that function's own body rather than a
	// call to it; see renderOutlined.
	inlineOnce string

	// Where the code being lowered came from, for runtime errors to say;
	// see where.go.
	curFile   string
	curLine   int
	fnDisplay string

	// gcOff is a program that said `gc off`: it frees with delete, and
	// nothing collects, not even mem.collect.
	gcOff    bool
	eqInline string

	// autoGC is automatic collection: on, except in a DLL, whose
	// functions the host may call on threads of its own, and when
	// VEYL_GC=off asks for the old behaviour.
	autoGC bool

	// mainLets are the names the top-level statements declare with let,
	// and initGlobal is set while a const or var is being computed:
	// between them, an undefined name can be explained rather than only
	// reported.
	mainLets   map[string]bool
	initGlobal bool

	// Declared structs, by name. Filled before anything is lowered,
	// because a function signature can name one.
	structs map[string]*structLayout

	// buf is the stack slot a string being built lives in, or -1 when
	// writes go to stdout. Rendering a container into a string reuses
	// the code that prints one, pointed at a buffer instead - the two
	// have to agree character for character, and the only way to be
	// sure of that is for there to be one of them.
	buf int64

	// globals maps a top-level const to its slot in static storage, and
	// globalTy remembers what is in it. A top-level `let` is not here:
	// it stays a local of the implicit main, which is what the language
	// says.
	globals  map[string]int64
	globalTy map[int64]vty

	// hint is the type the surrounding context expects. An empty list
	// literal has no element to infer from, so `let xs: []int = []` can
	// only work if the annotation reaches the literal.
	hint vty
}

type loopTarget struct {
	brk  int64
	cont int64

	// defers is how many blocks of deferred statements were open when
	// the loop began: a break or continue runs the ones opened since.
	defers int
}

// Lower turns the parsed program into a module. It reports its own
// errors rather than aborting, matching the rest of the pipeline: one
// mistake should not hide the next one.
func Lower(p *Program, file string) (*Module, []string) {
	enumVariants = map[string][]string{}
	for _, e := range p.Enums {
		enumVariants[e.Name] = e.Variants
	}
	l := &lowerer{
		mod:       &Module{Helpers: map[string]bool{}, Externs: map[string]bool{}},
		file:      file,
		sigs:      map[string]sig{},
		externFns: map[string]externSig{},
		globals:   map[string]int64{},
		globalTy:  map[int64]vty{},
		buf:       -1,
		gcStress:  os.Getenv("VEYL_GC_STRESS") != "" && p.GC != "off",
		autoGC:    !lowerForDLL && os.Getenv("VEYL_GC") != "off" && p.GC != "off",
		gcOff:     p.GC == "off",
	}

	// Struct layouts before signatures, because a parameter or a return
	// type can name one and the signature needs its vty.
	l.collectStructs(p)
	l.mainLets = map[string]bool{}
	for _, st := range p.Main {
		if let, ok := st.(*LetStmt); ok && !let.Const {
			l.mainLets[let.Name] = true
		}
	}

	// Signatures next, so a function can call one declared below it.
	// The Go backend guarantees order-independent declaration and this
	// has to match.
	for _, fd := range p.Funcs {
		if fd.Extern {
			l.collectExtern(fd)
			continue
		}
		s := sig{}
		ok := true
		for i, pa := range fd.Params {
			// `self` has no annotation - its type is the impl block it
			// was written in, which is why the checker fills it in from
			// the receiver rather than from the source text.
			if i == 0 && fd.Recv != "" && pa.Name == "self" {
				s.params = append(s.params, vStructOf(fd.Recv))
				continue
			}
			t, good := typeOfName(pa.Type)
			if !good || t.k == kVoid {
				l.errorAt(fd, "parameter %q has type %q, which the assembly backend does not handle yet",
					pa.Name, pa.Type)
				ok = false
				continue
			}
			s.params = append(s.params, t)
		}
		ret, good := typeOfName(fd.Ret)
		if !good {
			l.errorAt(fd, "return type %q is not on the assembly backend yet", fd.Ret)
			ok = false
		}
		s.ret = ret
		if ok {
			l.sigs[methodName(fd.Recv, fd.Name)] = s
		}
		if fd.Export && fd.Recv == "" {
			l.mod.Exports = append(l.mod.Exports, exportOf(fd))
		}
	}

	// Globals are declared before any function is lowered, so a function
	// can name one. They are only *declared* here - the values are
	// computed at the top of main, which is the first thing that runs.
	for _, g := range p.Globals {
		if _, dup := l.globals[g.Name]; dup {
			continue // importing the same file twice is harmless
		}
		// The type comes from the checker, which has already been over
		// this program. It has to be known now rather than when the
		// value is computed, because a function lowered before main
		// may read the global and needs to know what is in it.
		t, ok := vtyOf(g.T)
		if !ok {
			l.errorAt(g, "a global of type %s is not on the assembly backend yet", g.T)
			continue
		}
		slot := int64(len(l.globals)) + gcReserved
		l.globals[g.Name] = slot
		l.globalTy[slot] = t
	}

	for _, fd := range p.Funcs {
		if fd.Extern {
			continue // no body to lower; calls go straight to the symbol
		}
		l.function(fd)
	}

	// main last, so it reads as the entry point at the bottom of the
	// listing the way it does in the source.
	l.fn = &Func{Name: "main", Ret: vVoid}
	l.curFile, l.fnDisplay = file, "the top level"
	l.slotTy = map[int64]vty{}
	l.regTy = map[Reg]vty{}
	l.boxed = map[int64]bool{}
	l.captures = nil
	l.captureTy = nil
	l.boxNames = capturedInStmts(p.Main)
	l.pushScope()
	// The runtime's own words sit ahead of the program's, so a program
	// with no globals at all still has somewhere to keep the object
	// list. How many there are in total is written down where the
	// collector can read it, since it scans them as roots - and before
	// any global is computed, since computing one can collect.
	l.mod.NGlobals = len(l.globals) + gcReserved
	l.rtStore(gcNGlobSlot, l.constant(int64(l.mod.NGlobals)))
	l.rtStore(gcNextSlot, l.constant(gcFirst))
	l.installCrashHandler()
	// The globals' values are computed here, at the top of main, and
	// written into static storage. Every user function is reached from
	// main, so nothing can read one before this runs.
	done := map[string]bool{}
	for _, g := range p.Globals {
		if done[g.Name] {
			continue
		}
		done[g.Name] = true
		l.globalInit(g)
	}
	l.stmtList(p.Main)
	l.popScope()
	l.seal()

	// Before the crash handler, which lists every function by name.
	l.stripCollector()
	l.buildCrashHandler()
	l.checkLabels()
	return l.mod, l.errs
}

// checkLabels reports a jump to a label nothing ever marked.
//
// That is a bug in this compiler, not in the program, and without this
// it surfaces as the linker complaining about an undefined reference to
// `.Lmain_164` - a name from a file the user never saw, at a moment when
// they have every reason to think their own code is at fault. It has
// happened twice: a branch written with an early return that skipped
// its own mark, and a label created for a case that turned out not to
// need one.
func (l *lowerer) checkLabels() {
	for _, f := range l.mod.Funcs {
		marked := map[int64]bool{}
		for _, in := range f.Code {
			if in.Op == OpLabel {
				marked[in.Imm] = true
			}
		}
		for _, in := range f.Code {
			switch in.Op {
			case OpJump, OpJumpIf, OpJumpNot:
				if !marked[in.Imm] {
					l.errs = append(l.errs, fmt.Sprintf(
						"%s: internal error: %s jumps to label L%d, which is never "+
							"placed - this is a compiler bug, not a mistake in the program",
						l.file, f.Name, in.Imm))
					return
				}
			}
		}
	}
}

func (l *lowerer) function(fd *FnDecl) {
	name := methodName(fd.Recv, fd.Name)
	s, known := l.sigs[name]
	if !known {
		return // its signature was already rejected
	}

	l.fn = &Func{Name: name, NParams: len(fd.Params), ParamTypes: s.params, Ret: s.ret}
	l.curFile, l.fnDisplay = fd.File, name
	l.slotTy = map[int64]vty{}
	l.regTy = map[Reg]vty{}
	l.boxed = map[int64]bool{}
	l.captures = nil
	l.boxNames = capturedIn(fd)
	l.pushScope()

	// Parameters arrive in registers and are immediately written to
	// slots, like every other value. The allocator will hoist the ones
	// worth keeping in registers; doing it here would be guessing.
	for i, pa := range fd.Params {
		// The parameter is read into a slot before anything else,
		// because boxing allocates and an allocation is a call - and a
		// call clobbers the very registers the arguments arrived in.
		// Getting this backwards does not fail: it reads whatever malloc
		// left behind, so a captured parameter comes out as a plausible
		// wrong number.
		d := l.newReg()
		l.regTy[d] = s.params[i]
		l.emit(Instr{Op: OpParam, Dst: d, A: NoReg, B: NoReg, Imm: int64(i),
			Comment: pa.Name})
		slot := l.declareMaybeBoxed(pa.Name, s.params[i], l.boxNames[pa.Name])
		l.storeLocal(slot, d)
	}

	l.defers = nil
	l.stmtList(fd.Body.Stmts)
	l.popScope()

	l.endFunction(s.ret)
	l.seal()
}

// seal records the types this function's virtual registers were given
// and files the finished function. The register allocator reads them;
// nothing after the lowerer re-derives a type it threw away.
func (l *lowerer) seal() {
	l.fn.RegTypes = make([]vty, l.fn.NRegs)
	for r, t := range l.regTy {
		if r >= 0 && int(r) < len(l.fn.RegTypes) {
			l.fn.RegTypes[r] = t
		}
	}
	l.mod.Funcs = append(l.mod.Funcs, l.fn)
}

// endFunction closes a function off with the fallthrough return.
//
// A function that runs off the end returns zero. The Go backend's
// return-path checking would have rejected that already for a non-void
// function, and this backend does not have it yet, so the safe thing is
// a defined value rather than whatever happens to be in rax or xmm0. The
// zero has to be the right kind of zero: a float return reads xmm0, so
// an int OpConst left there would be garbage.
func (l *lowerer) endFunction(ret vty) {
	switch {
	case ret.k == kVoid && !ret.res && !ret.null:
		l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg})

	// A T! is a pointer to a two-word object, so the zero here has to be
	// a real one rather than the integer zero below. Falling off the end
	// of a void! is the ordinary way to say it worked:
	//
	//     fn check(n: int) -> void! {
	//         if n < 0 { return fail("negative") }
	//     }
	//
	// That used to return 0, and the first isOk on it dereferenced a
	// null pointer. For a non-void T! running off the end is a missing
	// return that nothing here catches yet, so it gets a defined value
	// rather than a crash.
	case ret.res:
		inner := ret.inner()
		var zero Reg
		switch {
		case inner.k == kVoid || inner.null:
			zero = l.constant(0)
		case inner.k == kFloat:
			zero = l.floatConst(0)
		default:
			zero = l.constant(0)
		}
		l.emit(Instr{Op: OpRet, A: l.resOk(zero, ret), Dst: NoReg})

	case ret.k == kFloat && !ret.res && !ret.null:
		z := l.newReg()
		l.regTy[z] = vFloat
		l.emit(Instr{Op: OpFConst, Dst: z, A: NoReg, B: NoReg, Imm: l.mod.internFloat(0)})
		l.emit(Instr{Op: OpRet, A: z, Dst: NoReg})
	default:
		z := l.newReg()
		l.regTy[z] = vInt
		l.emit(Instr{Op: OpConst, Dst: z, A: NoReg, B: NoReg, Imm: 0})
		l.emit(Instr{Op: OpRet, A: z, Dst: NoReg})
	}
}

// declareMaybeBoxed declares a local, in a heap cell when some closure
// captures it. The slot then holds the cell rather than the value, and
// every read and write of the name goes through loadLocal and
// storeLocal, which know the difference.
func (l *lowerer) declareMaybeBoxed(name string, t vty, box bool) int64 {
	slot := l.declare(name, t)
	if box {
		if l.boxed == nil {
			l.boxed = map[int64]bool{}
		}
		l.boxed[slot] = true
		cell := l.newBox(t)
		l.emit(Instr{Op: OpStore, A: cell, Dst: NoReg, Imm: slot,
			Comment: "box " + name})
	}
	return slot
}

// storeLocal writes a value to a declared slot, through the box when
// there is one.
func (l *lowerer) storeLocal(slot int64, v Reg) {
	if l.boxed[slot] {
		cell := l.newReg()
		l.regTy[cell] = vInt
		l.emit(Instr{Op: OpLoad, Dst: cell, A: NoReg, B: NoReg, Imm: slot})
		l.emit(Instr{Op: OpStoreMem, A: cell, B: v, Dst: NoReg, Imm: 0})
		return
	}
	l.emit(Instr{Op: OpStore, A: v, Dst: NoReg, Imm: slot})
}

// loadLocal reads a declared slot, through the box when there is one.
func (l *lowerer) loadLocal(slot int64) Reg {
	t := l.slotTy[slot]
	if l.boxed[slot] {
		cell := l.newReg()
		l.regTy[cell] = vInt
		l.emit(Instr{Op: OpLoad, Dst: cell, A: NoReg, B: NoReg, Imm: slot})
		d := l.newReg()
		l.regTy[d] = t
		l.emit(Instr{Op: OpLoadMem, Dst: d, A: cell, B: NoReg, Imm: 0})
		return d
	}
	d := l.newReg()
	l.regTy[d] = t
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: slot})
	return d
}

// ---- scopes ----

func (l *lowerer) pushScope() {
	l.scopes = append(l.scopes, map[string]int64{})
	if l.slotTy == nil {
		l.slotTy = map[int64]vty{}
		l.regTy = map[Reg]vty{}
	}
}

func (l *lowerer) popScope() { l.scopes = l.scopes[:len(l.scopes)-1] }

// declare puts a name in the innermost scope with a fresh slot. Slots
// are never reused across scopes: the frame is a few bytes larger and
// nothing can alias.
func (l *lowerer) declare(name string, t vty) int64 {
	slot := int64(l.fn.NSlots)
	l.fn.NSlots++
	l.scopes[len(l.scopes)-1][name] = slot
	l.slotTy[slot] = t
	return slot
}

func (l *lowerer) lookup(name string) (int64, bool) {
	for i := len(l.scopes) - 1; i >= 0; i-- {
		if slot, ok := l.scopes[i][name]; ok {
			return slot, true
		}
	}
	return 0, false
}

// temp makes an anonymous slot, for values that two branches write.
func (l *lowerer) temp(t vty) int64 {
	slot := int64(l.fn.NSlots)
	l.fn.NSlots++
	l.slotTy[slot] = t
	return slot
}

func (l *lowerer) errorAt(n Node, format string, args ...any) {
	line, col := n.Pos()
	l.errs = append(l.errs, fmt.Sprintf("%s:%d:%d: %s", l.file, line, col,
		fmt.Sprintf(format, args...)))
}

func (l *lowerer) newReg() Reg {
	r := Reg(l.fn.NRegs)
	l.fn.NRegs++
	return r
}

func (l *lowerer) emit(in Instr) { l.fn.Code = append(l.fn.Code, in) }

func (l *lowerer) newLabel() int64 {
	n := int64(l.fn.NLabels)
	l.fn.NLabels++
	return n
}

// coerce fits a value into the type a position wants, for the two
// conversions the checker does not represent as a node: a bare `nil`
// taking the shape of the ?T it is being stored into, and an untyped
// int literal widening into a float slot. It reports false when the two
// types are simply different, which is the caller's cue to complain.
func (l *lowerer) coerce(v Reg, want vty) (Reg, bool) {
	have := l.regTy[v]
	switch {
	case have.eq(want):
		return v, true
	case want.null && have.eq(vNil):
		return l.nilValue(want), true
	case want.null && have.eq(want.notNull()):
		// The checker normally marks this with a Widen; a position it
		// does not reach gets the same boxing here rather than a
		// mismatch the user cannot act on.
		return l.nullBox(v, want), true
	case want.k == kFloat && !want.null && have.k == kInt && !have.null:
		return l.toFloat(v), true
	}
	return v, false
}

// rvalueAs lowers an expression in a context that already knows what
// type it wants.
//
// It exists for the empty literal. `[]` and `{}` carry no element to
// infer from, so the only thing that can type one is what surrounds it,
// and before this the only surrounding that reached the literal was a
// let annotation. An argument, a return and a struct field all know
// what they want just as precisely.
//
// The hint is restored rather than cleared, so a nested expression sees
// the hint of the position it is actually in.
func (l *lowerer) rvalueAs(e Expr, want vty) Reg {
	saved := l.hint
	l.hint = want
	v := l.rvalue(e)
	l.hint = saved
	return v
}

// methodName is how a method is stored and called.
//
// A method is an ordinary function with the receiver as its first
// argument, named "Type.method" so that two structs can have a method of
// the same name and neither can collide with a plain function - a
// program is free to declare `fn area()` as well as
// `impl Circle { fn area() }`. The dot is not a namespace here any more
// than it is in os.file.read: it is one name with a dot in it.
func methodName(recv, name string) string {
	if recv == "" {
		return name
	}
	return recv + "." + name
}

func (l *lowerer) mark(label int64) {
	l.emit(Instr{Op: OpLabel, A: NoReg, Dst: NoReg, Imm: label})
}

// ---- statements ----

func (l *lowerer) stmt(s Stmt) {
	l.curLine, _ = s.Pos()
	// Stress mode. Not inside a helper: the collector is written in the
	// IR itself, and collecting on the way into collecting would not
	// terminate.
	if l.gcStress && !l.inHelper {
		l.collect()
	} else if l.autoGC && !l.inHelper {
		l.maybeCollect()
	}

	switch st := s.(type) {
	case *LetStmt:
		saved := l.hint
		if st.Type != "" {
			if want, ok := typeOfName(st.Type); ok {
				l.hint = want
			}
		}
		v := l.rvalue(st.Value)
		l.hint = saved

		t := l.regTy[v]
		if st.Type != "" {
			declared, ok := typeOfName(st.Type)
			if !ok {
				l.errorAt(st, "type %q is not on the assembly backend yet", st.Type)
				return
			}
			// The checker already accepted an untyped int literal going
			// into a float slot - Go's own untyped-constant rule, which
			// Veyl copies - so that combination is promoted rather than
			// rejected here, along with a bare nil taking the shape of
			// the ?T it is going into.
			if fitted, ok := l.coerce(v, declared); ok {
				v = fitted
			} else {
				l.errorAt(st, "%s was declared %s but the value is %s",
					st.Name, declared, t)
			}
			t = declared
		}
		slot := l.declareMaybeBoxed(st.Name, t, l.boxNames[st.Name])
		l.storeLocal(slot, v)

	case *AssignStmt:
		l.assign(st)

	case *ExprStmt:
		// A bare `f(x)?` is a statement, not a dead expression: the `?`
		// is the whole point of it, discarding the value but not the
		// failure. Everything else that is not a call really does
		// nothing, and saying so is a warning worth keeping.
		if try, ok := st.X.(*Try); ok {
			l.tryExpr(try)
			return
		}
		call, ok := st.X.(*Call)
		if !ok {
			l.errorAt(st, "this statement does nothing")
			return
		}
		l.call(call)

	case *Block:
		l.pushScope()
		l.stmtList(st.Stmts)
		l.popScope()

	case *DeferStmt:
		// Recorded, not lowered: it is lowered at each way out of its
		// block. See defer.go.
		top := len(l.defers) - 1
		l.defers[top] = append(l.defers[top], st.Body)

	case *IfStmt:
		cond := l.expr(st.Cond)
		if st.Else == nil {
			done := l.newLabel()
			l.emit(Instr{Op: OpJumpNot, A: cond, Dst: NoReg, Imm: done, Comment: "if"})
			l.stmt(st.Then)
			l.mark(done)
			return
		}
		otherwise := l.newLabel()
		done := l.newLabel()
		l.emit(Instr{Op: OpJumpNot, A: cond, Dst: NoReg, Imm: otherwise, Comment: "if"})
		l.stmt(st.Then)
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
		l.mark(otherwise)
		l.stmt(st.Else)
		l.mark(done)

	case *WhileStmt:
		top := l.newLabel()
		done := l.newLabel()
		l.mark(top)
		cond := l.expr(st.Cond)
		l.emit(Instr{Op: OpJumpNot, A: cond, Dst: NoReg, Imm: done, Comment: "while"})
		l.loops = append(l.loops, loopTarget{brk: done, cont: top, defers: len(l.defers)})
		l.stmt(st.Body)
		l.loops = l.loops[:len(l.loops)-1]
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})
		l.mark(done)

	case *ForStmt:
		if st.Lowered != nil {
			l.stmt(st.Lowered) // a loop over a channel; see the checker
			return
		}
		l.forRange(st)

	case *MatchStmt:
		if st.Lowered != nil {
			// A match on a data enum, which the checker has already
			// turned into ifs on a local holding the subject.
			l.pushScope()
			v := l.rvalue(st.Subject)
			l.storeLocal(l.declare(st.Temp, l.regTy[v]), v)
			l.stmt(st.Lowered)
			l.popScope()
			return
		}
		l.match(st)

	case *ReturnStmt:
		if st.Value == nil {
			// A bare `return` from a function declared void! still owes
			// the caller a result to inspect, so it returns a successful
			// one. Anywhere else it returns nothing at all.
			l.runDefers(0)
			if l.fn.Ret.res {
				l.emit(Instr{Op: OpRet, A: l.resOk(l.constant(0), l.fn.Ret),
					Dst: NoReg, Comment: "return ok"})
				return
			}
			l.emit(Instr{Op: OpRet, A: NoReg, Dst: NoReg, Comment: "return"})
			return
		}
		// A function returning []int can be returned an empty literal,
		// so the return type is a hint like any other. Strip the `!`
		// first: `return []` inside a []int! is boxed by a Widen the
		// checker inserted, and what the literal has to build is the
		// list, not the result around it.
		v := l.rvalueAs(st.Value, l.fn.Ret.inner())

		// Boxing a plain value into the T! a function promises is not
		// decided here: the checker already marked the spot with a
		// Widen, the same way it does for a nullable. All that is left
		// is the int-to-float promotion it does not represent as a node.
		if l.fn.Ret.k == kFloat && !l.fn.Ret.res && l.regTy[v].k == kInt {
			v = l.toFloat(v)
		}
		if l.hasDefers(0) {
			// The value is worked out first, as a deferred statement
			// could change what it reads; it waits in a slot meanwhile.
			slot := l.temp(l.regTy[v])
			l.emit(Instr{Op: OpStore, A: v, Dst: NoReg, Imm: slot})
			l.runDefers(0)
			v = l.load(slot, l.regTy[v])
		}
		l.emit(Instr{Op: OpRet, A: v, Dst: NoReg, Comment: "return"})

	case *BreakStmt:
		if len(l.loops) == 0 {
			l.errorAt(st, "break outside a loop")
			return
		}
		l.runDefers(l.loops[len(l.loops)-1].defers)
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg,
			Imm: l.loops[len(l.loops)-1].brk, Comment: "break"})

	case *ContinueStmt:
		if len(l.loops) == 0 {
			l.errorAt(st, "continue outside a loop")
			return
		}
		l.runDefers(l.loops[len(l.loops)-1].defers)
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg,
			Imm: l.loops[len(l.loops)-1].cont, Comment: "continue"})

	default:
		l.errorAt(s.(Node), "the assembly backend does not handle this statement yet")
	}
}

func (l *lowerer) assign(st *AssignStmt) {
	if idx, isIndex := st.Target.(*Index); isIndex {
		// The collection and the index are each lowered once, so
		// `counts[next()] += 1` does not advance next() twice - a
		// compound assignment reads and writes the same place, and
		// working out where it is twice would be a different place.
		coll := l.expr(idx.X)
		t := l.regTy[coll]
		switch t.k {
		case kList:
			key := l.expr(idx.Idx)
			val := l.rvalueAs(st.Value, t.elemType())
			if st.Op != ASSIGN {
				cur := l.listGet(coll, key, t.elemType())
				out, good := l.compound(st, st.Op, t.elemType(), cur, val)
				if !good {
					return
				}
				val = out
			}
			if !l.regTy[val].eq(t.elemType()) {
				l.errorAt(st, "this list holds %s, but the value is %s", t.elemType(), l.regTy[val])
				return
			}
			l.listSet(coll, key, val)
			return
		case kMap:
			key := l.expr(idx.Idx)
			if l.regTy[key].k != t.key {
				l.errorAt(st, "this map is keyed by %s, but the index is %s", t.keyType(), l.regTy[key])
				return
			}
			val := l.rvalueAs(st.Value, t.elemType())
			if st.Op != ASSIGN {
				// A missing key reads as the zero value, which is what
				// makes `counts[word] += 1` the idiom it is on the Go
				// backend too.
				cur := l.mapGet(st, coll, key, t)
				out, good := l.compound(st, st.Op, t.elemType(), cur, val)
				if !good {
					return
				}
				val = out
			}
			if !l.regTy[val].eq(t.elemType()) {
				l.errorAt(st, "this map holds %s, but the value is %s", t.elemType(), l.regTy[val])
				return
			}
			l.mapSet(coll, key, val, t)
			return
		}
		if isPtr(t) {
			l.ptrAssign(st, coll, idx.Idx)
			return
		}
		l.errorAt(st, "only a list or a map can be indexed on this backend so far")
		return
	}
	if u, isDeref := st.Target.(*Unary); isDeref && u.Op == STAR {
		l.ptrAssign(st, l.expr(u.X), nil)
		return
	}

	if f, isField := st.Target.(*Field); isField {
		l.fieldAssign(st, f)
		return
	}

	id, ok := st.Target.(*Ident)
	if !ok {
		l.errorAt(st, "the assembly backend can only assign to a plain name, a field or an index")
		return
	}
	if l.assignCaptured(st, id) {
		return
	}
	slot, known := l.lookup(id.Name)
	if !known {
		if g, isGlobal := l.globals[id.Name]; isGlobal {
			l.assignGlobal(st, g)
			return
		}
		l.undefined(st, id.Name)
		return
	}
	v := l.rvalue(st.Value)
	target := l.slotTy[slot]

	// Plain assignment into a float variable accepts the same untyped
	// int literal promotion `let` does; the checker already allowed it.
	if st.Op == ASSIGN && target.k == kFloat && l.regTy[v].k == kInt {
		v = l.toFloat(v)
	}

	// The compound forms read the target first. Ignoring st.Op here
	// silently turned `i += 1` into `i = 1`, which made every loop that
	// counted upwards run forever - a miscompile that produces no output
	// rather than wrong output, so nothing catches it except a deadline.
	if st.Op != ASSIGN {
		var applied bool
		v, applied = l.compound(st, st.Op, target, l.loadLocal(slot), v)
		if !applied {
			return
		}
	}

	l.storeLocal(slot, v)
}

// compound applies the operator behind a compound assignment, given the
// target's current value. It reports false once it has explained why it
// could not, which is the caller's cue to stop rather than store junk.
//
// Extracted so that `u.age += 1` on a struct field and `i += 1` on a
// variable cannot disagree. Ignoring the operator here once turned
// `i += 1` into `i = 1`, which made every counting loop run forever - a
// miscompile producing no output rather than wrong output, so nothing
// catches it except a deadline.
func (l *lowerer) compound(n Node, k Kind, target vty, cur, v Reg) (Reg, bool) {
	if isPtr(target) {
		return l.ptrStep(n, k, target, cur, v)
	}
	isFloat := target.k == kFloat
	if isFloat && l.regTy[v].k == kInt {
		v = l.toFloat(v)
	}

	var op Op
	switch k {
	case PLUSEQ:
		switch {
		case target.k == kStr:
			op = OpConcat
			l.mod.needs("concat")
		case isFloat:
			op = OpFAdd
		default:
			op = OpAdd
		}
	case MINUSEQ:
		op = pickOp(isFloat, OpFSub, OpSub)
	case STAREQ:
		op = pickOp(isFloat, OpFMul, OpMul)
	case SLASHEQ:
		op = pickOp(isFloat, OpFDiv, OpDiv)
	case PERCENTEQ:
		if isFloat {
			l.errorAt(n, "%%= needs two ints - use mod(...) for floats")
			return v, false
		}
		op = OpMod
	case AMPEQ:
		op = OpBAnd
	case PIPEEQ:
		op = OpBOr
	case CARETEQ:
		op = OpBXor
	case SHLEQ:
		op = OpShl
	case SHREQ:
		op = OpShr
	default:
		l.errorAt(n, "the assembly backend does not handle %s yet", k)
		return v, false
	}

	d := l.newReg()
	l.regTy[d] = target
	l.emit(Instr{Op: op, Dst: d, A: cur, B: v})
	return d, true
}

// pickOp chooses between a float and an int op, the same choice
// binary() makes for the infix forms. Named apart from the lowerer's
// own pick (a branch-select over two values) even though Go would not
// object to the clash, because a reader should not have to check which
// one a call site means.
func pickOp(float bool, f, i Op) Op {
	if float {
		return f
	}
	return i
}

// toFloat converts an int-typed register to a float-typed one. Every
// site that promotes an untyped int literal - let, assignment, return,
// a call argument, a binary operand - goes through this, so there is
// one place that knows what "int becomes float" means at the IR level.
func (l *lowerer) toFloat(v Reg) Reg {
	d := l.newReg()
	l.regTy[d] = vFloat
	l.emit(Instr{Op: OpIntToFloat, Dst: d, A: v, B: NoReg})
	return d
}

// forRange lowers the counted form into the same shape as a while loop.
//
// The bound and the step are each evaluated once into a slot, so
// `for i in 1..f()` calls f once rather than every iteration. Whether
// the loop counts up or down decides the comparison, and that has to be
// known here, so the step must be a literal for now: a dynamic one needs
// a runtime sign test that nothing yet writes programs to exercise.
func (l *lowerer) forRange(st *ForStmt) {
	if st.Var2 != "" && st.Coll == nil {
		l.errorAt(st, "the two-variable for form needs a list or a map to iterate")
		return
	}
	if st.Coll != nil {
		l.forList(st)
		return
	}

	step := int64(1)
	if st.Step != nil {
		n, ok := constInt(st.Step)
		if !ok {
			l.errorAt(st, "step must be an integer literal on the assembly backend so far")
			return
		}
		if n == 0 {
			l.errorAt(st, "step cannot be zero")
			return
		}
		step = n
	}

	l.pushScope()

	start := l.expr(st.Start)
	iSlot := l.declare(st.Var, vInt)
	l.emit(Instr{Op: OpStore, A: start, Dst: NoReg, Imm: iSlot,
		Comment: "for " + st.Var})

	end := l.expr(st.End)
	endSlot := l.temp(vInt)
	l.emit(Instr{Op: OpStore, A: end, Dst: NoReg, Imm: endSlot, Comment: "limit"})

	top := l.newLabel()
	cont := l.newLabel()
	done := l.newLabel()
	l.mark(top)

	// Counting up stops at <, counting down at >. Inclusive ranges use
	// the or-equal form.
	var cmp Op
	switch {
	case step > 0 && st.Inclusive:
		cmp = OpLe
	case step > 0:
		cmp = OpLt
	case st.Inclusive:
		cmp = OpGe
	default:
		cmp = OpGt
	}

	iv := l.newReg()
	l.regTy[iv] = vInt
	l.emit(Instr{Op: OpLoad, Dst: iv, A: NoReg, B: NoReg, Imm: iSlot})
	ev := l.newReg()
	l.regTy[ev] = vInt
	l.emit(Instr{Op: OpLoad, Dst: ev, A: NoReg, B: NoReg, Imm: endSlot})
	test := l.newReg()
	l.regTy[test] = vBool
	l.emit(Instr{Op: cmp, Dst: test, A: iv, B: ev})
	l.emit(Instr{Op: OpJumpNot, A: test, Dst: NoReg, Imm: done})

	// continue jumps to the increment, not the test, or the counter
	// never advances and the loop never ends.
	l.loops = append(l.loops, loopTarget{brk: done, cont: cont, defers: len(l.defers)})
	l.stmt(st.Body)
	l.loops = l.loops[:len(l.loops)-1]

	l.mark(cont)
	cur := l.newReg()
	l.regTy[cur] = vInt
	l.emit(Instr{Op: OpLoad, Dst: cur, A: NoReg, B: NoReg, Imm: iSlot})
	inc := l.newReg()
	l.regTy[inc] = vInt
	l.emit(Instr{Op: OpConst, Dst: inc, A: NoReg, B: NoReg, Imm: step})
	next := l.newReg()
	l.regTy[next] = vInt
	l.emit(Instr{Op: OpAdd, Dst: next, A: cur, B: inc})
	l.emit(Instr{Op: OpStore, A: next, Dst: NoReg, Imm: iSlot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: top})

	l.mark(done)
	l.popScope()
}

// constInt folds the integer expressions the lowerer has to know at
// compile time, which so far is only a loop's step. `step -2` is a unary
// minus around a literal rather than a negative literal, so a bare type
// assertion on *IntLit rejects the one form people actually write.
func constInt(e Expr) (int64, bool) {
	switch x := e.(type) {
	case *IntLit:
		n, err := strconv.ParseInt(strings.ReplaceAll(x.Val, "_", ""), 0, 64)
		return n, err == nil
	case *Unary:
		if x.Op != MINUS {
			return 0, false
		}
		n, ok := constInt(x.X)
		return -n, ok
	}
	return 0, false
}

// ---- calls ----

func (l *lowerer) call(c *Call) Reg {
	if c.Lit != nil {
		return l.expr(c.Lit) // a data enum's variant; see dataenums.go
	}
	// DottedName flattens both a plain identifier and a chain of field
	// accesses, so `print` and `time.now` arrive here the same way. The
	// checker has already refused any dotted name the library does not
	// declare, which is why nothing below needs to guess whether a
	// namespace exists.
	// A method call is a field access on something of struct type, and
	// it has to be recognised before the callee is flattened: `p.sum`
	// would otherwise be looked up as a library name, and the error
	// would say that p.sum is not on this backend when the problem is
	// that it is a method.
	if fld, isField := c.Callee.(*Field); isField {
		if recv, isMethod := l.methodCall(fld); isMethod {
			return l.callMethod(c, fld, recv)
		}
	}

	// A call through a function value. The checker marks these, which is
	// what tells `f(3)` where f is a local apart from `double(3)` where
	// double is declared - the two look identical here otherwise, and
	// the second must stay a direct call.
	if c.ViaValue {
		return l.callValue(c)
	}

	name, ok := DottedName(c.Callee)
	if !ok {
		l.errorAt(c, "the assembly backend cannot call this expression yet")
		return l.junk()
	}

	if es, isExtern := l.externFns[name]; isExtern {
		return l.callExtern(c, name, es)
	}

	// Player(addr): a view at an address, which costs nothing - the
	// address already is the value.
	if lay, isView := l.structs[name]; isView && lay.view && len(c.Args) == 1 {
		if _, local := l.lookup(name); !local {
			v := l.intArg(c, 0)
			d := l.newReg()
			l.regTy[d] = vStructOf(name)
			l.emit(Instr{Op: OpAdd, Dst: d, A: v, B: l.constant(0)})
			return d
		}
	}

	if s, isUser := l.sigs[name]; isUser {
		if len(c.Args) != len(s.params) {
			l.errorAt(c, "%s takes %d argument(s), got %d",
				name, len(s.params), len(c.Args))
			return l.junk()
		}
		args := make([]Reg, len(c.Args))
		for i, a := range c.Args {
			v := l.rvalueAs(a, s.params[i])
			if s.params[i].k == kFloat && l.regTy[v].k == kInt {
				v = l.toFloat(v)
			}
			args[i] = v
		}
		if len(args) > l.fn.MaxCallArgs {
			l.fn.MaxCallArgs = len(args)
		}
		d := l.newReg()
		l.regTy[d] = s.ret
		l.emit(Instr{Op: OpCall, Dst: d, A: NoReg, B: NoReg, Args: args,
			ArgTypes: s.params, RetType: s.ret, Sym: name, Comment: name + "()"})
		return d
	}

	return l.builtin(c, name)
}

// ccall emits a call to a foreign symbol - libc, or anything else the
// linker can find - rather than to a Veyl function.
//
// This is the seam the library is ported through. Before it, OpCall
// could only name __vy_<sym>, so a builtin that wanted to reach a C
// function had to become its own Op with its own hand-written case in
// x64.go. Now the shape a call needs - place the arguments, know which
// are floats, know where the result comes back - is expressed once.
//
// The caller is responsible for the argument types being right. Nothing
// here checks them against a C prototype, because there is no C
// prototype to check against; getting one wrong is the same class of
// mistake as getting an extern declaration wrong in C.
func (l *lowerer) ccall(sym string, args []Reg, argTypes []vty, ret vty, ret32, variadic bool) Reg {
	l.mod.needsExtern(sym)
	if len(args) > l.fn.MaxCallArgs {
		l.fn.MaxCallArgs = len(args)
	}
	d := NoReg
	if ret.k != kVoid {
		d = l.newReg()
		l.regTy[d] = ret
	}
	l.emit(Instr{Op: OpCall, Dst: d, A: NoReg, B: NoReg, Args: args,
		ArgTypes: argTypes, RetType: ret, Sym: sym, Extern: true,
		Ret32: ret32, Variadic: variadic, Comment: sym + "()"})
	return d
}

// callExtern lowers a call to a native function the program declared.
//
// Arguments go through the same conversion user calls do - values are
// passed as the declared parameter asks, with an int widened to float
// where the declaration says float. Two things are foreign-specific:
// a declared `from "x.dll"` pins which library the import table names
// the symbol under, and a `-> str` return is copied into a fresh Veyl
// string, because the C side may free or overwrite the buffer it
// handed back as soon as we return to it.
func (l *lowerer) callExtern(c *Call, name string, es externSig) Reg {
	n := len(c.Args)
	if n < len(es.params) || (!es.variadic && n > len(es.params)) {
		want := fmt.Sprintf("%d", len(es.params))
		if es.variadic {
			want = "at least " + want
		}
		l.errorAt(c, "%s takes %s argument(s), got %d", name, want, n)
		return l.junk()
	}
	args := make([]Reg, n)
	argTypes := make([]vty, n)
	for i := 0; i < n; i++ {
		var v Reg
		if i < len(es.params) && es.params[i].k == kFunc {
			v = l.callbackArg(c.Args[i], es.params[i], es.widen[i])
		} else if i < len(es.params) {
			v = l.rvalueAs(c.Args[i], es.params[i])
			if es.params[i].k == kFloat && l.regTy[v].k == kInt {
				v = l.toFloat(v)
			}
		} else {
			// An argument beyond the named parameters of a variadic
			// extern has no declaration to convert toward; it is passed
			// as its own type and the emitter places it by that.
			v = l.rvalue(c.Args[i])
		}
		args[i] = v
		argTypes[i] = l.regTy[v]
	}
	if es.dll != "" {
		overrideImport(name, es.dll)
	}
	d := l.ccall(name, args, argTypes, es.ret, es.ret32, es.variadic)
	if es.ret.k == kStr && d != NoReg {
		d = l.dupStr(d)
	}
	return d
}

// builtin covers the handful of library functions this backend has. The
// Go backend has 302; the gap is almost entirely things that need a
// heap and a runtime rather than things that need syntax.
func (l *lowerer) builtin(c *Call, name string) Reg {
	arity := func(n int) bool {
		if len(c.Args) != n {
			l.errorAt(c, "%s takes %d argument(s), got %d", name, n, len(c.Args))
			return false
		}
		return true
	}

	switch name {
	case "os.env.get":
		if !arity(1) {
			return l.junk()
		}
		p := l.getenv(c.Args[0])
		// getenv gives NULL for an unset variable, and the Go backend
		// gives "", so the null becomes the empty string here rather than
		// a pointer nothing downstream would survive dereferencing.
		out := l.temp(vStr)
		l.emit(Instr{Op: OpStore, A: l.emptyStr(), Dst: NoReg, Imm: out})
		skip := l.newLabel()
		set := l.compare(OpEq, p, l.constant(0))
		l.emit(Instr{Op: OpJumpIf, A: set, Dst: NoReg, Imm: skip})
		l.emit(Instr{Op: OpStore, A: p, Dst: NoReg, Imm: out})
		l.mark(skip)
		d := l.newReg()
		l.regTy[d] = vStr
		l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: out})
		return d

	case "os.env.has":
		if !arity(1) {
			return l.junk()
		}
		return l.compare(OpNe, l.getenv(c.Args[0]), l.constant(0))

	case "time.now":
		if !arity(0) {
			return l.junk()
		}
		// time(NULL): the null argument is a register holding zero
		// like any other, which is the whole point of the foreign-call
		// op - the shape of the call is not special-cased anywhere.
		return l.ccall("time", []Reg{l.constant(0)}, []vty{vInt}, vInt, false, false)

	case "print":
		if !arity(1) {
			return l.junk()
		}
		a := l.expr(c.Args[0])
		if isEnum(l.regTy[a]) {
			a = l.enumName(a)
		}
		// A nullable is the value or nil, unquoted, as interpolation
		// shows it; the word it is held in is a box's address.
		if t := l.regTy[a]; t.null && !t.res {
			l.emit(Instr{Op: OpPrintStr, A: l.strOfNull(c, a, t), Dst: NoReg, Comment: "print"})
			return l.void()
		}
		switch l.regTy[a].k {
		case kStruct:
			l.mod.needs("write")
			l.writeValue(c, a, l.regTy[a])
			l.writeLit("\n")
		case kList:
			l.printList(c, a, l.regTy[a])
		case kMap:
			l.printMap(c, a, l.regTy[a])
		case kFloat:
			l.mod.needs("floattostr")
			l.emit(Instr{Op: OpPrintFloat, A: a, Dst: NoReg, Comment: "print"})
		case kBool:
			l.emit(Instr{Op: OpPrintBool, A: a, Dst: NoReg, Comment: "print"})
		case kStr:
			l.emit(Instr{Op: OpPrintStr, A: a, Dst: NoReg, Comment: "print"})
			if l.gcOff && freshStr(c.Args[0]) {
				l.freeObject(a) // a temporary; see gcoff.go
			}
		case kBytes:
			l.mod.needs("write")
			l.renderBytes(a)
			l.writeLit("\n")
		default:
			l.emit(Instr{Op: OpPrintInt, A: a, Dst: NoReg, Comment: "print"})
		}
		return l.void()

	case "write":
		if !arity(1) {
			return l.junk()
		}
		a := l.expr(c.Args[0])
		if isEnum(l.regTy[a]) {
			a = l.enumName(a)
		}
		if t := l.regTy[a]; t.null && !t.res {
			l.emit(Instr{Op: OpWriteStr, A: l.strOfNull(c, a, t), Dst: NoReg, Comment: "write"})
			return l.void()
		}
		switch l.regTy[a].k {
		case kStr:
			l.emit(Instr{Op: OpWriteStr, A: a, Dst: NoReg, Comment: "write"})
			if l.gcOff && freshStr(c.Args[0]) {
				l.freeObject(a)
			}
		case kFloat:
			l.mod.needs("floattostr")
			l.emit(Instr{Op: OpWriteFloat, A: a, Dst: NoReg, Comment: "write"})
		case kBool:
			l.mod.needs("booltostr")
			d := l.newReg()
			l.regTy[d] = vStr
			l.emit(Instr{Op: OpBoolToStr, Dst: d, A: a, B: NoReg})
			l.emit(Instr{Op: OpWriteStr, A: d, Dst: NoReg})
		case kBytes:
			l.renderBytes(a)
		default:
			l.emit(Instr{Op: OpWriteInt, A: a, Dst: NoReg, Comment: "write"})
		}
		return l.void()

	case "len":
		if !arity(1) {
			return l.junk()
		}
		a := l.expr(c.Args[0])
		switch l.regTy[a].k {
		case kList:
			return l.field(a, listLenOff, vInt)
		case kMap:
			return l.field(a, mapLenOff, vInt)
		case kBytes:
			return l.bytesLen(a)
		case kStr:
			l.mod.needs("strlen")
			d := l.newReg()
			l.regTy[d] = vInt
			l.emit(Instr{Op: OpStrLen, Dst: d, A: a, B: NoReg})
			return d
		}
		l.errorAt(c, "len needs a str, a list or a map")
		return l.junk()

	case "push":
		if len(c.Args) < 2 {
			l.errorAt(c, "push takes at least 2 arguments, got %d", len(c.Args))
			return l.junk()
		}
		return l.push(c)

	case "find":
		if !arity(2) {
			return l.junk()
		}
		return l.findExpr(c)

	case "has":
		if !arity(2) {
			return l.junk()
		}
		m := l.expr(c.Args[0])
		t := l.regTy[m]
		if t.k != kMap {
			l.errorAt(c, "has expects a map, got %s", t)
			return l.junk()
		}
		key := l.expr(c.Args[1])
		if l.regTy[key].k != t.key {
			l.errorAt(c, "this map is keyed by %s, but the key is %s", t.keyType(), l.regTy[key])
			return l.junk()
		}
		// One scan answers it: mapScan reports where the key is and
		// whether it was there at all, and only the second is wanted.
		idxSlot := l.temp(vInt)
		hitSlot := l.temp(vInt)
		l.mapScan(m, key, t, idxSlot, hitSlot)
		return l.compare(OpNe, l.load(hitSlot, vInt), l.constant(0))

	case "str":
		if !arity(1) {
			return l.junk()
		}
		return l.toStr(l.expr(c.Args[0]), c)

	case "abs", "min", "max":
		return l.numMath(c, name)

	case "int":
		if !arity(1) {
			return l.junk()
		}
		v := l.expr(c.Args[0])
		if l.regTy[v].k == kFloat {
			d := l.newReg()
			l.regTy[d] = vInt
			l.emit(Instr{Op: OpFloatToInt, Dst: d, A: v, B: NoReg})
			return d
		}
		return v

	case "float":
		if !arity(1) {
			return l.junk()
		}
		v := l.expr(c.Args[0])
		if l.regTy[v].k == kInt {
			return l.toFloat(v)
		}
		return v

	case "divf":
		if !arity(2) {
			return l.junk()
		}
		a, b := l.numeric(c.Args[0]), l.numeric(c.Args[1])
		d := l.newReg()
		l.regTy[d] = vFloat
		l.emit(Instr{Op: OpFDiv, Dst: d, A: a, B: b})
		return d

	case "sqrt":
		if !arity(1) {
			return l.junk()
		}
		a := l.numeric(c.Args[0])
		d := l.newReg()
		l.regTy[d] = vFloat
		l.emit(Instr{Op: OpSqrt, Dst: d, A: a, B: NoReg})
		return d

	case "mod":
		if !arity(2) {
			return l.junk()
		}
		a, b := l.numeric(c.Args[0]), l.numeric(c.Args[1])
		l.mod.needs("fmod")
		d := l.newReg()
		l.regTy[d] = vFloat
		l.emit(Instr{Op: OpFMod, Dst: d, A: a, B: b})
		return d
	}

	if r, handled := l.mathBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.fmtBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.timeBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.miscBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.randBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.hofBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.taskBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.netBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.dbBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.winBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.mediaBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.bytesBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.textBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.resultBuiltin(c, name); handled {
		return r
	}

	// Before the string library, because contains and indexOf take
	// either a list or a str and the list side settles both.
	if r, handled := l.listBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.strListBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.numStrBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.stringBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.osBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.dirBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.mapBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.moreBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.jsonBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.decodeBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.jsonPathBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.memBuiltin(c, name); handled {
		return r
	}

	if r, handled := l.threadBuiltin(c, name); handled {
		return r
	}
	if r, handled := l.rawMemBuiltin(c, name); handled {
		return r
	}

	l.errorAt(c, "%q is not on the assembly backend yet", name)
	return l.junk()
}

// numeric lowers an argument that the Go backend accepts as int or
// float (the "Numeric" parameter kind) and always hands back a float,
// promoting an int the same way an untyped literal promotes elsewhere.
// divf, sqrt and mod all specify this: divf(7, 2) is 3.5 whichever of
// its arguments were written as int literals.
func (l *lowerer) numeric(e Expr) Reg {
	v := l.expr(e)
	if l.regTy[v].k == kInt {
		return l.toFloat(v)
	}
	return v
}

// numMath covers abs, min and max. They are worth having built in
// because none of them needs a runtime call - a compare and a branch is
// the whole implementation.
//
// All three take int or float, the same as on the Go backend, where abs
// is `math.Abs` and always yields a float while min and max hand back
// the type of their first argument. Matching that exactly matters more
// than it looks: `abs(-7)` is a float on the Go backend, so a program
// dividing by it gets float division, and an int here would have made
// the two backends disagree about arithmetic rather than about a type.
func (l *lowerer) numMath(c *Call, name string) Reg {
	if name == "abs" {
		if len(c.Args) != 1 {
			l.errorAt(c, "abs takes 1 argument, got %d", len(c.Args))
			return l.junk()
		}
		a := l.numeric(c.Args[0])
		neg := l.newReg()
		l.regTy[neg] = vFloat
		l.emit(Instr{Op: OpFNeg, Dst: neg, A: a, B: NoReg})
		isNeg := l.compare(OpFLt, a, l.floatConst(0))
		return l.pick(isNeg, neg, a, vFloat)
	}

	if len(c.Args) < 2 {
		l.errorAt(c, "%s takes at least 2 arguments, got %d", name, len(c.Args))
		return l.junk()
	}

	// The first argument decides the type, which is the Go backend's
	// `sameAsFirst`. A later float in an int call is a truncation rather
	// than a promotion, so it is refused instead of silently rounded.
	args := make([]Reg, len(c.Args))
	for i, a := range c.Args {
		args[i] = l.expr(a)
	}
	float := l.regTy[args[0]].k == kFloat
	for i, v := range args {
		switch {
		case float && l.regTy[v].k == kInt:
			args[i] = l.toFloat(v)
		case !float && l.regTy[v].k == kFloat:
			l.errorAt(c.Args[i], "%s started with an int, so argument %d cannot be a float",
				name, i+1)
			return l.junk()
		}
	}

	t := vInt
	lt, gt := OpLt, OpGt
	if float {
		t = vFloat
		lt, gt = OpFLt, OpFGt
	}

	// Folded left to right, so a wider call is the two-argument case
	// repeated rather than its own shape.
	best := args[0]
	for _, v := range args[1:] {
		op := gt
		if name == "min" {
			op = lt
		}
		best = l.pick(l.compare(op, best, v), best, v, t)
	}
	return best
}

// floatConst is a float literal as a register.
func (l *lowerer) floatConst(v float64) Reg {
	d := l.newReg()
	l.regTy[d] = vFloat
	l.emit(Instr{Op: OpFConst, Dst: d, A: NoReg, B: NoReg, Imm: l.mod.internFloat(v)})
	return d
}

// pick is a branching select: the value of `cond ? whenTrue : whenFalse`.
// It goes through a slot because two branches write it, which is exactly
// the situation the naive allocator cannot express in a register.
func (l *lowerer) pick(cond, whenTrue, whenFalse Reg, t vty) Reg {
	slot := l.temp(t)
	other := l.newLabel()
	done := l.newLabel()
	l.emit(Instr{Op: OpJumpNot, A: cond, Dst: NoReg, Imm: other})
	l.emit(Instr{Op: OpStore, A: whenTrue, Dst: NoReg, Imm: slot})
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
	l.mark(other)
	l.emit(Instr{Op: OpStore, A: whenFalse, Dst: NoReg, Imm: slot})
	l.mark(done)
	d := l.newReg()
	l.regTy[d] = t
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: slot})
	return d
}

// toStr converts any tracked type to a string.
func (l *lowerer) toStr(v Reg, at Node) Reg {
	if l.regTy[v].null {
		return l.strOfNull(at, v, l.regTy[v])
	}
	if isEnum(l.regTy[v]) {
		return l.enumName(v)
	}
	switch l.regTy[v].k {
	case kStr:
		return v
	case kBool:
		l.mod.needs("booltostr")
		d := l.newReg()
		l.regTy[d] = vStr
		l.emit(Instr{Op: OpBoolToStr, Dst: d, A: v, B: NoReg})
		return d
	case kInt:
		l.mod.needs("inttostr")
		d := l.newReg()
		l.regTy[d] = vStr
		l.emit(Instr{Op: OpIntToStr, Dst: d, A: v, B: NoReg})
		return d
	case kFloat:
		l.mod.needs("floattostr")
		d := l.newReg()
		l.regTy[d] = vStr
		l.emit(Instr{Op: OpFloatToStr, Dst: d, A: v, B: NoReg})
		return d
	case kList, kMap, kStruct, kBytes:
		return l.strOf(at, v, l.regTy[v])
	}
	l.errorAt(at, "cannot convert %s to a string on the assembly backend yet", l.regTy[v])
	return l.junk()
}

// floatConsts are the builtin float constants.
var floatConsts = map[string]float64{
	"PI":  math.Pi,
	"E":   math.E,
	"INF": math.Inf(1),
	"NAN": math.NaN(),
}

// junk is a defined-but-meaningless register, returned after an error so
// that lowering can continue and report the next mistake too.
func (l *lowerer) junk() Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpConst, Dst: d, A: NoReg, B: NoReg, Imm: 0})
	return d
}

func (l *lowerer) void() Reg {
	d := l.newReg()
	l.regTy[d] = vVoid
	return d
}

// ---- expressions ----

func (l *lowerer) expr(e Expr) Reg {
	switch x := e.(type) {
	case *IntLit:
		n, err := strconv.ParseInt(strings.ReplaceAll(x.Val, "_", ""), 0, 64)
		if err != nil {
			l.errorAt(x, "integer literal out of range: %s", x.Val)
			return l.junk()
		}
		d := l.newReg()
		l.regTy[d] = vInt
		l.emit(Instr{Op: OpConst, Dst: d, A: NoReg, B: NoReg, Imm: n})
		return d

	case *FloatLit:
		v, err := strconv.ParseFloat(strings.ReplaceAll(x.Val, "_", ""), 64)
		if err != nil {
			l.errorAt(x, "float literal out of range: %s", x.Val)
			return l.junk()
		}
		d := l.newReg()
		l.regTy[d] = vFloat
		l.emit(Instr{Op: OpFConst, Dst: d, A: NoReg, B: NoReg, Imm: l.mod.internFloat(v)})
		return d

	case *BoolLit:
		n := int64(0)
		if x.Val {
			n = 1
		}
		d := l.newReg()
		l.regTy[d] = vBool
		l.emit(Instr{Op: OpConst, Dst: d, A: NoReg, B: NoReg, Imm: n})
		return d

	case *StrLit:
		d := l.newReg()
		l.regTy[d] = vStr
		l.emit(Instr{Op: OpStr, Dst: d, A: NoReg, B: NoReg,
			Imm: l.mod.intern(x.Val)})
		return d

	case *Interp:
		return l.interp(x)

	case *ListLit:
		return l.listLit(x)

	case *MapLit:
		return l.mapLit(x)

	case *Index:
		coll := l.expr(x.X)
		t := l.regTy[coll]
		switch t.k {
		case kList:
			return l.listGet(coll, l.expr(x.Idx), t.elemType())
		case kBytes:
			// Bounds-checked like a list, since reading past the end
			// would be reading somebody else's heap.
			idx := l.expr(x.Idx)
			l.checkBounds(idx, l.bytesLen(coll))
			d := l.newReg()
			l.regTy[d] = vInt
			l.emit(Instr{Op: OpLoadByte, Dst: d, A: coll, B: idx})
			return d
		case kMap:
			key := l.expr(x.Idx)
			if l.regTy[key].k != t.key {
				l.errorAt(x, "this map is keyed by %s, but the index is %s", t.keyType(), l.regTy[key])
				return l.junk()
			}
			return l.mapGet(x, coll, key, t)
		}
		if isPtr(t) {
			return l.ptrIndexRead(x, coll)
		}
		l.errorAt(x, "only a list or a map can be indexed on this backend so far")
		return l.junk()

	case *Ident:
		// A captured name is not in any scope here: it lives in the
		// environment this lifted function was called with.
		if d, ok := l.capturedRead(x); ok {
			return d
		}
		slot, ok := l.lookup(x.Name)
		if !ok {
			// A declared function used as a value. It becomes a closure
			// with nothing captured, pointing at the function's own
			// code, which is why a plain function needs no wrapper.
			if d, t, isFn := l.namedFuncValue(x.Name); isFn {
				l.regTy[d] = t
				return d
			}
			if g, isGlobal := l.globals[x.Name]; isGlobal {
				d := l.readGlobal(g)
				if x.Narrowed && l.regTy[d].null {
					return l.nullValue(d, l.regTy[d])
				}
				return d
			}
			// The float constants are not variables, so they are not in
			// any scope; they lower straight to a pool entry.
			if v, isConst := floatConsts[x.Name]; isConst {
				d := l.newReg()
				l.regTy[d] = vFloat
				l.emit(Instr{Op: OpFConst, Dst: d, A: NoReg, B: NoReg,
					Imm: l.mod.internFloat(v), Comment: x.Name})
				return d
			}
			l.undefined(x, x.Name)
			return l.junk()
		}
		d := l.loadLocal(slot)
		// The checker marks a use it has proved non-nil, and only those,
		// so unboxing here needs no check of its own.
		if x.Narrowed && l.regTy[d].null {
			return l.nullValue(d, l.regTy[d])
		}
		return d

	case *FuncLit:
		return l.funcLit(x)

	case *Call:
		return l.call(x)

	case *Unary:
		switch x.Op {
		case STAR:
			return l.derefRead(x)
		case AMP:
			return l.addrOf(x)
		}
		a := l.expr(x.X)
		d := l.newReg()
		switch x.Op {
		case MINUS:
			if l.regTy[a].k == kFloat {
				l.regTy[d] = vFloat
				l.emit(Instr{Op: OpFNeg, Dst: d, A: a, B: NoReg})
			} else {
				l.regTy[d] = vInt
				l.emit(Instr{Op: OpNeg, Dst: d, A: a, B: NoReg})
			}
		case BANG:
			l.regTy[d] = vBool
			l.emit(Instr{Op: OpNot, Dst: d, A: a, B: NoReg})
		case TILDE:
			l.regTy[d] = vInt
			l.emit(Instr{Op: OpBNot, Dst: d, A: a, B: NoReg})
		default:
			l.errorAt(x, "the assembly backend does not handle unary %s yet", x.Op)
		}
		return d

	case *Binary:
		return l.binary(x)

	case *Cast:
		want, ok := vtyOf(x.T)
		if !ok {
			l.errorAt(x, "cannot convert to %s", x.Type)
			return l.junk()
		}
		return l.retype(l.expr(x.X), want)

	case *Try:
		return l.tryExpr(x)

	case *NilLit:
		d := l.constant(0)
		l.regTy[d] = vNil
		return d

	case *Widen:
		return l.widen(x)

	case *StructLit:
		return l.structLit(x)

	case *Field:
		if x.Lit != nil {
			return l.expr(x.Lit) // a data enum's variant; see dataenums.go
		}
		return l.fieldRead(x)

	default:
		l.errorAt(e.(Node), "the assembly backend does not handle this expression yet")
		return l.junk()
	}
}

func (l *lowerer) binary(x *Binary) Reg {
	// && and || short-circuit, so they cannot be a plain two-operand
	// instruction: the right side must not run once the left has
	// decided the answer.
	if x.Op == AND || x.Op == OR {
		return l.shortCircuit(x)
	}

	a := l.expr(x.L)
	b := l.expr(x.R)
	at, bt := l.regTy[a], l.regTy[b]

	// A nullable compares against nil and against nothing else, which is
	// the only thing the checker lets through without narrowing first.
	// It is one word, so the test is against zero.
	if at.null || bt.null {
		if x.Op != EQ && x.Op != NEQ {
			l.errorAt(x, "a %s can only be compared with nil until it is narrowed", at)
			return l.junk()
		}

		var same Reg
		switch {
		case at.eq(vNil) || bt.eq(vNil):
			// One side is the literal nil, so this is the presence test
			// and there is nothing to look inside.
			v := a
			if at.eq(vNil) {
				v = b
			}
			same = l.isNil(v)
		default:
			// Two nullables compare as nil-ness first and contents
			// after, which is what == on the values inside them means.
			eq, ok := l.deepEqual(x, a, b, at)
			if !ok {
				return l.junk()
			}
			same = eq
		}

		if x.Op == NEQ {
			d := l.newReg()
			l.regTy[d] = vBool
			l.emit(Instr{Op: OpNot, Dst: d, A: same, B: NoReg})
			return d
		}
		return same
	}

	// Pointer arithmetic counts in elements. Comparisons need nothing
	// special: a pointer is an int underneath.
	if (isPtr(at) || isPtr(bt)) && (x.Op == PLUS || x.Op == MINUS) {
		return l.ptrArith(x, a, b)
	}

	// A container or a struct compares by contents, which is what == on
	// one means everywhere else in the language and what its printed
	// form suggests. The comparison is generated from the type, so a
	// nested one recurses in the lowerer and the depth is whatever the
	// type says - there is no runtime walk and no type tag to read.
	if at.k == kList || at.k == kMap || at.k == kStruct || at.k == kBytes {
		if x.Op != EQ && x.Op != NEQ {
			l.errorAt(x, "%s is not defined on %s", x.Op, at)
			return l.junk()
		}
		if !at.eq(bt) {
			l.errorAt(x, "cannot compare %s with %s", at, bt)
			return l.junk()
		}
		same, ok := l.deepEqual(x, a, b, at)
		if !ok {
			return l.junk()
		}
		if x.Op == NEQ {
			d := l.newReg()
			l.regTy[d] = vBool
			l.emit(Instr{Op: OpNot, Dst: d, A: same, B: NoReg})
			return d
		}
		return same
	}

	// Strings overload + and the equality tests, and nothing else.
	if at.k == kStr || bt.k == kStr {
		switch x.Op {
		case PLUS:
			if at.k != kStr || bt.k != kStr {
				l.errorAt(x, "cannot add %s and %s", at, bt)
				return l.junk()
			}
			l.mod.needs("concat")
			d := l.newReg()
			l.regTy[d] = vStr
			l.emit(Instr{Op: OpConcat, Dst: d, A: a, B: b})
			if l.gcOff {
				l.freeTemps(x.L, a, x.R, b)
			}
			return d
		case EQ, NEQ:
			l.mod.needs("streq")
			d := l.newReg()
			l.regTy[d] = vBool
			l.emit(Instr{Op: OpStrEq, Dst: d, A: a, B: b})
			if x.Op == NEQ {
				n := l.newReg()
				l.regTy[n] = vBool
				l.emit(Instr{Op: OpNot, Dst: n, A: d, B: NoReg})
				return n
			}
			return d
		case LT, GT, LTE, GTE:
			// Go orders strings byte by byte, unsigned, which is what
			// strcmp does; only the sign of its answer is portable.
			if at.k != kStr || bt.k != kStr {
				l.errorAt(x, "cannot compare %s and %s", at, bt)
				return l.junk()
			}
			cmp := l.ccall("strcmp", []Reg{a, b}, []vty{vStr, vStr}, vInt, true, false)
			op := map[Kind]Op{LT: OpLt, GT: OpGt, LTE: OpLe, GTE: OpGe}[x.Op]
			return l.compare(op, cmp, l.constant(0))
		default:
			l.errorAt(x, "%s is not defined on strings", x.Op)
			return l.junk()
		}
	}

	// Numbers. An untyped int literal on one side of a float operand was
	// already accepted by the checker - Go's own untyped-constant rule,
	// which Veyl copies for exactly this case - so by the time lowering
	// sees a kind mismatch here it can only be that, and can safely
	// promote rather than error a second time.
	if at.k == kFloat || bt.k == kFloat {
		if at.k == kInt {
			a = l.toFloat(a)
		}
		if bt.k == kInt {
			b = l.toFloat(b)
		}
		if l.regTy[a].k != kFloat || l.regTy[b].k != kFloat {
			l.errorAt(x, "'%s' needs numbers, got %s and %s", OpText(x.Op), at, bt)
			return l.junk()
		}

		var fop Op
		switch x.Op {
		case PLUS:
			fop = OpFAdd
		case MINUS:
			fop = OpFSub
		case STAR:
			fop = OpFMul
		case SLASH:
			fop = OpFDiv
		case EQ:
			fop = OpFEq
		case NEQ:
			fop = OpFNe
		case LT:
			fop = OpFLt
		case LTE:
			fop = OpFLe
		case GT:
			fop = OpFGt
		case GTE:
			fop = OpFGe
		case PERCENT:
			l.errorAt(x, "'%%' needs two ints - use mod(...) for floats")
			return l.junk()
		default:
			l.errorAt(x, "'%s' is not defined on floats", OpText(x.Op))
			return l.junk()
		}
		d := l.newReg()
		switch fop {
		case OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe:
			l.regTy[d] = vBool
		default:
			l.regTy[d] = vFloat
		}
		l.emit(Instr{Op: fop, Dst: d, A: a, B: b})
		return d
	}

	var op Op
	switch x.Op {
	case PLUS:
		op = OpAdd
	case MINUS:
		op = OpSub
	case STAR:
		op = OpMul
	case SLASH:
		op = OpDiv
	case PERCENT:
		op = OpMod
	case AMP:
		op = OpBAnd
	case PIPE:
		op = OpBOr
	case CARET:
		op = OpBXor
	case SHL:
		op = OpShl
	case SHR:
		op = OpShr
	case EQ:
		op = OpEq
	case NEQ:
		op = OpNe
	case LT:
		op = OpLt
	case LTE:
		op = OpLe
	case GT:
		op = OpGt
	case GTE:
		op = OpGe
	default:
		l.errorAt(x, "the assembly backend does not handle %s yet", x.Op)
		return l.junk()
	}

	d := l.newReg()
	switch op {
	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
		l.regTy[d] = vBool
	default:
		l.regTy[d] = vInt
	}
	l.emit(Instr{Op: op, Dst: d, A: a, B: b})
	return d
}

// shortCircuit lowers && and ||.
//
// The result is built in a stack slot rather than a virtual register,
// because two different branches write it and the naive allocator gives
// every virtual register exactly one definition site. When a real
// allocator arrives this becomes a phi, which is the point at which SSA
// starts paying for itself.
func (l *lowerer) shortCircuit(x *Binary) Reg {
	slot := l.temp(vBool)
	done := l.newLabel()

	left := l.expr(x.L)
	l.emit(Instr{Op: OpStore, A: left, Dst: NoReg, Imm: slot})

	if x.Op == AND {
		l.emit(Instr{Op: OpJumpNot, A: left, Dst: NoReg, Imm: done, Comment: "&&"})
	} else {
		l.emit(Instr{Op: OpJumpIf, A: left, Dst: NoReg, Imm: done, Comment: "||"})
	}

	right := l.expr(x.R)
	l.emit(Instr{Op: OpStore, A: right, Dst: NoReg, Imm: slot})
	l.mark(done)

	d := l.newReg()
	l.regTy[d] = vBool
	l.emit(Instr{Op: OpLoad, Dst: d, A: NoReg, B: NoReg, Imm: slot})
	return d
}

// interp lowers "a{x}b" by converting each hole to a string and
// concatenating left to right.
//
// That allocates once per join, so a long interpolation allocates
// several times and frees none of it. Correct, wasteful, and honest:
// this backend has no collector, which is the single largest thing
// standing between it and the Go backend.
func (l *lowerer) interp(x *Interp) Reg {
	var acc Reg
	first := true
	// Under gc off, which pieces are strings made here and nowhere
	// else, and so freed once they are joined: see freeTemps.
	accFresh := false

	add := func(r Reg, fresh bool) {
		if first {
			acc = r
			accFresh = fresh
			first = false
			return
		}
		l.mod.needs("concat")
		d := l.newReg()
		l.regTy[d] = vStr
		l.emit(Instr{Op: OpConcat, Dst: d, A: acc, B: r})
		if l.gcOff {
			if accFresh {
				l.freeObject(acc)
			}
			if fresh {
				l.freeObject(r)
			}
		}
		acc = d
		accFresh = true
	}

	for _, part := range x.Parts {
		if part.Lit != "" {
			d := l.newReg()
			l.regTy[d] = vStr
			l.emit(Instr{Op: OpStr, Dst: d, A: NoReg, B: NoReg,
				Imm: l.mod.intern(part.Lit)})
			add(d, false)
		}
		if part.X != nil {
			v := l.expr(part.X)
			t := l.regTy[v]
			fresh := freshStr(part.X) || (t.k == kInt && !isEnum(t) && !t.null && !t.res)
			add(l.toStr(v, x), fresh)
		}
	}

	if first {
		d := l.newReg()
		l.regTy[d] = vStr
		l.emit(Instr{Op: OpStr, Dst: d, A: NoReg, B: NoReg, Imm: l.mod.intern("")})
		return d
	}
	return acc
}

// ---- printing, for `veyl ir` ----

func (m *Module) String() string {
	var b strings.Builder
	for i, s := range m.Strings {
		fmt.Fprintf(&b, "str%d = %q\n", i, s)
	}
	if len(m.Strings) > 0 {
		b.WriteString("\n")
	}
	for _, f := range m.Funcs {
		b.WriteString(f.String())
		b.WriteString("\n")
	}
	return b.String()
}

func (f *Func) String() string {
	out := fmt.Sprintf("func %s/%d -> %s:\n", f.Name, f.NParams, f.Ret)
	for _, in := range f.Code {
		line := "    "
		if in.Dst != NoReg && in.Op != OpStore {
			line += fmt.Sprintf("v%d = ", in.Dst)
		}
		line += in.Op.String()
		switch in.Op {
		case OpConst:
			line += fmt.Sprintf(" %d", in.Imm)
		case OpStr:
			line += fmt.Sprintf(" str%d", in.Imm)
		case OpLoad:
			line += fmt.Sprintf(" slot%d", in.Imm)
		case OpStore:
			line += fmt.Sprintf(" slot%d, v%d", in.Imm, in.A)
		case OpParam:
			line += fmt.Sprintf(" %d", in.Imm)
		case OpNeg, OpNot, OpBNot, OpPrintInt, OpPrintBool, OpPrintStr,
			OpStrLen, OpIntToStr, OpBoolToStr:
			line += fmt.Sprintf(" v%d", in.A)
		case OpLabel:
			line = fmt.Sprintf("  L%d:", in.Imm)
		case OpJump:
			line += fmt.Sprintf(" L%d", in.Imm)
		case OpJumpIf, OpJumpNot:
			line += fmt.Sprintf(" v%d, L%d", in.A, in.Imm)
		case OpRet:
			if in.A != NoReg {
				line += fmt.Sprintf(" v%d", in.A)
			}
		case OpCall:
			parts := make([]string, len(in.Args))
			for i, a := range in.Args {
				parts[i] = fmt.Sprintf("v%d", a)
			}
			sym := in.Sym
			if in.Extern {
				sym = "c:" + sym
			}
			line += fmt.Sprintf(" %s(%s)", sym, strings.Join(parts, ", "))
		default:
			line += fmt.Sprintf(" v%d, v%d", in.A, in.B)
		}
		if in.Comment != "" {
			line += "    ; " + in.Comment
		}
		out += line + "\n"
	}
	return out
}

// push appends to a list.
//
// A list is a header on the heap and growing it replaces the element
// block inside that header, never the header itself, so pushing through
// a variable, a field or a list element mutates the list everyone else
// is holding and there is nothing to write back.
//
// A *map* value is the exception, and only when the key is absent.
// Reading a missing key yields a zero value that is not in the map, so
// pushing into it would append to something nobody can reach again -
// silently, since the push itself works. The Go backend copies out,
// mutates and writes back; this does the same by storing the list under
// the key afterwards. Storing it when the key was already there writes
// the same pointer over itself and costs one scan.
//
// The map and the key are each lowered once, so push(g[next()], v) does
// not advance next() twice - the same guarantee the Go backend's
// mutate() makes.
func (l *lowerer) push(c *Call) Reg {
	idx, intoIndex := c.Args[0].(*Index)
	if intoIndex {
		if m := l.expr(idx.X); l.regTy[m].k == kMap {
			t := l.regTy[m]
			key := l.expr(idx.Idx)
			if l.regTy[key].k != t.key {
				l.errorAt(c, "this map is keyed by %s, but the index is %s", t.keyType(), l.regTy[key])
				return l.junk()
			}
			list := l.mapGet(c, m, key, t)
			if !l.pushAll(c, list) {
				return l.junk()
			}
			l.mapSet(m, key, list, t)
			return l.void()
		}
	}

	list := l.expr(c.Args[0])
	if !l.pushAll(c, list) {
		return l.junk()
	}
	return l.void()
}

// pushAll appends every argument after the first, left to right.
func (l *lowerer) pushAll(c *Call, list Reg) bool {
	for _, a := range c.Args[1:] {
		if !l.pushInto(c, list, a) {
			return false
		}
	}
	return true
}

// pushInto is the part that is the same however the list was reached.
func (l *lowerer) pushInto(c *Call, list Reg, arg Expr) bool {
	if l.regTy[list].k != kList {
		l.errorAt(c, "push needs a list")
		return false
	}
	v := l.rvalueAs(arg, l.regTy[list].elemType())
	if !l.regTy[v].eq(l.regTy[list].elemType()) {
		l.errorAt(c, "cannot push %s into %s", l.regTy[v], l.regTy[list])
		return false
	}
	l.listPush(list, v)
	return true
}

// listLit builds a list from its elements. The element type comes from
// the first element, which is what the Go backend's inference does for
// a literal with no annotation to guide it.
func (l *lowerer) listLit(x *ListLit) Reg {
	if len(x.Elems) == 0 {
		if l.hint.k != kList {
			l.errorAt(x, "an empty list needs an annotation saying what it holds, as in let xs: []int = []")
			return l.junk()
		}
		list := l.newList(l.hint, 0)
		l.regTy[list] = l.hint
		return list
	}

	// An annotation names the element type outright, which is the only
	// way `[1, nil, 3]` can be a []?int - nil has no type of its own and
	// the first element is a plain int.
	//
	// The hint is narrowed to the element before the elements are
	// lowered, or a nested literal in a [][]int would be handed the
	// outer type and try to build a list of lists inside each element.
	if l.hint.k == kList {
		elem := l.hint.elemType()
		vals := make([]Reg, len(x.Elems))
		for i, e := range x.Elems {
			v := l.rvalueAs(e, elem)
			fitted, ok := l.coerce(v, elem)
			if !ok {
				l.errorAt(x.Elems[i], "this list holds %s, but element %d is %s",
					elem, i, l.regTy[v])
				return l.junk()
			}
			vals[i] = fitted
		}
		t := vListOf(elem)
		list := l.newList(t, int64(len(vals)))
		for _, v := range vals {
			l.listPush(list, v)
		}
		l.regTy[list] = t
		return list
	}

	vals := make([]Reg, len(x.Elems))
	for i, e := range x.Elems {
		vals[i] = l.rvalue(e)
	}

	elem := l.regTy[vals[0]]

	// A literal mixing ints and floats is a list of floats, which is
	// what the checker decides and what the Go backend emits for
	// `[1, 2.5, 3]`. The ints are widened here rather than being
	// rejected as a mismatch.
	mixed := false
	for _, v := range vals {
		if l.regTy[v].k == kFloat {
			mixed = true
		}
	}
	if mixed && elem.k == kInt {
		elem = vFloat
	}
	if mixed {
		for i, v := range vals {
			if l.regTy[v].k == kInt {
				vals[i] = l.toFloat(v)
			}
		}
	}

	// res is a flag beside the kind rather than a kind of its own, so a
	// list of int! would otherwise pass the kind check below and be
	// built as a list of int, storing box pointers and reading them back
	// as numbers. It has to be refused explicitly.
	if elem.res || elem.k == kVoid {
		l.errorAt(x, "a list of %s is not on the assembly backend yet", elem)
		return l.junk()
	}
	for i, v := range vals {
		if l.regTy[v].k != elem.k {
			l.errorAt(x.Elems[i], "this list holds %s, but element %d is %s",
				elem, i, l.regTy[v])
			return l.junk()
		}
	}

	t := vListOf(elem)
	list := l.newList(t, int64(len(vals)))
	for _, v := range vals {
		l.listPush(list, v)
	}
	l.regTy[list] = t
	return list
}

// getenv lowers one environment lookup to the raw pointer, NULL and all.
func (l *lowerer) getenv(arg Expr) Reg {
	name := l.expr(arg)
	return l.ccall("getenv", []Reg{name}, []vty{vStr}, vStr, false, false)
}

// mapLit builds a map from its entries. The key and value types come
// from the first entry, or from the annotation when there is no first
// entry - the same inference the Go backend does, and the same reason
// an empty literal on its own cannot be typed.
//
// Entries are inserted through mapSet rather than written straight into
// the blocks, so a literal that lists its keys out of order still ends
// up sorted, and a literal that repeats a key keeps the last value.
// Both match the Go backend.
func (l *lowerer) mapLit(x *MapLit) Reg {
	if len(x.Keys) == 0 {
		if l.hint.k != kMap {
			l.errorAt(x, "cannot tell what kind of map this is - annotate it, as in: let m: {str: int} = {}")
			return l.junk()
		}
		return l.newMap(l.hint, 0)
	}

	keys := make([]Reg, len(x.Keys))
	vals := make([]Reg, len(x.Vals))
	for i := range x.Keys {
		keys[i] = l.expr(x.Keys[i])
		vals[i] = l.rvalue(x.Vals[i])
	}

	kk := l.regTy[keys[0]]
	vk := l.regTy[vals[0]]
	if kk.k != kInt && kk.k != kStr {
		l.errorAt(x, "a map keyed by %s is not on the assembly backend yet", kk)
		return l.junk()
	}
	if vk.res || vk.k == kVoid {
		l.errorAt(x, "a map of %s is not on the assembly backend yet", vk)
		return l.junk()
	}
	if kk.res {
		l.errorAt(x, "a map keyed by %s is not on the assembly backend yet", kk)
		return l.junk()
	}
	for i := range keys {
		if l.regTy[keys[i]].k != kk.k {
			l.errorAt(x.Keys[i], "this map is keyed by %s, but key %d is %s", kk, i, l.regTy[keys[i]])
			return l.junk()
		}
		if l.regTy[vals[i]].k != vk.k {
			l.errorAt(x.Vals[i], "this map holds %s, but value %d is %s", vk, i, l.regTy[vals[i]])
			return l.junk()
		}
	}

	t := vMapOf(kk.k, vk)
	m := l.newMap(t, int64(len(keys)))
	for i := range keys {
		l.mapSet(m, keys[i], vals[i], t)
	}
	l.regTy[m] = t
	return m
}

// match lowers a multi-way branch into a chain of comparisons.
//
// The subject is evaluated once into a slot and compared against each
// arm's values in turn. Arms do not fall through, so every one ends with
// a jump past the rest. A jump table would be faster for a dense set of
// integers and is the obvious later optimisation; a chain is correct for
// every case including strings, which is what matters now.
func (l *lowerer) match(st *MatchStmt) {
	subject := l.expr(st.Subject)
	t := l.regTy[subject]
	if t.k == kList {
		l.errorAt(st, "a list cannot be matched on, because it has no single value to compare")
		return
	}

	slot := l.temp(t)
	l.emit(Instr{Op: OpStore, A: subject, Dst: NoReg, Imm: slot, Comment: "match"})

	done := l.newLabel()
	bodies := make([]int64, len(st.Cases))
	for i := range st.Cases {
		bodies[i] = l.newLabel()
	}

	for i, arm := range st.Cases {
		for _, want := range arm.Values {
			held := l.newReg()
			l.regTy[held] = t
			l.emit(Instr{Op: OpLoad, Dst: held, A: NoReg, B: NoReg, Imm: slot})
			v := l.expr(want)
			if l.regTy[v].k != t.k {
				l.errorAt(want, "this arm compares %s against a %s subject",
					l.regTy[v], t)
				continue
			}
			hit := l.newReg()
			l.regTy[hit] = vBool
			if t.k == kStr {
				l.mod.needs("streq")
				l.emit(Instr{Op: OpStrEq, Dst: hit, A: held, B: v})
			} else {
				l.emit(Instr{Op: OpEq, Dst: hit, A: held, B: v})
			}
			l.emit(Instr{Op: OpJumpIf, A: hit, Dst: NoReg, Imm: bodies[i]})
		}
	}

	// Nothing matched. The else arm runs here, or control falls past.
	if st.Else != nil {
		l.stmt(st.Else)
	}
	l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})

	for i, arm := range st.Cases {
		l.mark(bodies[i])
		l.stmt(arm.Body)
		l.emit(Instr{Op: OpJump, A: NoReg, Dst: NoReg, Imm: done})
	}

	l.mark(done)
}

// globalAddr is the address of a global's word in static storage.
func (l *lowerer) globalAddr(slot int64) Reg {
	d := l.newReg()
	l.regTy[d] = vInt
	l.emit(Instr{Op: OpGlobalAddr, Dst: d, A: NoReg, B: NoReg, Imm: slot})
	return d
}

// globalInit computes one global's value and stores it.
func (l *lowerer) globalInit(g *LetStmt) {
	slot, ok := l.globals[g.Name]
	if !ok {
		return
	}
	l.initGlobal = true
	defer func() { l.initGlobal = false }()

	want := l.globalTy[slot]
	val := l.rvalueAs(g.Value, want)
	fitted, good := l.coerce(val, want)
	if !good {
		l.errorAt(g, "%s was declared %s but the value is %s",
			g.Name, want, l.regTy[val])
		return
	}

	l.emit(Instr{Op: OpStoreMem, A: l.globalAddr(slot), B: fitted, Imm: 0,
		Comment: "global " + g.Name})
}

// assignGlobal lowers an assignment to a top-level var. The checker has
// already refused one to a const.
func (l *lowerer) assignGlobal(st *AssignStmt, slot int64) {
	target := l.globalTy[slot]
	v := l.rvalueAs(st.Value, target)
	if st.Op == ASSIGN && target.k == kFloat && l.regTy[v].k == kInt {
		v = l.toFloat(v)
	}
	if st.Op != ASSIGN {
		var applied bool
		v, applied = l.compound(st, st.Op, target, l.readGlobal(slot), v)
		if !applied {
			return
		}
	}
	fitted, good := l.coerce(v, target)
	if !good {
		l.errorAt(st, "cannot assign %s to a global of type %s", l.regTy[v], target)
		return
	}
	l.emit(Instr{Op: OpStoreMem, A: l.globalAddr(slot), B: fitted, Imm: 0, Comment: "global ="})
}

// readGlobal loads a global's value.
func (l *lowerer) readGlobal(slot int64) Reg {
	d := l.newReg()
	l.regTy[d] = l.globalTy[slot]
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: l.globalAddr(slot), B: NoReg, Imm: 0})
	return d
}

// ---- IR helper functions ----

// helperFunc emits a function written directly in the IR, once, and
// returns the symbol to call it by.
//
// Everything else in this compiler is inlined at its use site, which is
// fine for a loop whose depth the type decides. It is not fine for
// anything that recurses on data: the JSON parser calls itself for every
// nested value, and inlining that to a fixed depth made the compiler
// allocate three gigabytes before it gave up. A real function recurses
// at runtime, where the depth costs stack rather than code.
//
// The signature is registered before the body is lowered, so the body
// can call itself.
func (l *lowerer) helperFunc(name string, params []vty, ret vty, body func(args []Reg)) string {
	if l.helpers == nil {
		l.helpers = map[string]bool{}
	}
	if l.helpers[name] {
		return name
	}
	l.helpers[name] = true

	// Everything about the function being lowered is swapped out and
	// back, because a helper is emitted in the middle of whatever was
	// already being built.
	savedFn, savedSlots, savedRegs := l.fn, l.slotTy, l.regTy
	savedScopes, savedLoops, savedBuf := l.scopes, l.loops, l.buf
	savedDefers := l.defers
	l.defers = nil
	savedFile, savedLine, savedDisplay := l.curFile, l.curLine, l.fnDisplay
	l.curFile = "" // the compiler's own code has no line of the program's
	savedInHelper := l.inHelper
	l.inHelper = true

	l.fn = &Func{Name: name, NParams: len(params), ParamTypes: params, Ret: ret}
	l.slotTy = map[int64]vty{}
	l.regTy = map[Reg]vty{}
	l.scopes = nil
	l.loops = nil
	l.buf = -1
	l.pushScope()

	args := make([]Reg, len(params))
	for i, t := range params {
		d := l.newReg()
		l.regTy[d] = t
		l.emit(Instr{Op: OpParam, Dst: d, A: NoReg, B: NoReg, Imm: int64(i)})
		args[i] = d
	}
	body(args)
	l.popScope()
	l.seal()

	l.fn, l.slotTy, l.regTy = savedFn, savedSlots, savedRegs
	l.scopes, l.loops, l.buf = savedScopes, savedLoops, savedBuf
	l.defers = savedDefers
	l.curFile, l.curLine, l.fnDisplay = savedFile, savedLine, savedDisplay
	l.inHelper = savedInHelper
	return name
}

// callHelper calls a function made by helperFunc.
func (l *lowerer) callHelper(name string, args []Reg, argTypes []vty, ret vty) Reg {
	if len(args) > l.fn.MaxCallArgs {
		l.fn.MaxCallArgs = len(args)
	}
	d := NoReg
	if ret.k != kVoid || ret.res || ret.null {
		d = l.newReg()
		l.regTy[d] = ret
	}
	l.emit(Instr{Op: OpCall, Dst: d, A: NoReg, B: NoReg, Args: args,
		ArgTypes: argTypes, RetType: ret, Sym: name, Comment: name + "()"})
	return d
}

// undefined reports a name that is not in scope, and says why when the
// reason is one of the two a program most often trips on: a function
// reaching for a top-level let, and a const or var computed from one.
func (l *lowerer) undefined(n Node, name string) {
	switch {
	case l.mainLets[name] && l.initGlobal:
		l.errorAt(n, "%q is a let in the program body, which runs after every const and var "+
			"is computed - make it a const or a var too", name)
	case l.mainLets[name] && l.fn != nil && l.fn.Name != "main":
		l.errorAt(n, "%q belongs to the program body, so functions cannot see it - "+
			"declare it with var instead of let to share it", name)
	default:
		l.errorAt(n, "undefined variable %q", name)
	}
}
