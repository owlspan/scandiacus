package main

// The assembly backend's builtin table, presented to the shared type
// checker.
//
// The checker in ../../frontend is the same code the Go backend runs. It
// asks a Library what a builtin looks like, and this is that Library.
// Where the Go backend declares 302 builtins backed by Go's standard
// library, this declares the ones written by hand here.
//
// That is the point of the seam. A program calling `http.get` on this
// backend is now an ordinary type error with a file, line and column,
// reported alongside every other error in the program, instead of
// something the lowerer discovers halfway through and reports in its own
// vocabulary. The subset stops being a property of what happens to be
// implemented and becomes something the compiler states up front.

import front "veylfront"

type asmLibrary struct{}

// sigs is the whole standard library of this backend. Keeping it as data
// rather than as a switch means the checker and the lowerer cannot
// disagree about what exists.
var sigs = map[string]front.Signature{
	// Output. print is variadic in neither backend, but it accepts any
	// single value and decides how to render it from the type.
	"print": {Params: []*Type{Any}, Ret: Void},
	"write": {Params: []*Type{Any}, Ret: Void},

	// Conversion.
	"str": {Params: []*Type{Any}, Ret: Str},

	// Numbers.
	"abs": {Params: []*Type{Numeric}, Ret: Float},
	// Numeric accepts int or float and always yields a float, matching
	// the Go backend: divf(7, 2) is 3.5 however its arguments were
	// written.
	"int":   {Params: []*Type{Numeric}, Ret: Int},
	"float": {Params: []*Type{Numeric}, Ret: Float},
	"divf":  {Params: []*Type{Numeric, Numeric}, Ret: Float},
	"sqrt":  {Params: []*Type{Numeric}, Ret: Float},
	// Whole numbers come back as an int, as on the Go backend, so
	// `let n: int = floor(x)` needs no conversion.
	"floor": {Params: []*Type{Numeric}, Ret: Int},
	"ceil":  {Params: []*Type{Numeric}, Ret: Int},
	"round": {Params: []*Type{Numeric}, Ret: Int},
	"trunc": {Params: []*Type{Numeric}, Ret: Int},
	"mod":   {Params: []*Type{Numeric, Numeric}, Ret: Float},

	// The rest of the math library. Most of these are one msvcrt call;
	// see mathlib.go for the three that are not and why.
	"sin":   {Params: []*Type{Numeric}, Ret: Float},
	"cos":   {Params: []*Type{Numeric}, Ret: Float},
	"tan":   {Params: []*Type{Numeric}, Ret: Float},
	"asin":  {Params: []*Type{Numeric}, Ret: Float},
	"acos":  {Params: []*Type{Numeric}, Ret: Float},
	"atan":  {Params: []*Type{Numeric}, Ret: Float},
	"exp":   {Params: []*Type{Numeric}, Ret: Float},
	"log":   {Params: []*Type{Numeric}, Ret: Float},
	"log2":  {Params: []*Type{Numeric}, Ret: Float},
	"log10": {Params: []*Type{Numeric}, Ret: Float},
	"cbrt":  {Params: []*Type{Numeric}, Ret: Float},
	"atan2": {Params: []*Type{Numeric, Numeric}, Ret: Float},
	"pow":   {Params: []*Type{Numeric, Numeric}, Ret: Float},
	"hypot": {Params: []*Type{Numeric, Numeric}, Ret: Float},
	"clamp": {Params: []*Type{Numeric, Numeric, Numeric}, Ret: Float},
	"sign":  {Params: []*Type{Numeric}, Ret: Int},
	"isNan": {Params: []*Type{Numeric}, Ret: Bool},
	"exit":  {Params: []*Type{Int}, Ret: Void},
	"sleep": {Params: []*Type{Int}, Ret: Void},

	// Private to the prelude. These reinterpret a float as its IEEE 754
	// bit pattern and back, which Frexp, Ldexp and the special-case
	// tests are all written in terms of and which Veyl otherwise cannot
	// say. They are in this table because the prelude is checked by this
	// checker like any other code; nothing stops a user program calling
	// one, and nothing needs to - the names are reserved by the same
	// convention every compiler-generated name here follows.
	"__bits":     {Params: []*Type{Float}, Ret: Int},
	"__frombits": {Params: []*Type{Int}, Ret: Float},

	// Also private to the prelude: stop with a message. It is declared
	// as returning a float only so that it can be the last statement of
	// a float function - it does not return at all.
	"__abort":    {Params: []*Type{Str}, Ret: Float},
	"__abortStr": {Params: []*Type{Str}, Ret: Str},

	// And these: one rendering of a float by the C library, at a
	// precision the caller chooses. See strfmt.go.
	"__fmtE": {Params: []*Type{Int, Numeric}, Ret: Str},
	"__fmtF": {Params: []*Type{Int, Numeric}, Ret: Str},

	// Strings.
	"upper":      {Params: []*Type{Str}, Ret: Str},
	"lower":      {Params: []*Type{Str}, Ret: Str},
	"substr":     {Params: []*Type{Str, Int, Int}, Ret: Str},
	"charAt":     {Params: []*Type{Str, Int}, Ret: Str},
	"startsWith": {Params: []*Type{Str, Str}, Ret: Bool},
	"endsWith":   {Params: []*Type{Str, Str}, Ret: Bool},
	"repeat":     {Params: []*Type{Str, Int}, Ret: Str},
	"split":      {Params: []*Type{Str, Str}, Ret: ListOf(Str)},
	"chars":      {Params: []*Type{Str}, Ret: ListOf(Str)},
	"lines":      {Params: []*Type{Str}, Ret: ListOf(Str)},
	"trim":       {Params: []*Type{Str}, Ret: Str},
	"isInt":      {Params: []*Type{Str}, Ret: Bool},
	"isFloat":    {Params: []*Type{Str}, Ret: Bool},
	"count":      {Params: []*Type{Str, Str}, Ret: Int},
	"pause":      {Ret: Void},

	// Private to the prelude: the byte-indexed originals of substr and
	// charAt, which the prelude's own parsers use, and the primitives
	// under the text builtins. See textlib.go.
	"__substrB":  {Params: []*Type{Str, Int, Int}, Ret: Str},
	"__charAtB":  {Params: []*Type{Str, Int}, Ret: Str},
	"__strByte":  {Params: []*Type{Str, Int}, Ret: Int},
	"__readByte": {Params: []*Type{Bytes}, Ret: Int},
	"__strtod":   {Params: []*Type{Str}, Ret: Float},

	// The namespaced library. A dotted name is looked up here exactly
	// like a plain one - the checker flattens `time.now` to that string
	// before asking - so a namespace is a naming convention rather than
	// a scope, the same as on the Go backend.
	//
	// Everything else the Go backend puts under os, time, mem, json, re,
	// hash, csv, net, http and task is absent, and absent here means a
	// type error naming the function rather than something the lowerer
	// discovers later.
	"time.now":     {Ret: Int},
	"time.millis":  {Ret: Int},
	"time.nanos":   {Ret: Int},
	"time.format":  {Params: []*Type{Int, Str}, Ret: Str},
	"time.parse":   {Params: []*Type{Str, Str}, Ret: Int},
	"time.date":    {Ret: Str},
	"time.clock":   {Ret: Str},
	"time.stamp":   {Ret: Str},
	"time.year":    {Ret: Int},
	"time.month":   {Ret: Int},
	"time.day":     {Ret: Int},
	"time.weekday": {Ret: Str},
	"time.since":   {Params: []*Type{Int}, Ret: Int},
	"time.sleep":   {Params: []*Type{Int}, Ret: Void},

	// Private to the prelude: the three questions only the operating
	// system answers. See timelib.go.
	"__tmField": {Params: []*Type{Int, Int}, Ret: Int},
	"__mktime":  {Params: []*Type{Int, Int, Int, Int, Int, Int}, Ret: Int},
	"__millis":  {Ret: Int},
	"__chr":     {Params: []*Type{Int}, Ret: Str},
	"__cmdline": {Ret: Str},
	"__isatty":  {Ret: Bool},
	"__strAt":   {Params: []*Type{Str, Int}, Ret: Int},

	"re.matches": {Params: []*Type{Str, Str}, Ret: Bool},
	"re.find":    {Params: []*Type{Str, Str}, Ret: Str},
	"re.findAll": {Params: []*Type{Str, Str}, Ret: ListOf(Str)},
	"re.groups":  {Params: []*Type{Str, Str}, Ret: ListOf(Str)},
	"re.split":   {Params: []*Type{Str, Str}, Ret: ListOf(Str)},
	"re.count":   {Params: []*Type{Str, Str}, Ret: Int},
	"re.replace": {Params: []*Type{Str, Str, Str}, Ret: Str},
	"re.valid":   {Params: []*Type{Str}, Ret: Bool},
	"re.escape":  {Params: []*Type{Str}, Ret: Str},

	// term. Colour is a str to a str; the bar and the colour question
	// are their own shapes.
	"term.red":       {Params: []*Type{Str}, Ret: Str},
	"term.green":     {Params: []*Type{Str}, Ret: Str},
	"term.yellow":    {Params: []*Type{Str}, Ret: Str},
	"term.blue":      {Params: []*Type{Str}, Ret: Str},
	"term.magenta":   {Params: []*Type{Str}, Ret: Str},
	"term.cyan":      {Params: []*Type{Str}, Ret: Str},
	"term.grey":      {Params: []*Type{Str}, Ret: Str},
	"term.bold":      {Params: []*Type{Str}, Ret: Str},
	"term.dim":       {Params: []*Type{Str}, Ret: Str},
	"term.underline": {Params: []*Type{Str}, Ret: Str},
	"term.invert":    {Params: []*Type{Str}, Ret: Str},
	"term.clear":     {Ret: Void},
	"term.colour":    {Ret: Bool},
	"term.bar":       {Params: []*Type{Int, Int, Int}, Ret: Str},

	"rand.seed":  {Params: []*Type{Int}, Ret: Void},
	"rand.int":   {Params: []*Type{Int, Int}, Ret: Int},
	"rand.float": {Ret: Float},
	"rand.bool":  {Ret: Bool},
	"rand.hex":   {Params: []*Type{Int}, Ret: Str},
	"rand.uuid":  {Ret: Str},
	"random":     {Ret: Float},
	"randomInt":  {Params: []*Type{Int, Int}, Ret: Int},

	// bytes. The primitives are in bytes.go; everything else is in the
	// prelude on top of them.
	"__bytesMake": {Params: []*Type{Int}, Ret: Bytes},
	"__byteAt":    {Params: []*Type{Bytes, Int}, Ret: Int},
	"__bytePut":   {Params: []*Type{Bytes, Int, Int}, Ret: Void},

	"bytes.of":         {Params: []*Type{Str}, Ret: Bytes},
	"bytes.str":        {Params: []*Type{Bytes}, Ret: Str},
	"bytes.hex":        {Params: []*Type{Bytes}, Ret: Str},
	"bytes.base64":     {Params: []*Type{Bytes}, Ret: Str},
	"bytes.fromHex":    {Params: []*Type{Str}, Ret: ResultOf(Bytes)},
	"bytes.fromBase64": {Params: []*Type{Str}, Ret: ResultOf(Bytes)},
	"bytes.slice":      {Params: []*Type{Bytes, Int, Int}, Ret: Bytes},
	"bytes.find":       {Params: []*Type{Bytes, Bytes}, Ret: Int},
	"bytes.list":       {Params: []*Type{Bytes}, Ret: ListOf(Int)},
	"bytes.ofList":     {Params: []*Type{ListOf(Int)}, Ret: ResultOf(Bytes)},
	"bytes.fill":       {Params: []*Type{Int, Int}, Ret: ResultOf(Bytes)},
	"bytes.hash":       {Params: []*Type{Bytes, Str}, Ret: ResultOf(Str)},
	"bytes.concat":     {Params: []*Type{Bytes}, Rest: Bytes, Ret: Bytes},
	"bytes.read":       {Params: []*Type{Str}, Ret: ResultOf(Bytes)},
	"bytes.write":      {Params: []*Type{Str, Bytes}, Ret: ResultOf(Void)},
	"bytes.getInt":     {Params: []*Type{Bytes, Int, Int}, Ret: ResultOf(Int)},
	"bytes.getIntBE":   {Params: []*Type{Bytes, Int, Int}, Ret: ResultOf(Int)},
	"bytes.putInt":     {Params: []*Type{Int, Int}, Ret: ResultOf(Bytes)},
	"bytes.putIntBE":   {Params: []*Type{Int, Int}, Ret: ResultOf(Bytes)},

	"hash.sha256":     {Params: []*Type{Str}, Ret: Str},
	"hash.sha1":       {Params: []*Type{Str}, Ret: Str},
	"hash.md5":        {Params: []*Type{Str}, Ret: Str},
	"hash.sha512":     {Params: []*Type{Str}, Ret: Str},
	"hash.crc32":      {Params: []*Type{Str}, Ret: Int},
	"hash.file":       {Params: []*Type{Str}, Ret: ResultOf(Str)},
	"hash.hex":        {Params: []*Type{Str}, Ret: Str},
	"hash.fromHex":    {Params: []*Type{Str}, Ret: ResultOf(Str)},
	"hash.base64":     {Params: []*Type{Str}, Ret: Str},
	"hash.fromBase64": {Params: []*Type{Str}, Ret: ResultOf(Str)},

	// Windows. A window is an opaque int handle, the same way a socket
	// is, so the UI layer above can be written in Veyl.
	"win.open":      {Params: []*Type{Str, Int, Int}, Ret: ResultOf(Int)},
	"win.poll":      {Params: []*Type{Int}, Ret: Bool},
	"win.present":   {Params: []*Type{Int}, Ret: Void},
	"win.close":     {Params: []*Type{Int}, Ret: Void},
	"win.title":     {Params: []*Type{Int, Str}, Ret: Void},
	"win.rgb":       {Params: []*Type{Int, Int, Int}, Ret: Int},
	"win.clear":     {Params: []*Type{Int, Int}, Ret: Void},
	"win.rect":      {Params: []*Type{Int, Int, Int, Int, Int, Int}, Ret: Void},
	"win.line":      {Params: []*Type{Int, Int, Int, Int, Int, Int}, Ret: Void},
	"win.circle":    {Params: []*Type{Int, Int, Int, Int, Int}, Ret: Void},
	"win.text":      {Params: []*Type{Int, Int, Int, Str, Int}, Ret: Void},
	"win.mouseX":    {Params: []*Type{Int}, Ret: Int},
	"win.mouseY":    {Params: []*Type{Int}, Ret: Int},
	"win.width":     {Params: []*Type{Int}, Ret: Int},
	"win.height":    {Params: []*Type{Int}, Ret: Int},
	"win.mouseDown": {Params: []*Type{Int}, Ret: Bool},
	"win.clicked":   {Params: []*Type{Int}, Ret: Bool},
	"win.key":       {Params: []*Type{Int, Int}, Ret: Bool},
	"win.resizable": {Params: []*Type{Int, Bool}, Ret: Void},

	// Canvases, images, text measurement and sound; see winmedia.go.
	"win.canvas": {Params: []*Type{Int, Int}, Ret: Int},
	"win.pixel":  {Params: []*Type{Int, Int, Int}, Ret: Int},
	"win.pixels": {Params: []*Type{Int}, Ret: PtrToC("u32")},
	"win.image":  {Params: []*Type{Str}, Ret: ResultOf(Int)},
	// Private to the prelude's win.image; see winmedia.go.
	"__imageFromBMP":    {Params: []*Type{Str}, Ret: Int},
	"__imageFromPixels": {Params: []*Type{Bytes}, Ret: Int},
	"win.imageWidth":    {Params: []*Type{Int}, Ret: Int},
	"win.imageHeight":   {Params: []*Type{Int}, Ret: Int},
	"win.freeImage":     {Params: []*Type{Int}, Ret: Void},
	"win.draw":          {Params: []*Type{Int, Int, Int, Int}, Ret: Void},
	"win.drawScaled":    {Params: []*Type{Int, Int, Int, Int, Int, Int}, Ret: Void},
	"win.drawKeyed":     {Params: []*Type{Int, Int, Int, Int, Int}, Ret: Void},
	"win.textWidth":     {Params: []*Type{Int, Str}, Ret: Int},
	"win.textHeight":    {Params: []*Type{Int}, Ret: Int},
	"sound.play":        {Params: []*Type{Str}, Ret: Bool},
	"sound.loop":        {Params: []*Type{Str}, Ret: Bool},
	"sound.stop":        {Ret: Void},

	// The widgets, written in Veyl in the prelude on top of the above.
	// SQLite. A connection is an opaque int handle, and every value
	// comes back as str because a column is dynamically typed and there
	// is no way yet to say what a query returns.
	//
	// exec and query both take their arguments separately and bind
	// them, so there is no path through this API that pastes a value
	// into SQL.
	"db.open":    {Params: []*Type{Str}, Ret: ResultOf(Int)},
	"db.close":   {Params: []*Type{Int}, Ret: Void},
	"db.exec":    {Params: []*Type{Int, Str, ListOf(Str)}, Ret: ResultOf(Void)},
	"db.query":   {Params: []*Type{Int, Str, ListOf(Str)}, Ret: ResultOf(ListOf(ListOf(Str)))},
	"db.changes": {Params: []*Type{Int}, Ret: Int},
	"db.lastId":  {Params: []*Type{Int}, Ret: Int},
	"db.version": {Ret: Str},

	"win.pressed": {Params: []*Type{Int, Str}, Ret: Bool},
	"win.hover":   {Params: []*Type{Int, Int, Int, Int, Int}, Ret: Bool},
	"win.button":  {Params: []*Type{Int, Int, Int, Int, Int, Str}, Ret: Bool},
	"win.bar":     {Params: []*Type{Int, Int, Int, Int, Int, Float, Int}, Ret: Void},
	"win.frame":   {Params: []*Type{Int, Int, Int, Int, Int, Int}, Ret: Void},

	// http. Request and Response are structs declared in the prelude and
	// folded into any program that names them, so StructOf is enough
	// here: the checker resolves the name against what was folded in and
	// reports an undeclared struct if it was not.
	"http.get":      {Params: []*Type{Str}, Ret: ResultOf(Str)},
	"http.post":     {Params: []*Type{Str, Str}, Ret: ResultOf(Str)},
	"http.download": {Params: []*Type{Str, Str}, Ret: ResultOf(Void)},
	"__winhttp":     {Params: []*Type{Str, Int, Str, Bool, Str, Str}, Ret: ResultOf(Str)},
	"http.ok":       {Params: []*Type{Str}, Ret: StructOf("Response")},
	"http.text":     {Params: []*Type{Str}, Ret: StructOf("Response")},
	"http.json":     {Params: []*Type{Str}, Ret: StructOf("Response")},
	"http.status":   {Params: []*Type{Int, Str}, Ret: StructOf("Response")},
	"http.notFound": {Ret: StructOf("Response")},
	"http.header":   {Params: []*Type{StructOf("Request"), Str}, Ret: Str},

	// Sockets. A socket is the int handle Windows hands back, which is
	// what lets the http layer above be written in Veyl.
	"net.listen":  {Params: []*Type{Int}, Ret: ResultOf(Int)},
	"net.accept":  {Params: []*Type{Int}, Ret: ResultOf(Int)},
	"net.recv":    {Params: []*Type{Int}, Ret: ResultOf(Str)},
	"net.send":    {Params: []*Type{Int, Str}, Ret: ResultOf(Int)},
	"net.close":   {Params: []*Type{Int}, Ret: Void},
	"net.connect": {Params: []*Type{Str, Int}, Ret: ResultOf(Int)},

	"csv.parse": {Params: []*Type{Str}, Ret: ResultOf(ListOf(ListOf(Str)))},
	"csv.write": {Params: []*Type{ListOf(ListOf(Str))}, Ret: Str},
	"csv.read":  {Params: []*Type{Str}, Ret: ResultOf(ListOf(ListOf(Str)))},
	"csv.save":  {Params: []*Type{Str, ListOf(ListOf(Str))}, Ret: Bool},

	// bits, args and url. All of these are prelude functions; see
	// prelude_more.go.
	"bits.count":    {Params: []*Type{Int}, Ret: Int},
	"bits.length":   {Params: []*Type{Int}, Ret: Int},
	"bits.leading":  {Params: []*Type{Int}, Ret: Int},
	"bits.trailing": {Params: []*Type{Int}, Ret: Int},
	"bits.reverse":  {Params: []*Type{Int}, Ret: Int},
	"bits.rotate":   {Params: []*Type{Int, Int}, Ret: Int},
	"bits.toBase":   {Params: []*Type{Int, Int}, Ret: Str},
	"bits.fromBase": {Params: []*Type{Str, Int}, Ret: ResultOf(Int)},

	"args.flag":  {Params: []*Type{Str}, Ret: Bool},
	"args.value": {Params: []*Type{Str, Str}, Ret: Str},
	"args.rest":  {Ret: ListOf(Str)},
	"os.args":    {Ret: ListOf(Str)},

	"url.scheme":   {Params: []*Type{Str}, Ret: ResultOf(Str)},
	"url.host":     {Params: []*Type{Str}, Ret: ResultOf(Str)},
	"url.port":     {Params: []*Type{Str}, Ret: ResultOf(Str)},
	"url.path":     {Params: []*Type{Str}, Ret: ResultOf(Str)},
	"url.fragment": {Params: []*Type{Str}, Ret: ResultOf(Str)},
	"url.query":    {Params: []*Type{Str}, Ret: ResultOf(MapOf(Str, Str))},
	"url.join":     {Params: []*Type{Str, Str}, Ret: ResultOf(Str)},

	"os.env.get": {Params: []*Type{Str}, Ret: Str},
	"os.env.has": {Params: []*Type{Str}, Ret: Bool},
	"os.env.set": {Params: []*Type{Str, Str}, Ret: ResultOf(Void)},

	// Files, on Win32. Every one of these that can fail says so as a
	// T!, with the same wording the Go backend produces, which is the
	// reason they are written against CreateFile rather than fopen.
	"os.file.read":   {Params: []*Type{Str}, Ret: ResultOf(Str)},
	"os.file.lines":  {Params: []*Type{Str}, Ret: ResultOf(ListOf(Str))},
	"os.file.write":  {Params: []*Type{Str, Str}, Ret: ResultOf(Void)},
	"os.file.append": {Params: []*Type{Str, Str}, Ret: ResultOf(Void)},
	"os.file.exists": {Params: []*Type{Str}, Ret: Bool},
	"os.file.size":   {Params: []*Type{Str}, Ret: ResultOf(Int)},
	"os.file.delete": {Params: []*Type{Str}, Ret: ResultOf(Void)},
	"os.file.rename": {Params: []*Type{Str, Str}, Ret: ResultOf(Void)},
	"os.dir.is":      {Params: []*Type{Str}, Ret: Bool},
	"os.dir.list":    {Params: []*Type{Str}, Ret: ResultOf(ListOf(Str))},
	"os.dir.make":    {Params: []*Type{Str}, Ret: ResultOf(Void)},
	"os.dir.delete":  {Params: []*Type{Str}, Ret: ResultOf(Void)},
	"os.run":         {Params: []*Type{Str, ListOf(Str)}, Ret: ResultOf(Str)},
	"url.build":      {Params: []*Type{Str, MapOf(Str, Str)}, Ret: Str},
	"json.encode":    {Params: []*Type{Any}, Ret: Str},
	"json.pretty":    {Params: []*Type{Any}, Ret: Str},
	"json.get":       {Params: []*Type{Str, Str}, Ret: Str},
	"json.int":       {Params: []*Type{Str, Str}, Ret: Int},
	"json.num":       {Params: []*Type{Str, Str}, Ret: Float},
	"json.bool":      {Params: []*Type{Str, Str}, Ret: Bool},
	"json.count":     {Params: []*Type{Str, Str}, Ret: Int},
	"json.has":       {Params: []*Type{Str, Str}, Ret: Bool},
	"json.keys":      {Params: []*Type{Str}, Ret: ListOf(Str)},
	"json.valid":     {Params: []*Type{Str}, Ret: Bool},

	// The memory library. There is a real collector behind collect();
	// the counters come from the object list it walks.
	// Raw memory; see memlib.go. An address is an int.
	"mem.alloc":    {Params: []*Type{Int}, Ret: Int},
	"mem.resize":   {Params: []*Type{Int, Int}, Ret: Int},
	"mem.free":     {Params: []*Type{Int}, Ret: Void},
	"mem.readU8":   {Params: []*Type{Int}, Ret: Int},
	"mem.readI8":   {Params: []*Type{Int}, Ret: Int},
	"mem.readU16":  {Params: []*Type{Int}, Ret: Int},
	"mem.readI16":  {Params: []*Type{Int}, Ret: Int},
	"mem.readU32":  {Params: []*Type{Int}, Ret: Int},
	"mem.readI32":  {Params: []*Type{Int}, Ret: Int},
	"mem.readI64":  {Params: []*Type{Int}, Ret: Int},
	"mem.readF32":  {Params: []*Type{Int}, Ret: Float},
	"mem.readF64":  {Params: []*Type{Int}, Ret: Float},
	"mem.write8":   {Params: []*Type{Int, Int}, Ret: Void},
	"mem.write16":  {Params: []*Type{Int, Int}, Ret: Void},
	"mem.write32":  {Params: []*Type{Int, Int}, Ret: Void},
	"mem.write64":  {Params: []*Type{Int, Int}, Ret: Void},
	"mem.writeF32": {Params: []*Type{Int, Numeric}, Ret: Void},
	"mem.writeF64": {Params: []*Type{Int, Numeric}, Ret: Void},
	"mem.copy":     {Params: []*Type{Int, Int, Int}, Ret: Void},
	"mem.fill":     {Params: []*Type{Int, Int, Int}, Ret: Void},
	"mem.str":      {Params: []*Type{Int}, Ret: Str},
	"mem.strN":     {Params: []*Type{Int, Int}, Ret: Str},
	"mem.bytes":    {Params: []*Type{Int, Int}, Ret: Bytes},
	"mem.addr":     {Params: []*Type{Any}, Ret: Int},
	"mem.protect":  {Params: []*Type{Int, Int, Str}, Ret: Bool},
	"mem.scan":     {Params: []*Type{Int, Int, Str}, Ret: Int},
	"mem.call":     {Params: []*Type{Int}, Rest: Any, Ret: Int},
	"mem.callF":    {Params: []*Type{Int}, Rest: Any, Ret: Float},
	"mem.symbol":   {Params: []*Type{Str, Str}, Ret: Int},

	// proc: another process's memory, the standard Windows debug
	// surface. See prelude_proc.go.
	"proc.current":  {Ret: Int},
	"proc.pid":      {Ret: Int},
	"proc.open":     {Params: []*Type{Int}, Ret: Int},
	"proc.close":    {Params: []*Type{Int}, Ret: Bool},
	"proc.read":     {Params: []*Type{Int, Int, Int}, Ret: Bytes},
	"proc.write":    {Params: []*Type{Int, Int, Bytes}, Ret: Int},
	"proc.readU8":   {Params: []*Type{Int, Int}, Ret: Int},
	"proc.readI32":  {Params: []*Type{Int, Int}, Ret: Int},
	"proc.readI64":  {Params: []*Type{Int, Int}, Ret: Int},
	"proc.writeI32": {Params: []*Type{Int, Int, Int}, Ret: Bool},
	"proc.writeI64": {Params: []*Type{Int, Int, Int}, Ret: Bool},
	"proc.scan":     {Params: []*Type{Int, Int, Int, Str}, Ret: Int},
	"proc.pids":     {Ret: ListOf(Int)},
	"proc.name":     {Params: []*Type{Int}, Ret: Str},
	"proc.parent":   {Params: []*Type{Int}, Ret: Int},
	"proc.find":     {Params: []*Type{Str}, Ret: Int},
	"proc.modules":  {Params: []*Type{Int}, Ret: ListOf(StructOf("ProcModule"))},
	"proc.base":     {Params: []*Type{Int, Str}, Ret: Int},
	"proc.threads":  {Params: []*Type{Int}, Ret: ListOf(Int)},
	"proc.regions":  {Params: []*Type{Int}, Ret: ListOf(StructOf("ProcRegion"))},
	"proc.scanAll":  {Params: []*Type{Int, Str}, Ret: ListOf(Int)},

	"mem.used":        {Ret: Int},
	"mem.total":       {Ret: Int},
	"mem.system":      {Ret: Int},
	"mem.objects":     {Ret: Int},
	"mem.collections": {Ret: Int},
	"mem.goroutines":  {Ret: Int},
	"mem.collect":     {Ret: Void},

	"thread.spawn":     {Params: []*Type{FuncOf(nil, Void)}, Ret: Int},
	"thread.join":      {Params: []*Type{Int}, Ret: Void},
	"thread.id":        {Ret: Int},
	"thread.cores":     {Ret: Int},
	"thread.mutex":     {Ret: Int},
	"thread.lock":      {Params: []*Type{Int}, Ret: Void},
	"thread.unlock":    {Params: []*Type{Int}, Ret: Void},
	"thread.cond":      {Ret: Int},
	"thread.wait":      {Params: []*Type{Int, Int}, Ret: Void},
	"thread.notify":    {Params: []*Type{Int}, Ret: Void},
	"thread.notifyAll": {Params: []*Type{Int}, Ret: Void},
	"atomic.new":       {Params: []*Type{Int}, Ret: Int},
	"atomic.add":       {Params: []*Type{Int, Int}, Ret: Int},
	"atomic.get":       {Params: []*Type{Int}, Ret: Int},
	"atomic.set":       {Params: []*Type{Int, Int}, Ret: Void},
	"atomic.swap":      {Params: []*Type{Int, Int}, Ret: Int},
	"atomic.cas":       {Params: []*Type{Int, Int, Int}, Ret: Bool},
	"delete":           {Params: []*Type{Any}, Ret: Void},
	"os.file.readOr":   {Params: []*Type{Str, Str}, Ret: Str},
	"os.path.base":     {Params: []*Type{Str}, Ret: Str},
	"os.path.dir":      {Params: []*Type{Str}, Ret: Str},
	"os.path.ext":      {Params: []*Type{Str}, Ret: Str},
	"os.pid":           {Ret: Int},
	"os.cpus":          {Ret: Int},
	"os.hostname":      {Ret: Str},
	"os.name":          {Ret: Str},
	"os.arch":          {Ret: Str},
	"os.dir.current":   {Ret: Str},
	"os.dir.change":    {Params: []*Type{Str}, Ret: ResultOf(Void)},
	"os.dir.home":      {Ret: Str},
	"os.dir.temp":      {Ret: Str},
	"os.path.absolute": {Params: []*Type{Str}, Ret: Str},
	"os.path.clean":    {Params: []*Type{Str}, Ret: Str},
	"replace":          {Params: []*Type{Str, Str, Str}, Ret: Str},

	// The older spellings the Go backend still accepts. Same functions,
	// so they are the same names here rather than a second lowering.
	"os.read.file":   {Params: []*Type{Str}, Ret: ResultOf(Str)},
	"os.write.file":  {Params: []*Type{Str, Str}, Ret: ResultOf(Void)},
	"os.list.dir":    {Params: []*Type{Str}, Ret: ResultOf(ListOf(Str))},
	"os.make.dir":    {Params: []*Type{Str}, Ret: ResultOf(Void)},
	"os.delete.file": {Params: []*Type{Str}, Ret: ResultOf(Void)},
	"os.append.file": {Params: []*Type{Str, Str}, Ret: ResultOf(Void)},
}

