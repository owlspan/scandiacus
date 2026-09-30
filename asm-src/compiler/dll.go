package main

// DLLs: `veyl build --dll mod.vl` writes mod.dll.
//
//	export fn add(a: int, b: int) -> int { return a + b }
//	print("loaded")
//
// The top-level statements run once, when the library is loaded - that
// is what a mod's setup is - and every `export fn` is in the export
// table under its own name, for GetProcAddress or a mod loader to find.
//
// Three things differ from an executable.
//
// The entry point. Windows calls a DLL's with (instance, reason,
// reserved) on load, unload and thread start. On load it runs main,
// which is the top-level code; every call returns TRUE, since FALSE on
// load would make LoadLibrary fail. It does not touch the host's
// stdout mode: the program printing is a guest in someone else's
// console.
//
// The export table, built in pe.go from Module.Exports. Each export
// points at a stub rather than the function itself, for the same
// reason a callback does - see callback.go - with one difference: an
// exported int is a 64-bit int64_t, as a Veyl int is, so only a bool
// needs fixing up on the way in.
//
// Relocation. A DLL cannot count on its preferred address being free,
// so it has to be movable. Nothing this compiler emits holds an
// absolute address - every reference is a direct call or rip-relative,
// see pe.go - so moving the image needs no fix-ups at all, and the
// relocation table it carries is one empty block that says exactly
// that.

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// lowerForDLL is set when the program being compiled is a DLL, which
// changes one thing in lowering: nothing collects automatically, since
// the host may call exports on threads the collector cannot see.
var lowerForDLL bool

// dllBase is where a DLL asks to be loaded, the conventional spot for a
// 64-bit one. It moves elsewhere freely when this is taken.
const dllBase = 0x180000000

// dllEntrySymbol is the DLL's entry point, as __start is an executable's.
const dllEntrySymbol = "__dllstart"

// An Export is one exported function: its name and the stub native code
// enters it through.
type Export struct {
	Name  string
	Thunk Thunk
}

// exportOf builds the export for an `export fn`. The parameter types as
// written say which arguments are bools; see callbackWidening.
func exportOf(fd *FnDecl) Export {
	var widen []widenArg
	for i, p := range fd.Params {
		if t := strings.TrimSpace(p.Type); t != "int" {
			widen = append(widen, widenFor(i, t)...)
		}
	}
	if strings.TrimSpace(fd.Ret) == "f32" {
		widen = append(widen, widenArg{pos: -1, kind: memF32})
	}
	return Export{
		Name:  fd.Name,
		Thunk: Thunk{Sym: "__vy_export_" + sanitizeSym(fd.Name), Target: fnSym(fd.Name), Widen: widen},
	}
}

// dllStart is the entry point Windows calls with the reason in edx:
// 1 is DLL_PROCESS_ATTACH, the load.
func (e *Emitter) dllStart() {
	e.b.WriteString("\n")
	e.label(dllEntrySymbol)
	e.comment("the entry point Windows calls on load, unload and thread start")
	e.line("cmp edx, 1")
	e.line("jne .Ldll_done")
	// At entry rsp is 8 past a multiple of 16; 40 makes it a multiple
	// again and leaves main its 32 bytes of shadow space.
	e.line("sub rsp, 40")
	e.line("call main")
	e.line("add rsp, 40")
	e.b.WriteString(".Ldll_done:\n")
	e.line("mov eax, 1")
	e.line("ret")
}

// buildExports lays out a DLL's export table at rva:
//
//	the directory, forty bytes
//	the function table: one RVA per export
//	the name table: one RVA per name, sorted, since the loader
//	    binary-searches it
//	the ordinal table: which function each name is
//	the DLL's own name, then each export's name
//
// The functions are listed in the same sorted order as the names, so
// name i is simply function i.
func buildExports(obj *object, opt peOptions, rva, textRVA int) ([]byte, error) {
	exports := append([]Export(nil), opt.exports...)
	sort.Slice(exports, func(i, j int) bool { return exports[i].Name < exports[j].Name })
	n := len(exports)

	funcsAt := 40
	namesAt := funcsAt + 4*n
	ordsAt := namesAt + 4*n
	strAt := ordsAt + 2*n

	var strs []byte
	dllName := strAt + len(strs)
	strs = append(append(strs, opt.name...), 0)
	nameRVAs := make([]int, n)
	for i, x := range exports {
		nameRVAs[i] = strAt + len(strs)
		strs = append(append(strs, x.Name...), 0)
	}

	buf := make([]byte, strAt+len(strs))
	put32 := func(at, v int) { binary.LittleEndian.PutUint32(buf[at:], uint32(v)) }

	put32(12, rva+dllName)
	put32(16, 1) // ordinals start at 1
	put32(20, n)
	put32(24, n)
	put32(28, rva+funcsAt)
	put32(32, rva+namesAt)
	put32(36, rva+ordsAt)

	for i, x := range exports {
		sym, ok := obj.sym[x.Thunk.Sym]
		if !ok || sym.sec != secText {
			return nil, fmt.Errorf("the export %s has no code", x.Name)
		}
		put32(funcsAt+4*i, textRVA+sym.off)
		put32(namesAt+4*i, rva+nameRVAs[i])
		binary.LittleEndian.PutUint16(buf[ordsAt+2*i:], uint16(i))
	}
	copy(buf[strAt:], strs)
	return buf, nil
}

// emptyRelocations is a base relocation table holding nothing to fix:
// one block, for the first page of code, whose two entries are the
// padding kind the loader skips. An image with no table at all reads to
// some tools as one that cannot move; this one says it can and needs
// nothing done.
func emptyRelocations(page int) []byte {
	buf := make([]byte, 12)
	binary.LittleEndian.PutUint32(buf[0:], uint32(page))
	binary.LittleEndian.PutUint32(buf[4:], 12)
	return buf
}
