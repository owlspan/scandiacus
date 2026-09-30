package main

// The PE writer: a laid-out image to an executable Windows will run.
//
// This is what removes MinGW. link.go has already turned the assembly
// into sections and knows where every label sits relative to the start
// of its section; here those sections are given addresses in one image,
// every reference is filled in, and the whole thing is written in the
// format the loader reads.
//
// Two decisions keep it short.
//
// The image is fixed at one base address and says so, by setting
// RELOCS_STRIPPED and not asking for a dynamic base. Every reference
// this compiler emits is either a direct call or rip-relative, so
// nothing in the file holds an absolute address and there is no base
// relocation table to write. The cost is that the executable is not
// ASLR'd, which is a security property worth having and worth adding
// later; the gain is that a whole section and a whole pass do not exist.
//
// Imports come from msvcrt.dll and kernel32.dll, both of which ship with
// Windows on every machine that can run the result. That is the point:
// a self-written import table against DLLs that are already there needs
// no toolchain and no redistributable, so the dependency goes away
// rather than moving somewhere else.

import (
	"encoding/binary"
	"fmt"
	"os"
	"sort"
)

const (
	// A high base, the conventional one for a 64-bit executable, chosen
	// so that a null pointer dereference cannot land anywhere mapped.
	imageBase = 0x140000000

	sectionAlignment = 0x1000
	fileAlignment    = 0x200

	// The name Windows starts the program at. x64.go writes it: it sets
	// the stack up, calls main, and hands main's result to exit.
	entrySymbol = "__start"
)

func alignUp(n, to int) int {
	if n%to == 0 {
		return n
	}
	return n + to - n%to
}

// importOverride is for a foreign symbol the rule below gets wrong.
// The sockets, which the naming rule cannot place: `socket` and `recv`
// are lowercase and are not in msvcrt, `WSAStartup` is capitalised and
// is not in kernel32.
var importOverride = func() map[string]string {
	m := map[string]string{}
	for _, s := range winsockSyms {
		m[s] = "ws2_32.dll"
	}
	for _, s := range user32Syms {
		m[s] = "user32.dll"
	}
	for _, s := range gdi32Syms {
		m[s] = "gdi32.dll"
	}
	for _, s := range msimg32Syms {
		m[s] = "msimg32.dll"
	}
	for _, s := range winmmSyms {
		m[s] = "winmm.dll"
	}
	for _, s := range winhttpSyms {
		m[s] = "winhttp.dll"
	}
	// Not a Windows library: it ships with the sqlite package and is
	// copied next to the executable at build time. A program that never
	// calls db.* never imports it.
	for _, s := range sqliteSyms {
		m[s] = "sqlite3.dll"
	}
	return m
}()

// importDLL says which library a foreign symbol comes from.
//
// The rule is the one the two libraries themselves follow: a Win32 API
// is named in PascalCase and a C runtime function is not, so an initial
// capital means kernel32 and anything else means msvcrt. It holds for
// every symbol this compiler emits and for the wider API either side of
// them, which is why it is a rule here rather than a list that would
// have to be edited every time a library function is added.
func importDLL(sym string) string {
	if dll, ok := importOverride[sym]; ok {
		return dll
	}
	if sym != "" && sym[0] >= 'A' && sym[0] <= 'Z' {
		return "kernel32.dll"
	}
	return "msvcrt.dll"
}

// overrideImport pins a foreign symbol to the library that exports it.
// This is how `extern fn ... from "miniz.dll"` reaches the import
// table: lowering records the pairing here, and buildImports reads it
// afterwards. The compiler process handles one program, so mutating a
// package-level map costs nothing and threads no extra state through.
func overrideImport(sym, dll string) { importOverride[sym] = dll }

// knownSystemDLL lists the libraries Windows itself installs, which a
// program may import without anything being placed beside it. Anything
// outside this set has to be found before the build, or the executable
// will refuse to start with no useful message - see missingForeignDLLs
// in pkg.go, which is what consumes it.
var knownSystemDLL = map[string]bool{
	"kernel32.dll": true,
	"user32.dll":   true,
	"gdi32.dll":    true,
	"shell32.dll":  true,
	"advapi32.dll": true,
	"winmm.dll":    true,
	"msimg32.dll":  true,
	"comdlg32.dll": true,
	"ole32.dll":    true,
	"oleaut32.dll": true,
	"ws2_32.dll":   true,
	"winhttp.dll":  true,
	"msvcrt.dll":   true,
}