func (asmLibrary) Signature(name string) (front.Signature, bool) {
	// The polymorphic ones. A fixed Params/Ret cannot say "any T!" or
	// "a str or a list", so each carries its own check, the same way the
	// Go backend's overloaded builtins do.
	switch name {
	case "len":
		return front.Signature{Check: checkLen}, true
	case "push":
		return front.Signature{Check: checkPush}, true
	case "contains":
		return front.Signature{Check: checkContains}, true
	case "indexOf":
		return front.Signature{Check: checkIndexOf}, true
	case "has":
		return front.Signature{Check: checkHas}, true
	case "find":
		return front.Signature{Check: checkFind}, true
	case "min", "max":
		return front.Signature{Check: checkMinMax}, true
	case "map":
		return front.Signature{Check: checkMap}, true
	case "filter":
		return front.Signature{Check: checkFilter}, true
	case "reduce":
		return front.Signature{Check: checkReduce}, true
	case "sortBy":
		return front.Signature{Check: checkSortBy}, true
	case "any":
		return front.Signature{Check: checkAnyAll("any")}, true
	case "all":
		return front.Signature{Check: checkAnyAll("all")}, true
	case "each":
		return front.Signature{Check: checkEach}, true
	case "task.map":
		return front.Signature{Check: checkTaskMap}, true
	case "task.mapLimit":
		return front.Signature{Check: checkTaskMapLimit}, true
	case "task.each":
		return front.Signature{Check: checkTaskEach}, true
	case "task.all":
		return front.Signature{Check: checkTaskAll}, true
	case "http.serve":
		return front.Signature{Check: checkHTTPServe}, true
	case "stats.mean", "stats.median", "stats.var", "stats.stdev":
		return front.Signature{Check: checkNumberList}, true
	case "stats.percentile":
		return front.Signature{Check: checkPercentile}, true
	case "json.decode":
		return front.Signature{Check: checkJSONDecode, WantsTarget: true}, true
	case "json.decodeOr":
		return front.Signature{Check: checkJSONDecodeOr}, true
	case "os.path.join":
		return front.Signature{Check: checkPathJoin}, true
	case "toInt":
		return front.Signature{Check: checkToInt}, true
	case "toFloat":
		return front.Signature{Check: checkOptional("toFloat", []*Type{Str, Numeric}, 1, Float)}, true
	case "input":
		return front.Signature{Check: checkOptional("input", []*Type{Str}, 0, Str)}, true
	case "padLeft":
		return front.Signature{Check: checkOptional("padLeft", []*Type{Str, Int, Str}, 2, Str)}, true
	case "padRight":
		return front.Signature{Check: checkOptional("padRight", []*Type{Str, Int, Str}, 2, Str)}, true
	case "first", "last", "pop":
		return front.Signature{Check: checkElemOf}, true
	case "sum":
		return front.Signature{Check: checkSum}, true
	case "reverse", "sort", "rand.shuffle":
		return front.Signature{Check: checkSameList}, true
	case "rand.sample":
		return front.Signature{Check: checkSample}, true
	case "rand.pick":
		return front.Signature{Check: checkElemOf}, true
	case "slice":
		return front.Signature{Check: checkSlice}, true
	case "join":
		return front.Signature{Check: checkJoin}, true
	case "insert":
		return front.Signature{Check: checkInsert}, true
	case "removeAt":
		return front.Signature{Check: checkRemoveAt}, true
	case "keys":
		return front.Signature{Check: checkKeys}, true
	case "values":
		return front.Signature{Check: checkValues}, true
	case "remove":
		return front.Signature{Check: checkRemove}, true
	case "clear":
		return front.Signature{Check: checkClear}, true

	// The error type. These mirror the Go backend's declarations exactly,
	// because a program that type-checks on one backend and not on the
	// other would be a language with two definitions.
	case "fail":
		return front.Signature{Params: []*Type{Str}, Ret: ErrLitT}, true
	case "ok":
		return front.Signature{Check: checkOk}, true
	case "isOk":
		return front.Signature{Check: checkIsOk}, true
	case "failed":
		return front.Signature{Check: checkFailed}, true
	case "errorOf":
		return front.Signature{Check: checkErrorOf}, true
	case "must":
		return front.Signature{Check: checkMust, WantsTarget: true, HintFor: hintResult}, true
	case "valueOr":
		return front.Signature{Check: checkValueOr, WantsTarget: true, HintFor: hintValueOr}, true
	}
	s, ok := sigs[name]
	return s, ok
}

