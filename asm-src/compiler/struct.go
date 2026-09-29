package main

// Structs, laid out at compile time.
//
// A struct is a fixed set of named fields, so unlike a list or a map
// there is nothing to decide at runtime: every access is a load or a
// store at an offset the compiler already knows. That is what makes
// this the cheapest of the features the backend was missing - alloc,
// loadmem and storemem already did everything needed.
//
// Layout. A struct instance is one heap object of n words:
//
//	[ptr-8]  size << 16 | nptr << 8 | tagStruct
//	[ptr+0]  the first pointer-holding field
//	   ...
//	[ptr+8k] the first field holding no pointer
//	   ...
//
// Fields are reordered so that every pointer comes first, and the
// header records how many there are. That is the whole reason for the
// reordering, and it is worth spelling out because it is the first
// place the object header earns anything.
//
// A collector has to know, for each word of an object, whether it is a
// pointer to follow or an integer that merely looks like one. The tags
// that existed before answered that for a block at a time: the bytes of
// a string are never pointers, the elements of a []str always are. A
// struct is the first thing here that is genuinely mixed, so one tag
// cannot describe it. Sorting the pointers to the front turns the
// answer back into a single number, which fits in the eight spare bits
// the header already had.
//
// The alternative was a per-instance pointer to a static descriptor,
// which costs a word on every object ever allocated. This costs
// nothing, and the reordering is invisible: fields are reached by name
// through a compile-time offset, so nothing above this file can observe
// what order they ended up in.
//
// Value semantics. On the Go backend a struct is a Go struct, so it is
// copied on assignment, on being passed, and on being returned. Here it
// is a pointer, so the copy has to be written out. copyStruct does it,
// and rvalue is where the decision is made. Getting this wrong would
// not be a crash - it would be `let b = a` quietly aliasing, which the
// differential test only catches if a program happens to mutate through
// the second name.
const (
	tagStruct = 7

	// The struct header packs one more number than the others, so it
	// shifts the size further left to make room. A collector switches on
	// the tag before reading either field, so the two encodings never
	// have to be told apart by anything else.
	structNPtrShift = 8
	structSizeShift = 16
)

type structField struct {
	name string
	t    vty
	off  int64 // bytes from the payload pointer

	// An extern struct field: its width, and whether it is an array,
	// which reads as its own address. See views.go.
	kind  int64
	array bool
}

// A structLayout is one declared struct. fields stays in declaration
// order, because that is the order the Go backend prints them in and
// this has to match; the offsets inside are what carry the reordering.
type structLayout struct {
	name   string
	fields []structField
	bytes  int64
	nptr   int64

	// view marks an extern struct: a C layout at an address, never
	// copied, never allocated as a Veyl object. See views.go.
	view bool
}

func (s *structLayout) field(name string) (structField, bool) {
	for _, f := range s.fields {
		if f.name == name {
			return f, true
		}
	}
	return structField{}, false
}

func (s *structLayout) header() int64 {
	return s.bytes<<structSizeShift | s.nptr<<structNPtrShift | tagStruct
}

// collectStructs resolves every declared struct into a layout.
//
// It runs before signatures are collected, because a function can take
// or return a struct and the signature needs its vty. Declaration order
// does not matter: nothing here looks at another layout, since a struct
// field holding a struct stores a pointer whatever that struct turns
// out to contain.
func (l *lowerer) collectStructs(p *Program) {
	l.structs = map[string]*structLayout{}

	for _, sd := range p.Structs {
		if sd.Extern {
			l.structs[sd.Name] = viewLayout(sd)
			continue
		}
		if sd.Enum != nil {
			dataEnums[sd.Name] = sd.Enum
		}
		lay := &structLayout{name: sd.Name}
		ok := true

		for _, f := range sd.Fields {
			t, good := typeOfName(f.Type)
			if !good || t.k == kVoid {
				l.errorAt(sd, "field %q of struct %s has type %q, which is not on the assembly backend yet",
					f.Name, sd.Name, f.Type)
				ok = false
				continue
			}
			if _, dup := lay.field(f.Name); dup {
				l.errorAt(sd, "struct %s declares field %q twice", sd.Name, f.Name)
				ok = false
				continue
			}
			lay.fields = append(lay.fields, structField{name: f.Name, t: t})
		}
		if !ok {
			continue
		}

		// Pointers first, then the rest, so that the header's count is
		// all a collector needs. Both passes walk the fields in
		// declaration order, so the layout is deterministic.
		var off int64
		for i := range lay.fields {
			if lay.fields[i].t.holdsPointer() {
				lay.fields[i].off = off
				off += wordSize
				lay.nptr++
			}
		}
		for i := range lay.fields {
			if !lay.fields[i].t.holdsPointer() {
				lay.fields[i].off = off
				off += wordSize
			}
		}
		lay.bytes = off

		l.structs[sd.Name] = lay
	}
}

