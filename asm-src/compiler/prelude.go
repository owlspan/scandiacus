package main

// The prelude: library functions written in Veyl and compiled by this
// compiler like any other code.
//
// Why this exists. The guarantee of this backend is that a program
// compiled here prints the same bytes as the same program compiled
// through Go. For arithmetic that means agreeing to the last bit,
// because printing goes through the shortest round-tripping decimal and
// a one-bit difference is a visible digit. msvcrt's libm is a perfectly
// good libm and it is not Go's: measured across a hundred and twenty
// values of sin, cos, tan, exp, log, atan, asin, acos and pow, a third
// of them differ in the last place. Calling msvcrt is therefore not an
// implementation of these functions, it is a different function.
//
// So they are implemented, from Go's own algorithms. What this file adds
// is that they are implemented *in Veyl* rather than by hand-writing
// calls into the IR builder. `y = z + z*(zz*((P0*zz+P1)*zz+P2)/D)` is
// one line of Veyl and about twenty lines of emit calls, and the twenty
// lines cannot be read against the algorithm they came from. Writing it
// in the language the compiler already compiles gets the lowering, the
// type checking and the byte writer for nothing.
//
// How it works. The source below is parsed once, and a program that
// mentions one of these builtins has the function it needs - and every
// function that one calls, transitively - folded into it before
// checking, exactly the way an imported file is. Nothing is pulled in
// for a program that does not use it.
//
// Two private builtins exist only for this file: `__bits` and
// `__frombits` reinterpret a float as its IEEE 754 bit pattern and back.
// Go's Frexp, Ldexp, Modf and the special-case tests are all written in
// terms of that, and there is no way to say it in Veyl otherwise. They
// are not in the language and a user program naming one gets the
// ordinary "not on the assembly backend yet" error, because they are
// registered for the prelude's own compilation and nowhere else.

import (
	"strings"
	"sync"
)

