package main

import "strings"

// Typed pointers: *i32, *Player, **u8. See frontend/pointers.go for the
// rules; this is how they run.
//
// A pointer is an int as far as the machine goes, so it is tracked as
// one: kind kInt, with the pointer type's spelling in name. That is the
// same trick an enum uses, and it buys the same thing - every path that
// takes an int (a register, an extern argument, mem.*, a list element)
// takes a pointer unchanged, and the collector never mistakes one for
// an object it owns. What the name adds is what is at the address,
// which is all that *p, p[i] and p + n need to know.

func vPtr(spelling string) vty { return vty{k: kInt, name: spelling} }

func isPtr(t vty) bool {
	return t.k == kInt && strings.HasPrefix(t.name, "*") && !t.res && !t.null
}

// pointee describes what a pointer points at as a one-field layout at
// offset 0, which lets viewRead and viewWrite do the loading and
// storing they already do for extern struct fields: the width, the
// sign, a float, a bool, or a nested struct copied in whole. size is
// the stride for p[i] and p + n.
func (l *lowerer) pointee(n Node, t vty) (f structField, size int64, ok bool) {
	rest := strings.TrimPrefix(t.name, "*")
	f.name = "*"
	switch {
	case cKinds[rest] != 0:
		f.kind = cKinds[rest]
		size = int64(CTypeSize(rest))
		switch rest {
		case "f32", "f64":
			f.t = vFloat
		case "bool":
			f.t = vBool
		default:
			f.t = vInt
		}
	case strings.HasPrefix(rest, "*"):
		f.kind, f.t, size = memI64, vPtr(rest), 8
	default:
		lay, has := l.structs[rest]
		if !has || !lay.view {
			l.errorAt(n, "%s does not point at anything with a C layout", t)
			return f, 0, false
		}
		f.t, size = vStructOf(rest), lay.bytes
	}
	return f, size, true
}

// retype is the same value seen as another type, which is all `as`
// and taking an address ever do.
func (l *lowerer) retype(v Reg, t vty) Reg {
	d := l.newReg()
	l.regTy[d] = t
	l.emit(Instr{Op: OpAdd, Dst: d, A: v, B: l.constant(0)})
	return d
}

// ptrElemAddr is p + i*size, the address of p[i].
func (l *lowerer) ptrElemAddr(p, i Reg, size int64) Reg {
	if size != 1 {
		i = l.arith(OpMul, i, l.constant(size))
	}
	return l.arith(OpAdd, p, i)
}

// derefRead lowers `*p`.
func (l *lowerer) derefRead(x *Unary) Reg {
	p := l.expr(x.X)
	f, _, ok := l.pointee(x, l.regTy[p])
	if !ok {
		return l.junk()
	}
	return l.viewRead(p, f)
}

// ptrIndexRead lowers `p[i]`.
func (l *lowerer) ptrIndexRead(x *Index, p Reg) Reg {
	f, size, ok := l.pointee(x, l.regTy[p])
	if !ok {
		return l.junk()
	}
	return l.viewRead(l.ptrElemAddr(p, l.expr(x.Idx), size), f)
}

// addrOf lowers `&place`: the checker has already said which shapes a
// place can take, so each one here only works out where it is.
func (l *lowerer) addrOf(x *Unary) Reg {
	want, ok := vtyOf(x.T)
	if !ok {
		l.errorAt(x, "cannot take this address")
		return l.junk()
	}
	switch inner := x.X.(type) {
	case *Unary: // &*p
		return l.retype(l.expr(inner.X), want)
	case *Index: // &p[i]
		p := l.expr(inner.X)
		_, size, ok := l.pointee(x, l.regTy[p])
		if !ok {
			return l.junk()
		}
		return l.retype(l.ptrElemAddr(p, l.expr(inner.Idx), size), want)
	case *Field: // &v.f
		base, lay, ok := l.viewOf(inner)
		if !ok {
			return l.junk()
		}
		f, has := lay.field(inner.Name)
		if !has {
			l.errorAt(x, "%s has no field %q", lay.name, inner.Name)
			return l.junk()
		}
		return l.retype(l.fieldAddr(base, f), want)
	}
	l.errorAt(x, "cannot take this address")
	return l.junk()
}

// viewOf lowers the struct side of `v.f` when v is an extern struct
// or a pointer to one, answering the address and the layout.
func (l *lowerer) viewOf(x *Field) (Reg, *structLayout, bool) {
	base := l.expr(x.X)
	t := l.regTy[base]
	if isPtr(t) {
		t = pointedStruct(t)
	}
	lay, ok := l.layoutOf(x, t)
	if !ok || !lay.view {
		l.errorAt(x, "cannot take the address of %q here", x.Name)
		return base, nil, false
	}
	return base, lay, true
}

// ptrAssign lowers `*p = v`, `p[i] = v` and their compound forms. The
// address is worked out once, so `p[next()] += 1` calls next() once.
func (l *lowerer) ptrAssign(st *AssignStmt, p Reg, idx Expr) {
	f, size, ok := l.pointee(st, l.regTy[p])
	if !ok {
		return
	}
	at := p
	if idx != nil {
		at = l.ptrElemAddr(p, l.expr(idx), size)
	}
	v := l.rvalueAs(st.Value, f.t)
	if st.Op != ASSIGN {
		cur := l.viewRead(at, f)
		applied := false
		v, applied = l.compound(st, st.Op, f.t, cur, v)
		if !applied {
			return
		}
	}
	l.viewWrite(st, at, f, v)
}

// ptrArith lowers p + n, n + p, p - n and p - q, all counted in
// elements the way C counts them.
func (l *lowerer) ptrArith(x *Binary, a, b Reg) Reg {
	at, bt := l.regTy[a], l.regTy[b]
	if x.Op == PLUS && !isPtr(at) {
		a, b, at = b, a, bt
	}
	_, size, ok := l.pointee(x, at)
	if !ok {
		return l.junk()
	}
	if x.Op == MINUS && isPtr(l.regTy[b]) {
		diff := l.arith(OpSub, a, b)
		if size == 1 {
			return diff
		}
		return l.arith(OpDiv, diff, l.constant(size))
	}
	if size != 1 {
		b = l.arith(OpMul, b, l.constant(size))
	}
	op := OpAdd
	if x.Op == MINUS {
		op = OpSub
	}
	return l.retype(l.arith(op, a, b), at)
}

// ptrStep is `p += n` and `p -= n` on a pointer variable.
func (l *lowerer) ptrStep(n Node, k Kind, t vty, cur, v Reg) (Reg, bool) {
	_, size, ok := l.pointee(n, t)
	if !ok {
		return v, false
	}
	if size != 1 {
		v = l.arith(OpMul, v, l.constant(size))
	}
	op := OpAdd
	if k == MINUSEQ {
		op = OpSub
	}
	return l.retype(l.arith(op, cur, v), t), true
}

// pointedStruct is the extern struct a *Struct points at, as the view
// type its fields are read through.
func pointedStruct(t vty) vty { return vStructOf(strings.TrimPrefix(t.name, "*")) }
