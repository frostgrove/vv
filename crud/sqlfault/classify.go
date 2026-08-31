package sqlfault

import (
	"errors"
	"fmt"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/errs/sqlerr"
)

type Classifier struct {
	engine string
	codes  *errs.Codes
	ex     Extractor
	cols   Columns
}

type Option func(*Classifier)

func WithCodes(c *errs.Codes) Option { return func(k *Classifier) { k.codes = c } }

func WithExtractor(x Extractor) Option { return func(k *Classifier) { k.ex = x } }

func WithColumns(c Columns) Option { return func(k *Classifier) { k.cols = c } }

func New(engine string, options ...Option) *Classifier {
	c := &Classifier{engine: engine, codes: errs.StandardCodes()}
	for _, o := range options {
		if o != nil {
			o(c)
		}
	}
	return c
}

func (this *Classifier) Engine() string {
	if this == nil {
		return ""
	}
	return this.engine
}

func (this *Classifier) Classify(err error) (*errs.Fault, bool) {
	if this == nil || err == nil {
		return nil, false
	}
	e := this.extract(err)
	if e == nil {
		return nil, false
	}
	code, source, ok := sqlerr.Classify(this.engine, e)
	if !ok {
		return nil, false
	}
	kind, ok := this.codes.KindOf(code)
	if !ok {
		return nil, false
	}
	source = this.fill(source)

	b := errs.New(kind).Code(code).
		General().Code(code).Origin(errs.OriginState).Source(source).
		Detail(errs.Detail{
			Dialect:    this.engine,
			SQLState:   e.SQLState,
			Native:     int(e.Native),
			Constraint: source.Constraint,
			Table:      source.Table,
			Columns:    source.Columns,
			Driver:     err,
		})

	if code == errs.CodeSchemaNotReady {
		b = b.Wrapping(crud.ErrSchemaNotReady)
	}

	if integrity(e, sqliteNative(err)) {
		b = b.Wrapping(crud.ErrConflict)
	}
	return b.Wrapping(err).Fault(), true
}

func (this *Classifier) extract(err error) *sqlerr.Err {
	if this.ex != nil {
		return this.ex.Extract(err)
	}
	return Extract(err)
}

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

var _ errs.Classifier = (*Classifier)(nil)
