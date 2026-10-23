package frontend

import (
	"fmt"
	"strings"
)

// Lexer turns Scandiacus source text into a flat slice of Tokens.
type Lexer struct {
	src  string
	file string
	Span int // byte offset into src
	line int
	col  int

	tokens []Token
	Errors []string

	// KeepComments makes the lexer emit COMMENT tokens instead of
	// discarding them. Only the formatter wants this: every other stage
	// would have to skip them, and a formatter that deleted every
	// comment in the file would be worse than no formatter.
	KeepComments bool
}

func NewLexer(file, src string) *Lexer {
	// Notepad writes a UTF-8 byte order mark by default, and so do
	// several other Windows editors. It is metadata, not content, but
	// the lexer would see three stray bytes, 0xEF 0xBB 0xBF, and report
	// each as an unexpected character, printed as the Latin-1 letters
	// those bytes happen to be. That is both baffling and unfixable by
	// anyone reading their own file, since the mark is invisible in the
	// editor that wrote it.
	// Written as an escape because Go will not accept a byte order mark
	// sitting in its own source either.
	src = strings.TrimPrefix(src, "\ufeff")
	return &Lexer{src: src, file: file, line: 1, col: 1}
}

// Scan runs the whole lexer and returns the token slice.
// It always ends with an EOF token, even on error.
func (l *Lexer) Scan() []Token {
	for {
		l.skipSpaceAndComments()
		if l.atEnd() {
			break
		}

		line, col := l.line, l.col
		c := l.peek()

		switch {
		case c == '\n':
			l.advance()
			l.emit(NEWLINE, "\n", line, col)
		case IsAlpha(c):
			l.ident(line, col)
		case IsDigit(c):
			l.number(line, col)
		case c == '"':
			l.str(line, col)
		case c == '`':
			l.rawStr(line, col)
		default:
			l.operator(line, col)
		}
	}
	l.emit(EOF, "", l.line, l.col)
	return l.tokens
}

// ---- scanners for each token shape ----

func (l *Lexer) ident(line, col int) {
	start := l.Span
	for !l.atEnd() && (IsAlpha(l.peek()) || IsDigit(l.peek())) {
		l.advance()
	}
	text := l.src[start:l.Span]
	if kind, ok := Keywords[text]; ok {
		l.emit(kind, text, line, col)
		return
	}
	l.emit(IDENT, text, line, col)
}

func (l *Lexer) number(line, col int) {
	start := l.Span

	// 0x and 0b bases. A language with & | ^ << >> in it needs a way to
	// write a mask that looks like one. Go accepts the same spellings,
	// so these pass straight through codegen.
	if l.peek() == '0' && !l.atEnd() {
		switch l.peekNext() {
		case 'x', 'X':
			l.advance()
			l.advance()
			if !IsHexDigit(l.peek()) {
				l.errorf(line, col, "a hex literal needs at least one digit after 0x")
			}
			for !l.atEnd() && (IsHexDigit(l.peek()) || l.peek() == '_') {
				l.advance()
			}
			l.endNumber(start, line, col, "hex")
			return
		case 'b', 'B':
			l.advance()
			l.advance()
			if l.peek() != '0' && l.peek() != '1' {
				l.errorf(line, col, "a binary literal needs at least one 0 or 1 after 0b")
			}
			for !l.atEnd() && (l.peek() == '0' || l.peek() == '1' || l.peek() == '_') {
				l.advance()
			}
			l.endNumber(start, line, col, "binary")
			return
		}
	}

	// Underscores group digits - 1_000_000 - and are ignored by Go too.
	for !l.atEnd() && (IsDigit(l.peek()) || l.peek() == '_') {
		l.advance()
	}
	// a fractional part, but only if a digit actually follows the dot,
	// so that `1.method()` still lexes as NUMBER DOT IDENT later on
	if !l.atEnd() && l.peek() == '.' && IsDigit(l.peekNext()) {
		l.advance() // consume '.'
		for !l.atEnd() && (IsDigit(l.peek()) || l.peek() == '_') {
			l.advance()
		}
	}
	// A scientific exponent: 1e9, 2.5e-3, 6.02E+23. Taken only when a
	// digit really follows, with an optional sign between, so that a
	// bare `1e` is the malformed-literal error below rather than a
	// number silently followed by an identifier.
	if !l.atEnd() && (l.peek() == 'e' || l.peek() == 'E') && l.exponentFollows() {
		l.advance()
		if l.peek() == '+' || l.peek() == '-' {
			l.advance()
		}
		for !l.atEnd() && (IsDigit(l.peek()) || l.peek() == '_') {
			l.advance()
		}
	}
	l.endNumber(start, line, col, "number")
}

