package crudpgx

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"rx-crud/crud"
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
	return &pgconn.PgError{Code: "23505", Message: `duplicate key value violates unique constraint "users_email_key"`}
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
}

// Everything else has to pass through untouched. A serialisation failure is
// retryable and a syntax error is the caller's bug; answering 409 to either
// would send the client back with the same request.
func TestOnlyIntegrityErrorsBecomeConflicts(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"a serialisation failure", &pgconn.PgError{Code: "40001"}},
		{"a syntax error", &pgconn.PgError{Code: "42601"}},
		{"a dead connection", errors.New("conn closed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := From(fake{err: tc.err, deferErr: true}).Query(ctx, "SELECT 1")
			if err != nil {
				t.Fatal(err)
			}
			rows.Next()
			if errors.Is(rows.Err(), crud.ErrConflict) {
				t.Fatalf("%v was classified as a conflict", tc.err)
			}
			if !errors.Is(rows.Err(), tc.err) {
				t.Fatalf("the driver error was lost: %v", rows.Err())
			}
		})
	}
}

func wantConflict(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, crud.ErrConflict) {
		t.Fatalf("err = %v, want it to wrap crud.ErrConflict so a transport can answer 409", err)
	}
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23505" {
		t.Fatalf("the PgError underneath is gone, so the constraint name is unreachable: %v", err)
	}
}
