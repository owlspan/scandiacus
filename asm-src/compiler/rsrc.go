package main

// The .rsrc section: the icon from --icon, the version information
// Explorer shows under Properties, Details, and the manifest.
//
// Every PE keeps resources in the same three-level tree: type, then
// name or ID, then language, then a data entry pointing at the bytes.
// Only IDs are used here, never names.
//
// An .ico file is a header, one 16-byte entry per image, and the images.
// An executable keeps the same images as separate RT_ICON resources and
// an RT_GROUP_ICON that lists them - the same entries with the file
// offset replaced by the resource ID.
//
// The version information and the manifest are there because almost
// every executable a mainstream toolchain writes has them, and their
// absence is one of the things antivirus heuristics score a file on.
// The manifest also asks for no elevation (asInvoker), which stops
// Windows guessing that a program called setup or install wants to run
// as administrator.

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
	rtManifest  = 24
	langNeutral = 0
	langEnUS    = 0x409
)

type icoImage struct {
	entry []byte // the 16-byte directory entry from the file
	data  []byte
}

// A resource is one leaf of the tree.
type resource struct {
	typ, id, lang int
	data          []byte
}

// parseICO reads an .ico file into its images, refusing anything that
// is not one rather than writing a resource Windows would ignore.
func parseICO(b []byte) ([]icoImage, error) {
	if len(b) < 6 || binary.LittleEndian.Uint16(b[0:]) != 0 || binary.LittleEndian.Uint16(b[2:]) != 1 {
		return nil, fmt.Errorf("not an .ico file")
	}
	n := int(binary.LittleEndian.Uint16(b[4:]))
	if n == 0 || len(b) < 6+16*n {
		return nil, fmt.Errorf("the .ico file has no images")
	}
	imgs := make([]icoImage, n)
	for i := range imgs {
		e := b[6+16*i : 6+16*i+16]
		size := int(binary.LittleEndian.Uint32(e[8:]))
		off := int(binary.LittleEndian.Uint32(e[12:]))
		if off < 0 || size <= 0 || off+size > len(b) {
			return nil, fmt.Errorf("image %d of the .ico file runs past its end", i+1)
		}
		imgs[i] = icoImage{entry: e, data: b[off : off+size]}
	}
	return imgs, nil
}

// iconResources is an icon as the resources an executable carries.
func iconResources(imgs []icoImage) []resource {
	n := len(imgs)
	le := binary.LittleEndian
	// The group: an .ico header, then each entry with its last field,
	// the 4-byte file offset, cut to a 2-byte resource ID.
	group := make([]byte, 6+14*n)
	le.PutUint16(group[2:], 1)
	le.PutUint16(group[4:], uint16(n))
	var out []resource
	for i, im := range imgs {
		g := group[6+14*i:]
		copy(g[:12], im.entry[:12])
		le.PutUint16(g[12:], uint16(i+1))
		out = append(out, resource{rtIcon, i + 1, langNeutral, im.data})
	}
	return append(out, resource{rtGroupIcon, 1, langNeutral, group})
}

// buildResources is the tree for an icon alone, which is what the
// compiler's own .syso carries.
func buildResources(imgs []icoImage, rva int) []byte {
	return buildResourceTree(iconResources(imgs), rva)
}

// buildResourceTree lays out the tree at the given RVA. Offsets inside
// the tree count from the section's start; a data entry's own offset
// is an RVA.
func buildResourceTree(res []resource, rva int) []byte {
	res = append([]resource(nil), res...)
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].typ != res[j].typ {
			return res[i].typ < res[j].typ
		}
		return res[i].id < res[j].id
	})
	var types []int
	perType := map[int][]int{} // type -> indexes into res
	for i, r := range res {
		if len(perType[r.typ]) == 0 {
			types = append(types, r.typ)
		}
		perType[r.typ] = append(perType[r.typ], i)
	}

	dir := func(entries int) int { return 16 + 8*entries }
	// Root, then a name directory per type, then a language directory
	// per resource, then the data entries, then the bytes.
	at := dir(len(types))
	namesAt := map[int]int{}
	for _, t := range types {
		namesAt[t] = at
		at += dir(len(perType[t]))
	}
	langAt := make([]int, len(res))
	for i := range res {
		langAt[i] = at
		at += dir(1)
	}
	entriesAt := at
	at += 16 * len(res)
	dataAt := make([]int, len(res))
	for i, r := range res {
		at = alignUp(at, 8)
		dataAt[i] = at
		at += len(r.data)
	}
	out := make([]byte, alignUp(at, 8))
	le := binary.LittleEndian

	putDir := func(o, entries int) {
		le.PutUint16(out[o+14:], uint16(entries)) // all by ID, none by name
	}
	putEntry := func(dirAt, i, id, target int, sub bool) {
		e := dirAt + 16 + 8*i
		le.PutUint32(out[e:], uint32(id))
		v := uint32(target)
		if sub {
			v |= 0x80000000
		}
		le.PutUint32(out[e+4:], v)
	}

	putDir(0, len(types))
	for ti, t := range types {
		putEntry(0, ti, t, namesAt[t], true)
		putDir(namesAt[t], len(perType[t]))
		for k, i := range perType[t] {
			putEntry(namesAt[t], k, res[i].id, langAt[i], true)
		}
	}
	for i, r := range res {
		putDir(langAt[i], 1)
		putEntry(langAt[i], 0, r.lang, entriesAt+16*i, false)
		d := entriesAt + 16*i
		le.PutUint32(out[d:], uint32(rva+dataAt[i]))
		le.PutUint32(out[d+4:], uint32(len(r.data)))
		copy(out[dataAt[i]:], r.data)
	}
	return out
}