// exponentFollows looks past the e and an optional sign for a digit.
//
// Two characters of lookahead, because `1e` and `1e+` are not numbers
// and `1even` is a number beside an identifier in no language anyone
// wants. Without the check, `let e = 2` and `1e` would lex differently
// depending on what came next.
func (l *Lexer) exponentFollows() bool {
	i := l.Span + 1
	if i < len(l.src) && (l.src[i] == '+' || l.src[i] == '-') {
		i++
	}
	return i < len(l.src) && IsDigit(l.src[i])
}

// endNumber emits a numeric literal, refusing one that runs straight
// into a letter or digit.
//
// Without this, `0b1210` quietly lexes as `0b1` followed by `210` - two
// numbers where the author wrote one. A malformed literal should say so
// rather than parse as something else entirely.
func (l *Lexer) endNumber(start, line, col int, base string) {
	if !l.atEnd() && (IsAlpha(l.peek()) || IsDigit(l.peek())) {
		bad := l.peek()
		for !l.atEnd() && (IsAlpha(l.peek()) || IsDigit(l.peek()) || l.peek() == '_') {
			l.advance()
		}
		l.errorf(line, col, "%q is not a %s digit, in %s", string(bad), base, l.src[start:l.Span])
		l.emit(ILLEGAL, l.src[start:l.Span], line, col)
		return
	}
	l.emit(NUMBER, l.src[start:l.Span], line, col)
}

func IsHexDigit(c byte) bool {
	return IsDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// rawStr reads a backtick string: everything up to the closing
// backtick, spanning as many lines as it likes, with no escapes and no
// interpolation. For text that is already full of backslashes and
// braces - a regular expression, a block of CSV, some JSON - quoting it
// twice is the thing that goes wrong.
func (l *Lexer) rawStr(line, col int) {
	rawStart := l.Span
	l.advance() // opening backtick
	start := l.Span

	for {
		if l.atEnd() {
			l.errorf(line, col, "unterminated `...` string")
			l.emit(ILLEGAL, l.src[start:l.Span], line, col)
			return
		}
		if l.peek() == '`' {
			text := l.src[start:l.Span]
			l.advance() // closing backtick
			// Carriage returns are dropped so a file saved on Windows
			// behaves the same as one saved anywhere else.
			l.emitRaw(RAWSTRING, strings.ReplaceAll(text, "\r\n", "\n"),
				l.src[rawStart:l.Span], line, col)
			return
		}
		l.advance()
	}
}

func (l *Lexer) str(line, col int) {
	rawStart := l.Span
	l.advance() // opening quote

	var sb strings.Builder
	depth := 0     // {} nesting inside an interpolation
	inner := false // inside a nested "..." within an interpolation

	for {
		if l.atEnd() || l.peek() == '\n' {
			l.errorf(line, col, "unterminated string literal")
			l.emit(ILLEGAL, sb.String(), line, col)
			return
		}
		c := l.advance()

		// Escapes are handled first so that \" and \\ can never affect
		// the nesting state below.
		if c == '\\' {
			if l.atEnd() {
				l.errorf(l.line, l.col, "unterminated escape sequence")
				l.emit(ILLEGAL, sb.String(), line, col)
				return
			}
			// Inside an interpolation the text belongs to a nested lexer,
			// which will handle its own escapes. Decoding here as well
			// would mean `"{re.find("\\d", s)}"` needed four backslashes
			// to survive both passes, and `\d` - perfectly good inside
			// the inner string - would be rejected by the outer one.
			if depth > 0 {
				sb.WriteByte(c)
				sb.WriteByte(l.advance())
				continue
			}
			eLine, eCol := l.line, l.col
			switch e := l.advance(); e {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '0':
				sb.WriteByte(0)
			case '\\':
				sb.WriteByte('\\')
			case '"':
				sb.WriteByte('"')
			case 'x':
				l.byteEscape(&sb, eLine, eCol)
			case 'u':
				l.runeEscape(&sb, eLine, eCol)
			default:
				l.errorf(eLine, eCol, "unknown escape sequence \\%c", e)
				sb.WriteByte(e)
			}
			continue
		}

		switch {
		case c == '"' && depth == 0:
			// Only a quote at brace-depth zero ends the literal, which is
			// what lets "{f("x")}" nest a string inside an interpolation.
			l.emitRaw(STRING, sb.String(), l.src[rawStart:l.Span], line, col)
			return

		case c == '"':
			inner = !inner

		case inner:
			// plain text inside a nested string; braces here mean nothing

		case c == '{':
			if depth == 0 && l.peek() == '{' {
				sb.WriteByte('{')
				sb.WriteByte(l.advance())
				continue
			}
			depth++

		case c == '}':
			if depth == 0 && l.peek() == '}' {
				sb.WriteByte('}')
				sb.WriteByte(l.advance())
				continue
			}
			if depth > 0 {
				depth--
			}
		}
		sb.WriteByte(c)
	}
}

// byteEscape reads the two hex digits of \xHH and writes that one byte,
// which need not be valid UTF-8 on its own.
func (l *Lexer) byteEscape(sb *strings.Builder, line, col int) {
	v := 0
	for i := 0; i < 2; i++ {
		if !IsHexDigit(l.peek()) {
			l.errorf(line, col, "\\x needs exactly two hex digits")
			return
		}
		v = v*16 + hexVal(l.advance())
	}
	writeLiteral(sb, byte(v))
}

// runeEscape reads \u{H...}, one to six hex digits naming a Unicode code
// point, and writes its UTF-8 encoding. The braces are required, the way
// Rust and Swift spell it, so the end of the escape is never a guess.
func (l *Lexer) runeEscape(sb *strings.Builder, line, col int) {
	if l.peek() != '{' {
		l.errorf(line, col, "\\u needs braces: \\u{41}")
		return
	}
	l.advance()
	v, n := 0, 0
	for IsHexDigit(l.peek()) {
		v = v*16 + hexVal(l.advance())
		n++
	}
	if l.peek() != '}' || n == 0 {
		l.errorf(line, col, "\\u{...} needs one to six hex digits and a closing brace")
		return
	}
	l.advance()
	if n > 6 {
		l.errorf(line, col, "\\u{...} takes at most six hex digits")
		return
	}
	if v > 0x10FFFF || (v >= 0xD800 && v <= 0xDFFF) {
		l.errorf(line, col, "\\u{%X} is not a Unicode code point", v)
		return
	}
	if v < 0x80 {
		writeLiteral(sb, byte(v))
		return
	}
	sb.WriteRune(rune(v))
}

// writeLiteral writes a byte that came from an escape. A brace is
// doubled, because the parser reads the literal's text again to find
// the interpolations and would otherwise take \u{7B} for the start of
// one.
func writeLiteral(sb *strings.Builder, b byte) {
	if b == '{' || b == '}' {
		sb.WriteByte(b)
	}
	sb.WriteByte(b)
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	}
	return int(c-'A') + 10
}