// preludeOf maps a builtin to the Veyl function that implements it.
// A builtin absent from here is either lowered directly in the IR or is
// not on this backend at all.
var preludeOf = map[string]string{
	"sin":   "__vy_sin",
	"cos":   "__vy_cos",
	"tan":   "__vy_tan",
	"asin":  "__vy_asin",
	"acos":  "__vy_acos",
	"atan":  "__vy_atan",
	"atan2": "__vy_atan2",
	"exp":   "__vy_exp",
	"log":   "__vy_log",
	"log2":  "__vy_log2",
	"log10": "__vy_log10",
	"pow":   "__vy_pow",
	"cbrt":  "__vy_cbrt",
	"hypot": "__vy_hypot",

	"time.format":  "__vy_timeFormat",
	"time.parse":   "__vy_timeParse",
	"time.date":    "__vy_timeDate",
	"time.clock":   "__vy_timeClock",
	"time.stamp":   "__vy_timeStamp",
	"time.year":    "__vy_timeYear",
	"time.month":   "__vy_timeMonth",
	"time.day":     "__vy_timeDay",
	"time.weekday": "__vy_timeWeekday",
	"time.since":   "__vy_timeSince",

	"bits.count":    "__vy_bitsCount",
	"proc.current":  "__vy_procSelf",
	"proc.pid":      "__vy_procId",
	"proc.open":     "__vy_procOpen",
	"proc.close":    "__vy_procClose",
	"proc.read":     "__vy_procRead",
	"proc.write":    "__vy_procWrite",
	"proc.readU8":   "__vy_procReadU8",
	"proc.readI32":  "__vy_procReadI32",
	"proc.readI64":  "__vy_procReadI64",
	"proc.writeI32": "__vy_procWriteI32",
	"proc.writeI64": "__vy_procWriteI64",
	"proc.scan":     "__vy_procScan",
	"bits.length":   "__vy_bitsLength",
	"bits.leading":  "__vy_bitsLeading",
	"bits.trailing": "__vy_bitsTrailing",
	"bits.reverse":  "__vy_bitsReverse",
	"bits.rotate":   "__vy_bitsRotate",
	"bits.toBase":   "__vy_toBase",
	"bits.fromBase": "__vy_fromBase",

	"args.flag":     "__vy_argsFlag",
	"args.value":    "__vy_argsValue",
	"args.rest":     "__vy_argsRest",
	"os.args":       "__vy_osArgs",
	"os.dir.home":   "__vy_dirHome",
	"os.dir.temp":   "__vy_dirTemp",
	"os.path.clean": "__vy_pathClean",

	"url.scheme":   "__vy_urlScheme",
	"url.host":     "__vy_urlHost",
	"url.port":     "__vy_urlPort",
	"url.path":     "__vy_urlPath",
	"url.fragment": "__vy_urlFragment",
	"url.query":    "__vy_urlQuery",
	"url.join":     "__vy_urlJoin",

	"stats.mean":       "__vy_mean",
	"stats.median":     "__vy_median",
	"stats.var":        "__vy_variance",
	"stats.stdev":      "__vy_stdev",
	"stats.percentile": "__vy_percentile",

	"term.red":       "__vy_termRed",
	"term.green":     "__vy_termGreen",
	"term.yellow":    "__vy_termYellow",
	"term.blue":      "__vy_termBlue",
	"term.magenta":   "__vy_termMagenta",
	"term.cyan":      "__vy_termCyan",
	"term.grey":      "__vy_termGrey",
	"term.bold":      "__vy_termBold",
	"term.dim":       "__vy_termDim",
	"term.underline": "__vy_termUnderline",
	"term.invert":    "__vy_termInvert",
	"term.clear":     "__vy_termClear",
	"term.colour":    "__vy_termColour",
	"term.bar":       "__vy_termBar",

	"rand.seed":  "__vy_randSeed",
	"rand.int":   "__vy_randInt",
	"rand.float": "__vy_randFloat",
	"rand.bool":  "__vy_randBool",
	"rand.hex":   "__vy_randHex",
	"rand.uuid":  "__vy_randUUID",
	"random":     "__vy_randFloat",
	"randomInt":  "__vy_randomInt",

	"bytes.hex":        "__vy_bytesHex",
	"bytes.fromHex":    "__vy_bytesFromHex",
	"bytes.base64":     "__vy_bytesBase64",
	"bytes.fromBase64": "__vy_bytesFromBase64",
	"bytes.slice":      "__vy_bytesSlice",
	"bytes.find":       "__vy_bytesFind",
	"bytes.list":       "__vy_bytesList",
	"bytes.ofList":     "__vy_bytesOfList",
	"bytes.fill":       "__vy_bytesFill",
	"bytes.hash":       "__vy_bytesHash",
	"bytes.getInt":     "__vy_getIntLE",
	"bytes.getIntBE":   "__vy_getIntBE",
	"bytes.putInt":     "__vy_putIntLE",
	"bytes.putIntBE":   "__vy_putIntBE",
	"hash.sha256":      "__vy_hashSha256",
	"hash.sha1":        "__vy_hashSha1",
	"hash.md5":         "__vy_hashMd5",
	"hash.sha512":      "__vy_hashSha512",
	"hash.crc32":       "__vy_crc32",
	"hash.file":        "__vy_hashFile",
	"hash.hex":         "__vy_hashHex",
	"hash.fromHex":     "__vy_hashFromHex",
	"hash.base64":      "__vy_hashBase64",
	"hash.fromBase64":  "__vy_hashFromBase64",
	"csv.parse":        "__vy_csvParse",
	"csv.write":        "__vy_csvWrite",
	"csv.read":         "__vy_csvRead",
	"csv.save":         "__vy_csvSave",
	"http.serve":       "__vy_httpServe",
	"http.get":         "__vy_httpGet",
	"http.post":        "__vy_httpPost",
	"http.download":    "__vy_httpDownload",
	"http.ok":          "__vy_httpOK",
	"http.text":        "__vy_httpText",
	"http.json":        "__vy_httpJSON",
	"http.status":      "__vy_httpStatus",
	"http.notFound":    "__vy_httpNotFound",
	"http.header":      "__vy_httpHeader",
	"win.button":       "__vy_winButton",
	"win.bar":          "__vy_winBar",
	"win.frame":        "__vy_winFrame",
	"win.hover":        "__vy_winHover",
	"win.pressed":      "__vy_winPressed",

	"re.matches": "__vy_reMatches",
	"re.find":    "__vy_reFind",
	"re.findAll": "__vy_reFindAll",
	"re.groups":  "__vy_reGroups",
	"re.split":   "__vy_reSplit",
	"re.count":   "__vy_reCount",
	"re.replace": "__vy_reReplace",
	"re.valid":   "__vy_reValid",
	"re.escape":  "__vy_reEscape",
	"floor":      "__vy_floorInt",
	"ceil":       "__vy_ceilInt",
	"round":      "__vy_roundInt",
	"trunc":      "__vy_truncInt",

	"input":    "__vy_input",
	"pause":    "__vy_pause",
	"toFloat":  "__vy_toFloat",
	"isFloat":  "__vy_isFloat",
	"count":    "__vy_count",
	"padLeft":  "__vy_padLeft",
	"padRight": "__vy_padRight",
	"substr":   "__vy_substr",
	"charAt":   "__vy_charAt",
	"chars":    "__vy_chars",

	"mem.scan": "__vy_memScan",

	"win.image": "__vy_winImage",
}

