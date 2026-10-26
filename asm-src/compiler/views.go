package main

import "strings"

// Extern structs: C layouts laid over memory at an address.
//
// A Scandiacus struct is an object this program owns, copied on assignment.
// An extern struct is the opposite: a description of bytes somewhere
// else - a Win32 out-parameter, a block from mem.alloc, a structure in
// another process read into a buffer - and a value of one is nothing
// but the address of its first byte. Reading a field is a load of the
// field's own width at address + offset; writing one is a store; and
// assigning the value copies the address, never the bytes, because
// two names for the same memory is what a view is for.
//
// The offsets come from the checker, which lays the fields out the way
// a C compiler would; see layout in frontend/check.go.

// cKinds maps a C field type to the width OpPeek and OpPoke move.
var cKinds = map[string]int64{
	"i8": memI8, "u8": memU8, "i16": memI16, "u16": memU16,
	"i32": memI32, "u32": memU32, "i64": memI64, "u64": memI64,
	"f32": memF32, "f64": memF64, "ptr": memI64, "bool": memU8,
}

// unsignedKind is the unsigned width of the same size, for the unit a
// bit field lives in.
var unsignedKind = map[int64]int64{
	memI8: memU8, memU8: memU8, memI16: memU16, memU16: memU16,
	memI32: memU32, memU32: memU32, memI64: memI64,
}

// viewLayout turns a checked extern struct declaration into the layout
// the rest of the lowerer uses. A nested extern struct has kind 0: its
// field reads as a view at the field's address.
func viewLayout(sd *StructDecl) *structLayout {
	lay := &structLayout{name: sd.Name, view: true, bytes: int64(sd.Size)}
	for _, f := range sd.Fields {
		sf := structField{name: f.Name, off: int64(f.Offset), kind: cKinds[f.Type], array: f.Len > 0,
			bits: int64(f.Bits), pos: int64(f.BitPos)}
		if sf.bits > 0 {
			// The unit is read and written whole and unsigned; the
			// field's sign is put back when it is read.
			sf.signed = f.Type[0] == 'i'
			sf.kind = unsignedKind[sf.kind]
		}
		if strings.HasPrefix(f.Type, "*") {
			sf.kind = memI64
		}
		switch {
		case sf.array || sf.kind == memI64 && strings.HasPrefix(f.Type, "*"):
			// An array reads as a pointer to its first element, and a
			// pointer field as the pointer it holds.
			sf.t = vPtr(f.T.String())
		case sf.kind == 0:
			sf.t = vStructOf(f.Type)
		default:
			sf.t = cScalarVty(f.Type)
		}
		lay.fields = append(lay.fields, sf)
	}
	return lay
}

// isView reports whether a type is an extern struct.
func (l *lowerer) isView(t vty) bool {
	if t.k != kStruct || t.res || t.null {
		return false
	}
	lay, ok := l.structs[t.name]
	return ok && lay.view
}

// viewNamed is the layout of the extern struct an expression names as a
// type, as in Player.size, or nil when the name is a variable.
func (l *lowerer) viewNamed(e Expr) *structLayout {
	id, ok := e.(*Ident)
	if !ok {
		return nil
	}
	if _, local := l.lookup(id.Name); local {
		return nil
	}
	if lay, ok := l.structs[id.Name]; ok && lay.view {
		return lay
	}
	return nil
}

// fieldAddr is where a field of the view at base lives.
func (l *lowerer) fieldAddr(base Reg, f structField) Reg {
	if f.off == 0 {
		return base
	}
	return l.arith(OpAdd, base, l.constant(f.off))
}

// viewRead reads one field.
func (l *lowerer) viewRead(base Reg, f structField) Reg {
	at := l.fieldAddr(base, f)
	if f.array || f.kind == 0 {
		// An array reads as its address and a nested struct as a view
		// of it, and both of those are just the address, retyped.
		d := l.newReg()
		l.regTy[d] = f.t
		l.emit(Instr{Op: OpAdd, Dst: d, A: at, B: l.constant(0)})
		return d
	}
	v := l.loadWidth(at, f.kind)
	if f.bits > 0 {
		v = l.bitsOf(v, f)
	}
	if f.t.k == kBool {
		return l.compare(OpNe, v, l.constant(0))
	}
	if isPtr(f.t) || fixedOf(f.t) != "" {
		l.regTy[v] = f.t
	}
	return v
}

