package main

// Linking static libraries.
//
// `extern fn f(...) from "x.lib"` names a symbol that lives in a static
// library rather than a DLL. A DLL is resolved by the loader at run time
// through the import table (see pe.go); a static library is resolved here,
// at build time, by pulling the object code that defines the symbol out of
// the library and folding it into the image as if the compiler had emitted
// it.
//
// The format is COFF: an archive (`!<arch>`) of COFF object files, the
// same thing `ar` writes and `link` reads. This file reads both, pulls the
// members a program needs, lays their sections into the image's own
// sections, and turns their relocations into the relocation list writePE
// already knows how to apply.
//
// What is supported so far: self-contained object code - functions and the
// read-only, writable and zeroed data they refer to, calling each other
// and nothing outside the libraries named. Relocations handled are ADDR64,
// ADDR32NB and REL32 (including the REL32_1..5 forms). Not yet: object
// code that calls into a DLL, the small runtime helpers a C compiler
// expects a linker to supply, COMDAT de-duplication, weak externals,
// common symbols, and the unwind tables (.pdata/.xdata) that exception
// handling needs. Each of those is a clear error naming what was missing
// rather than a wrong image.

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ---- the registry lowering fills in ----
//
// Like overrideImport in pe.go, this is a package-level map written while
// the program is lowered and read once at build time. The compiler process
// handles one program, so this threads no extra state through the lowerer.
var staticImports = map[string]string{}

// registerStatic records that a symbol comes from a static library file,
// named as the `from` string wrote it. buildExe reads the map afterwards.
func registerStatic(sym, file string) { staticImports[sym] = file }

// isStaticLib reports whether a `from` string names a static library
// rather than a DLL, by its extension.
func isStaticLib(file string) bool {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".lib", ".a", ".obj", ".o":
		return true
	}
	return false
}

