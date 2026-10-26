package main

import "testing"

func TestInlineStride(t *testing.T) {
	cases := []struct {
		name   string
		lay    structLayout
		stride int64
		words  int64
		ok     bool
	}{
		{"empty", structLayout{}, 8, 0, true},
		{"four words", structLayout{bytes: 32, nptr: 1}, 32, 4, true},
		{"odd words", structLayout{bytes: 24}, 24, 3, true},
		{"extern view", structLayout{bytes: 16, view: true}, 16, 2, false},
		{"too many pointers", structLayout{bytes: 256 * 8, nptr: 256}, 256 * 8, 256, false},
		{"stride too wide", structLayout{bytes: 65536 * 8, nptr: 1}, 65536 * 8, 65536, false},
		{"widest stride", structLayout{bytes: 65535 * 8, nptr: 255}, 65535 * 8, 65535, true},
	}
	for _, c := range cases {
		if got := c.lay.stride(); got != c.stride {
			t.Errorf("%s: stride %d, want %d", c.name, got, c.stride)
		}
		if got := c.lay.words(); got != c.words {
			t.Errorf("%s: words %d, want %d", c.name, got, c.words)
		}
		if got := c.lay.inlineOK(); got != c.ok {
			t.Errorf("%s: inlineOK %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestStructsHeaderRoundTrip(t *testing.T) {
	cases := [][3]int64{{0, 1, 8}, {1, 4, 32 * 1000}, {255, 65535, 1 << 31}, {3, 5, 1<<32 - 8}}
	for _, c := range cases {
		h := structsHeader(c[0], c[1], c[2])
		if h&0xFF != tagStructs {
			t.Errorf("%v: tag %d", c, h&0xFF)
		}
		n, s, b := decodeStructsHeader(h)
		if n != c[0] || s != c[1] || b != c[2] {
			t.Errorf("%v: decoded %d %d %d", c, n, s, b)
		}
	}
}

// The existing decoders read the size from bit 8 or bit 16 and would
// misread an inline header, so the tag has to be distinct from every
// tag they handle.
func TestTagStructsIsDistinct(t *testing.T) {
	for _, o := range []int64{tagBytes, tagWords, tagPtrs, tagList, tagMap, tagRes, tagResPtr, tagStruct} {
		if o == tagStructs {
			t.Fatalf("tagStructs collides with tag %d", o)
		}
	}
}