// ConstType reports the builtin float constants. NAN and INF were left
// out until the float comparisons were NaN-correct and the printer wrote
// Go's +Inf, -Inf and NaN rather than msvcrt's 1.#INF.
func (asmLibrary) ConstType(name string) (*Type, bool) {
	switch name {
	case "PI", "E", "INF", "NAN":
		return Float, true
	}
	return nil, false
}

func checkLen(c *Checker, x *Call, args []*Type) *Type {
	if len(args) != 1 {
		c.ErrorAt(x, "len takes 1 argument, got %d", len(args))
		return Unknown
	}
	switch args[0].Kind {
	case KStr, KList, KMap, KBytes:
		return Int
	}
	c.ErrorAt(x, "len needs a str, a list or a map, got %s", args[0])
	return Unknown
}

// checkHas mirrors the Go backend's declaration exactly. A program that
// type-checks on one backend and not the other would be a language with
// two definitions.
func checkHas(c *Checker, x *Call, args []*Type) *Type {
	if len(args) != 2 {
		c.ErrorAt(x, "has takes 2 arguments, got %d", len(args))
		return Bool
	}
	if args[0].IsUnknown() {
		return Bool
	}
	if args[0].Kind != KMap {
		c.ErrorAt(x.Args[0], "has expects a map, got %s", args[0])
		return Bool
	}
	if !args[1].IsUnknown() && !args[1].Equal(args[0].Key) {
		c.ErrorAt(x.Args[1], "has expects %s for argument 2, got %s", args[0].Key, args[1])
	}
	return Bool
}