// staticLibFiles is every distinct library file the program named, so the
// missing-file check and the linker can look at each once.
func staticLibFiles() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range staticImports {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// staticSymbolSet is the set of symbols a static library defines, so
// assembleObject leaves them for this file to resolve rather than making
// an import thunk.
func staticSymbolSet() map[string]bool {
	m := make(map[string]bool, len(staticImports))
	for s := range staticImports {
		m[s] = true
	}
	return m
}

// ---- COFF, as read out of a file ----

const (
	coffMachineAMD64 = 0x8664

	scnCntCode    = 0x00000020
	scnCntInit    = 0x00000040
	scnCntUninit  = 0x00000080
	scnLnkInfo    = 0x00000200 // .drectve and the like: not part of the image
	scnLnkRemove  = 0x00000800
	scnMemDiscard = 0x02000000
	scnMemExec    = 0x20000000
	scnMemRead    = 0x40000000
	scnMemWrite   = 0x80000000
	scnAlignMask  = 0x00F00000

	symClassExternal = 2
	symClassStatic   = 3

	relAMD64Absolute = 0
	relAMD64Addr64   = 1
	relAMD64Addr32   = 2
	relAMD64Addr32NB = 3
	relAMD64Rel32    = 4
	relAMD64Rel32_1  = 5
	relAMD64Rel32_5  = 9
)

// objSection is one section of an object, its bytes and its relocations
// still as the file stored them.
type objSection struct {
	name   string
	chars  uint32
	data   []byte // nil for an uninitialised (.bss) section
	size   int    // byte length, which for .bss is all there is
	align  int
	relocs []coffReloc

	// Filled in when the section is placed into the image: which image
	// section it went to and where it starts there. kept is false for a
	// section left out of the image (debug, directives, unwind tables).
	kept  bool
	place secID
	off   int
}

type coffReloc struct {
	off uint32 // where in this section the field is
	sym uint32 // index into the object's symbol table
	typ uint16
}

// coffSym is one symbol table entry, the auxiliary records skipped.
type coffSym struct {
	name    string
	value   uint32
	section int // 1-based section number; 0 undefined; negative is special
	class   uint8
}

// coffObject is a whole object file: its member name for error messages,
// its sections, its symbol table, and the two derived sets the on-demand
// pull needs.
type coffObject struct {
	member    string
	sections  []objSection
	symbols   []coffSym
	defs      []string // external symbols this object defines
	undefined []string // external symbols it references but does not define
}

// parseStaticLib reads a file that is either a COFF archive or a single
// COFF object and returns the objects in it.
func parseStaticLib(name string, raw []byte) ([]*coffObject, error) {
	if len(raw) >= 8 && string(raw[:8]) == "!<arch>\n" {
		return parseArchive(name, raw)
	}
	o, err := parseObject(name, raw)
	if err != nil {
		return nil, err
	}
	return []*coffObject{o}, nil
}

// parseArchive walks the members of a `!<arch>` file. The three special
// members - the symbol index `/`, the long-name table `//`, and any
// __.SYMDEF - are skipped; every other member is a COFF object.
func parseArchive(name string, raw []byte) ([]*coffObject, error) {
	var longNames []byte
	var out []*coffObject
	p := 8
	for p+60 <= len(raw) {
		hdr := raw[p : p+60]
		memName := strings.TrimRight(string(hdr[0:16]), " ")
		sizeStr := strings.TrimSpace(string(hdr[48:58]))
		size, err := strconv.Atoi(sizeStr)
		if err != nil {
			return nil, fmt.Errorf("%s: a member header has an unreadable size %q", name, sizeStr)
		}
		body := p + 60
		if body+size > len(raw) {
			return nil, fmt.Errorf("%s: a member runs past the end of the file", name)
		}
		data := raw[body : body+size]

		switch {
		case memName == "/" || memName == "/SYM64/" || memName == "__.SYMDEF":
			// The symbol index. We build our own from each member, so it
			// is not needed.
		case memName == "//":
			longNames = data
		default:
			resolved := memName
			if strings.HasPrefix(memName, "/") {
				// A long member name: /NNN is an offset into the // table.
				if off, err := strconv.Atoi(memName[1:]); err == nil {
					resolved = archiveLongName(longNames, off)
				}
			} else {
				resolved = strings.TrimSuffix(memName, "/")
			}
			o, err := parseObject(name+"("+resolved+")", data)
			if err != nil {
				return nil, err
			}
			out = append(out, o)
		}

		p = body + size
		if size%2 == 1 {
			p++ // members are padded to an even offset
		}
	}
	return out, nil
}

func archiveLongName(table []byte, off int) string {
	if off < 0 || off >= len(table) {
		return "?"
	}
	end := off
	for end < len(table) && table[end] != '\n' && table[end] != 0 {
		end++
	}
	return strings.TrimSuffix(string(table[off:end]), "/")
}

// parseObject reads one COFF object file.
func parseObject(member string, raw []byte) (*coffObject, error) {
	if len(raw) < 20 {
		return nil, fmt.Errorf("%s is too short to be an object file", member)
	}
	machine := binary.LittleEndian.Uint16(raw[0:])
	if machine != coffMachineAMD64 {
		return nil, fmt.Errorf("%s is not an x86-64 object file", member)
	}
	numSec := int(binary.LittleEndian.Uint16(raw[2:]))
	ptrSym := int(binary.LittleEndian.Uint32(raw[8:]))
	numSym := int(binary.LittleEndian.Uint32(raw[12:]))
	optSize := int(binary.LittleEndian.Uint16(raw[16:]))

	// The string table sits after the symbols; long section and symbol
	// names point into it.
	strTab := []byte(nil)
	if ptrSym != 0 {
		strAt := ptrSym + numSym*18
		if strAt <= len(raw) {
			strTab = raw[strAt:]
		}
	}

	obj := &coffObject{member: member}

	// Section headers.
	secStart := 20 + optSize
	for i := 0; i < numSec; i++ {
		h := secStart + i*40
		if h+40 > len(raw) {
			return nil, fmt.Errorf("%s: a section header runs past the end", member)
		}
		sec := objSection{
			name:  coffSecName(raw[h:h+8], strTab),
			chars: binary.LittleEndian.Uint32(raw[h+36:]),
		}
		sec.align = alignFromChars(sec.chars)
		sizeRaw := int(binary.LittleEndian.Uint32(raw[h+16:]))
		virt := int(binary.LittleEndian.Uint32(raw[h+8:]))
		ptrRaw := int(binary.LittleEndian.Uint32(raw[h+20:]))
		if sec.chars&scnCntUninit != 0 {
			sec.size = sizeRaw
			if virt > sec.size {
				sec.size = virt
			}
		} else if ptrRaw != 0 {
			if ptrRaw+sizeRaw > len(raw) {
				return nil, fmt.Errorf("%s: section %s runs past the end", member, sec.name)
			}
			sec.data = raw[ptrRaw : ptrRaw+sizeRaw]
			sec.size = sizeRaw
		}

		ptrRel := int(binary.LittleEndian.Uint32(raw[h+24:]))
		numRel := int(binary.LittleEndian.Uint16(raw[h+32:]))
		for r := 0; r < numRel; r++ {
			e := ptrRel + r*10
			if e+10 > len(raw) {
				return nil, fmt.Errorf("%s: a relocation of %s runs past the end", member, sec.name)
			}
			sec.relocs = append(sec.relocs, coffReloc{
				off: binary.LittleEndian.Uint32(raw[e:]),
				sym: binary.LittleEndian.Uint32(raw[e+4:]),
				typ: binary.LittleEndian.Uint16(raw[e+8:]),
			})
		}
		obj.sections = append(obj.sections, sec)
	}

	// Symbol table. Each entry is 18 bytes; an entry may be followed by
	// auxiliary records, which are counted in its last byte and skipped.
	// The skipped slots are kept as empty symbols so that a relocation's
	// index still lands on the right entry.
	for i := 0; i < numSym; i++ {
		e := ptrSym + i*18
		if e+18 > len(raw) {
			return nil, fmt.Errorf("%s: the symbol table runs past the end", member)
		}
		s := coffSym{
			name:    coffSymName(raw[e:e+8], strTab),
			value:   binary.LittleEndian.Uint32(raw[e+8:]),
			section: int(int16(binary.LittleEndian.Uint16(raw[e+12:]))),
			class:   raw[e+16],
		}
		aux := int(raw[e+17])
		obj.symbols = append(obj.symbols, s)
		for a := 0; a < aux; a++ {
			i++
			obj.symbols = append(obj.symbols, coffSym{})
		}

		if s.class == symClassExternal {
			switch {
			case s.section > 0:
				obj.defs = append(obj.defs, s.name)
			case s.section == 0 && s.value == 0:
				obj.undefined = append(obj.undefined, s.name)
			}
		}
	}
	return obj, nil
}

// coffSecName reads a section header's name, following a /NNN reference
// into the string table for the long ones.
func coffSecName(field, strTab []byte) string {
	if field[0] == '/' {
		if off, err := strconv.Atoi(strings.TrimRight(string(field[1:]), "\x00 ")); err == nil {
			return stringAt(strTab, off)
		}
	}
	return strings.TrimRight(string(field), "\x00")
}

// coffSymName reads a symbol's name: eight bytes inline, or, when the
// first four are zero, an offset into the string table.
func coffSymName(field, strTab []byte) string {
	if binary.LittleEndian.Uint32(field[0:]) == 0 {
		off := int(binary.LittleEndian.Uint32(field[4:]))
		return stringAt(strTab, off)
	}
	return strings.TrimRight(string(field), "\x00")
}

func stringAt(strTab []byte, off int) string {
	if off < 0 || off >= len(strTab) {
		return ""
	}
	end := off
	for end < len(strTab) && strTab[end] != 0 {
		end++
	}
	return string(strTab[off:end])
}

func alignFromChars(chars uint32) int {
	n := (chars & scnAlignMask) >> 20
	if n == 0 {
		return 1
	}
	return 1 << (n - 1)
}

// ---- folding the objects into the image ----

// linkStatic pulls the object code the program needs out of the static
// libraries it named and folds it into obj, so writePE lays it out with
// everything else. root is the directory the source sits in, which is
// where a relative library path is resolved against.
func linkStatic(obj *object, root string, imports map[string]string) error {
	if len(imports) == 0 {
		return nil
	}

	// Which library-provided symbols the program actually refers to. A
	// declared-but-uncalled extern pulls nothing.
	referenced := map[string]bool{}
	for _, r := range obj.relocs {
		if imports[r.sym] != "" {
			if _, defined := obj.sym[r.sym]; !defined {
				referenced[r.sym] = true
			}
		}
	}
	if len(referenced) == 0 {
		return nil
	}

	// Read every named library once and index which member defines each
	// external symbol. The first definition of a name wins.
	var members []*coffObject
	defBy := map[string]int{}
	for _, f := range staticLibFiles() {
		path := f
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, f)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("cannot read the static library %s: %v", f, err)
		}
		objs, err := parseStaticLib(f, raw)
		if err != nil {
			return err
		}
		for _, o := range objs {
			idx := len(members)
			members = append(members, o)
			for _, name := range o.defs {
				if _, dup := defBy[name]; !dup {
					defBy[name] = idx
				}
			}
		}
	}

	// Pull members on demand: start from the referenced symbols, then
	// follow each pulled member's own undefined externals as far as the
	// libraries can satisfy them.
	pulled := map[int]bool{}
	var order []int
	var work []string
	for s := range referenced {
		work = append(work, s)
	}
	sort.Strings(work)
	for len(work) > 0 {
		s := work[0]
		work = work[1:]
		idx, ok := defBy[s]
		if !ok {
			continue // resolved elsewhere, or an error caught below
		}
		if pulled[idx] {
			continue
		}
		pulled[idx] = true
		order = append(order, idx)
		for _, u := range members[idx].undefined {
			if _, ok := defBy[u]; ok {
				work = append(work, u)
			}
		}
	}
	sort.Ints(order) // a stable image regardless of pull order

	// Pass one: place each pulled member's kept sections into the image,
	// and register the external symbols they define.
	for _, mi := range order {
		if err := placeMemberSections(obj, members[mi]); err != nil {
			return err
		}
	}
	for _, mi := range order {
		m := members[mi]
		for i := range m.symbols {
			s := m.symbols[i]
			if s.class != symClassExternal || s.section <= 0 {
				continue
			}
			sec := &m.sections[s.section-1]
			if !sec.kept {
				continue
			}
			if _, exists := obj.sym[s.name]; !exists {
				obj.sym[s.name] = symbol{sec.place, sec.off + int(s.value)}
			}
		}
	}

	// Pass two: turn each kept section's relocations into the image's own,
	// collecting the external symbols no member defines.
	deps := &staticDeps{direct: map[string]bool{}, imp: map[string]bool{}}
	for _, mi := range order {
		if err := relocateMember(obj, members[mi], mi, deps); err != nil {
			return err
		}
	}

	// Satisfy those external symbols as DLL imports.
	if err := resolveObjectImports(obj, deps); err != nil {
		return err
	}

	// Every referenced library symbol must now have a definition.
	var undefined []string
	for s := range referenced {
		if _, ok := obj.sym[s]; !ok {
			undefined = append(undefined, s)
		}
	}
	if len(undefined) > 0 {
		sort.Strings(undefined)
		return fmt.Errorf("no static library defines: %s", strings.Join(undefined, ", "))
	}
	return nil
}

