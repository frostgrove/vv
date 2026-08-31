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

type deferred struct {
	pgx.Rows
	err error
}

func (this deferred) Next() bool      { return false }
func (this deferred) Err() error      { return this.err }
func (this deferred) Close()          {}
func (this deferred) Conn() *pgx.Conn { return nil }

type fake struct {
	err      error
	deferErr bool
}

func (this fake) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, this.err
}

func (this fake) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	if this.deferErr {
		return deferred{err: this.err}, nil
	}
	return nil, this.err
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

func TestSourceBoundExecutorInheritsItsClassifier(t *testing.T) {
	custom := sqlfault.New("postgres")
	source := Open(fake{}, WithFaults(custom))
	ctx := source.BindExecutor(context.Background(), fake{})

	bound, ok := crud.ExecutorFor(ctx, source)
	if !ok {
		t.Fatal("the adapter helper did not bind its source")
	}
	executor, ok := bound.(Executor)
	if !ok {
		t.Fatalf("bound executor is %T, want crudpgx.Executor", bound)
	}
	if executor.faults != custom {
		t.Fatalf("joined executor inherited %#v, want the receiver's classifier %#v", executor.faults, custom)
	}
}

func TestEveryBindingPathRefusesATypedNilInnerPgxHandle(t *testing.T) {
	var q *fake
	source := Open(fake{})

	if _, err := crud.NewSession(source, From(q)); !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("NewSession returned %v, want ErrExecutorScope", err)
	}

	contexts := map[string]context.Context{
		"adapter helper":       source.BindExecutor(context.Background(), q),
		"deprecated inference": crud.WithExecutor(context.Background(), From(q)),
		"low-level scoped":     crud.WithExecutorFor(context.Background(), source, From(q)),
		"explicit unsafe":      crud.WithUnsafeExecutor(context.Background(), From(q)),
	}
	for name, ctx := range contexts {
		t.Run(name, func(t *testing.T) {
			executor, ok := crud.ExecutorFor(ctx, source)
			if !ok {
				t.Fatal("typed-nil declaration was silently ignored")
			}
			if _, err := executor.Exec(ctx, "must not dereference the typed-nil handle"); !errors.Is(err, crud.ErrExecutorScope) {
				t.Fatalf("Exec returned %v, want ErrExecutorScope", err)
			}
			var scoped *crud.ExecutorScopeError
			if _, err := executor.Exec(ctx, "still no driver call"); !errors.As(err, &scoped) || scoped.Reason != crud.ExecutorScopeMissingExecutor {
				t.Fatalf("scope error = %#v, want missing_executor", scoped)
			}
		})
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

func TestOnlyIntegrityErrorsBecomeConflicts(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		err  error
		code errs.Code
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

	bare := extract(&pgconn.PgError{Code: "23505"})
	if bare.Fields != nil {
		t.Fatalf("Fields = %#v, want nil for an error that named nothing", bare.Fields)
	}
	if bare.Native != 0 {
		t.Fatalf("Native = %d, want 0", bare.Native)
	}

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

	if got := extract(fmt.Errorf("exec: %w", pg)).Message; got != pg.Message {
		t.Fatalf("Message = %q, want the server's own text %q: extraction fell through to the by-shape reader", got, pg.Message)
	}
}

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

	wrapped := fmt.Errorf("exec: %w", pg)
	if extract(wrapped).Message == sqlfault.Extract(wrapped).Message {
		t.Fatal("both sides of the comparison above are the by-shape reader, so their agreeing says nothing")
	}
}

type stateful struct {
	ConstraintName string
	TableName      string
	state          string
}

func (this *stateful) Error() string    { return "insert or update failed" }
func (this *stateful) SQLState() string { return this.state }

func TestANonPgErrorStillReachesTheByShapeReader(t *testing.T) {
	err := &stateful{state: "23505", ConstraintName: "users_email_key", TableName: "users"}

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
