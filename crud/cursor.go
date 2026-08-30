package crud

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/frostgrove/vv/utils"
)

// A cursor is the position of one row in a sorted result: the values of the
// columns the query sorts by, for the row at the edge of a page.
//
// Offset paging asks the database to count rows it will then throw away, and it
// asks the wrong question besides: "skip 10 000" means something different after
// somebody inserts a row above them, so a client walking a list sees one row
// twice and never sees another. A cursor asks "the rows after *this one*", which
// no concurrent write can change the meaning of.
//
// The precondition is that the sort is unique, or "after this one" is ambiguous.
// It is: a paged read appends the primary key to the sort unless the caller has
// already sorted by it (see sqlrepo.UnstablePagination for the opt-out, which also
// opts out of cursors).

// cursorPayload is what the opaque string carries. The field names travel with
// the values so a cursor cannot be replayed against a different sort, which
// would silently compare the wrong columns.
type cursorPayload struct {
	F []string          `json:"f"`
	V []json.RawMessage `json:"v"`
}

// EncodeCursor builds the opaque string for a row's sort values. Callers do not
// normally reach for this; a paged read returns one.
func EncodeCursor(fields []string, values []any) (string, error) {
	if len(fields) != len(values) {
		return "", fmt.Errorf("crud: cursor has %d fields and %d values", len(fields), len(values))
	}
	p := cursorPayload{F: fields, V: make([]json.RawMessage, len(values))}
	for i, v := range values {
		raw, err := json.Marshal(ElemValue(v))
		if err != nil {
			return "", fmt.Errorf("crud: cursor value for %s: %w", fields[i], err)
		}
		p.V[i] = raw
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeCursor parses the string and checks it against the sort it is about to
// be used with. A mismatch is refused rather than reinterpreted: the values are
// positional, so replaying a cursor under a different sort compares whatever
// happens to line up.
func decodeCursor(s string, sort []Order) (*cursorPayload, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, &SchemaError{Field: "cursor", Reason: "not a valid cursor"}
	}
	var p cursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, &SchemaError{Field: "cursor", Reason: "not a valid cursor"}
	}
	if len(p.F) != len(p.V) || len(p.F) == 0 {
		return nil, &SchemaError{Field: "cursor", Reason: "not a valid cursor"}
	}
	if len(p.F) != len(sort) {
		return nil, &SchemaError{Field: "cursor",
			Reason: "this cursor was made for a different sort order"}
	}
	for i, o := range sort {
		if p.F[i] != o.Field {
			return nil, &SchemaError{Field: "cursor",
				Reason: "this cursor was made for a different sort order"}
		}
	}
	return &p, nil
}

// value decodes the i-th cursor value against the Go type of the column it will
// be compared with, so a JSON number reaches the driver as the column's own
// integer type and a quoted timestamp as a time.Time.
func (this *cursorPayload) value(i int, t reflect.Type) (any, error) {
	destination := reflect.New(t)
	if err := json.Unmarshal(this.V[i], destination.Interface()); err != nil {
		return nil, &SchemaError{Field: "cursor",
			Reason: fmt.Sprintf("value for %s does not fit %s", this.F[i], t)}
	}
	return destination.Elem().Interface(), nil
}

// CursorFieldSupported reports whether the field's Go shape can represent SQL
// NULL. Cursor comparisons need a total order, while portable SQL comparisons
// with NULL are three-valued. The repository uses the same check before it
// advertises a cursor, so it never emits a token its next request must refuse.
func CursorFieldSupported(f *Field) bool {
	return f != nil && !cursorNullableType(f.Type)
}

func cursorNullableType(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer || utils.IsOptType(t) {
		return true
	}
	// The standard library's generic and legacy nullable values all use this
	// package/name shape. They implement Scanner/Valuer, but those interfaces
	// alone are not enough: non-nullable decimals and UUIDs commonly implement
	// the same pair.
	if t.PkgPath() == reflect.TypeFor[sql.NullString]().PkgPath() && strings.HasPrefix(t.Name(), "Null") {
		return true
	}
	// Wrappers such as gorm.DeletedAt embed a standard nullable value.
	if t.Kind() == reflect.Struct {
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.Anonymous && cursorNullableType(field.Type) {
				return true
			}
		}
	}
	return false
}

// CursorPredicate turns a cursor into the row-comparison that selects what comes
// after it — or before it, when back is set.
//
// The shape is the lexicographic expansion rather than SQL's row-value syntax:
//
//	(a > va) OR (a = va AND b < vb) OR (a = va AND b = vb AND id > vid)
//
// Row values would say the same thing in one line, but only when every column
// sorts the same direction, and MySQL will not use an index for the mixed case
// anyway. The expansion is portable and every engine plans it.
//
// It is exported because a caller assembling options by hand may want it; the
// repository builds it from the resolved sort, which is the only place the real
// sort is known.
func CursorPredicate(m *Meta, sort []Order, cursor string, back bool) (Predicate, error) {
	p, err := decodeCursor(cursor, sort)
	if err != nil {
		return nil, err
	}

	fields := make([]*Field, len(sort))
	values := make([]any, len(sort))
	for i, o := range sort {
		f := m.Field(o.Field)
		if f == nil {
			return nil, &UnknownFieldError{Model: m.Name, Field: o.Field}
		}
		// A NULL never compares equal or greater, so a page boundary on a
		// nullable column silently drops every row that has one. Refusing is the
		// honest answer; sort by something total, or add the key first.
		if !CursorFieldSupported(f) {
			return nil, &SchemaError{Model: m.Name, Field: o.Field,
				Reason: "a cursor cannot page by a nullable column"}
		}
		if v, err := p.value(i, f.Type); err != nil {
			return nil, err
		} else {
			values[i] = v
		}
		fields[i] = f
	}

	branches := make([]Predicate, 0, len(sort))
	for i := range sort {
		terms := make([]Predicate, 0, i+1)
		for j := 0; j < i; j++ {
			terms = append(terms, Eq(sort[j].Field, values[j]))
		}
		terms = append(terms, cursorStep(sort[i], values[i], back))
		if len(terms) == 1 {
			branches = append(branches, terms[0])
		} else {
			branches = append(branches, And(terms...))
		}
	}
	if len(branches) == 1 {
		return branches[0], nil
	}
	return Or(branches...), nil
}

// cursorStep is the strict comparison for one sort column: forward means "past
// it in the sort's own direction", and paging back inverts every column at once.
func cursorStep(o Order, v any, back bool) Predicate {
	if o.Desc != back {
		return Lt(o.Field, v)
	}
	return Gt(o.Field, v)
}