// layoutOf looks up a struct by name, reporting the one case the
// checker cannot have caught: a struct this backend refused to lay out
// because one of its fields uses a type that is not here yet.
func (l *lowerer) layoutOf(n Node, t vty) (*structLayout, bool) {
	lay, ok := l.structs[t.name]
	if !ok {
		l.errorAt(n, "struct %s is not available here", t.name)
		return nil, false
	}
	return lay, true
}

// allocStruct allocates a zeroed instance. Every field is written, so
// nothing downstream ever reads a word the allocator left dirty.
func (l *lowerer) allocStruct(lay *structLayout) Reg {
	obj := l.allocTagged(l.constant(lay.bytes), l.constant(lay.header()))
	l.regTy[obj] = vStructOf(lay.name)
	return obj
}

// zeroOf is the value a field takes when a literal leaves it out.
//
// It has to agree with Go's zero value, which is what the Go backend
// gives such a field: 0, 0.0, false, and the empty string. A list or a
// map is nil there rather than empty, but nothing a program can ask
// tells the two apart - both have length zero and both print as empty -
// and an allocated one cannot be null-dereferenced later.
//
// A struct field holding another struct is a zeroed instance of it
// rather than a null pointer, because on the Go backend it is a value
// and there is nothing there for null to mean. depth stops a struct
// that contains itself from spinning here; the checker rejects one, so
// hitting the limit means the checker let something through.
func (l *lowerer) zeroOf(n Node, t vty, depth int) Reg {
	if t.res {
		// A field of type T! would be a null pointer with no failure and
		// no value, which nothing can read safely.
		l.errorAt(n, "a struct field of type %s is not on the assembly backend yet", t)
		return l.junk()
	}
	if t.null {
		// The zero of a ?T is nil, which is the zero word - so a struct
		// field that is nullable needs no initialiser beyond it.
		return l.nilValue(t)
	}
	switch t.k {
	case kStr:
		return l.emptyStr()
	case kFloat:
		d := l.newReg()
		l.regTy[d] = vFloat
		l.emit(Instr{Op: OpFConst, Dst: d, A: NoReg, B: NoReg, Imm: l.mod.internFloat(0)})
		return d
	case kList:
		return l.newList(t, initialCap)
	case kMap:
		return l.newMap(t, initialCap)
	case kStruct:
		if l.isView(t) {
			// A view nobody pointed anywhere: the null address.
			d := l.constant(0)
			l.regTy[d] = t
			return d
		}
		if depth > 16 {
			l.errorAt(n, "struct %s contains itself", t.name)
			return l.junk()
		}
		lay, ok := l.layoutOf(n, t)
		if !ok {
			return l.junk()
		}
		if e := dataEnums[lay.name]; e != nil {
			return l.zeroVariant(n, lay, e, depth+1)
		}
		return l.zeroStruct(n, lay, depth+1)
	}
	d := l.constant(0)
	l.regTy[d] = t
	return d
}

func (l *lowerer) zeroStruct(n Node, lay *structLayout, depth int) Reg {
	obj := l.allocStruct(lay)
	for _, f := range lay.fields {
		l.emit(Instr{Op: OpStoreMem, A: obj, B: l.zeroOf(n, f.t, depth), Imm: f.off})
	}
	return obj
}