// A section as it will appear in the file.
type peSection struct {
	name  string
	rva   int
	vsize int
	data  []byte // nil means uninitialised, as .bss is
	chars uint32
}

const (
	scnCode    = 0x00000020
	scnData    = 0x00000040
	scnBSS     = 0x00000080
	scnExecute = 0x20000000
	scnRead    = 0x40000000
	scnWrite   = 0x80000000
	// The loader may drop the section once the image is in place.
	scnDiscardable = 0x02000000
)

// writePE lays the object out and writes an executable.
//
// The read-only data rides inside the text section rather than getting
// one of its own. Both land on read-only pages once mapped, so one
// section carries both, and every executable saves a file-aligned block
// and a page of image. The trade is stated plainly: string bytes sit on
// pages marked executable. Nothing writes through those addresses and
// control never reaches them, so the pages are allowed more than they
// are used for, never less. The region is kept eight-byte aligned,
// because float loads read pooled doubles straight out of it.
// Disabled with VEYL_NOMERGE=1, which puts .rdata back in its own
// section exactly as before.
// peOptions is what differs between an executable and a DLL.
type peOptions struct {
	dll     bool
	name    string   // the DLL's file name, as its export table records it
	exports []Export // what the export table lists
}

func writePE(obj *object, out string, opt peOptions) error {
	text := append([]byte(nil), obj.text...)

	// Where the read-only data begins inside the merged text, or -1
	// when the two stay separate sections.
	rdataAt := -1
	if !envOff("VEYL_NOMERGE") && !envOff("VEYL_NOOPT") && len(obj.rdata) > 0 {
		for len(text)%8 != 0 {
			text = append(text, 0)
		}
		rdataAt = len(text)
		text = append(text, obj.rdata...)
	}

	textRVA := sectionAlignment
	rdataRVA := alignUp(textRVA+len(text), sectionAlignment)
	if rdataAt >= 0 {
		rdataRVA = textRVA + rdataAt
	}
	idataRVA := alignUp(rdataRVA+len(obj.rdata), sectionAlignment)

	idata, slots, iatRVA, iatSize := buildImports(obj.externs, idataRVA)

	// A DLL's export table and its relocation block come next, each in
	// a section of its own.
	next := alignUp(idataRVA+len(idata), sectionAlignment)
	var edata, reloc []byte
	edataRVA, relocRVA := 0, 0
	if opt.dll {
		edataRVA = next
		var err error
		edata, err = buildExports(obj, opt, edataRVA, textRVA)
		if err != nil {
			return err
		}
		relocRVA = alignUp(edataRVA+len(edata), sectionAlignment)
		reloc = emptyRelocations(textRVA)
		next = alignUp(relocRVA+len(reloc), sectionAlignment)
	}

	// Writable initialised data, present only when a static library was
	// linked in; the compiler's own output never uses it.
	dataRVA := 0
	if len(obj.data) > 0 {
		dataRVA = next
		next = alignUp(next+len(obj.data), sectionAlignment)
	}
	bssRVA := next

	// Where each section begins, so a symbol's offset becomes an RVA.
	base := map[secID]int{
		secText:  textRVA,
		secRdata: rdataRVA,
		secData:  dataRVA,
		secBss:   bssRVA,
	}

	resolve := func(name string) (int, bool) {
		if rva, ok := slots[name]; ok {
			return rva, true
		}
		s, ok := obj.sym[name]
		if !ok {
			return 0, false
		}
		return base[s.sec] + s.off, true
	}

	// sectionBytes gives the byte slice a reloc's field lives in and the
	// address that slice starts at. .rdata may have been merged into the
	// text above, in which case its bytes are a window into text.
	sectionBytes := func(sec secID) ([]byte, int) {
		switch sec {
		case secRdata:
			if rdataAt >= 0 {
				return text[rdataAt:], rdataRVA
			}
			return obj.rdata, rdataRVA
		case secData:
			return obj.data, dataRVA
		}
		return text, textRVA
	}

	for _, r := range obj.relocs {
		target, ok := resolve(r.sym)
		if !ok {
			return fmt.Errorf("nothing defines %s", r.sym)
		}
		buf, secRVA := sectionBytes(r.sec)
		switch r.kind {
		case relAddr64:
			// A full 64-bit address. The image loads at a fixed base with
			// its relocations stripped, so the absolute value is written
			// once here and never moved.
			addend := int64(binary.LittleEndian.Uint64(buf[r.at : r.at+8]))
			binary.LittleEndian.PutUint64(buf[r.at:r.at+8], uint64(int64(imageBase)+int64(target)+addend))
		case relAddr32NB:
			// An image-relative 32-bit address.
			addend := int32(binary.LittleEndian.Uint32(buf[r.at : r.at+4]))
			binary.LittleEndian.PutUint32(buf[r.at:r.at+4], uint32(int32(target)+addend))
		default:
			// rel32: a call, a jump or a rip-relative load, all counted
			// from the end of the instruction. Whatever is already in the
			// field is the addend, put there by the encoder or carried in
			// from the object file.
			addend := int32(binary.LittleEndian.Uint32(buf[r.at : r.at+4]))
			delta := target - (secRVA + r.next) + int(addend)
			if delta < -(1<<31) || delta >= 1<<31 {
				return fmt.Errorf("%s is too far away to reach", r.sym)
			}
			binary.LittleEndian.PutUint32(buf[r.at:r.at+4], uint32(int32(delta)))
		}
	}

	entrySym := entrySymbol
	if opt.dll {
		entrySym = dllEntrySymbol
	}
	entry, ok := resolve(entrySym)
	if !ok {
		return fmt.Errorf("the program has no %s", entrySym)
	}

	sections := []peSection{
		{".text", textRVA, len(text), text, scnCode | scnExecute | scnRead},
	}
	if rdataAt < 0 {
		// Separate sections: the old layout, kept byte-for-byte under
		// the switch so the two are always comparable.
		sections = append(sections,
			peSection{".rdata", rdataRVA, len(obj.rdata), obj.rdata, scnData | scnRead})
	}
	sections = append(sections,
		peSection{".idata", idataRVA, len(idata), idata, scnData | scnRead | scnWrite})
	img := imageInfo{entry: entry, importRVA: idataRVA, importSize: len(idata),
		iatRVA: iatRVA, iatSize: iatSize, dll: opt.dll}
	if opt.dll {
		sections = append(sections,
			peSection{".edata", edataRVA, len(edata), edata, scnData | scnRead},
			peSection{".reloc", relocRVA, len(reloc), reloc, scnData | scnRead | scnDiscardable})
		img.exportRVA, img.exportSize = edataRVA, len(edata)
		img.relocRVA, img.relocSize = relocRVA, len(reloc)
	}
	if len(obj.data) > 0 {
		sections = append(sections,
			peSection{".data", dataRVA, len(obj.data), obj.data, scnData | scnRead | scnWrite})
	}
	if obj.bssLen > 0 {
		sections = append(sections,
			peSection{".bss", bssRVA, obj.bssLen, nil, scnBSS | scnRead | scnWrite})
	}

	return writeImage(out, sections, img)
}