// placeMemberSections appends one object's kept sections to the image
// sections, recording where each one landed.
func placeMemberSections(obj *object, m *coffObject) error {
	for i := range m.sections {
		sec := &m.sections[i]
		dest, keep := classifySection(sec)
		if !keep {
			continue
		}
		align := sec.align
		if align < 1 {
			align = 1
		}
		switch dest {
		case secText:
			sec.off = alignImage(&obj.text, align)
			obj.text = append(obj.text, sec.data...)
		case secRdata:
			sec.off = alignImage(&obj.rdata, align)
			obj.rdata = append(obj.rdata, sec.data...)
		case secData:
			sec.off = alignImage(&obj.data, align)
			obj.data = append(obj.data, sec.data...)
		case secBss:
			obj.bssLen = alignUp(obj.bssLen, align)
			sec.off = obj.bssLen
			obj.bssLen += sec.size
		}
		sec.kept = true
		sec.place = dest
	}
	return nil
}

// classifySection decides which image section an object's section belongs
// in, or that it is left out of the image entirely.
func classifySection(sec *objSection) (secID, bool) {
	if sec.chars&(scnLnkRemove|scnLnkInfo|scnMemDiscard) != 0 {
		return 0, false
	}
	// Unwind tables are not laid out yet; a program that never unwinds
	// runs without them.
	if strings.HasPrefix(sec.name, ".pdata") || strings.HasPrefix(sec.name, ".xdata") {
		return 0, false
	}
	if strings.HasPrefix(sec.name, ".debug") {
		return 0, false
	}
	switch {
	case sec.chars&(scnCntCode|scnMemExec) != 0:
		return secText, true
	case sec.chars&scnCntUninit != 0:
		return secBss, true
	case sec.chars&scnMemWrite != 0:
		return secData, true
	default:
		return secRdata, true
	}
}