// mapArg is the shared front of keys, values and remove: one map
// argument, or a type error naming what came instead.
func mapArg(c *Checker, x *Call, args []*Type, name string, want int) (*Type, bool) {
	if len(args) != want {
		c.ErrorAt(x, "%s takes %d argument(s), got %d", name, want, len(args))
		return nil, false
	}
	if args[0].IsUnknown() {
		return nil, false
	}
	if args[0].Kind != KMap {
		c.ErrorAt(x.Args[0], "%s expects a map, got %s", name, args[0])
		return nil, false
	}
	return args[0], true
}

// checkMinMax mirrors the Go backend: two or more numbers, and the type
// of the first one back.
func checkMinMax(c *Checker, x *Call, args []*Type) *Type {
	if len(args) < 2 {
		c.ErrorAt(x, "min and max take at least 2 arguments, got %d", len(args))
		return Unknown
	}
	for i, a := range args {
		if a.IsUnknown() {
			continue
		}
		if a.Kind != KInt && a.Kind != KFloat {
			c.ErrorAt(x.Args[i], "min and max need numbers, got %s", a)
			return Unknown
		}
	}
	return args[0]
}

// listArg is the shared front of the list library: one list argument,
// or a type error naming what came instead.
func listArg(c *Checker, x *Call, args []*Type, name string, want int) (*Type, bool) {
	if len(args) != want {
		c.ErrorAt(x, "%s takes %d argument(s), got %d", name, want, len(args))
		return nil, false
	}
	if args[0].IsUnknown() {
		return nil, false
	}
	if args[0].Kind != KList {
		c.ErrorAt(x.Args[0], "%s expects a list, got %s", name, args[0])
		return nil, false
	}
	return args[0], true
}

