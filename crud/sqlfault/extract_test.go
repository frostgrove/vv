package sqlfault

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/errs/sqlerr"
)

// dispatchKey is the three things sqlerr.Classify reads, and the three things a
// wrapping must not be able to change.
//
// sqlerr.Err.Key would also compare the driver type, and that one is not what a
// classifier dispatches on: it is the capture's evidence marker, and Err.SameKey
// compares it because the corpus records unwrapped errors. An error that says
// what happened in neither a state nor a number — pgconn's ConnectError, a bare
// net.OpError — gives driverType no mark to find, so its type is the outermost
// error's and does move under wrapping.
func dispatchKey(e *sqlerr.Err) string {
	if e == nil {
		return "<nothing extracted>"
	}
	names := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		names = append(names, k)
	}
	slices.Sort(names)
	return fmt.Sprintf("sqlstate=%q native=%d fields=[%s]", e.SQLState, e.Native, strings.Join(names, " "))
}

// A driver error has to be found however the layers above wrapped it, and the
// shapes that matter are the ones errors.Unwrap cannot walk.
//
// This is [[D-038]]'s owed regression, and it covers all three readers rather
// than only the SQLSTATE: the forbid is general, and the MySQL number and the
// SQLite result code were separate walks with the same blindness. Leaving those
// two behind would re-open the HY000 arm and the whole SQLite arm that phase 0
// fixed.
func TestADriverErrorIsFoundThroughEveryWrappingShape(t *testing.T) {
	for _, fx := range []struct {
		name string
		err  error
	}{
		{"a SQLSTATE from a method", pgish("23505")},
		{"a MySQL number under HY000", myish(3819, "HY000", "Check constraint 'ck' is violated.")},
		{"a SQLite result code from a method", &sqliteish{code: 2067}},
	} {
		t.Run(fx.name, func(t *testing.T) {
			want := dispatchKey(Extract(fx.err))
			// The blank to guard against is an Err that read nothing, not a nil
			// one: Extract answers nil only for a nil error and says so. Compared
			// against nil this guard cannot fire, and the table then goes green
			// with extraction broken because every leg compares two blanks.
			if want == dispatchKey(&sqlerr.Err{}) {
				t.Fatalf("the bare error extracted nothing (%s), so every leg below compares two blanks", want)
			}

			for _, leg := range []struct {
				name string
				err  error
			}{
				{"bare", fx.err},
				{"wrapped once", fmt.Errorf("exec: %w", fx.err)},
				// The sentinel goes first on purpose. A walk that returns
				// whatever the first branch produced finds crud.ErrConflict,
				// which carries no state and no number, and stops.
				{"a multi-error with the sentinel first", fmt.Errorf("%w: %w", crud.ErrConflict, fx.err)},
				{"errors.Join", errors.Join(errors.New("saving user"), fx.err)},
				{"inside a fault", errs.Conflict().Wrapping(crud.ErrConflict, fx.err).Fault()},
			} {
				for _, outer := range []struct {
					name string
					err  error
				}{
					{leg.name, leg.err},
					{leg.name + ", wrapped again", fmt.Errorf("saving user: %w", leg.err)},
				} {
					t.Run(outer.name, func(t *testing.T) {
						if got := dispatchKey(Extract(outer.err)); got != want {
							t.Fatalf("the driver error was not found through this wrapping:\ngot  %s\nwant %s", got, want)
						}
					})
				}
			}
		})
	}
}

// Why a plain errors.Unwrap loop goes blind, stated rather than implied.
func TestTheWrappingsThatDefeatAPlainUnwrapLoop(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"fmt.Errorf with two %w", fmt.Errorf("%w: %w", crud.ErrConflict, pgish("23505"))},
		{"errors.Join", errors.Join(errors.New("a"), pgish("23505"))},
		{"a fault", errs.Conflict().Wrapping(crud.ErrConflict, pgish("23505")).Fault()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errors.Unwrap(tc.err) != nil {
				t.Fatal("errors.Unwrap sees through this one, so it is not the shape the tree walk exists for")
			}
			if Extract(tc.err).SQLState != "23505" {
				t.Fatal("the tree walk did not see through it either")
			}
		})
	}
}

