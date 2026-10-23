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
	"regexp"
	"strconv"
	"strings"
)

var (
	peepStore = regexp.MustCompile(`^    mov qword ptr \[rbp-(\d+)\], rax$`)
	peepLoad  = regexp.MustCompile(`^    mov rax, qword ptr \[rbp-(\d+)\]$`)
	peepImm   = regexp.MustCompile(`^    mov rax, (\d+)$`)

	// The same for a register: mov r8, rax then mov rax, r8.
	peepToReg   = regexp.MustCompile(`^    mov (r8|r9|r10|r11|rbx|rsi|rdi|r12|r13|r14|r15), rax$`)
	peepFromReg = regexp.MustCompile(`^    mov rax, (r8|r9|r10|r11|rbx|rsi|rdi|r12|r13|r14|r15)$`)
)

func peephole(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))

	stored := "" // the slot the last kept line stored to, if any
	for _, ln := range lines {
		if m := peepLoad.FindStringSubmatch(ln); m != nil && stored == m[1] {
			continue // the value is in rax already
		}
		if m := peepFromReg.FindStringSubmatch(ln); m != nil && stored == m[1] {
			continue
		}

		if m := peepImm.FindStringSubmatch(ln); m != nil {
			if v, err := strconv.Atoi(m[1]); err == nil && v <= 0x7fffffff {
				ln = "    mov eax, " + m[1]
			}
		}

		if dupableAsm(ln) && len(out) > 0 && out[len(out)-1] == ln {
			continue
		}

		out = append(out, ln)
		if m := peepStore.FindStringSubmatch(ln); m != nil {
			stored = m[1]
		} else if m := peepToReg.FindStringSubmatch(ln); m != nil {
			stored = m[1]
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
		for _, tok := range asmWord.FindAllString(src, -1) {
			if regFamily(tok) == family {
				return false
			}
		}
		return true
	}
	return false
}

var asmWord = regexp.MustCompile(`[a-z][a-z0-9]*`)

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