// alignImage pads an image section up to an alignment and returns the new
// offset, where the caller then appends.
func alignImage(buf *[]byte, align int) int {
	for len(*buf)%align != 0 {
		*buf = append(*buf, 0)
	}
	return len(*buf)
}

// staticDeps collects the external symbols object code refers to that no
// member defines, so they can be satisfied as DLL imports afterwards.
// A direct reference (a call, a rip-relative load) needs a thunk; an
// __imp_ reference wants the import-table slot itself.
type staticDeps struct {
	direct map[string]bool
	imp    map[string]bool
}

// relocateMember turns one object's relocations into entries on obj.relocs,
// resolving each to a name writePE can look up.
func relocateMember(obj *object, m *coffObject, mi int, deps *staticDeps) error {
	for si := range m.sections {
		sec := &m.sections[si]
		if !sec.kept {
			continue
		}
		for _, rel := range sec.relocs {
			if rel.typ == relAMD64Absolute {
				continue
			}
			if int(rel.sym) >= len(m.symbols) {
				return fmt.Errorf("%s: a relocation names symbol %d, which does not exist", m.member, rel.sym)
			}
			target := m.symbols[rel.sym]

			name, err := relocTargetName(obj, m, mi, int(rel.sym), target, deps)
			if err != nil {
				return err
			}

			r := reloc{
				at:  sec.off + int(rel.off),
				sym: name,
				sec: sec.place,
			}
			switch {
			case rel.typ == relAMD64Addr64:
				r.kind = relAddr64
			case rel.typ == relAMD64Addr32NB:
				r.kind = relAddr32NB
			case rel.typ >= relAMD64Rel32 && rel.typ <= relAMD64Rel32_5:
				r.kind = relRel32
				r.next = r.at + 4 + int(rel.typ-relAMD64Rel32)
				r.isCall = true
			case rel.typ == relAMD64Addr32:
				return fmt.Errorf("%s: a 32-bit absolute address relocation cannot be used "+
					"in an image with a 64-bit base", m.member)
			default:
				return fmt.Errorf("%s: relocation type %d is not supported", m.member, rel.typ)
			}
			obj.relocs = append(obj.relocs, r)
		}
	}
	return nil
}

