package errs

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// Detail is everything the classifier learned and nothing a client may see:
// the dialect, the SQLSTATE, the engine's own number, the constraint, the
// table, the columns, and the driver error itself.
//
// It is never rendered. [Fault.MarshalJSON] exists because the default marshal
// of a fault does not fail on the Driver field — it succeeds, and emits the
// constraint name, the table and every exported field the driver error has.
type Detail struct {
	Dialect    string // "postgres", "mysql", "mariadb", "sqlite"
	SQLState   string // "23505"
	Native     int    // 1062, 2067
	Constraint string
	Table      string
	Columns    []string
	Value      string // best-effort; a value parsed from a message is not an interface ([[D-039]])
	RefTable   string
	RefColumns []string
	Driver     error
}

// A Fault is a classified failure with every violation it found underneath it.
//
// It wraps the errors it describes and never replaces them. A caller who wrote
// errors.Is(err, crud.ErrConflict) before any of this existed keeps that
// branch, and a caller who wants the list reaches it with errors.As — both on
// the same value, through as many further wrappings as a service layer adds
// ([[D-038]]).
//
// There is no Retryable field. [KindRetryable] already says it, and a second
// spelling would make representable the one state [[D-040]] forbids: a conflict
// that claims to be retryable, with no rule saying which of the two a transport
// should believe.
type Fault struct {
	Kind       Kind
	Code       Code
	Message    string // developer-facing. never rendered, and never in Error().
	Violations []Violation
	Op         string // the repository verb: "Save", "Update"
	Entity     string
	Partial    bool // a cap was hit; the set is incomplete
	Detail     Detail

	// wrapped is unexported so a third-party Classifier cannot forge a sentinel
	// match. [Builder.Wrapping] is the only way anything gets in here.
	wrapped []error
}

// Error is classification only: the op, the entity, the kind, the code and how
// many violations there were. Not the developer [Fault.Message], not [Detail],
// not [Source], not [Violation.Params], and not one word from any wrapped
// error.
//
// That is against the usual Go instinct and it is deliberate ([[D-047]]).
// port/porthttp/render.go copies the outermost err.Error() into the body of
// every status below 500, and the adapters return a fault as that outermost
// error — so what this method prints is what a client reads on a classified 409
// today, phases before the rule that forbids naming anything internal comes into
// force. The exported fields and [Fault.MarshalJSON] are the debug channel.
func (f *Fault) Error() string {
	var b strings.Builder
	b.WriteString("errs: ")
	switch {
	case f.Op != "" && f.Entity != "":
		b.WriteString(f.Op)
		b.WriteByte(' ')
		b.WriteString(f.Entity)
		b.WriteString(": ")
	case f.Op != "":
		b.WriteString(f.Op)
		b.WriteString(": ")
	case f.Entity != "":
		b.WriteString(f.Entity)
		b.WriteString(": ")
	}
	b.WriteString(f.Kind.String())
	if f.Code != "" {
		b.WriteString(": ")
		b.WriteString(string(f.Code))
	}
	if n := len(f.Violations); n > 0 {
		b.WriteString(" (")
		b.WriteString(strconv.Itoa(n))
		b.WriteString(" violation")
		if n != 1 {
			b.WriteByte('s')
		}
		b.WriteByte(')')
	}
	return b.String()
}

// Unwrap returns every error this fault was built over, so errors.Is and
// errors.As walk the tree. A fault that wraps nothing returns nil and matches
// nothing — which is the half of [[D-038]] that actually pins it.
func (f *Fault) Unwrap() []error { return f.wrapped }

// MarshalJSON emits the kind, the code, the violations and the partial marker,
// and nothing else. [Detail], [Source], [Fault.Message] and the wrapped errors
// stay behind.
//
// The receiver is the value one for the same measured reason as
// [Violation.MarshalJSON]: a pointer receiver is bypassed for a value, a map
// entry and a struct field, and the default marshal of a Fault succeeds — it
// does not fail on the Driver error — so the leak would be silent.
func (f Fault) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteString(`{"kind":`)
	kind, err := f.Kind.MarshalJSON()
	if err != nil {
		return nil, err
	}
	b.Write(kind)
	if f.Code != "" {
		code, err := json.Marshal(string(f.Code))
		if err != nil {
			return nil, err
		}
		b.WriteString(`,"error_code":`)
		b.Write(code)
	}
	b.WriteString(`,"violations":`)
	if len(f.Violations) == 0 {
		// A 404 and a bare 500 carry no violations, and json.Marshal of a nil
		// slice is null. The envelope's field is an array a client iterates, so
		// emitting null there breaks every such client at the one status they
		// are least likely to have exercised.
		b.WriteString("[]")
	} else {
		vs, err := json.Marshal(f.Violations)
		if err != nil {
			return nil, err
		}
		b.Write(vs)
	}
	if f.Partial {
		b.WriteString(`,"partial":true`)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// String carries what [Fault.Error] carries, for the same reason. Without it,
// fmt.Sprintf("%+v", *f) on a fault value prints the whole struct — Detail,
// constraint name and driver error included — because a value does not satisfy
// error and fmt falls through to the struct printer.
func (f Fault) String() string { return f.Error() }

// AsFault finds the fault in an error chain.
func AsFault(err error) (*Fault, bool) {
	var f *Fault
	if errors.As(err, &f) {
		return f, true
	}
	return nil, false
}