// preludeDefaults supplies the trailing arguments a call may leave off,
// for the builtins checkOptional admits with fewer than their full
// parameter list. The values are the Go backend's.
var preludeDefaults = map[string][]Expr{
	"input":    {&StrLit{Val: ""}},
	"toFloat":  {&FloatLit{Val: "0.0"}},
	"padLeft":  {&StrLit{Val: " "}},
	"padRight": {&StrLit{Val: " "}},
}

// preludeAlso is for a builtin the lowerer implements itself but which
// still needs something from the prelude. shuffle, sample and pick are
// lowered in randlib.go because they work on a list of anything, and
// they draw from the prelude's generator - so naming one has to fold
// that in, even though the call never goes through the prelude path.
var preludeAlso = map[string][]string{
	"rand.shuffle": {randSource},
	"rand.sample":  {randSource},
	"rand.pick":    {randSource},
}

var (
	preludeOnce sync.Once
	preludeProg *Program
	preludeErrs []string
)

// parsePrelude reads the source once. A syntax error in it is a compiler
// bug rather than anything a user did, so it is reported that way.
func parsePrelude() (*Program, []string) {
	preludeOnce.Do(func() {
		lx := NewLexer("<prelude>", preludeSource)
		toks := lx.Scan()
		ps := NewParser("<prelude>", toks)
		preludeProg = ps.ParseProgram()
		preludeErrs = append(append([]string{}, lx.Errors...), ps.Errors...)
		for _, f := range preludeProg.Funcs {
			f.File = "<prelude>"
		}
	})
	return preludeProg, preludeErrs
}

