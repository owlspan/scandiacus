package frontend

// Every node knows where it came from. This is what makes error
// messages and //line directives possible.
type Node interface {
	Pos() (line, col int)
}

type Span struct {
	Line int
	Col  int
}

func (p Span) Pos() (int, int) { return p.Line, p.Col }

// setPos lets the parser move a whole subtree, which it needs for
// expressions parsed out of a string interpolation by a nested lexer
// that counted lines from one.
func (p *Span) setPos(q Span) { *p = q }

func at(t Token) Span { return Span{Line: t.Line, Col: t.Col} }

type Expr interface {
	Node
	exprNode()
}

type Stmt interface {
	Node
	stmtNode()
}

// Program is a whole .vl file: zero or more functions, plus the
// top-level statements that become main().
// Program is a whole compilation: the main file plus everything it
// imported, flattened into one set of declarations. Which file each
// declaration came from is remembered so `pub` can be enforced.
type Program struct {
	Structs []*StructDecl
	Funcs   []*FnDecl
	Globals []*LetStmt // top-level const and var, visible everywhere
	Enums   []*EnumDecl
	Ifaces  []*InterfaceDecl
	Main    []Stmt // top-level statements of the main file only
	Imports []*ImportDecl

	// MainFile is the absolute path of the file the compiler was invoked
	// on, as opposed to anything it pulled in.
	MainFile string

	// GC is "off" when the file says `gc off`: nothing is collected,
	// and delete frees a value by hand. "" is the default, collected.
	GC   string
	GCAt Span
}

// EnumDecl is `enum State { Idle, Running, Done }`. A variant is
// written State.Idle, and its value is its position in the list.
type EnumDecl struct {
	Span
	Name     string
	Variants []string
	Pub      bool
	File     string

	// Payloads, parallel to Variants, are the fields a variant carries:
	// Circle(r: float). A variant with none has nil. An enum with any
	// payload, or with type parameters, is a data enum; see
	// dataenums.go.
	Payloads   [][]Param
	TypeParams []string
	Toks       []Token
	Data       bool

	// Interface marks the enum an interface is kept in, whose variants
	// are the structs converted to it; see interfaces.go.
	Interface bool
}

// InterfaceDecl is `interface Shape { fn area(self) -> float }`: the
// methods a struct needs to be used as one.
type InterfaceDecl struct {
	Span
	Name    string
	Methods []*FnDecl // signatures only
	Pub     bool
	File    string
}

// ImportDecl is `import "helpers.vl"`. The path is relative to the file
// containing the import.
type ImportDecl struct {
	Span
	Path string
	File string // the file this import was written in
}

// ---- expressions ----

type IntLit struct {
	Span
	Val string // kept as text; Go parses it identically
}

type FloatLit struct {
	Span
	Val string
}

type StrLit struct {
	Span
	Val string // already decoded by the lexer
}

type BoolLit struct {
	Span
	Val bool
}

type Ident struct {
	Span
	Name string

	// Narrowed is set by the checker where an `if x != nil` has proved
	// this use is safe. Codegen dereferences those, and only those.
	Narrowed bool
}

// NilLit is the bare `nil`.
type NilLit struct{ Span }

// FuncLit is an anonymous function used as a value:
//
//	fn(x: int) -> int { return x * 2 }
//
// It shares FnDecl so that resolve, check and codegen can treat a
// literal and a declaration the same way.
type FuncLit struct {
	Span
	Decl *FnDecl
	T    *Type // resolved by the checker
}

func (*FuncLit) exprNode() {}

// Try is the postfix `?`: unwrap a T! here, or return its failure from
// the enclosing function. It replaces Go's four-line err check with one
// character, which is the whole reason the error type exists.
type Try struct {
	Span
	X Expr
	T *Type // the unwrapped type, filled in by the checker
}

func (*Try) exprNode() {}

// Widen boxes a plain value into a nullable one. The checker inserts
// these wherever a T is used as a ?T, so codegen never has to work out
// on its own where a pointer is needed.
type Widen struct {
	Span
	X Expr
	T *Type // the nullable type produced
}

// Convert is an untyped literal the checker has fitted to a
// fixed-width type: X is the literal, rewritten to the value the machine
// holds, and T is the type. See fixed.go.
type Convert struct {
	Span
	X Expr
	T *Type
}

