package frontend

// Warnings: things that are allowed but are almost always a mistake.
//
//	x.vl:4:5: warning: "total" is declared but never used
//	x.vl:9:5: warning: this can never run - it comes after a return
//
// They do not stop a build. veyl build and veyl check print them, and
// the language server shows them as warnings rather than errors. A name
// that starts with an underscore is exempt from the first, which is the
// way to say a value is kept on purpose.

import "fmt"

type localUse struct {
	node Node
	name string
	file string
	used bool
}

// WarnAt records a warning, in the same form as an error.
func (c *Checker) WarnAt(n Node, format string, args ...any) {
	if c.inLibrary() {
		return
	}
	line, col := n.Pos()
	c.Warnings = append(c.Warnings, fmt.Sprintf("%s:%d:%d: %s", c.useFile(), line, col, fmt.Sprintf(format, args...)))
}

// inLibrary reports whether the code being checked is the compiler's
// own - the prelude and the library written in Veyl - which has no file.
func (c *Checker) inLibrary() bool {
	return c.curFn != nil && c.curFn.File == "" && c.instFile == ""
}

// declareLocal notes a let, to warn at the end of its scope if nothing
// read it.
func (c *Checker) declareLocal(st *LetStmt) {
	if len(c.uses) == 0 || st.Global || st.Name == "" || st.Name[0] == '_' || st.Name[0] == '#' || c.inLibrary() {
		return
	}
	c.uses[len(c.uses)-1][st.Name] = &localUse{node: st, name: st.Name, file: c.useFile()}
}

// markUsed records a read of a name, against the innermost declaration.
func (c *Checker) markUsed(name string) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if _, ok := c.scopes[i][name]; ok {
			if i < len(c.uses) {
				if u := c.uses[i][name]; u != nil {
					u.used = true
				}
			}
			return
		}
	}
}

// useOf is the record for the innermost declaration of a name, or nil.
func (c *Checker) useOf(name string) *localUse {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if _, ok := c.scopes[i][name]; ok {
			if i < len(c.uses) {
				return c.uses[i][name]
			}
			return nil
		}
	}
	return nil
}

// reportUnused warns about the lets of the scope being closed.
func (c *Checker) reportUnused() {
	if len(c.uses) == 0 {
		return
	}
	for _, u := range c.uses[len(c.uses)-1] {
		if !u.used {
			line, col := u.node.Pos()
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s:%d:%d: %q is declared but never used", u.file, line, col, u.name))
		}
	}
}

// unreachable warns about a statement that follows one control never
// comes back from, once per block.
func (c *Checker) unreachable(list []Stmt) {
	for i, s := range list[:max(0, len(list)-1)] {
		why := ""
		switch s.(type) {
		case *ReturnStmt:
			why = "a return"
		case *BreakStmt:
			why = "a break"
		case *ContinueStmt:
			why = "a continue"
		}
		if why != "" {
			c.WarnAt(list[i+1], "this can never run - it comes after %s", why)
			return
		}
	}
}
