package main

// The linker: assembly text to a laid-out image.
//
// This is the second half of removing MinGW. encode.go turns one line of
// assembly into bytes; this turns a whole file into sections, gives
// every label an address, and fills in every reference. pe.go then
// writes the result out in the format Windows loads.
//
// The directive set it has to understand is small and closed, because
// x64.go writes it: .intel_syntax, .global, .extern, .section, .text,
// .align, .space, .asciz and .quad. Anything else is a compiler bug
// rather than something to guess at, so it is an error.
//
// Externs are the interesting part. A call to strlen cannot be a direct
// call to an address this compiler knows, because the address is only
// known once Windows has mapped msvcrt.dll. What a linker does, and what
// happens here, is to put a six-byte thunk in the text -
//
//     strlen: jmp qword ptr [rip + __iat_strlen]
//
// - so the call site stays an ordinary direct call and the one indirect
// jump reads the slot the loader wrote. Those slots are the import
// address table, which pe.go builds.

import (
	"fmt"

	"strconv"
	"strings"
)

type secID int

const (
	secText secID = iota
	secRdata
	secData // writable initialised data, only ever from linked object files
	secBss
	numSections
)

// A symbol is a label: which section it is in and how far into it.
type symbol struct {
	sec secID
	off int
}

// An object is everything the assembly said, with nothing resolved yet.
type object struct {
	text    []byte
	rdata   []byte
	data    []byte // writable initialised bytes, only from linked objects
	bssLen  int
	sym     map[string]symbol
	relocs  []reloc  // into any section; see reloc.sec
	externs []string // sorted, so two builds agree
}

// assembleObject reads the generated assembly and produces an object.
//
// static names the externs that a linked static library will define. They
// still appear as `.extern` in the assembly and as ordinary calls, but no
// import thunk is written for them: linkStatic defines the symbol from the
// library's own code instead, so the call reaches it directly.
func assembleObject(text string, static map[string]bool) (*object, error) {
	obj := &object{sym: map[string]symbol{}}
	b := &block{at: map[string]int{}}
	extern := map[string]bool{}

	cur := secText
	for n, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}

		if strings.HasSuffix(line, ":") {
			name := strings.TrimSuffix(line, ":")
			switch cur {
			case secText:
				b.at[name] = len(b.items)
			case secRdata:
				obj.sym[name] = symbol{secRdata, len(obj.rdata)}
			case secBss:
				obj.sym[name] = symbol{secBss, obj.bssLen}
			}
			continue
		}

		if strings.HasPrefix(line, ".") {
			next, err := obj.directive(line, cur, extern)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n+1, err)
			}
			cur = next
			continue
		}

		if cur != secText {
			return nil, fmt.Errorf("line %d: an instruction outside .text: %s", n+1, line)
		}
		if err := b.encodeLine(line); err != nil {
			return nil, fmt.Errorf("line %d: %w", n+1, err)
		}
	}

	// An extern a static library defines is not imported: drop it here so
	// no thunk and no import-table entry is made for it. linkStatic gives
	// the name a real definition from the library's object code.
	for name := range extern {
		if static[name] {
			delete(extern, name)
		}
	}
	obj.externs = sortedKeys(extern)

	// One thunk per extern, at the end of the text, labelled with the
	// extern's own name so that every call site already points at it.
	for _, name := range obj.externs {
		if _, taken := b.at[name]; taken {
			return nil, fmt.Errorf("%s is both defined here and imported", name)
		}
		b.at[name] = len(b.items)
		b.items = append(b.items, item{
			bytes: []byte{0xFF, 0x25, 0, 0, 0, 0}, // jmp qword ptr [rip+d32]
			rel:   []reloc{{at: 2, sym: iatSym(name), next: 6}},
		})
	}

	b.settle()
	obj.text = b.code
	obj.relocs = b.relocs
	for name, off := range b.labels {
		obj.sym[name] = symbol{secText, off}
	}
	return obj, nil
}

func iatSym(name string) string { return "__iat_" + name }