// addPrelude folds in the prelude functions a program needs.
//
// What a program needs is decided by which identifiers appear in its
// source, not by walking its syntax tree. That over-approximates - a
// program with the word `sin` in a string literal pulls the function in
// - and over-approximating is the safe direction: the cost is a few
// hundred bytes of code nothing calls, where under-approximating is a
// link failure. A twenty-nine-case tree walk that has to be updated
// every time the language grows a node would be the unsafe direction.
//
// The prelude's own internal dependencies are resolved the same way,
// chunk by chunk, which is exact enough because the prelude is this
// compiler's own source and every name in it is deliberate.
func addPrelude(prog *Program, sources []string) []string {
	pre, errs := parsePrelude()
	if len(errs) > 0 {
		return errs
	}

	chunks := preludeChunks()

	wanted := map[string]bool{}
	for _, src := range sources {
		for name, fn := range preludeOf {
			if mentions(src, name) {
				wanted[fn] = true
			}
		}
		for name, fns := range preludeAlso {
			if mentions(src, name) {
				for _, fn := range fns {
					wanted[fn] = true
				}
			}
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	// Transitive closure. A prelude function's dependencies are the
	// other prelude names its own text mentions.
	taken := map[string]bool{}
	var pull func(name string)
	pull = func(name string) {
		if taken[name] {
			return
		}
		body, ok := chunks[name]
		if !ok {
			return
		}
		taken[name] = true
		for other := range chunks {
			if other != name && mentions(body, other) {
				pull(other)
			}
		}
		// A prelude function may call a builtin that is itself a prelude
		// function - percentile calls floor - and it calls it by the
		// name a user would write, not by the internal one. Without
		// this, the call compiles and the callee is never folded in.
		for builtin, fn := range preludeOf {
			if fn != name && mentions(body, builtin) {
				pull(fn)
			}
		}
	}
	for name := range wanted {
		pull(name)
	}

	// In the prelude's own order, so two compilations of the same
	// program lay the functions out identically.
	for _, f := range pre.Funcs {
		if taken[f.Name] {
			prog.Funcs = append(prog.Funcs, f)
		}
	}

	// A prelude const is state the functions share - the generator's
	// vector is the only one - so it comes in whenever anything does.
	// Unconditionally would cost a word of static storage in every
	// program, which is why it is behind the same gate.
	prog.Globals = append(prog.Globals, pre.Globals...)

	// A prelude struct comes in only if something actually names it,
	// either the program or one of the functions being folded in.
	// Unconditionally would reserve `Request` and `Response` in every
	// program that so much as calls sin, and a user's own struct of
	// that name would collide with one they never asked for.
	for _, s := range pre.Structs {
		named := false
		for _, src := range sources {
			if mentions(src, s.Name) {
				named = true
				break
			}
		}
		if !named {
			for name := range taken {
				if mentions(chunks[name], s.Name) {
					named = true
					break
				}
			}
		}
		if named {
			prog.Structs = append(prog.Structs, s)
		}
	}
	return nil
}

// mentions reports whether name appears in src as a whole identifier,
// rather than as part of a longer one. Without the boundary check,
// wanting `log` would drag in every function whose name contains it.
func mentions(src, name string) bool {
	from := 0
	for {
		i := strings.Index(src[from:], name)
		if i < 0 {
			return false
		}
		i += from
		before := byte(' ')
		if i > 0 {
			before = src[i-1]
		}
		after := byte(' ')
		if i+len(name) < len(src) {
			after = src[i+len(name)]
		}
		if !identByte(before) && !identByte(after) {
			return true
		}
		from = i + 1
	}
}

func identByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

var (
	chunkOnce sync.Once
	chunkText map[string]string
)

// preludeChunks splits the source into one entry per function, keyed by
// name. A function starts at a `fn ` in the first column and runs to the
// next one, which is a property of how this file is written rather than
// of the language - the prelude is formatted for this.
func preludeChunks() map[string]string {
	chunkOnce.Do(func() {
		chunkText = map[string]string{}
		lines := strings.Split(preludeSource, "\n")
		name := ""
		var body []string
		flush := func() {
			if name != "" {
				chunkText[name] = strings.Join(body, "\n")
			}
		}
		for _, line := range lines {
			if strings.HasPrefix(line, "fn ") {
				flush()
				rest := line[3:]
				cut := strings.IndexAny(rest, "( ")
				if cut < 0 {
					cut = len(rest)
				}
				name = rest[:cut]
				body = nil
			}
			body = append(body, line)
		}
		flush()
	})
	return chunkText
}

// preludeSource is the Veyl text. It is split into named chunks only so
// that the algorithms can carry the comments explaining where their
// constants came from.
var preludeSource = strings.Join([]string{
	preludeBits,
	preludeLog,
	preludeExp,
	preludeTrig,
	preludeAtan,
	preludePow,
	preludeRoot,
	preludeRound,
	preludeTime,
	preludeTimeHelpers,
	preludeBits2,
	preludeArgs,
	preludeURL,
	preludeOsMore,
	preludeStats,
	preludeTerm,
	preludeRand,
	preludeRandTable,
	preludeBytes,
	preludeBytesWrap,
	preludeHash,
	preludeHash2,
	preludeCsv,
	preludeHTTP,
	preludeUI,
	preludeRe,
	preludeText,
	preludePNG,
	preludeProc,
}, "\n")
