package main

// The assembly peephole: three tiny rewrites over the text Emit
// produced, applied before either linking route sees it, so the byte
// encoder and GNU as stay comparable references for each other.
//
//   - A store to a frame slot immediately followed by reloading that
//     same slot into rax loses the reload. The store already left the
//     value there.
//   - A line identical to the line before it loses its copy. Data
//     movement only: two calls in a row are two calls, and a repeated
//     compare would tempt rewrites that depend on flags.
//   - mov rax, small positive becomes mov eax, which encodes five
//     bytes shorter. Zeroing the high half is what the wide form was
//     already holding, and mov writes no flags, so compares and jumps
//     around it are untouched.
//
// Nothing here thinks about the program. The IR passes do that; this
// file sweeps up the shapes their output leaves behind.

import (
	"strconv"
	"strings"
)

// The line shapes the pass looks for, matched with plain string tests:
// it reads every line of the program, and regular expressions made it
// a good part of the time a build took.
var peepRegs = map[string]bool{"r8": true, "r9": true, "r10": true, "r11": true, "rbx": true,
	"rsi": true, "rdi": true, "r12": true, "r13": true, "r14": true, "r15": true}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// between returns what lies between prefix and suffix in ln, if ln has
// both.
func between(ln, prefix, suffix string) (string, bool) {
	if !strings.HasPrefix(ln, prefix) || !strings.HasSuffix(ln, suffix) || len(ln) < len(prefix)+len(suffix) {
		return "", false
	}
	return ln[len(prefix) : len(ln)-len(suffix)], true
}

// peepLoad: mov rax, qword ptr [rbp-N], or [rsp+N] in a function with
// no frame pointer. The base stays in the answer so the two never match
// each other.
func peepLoad(ln string) (string, bool) {
	if m, ok := between(ln, "    mov rax, qword ptr [rbp-", "]"); ok && allDigits(m) {
		return "rbp-" + m, true
	}
	m, ok := between(ln, "    mov rax, qword ptr [rsp+", "]")
	return "rsp+" + m, ok && allDigits(m)
}

// peepStore: mov qword ptr [rbp-N], rax, or [rsp+N]
func peepStore(ln string) (string, bool) {
	if m, ok := between(ln, "    mov qword ptr [rbp-", "], rax"); ok && allDigits(m) {
		return "rbp-" + m, true
	}
	m, ok := between(ln, "    mov qword ptr [rsp+", "], rax")
	return "rsp+" + m, ok && allDigits(m)
}

// peepImm: mov rax, N
func peepImm(ln string) (string, bool) {
	m, ok := between(ln, "    mov rax, ", "")
	return m, ok && allDigits(m)
}

// peepFromReg: mov rax, REG
func peepFromReg(ln string) (string, bool) {
	m, ok := between(ln, "    mov rax, ", "")
	return m, ok && peepRegs[m]
}

// peepToReg: mov REG, rax
func peepToReg(ln string) (string, bool) {
	m, ok := between(ln, "    mov ", ", rax")
	return m, ok && peepRegs[m]
}

func peephole(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))

	stored := "" // the slot the last kept line stored to, if any
	for _, ln := range lines {
		if stored != "" {
			if m, ok := peepLoad(ln); ok && stored == m {
				continue // the value is in rax already
			}
			if m, ok := peepFromReg(ln); ok && stored == m {
				continue
			}
		}

		if m, ok := peepImm(ln); ok {
			if v, err := strconv.Atoi(m); err == nil && v <= 0x7fffffff {
				ln = "    mov eax, " + m
			}
		}

		// movsd between registers writes only the low half and keeps the
		// rest of the destination, so it waits for whatever last wrote
		// that register. In a loop that chains each trip to the one
		// before: a float moved through the register the previous trip
		// left its last result in. movapd copies the whole register and
		// has no such input.
		if m, ok := between(ln, "    movsd xmm", ""); ok {
			if a, b, two := strings.Cut(m, ", xmm"); two && allDigits(a) && allDigits(b) {
				ln = "    movapd xmm" + m
			}
		}

		if len(out) > 0 && out[len(out)-1] == ln && dupableAsm(ln) {
			continue
		}

		out = append(out, ln)
		if m, ok := peepStore(ln); ok {
			stored = m
		} else if m, ok := peepToReg(ln); ok {
			stored = m
		} else {
			stored = ""
		}
	}
	return strings.Join(out, "\n")
}

// dupableAsm reports whether running a line twice back to back does
// what running it once does. Pure data movement qualifies; anything
// with an effect or a flag reading does not.
//
// Only when the destination is not also read: mov r11, [r11] twice is a
// load through a pointer and then through what that pointed to, which
// the register allocator produces once a value may take over the
// register of the operand that dies making it.
func dupableAsm(ln string) bool {
	for _, p := range []string{"    mov ", "    lea ", "    movsd "} {
		if !strings.HasPrefix(ln, p) {
			continue
		}
		dst, src, ok := strings.Cut(strings.TrimPrefix(ln, p), ",")
		if !ok {
			return false
		}
		family := regFamily(strings.TrimSpace(dst))
		if family == "" {
			return true // a store to memory: the same bytes written again
		}
		for _, tok := range asmWords(src) {
			if regFamily(tok) == family {
				return false
			}
		}
		return true
	}
	return false
}

// asmWords splits out the words of an operand: a lower-case letter and
// the letters and digits after it.
func asmWords(src string) []string {
	var out []string
	for i := 0; i < len(src); {
		if src[i] < 'a' || src[i] > 'z' {
			i++
			continue
		}
		j := i + 1
		for j < len(src) && (src[j] >= 'a' && src[j] <= 'z' || src[j] >= '0' && src[j] <= '9') {
			j++
		}
		out = append(out, src[i:j])
		i = j
	}
	return out
}

// regFamily names the 64-bit register a register name is part of -
// eax and al are rax, r11d is r11 - or "" for anything that is not a
// general register. An xmm register is its own family.
func regFamily(name string) string {
	switch name {
	case "rax", "eax", "ax", "al":
		return "rax"
	case "rbx", "ebx", "bx", "bl":
		return "rbx"
	case "rcx", "ecx", "cx", "cl":
		return "rcx"
	case "rdx", "edx", "dx", "dl":
		return "rdx"
	case "rsi", "esi", "si", "sil":
		return "rsi"
	case "rdi", "edi", "di", "dil":
		return "rdi"
	case "rsp", "esp", "rbp", "ebp":
		return name[1:]
	}
	if strings.HasPrefix(name, "xmm") {
		return name
	}
	if len(name) >= 2 && name[0] == 'r' && name[1] >= '0' && name[1] <= '9' {
		return strings.TrimRight(name, "dwb")
	}
	return ""
}
