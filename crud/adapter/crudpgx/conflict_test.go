package crudpgx

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/errs/sqlerr"
)

// pgx does not report a refused statement from Query. It hands back a Rows that
// says "no rows" and keeps the error for Err — and on PostgreSQL every insert
// and every update is an INSERT/UPDATE ... RETURNING, so Query is the path they
// all take. Classifying only Query's own return value therefore classified
// nothing at all: the integration suite passed for two engines because it never
// ran this adapter, and a pgx caller's duplicate key came back as a bare 500.

// deferred is pgx's failure shape: a live Rows whose Next is false and whose Err
// carries the PgError.
type deferred struct {
	pgx.Rows
	err error
}

func (d deferred) Next() bool      { return false }
func (d deferred) Err() error      { return d.err }
func (d deferred) Close()          {}
func (d deferred) Conn() *pgx.Conn { return nil }

// fake is a pgx handle that fails the way the server does.
type fake struct {
	err      error
	deferErr bool // report through Rows.Err rather than from Query
}

func (f fake) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, f.err
}

func (f fake) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	if f.deferErr {
		return deferred{err: f.err}, nil
	}
	return nil, f.err
}

func duplicateKey() error {
	return &pgconn.PgError{
		Code:           "23505",
		Message:        `duplicate key value violates unique constraint "users_email_key"`,
		ConstraintName: "users_email_key",
		TableName:      "users",
		SchemaName:     "public",
	}
}

func TestADuplicateKeyIsAConflictWhicheverWayPgxReportsIt(t *testing.T) {
	ctx := context.Background()

	t.Run("reported by Exec", func(t *testing.T) {
		_, err := From(fake{err: duplicateKey()}).Exec(ctx, "INSERT INTO users ...")
		wantConflict(t, err)
	})

	t.Run("reported by Query", func(t *testing.T) {
		_, err := From(fake{err: duplicateKey()}).Query(ctx, "INSERT INTO users ... RETURNING id")
		wantConflict(t, err)
	})

	t.Run("deferred to Rows.Err, which is what really happens", func(t *testing.T) {
		rows, err := From(fake{err: duplicateKey(), deferErr: true}).Query(ctx, "INSERT INTO users ... RETURNING id")
		if err != nil {
			t.Fatalf("pgx reports this one later, not here: %v", err)
		}
		defer rows.Close()
		if rows.Next() {
			t.Fatal("a refused statement returned a row")
		}
		wantConflict(t, rows.Err())
	})

	// A fault must not become a status by being a fault. 22001 is the caller's
	// input to fix and it is not a collision, so it classifies — too_long — and
	// wraps no sentinel at all ([[D-038]], [[D-046]]).
	t.Run("a value too long carries a code and no sentinel", func(t *testing.T) {
		_, err := From(fake{err: &pgconn.PgError{Code: "22001", Message: "value too long for type character varying(8)"}}).
			Exec(ctx, "INSERT INTO users ...")
		f, ok := errs.AsFault(err)
		if !ok {
			t.Fatalf("22001 produced no fault: %v", err)
		}
		if f.Code != errs.CodeTooLong || f.Kind != errs.KindValidation {
			t.Fatalf("the fault says %v/%v, want validation/too_long", f.Kind, f.Code)
		}
		if errors.Is(err, crud.ErrConflict) {
			t.Fatal("a value too long came back as a conflict, so it answers 409 rather than the 500 it is today")
		}
	})
}

// Everything else has to pass through untouched. A serialisation failure is
// retryable and a syntax error is the caller's bug; answering 409 to either
// would send the client back with the same request.
//
// The second half is the phase's own control case: an unclassifiable state stays
// a 500. A syntax error and a dead connection produce no fault at all, on all
// three paths — and the serialisation failure beside them does produce one. The
// pair is what gives either half teeth: without the 40001 row, "no fault" passes
// for a classifier that produces none for anything, which is what the tree did
// before phase 3; without the 42601 row, a classifier that classified everything
// would pass too.
func TestOnlyIntegrityErrorsBecomeConflicts(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		err  error
		code errs.Code // "" when nothing may classify it
	}{
		{"a serialisation failure", &pgconn.PgError{Code: "40001"}, errs.CodeSerializationFailure},
		{"a syntax error", &pgconn.PgError{Code: "42601"}, ""},
		{"a dead connection", errors.New("conn closed"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range paths(ctx, t, tc.err) {
				t.Run(path.name, func(t *testing.T) {
					got := path.err()
					if errors.Is(got, crud.ErrConflict) {
						t.Fatalf("%v was classified as a conflict", tc.err)
					}
					if !errors.Is(got, tc.err) {
						t.Fatalf("the driver error was lost: %v", got)
					}
					f, ok := errs.AsFault(got)
					if ok != (tc.code != "") {
						t.Fatalf("a fault was produced = %v, want %v: %v", ok, tc.code != "", got)
					}
					if ok && f.Code != tc.code {
						t.Fatalf("Code = %q, want %q", f.Code, tc.code)
					}
				})
			}
		})
	}
}

