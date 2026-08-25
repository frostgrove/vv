package sqlfault

import (
	"errors"
	"fmt"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/errs/sqlerr"
)

// A Classifier turns one engine's driver errors into faults. It is an
// errs.Classifier, and it is a value: nothing here is registered, and two
// wirings in one binary cannot decide each other's vocabulary ([[D-048]],
// errs/doc.go's fourth refusal).
type Classifier struct {
	engine string
	codes  *errs.Codes
	ex     Extractor
	cols   Columns
}

// An Option wires one part of a [Classifier].
type Option func(*Classifier)

// WithCodes replaces the vocabulary. The default is errs.StandardCodes, built
// per classifier rather than shared from a package-level variable, so an import
// cannot mutate another wiring's table.
func WithCodes(c *errs.Codes) Option { return func(k *Classifier) { k.codes = c } }

// WithExtractor replaces the by-shape extraction with a typed one. crudpgx
// passes its own, because it can name *pgconn.PgError.
func WithExtractor(x Extractor) Option { return func(k *Classifier) { k.ex = x } }

// WithColumns wires the schema lookup that fills in the columns a driver named
// no column for. Without it those stay nil.
func WithColumns(c Columns) Option { return func(k *Classifier) { k.cols = c } }

// New builds a classifier for one engine — "postgres", "mysql", "mariadb" or
// "sqlite", the vocabulary errs.Detail.Dialect documents.
//
// The engine is declared by the caller and derived from nothing. See the package
// doc for why a type switch over crud.Dialect is not a shortcut to it.
func New(engine string, opts ...Option) *Classifier {
	c := &Classifier{engine: engine, codes: errs.StandardCodes()}
	for _, o := range opts {
		if o != nil {
			o(c)
		}
	}
	return c
}

// Engine names the dialect this classifier was declared for.
func (c *Classifier) Engine() string {
	if c == nil {
		return ""
	}
	return c.engine
}

// Classify implements errs.Classifier.
//
// It refuses unless a code and its kind are both known. errs.KindInternal is the
// zero value, so a fault built from an unknown code would claim 500 for a
// duplicate key — the one lie the type makes representable.
//
// A refusal is not a failure. An engine this classifier was not declared for, an
// unlisted state, a code nobody wired: all three answer false, and [Wrap] then
// still attaches the sentinel where [Integrity] says one belongs.
func (c *Classifier) Classify(err error) (*errs.Fault, bool) {
	if c == nil || err == nil {
		return nil, false
	}
	e := c.extract(err)
	if e == nil {
		return nil, false
	}
	code, src, ok := sqlerr.Classify(c.engine, e)
	if !ok {
		return nil, false
	}
	kind, ok := c.codes.KindOf(code)
	if !ok {
		return nil, false
	}
	src = c.fill(src)

	b := errs.New(kind).Code(code).
		// Origin is written out because errs.OriginInput is the zero value: a
		// chain that never calls it marks every driver violation input-shaped,
		// which is what the never-echo default and the envelope's grouping both
		// key on.
		//
		// The path stays nil. This layer owns no hop of it — the column-to-field
		// translation belongs to the decorator that has crud.Meta ([[D-043]]) —
		// and a nil path is not an unresolved one, so nothing is marked
		// approximate either.
		General().Code(code).Origin(errs.OriginState).Source(src).
		Detail(errs.Detail{
			Dialect:    c.engine,
			SQLState:   e.SQLState,
			Native:     int(e.Native),
			Constraint: src.Constraint,
			Table:      src.Table,
			Columns:    src.Columns,
			Driver:     err,
		})

	// The sentinel goes inside the fault rather than around it, so the fault is
	// the outermost error and its Error() — classification only ([[D-047]]) — is
	// what http/crudhttp:Body copies into a 409 body today. Wrap re-checks and
	// attaches it for a classifier that did not.
	if integrity(e, sqliteNative(err)) {
		b = b.Wrapping(crud.ErrConflict)
	}
	return b.Wrapping(err).Fault(), true
}

func (c *Classifier) extract(err error) *sqlerr.Err {
	if c.ex != nil {
		return c.ex.Extract(err)
	}
	return Extract(err)
}

// Wrap is what an adapter calls. It is total: a nil classifier and a nil error
// both answer without panicking, so an adapter that was never given one degrades
// to the sentinel gate instead of taking the process down.
//
// The order matters. An error that already carries a fault is returned
// untouched: a crud.Source wrapping another adapter's executor would otherwise
// classify twice, and the second fault would shadow the first for errors.As.
// Then the classifier is asked. Then, whatever it answered, [Integrity] decides
// the sentinel — so a third-party errs.Classifier can neither forge a
// crud.ErrConflict nor drop one ([[D-038]]).
func Wrap(c errs.Classifier, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errs.AsFault(err); ok {
		return err
	}
	conflict := Integrity(err)
	if c != nil {
		if f, ok := c.Classify(err); ok && f != nil {
			if !conflict || errors.Is(f, crud.ErrConflict) {
				return f
			}
			return fmt.Errorf("%w: %w", crud.ErrConflict, f)
		}
	}
	if conflict {
		return fmt.Errorf("%w: %w", crud.ErrConflict, err)
	}
	return err
}

// The interface is what an adapter holds, so a consumer can hand one of its own.
var _ errs.Classifier = (*Classifier)(nil)