// The control. Without it the test above passes for an extractor that answers
// 23505 for anything, and the nil half is what keeps "the driver populated no
// fields" from reading as "there were none".
func TestAnErrorWithNothingInItExtractsToNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"a multi-error of two plain errors", fmt.Errorf("%w: %w", errors.New("a"), errors.New("b"))},
		{"a fault wrapping only the sentinel", errs.Conflict().Wrapping(crud.ErrConflict).Fault()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Extract(tc.err)
			if e == nil {
				t.Fatal("a non-nil error extracted to nil; only a nil error may do that")
			}
			if e.SQLState != "" || e.Native != 0 {
				t.Fatalf("something was read out of an error that carries nothing: %s", e.Key())
			}
			if e.Fields != nil {
				t.Fatalf("Fields is %#v, want nil — an empty map reads as \"the driver named none\"", e.Fields)
			}
		})
	}
	if Extract(nil) != nil {
		t.Fatal("a nil error extracted to something")
	}
}

// A driver error need not be a struct. modernc.org/sqlite's is, but a defined
// integer or string type with one method is a shape Go drivers do use, and a
// walk that hands its callback only structs drops both method paths silently —
// every fake in crud/adapter/crudsql/conflict_test.go is a struct, so nothing there
// would go red.
type sqliteCode int

func (this sqliteCode) Error() string { return "constraint failed" }
func (this sqliteCode) Code() int     { return int(this) }

type pqState string

func (this pqState) Error() string    { return "pq: " + string(this) }
func (this pqState) SQLState() string { return string(this) }

// The controls: the same underlying types with no method at all.
type bareCode int

func (this bareCode) Error() string { return "constraint failed" }

type bareState string

func (this bareState) Error() string { return string(this) }

func TestTheMethodPathIsReachedOnAnErrorThatIsNotAStruct(t *testing.T) {
	if got := Extract(sqliteCode(2067)).Native; got != 2067 {
		t.Fatalf("Native = %d, want 2067 — the Code method on a non-struct error was not reached", got)
	}
	if got := Extract(pqState("23505")).SQLState; got != "23505" {
		t.Fatalf("SQLState = %q, want 23505 — the SQLState method on a non-struct error was not reached", got)
	}

	// The control. Without it both assertions above pass for an extractor that
	// read the underlying integer or string directly, which would classify
	// every defined-type error by coincidence.
	if got := Extract(bareCode(2067)).Native; got != 0 {
		t.Fatalf("Native = %d for an error with no Code method: the underlying integer was read", got)
	}
	if got := Extract(bareState("23505")).SQLState; got != "" {
		t.Fatalf("SQLState = %q for an error with no SQLState method: the underlying string was read", got)
	}
}

func TestExtractionCarriesOnlyTheWhitelistedFields(t *testing.T) {
	full := &pgconnish{
		Code:           "23505",
		Message:        "duplicate key value violates unique constraint",
		ConstraintName: "users_email_key",
		TableName:      "users",
		SchemaName:     "public",
		ColumnName:     "email",
		DataTypeName:   "text",
		Detail:         "Key (email)=(a@b.c) already exists.",
		Hint:           "try another one",
		File:           "nbtinsert.c",
		Line:           673,
		Routine:        "_bt_check_unique",
		Position:       17,
	}
	// The excluded ones have to be populated, or "they are not carried" is a
	// statement about a fixture that had nothing to carry.
	for name, v := range map[string]string{"File": full.File, "Routine": full.Routine} {
		if v == "" {
			t.Fatalf("the fixture's %s is empty, so excluding it proves nothing", name)
		}
	}
	if full.Line == 0 || full.Position == 0 {
		t.Fatal("the fixture's Line and Position are zero, so excluding them proves nothing")
	}

	e := Extract(full)
	want := map[string]string{
		"ConstraintName": "users_email_key",
		"TableName":      "users",
		"SchemaName":     "public",
		"ColumnName":     "email",
		"DataTypeName":   "text",
		"Detail":         "Key (email)=(a@b.c) already exists.",
		"Hint":           "try another one",
	}
	if !reflect.DeepEqual(e.Fields, want) {
		t.Fatalf("Fields = %#v\nwant %#v", e.Fields, want)
	}
	if got, want := e.Key(), `*sqlfault.pgconnish sqlstate="23505" native=0 fields=[ColumnName ConstraintName DataTypeName Detail Hint SchemaName TableName]`; got != want {
		t.Fatalf("Key() = %s\nwant  %s", got, want)
	}

	// The control: the same shape with every whitelisted field blank still reads
	// the SQLSTATE, and carries no map at all. Without it the test above passes
	// for an extractor that copies whatever it finds rather than filtering.
	e = Extract(&pgconnish{Code: "23505", Message: full.Message, File: full.File, Line: full.Line, Routine: full.Routine, Position: full.Position})
	if e.SQLState != "23505" {
		t.Fatalf("SQLState = %q: the state stopped being read once the fields were empty", e.SQLState)
	}
	if e.Fields != nil {
		t.Fatalf("Fields = %#v, want nil: File, Line, Routine and Position reached the map", e.Fields)
	}
}

