package frontend

// Undefined names.
//
// The checker types every expression, so it is also where a name that
// means nothing is first noticed. Reporting it here rather than leaving
// it to the backend puts it in the same list as the type errors, instead
// of only after they are all fixed.
//
// A misspelling is the usual reason, so the message offers the closest
// name that does exist, when one is close enough to be a likely slip:
// one edit for a short name, two for a longer one.

import (
	"sort"
	"strings"
)

// LibraryNames is implemented by a Library that can list its builtins,
// which lets an undefined call suggest one of them.
type LibraryNames interface {
	Names() []string
}

// undefinedName reports name as not existing. what is "variable",
// "function" or "struct".
func (c *Checker) undefinedName(n Node, name, what string) {
	// The two ways a name that does exist is still out of reach, both a
	// let in the program body: it runs after every const and var is
	// computed, and no function can see into it.
	if what == "variable" && c.mainLet(name) {
		switch {
		case c.curGlob != nil:
			c.ErrorAt(n, "%q is a let in the program body, which runs after every const and var "+
				"is computed - make it a const or a var too", name)
			return
		case c.curFn != nil:
			c.ErrorAt(n, "%q belongs to the program body, so functions cannot see it - "+
				"declare it with var instead of let to share it", name)
			return
		}
	}
	if near := c.nearest(name, what); near != "" {
		c.ErrorAt(n, "undefined %s %q - did you mean %q?", what, name, near)
		return
	}
	c.ErrorAt(n, "undefined %s %q", what, name)
}

// mainLet reports whether the program body declares name with let.
func (c *Checker) mainLet(name string) bool {
	for _, st := range c.prog.Main {
		if let, ok := st.(*LetStmt); ok && !let.Const && let.Name == name {
			return true
		}
	}
	return false
}

// nearest is the defined name closest to name, or "" when none is close.
func (c *Checker) nearest(name, what string) string {
	var cands []string
	switch what {
	case "struct":
		for s, d := range c.structs {
			if d.Enum == nil && !strings.HasPrefix(s, "__") {
				cands = append(cands, s)
			}
		}
	default:
		for i := len(c.scopes) - 1; i >= 0; i-- {
			for v := range c.scopes[i] {
				cands = append(cands, v)
			}
		}
		for g := range c.globals {
			cands = append(cands, g)
		}
		for f := range c.funcs {
			cands = append(cands, f)
		}
		if ln, ok := c.lib.(LibraryNames); ok {
			cands = append(cands, ln.Names()...)
		}
	}
	limit := 1
	if len(name) > 5 {
		limit = 2
	}
	best, bestD := "", limit+1
	sort.Strings(cands)
	for _, s := range cands {
		if s == name || strings.HasPrefix(s, "__") || strings.Contains(s, "<") {
			continue
		}
		if d := editDistance(strings.ToLower(name), strings.ToLower(s)); d < bestD {
			best, bestD = s, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance, with a swap of two
// neighbouring letters counted as one edit, since that is the commonest
// typing slip.
func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(b)]
}