// NewExpr is `new T`, `new T{...}` or `new [n]T`: zeroed memory with a
// C layout, from the C heap, freed with delete. See cstruct.go.
type NewExpr struct {
	Span
	Type  string     // T as written
	Count Expr       // n in new [n]T, or nil for one
	Lit   *StructLit // the fields in new T{...}, or nil
	T     *Type      // the pointer type produced, set by the checker
	Elem  *Type      // T itself, set by the checker
}

type Unary struct {
	Span
	Op Kind // BANG, MINUS, TILDE, STAR (read through a pointer) or AMP (address of)
	X  Expr
	T  *Type // AMP only: the pointer type, set by the checker

	// Local is set by the checker on &x for a variable x: the address
	// of the variable itself. See addr.go.
	Local bool
}

// Cast is `x as *T`: the same address, seen as another pointer type or
// back as an int. Type is as written, T resolved by the checker.
type Cast struct {
	Span
	X    Expr
	Type string
	T    *Type
}

func (*Cast) exprNode() {}

type Binary struct {
	Span
	Op   Kind
	L, R Expr

	// T is the result type, filled in by the checker. Codegen reads it to
	// tell integer division from float division.
	T *Type

	// OpT is the type of the operands, which the result type does not
	// reveal for a comparison: `xs == ys` is a bool either way, but how
	// it is compared depends on what xs is.
	OpT *Type
}

type Call struct {
	Span
	Callee Expr
	Args   []Expr

	// ArgT and T are filled in by the checker. Polymorphic builtins -
	// contains, push, join - need the argument types at codegen time to
	// decide what to emit.
	ArgT []*Type
	T    *Type

	// Want is the type this call's result is expected to produce, when
	// there is one - the annotation on `let p: Point = json.decode(s)`.
	// Only builtins that ask for it ever see this; it is how a decoder
	// learns what to decode into without Veyl having type arguments.
	Want *Type

	// ViaValue is set when the callee is a variable holding a function
	// rather than a declared name. Codegen needs it so a local called
	// `print` is not mistaken for the builtin.
	ViaValue bool

	// Lit is what the checker made of a data enum's variant built with
	// its payload, Shape.Circle(2.0): the value to build in its place.
	Lit Expr

	// Method is set by the checker when the callee turned out to be a
	// method on a struct rather than a function or a library path.
	// Codegen must not re-derive this: os.file.read and user.rename are
	// the same shape, and only the checker knows the receiver's type.
	Method bool
}

// ListLit is `[1, 2, 3]`. An empty literal carries no element type of
// its own, so it needs an annotation on the binding.
type ListLit struct {
	Span
	Elems []Expr
	T     *Type // the list type, filled in by the checker
}

// MapLit is `{"a": 1, "b": 2}`.
type MapLit struct {
	Span
	Keys []Expr
	Vals []Expr
	T    *Type // the map type, filled in by the checker
}

// Field is `a.b`. Today it only ever forms a dotted builtin path such
// as os.file.read; when structs arrive it becomes field access too.
type Field struct {
	Span
	X    Expr
	Name string

	// Lit is what the checker made of a data enum's variant with no
	// payload, Shape.Empty: the value to build in its place.
	Lit Expr
}

// DottedName flattens a chain of Field nodes rooted at an Ident into
// "os.file.read". It reports false for anything else, which is how the
// compiler tells a namespace path from an expression.
func DottedName(e Expr) (string, bool) {
	switch x := e.(type) {
	case *Ident:
		return x.Name, true
	case *Field:
		base, ok := DottedName(x.X)
		if !ok {
			return "", false
		}
		return base + "." + x.Name, true
	}
	return "", false
}

// Index is `xs[i]` for a list or `m[k]` for a map.
type Index struct {
	Span
	X   Expr
	Idx Expr
	T   *Type // the type of X, filled in by the checker
}

// InterpPart is one chunk of an interpolated string: either raw text
// (X == nil) or an embedded expression.
type InterpPart struct {
	Lit string
	X   Expr
	T   *Type // the expression's type, filled in by the checker
}

// Interp is a string literal containing {expr} holes.
type Interp struct {
	Span
	Parts []InterpPart
}

