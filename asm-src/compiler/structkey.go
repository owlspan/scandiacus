package main

// A struct as a map key.
//
//	struct Cell { x: int, y: int }
//	let seen: {Cell: bool} = {}
//	seen[Cell{x: 3, y: 4}] = true
//
// A struct can key a map when every field is a str, an int, a bool, a
// plain enum, or another such struct (the checker's keyable). The map
// needs three things of a key: a hash, equality and an order, since a
// map is printed and walked sorted by key. Equality is the language's
// own == on structs. The hash mixes the fields' hashes, a string's the
// one a {str: _} map uses. The order is field by field, in the order the
// struct declares them, each field compared the way a key of its own
// type would be.
//
// A struct is a value, and a key the map keeps must not change under it,
// or it would sit where a different key belongs. So the map stores a
// copy of a new key, and keys() and a for loop over the map hand out
// copies too.

// keyNode stands in for a source position in code the compiler writes
// itself, where a struct's layout is known to exist.
var keyNode = &Ident{Name: "key"}

// structKeyHash is one function per struct type.
func (l *lowerer) structKeyHash(v Reg, kt vty) Reg {
	lay, ok := l.layoutOf(keyNode, kt)
	if !ok {
		return l.constant(0)
	}
	sym := l.helperFunc("__keyhash "+kt.name, []vty{kt}, vInt, func(a []Reg) {
		obj := l.temp(kt)
		l.setSlot(obj, a[0])
		h := l.temp(vInt)
		l.setSlot(h, l.constant(-3750763034362895579))
		for _, f := range lay.fields {
			x := l.loadField(l.load(obj, kt), f)
			switch f.t.k {
			case kStr:
				x = l.mapHash(x, vMapOf(kStr, vInt))
			case kStruct:
				x = l.structKeyHash(x, f.t)
			}
			m := l.arith(OpMul, l.arith(OpBXor, l.load(h, vInt), x), l.constant(-7046029254386353131))
			l.setSlot(h, l.arith(OpBXor, m, l.arith(OpShrU, m, l.constant(29))))
		}
		l.ret(l.load(h, vInt))
	})
	return l.callHelper(sym, []Reg{v}, []vty{kt}, vInt)
}

// structKeyCmp is a three-way comparison, field by field.
func (l *lowerer) structKeyCmp(a, b Reg, kt vty) Reg {
	lay, ok := l.layoutOf(keyNode, kt)
	if !ok {
		return l.constant(0)
	}
	sym := l.helperFunc("__keycmp "+kt.name, []vty{kt, kt}, vInt, func(p []Reg) {
		as, bs := l.temp(kt), l.temp(kt)
		l.setSlot(as, p[0])
		l.setSlot(bs, p[1])
		for _, f := range lay.fields {
			fa := l.loadField(l.load(as, kt), f)
			fb := l.loadField(l.load(bs, kt), f)
			next := l.newLabel()
			switch f.t.k {
			case kStr, kStruct:
				c := l.temp(vInt)
				if f.t.k == kStr {
					l.setSlot(c, l.strCmp(fa, fb))
				} else {
					l.setSlot(c, l.structKeyCmp(fa, fb, f.t))
				}
				l.jumpIf(l.compare(OpEq, l.load(c, vInt), l.constant(0)), next)
				l.ret(l.load(c, vInt))
			default:
				fas, fbs := l.temp(vInt), l.temp(vInt)
				l.setSlot(fas, fa)
				l.setSlot(fbs, fb)
				notLess := l.newLabel()
				l.jumpNot(l.compare(OpLt, l.load(fas, vInt), l.load(fbs, vInt)), notLess)
				l.ret(l.constant(-1))
				l.mark(notLess)
				l.jumpNot(l.compare(OpGt, l.load(fas, vInt), l.load(fbs, vInt)), next)
				l.ret(l.constant(1))
			}
			l.mark(next)
		}
		l.ret(l.constant(0))
	})
	return l.callHelper(sym, []Reg{a, b}, []vty{kt, kt}, vInt)
}
