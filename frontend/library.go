package frontend

import "fmt"

// The seam between the type checker and a backend's set of builtins.
//
// The checker is shared; the builtins are not. The Go backend offers 302
// of them, backed by Go's standard library. The assembly backend offers
// far fewer, because everything it has it had to write. Neither list
// belongs in the checker, and the checker should not have to care which
// one it is looking at.
//
// So a backend hands the checker a Library, and the checker asks it
// questions. That is the whole interface. What it buys, beyond letting
// check.go be shared at all:
//
//   - A builtin a backend does not have becomes an ordinary compile
//     error with a position and a suggestion, rather than something
//     discovered further down the pipeline in a different vocabulary.
//   - The two backends cannot drift on what a builtin's signature is,
//     because the signature is declared in one shape.

// A Signature is everything the checker needs to know about a builtin.
// Deliberately not the whole builtin: how one is emitted, which imports
// it drags in and which runtime helpers it needs are the backend's
// business and never the checker's.
type Signature struct {
	// Params are the positional parameter types. Numeric accepts int or
	// float; Any accepts anything.
	Params []*Type
	// Rest types the variadic tail.
	Rest *Type
	// Ret is the return type. Void means the builtin produces no value.
	Ret *Type
	// RetOf overrides Ret for polymorphic builtins - min and max return
	// whatever type they were given.
	RetOf func(args []*Type) *Type

	// Check replaces the Params/Rest/Ret machinery for builtins whose
	// rules cannot be written as a fixed signature. It reports its own
	// errors and returns the result type.
	Check func(c *Checker, x *Call, args []*Type) *Type

	// HintFor supplies the expected type of argument i given the ones
	// before it, so an empty literal passed to a polymorphic builtin has
	// something to infer from. Return nil to leave an argument unhinted.
	HintFor func(x *Call, known []*Type, i int) *Type

	// WantsTarget asks the checker to record the expected result type on
	// the call, for builtins whose return type comes from the context
	// rather than their arguments - a decoder, essentially.
	WantsTarget bool
}

// A Library is one backend's set of builtins.
type Library interface {
	// Signature reports what a builtin looks like, or false if this
	// backend has no builtin by that name.
	Signature(name string) (Signature, bool)

	// ConstType reports the type of a builtin constant such as PI.
	ConstType(name string) (*Type, bool)
}

// EmptyLibrary is a Library with nothing in it, for a caller that wants
// to check a program using no builtins at all. Mostly useful in tests.
type EmptyLibrary struct{}

func (EmptyLibrary) Signature(string) (Signature, bool) { return Signature{}, false }
func (EmptyLibrary) ConstType(string) (*Type, bool)     { return nil, false }

// CompoundOp maps `x op= y` to the binary operator it stands for.
var CompoundOp = map[Kind]Kind{
	PLUSEQ:    PLUS,
	MINUSEQ:   MINUS,
	STAREQ:    STAR,
	SLASHEQ:   SLASH,
	PERCENTEQ: PERCENT,
	AMPEQ:     AMP,
	PIPEEQ:    PIPE,
	CARETEQ:   CARET,
	SHLEQ:     SHL,
	SHREQ:     SHR,
}

// AssignOpText spells a compound assignment operator, for error
// messages. Scandiacus and Go happen to agree on all of these, which is why
// this used to live in the Go backend under a name that implied it was
// about Go. It is not: it is how the operator is written in Scandiacus.
func AssignOpText(k Kind) string {
	switch k {
	case PLUSEQ:
		return "+="
	case MINUSEQ:
		return "-="
	case STAREQ:
		return "*="
	case SLASHEQ:
		return "/="
	case PERCENTEQ:
		return "%="
	case AMPEQ:
		return "&="
	case PIPEEQ:
		return "|="
	case CARETEQ:
		return "^="
	case SHLEQ:
		return "<<="
	case SHREQ:
		return ">>="
	}
	return "="
}

// OpText spells a binary operator, for error messages. Scandiacus and Go
// agree on all of them, which is why this used to live in the Go
// backend under a name implying it was about Go. It is not.
func OpText(k Kind) string {
	switch k {
	case PLUS:
		return "+"
	case MINUS:
		return "-"
	case STAR:
		return "*"
	case SLASH:
		return "/"
	case PERCENT:
		return "%"
	case EQ:
		return "=="
	case NEQ:
		return "!="
	case LT:
		return "<"
	case LTE:
		return "<="
	case GT:
		return ">"
	case GTE:
		return ">="
	case AND:
		return "&&"
	case OR:
		return "||"
	case AMP:
		return "&"
	case PIPE:
		return "|"
	case CARET:
		return "^"
	case SHL:
		return "<<"
	case SHR:
		return ">>"
	}
	return "?"
}

// ArityText describes how many arguments something takes.
func ArityText(min, max int) string {
	switch {
	case max < 0 && min == 0:
		return "any number of arguments"
	case max < 0:
		return fmt.Sprintf("at least %d argument(s)", min)
	case min == max && min == 1:
		return "1 argument"
	case min == max:
		return fmt.Sprintf("%d arguments", min)
	default:
		return fmt.Sprintf("%d to %d arguments", min, max)
	}
}