// structLit lowers `User{name: "ada", age: 36}`.
func (l *lowerer) structLit(x *StructLit) Reg {
	lay, ok := l.structs[x.Name]
	if !ok {
		l.errorAt(x, "struct %s is not available here", x.Name)
		return l.junk()
	}
	if lay.view {
		return l.viewLit(x, lay)
	}

	obj := l.allocStruct(lay)

	// Every field is written exactly once, in memory order, whether or
	// not the literal mentioned it. Writing the given ones over a zeroed
	// object would be simpler and would allocate the zero values of
	// fields that are about to be overwritten, which for a struct field
	// means a whole wasted instance.
	given := map[string]Expr{}
	for i, name := range x.Fields {
		if i < len(x.Vals) {
			given[name] = x.Vals[i]
		}
	}
	for _, f := range lay.fields {
		var v Reg
		if e, has := given[f.name]; has {
			v = l.rvalueAs(e, f.t)
			if f.t.k == kFloat && l.regTy[v].k == kInt {
				v = l.toFloat(v)
			}
		} else if dataEnums[lay.name] != nil {
			// Another variant's field, which is only ever read after the
			// tag says it is that variant: it holds nothing at all.
			v = l.constant(0)
			l.regTy[v] = f.t
		} else {
			v = l.zeroOf(x, f.t, 0)
		}
		l.emit(Instr{Op: OpStoreMem, A: obj, B: v, Imm: f.off})
	}
	return obj
}

// fieldRead lowers `u.name`.
//
// A Field node is also how a dotted builtin path such as os.env.get is
// written, but those only ever appear as the callee of a call and are
// flattened there, so anything reaching here is a real field access.
func (l *lowerer) fieldRead(x *Field) Reg {
	if name := l.enumNamed(x.X); name != "" {
		return l.enumValue(x, name)
	}
	if lay := l.viewNamed(x.X); lay != nil {
		// Player.size, the only thing an extern struct's name has.
		d := l.constant(lay.bytes)
		return d
	}
	obj := l.expr(x.X)
	t := l.regTy[obj]
	if isPtr(t) {
		t = pointedStruct(t) // p.hp is p->hp
	}
	if t.k != kStruct || t.res {
		l.errorAt(x, "cannot read field %q of %s", x.Name, t)
		return l.junk()
	}
	lay, ok := l.layoutOf(x, t)
	if !ok {
		return l.junk()
	}
	f, has := lay.field(x.Name)
	if !has {
		l.errorAt(x, "struct %s has no field %q", lay.name, x.Name)
		return l.junk()
	}
	if lay.view {
		return l.viewRead(obj, f)
	}

	d := l.newReg()
	l.regTy[d] = f.t
	l.emit(Instr{Op: OpLoadMem, Dst: d, A: obj, B: NoReg, Imm: f.off,
		Comment: lay.name + "." + f.name})
	return d
}

// fieldAssign lowers `u.age = 36` and `u.age += 1`.
func (l *lowerer) fieldAssign(st *AssignStmt, target *Field) {
	obj := l.expr(target.X)
	t := l.regTy[obj]
	if isPtr(t) {
		t = pointedStruct(t)
	}
	if t.k != kStruct || t.res {
		l.errorAt(st, "cannot assign to field %q of %s", target.Name, t)
		return
	}
	lay, ok := l.layoutOf(st, t)
	if !ok {
		return
	}
	f, has := lay.field(target.Name)
	if !has {
		l.errorAt(st, "struct %s has no field %q", lay.name, target.Name)
		return
	}
	if lay.view {
		l.viewAssign(st, obj, f)
		return
	}

	v := l.rvalue(st.Value)
	if st.Op != ASSIGN {
		// A compound assignment reads the field, applies the operator
		// and writes it back. Reusing binaryOn keeps `+=` on a field
		// agreeing with `+` on one, including the string case.
		cur := l.newReg()
		l.regTy[cur] = f.t
		l.emit(Instr{Op: OpLoadMem, Dst: cur, A: obj, B: NoReg, Imm: f.off})
		applied := false
		v, applied = l.compound(st, st.Op, f.t, cur, v)
		if !applied {
			return
		}
	}
	if f.t.k == kFloat && l.regTy[v].k == kInt {
		v = l.toFloat(v)
	}
	l.emit(Instr{Op: OpStoreMem, A: obj, B: v, Imm: f.off,
		Comment: lay.name + "." + f.name + " ="})
}