// paths is the three ways a pgx statement fails. Every one of them goes through
// Executor, so a classifier wired once covers all four call sites — but a test
// that only walked Exec would miss the one that actually fires on PostgreSQL,
// where every insert runs as INSERT ... RETURNING.
type failurePath struct {
	name string
	err  func() error
}

func paths(ctx context.Context, t *testing.T, driver error) []failurePath {
	t.Helper()
	return []failurePath{
		{"Exec", func() error {
			_, err := From(fake{err: driver}).Exec(ctx, "INSERT INTO users ...")
			return err
		}},
		{"Query", func() error {
			_, err := From(fake{err: driver}).Query(ctx, "INSERT INTO users ... RETURNING id")
			return err
		}},
		{"the deferred Rows.Err", func() error {
			rows, err := From(fake{err: driver, deferErr: true}).Query(ctx, "SELECT 1")
			if err != nil {
				t.Fatalf("pgx reports this one later, not here: %v", err)
			}
			rows.Next()
			return rows.Err()
		}},
	}
}

// The typed extractor reads the fields *pgconn.PgError actually has. There is no
// Column and no Table; the spellings are ColumnName and TableName, and getting
// one wrong produces a silently empty Source that no compiler catches — which is
// the whole reason this path is typed rather than by shape.
func TestTheTypedExtractorReadsThePgErrorFieldsThatExist(t *testing.T) {
	pg := &pgconn.PgError{
		Code:           "23505",
		Message:        `duplicate key value violates unique constraint "users_email_key"`,
		Detail:         "Key (email)=(a@b.c) already exists.",
		Hint:           "try another one",
		ConstraintName: "users_email_key",
		TableName:      "users",
		SchemaName:     "public",
		ColumnName:     "email",
		DataTypeName:   "text",
		File:           "nbtinsert.c",
		Line:           673,
		Routine:        "_bt_check_unique",
	}
	e := extract(pg)
	if e.Type != "*pgconn.PgError" || e.SQLState != "23505" {
		t.Fatalf("Type/SQLState = %q/%q", e.Type, e.SQLState)
	}
	// pgconn has no number. Reading its string Code as one would record zero for
	// every PostgreSQL error while looking like it had worked, and would send
	// every one of them through SQLite's byte arm.
	if e.Native != 0 {
		t.Fatalf("Native = %d, want 0: pgconn spells the SQLSTATE in a field called Code", e.Native)
	}
	want := map[string]string{
		"ConstraintName": "users_email_key",
		"TableName":      "users",
		"SchemaName":     "public",
		"ColumnName":     "email",
		"DataTypeName":   "text",
		"Detail":         "Key (email)=(a@b.c) already exists.",
		"Hint":           "try another one",
	}
	if !maps.Equal(e.Fields, want) {
		t.Fatalf("Fields = %#v, want %#v", e.Fields, want)
	}

	// The control on the map: a bare error carries none, and none must be nil
	// rather than empty. The Native half catches the string-Code misread even
	// where no other field would have shown it.
	bare := extract(&pgconn.PgError{Code: "23505"})
	if bare.Fields != nil {
		t.Fatalf("Fields = %#v, want nil for an error that named nothing", bare.Fields)
	}
	if bare.Native != 0 {
		t.Fatalf("Native = %d, want 0", bare.Native)
	}

	// And the value stays where it is. Detail holds it, Detail is localised, and
	// the fault's own Value field is left empty on purpose ([[D-039]]).
	if pg.Detail == "" {
		t.Fatal("the fixture's Detail is empty, so asserting the value was not read proves nothing")
	}
	f, ok := errs.AsFault(From(fake{}).conflict(pg))
	if !ok {
		t.Fatal("no fault")
	}
	if f.Detail.Value != "" {
		t.Fatalf("Detail.Value = %q: a value parsed out of a localised message is not an interface", f.Detail.Value)
	}

	// And the typed reader is what ran. Every other assertion in this file holds
	// for sqlfault.Extract too, so without this the whole typed path can be
	// deleted in one edit with the suite green and nothing here still naming
	// pgconn. Message is the lever because no classifier arm reads it ([[D-039]]):
	// asserting it couples this to extraction fidelity and not to behaviour. The
	// by-shape reader carries err.Error(), which is the wrapper's prefix plus
	// pgconn's own "(SQLSTATE …)" suffix rather than the server's own sentence.
	if got := extract(fmt.Errorf("exec: %w", pg)).Message; got != pg.Message {
		t.Fatalf("Message = %q, want the server's own text %q: extraction fell through to the by-shape reader", got, pg.Message)
	}
}