// relocTargetName gives a relocation's target a name writePE resolves.
//
// A target defined in this same member (a static function, a section) is
// given a unique internal name and registered on the spot. A target that
// is defined by another member, or by the program, keeps its own name. A
// target that nothing here defines is recorded as a dependency to satisfy
// as a DLL import; an __imp_ reference wants the import slot itself, so it
// resolves to the slot's own name.
func relocTargetName(obj *object, m *coffObject, mi, symIdx int, s coffSym, deps *staticDeps) (string, error) {
	if s.section > 0 {
		sec := &m.sections[s.section-1]
		if !sec.kept {
			return "", fmt.Errorf("%s: a relocation points into section %s, which is not in the image",
				m.member, sec.name)
		}
		name := fmt.Sprintf("\x00static.%d.%d", mi, symIdx)
		if _, ok := obj.sym[name]; !ok {
			obj.sym[name] = symbol{sec.place, sec.off + int(s.value)}
		}
		return name, nil
	}
	if s.section == 0 && s.name != "" {
		if s.value != 0 {
			return "", fmt.Errorf("%s references the common symbol %s, "+
				"which is not supported yet", m.member, s.name)
		}
		if base := strings.TrimPrefix(s.name, "__imp_"); base != s.name {
			deps.imp[base] = true
			return iatSym(base), nil
		}
		if _, ok := obj.sym[s.name]; !ok {
			deps.direct[s.name] = true
		}
		return s.name, nil
	}
	return "", fmt.Errorf("%s: a relocation names an unusable symbol", m.member)
}