// copyStruct duplicates an instance, field by field.
//
// A nested struct field is copied too, because it is a value on the Go
// backend and copying only the pointer would leave the two instances
// sharing it. That recursion terminates for the same reason zeroOf's
// does: a struct cannot contain itself by value.
func (l *lowerer) copyStruct(n Node, v Reg, t vty) Reg {
	lay, ok := l.layoutOf(n, t)
	if !ok || lay.view {
		// A view is an address, and copying it is the point: two
		// names for the same native memory.
		return v
	}
	if dataEnums[lay.name] != nil {
		// Nothing can change a data enum once it is built, so there is
		// nothing a copy would protect: sharing it is the same value.
		return v
	}
	dup := l.allocStruct(lay)
	for _, f := range lay.fields {
		cur := l.newReg()
		l.regTy[cur] = f.t
		l.emit(Instr{Op: OpLoadMem, Dst: cur, A: v, B: NoReg, Imm: f.off})
		if f.t.k == kStruct {
			cur = l.copyStruct(n, cur, f.t)
		}
		l.emit(Instr{Op: OpStoreMem, A: dup, B: cur, Imm: f.off})
	}
	return dup
}

// rvalue lowers an expression that is about to be bound to a name,
// passed, returned or stored, copying a struct read out of a place so
// that the binding does not alias it.
//
// Only a place needs the copy. A struct literal or the return of a call
// is already a fresh object nobody else holds, and copying it again
// would double the allocation on the common path.
func (l *lowerer) rvalue(e Expr) Reg {
	v := l.expr(e)
	t := l.regTy[v]
	if t.k == kStruct && !t.res && isPlace(e) {
		return l.copyStruct(e.(Node), v, t)
	}
	return v
}

// isPlace reports whether an expression names storage somebody else can
// still reach, rather than producing a fresh value.
func isPlace(e Expr) bool {
	switch e.(type) {
	case *Ident, *Field, *Index:
		return true
	}
	return false
}

// writeStruct renders an instance the way the Go backend does:
// User{name: "ada", age: 36, active: true}, fields in declaration
// order, strings quoted.
func (l *lowerer) writeStruct(n Node, v Reg, t vty) {
	lay, ok := l.layoutOf(n, t)
	if !ok {
		return
	}
	if lay.view {
		l.writeView(n, v, lay)
		return
	}
	// Rendering is inlined, which is fine for a type of fixed depth and
	// never ends for one that holds itself - a tree, a data enum with a
	// list of itself. That kind is rendered by a function of its own,
	// once, which calls itself for each value inside.
	if l.inlineOnce == lay.name {
		l.inlineOnce = ""
	} else if l.recursiveStruct(lay.name) {
		plain := vStructOf(lay.name)
		sym := l.helperFunc("__show_"+lay.name, []vty{plain}, vStr, func(a []Reg) {
			slot := l.temp(vStr)
			l.emit(Instr{Op: OpStore, A: l.emptyStr(), Dst: NoReg, Imm: slot})
			l.buf = slot
			l.inlineOnce = lay.name
			l.writeStruct(n, a[0], plain)
			l.emit(Instr{Op: OpRet, A: l.load(slot, vStr), Dst: NoReg})
		})
		l.emitStr(l.callHelper(sym, []Reg{v}, []vty{plain}, vStr))
		return
	}
	if e := dataEnums[lay.name]; e != nil {
		l.writeVariant(n, v, lay, e)
		return
	}
	l.writeLit(lay.name + "{")
	for i, f := range lay.fields {
		if i > 0 {
			l.writeLit(", ")
		}
		l.writeLit(f.name + ": ")

		cur := l.newReg()
		l.regTy[cur] = f.t
		l.emit(Instr{Op: OpLoadMem, Dst: cur, A: v, B: NoReg, Imm: f.off})

		l.writeValue(n, cur, f.t)
	}
	l.writeLit("}")
}

// methodCall reports whether a field access names a method, and if so
// which struct it is on.
//
// It cannot simply lower the base and look at its type: for `os.file.read`
// the base is not an expression at all, and lowering it would report an
// undefined variable before the library ever got a chance. So the
// question is asked of the *declared* methods first, and the base is only
// lowered once the answer is yes.
func (l *lowerer) methodCall(fld *Field) (recv string, isMethod bool) {
	// A dotted library name has an identifier at its root that is not a
	// variable. Anything that is not a known local cannot be a receiver.
	root, plain := fld.X.(*Ident)
	if plain {
		slot, known := l.lookup(root.Name)
		var t vty
		switch {
		case known:
			t = l.slotTy[slot]
		default:
			// A variable a closure captured is a receiver too.
			ct, captured := l.captureTy[root.Name]
			if !captured {
				return "", false
			}
			t = ct
		}
		if t.k != kStruct {
			return "", false
		}
		if _, has := l.sigs[methodName(t.name, fld.Name)]; !has {
			return "", false
		}
		return t.name, true
	}

	// A longer chain - `line.from.length()` - has a Field or an Index
	// underneath. Those are always expressions, so the type is whatever
	// the base resolves to.
	switch fld.X.(type) {
	case *Field, *Index, *Call, *StructLit:
		t, ok := l.staticTypeOf(fld.X)
		if !ok || t.k != kStruct {
			return "", false
		}
		if _, has := l.sigs[methodName(t.name, fld.Name)]; !has {
			return "", false
		}
		return t.name, true
	}
	return "", false
}