func (l *Lexer) operator(line, col int) {
	c := l.advance()

	two := func(next byte, ifMatch Kind, matchLex string, otherwise Kind, otherLex string) {
		if l.match(next) {
			l.emit(ifMatch, matchLex, line, col)
		} else {
			l.emit(otherwise, otherLex, line, col)
		}
	}

	switch c {
	case '+':
		two('=', PLUSEQ, "+=", PLUS, "+")
	case '*':
		two('=', STAREQ, "*=", STAR, "*")
	case '/':
		two('=', SLASHEQ, "/=", SLASH, "/")
	case '=':
		switch {
		case l.match('='):
			l.emit(EQ, "==", line, col)
		case l.match('>'):
			l.emit(FATARROW, "=>", line, col)
		default:
			l.emit(ASSIGN, "=", line, col)
		}
	case '!':
		two('=', NEQ, "!=", BANG, "!")
	case '%':
		two('=', PERCENTEQ, "%=", PERCENT, "%")
	case '^':
		two('=', CARETEQ, "^=", CARET, "^")
	case '~':
		l.emit(TILDE, "~", line, col)

	// Multi-character operators must be matched longest-first, or `<<=`
	// lexes as `<<` followed by `=`.
	case '<':
		switch {
		case l.match('<'):
			two('=', SHLEQ, "<<=", SHL, "<<")
		case l.match('='):
			l.emit(LTE, "<=", line, col)
		default:
			l.emit(LT, "<", line, col)
		}
	case '>':
		switch {
		case l.match('>'):
			two('=', SHREQ, ">>=", SHR, ">>")
		case l.match('='):
			l.emit(GTE, ">=", line, col)
		default:
			l.emit(GT, ">", line, col)
		}

	case '-':
		switch {
		case l.match('='):
			l.emit(MINUSEQ, "-=", line, col)
		case l.match('>'):
			l.emit(ARROW, "->", line, col)
		default:
			l.emit(MINUS, "-", line, col)
		}

	case '&':
		switch {
		case l.match('&'):
			l.emit(AND, "&&", line, col)
		case l.match('='):
			l.emit(AMPEQ, "&=", line, col)
		default:
			l.emit(AMP, "&", line, col)
		}
	case '|':
		switch {
		case l.match('|'):
			l.emit(OR, "||", line, col)
		case l.match('='):
			l.emit(PIPEEQ, "|=", line, col)
		default:
			l.emit(PIPE, "|", line, col)
		}

	case '(':
		l.emit(LPAREN, "(", line, col)
	case ')':
		l.emit(RPAREN, ")", line, col)
	case '{':
		l.emit(LBRACE, "{", line, col)
	case '}':
		l.emit(RBRACE, "}", line, col)
	case '[':
		l.emit(LBRACKET, "[", line, col)
	case ']':
		l.emit(RBRACKET, "]", line, col)
	case ',':
		l.emit(COMMA, ",", line, col)
	case '.':
		switch {
		case l.match('.'):
			if l.match('.') {
				l.emit(ELLIPSIS, "...", line, col)
			} else if l.match('=') {
				l.emit(DOTDOTEQ, "..=", line, col)
			} else {
				l.emit(DOTDOT, "..", line, col)
			}
		default:
			l.emit(DOT, ".", line, col)
		}
	case ':':
		l.emit(COLON, ":", line, col)
	case '?':
		l.emit(QUESTION, "?", line, col)

	default:
		l.illegal(line, col, string(c))
	}
}