// runtimeHelpers are symbols a C compiler expects the linker to supply
// itself - stack probes, the constructor shim, the stack-guard cookie -
// rather than import from a DLL. They are not linked yet, so a program
// that needs one gets a clear error instead of an image that will not
// load.
var runtimeHelpers = map[string]bool{
	"__chkstk_ms": true, "___chkstk_ms": true, "__chkstk": true, "___chkstk": true,
	"__main": true, "___main": true,
	"_fltused": true, "__fltused": true,
	"__security_cookie": true, "__security_check_cookie": true,
	"__GSHandlerCheck": true, "__CxxFrameHandler3": true,
}

// resolveObjectImports satisfies the external symbols object code needs
// that no member defines, by importing them from a DLL the way the loader
// resolves the rest of the program's imports. A direct reference also gets
// a jmp thunk so an ordinary call reaches the imported address.
func resolveObjectImports(obj *object, deps *staticDeps) error {
	need := map[string]bool{}
	for s := range deps.imp {
		need[s] = true
	}
	for s := range deps.direct {
		need[s] = true
	}

	var helpers []string
	for s := range need {
		if runtimeHelpers[s] {
			helpers = append(helpers, s)
		}
	}
	if len(helpers) > 0 {
		sort.Strings(helpers)
		return fmt.Errorf("the static library needs the C runtime helper(s) %s, "+
			"which are not linked yet", strings.Join(helpers, ", "))
	}

	// Add each needed symbol to the import set, then rebuild a sorted,
	// duplicate-free extern list for the import table.
	externSet := map[string]bool{}
	for _, e := range obj.externs {
		externSet[e] = true
	}
	for s := range need {
		externSet[s] = true
	}
	obj.externs = obj.externs[:0]
	for e := range externSet {
		obj.externs = append(obj.externs, e)
	}
	sort.Strings(obj.externs)

	// A direct reference reaches the import through a six-byte thunk, the
	// same one link.go writes for the program's own externs. An __imp_
	// reference already points at the slot, so it needs none.
	var direct []string
	for s := range deps.direct {
		if _, ok := obj.sym[s]; !ok {
			direct = append(direct, s)
		}
	}
	sort.Strings(direct)
	for _, s := range direct {
		off := len(obj.text)
		obj.text = append(obj.text, 0xFF, 0x25, 0, 0, 0, 0) // jmp qword ptr [rip+d32]
		obj.sym[s] = symbol{secText, off}
		obj.relocs = append(obj.relocs, reloc{at: off + 2, sym: iatSym(s), next: off + 6})
	}
	return nil
}