func checkElemOf(c *Checker, x *Call, args []*Type) *Type {
	xs, ok := listArg(c, x, args, "this", 1)
	if !ok {
		return Unknown
	}
	return xs.Elem
}

func checkSum(c *Checker, x *Call, args []*Type) *Type {
	xs, ok := listArg(c, x, args, "sum", 1)
	if !ok {
		return Unknown
	}
	if xs.Elem.Kind != KInt && xs.Elem.Kind != KFloat {
		c.ErrorAt(x.Args[0], "sum needs a list of numbers, got %s", xs)
		return Unknown
	}
	return xs.Elem
}

func checkSameList(c *Checker, x *Call, args []*Type) *Type {
	xs, ok := listArg(c, x, args, "this", 1)
	if !ok {
		return Unknown
	}
	return xs
}

func checkSlice(c *Checker, x *Call, args []*Type) *Type {
	xs, ok := listArg(c, x, args, "slice", 3)
	if !ok {
		return Unknown
	}
	wantInt(c, x, args, 1, "slice")
	wantInt(c, x, args, 2, "slice")
	return xs
}

func checkJoin(c *Checker, x *Call, args []*Type) *Type {
	if _, ok := listArg(c, x, args, "join", 2); !ok {
		return Str
	}
	if !args[1].IsUnknown() && args[1].Kind != KStr {
		c.ErrorAt(x.Args[1], "join expects str for argument 2, got %s", args[1])
	}
	return Str
}