func (*IntLit) exprNode()   {}
func (*FloatLit) exprNode() {}
func (*StrLit) exprNode()   {}
func (*BoolLit) exprNode()  {}
func (*Ident) exprNode()    {}
func (*Unary) exprNode()    {}
func (*Binary) exprNode()   {}
func (*Call) exprNode()     {}
func (*Interp) exprNode()   {}
func (*Field) exprNode()    {}
func (*NilLit) exprNode()   {}
func (*Widen) exprNode()    {}
func (*Convert) exprNode()  {}
func (*NewExpr) exprNode()  {}
func (*ListLit) exprNode()  {}
func (*MapLit) exprNode()   {}
func (*Index) exprNode()    {}

// StructLit is `User{name: "ada", age: 36}`. Fields may be given in any
// order; any left out take their zero value.
type StructLit struct {
	Span
	Name   string
	Fields []string
	Vals   []Expr
	T      *Type // filled in by the checker
}

func (*StructLit) exprNode() {}

// ---- declarations ----

// StructField is one `name: type` line in a struct declaration.
type StructField struct {
	Span
	Name string
	Type string // as written
	T    *Type  // resolved by the checker

	// The rest is for an extern struct only. Type is then a C type
	// (i32, f32, ptr, another extern struct) and Len is the element
	// count of an array field, 0 for a single value. At is the offset
	// the source gave with `at`, or -1; Offset is where the checker put
	// the field, in bytes from the start.
	Len    int
	At     int
	Offset int
}

// StructDecl is `struct User { ... }`.
type StructDecl struct {
	Span
	Name   string
	Fields []StructField
	Pub    bool
	File   string
	Pkg    string // see FnDecl.Pkg

	// Extern marks `extern struct`: a C layout laid over memory the
	// program does not own, rather than a Veyl value. A value of one is
	// the address of its first byte. Size and Align are filled in by
	// the checker.
	Extern bool
	Size   int
	Align  int

	// TypeParams and Toks make a generic struct a template, as they do
	// a function; see FnDecl.
	TypeParams []string
	Toks       []Token

	// Enum is set on the struct a data enum is kept in.
	Enum *EnumDecl
}

// ImplBlock is `impl User { fn ... }`. Its methods are hoisted into the
// program's function list with a receiver attached, so most of the
// compiler never has to know impl blocks exist.
type ImplBlock struct {
	Span
	Type    string
	Methods []*FnDecl
}

type Param struct {
	Span
	Name string
	Type string // as written in the source
	T    *Type  // resolved by the checker

	// Default is the value after `=`, as in greeting: str = "hi", which a
	// call that leaves the argument off gets a copy of. A constant.
	Default Expr

	// Addressed is set by the checker when the function takes &name, so
	// a backend keeps the parameter in memory. See addr.go.
	Addressed bool
}

type FnDecl struct {
	Span
	Name   string
	Params []Param
	Ret    string // "" means the function returns nothing
	RetT   *Type  // resolved by the checker; Void when Ret is ""
	Body   *Block

	// Recv is the struct this is a method on, or "" for a plain
	// function. Methods are stored alongside functions and told apart by
	// this field alone.
	Recv string

	Pub  bool
	File string

	// Pkg is the namespace this declaration was imported under, or ""
	// for the program's own code. A package's declarations are stored
	// under "pkg.name" so two packages exporting the same name cannot
	// collide, and are reachable from outside only through that path.
	Pkg string

	// Extern marks a native function declared rather than defined:
	//
	//     extern fn MessageBoxA(hwnd: int, text: str, cap: str, kind: int) -> int
	//
	// There is no body to check; a call lowers straight to the named
	// symbol in whatever DLL the declaration names or the backend's
	// naming rule places.
	Extern bool

	// DLL is the library an extern declaration names after `from`, as in
	// `from "miniz.dll"`. Empty means the backend's own rule decides.
	DLL string

	// Variadic marks a trailing `...` in the parameter list, which is
	// only meaningful on an extern declaration: it says calls may pass
	// more arguments than there are named parameters, and tells the
	// emitter to set AL per the Windows x64 variadic rule.
	Variadic bool

	// Export marks `export fn`: in a DLL build the function is in the
	// export table under its own name, callable from native code.
	Export bool

	// TypeParams are the names in `fn max<T>(...)`. A function with any
	// is a template: it is never checked or compiled as written, only
	// copied out once per set of types it is called with. See
	// generics.go. RecvParams are the same for a method in `impl Box<T>`,
	// whose type parameters belong to the struct.
	TypeParams []string
	RecvParams []string

	// Toks is a template's source, from `fn` to the end of the body.
	Toks []Token

	// Instance marks a copy made from a template.
	Instance bool
}

