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