func checkInsert(c *Checker, x *Call, args []*Type) *Type {
	xs, ok := listArg(c, x, args, "insert", 3)
	if !ok {
		return Void
	}
	wantInt(c, x, args, 1, "insert")
	if !args[2].IsUnknown() && !args[2].Equal(xs.Elem) {
		c.ErrorAt(x.Args[2], "insert expects %s for argument 3, got %s", xs.Elem, args[2])
	}
	return Void
}

func checkRemoveAt(c *Checker, x *Call, args []*Type) *Type {
	xs, ok := listArg(c, x, args, "removeAt", 2)
	if !ok {
		return Unknown
	}
	wantInt(c, x, args, 1, "removeAt")
	return xs.Elem
}

func wantInt(c *Checker, x *Call, args []*Type, i int, name string) {
	if !args[i].IsUnknown() && args[i].Kind != KInt {
		c.ErrorAt(x.Args[i], "%s expects int for argument %d, got %s", name, i+1, args[i])
	}
}

// checkToInt takes an optional fallback, which is what the Go backend's
// minArgs 1 / maxArgs 2 means.
func checkToInt(c *Checker, x *Call, args []*Type) *Type {
	if len(args) < 1 || len(args) > 2 {
		c.ErrorAt(x, "toInt takes 1 or 2 arguments, got %d", len(args))
		return Int
	}
	if !args[0].IsUnknown() && args[0].Kind != KStr {
		c.ErrorAt(x.Args[0], "toInt expects str for argument 1, got %s", args[0])
	}
	if len(args) == 2 && !args[1].IsUnknown() && args[1].Kind != KInt {
		c.ErrorAt(x.Args[1], "toInt expects int for argument 2, got %s", args[1])
	}
	return Int
}

// checkOptional checks a builtin whose trailing parameters may be left
// off: the first `required` of params must be given, the rest may be.
// What an omitted one defaults to is the lowerer's business - see
// preludeDefaults.
func checkOptional(name string, params []*Type, required int, ret *Type) func(*Checker, *Call, []*Type) *Type {
	return func(c *Checker, x *Call, args []*Type) *Type {
		if len(args) < required || len(args) > len(params) {
			if required == len(params)-1 {
				c.ErrorAt(x, "%s takes %d or %d arguments, got %d", name, required, len(params), len(args))
			} else {
				c.ErrorAt(x, "%s takes %d to %d arguments, got %d", name, required, len(params), len(args))
			}
			return ret
		}
		for i, a := range args {
			want := params[i]
			if a.IsUnknown() {
				continue
			}
			if want == Numeric {
				if a.Kind != KInt && a.Kind != KFloat {
					c.ErrorAt(x.Args[i], "%s expects a number for argument %d, got %s", name, i+1, a)
				}
				continue
			}
			if !a.Equal(want) {
				c.ErrorAt(x.Args[i], "%s expects %s for argument %d, got %s", name, want, i+1, a)
			}
		}
		return ret
	}
}

// checkFind is has() that hands back the value: a ?V, so that absent and
// zero stay apart where a bare index cannot tell them apart.
func checkFind(c *Checker, x *Call, args []*Type) *Type {
	m, ok := mapArg(c, x, args, "find", 2)
	if !ok {
		return Unknown
	}
	if !args[1].IsUnknown() && !args[1].Equal(m.Key) {
		c.ErrorAt(x.Args[1], "find expects %s for argument 2, got %s", m.Key, args[1])
	}
	return NullableOf(m.Elem)
}

// checkPathJoin is variadic over strings.
func checkPathJoin(c *Checker, x *Call, args []*Type) *Type {
	if len(args) < 1 {
		c.ErrorAt(x, "os.path.join takes at least 1 argument")
		return Str
	}
	for i, a := range args {
		if !a.IsUnknown() && a.Kind != KStr {
			c.ErrorAt(x.Args[i], "os.path.join expects str for argument %d, got %s", i+1, a)
		}
	}
	return Str
}

// checkJSONDecode takes its result type from the binding it is assigned
// to, exactly as the Go backend's declaration does. The annotation
// carries the wrapper too, so `let p: Point! = json.decode(t)` names
// Point as the shape to build and Point! as what the call produces.
func checkJSONDecode(c *Checker, x *Call, args []*Type) *Type {
	if len(args) != 1 {
		c.ErrorAt(x, "json.decode takes 1 argument, got %d", len(args))
		return Unknown
	}
	if !args[0].IsUnknown() && args[0].Kind != KStr {
		c.ErrorAt(x.Args[0], "json.decode expects str, got %s", args[0])
	}
	if x.Want == nil || x.Want.IsUnknown() || !x.Want.IsResult() {
		c.ErrorAt(x, "json.decode needs to know what to decode into, and it can fail - "+
			"annotate the variable, as in: let p: Point! = json.decode(text)")
		return Unknown
	}
	return x.Want
}

