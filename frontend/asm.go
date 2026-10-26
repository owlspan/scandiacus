package frontend

// Inline assembly: what the checker can say about an asm statement
// before the backend sees the instructions.
//
// Values go in and out through registers the program names, so nothing
// here has to know how the backend keeps a variable. An input is
// anything that fits a register: an integer, bool or pointer in a
// general register, a float in an xmm one, and an array as the address
// of its first element, which is how asm gets at a block of numbers.
// An output is a variable of one of those scalar types. The instructions
// themselves are checked by the backend's encoder, the one thing that
// knows which it can write.

import (
	"strconv"
	"strings"
)

// asmGP are the general registers an operand may name: all but rsp and
// rbp, which hold the frame the asm runs in.
var asmGP = map[string]bool{
	"rax": true, "rbx": true, "rcx": true, "rdx": true, "rsi": true, "rdi": true,
	"r8": true, "r9": true, "r10": true, "r11": true, "r12": true, "r13": true,
	"r14": true, "r15": true,
}

func asmXmm(reg string) bool {
	n, err := strconv.Atoi(strings.TrimPrefix(reg, "xmm"))
	return strings.HasPrefix(reg, "xmm") && err == nil && n >= 0 && n <= 15
}

// asmFile is the register file a value of type t travels in: "gp",
// "xmm", or "" when it cannot go through a register at all.
func asmFile(t *Type, output bool) string {
	switch {
	case t.IsFloatLike():
		return "xmm"
	case t.IsInteger(), t.Kind == KBool, t.Kind == KPtr:
		return "gp"
	case t.Kind == KArray && !output:
		return "gp"
	}
	return ""
}

func (c *Checker) asm(st *AsmStmt) {
	used := map[string]bool{}
	for i := range st.In {
		op := &st.In[i]
		op.T = c.expr(op.X)
		if used[op.Reg] {
			c.ErrorAt(op, "%s is given two values going in", op.Reg)
		}
		used[op.Reg] = true
		c.asmOperand(op, false)
	}
	used = map[string]bool{}
	names := map[string]bool{}
	for i := range st.Out {
		op := &st.Out[i]
		id := op.X.(*Ident)
		if c.isConst(id.Name) {
			c.ErrorAt(op, "cannot assign to %q because it was declared const", id.Name)
			continue
		}
		// Writing a name is not reading it, as with x = 5.
		keep := c.useOf(id.Name)
		wasUsed := keep != nil && keep.used
		op.T = c.expr(id)
		if keep != nil {
			keep.used = wasUsed
		}
		if used[op.Reg] {
			c.ErrorAt(op, "%s goes to two variables", op.Reg)
		}
		if names[id.Name] {
			c.ErrorAt(op, "%s is written by two registers", id.Name)
		}
		used[op.Reg], names[id.Name] = true, true
		c.asmOperand(op, true)
	}
	for _, ln := range st.Lines {
		if msg := asmLineProblem(ln.Text); msg != "" {
			c.ErrorAt(ln, "%s", msg)
		}
	}
}

func (c *Checker) asmOperand(op *AsmOperand, output bool) {
	if op.T.IsUnknown() {
		return
	}
	file := ""
	switch {
	case asmGP[op.Reg]:
		file = "gp"
	case asmXmm(op.Reg):
		file = "xmm"
	case op.Reg == "rsp" || op.Reg == "rbp":
		c.ErrorAt(op, "%s holds the frame the asm runs in and cannot be an operand", op.Reg)
		return
	default:
		c.ErrorAt(op, "%q is not a register an operand can use - name a 64-bit general "+
			"register such as rax or r8, or xmm0 to xmm15", op.Reg)
		return
	}
	want := asmFile(op.T, output)
	switch {
	case want == "" && output:
		c.ErrorAt(op, "an asm output must be an integer, bool, float or pointer variable, not %s", op.T)
	case want == "":
		c.ErrorAt(op, "an asm input must be an integer, bool, float, pointer or array, not %s", op.T)
	case want != file && want == "xmm":
		c.ErrorAt(op, "%s is %s, which goes in an xmm register, not %s", exprText(op.X), op.T, op.Reg)
	case want != file:
		c.ErrorAt(op, "%s is %s, which goes in a general register, not %s", exprText(op.X), op.T, op.Reg)
	}
}

// asmLineProblem is what is wrong with a line before any instruction is
// looked at, or "". These are the things the two assemblers behind the
// backend would read differently: GNU as takes ; as a second statement
// and # as a comment, and runs any directive, where the encoder sees
// one instruction per line.
func asmLineProblem(text string) string {
	s := strings.TrimSpace(text)
	switch {
	case strings.ContainsAny(s, ";#"):
		return "an asm line is one instruction, with no ; or # in it - put a comment outside the string"
	case strings.HasSuffix(s, ":"):
		name := strings.TrimSuffix(s, ":")
		if !asmLabelName(name) {
			return "a label is a name made of letters, digits and _, as in loop:"
		}
	case strings.HasPrefix(s, "."):
		if !strings.HasPrefix(s, ".byte ") {
			return "the only directive asm takes is .byte, for an instruction written as its bytes"
		}
		for _, part := range strings.Split(s[len(".byte "):], ",") {
			n, err := strconv.ParseUint(strings.TrimSpace(part), 0, 8)
			if err != nil || n > 255 {
				return ".byte takes numbers from 0 to 255, separated by commas"
			}
		}
	case s == "ret" || strings.HasPrefix(s, "ret ") || s == "leave":
		return "asm cannot return - the code after the block does that, once the outputs are written"
	}
	return ""
}

func asmLabelName(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