// imageInfo is what the headers point at.
type imageInfo struct {
	entry                 int
	importRVA, importSize int
	iatRVA, iatSize       int
	exportRVA, exportSize int
	relocRVA, relocSize   int
	dll                   bool
}

// buildImports produces the import section: one descriptor per library,
// a lookup table and an address table per library, and the name of every
// symbol.
//
// The two tables start out identical. The loader reads the lookup table
// to find out what to import and overwrites the address table with the
// addresses it found, which is why the call thunks in link.go point at
// the second one.
func buildImports(externs []string, rva int) (blob []byte, slots map[string]int, iatRVA, iatSize int) {
	byDLL := map[string][]string{}
	for _, sym := range externs {
		dll := importDLL(sym)
		byDLL[dll] = append(byDLL[dll], sym)
	}
	dlls := make([]string, 0, len(byDLL))
	for name := range byDLL {
		dlls = append(dlls, name)
	}
	sort.Strings(dlls)

	// Sizes first, so every part knows its own address before any of it
	// is written.
	descSize := (len(dlls) + 1) * 20
	tableSize := 0
	for _, dll := range dlls {
		tableSize += (len(byDLL[dll]) + 1) * 8
	}

	iltAt := descSize
	iatAt := iltAt + tableSize
	namesAt := iatAt + tableSize

	// The hint/name entries, and where each one ends up.
	nameOf := map[string]int{}
	var names []byte
	for _, dll := range dlls {
		for _, sym := range byDLL[dll] {
			nameOf[sym] = namesAt + len(names)
			names = append(names, 0, 0) // the hint, which may be zero
			names = append(names, []byte(sym)...)
			names = append(names, 0)
			if len(names)%2 != 0 {
				names = append(names, 0)
			}
		}
	}
	dllNameAt := map[string]int{}
	for _, dll := range dlls {
		dllNameAt[dll] = namesAt + len(names)
		names = append(names, []byte(dll)...)
		names = append(names, 0)
		if len(names)%2 != 0 {
			names = append(names, 0)
		}
	}

	blob = make([]byte, namesAt+len(names))
	copy(blob[namesAt:], names)

	slots = map[string]int{}
	ilt, iat := iltAt, iatAt
	for i, dll := range dlls {
		d := i * 20
		binary.LittleEndian.PutUint32(blob[d+0:], uint32(rva+ilt))
		binary.LittleEndian.PutUint32(blob[d+12:], uint32(rva+dllNameAt[dll]))
		binary.LittleEndian.PutUint32(blob[d+16:], uint32(rva+iat))

		for _, sym := range byDLL[dll] {
			binary.LittleEndian.PutUint64(blob[ilt:], uint64(rva+nameOf[sym]))
			binary.LittleEndian.PutUint64(blob[iat:], uint64(rva+nameOf[sym]))
			slots[iatSym(sym)] = rva + iat
			ilt += 8
			iat += 8
		}
		// The terminating zero of each table, which is already zero.
		ilt += 8
		iat += 8
	}

	return blob, slots, rva + iatAt, tableSize
}