// checkJSONDecodeOr takes its shape from the fallback, so it needs no
// annotation at all.
func checkJSONDecodeOr(c *Checker, x *Call, args []*Type) *Type {
	if len(args) != 2 {
		c.ErrorAt(x, "json.decodeOr takes 2 arguments, got %d", len(args))
		return Unknown
	}
	if !args[0].IsUnknown() && args[0].Kind != KStr {
		c.ErrorAt(x.Args[0], "json.decodeOr expects str for argument 1, got %s", args[0])
	}
	if args[1].IsUnknown() {
		return Unknown
	}
	return args[1]
}

func checkClear(c *Checker, x *Call, args []*Type) *Type {
	if len(args) != 1 {
		c.ErrorAt(x, "clear takes 1 argument, got %d", len(args))
		return Void
	}
	switch args[0].Kind {
	case KMap, KList, KUnknown:
		return Void
	}
	c.ErrorAt(x.Args[0], "clear expects a list or a map, got %s", args[0])
	return Void
}

func checkKeys(c *Checker, x *Call, args []*Type) *Type {
	m, ok := mapArg(c, x, args, "keys", 1)
	if !ok {
		return Unknown
	}
	return ListOf(m.Key)
}

func checkValues(c *Checker, x *Call, args []*Type) *Type {
	m, ok := mapArg(c, x, args, "values", 1)
	if !ok {
		return Unknown
	}
	return ListOf(m.Elem)
}

func checkRemove(c *Checker, x *Call, args []*Type) *Type {
	m, ok := mapArg(c, x, args, "remove", 2)
	if !ok {
		return Void
	}
	if !args[1].IsUnknown() && !args[1].Equal(m.Key) {
		c.ErrorAt(x.Args[1], "remove expects %s for argument 2, got %s", m.Key, args[1])
	}
	return Void
}

// checkContains and checkIndexOf take a str or a list, and mirror the Go
// backend down to the wording of the error that steers a map to has().
func seqArg(c *Checker, x *Call, args []*Type, name string) bool {
	if len(args) != 2 {
		c.ErrorAt(x, "%s takes 2 arguments, got %d", name, len(args))
		return false
	}
	if args[0].IsUnknown() {
		return false
	}
	switch args[0].Kind {
	case KStr:
		if !args[1].IsUnknown() && args[1].Kind != KStr {
			c.ErrorAt(x.Args[1], "%s expects str for argument 2, got %s", name, args[1])
		}
	case KList:
		if !args[1].IsUnknown() && !args[1].Equal(args[0].Elem) &&
			!(args[0].Elem.IsFixed() && c.Coerce(&x.Args[1], args[0].Elem, args[1])) {
			c.ErrorAt(x.Args[1], "%s expects %s for argument 2, got %s",
				name, args[0].Elem, args[1])
		}
	case KMap:
		c.ErrorAt(x.Args[0], "use has(map, key) to test a map")
	default:
		c.ErrorAt(x.Args[0], "%s expects a str or a list, got %s", name, args[0])
	}
	return true
}

func checkContains(c *Checker, x *Call, args []*Type) *Type {
	seqArg(c, x, args, "contains")
	return Bool
}

func checkIndexOf(c *Checker, x *Call, args []*Type) *Type {
	seqArg(c, x, args, "indexOf")
	return Int
}

// checkPush is variadic: push(xs, a, b, c) appends all three, which is
// what the Go backend accepts.
func checkPush(c *Checker, x *Call, args []*Type) *Type {
	if len(args) < 2 {
		c.ErrorAt(x, "push takes at least 2 arguments, got %d", len(args))
		return Unknown
	}
	if args[0].IsUnknown() {
		return Void
	}
	if args[0].Kind != KList {
		c.ErrorAt(x, "push needs a list, got %s", args[0])
		return Unknown
	}
	for i := 1; i < len(args); i++ {
		// A struct pushed onto a list of an interface becomes one.
		if !args[i].IsUnknown() && !args[i].Equal(args[0].Elem) &&
			!((args[0].Elem.Kind == KStruct || args[0].Elem.IsFixed()) &&
				c.Coerce(&x.Args[i], args[0].Elem, args[i])) {
			c.ErrorAt(x.Args[i], "cannot push %s into %s", args[i], args[0])
			return Unknown
		}
	}
	return Void
}

// wantResult is the shared front half of every builtin that takes a
// single T! and does something with it. It returns the inner T, or
// Unknown once it has reported why there is not one.
func wantResult(c *Checker, x *Call, args []*Type, name string) *Type {
	if len(args) != 1 {
		c.ErrorAt(x, "%s takes 1 argument, got %d", name, len(args))
		return Unknown
	}
	if args[0].IsUnknown() {
		return Unknown
	}
	if !args[0].IsResult() {
		c.ErrorAt(x, "%s needs a T!, got %s", name, args[0])
		return Unknown
	}
	return args[0].Elem
}

// ok() is the counterpart to fail() for an action that produces no
// value: `return ok()` in a function declared void!.
func checkOk(c *Checker, x *Call, args []*Type) *Type {
	if len(args) != 0 {
		c.ErrorAt(x, "ok takes no arguments, got %d", len(args))
	}
	return ResultOf(Void)
}

func checkIsOk(c *Checker, x *Call, args []*Type) *Type {
	wantResult(c, x, args, "isOk")
	return Bool
}

func checkFailed(c *Checker, x *Call, args []*Type) *Type {
	wantResult(c, x, args, "failed")
	return Bool
}

func checkErrorOf(c *Checker, x *Call, args []*Type) *Type {
	wantResult(c, x, args, "errorOf")
	return Str
}

func checkMust(c *Checker, x *Call, args []*Type) *Type {
	return wantResult(c, x, args, "must")
}

func checkValueOr(c *Checker, x *Call, args []*Type) *Type {
	if len(args) != 2 {
		c.ErrorAt(x, "valueOr takes 2 arguments, got %d", len(args))
		return Unknown
	}
	inner := wantResult(c, x, args[:1], "valueOr")
	if inner.IsUnknown() || args[1].IsUnknown() {
		return inner
	}
	if !args[1].Equal(inner) {
		c.ErrorAt(x, "valueOr needs a %s to fall back on, got %s", inner, args[1])
	}
	return inner
}

// hintResult carries the type the context wants inwards, so that the
// argument of must() knows which result it is meant to produce.
func hintResult(x *Call, known []*Type, i int) *Type {
	if i == 0 && x.Want != nil && !x.Want.IsUnknown() {
		return ResultOf(x.Want)
	}
	return nil
}

// hintValueOr does the same, and additionally tells the fallback which
// type it has to be once the result argument is known.
func hintValueOr(x *Call, known []*Type, i int) *Type {
	if i == 0 && x.Want != nil && !x.Want.IsUnknown() {
		return ResultOf(x.Want)
	}
	if i == 1 && len(known) == 1 && known[0].IsResult() {
		return known[0].Elem
	}
	return nil
}

// checkNumberList is the shape every stats function but percentile has:
// one list of numbers in, one float out. A list of ints is accepted and
// widened, the same way the Go backend widens before calling.
func checkNumberList(c *Checker, x *Call, args []*Type) *Type {
	if len(args) != 1 {
		c.ErrorAt(x, "this takes 1 argument, got %d", len(args))
		return Float
	}
	if args[0].IsUnknown() {
		return Float
	}
	if args[0].Kind != KList || (args[0].Elem.Kind != KInt && args[0].Elem.Kind != KFloat) {
		c.ErrorAt(x.Args[0], "expected a list of numbers, got %s", args[0])
	}
	return Float
}

func checkPercentile(c *Checker, x *Call, args []*Type) *Type {
	if len(args) != 2 {
		c.ErrorAt(x, "stats.percentile takes 2 arguments, got %d", len(args))
		return Float
	}
	checkNumberList(c, x, args[:1])
	if !args[1].IsUnknown() && args[1].Kind != KInt && args[1].Kind != KFloat {
		c.ErrorAt(x.Args[1], "stats.percentile expects a number, got %s", args[1])
	}
	return Float
}