// versionInfo is what Properties, Details shows for an executable.
type versionInfo struct {
	fileName    string // the executable's own name, a.exe
	description string
	product     string
	company     string
	copyright   string
	version     [4]uint16
	dll         bool
}

// parseVersion reads "1", "1.2", "1.2.3" or "1.2.3.4".
func parseVersion(s string) ([4]uint16, error) {
	var v [4]uint16
	parts := strings.Split(s, ".")
	if len(parts) > 4 || s == "" {
		return v, fmt.Errorf("%q is not a version like 1.2.3", s)
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return v, fmt.Errorf("%q is not a version like 1.2.3", s)
		}
		v[i] = uint16(n)
	}
	return v, nil
}

// versionResource builds VS_VERSIONINFO: a fixed block of numbers, then
// the strings, in UTF-16, every block a length, a value length, a type
// and a key, each part aligned to four bytes.
func versionResource(vi versionInfo) resource {
	le := binary.LittleEndian
	var block func(key string, value []byte, text bool, children [][]byte) []byte
	block = func(key string, value []byte, text bool, children [][]byte) []byte {
		var b []byte
		b = append(b, 0, 0, 0, 0, 0, 0) // length, value length, type
		b = append(b, utf16z(key)...)
		b = pad4(b)
		b = append(b, value...)
		b = pad4(b)
		for _, c := range children {
			b = append(pad4(b), c...)
		}
		le.PutUint16(b[0:], uint16(len(b)))
		vlen := len(value)
		if text {
			vlen /= 2 // a string's value length counts characters
			le.PutUint16(b[4:], 1)
		}
		le.PutUint16(b[2:], uint16(vlen))
		return b
	}

	v := vi.version
	ms := uint32(v[0])<<16 | uint32(v[1])
	ls := uint32(v[2])<<16 | uint32(v[3])
	fixed := make([]byte, 52)
	le.PutUint32(fixed[0:], 0xFEEF04BD)
	le.PutUint32(fixed[4:], 0x00010000)
	le.PutUint32(fixed[8:], ms)
	le.PutUint32(fixed[12:], ls)
	le.PutUint32(fixed[16:], ms)
	le.PutUint32(fixed[20:], ls)
	le.PutUint32(fixed[24:], 0x3F)    // which flags are meaningful
	le.PutUint32(fixed[32:], 0x40004) // VOS_NT_WINDOWS32
	le.PutUint32(fixed[36:], 1)       // VFT_APP
	if vi.dll {
		le.PutUint32(fixed[36:], 2) // VFT_DLL
	}

	ver := fmt.Sprintf("%d.%d.%d.%d", v[0], v[1], v[2], v[3])
	name := strings.TrimSuffix(vi.fileName, ".exe")
	name = strings.TrimSuffix(name, ".dll")
	pairs := [][2]string{
		{"CompanyName", vi.company},
		{"FileDescription", vi.description},
		{"FileVersion", ver},
		{"InternalName", name},
		{"LegalCopyright", vi.copyright},
		{"OriginalFilename", vi.fileName},
		{"ProductName", vi.product},
		{"ProductVersion", ver},
		{"Comments", "Built with Veyl " + Version},
	}
	var strs [][]byte
	for _, kv := range pairs {
		if kv[1] == "" {
			continue
		}
		strs = append(strs, block(kv[0], utf16z(kv[1]), true, nil))
	}
	table := block("040904B0", nil, true, strs) // US English, Unicode
	stringInfo := block("StringFileInfo", nil, true, [][]byte{table})
	trans := make([]byte, 4)
	le.PutUint16(trans[0:], 0x0409)
	le.PutUint16(trans[2:], 1200)
	varInfo := block("VarFileInfo", nil, true, [][]byte{block("Translation", trans, false, nil)})
	return resource{rtVersion, 1, langEnUS, block("VS_VERSION_INFO", fixed, false, [][]byte{stringInfo, varInfo})}
}

// manifestResource asks for no elevation: the program runs with the
// rights of whoever starts it.
func manifestResource() resource {
	m := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
</assembly>
`
	return resource{rtManifest, 1, langEnUS, []byte(strings.ReplaceAll(m, "\n", "\r\n"))}
}

func utf16z(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u)+2)
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}