// viewWrite stores a value into one field. A nested struct is copied
// in, bytes and all, since the field is the struct and not a pointer
// to one.
func (l *lowerer) viewWrite(n Node, base Reg, f structField, v Reg) {
	at := l.fieldAddr(base, f)
	if f.kind == 0 {
		lay, ok := l.layoutOf(n, f.t)
		if !ok {
			return
		}
		l.ccall("memmove", []Reg{at, v, l.constant(lay.bytes)},
			[]vty{vInt, vInt, vInt}, vInt, false, false)
		return
	}
	if f.t.k == kFloat && l.regTy[v].k == kInt {
		v = l.toFloat(v)
	}
	if f.bits > 0 {
		// Read, clear the field's bits, put the low bits of the value
		// there, write back: the other fields in the unit keep theirs.
		vs := l.scalarTemp(vInt)
		l.setSlot(vs, v)
		mask := int64(1)<<f.bits - 1
		if f.bits == 64 {
			mask = -1
		}
		unit := l.loadWidth(at, f.kind)
		kept := l.arith(OpBAnd, unit, l.constant(^(mask << f.pos)))
		put := l.arith(OpShl, l.arith(OpBAnd, l.load(vs, vInt), l.constant(mask)), l.constant(f.pos))
		l.storeWidth(l.fieldAddr(base, f), l.arith(OpBOr, kept, put), f.kind)
		return
	}
	l.storeWidth(at, v, f.kind)
}

// bitsOf takes a bit field out of the unit it was read in: shifted down
// and masked, then sign-extended from its top bit if its type is
// signed.
func (l *lowerer) bitsOf(unit Reg, f structField) Reg {
	if f.signed {
		// Up so its top bit is the word's, then an arithmetic shift down.
		up := l.arith(OpShl, unit, l.constant(64-f.pos-f.bits))
		return l.arith(OpShr, up, l.constant(64-f.bits))
	}
	v := l.arith(OpShrU, unit, l.constant(f.pos))
	if f.bits == 64 {
		return v
	}
	return l.arith(OpBAnd, v, l.constant(int64(1)<<f.bits-1))
}

// viewAssign lowers `p.hp = 100` and `p.hp -= 10` on a view.
func (l *lowerer) viewAssign(st *AssignStmt, base Reg, f structField) {
	if f.array {
		l.errorAt(st, "%s is an array and cannot be assigned", f.name)
		return
	}
	v := l.rvalue(st.Value)
	if st.Op != ASSIGN {
		cur := l.viewRead(base, f)
		applied := false
		v, applied = l.compound(st, st.Op, f.t, cur, v)
		if !applied {
			return
		}
	}
	l.viewWrite(st, base, f, v)
}

// viewLit lowers `Point{x: 1, y: 2}` for an extern struct: fresh zeroed
// memory of the struct's size, owned by the collector like a bytes
// value, with the given fields written into it. It is for the common
// Win32 pattern of filling in a structure and passing its address, and
// it lives as long as something in the program still refers to it.
func (l *lowerer) viewLit(x *StructLit, lay *structLayout) Reg {
	size := l.constant(lay.bytes)
	obj := l.allocObj(size, tagBytes)
	l.ccall("memset", []Reg{obj, l.constant(0), size},
		[]vty{vInt, vInt, vInt}, vInt, false, false)
	l.regTy[obj] = vStructOf(lay.name)
	for i, name := range x.Fields {
		if i >= len(x.Vals) {
			break
		}
		f, ok := lay.field(name)
		if !ok {
			l.errorAt(x, "%s has no field %q", lay.name, name)
			continue
		}
		v := l.rvalueAs(x.Vals[i], f.t)
		l.viewWrite(x, obj, f, v)
	}
	return obj
}

// writeView prints a view the way a struct prints, reading each field
// from memory. An array field prints its address.
func (l *lowerer) writeView(n Node, base Reg, lay *structLayout) {
	l.writeLit(lay.name + "{")
	for i, f := range lay.fields {
		if i > 0 {
			l.writeLit(", ")
		}
		l.writeLit(f.name + ": ")
		l.writeValue(n, l.viewRead(base, f), f.t)
	}
	l.writeLit("}")
}