// directive handles one line beginning with a dot, and reports which
// section the lines after it belong to.
func (obj *object) directive(line string, cur secID, extern map[string]bool) (secID, error) {
	name := line
	arg := ""
	if sp := strings.IndexByte(line, ' '); sp >= 0 {
		name = line[:sp]
		arg = strings.TrimSpace(line[sp+1:])
	}

	switch name {
	case ".intel_syntax", ".global", ".globl":
		return cur, nil

	case ".extern":
		extern[arg] = true
		return cur, nil

	case ".text":
		return secText, nil

	case ".section":
		switch arg {
		case ".text":
			return secText, nil
		case ".rdata", ".rodata":
			return secRdata, nil
		case ".bss":
			return secBss, nil
		}
		return cur, fmt.Errorf("unknown section %q", arg)

	case ".align", ".p2align":
		n, err := strconv.Atoi(arg)
		if err != nil || n <= 0 {
			return cur, fmt.Errorf("bad alignment %q", arg)
		}
		obj.pad(cur, (n-obj.size(cur)%n)%n)
		return cur, nil

	case ".space", ".zero":
		n, err := strconv.Atoi(arg)
		if err != nil || n < 0 {
			return cur, fmt.Errorf("bad size %q", arg)
		}
		obj.pad(cur, n)
		return cur, nil

	case ".quad":
		v, err := strconv.ParseUint(arg, 0, 64)
		if err != nil {
			// A negative literal is still eight bytes of the same shape.
			s, serr := strconv.ParseInt(arg, 0, 64)
			if serr != nil {
				return cur, fmt.Errorf("bad .quad %q", arg)
			}
			v = uint64(s)
		}
		var buf [8]byte
		for i := 0; i < 8; i++ {
			buf[i] = byte(v >> (8 * i))
		}
		return cur, obj.emit(cur, buf[:])

	case ".asciz", ".string":
		s, err := unescapeAsm(arg)
		if err != nil {
			return cur, err
		}
		return cur, obj.emit(cur, append([]byte(s), 0))
	}

	return cur, fmt.Errorf("unhandled directive %q", name)
}

func (obj *object) size(sec secID) int {
	switch sec {
	case secRdata:
		return len(obj.rdata)
	case secData:
		return len(obj.data)
	case secBss:
		return obj.bssLen
	}
	return len(obj.text)
}

func (obj *object) pad(sec secID, n int) {
	if sec == secBss {
		obj.bssLen += n
		return
	}
	obj.emit(sec, make([]byte, n))
}

// emit appends initialised bytes. .bss holds none by definition, so a
// directive that would put a value there is a bug in x64.go.
func (obj *object) emit(sec secID, bs []byte) error {
	switch sec {
	case secRdata:
		obj.rdata = append(obj.rdata, bs...)
		return nil
	case secData:
		obj.data = append(obj.data, bs...)
		return nil
	case secBss:
		return fmt.Errorf(".bss cannot hold initialised bytes")
	}
	return fmt.Errorf("data outside a data section")
}

// unescapeAsm is the inverse of escapeAsm in x64.go. The two have to
// agree exactly: this is how every string literal in a program gets its
// bytes, so a disagreement is silently wrong output rather than an error.
func unescapeAsm(arg string) (string, error) {
	if len(arg) < 2 || arg[0] != '"' || arg[len(arg)-1] != '"' {
		return "", fmt.Errorf("a string operand must be quoted, got %q", arg)
	}
	in := arg[1 : len(arg)-1]

	var out []byte
	for i := 0; i < len(in); i++ {
		if in[i] != '\\' {
			out = append(out, in[i])
			continue
		}
		i++
		if i >= len(in) {
			return "", fmt.Errorf("a string ends in a backslash")
		}
		switch c := in[i]; c {
		case '\\', '"':
			out = append(out, c)
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case '0', '1', '2', '3', '4', '5', '6', '7':
			// Up to three octal digits, which is what escapeAsm writes.
			v := 0
			j := 0
			for ; j < 3 && i+j < len(in) && in[i+j] >= '0' && in[i+j] <= '7'; j++ {
				v = v*8 + int(in[i+j]-'0')
			}
			i += j - 1
			out = append(out, byte(v))
		default:
			return "", fmt.Errorf("unknown escape \\%c", c)
		}
	}
	return string(out), nil
}

// stripComment removes a trailing assembler comment.
//
// It has to know about quotes. A Veyl string literal reaches the
// assembly inside .asciz, and a program printing a "#" would otherwise
// have the rest of its own text deleted here - wrong output rather than
// an error, and only in programs that happen to contain that character.
func stripComment(line string) string {
	inString := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if inString {
				i++
			}
		case '"':
			inString = !inString
		case '#':
			if !inString {
				return line[:i]
			}
		}
	}
	return line
}