// writeImage assembles the headers and the section bodies into a file.
func writeImage(out string, sections []peSection, img imageInfo) error {
	headerSize := alignUp(0x40+4+20+240+len(sections)*40, fileAlignment)

	// File offsets. A section with no initialised data occupies none.
	at := headerSize
	offsets := make([]int, len(sections))
	for i, s := range sections {
		if s.data == nil {
			continue
		}
		offsets[i] = at
		at += alignUp(len(s.data), fileAlignment)
	}
	fileSize := at

	last := sections[len(sections)-1]
	imageSize := alignUp(last.rva+last.vsize, sectionAlignment)

	var codeSize, initSize, uninitSize int
	for _, s := range sections {
		switch {
		case s.chars&scnCode != 0:
			codeSize += alignUp(len(s.data), fileAlignment)
		case s.chars&scnBSS != 0:
			uninitSize += alignUp(s.vsize, fileAlignment)
		default:
			initSize += alignUp(len(s.data), fileAlignment)
		}
	}

	buf := make([]byte, fileSize)

	// The DOS header. Only the magic and the offset at 0x3c matter to
	// Windows; the rest of the sixty-four bytes would be the stub's
	// business, and there is no stub, so running this under DOS prints
	// nothing rather than a message.
	copy(buf, "MZ")
	binary.LittleEndian.PutUint16(buf[0x02:], 0x90)
	binary.LittleEndian.PutUint16(buf[0x04:], 3)
	binary.LittleEndian.PutUint16(buf[0x08:], 4)
	binary.LittleEndian.PutUint16(buf[0x0c:], 0xffff)
	binary.LittleEndian.PutUint16(buf[0x18:], 0x40)
	binary.LittleEndian.PutUint32(buf[0x3c:], 0x40)

	p := 0x40
	copy(buf[p:], []byte{'P', 'E', 0, 0})
	p += 4

	// The COFF header.
	binary.LittleEndian.PutUint16(buf[p+0:], 0x8664) // x86-64
	binary.LittleEndian.PutUint16(buf[p+2:], uint16(len(sections)))
	binary.LittleEndian.PutUint16(buf[p+16:], 240) // optional header size
	// EXECUTABLE_IMAGE | LARGE_ADDRESS_AWARE | RELOCS_STRIPPED. A DLL
	// swaps the last for the DLL flag: it has to be movable.
	if img.dll {
		binary.LittleEndian.PutUint16(buf[p+18:], 0x0002|0x0020|0x2000)
	} else {
		binary.LittleEndian.PutUint16(buf[p+18:], 0x0002|0x0020|0x0001)
	}
	p += 20

	// The optional header, which is not optional.
	o := p
	binary.LittleEndian.PutUint16(buf[o+0:], 0x20b) // PE32+
	buf[o+2] = 14                                   // a linker version, for tools that print one
	binary.LittleEndian.PutUint32(buf[o+4:], uint32(codeSize))
	binary.LittleEndian.PutUint32(buf[o+8:], uint32(initSize))
	binary.LittleEndian.PutUint32(buf[o+12:], uint32(uninitSize))
	binary.LittleEndian.PutUint32(buf[o+16:], uint32(img.entry))
	binary.LittleEndian.PutUint32(buf[o+20:], uint32(sections[0].rva))
	if img.dll {
		binary.LittleEndian.PutUint64(buf[o+24:], dllBase)
	} else {
		binary.LittleEndian.PutUint64(buf[o+24:], imageBase)
	}
	binary.LittleEndian.PutUint32(buf[o+32:], sectionAlignment)
	binary.LittleEndian.PutUint32(buf[o+36:], fileAlignment)
	binary.LittleEndian.PutUint16(buf[o+40:], 6) // major OS version
	binary.LittleEndian.PutUint16(buf[o+48:], 6) // major subsystem version
	binary.LittleEndian.PutUint32(buf[o+56:], uint32(imageSize))
	binary.LittleEndian.PutUint32(buf[o+60:], uint32(headerSize))
	binary.LittleEndian.PutUint16(buf[o+68:], 3) // console subsystem
	// NX_COMPAT | TERMINAL_SERVER_AWARE. No dynamic base: the image has
	// no relocations to apply, so it has to load where it says it does.
	binary.LittleEndian.PutUint16(buf[o+70:], 0x0100|0x8000)
	if img.dll {
		// HIGH_ENTROPY_VA | DYNAMIC_BASE | NX_COMPAT: a DLL moves.
		binary.LittleEndian.PutUint16(buf[o+70:], 0x0020|0x0040|0x0100)
	}
	binary.LittleEndian.PutUint64(buf[o+72:], 0x100000) // stack reserve
	binary.LittleEndian.PutUint64(buf[o+80:], 0x1000)   // stack commit
	binary.LittleEndian.PutUint64(buf[o+88:], 0x100000) // heap reserve
	binary.LittleEndian.PutUint64(buf[o+96:], 0x1000)   // heap commit
	binary.LittleEndian.PutUint32(buf[o+108:], 16)      // data directories

	dirs := o + 112
	binary.LittleEndian.PutUint32(buf[dirs+0*8+0:], uint32(img.exportRVA))
	binary.LittleEndian.PutUint32(buf[dirs+0*8+4:], uint32(img.exportSize))
	binary.LittleEndian.PutUint32(buf[dirs+1*8+0:], uint32(img.importRVA))
	binary.LittleEndian.PutUint32(buf[dirs+1*8+4:], uint32(img.importSize))
	binary.LittleEndian.PutUint32(buf[dirs+5*8+0:], uint32(img.relocRVA))
	binary.LittleEndian.PutUint32(buf[dirs+5*8+4:], uint32(img.relocSize))
	binary.LittleEndian.PutUint32(buf[dirs+12*8+0:], uint32(img.iatRVA))
	binary.LittleEndian.PutUint32(buf[dirs+12*8+4:], uint32(img.iatSize))

	p = o + 240
	for i, s := range sections {
		h := p + i*40
		copy(buf[h:h+8], s.name)
		binary.LittleEndian.PutUint32(buf[h+8:], uint32(s.vsize))
		binary.LittleEndian.PutUint32(buf[h+12:], uint32(s.rva))
		if s.data != nil {
			binary.LittleEndian.PutUint32(buf[h+16:], uint32(alignUp(len(s.data), fileAlignment)))
			binary.LittleEndian.PutUint32(buf[h+20:], uint32(offsets[i]))
			copy(buf[offsets[i]:], s.data)
		}
		binary.LittleEndian.PutUint32(buf[h+36:], s.chars)
	}

	return os.WriteFile(out, buf, 0o755)
}