// The number is one field, and which of a driver's several integers fills it is
// not free. mattn/go-sqlite3 has Code and ExtendedCode and only the extended one
// names the constraint; pgconn has a Code that is a string.
func TestTheNumberComesFromTheFieldThatMeansIt(t *testing.T) {
	if got := Extract(&mattnish{Code: 19, ExtendedCode: 2067}).Native; got != 2067 {
		t.Fatalf("Native = %d, want the extended code 2067 — the primary code says only that it was a constraint", got)
	}
	if got := Extract(&mattnish{Code: 19}).Native; got != 19 {
		t.Fatalf("Native = %d, want 19 — with no extended code the primary one is all there is", got)
	}
	if got := Extract(pgish("23505")).Native; got != 0 {
		t.Fatalf("Native = %d: pgconn's Code holds the SQLSTATE as a string and was read as a number", got)
	}
	if got := Extract(myish(1062, "23000", "Duplicate entry")).Native; got != 1062 {
		t.Fatalf("Native = %d, want MySQL's 1062", got)
	}
}

// A wrapper of the caller's own is not the driver. The names in the whitelist are
// ordinary ones — Detail and Hint most of all — and the walk meets the wrapper
// first, so a reader that took whatever contributed first would hand the
// classifier the wrapper's sentence in place of pgconn's constraint and table,
// and Classifier.fill would then have no key to look the columns up by.
type noisy struct {
	Detail string
	err    error
}

func (this *noisy) Error() string { return this.Detail + ": " + this.err.Error() }
func (this *noisy) Unwrap() error { return this.err }

func TestAWrappersOwnFieldsAreNotTheDriversFields(t *testing.T) {
	pg := duplicateKey()

	// The control: what the bare driver error yields. Without it the assertions
	// below pass for a reader that answers the same thing for everything.
	bare := Extract(pg)
	if bare.Fields["ConstraintName"] != "users_email_key" || bare.Fields["TableName"] != "users" {
		t.Fatalf("the bare fixture already carries %#v, so wrapping it proves nothing", bare.Fields)
	}
	if bare.Fields["Detail"] == "" {
		t.Fatal("the fixture's Detail is empty, so the wrapper's own Detail cannot be told from it")
	}

	wrapped := Extract(&noisy{Detail: "saving user", err: pg})
	if !reflect.DeepEqual(wrapped.Fields, bare.Fields) {
		t.Fatalf("the wrapper's fields displaced the driver's:\ngot  %#v\nwant %#v", wrapped.Fields, bare.Fields)
	}
	if wrapped.Type != bare.Type {
		t.Fatalf("Type = %q, want the driver's %q — a Detail field is not the mark of an engine's error", wrapped.Type, bare.Type)
	}

	// And the consequence, which is what makes the mechanism worth pinning: the
	// constraint and the table are the key the catalog fill is asked with.
	cat := &fakeColumns{cols: []string{"email"}}
	f, ok := New("postgres", WithColumns(cat)).Classify(&noisy{Detail: "saving user", err: pg})
	if !ok {
		t.Fatal("no fault")
	}
	if f.Detail.Constraint != "users_email_key" || f.Detail.Table != "users" {
		t.Fatalf("Detail = %+v: a wrapped driver error lost what it named", f.Detail)
	}
	if !slices.Equal(f.Detail.Columns, []string{"email"}) {
		t.Fatalf("Detail.Columns = %v: the lookup was never keyed on anything", f.Detail.Columns)
	}
}