// The two extractors have to agree, because a PostgreSQL violation can arrive
// through either — database/sql over pgx/stdlib asks by shape, crudpgx asks by
// name — and one duplicate key answering two different things is the divergence
// this whole layer exists to prevent.
func TestBothExtractorsAgreeOnOnePgError(t *testing.T) {
	pg := &pgconn.PgError{
		Code:           "23505",
		Message:        "duplicate key",
		ConstraintName: "users_email_key",
		TableName:      "users",
		SchemaName:     "public",
	}
	typed, shaped := extract(pg), sqlfault.Extract(pg)
	if !typed.SameKey(shaped) {
		t.Fatalf("the two extractors disagree:\ntyped  %s\nshaped %s", typed.Key(), shaped.Key())
	}
	if !maps.Equal(typed.Fields, shaped.Fields) {
		t.Fatalf("the field values differ:\ntyped  %#v\nshaped %#v", typed.Fields, shaped.Fields)
	}

	// The control: SameKey has to be able to say no, or a stuck comparator
	// passes the assertion above whatever the two produced.
	moved := *pg
	moved.Code = "23503"
	if typed.SameKey(extract(&moved)) {
		t.Fatal("SameKey said two different SQLSTATEs would classify the same way")
	}
	moved = *pg
	moved.ColumnName = "email"
	if shaped.SameKey(sqlfault.Extract(&moved)) {
		t.Fatal("SameKey ignored a field the driver populated")
	}

	// The control on "they agree": there have to be two readers to agree. They
	// are meant to match on everything a classifier keys on and to differ on the
	// one field none of them reads, so a comparison of a function with itself
	// fails here rather than passing above.
	wrapped := fmt.Errorf("exec: %w", pg)
	if extract(wrapped).Message == sqlfault.Extract(wrapped).Message {
		t.Fatal("both sides of the comparison above are the by-shape reader, so their agreeing says nothing")
	}
}

// The typed arm is not the whole extractor. An error whose SQLSTATE is reachable
// only by shape has to classify the same as pgconn's, or one state is a 409
// through database/sql and a 500 through pgx — which is the divergence sqlfault
// exists to prevent.
type stateful struct {
	ConstraintName string
	TableName      string
	state          string
}

func (s *stateful) Error() string    { return "insert or update failed" }
func (s *stateful) SQLState() string { return s.state }

func TestANonPgErrorStillReachesTheByShapeReader(t *testing.T) {
	err := &stateful{state: "23505", ConstraintName: "users_email_key", TableName: "users"}

	// The control on the fixture: were this a *pgconn.PgError the typed arm would
	// answer it and the fallback would never run, so everything below would be
	// measuring the wrong branch.
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		t.Fatal("the fixture is a *pgconn.PgError, so it never reaches the fallback")
	}

	e := extract(err)
	if e == nil {
		t.Fatal("the fallback answered nothing, so a state pgx did not spell as a PgError is a 500")
	}
	if e.SQLState != "23505" {
		t.Fatalf("SQLState = %q, want 23505 read by shape", e.SQLState)
	}

	got := From(fake{}).conflict(err)
	f, ok := errs.AsFault(got)
	if !ok {
		t.Fatalf("no fault: %v", got)
	}
	if f.Code != errs.CodeUnique || !errors.Is(got, crud.ErrConflict) {
		t.Fatalf("Code = %q, conflict = %v; want unique and a sentinel a transport can answer 409 on", f.Code, errors.Is(got, crud.ErrConflict))
	}
	if f.Detail.Constraint != "users_email_key" || f.Detail.Table != "users" {
		t.Fatalf("Detail = %+v, want what the fixture named", f.Detail)
	}
}

var _ *sqlerr.Err = extract(nil)

// wantConflict is the three assertions one value has to satisfy at once, and it
// makes them through a further wrapping because that is how a service layer
// hands the error on: errors.Is for the sentinel a transport maps, errors.As for
// the driver error an operator needs, errs.AsFault for the code a client
// branches on ([[D-038]]).
func wantConflict(t *testing.T, err error) {
	t.Helper()
	wrapped := fmt.Errorf("saving user: %w", err)

	if !errors.Is(wrapped, crud.ErrConflict) {
		t.Fatalf("err = %v, want it to wrap crud.ErrConflict so a transport can answer 409", err)
	}
	var pg *pgconn.PgError
	if !errors.As(wrapped, &pg) || pg.Code != "23505" {
		t.Fatalf("the PgError underneath is gone, so the constraint name is unreachable: %v", err)
	}
	f, ok := errs.AsFault(wrapped)
	if !ok {
		t.Fatalf("no fault, so the conflict reaches a client with no code: %v", err)
	}
	if f.Code != errs.CodeUnique || f.Kind != errs.KindConflict {
		t.Fatalf("the fault says %v/%v, want conflict/unique", f.Kind, f.Code)
	}
	if f.Detail.Constraint != "users_email_key" || f.Detail.Table != "users" {
		t.Fatalf("Detail = %+v, want what pgconn named", f.Detail)
	}
}