// checkSample is rand.sample: a list and a count, the same list type
// back. Declared like the Go backend declares it, so a program that
// type-checks there type-checks here.
func checkSample(c *Checker, x *Call, args []*Type) *Type {
	if len(args) != 2 {
		c.ErrorAt(x, "rand.sample takes 2 arguments, got %d", len(args))
		return Unknown
	}
	if args[0].IsUnknown() {
		return args[0]
	}
	if args[0].Kind != KList {
		c.ErrorAt(x.Args[0], "rand.sample expects a list, got %s", args[0])
		return Unknown
	}
	if !args[1].IsUnknown() && args[1].Kind != KInt {
		c.ErrorAt(x.Args[1], "rand.sample expects a count, got %s", args[1])
	}
	return args[0]
}

// ---- higher-order ----
//
// These mirror the Go backend's declarations, including their wording.
// A program that type-checks on one backend and not on the other would
// be a language with two definitions, and the messages are the part a
// user actually reads.

func hofList(c *Checker, x *Call, i int, args []*Type, fn string) *Type {
	if i >= len(args) {
		return Unknown
	}
	t := args[i]
	if t.IsUnknown() {
		return Unknown
	}
	if t.Kind != KList {
		c.ErrorAt(x.Args[i], "%s expects a list, got %s", fn, t)
		return Unknown
	}
	return t.Elem
}

func hofCallback(c *Checker, x *Call, i int, args []*Type, fn string, params []*Type) *Type {
	if i >= len(args) || args[i].IsUnknown() {
		return Unknown
	}
	got := args[i]
	if !got.IsFunc() {
		c.ErrorAt(x.Args[i], "%s expects a function here, got %s", fn, got)
		return Unknown
	}
	if len(got.Params) != len(params) {
		c.ErrorAt(x.Args[i], "%s expects a function taking %d argument(s), got one taking %d",
			fn, len(params), len(got.Params))
		return Unknown
	}
	for j, want := range params {
		if want.IsUnknown() || want.Accepts(got.Params[j]) {
			continue
		}
		c.ErrorAt(x.Args[i], "%s passes %s to its function, but it takes %s",
			fn, want, got.Params[j])
		return Unknown
	}
	return got.Elem
}

func hofPredicate(c *Checker, x *Call, i int, args []*Type, fn string, params []*Type) {
	ret := hofCallback(c, x, i, args, fn, params)
	if !ret.IsUnknown() && ret.Kind != KBool {
		c.ErrorAt(x.Args[i], "%s needs a function returning bool, got one returning %s", fn, ret)
	}
}

func checkMap(c *Checker, x *Call, args []*Type) *Type {
	elem := hofList(c, x, 0, args, "map")
	ret := hofCallback(c, x, 1, args, "map", []*Type{elem})
	if elem.IsUnknown() || ret.IsUnknown() {
		return Unknown
	}
	if ret.Kind == KVoid {
		c.ErrorAt(x.Args[1], "map needs a function that returns something - use each(...) to just do work")
		return Unknown
	}
	return ListOf(ret)
}

func checkFilter(c *Checker, x *Call, args []*Type) *Type {
	elem := hofList(c, x, 0, args, "filter")
	hofPredicate(c, x, 1, args, "filter", []*Type{elem})
	if elem.IsUnknown() {
		return Unknown
	}
	return ListOf(elem)
}

func checkReduce(c *Checker, x *Call, args []*Type) *Type {
	elem := hofList(c, x, 0, args, "reduce")
	if len(args) < 3 || elem.IsUnknown() {
		return Unknown
	}
	acc := args[1]
	ret := hofCallback(c, x, 2, args, "reduce", []*Type{acc, elem})
	if !ret.IsUnknown() && !acc.Equal(ret) {
		c.ErrorAt(x.Args[2], "reduce starts with %s, so its function must return %s, not %s",
			acc, acc, ret)
		return Unknown
	}
	return acc
}

func checkSortBy(c *Checker, x *Call, args []*Type) *Type {
	elem := hofList(c, x, 0, args, "sortBy")
	hofPredicate(c, x, 1, args, "sortBy", []*Type{elem, elem})
	if elem.IsUnknown() {
		return Unknown
	}
	return ListOf(elem)
}

func checkAnyAll(name string) func(*Checker, *Call, []*Type) *Type {
	return func(c *Checker, x *Call, args []*Type) *Type {
		elem := hofList(c, x, 0, args, name)
		hofPredicate(c, x, 1, args, name, []*Type{elem})
		return Bool
	}
}

func checkEach(c *Checker, x *Call, args []*Type) *Type {
	elem := hofList(c, x, 0, args, "each")
	hofCallback(c, x, 1, args, "each", []*Type{elem})
	return Void
}

// The task family. Same shapes as the Go backend's, same wording, so a
// program rejected there is rejected here for the same reason.
func checkTaskMap(c *Checker, x *Call, args []*Type) *Type {
	elem := hofList(c, x, 0, args, "task.map")
	ret := hofCallback(c, x, 1, args, "task.map", []*Type{elem})
	if elem.IsUnknown() || ret.IsUnknown() {
		return Unknown
	}
	if ret.Kind == KVoid {
		c.ErrorAt(x.Args[1], "task.map needs a function that returns something - "+
			"use task.each(...) to just do work")
		return Unknown
	}
	return ListOf(ret)
}

func checkTaskMapLimit(c *Checker, x *Call, args []*Type) *Type {
	elem := hofList(c, x, 0, args, "task.mapLimit")
	if len(args) > 1 && !args[1].IsUnknown() && args[1].Kind != KInt {
		c.ErrorAt(x.Args[1], "task.mapLimit needs an int for argument 2, got %s", args[1])
	}
	ret := hofCallback(c, x, 2, args, "task.mapLimit", []*Type{elem})
	if elem.IsUnknown() || ret.IsUnknown() {
		return Unknown
	}
	if ret.Kind == KVoid {
		c.ErrorAt(x.Args[2], "task.mapLimit needs a function that returns something")
		return Unknown
	}
	return ListOf(ret)
}

func checkTaskEach(c *Checker, x *Call, args []*Type) *Type {
	elem := hofList(c, x, 0, args, "task.each")
	hofCallback(c, x, 1, args, "task.each", []*Type{elem})
	return Void
}

// http.serve(port, fn(Request) -> Response). The callback is checked
// the way the task family's is, so a handler with the wrong shape is an
// error naming what it should have been rather than a lowering failure.
func checkHTTPServe(c *Checker, x *Call, args []*Type) *Type {
	if len(args) != 2 {
		c.ErrorAt(x, "http.serve takes 2 arguments, got %d", len(args))
		return ResultOf(Void)
	}
	if !args[0].IsUnknown() && args[0].Kind != KInt {
		c.ErrorAt(x.Args[0], "http.serve needs an int port, got %s", args[0])
	}
	hofCallback(c, x, 1, args, "http.serve", []*Type{StructOf("Request")})
	if got := args[1]; got.IsFunc() && !got.Elem.IsUnknown() {
		if got.Elem.Kind != KStruct || got.Elem.Name != "Response" {
			c.ErrorAt(x.Args[1],
				"http.serve needs a handler returning Response, got one returning %s", got.Elem)
		}
	}
	return ResultOf(Void)
}

func checkTaskAll(c *Checker, x *Call, args []*Type) *Type {
	elem := hofList(c, x, 0, args, "task.all")
	if elem.IsUnknown() {
		return Void
	}
	if !elem.IsFunc() || len(elem.Params) != 0 || elem.Elem.Kind != KVoid {
		c.ErrorAt(x.Args[0], "task.all expects a list of fn() taking nothing "+
			"and returning nothing, got []%s", elem)
	}
	return Void
}
