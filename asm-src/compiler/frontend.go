package main

// Aliases for the shared front end.
//
// The lexer, parser, AST and type representation live in ../../frontend
// so that both backends compile the same language from one definition
// rather than from two copies that drift.
//
// These are Go type aliases, not new types, which is what lets every
// other file in this package go on writing Token, Expr and PLUS
// unqualified. Adding a name to the front end means adding one line
// here, and nothing else changes.
//
// Generated once by hand; keep it sorted.

import front "veylfront"

type (
	AssignStmt   = front.AssignStmt
	Binary       = front.Binary
	Block        = front.Block
	BoolLit      = front.BoolLit
	BreakStmt    = front.BreakStmt
	Call         = front.Call
	Checker      = front.Checker
	ContinueStmt = front.ContinueStmt
	DeferStmt    = front.DeferStmt
	EmptyLibrary = front.EmptyLibrary
	Expr         = front.Expr
	ExprStmt     = front.ExprStmt
	Field        = front.Field
	FloatLit     = front.FloatLit
	FnDecl       = front.FnDecl
	ForStmt      = front.ForStmt
	FuncLit      = front.FuncLit
	Ident        = front.Ident
	IfStmt       = front.IfStmt
	ImplBlock    = front.ImplBlock
	ImportDecl   = front.ImportDecl
	Index        = front.Index
	IntLit       = front.IntLit
	Interp       = front.Interp
	InterpPart   = front.InterpPart
	Kind         = front.Kind
	LetStmt      = front.LetStmt
	Lexer        = front.Lexer
	Library      = front.Library
	ListLit      = front.ListLit
	MapLit       = front.MapLit
	MatchCase    = front.MatchCase
	MatchStmt    = front.MatchStmt
	NilLit       = front.NilLit
	Node         = front.Node
	Param        = front.Param
	Parser       = front.Parser
	Program      = front.Program
	ReturnStmt   = front.ReturnStmt
	Signature    = front.Signature
	Span         = front.Span
	Stmt         = front.Stmt
	StrLit       = front.StrLit
	StructDecl   = front.StructDecl
	EnumDecl     = front.EnumDecl
	StructField  = front.StructField
	StructLit    = front.StructLit
	Token        = front.Token
	Try          = front.Try
	Type         = front.Type
	TypeKind     = front.TypeKind
	Unary        = front.Unary
	Cast         = front.Cast
	WhileStmt    = front.WhileStmt
	Widen        = front.Widen
	Convert      = front.Convert
)

const TagField = front.TagField

var (
	PayloadField = front.PayloadField
	ArityText    = front.ArityText
	AssignOpText = front.AssignOpText
	DottedName   = front.DottedName
	FuncOf       = front.FuncOf
	IsAlpha      = front.IsAlpha
	IsDigit      = front.IsDigit
	IsHexDigit   = front.IsHexDigit
	IsUntypedInt = front.IsUntypedInt
	ListOf       = front.ListOf
	MapOf        = front.MapOf
	NewChecker   = front.NewChecker
	NewLexer     = front.NewLexer
	NewParser    = front.NewParser
	NullableOf   = front.NullableOf
	OpText       = front.OpText
	ParseType    = front.ParseType
	CTypeSize    = front.CTypeSize
	PtrToC       = front.PtrToC
	Qual         = front.Qual
	ResultOf     = front.ResultOf
	StructOf     = front.StructOf
	EnumOf       = front.EnumOf
	Any          = front.Any
	Bool         = front.Bool
	Bytes        = front.Bytes
	CompoundOp   = front.CompoundOp
	ErrLitT      = front.ErrLitT
	Float        = front.Float
	Int          = front.Int
	Keywords     = front.Keywords
	NilLitT      = front.NilLitT
	Numeric      = front.Numeric
	Str          = front.Str
	Unknown      = front.Unknown
	Void         = front.Void
)

const (
	AMP       = front.AMP
	AMPEQ     = front.AMPEQ
	AND       = front.AND
	ARROW     = front.ARROW
	ASSIGN    = front.ASSIGN
	BANG      = front.BANG
	BREAK     = front.BREAK
	CARET     = front.CARET
	CARETEQ   = front.CARETEQ
	COLON     = front.COLON
	COMMA     = front.COMMA
	COMMENT   = front.COMMENT
	CONST     = front.CONST
	CONTINUE  = front.CONTINUE
	DEFER     = front.DEFER
	DOT       = front.DOT
	DOTDOT    = front.DOTDOT
	DOTDOTEQ  = front.DOTDOTEQ
	ELSE      = front.ELSE
	ELLIPSIS  = front.ELLIPSIS
	EOF       = front.EOF
	EQ        = front.EQ
	EXTERN    = front.EXTERN
	FALSE     = front.FALSE
	FATARROW  = front.FATARROW
	FN        = front.FN
	FOR       = front.FOR
	GT        = front.GT
	GTE       = front.GTE
	IDENT     = front.IDENT
	IF        = front.IF
	ILLEGAL   = front.ILLEGAL
	IMPL      = front.IMPL
	IMPORT    = front.IMPORT
	IN        = front.IN
	KAny      = front.KAny
	KBool     = front.KBool
	KBytes    = front.KBytes
	KErrLit   = front.KErrLit
	KFloat    = front.KFloat
	KFunc     = front.KFunc
	KInt      = front.KInt
	KList     = front.KList
	KMap      = front.KMap
	KNilLit   = front.KNilLit
	KNullable = front.KNullable
	KNumeric  = front.KNumeric
	KResult   = front.KResult
	KStr      = front.KStr
	KStruct   = front.KStruct
	KEnum     = front.KEnum
	KPtr      = front.KPtr
	KFixed    = front.KFixed
	KUnknown  = front.KUnknown
	KVoid     = front.KVoid
	LBRACE    = front.LBRACE
	LBRACKET  = front.LBRACKET
	LET       = front.LET
	LPAREN    = front.LPAREN
	LT        = front.LT
	LTE       = front.LTE
	MATCH     = front.MATCH
	MINUS     = front.MINUS
	MINUSEQ   = front.MINUSEQ
	NEQ       = front.NEQ
	NEWLINE   = front.NEWLINE
	NIL       = front.NIL
	NUMBER    = front.NUMBER
	OR        = front.OR
	OWN       = front.OWN
	PERCENT   = front.PERCENT
	PERCENTEQ = front.PERCENTEQ
	PIPE      = front.PIPE
	PIPEEQ    = front.PIPEEQ
	PLUS      = front.PLUS
	PLUSEQ    = front.PLUSEQ
	PUB       = front.PUB
	QUESTION  = front.QUESTION
	RAWSTRING = front.RAWSTRING
	RBRACE    = front.RBRACE
	RBRACKET  = front.RBRACKET
	RETURN    = front.RETURN
	RPAREN    = front.RPAREN
	SELF      = front.SELF
	SHL       = front.SHL
	SHLEQ     = front.SHLEQ
	SHR       = front.SHR
	SHREQ     = front.SHREQ
	SLASH     = front.SLASH
	SLASHEQ   = front.SLASHEQ
	STAR      = front.STAR
	STAREQ    = front.STAREQ
	STEP      = front.STEP
	STRING    = front.STRING
	STRUCT    = front.STRUCT
	TILDE     = front.TILDE
	TRUE      = front.TRUE
	UNSAFE    = front.UNSAFE
	WHILE     = front.WHILE
)
