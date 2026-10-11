package main

// The .rsrc section, for `veyl build --icon game.ico`: what Explorer,
// the taskbar and a shortcut show for the executable.
//
// An .ico file is a header, one 16-byte entry per image, and the images.
// An executable keeps the same images as separate RT_ICON resources and
// an RT_GROUP_ICON that lists them - the same entries with the file
// offset replaced by the resource ID. Both hang off the three-level
// resource tree every PE uses: type, then name or ID, then language.

import (
	"encoding/binary"
	"fmt"
)

const (
	rtIcon      = 3
	rtGroupIcon = 14
	langNeutral = 0
)

type icoImage struct {
	entry []byte // the 16-byte directory entry from the file
	data  []byte
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

// buildResources lays out the resource tree for one icon group at the
// given RVA. Offsets inside the tree count from the section's start;
// a data entry's own offset is an RVA.
func buildResources(imgs []icoImage, rva int) []byte {
	n := len(imgs)
	le := binary.LittleEndian

	// The group: an .ico header, then each entry with its last field,
	// the 4-byte file offset, cut to a 2-byte resource ID.
	group := make([]byte, 6+14*n)
	le.PutUint16(group[2:], 1)
	le.PutUint16(group[4:], uint16(n))
	for i, im := range imgs {
		g := group[6+14*i:]
		copy(g[:12], im.entry[:12])
		le.PutUint16(g[12:], uint16(i+1))
	}

	dir := func(entries int) int { return 16 + 8*entries }
	// Root (2 types), then per type a name directory and per name a
	// language directory, then the data entries, then the bytes.
	rootAt := 0
	iconNamesAt := rootAt + dir(2)
	groupNamesAt := iconNamesAt + dir(n)
	iconLangsAt := groupNamesAt + dir(1)
	groupLangAt := iconLangsAt + n*dir(1)
	dataEntriesAt := groupLangAt + dir(1)
	bytesAt := dataEntriesAt + 16*(n+1)

	// Where each blob of bytes goes, kept 8-aligned.
	at := bytesAt
	offs := make([]int, n+1)
	for i, im := range imgs {
		offs[i] = at
		at = alignUp(at+len(im.data), 8)
	}
	offs[n] = at
	at += len(group)
	out := make([]byte, at)

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

	putDir(rootAt, 2)
	putEntry(rootAt, 0, rtIcon, iconNamesAt, true) // IDs in ascending order
	putEntry(rootAt, 1, rtGroupIcon, groupNamesAt, true)

	putDir(iconNamesAt, n)
	for i := range imgs {
		lang := iconLangsAt + i*dir(1)
		putEntry(iconNamesAt, i, i+1, lang, true)
		putDir(lang, 1)
		putEntry(lang, 0, langNeutral, dataEntriesAt+16*i, false)
	}
	putDir(groupNamesAt, 1)
	putEntry(groupNamesAt, 0, 1, groupLangAt, true)
	putDir(groupLangAt, 1)
	putEntry(groupLangAt, 0, langNeutral, dataEntriesAt+16*n, false)

	for i := 0; i <= n; i++ {
		d := dataEntriesAt + 16*i
		size := len(group)
		if i < n {
			size = len(imgs[i].data)
			copy(out[offs[i]:], imgs[i].data)
		} else {
			copy(out[offs[n]:], group)
		}
		le.PutUint32(out[d:], uint32(rva+offs[i]))
		le.PutUint32(out[d+4:], uint32(size))
	}
	return out
}