// ---- statements ----

type Block struct {
	Span
	Stmts []Stmt
}

type LetStmt struct {
	Span
	Name  string
	Const bool
	Type  string // as written; "" when inferred
	T     *Type  // resolved by the checker, whether written or inferred
	Value Expr

	// Used is filled in by the resolver. Go rejects unused locals, so
	// codegen emits a `_ = x` discard only when this is false.
	Used bool

	// Addressed is set by the checker when &Name is taken, so a backend
	// keeps the variable in memory at its C width. See addr.go.
	Addressed bool

	// Set on a top-level const, which becomes a real global rather than
	// a local of the implicit main.
	Global bool
	Pub    bool
	File   string
	Pkg    string // see FnDecl.Pkg; only meaningful on a global
}

// AssignStmt covers `x = v`, `x += v`, and `xs[i] = v`. Target is an
// *Ident or an *Index; nothing else is assignable.
type AssignStmt struct {
	Span
	Target Expr
	Op     Kind // ASSIGN, PLUSEQ, MINUSEQ, STAREQ, SLASHEQ
	Value  Expr
}

// TargetName returns the variable at the root of an assignment target,
// which is what the resolver needs for its const and scope checks.
// `xs[0][1] = v` roots at "xs".
func (a *AssignStmt) TargetName() string {
	e := a.Target
	for {
		switch t := e.(type) {
		case *Ident:
			return t.Name
		case *Index:
			e = t.X
		case *Field:
			e = t.X
		default:
			return ""
		}
	}
}

type ExprStmt struct {
	Span
	X Expr
}

type IfStmt struct {
	Span
	Cond Expr
	Then *Block
	Else Stmt // *Block, *IfStmt, or nil
}

type WhileStmt struct {
	Span
	Cond Expr
	Body *Block
}

type ReturnStmt struct {
	Span
	Value Expr // nil for a bare `return`
}

// ForStmt is either a counted loop over a range -
//
//	for i in start..end step n
//
// - or a loop over a collection:
//
//	for x in list
//	for k, v in map
//
// Coll distinguishes them: nil means the range form.
type ForStmt struct {
	Span
	Var  string
	Var2 string // the value variable in `for k, v in map`; "" otherwise
	Body *Block

	// range form
	Start     Expr
	End       Expr
	Step      Expr // nil means 1
	Inclusive bool

	// collection form
	Coll  Expr
	CollT *Type // the collection's type, filled in by the checker

	// Lowered is what the checker made of a loop over a channel: a
	// while that receives until the channel is closed and drained. A
	// backend runs this instead when it is set.
	Lowered Stmt
}

// MatchCase is one arm: `1, 2 => body`.
type MatchCase struct {
	Span
	Values []Expr
	Body   Stmt
}

// MatchStmt is a multi-way branch on one value.
//
//	match code {
//	    200      => print("ok")
//	    404, 410 => print("gone")
//	    else     => print("something else")
//	}
type MatchStmt struct {
	Span
	Subject Expr
	Cases   []MatchCase
	Else    Stmt // nil when there is no else arm

	// Lowered is what the checker made of a match on a data enum: the
	// same thing as a chain of ifs on the variant, with each arm's names
	// bound to what it holds. A backend runs this instead when it is set,
	// having first put the subject in a local named Temp.
	Lowered Stmt
	Temp    string
}

// DeferStmt is `defer stmt`: stmt runs when the block the defer is in
// is left, however it is left - its end, a return, a break or a
// continue - after anything deferred later in the same block.
type DeferStmt struct {
	Span
	Body Stmt
}

func (*DeferStmt) stmtNode() {}

type BreakStmt struct{ Span }

type ContinueStmt struct{ Span }

func (*Block) stmtNode()        {}
func (*LetStmt) stmtNode()      {}
func (*AssignStmt) stmtNode()   {}
func (*ExprStmt) stmtNode()     {}
func (*IfStmt) stmtNode()       {}
func (*WhileStmt) stmtNode()    {}
func (*ReturnStmt) stmtNode()   {}
func (*ForStmt) stmtNode()      {}
func (*MatchStmt) stmtNode()    {}
func (*BreakStmt) stmtNode()    {}
func (*ContinueStmt) stmtNode() {}