// staticTypeOf answers the type of an expression without emitting
// anything for it, for the few places that have to decide what an
// expression is before deciding whether to lower it at all.
//
// Only the shapes a method receiver can take are handled. Everything
// else reports false, which the caller reads as "not a method".
func (l *lowerer) staticTypeOf(e Expr) (vty, bool) {
	switch x := e.(type) {
	case *Ident:
		slot, ok := l.lookup(x.Name)
		if !ok {
			return vVoid, false
		}
		return l.slotTy[slot], true
	case *Field:
		base, ok := l.staticTypeOf(x.X)
		if !ok || base.k != kStruct {
			return vVoid, false
		}
		lay, ok := l.structs[base.name]
		if !ok {
			return vVoid, false
		}
		for _, f := range lay.fields {
			if f.name == x.Name {
				return f.t, true
			}
		}
		return vVoid, false
	case *Index:
		base, ok := l.staticTypeOf(x.X)
		if !ok || (base.k != kList && base.k != kMap) {
			return vVoid, false
		}
		return base.elemType(), true
	case *Call:
		// The checker has typed every call, which covers the shapes a
		// name cannot: a method's result, a.me().get().
		if x.T != nil && !x.T.IsUnknown() {
			if t, ok := vtyOf(x.T); ok {
				return t, true
			}
		}
		name, ok := DottedName(x.Callee)
		if !ok {
			return vVoid, false
		}
		s, known := l.sigs[name]
		if !known {
			return vVoid, false
		}
		return s.ret, true
	case *StructLit:
		// A literal called on directly: `(Point{...}).length()`.
		if _, ok := l.structs[x.Name]; !ok {
			return vVoid, false
		}
		return vStructOf(x.Name), true
	}
	return vVoid, false
}

// callMethod lowers `recv.name(args)` as the plain call it is, with the
// receiver passed as the first argument.
func (l *lowerer) callMethod(c *Call, fld *Field, recv string) Reg {
	name := methodName(recv, fld.Name)
	s := l.sigs[name]

	if len(c.Args)+1 != len(s.params) {
		l.errorAt(c, "%s takes %d argument(s), got %d",
			name, len(s.params)-1, len(c.Args))
		return l.junk()
	}

	// The receiver is passed by reference, not copied. The Go backend
	// declares every method on `(self *T)`, so `p.scale(0.5)` changes p
	// itself; a struct is already a pointer here, so handing over the
	// place rather than an rvalue is all that takes. Copying it - which
	// is what every other struct argument gets - would make a mutating
	// method silently work on a temporary.
	args := make([]Reg, len(s.params))
	args[0] = l.expr(fld.X)
	for i, a := range c.Args {
		v := l.rvalueAs(a, s.params[i+1])
		if s.params[i+1].k == kFloat && l.regTy[v].k == kInt {
			v = l.toFloat(v)
		}
		args[i+1] = v
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

// recursiveStruct reports whether a struct can hold another of its own
// type, through any depth of fields, lists and maps.
func (l *lowerer) recursiveStruct(name string) bool {
	seen := map[string]bool{}
	var reaches func(t vty) bool
	reaches = func(t vty) bool {
		switch t.k {
		case kStruct:
			if t.name == name {
				return true
			}
			if seen[t.name] {
				return false
			}
			seen[t.name] = true
			lay, ok := l.structs[t.name]
			if !ok || lay.view {
				return false
			}
			for _, f := range lay.fields {
				if reaches(f.t) {
					return true
				}
			}
		case kList, kMap:
			return reaches(t.elemType())
		}
		return false
	}
	lay, ok := l.structs[name]
	if !ok {
		return false
	}
	for _, f := range lay.fields {
		if reaches(f.t) {
			return true
		}
	}
	return false
}