func (l *Lexer) skipSpaceAndComments() {
	for !l.atEnd() {
		switch c := l.peek(); {
		case c == ' ' || c == '\t' || c == '\r':
			l.advance()

		case c == '/' && l.peekNext() == '/':
			line, col := l.line, l.col
			start := l.Span
			for !l.atEnd() && l.peek() != '\n' {
				l.advance()
			}
			if l.KeepComments {
				l.emit(COMMENT, l.src[start:l.Span], line, col)
			}

		case c == '/' && l.peekNext() == '*':
			line, col := l.line, l.col
			start := l.Span
			l.advance() // '/'
			l.advance() // '*'
			depth := 1
			for depth > 0 {
				if l.atEnd() {
					l.errorf(line, col, "unterminated block comment")
					return
				}
				switch {
				case l.peek() == '/' && l.peekNext() == '*':
					l.advance()
					l.advance()
					depth++
				case l.peek() == '*' && l.peekNext() == '/':
					l.advance()
					l.advance()
					depth--
				default:
					l.advance()
				}
			}
			if l.KeepComments {
				l.emit(COMMENT, l.src[start:l.Span], line, col)
			}

		default:
			return
		}
	}
}

// ---- low-level cursor helpers ----

func (l *Lexer) atEnd() bool { return l.Span >= len(l.src) }

func (l *Lexer) peek() byte {
	if l.atEnd() {
		return 0
	}
	return l.src[l.Span]
}

func (l *Lexer) peekNext() byte {
	if l.Span+1 >= len(l.src) {
		return 0
	}
	return l.src[l.Span+1]
}

func (l *Lexer) advance() byte {
	c := l.src[l.Span]
	l.Span++
	if c == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return c
}

func (l *Lexer) match(expected byte) bool {
	if l.atEnd() || l.src[l.Span] != expected {
		return false
	}
	l.advance()
	return true
}

func (l *Lexer) emit(k Kind, lex string, line, col int) {
	l.tokens = append(l.tokens, Token{Kind: k, Lex: lex, Line: line, Col: col})
}

// emitRaw records the original source text alongside the decoded value.
// Only worth doing where the two differ, and only when something is
// going to write the source back out.
func (l *Lexer) emitRaw(k Kind, lex, raw string, line, col int) {
	if !l.KeepComments {
		raw = ""
	}
	l.tokens = append(l.tokens, Token{Kind: k, Lex: lex, Line: line, Col: col, Raw: raw})
}

func (l *Lexer) illegal(line, col int, lex string) {
	l.errorf(line, col, "unexpected character %q", lex)
	l.emit(ILLEGAL, lex, line, col)
}

func (l *Lexer) errorf(line, col int, format string, args ...any) {
	l.Errors = append(l.Errors,
		fmt.Sprintf("%s:%d:%d: %s", l.file, line, col, fmt.Sprintf(format, args...)))
}

func IsAlpha(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func IsDigit(c byte) bool { return c >= '0' && c <= '9' }
