package crudsql

import (
	"errors"
	"fmt"
	"testing"

	"github.com/shardit-io/ordo/crud"
)

// The classifier has to find a SQLSTATE in an error whose type this package is
// not allowed to name — the module has no dependencies, drivers included — so it
// asks by shape. Two shapes exist in the wild and neither is guessable from the
// other: pgx and lib/pq expose a method, go-sql-driver/mysql an exported array
// field. This is the fragile half of the mapping, and the half no database can
// prove, so it is pinned here rather than through an engine.

// pgErr is the shape pgx and lib/pq present: a method.
type pgErr struct{ state string }

func (e pgErr) Error() string    { return "pq: " + e.state }
func (e pgErr) SQLState() string { return e.state }

// myErr is go-sql-driver/mysql's shape: an exported [5]byte field on a struct
// reached through a pointer.
type myErr struct {
	Number   uint16
	SQLState [5]byte
	Message  string
}

func (e *myErr) Error() string { return e.Message }

// oddErr has a SQLState field of a type nothing can read a state out of. It must
// not panic and must not be classified.
type oddErr struct{ SQLState int }

func (e oddErr) Error() string { return "odd" }

func state(s string) [5]byte {
	var b [5]byte
	copy(b[:], s)
	return b
}

func TestIntegrityErrorsAreClassifiedWhateverShapeTheDriverUses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		conflict bool
	}{
		{"a driver that exposes SQLState() — unique violation", pgErr{"23505"}, true},
		{"a driver that exposes SQLState() — foreign key", pgErr{"23503"}, true},
		{"a driver that exposes SQLState() — not null", pgErr{"23502"}, true},
		{"a driver with a SQLState [5]byte field", &myErr{1062, state("23000"), "Duplicate entry"}, true},
		{"wrapped twice on its way up", fmt.Errorf("saving user: %w",
			fmt.Errorf("exec: %w", pgErr{"23505"})), true},

		{"a syntax error is the caller's mistake, not a conflict", pgErr{"42601"}, false},
		{"a serialisation failure is worth retrying, not refusing", pgErr{"40001"}, false},
		{"a connection that went away", errors.New("driver: bad connection"), false},
		{"nothing at all", nil, false},
		{"a SQLState field of the wrong type", oddErr{SQLState: 23}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := conflict(tc.err)
			if errors.Is(got, crud.ErrConflict) != tc.conflict {
				t.Fatalf("errors.Is(%v, crud.ErrConflict) = %v, want %v", got, !tc.conflict, tc.conflict)
			}
			if tc.err == nil {
				return
			}
			// The driver's error stays reachable underneath: a caller who wants
			// the constraint name must still be able to get at it.
			if !errors.Is(got, tc.err) {
				t.Fatalf("the driver error was replaced rather than wrapped: %v", got)
			}
		})
	}
}

// A conflict must never be mistaken for one of the sentinels a transport turns
// into a 404, a 400 or a 403 — those are all answers that tell the client to
// stop trying, and a duplicate key is not one of them.
func TestAClassifiedConflictIsNotAnyOtherSentinel(t *testing.T) {
	err := conflict(pgErr{"23505"})
	for _, sentinel := range []error{crud.ErrNotFound, crud.ErrMissingID, crud.ErrReadOnly, crud.ErrForbidden} {
		if errors.Is(err, sentinel) {
			t.Fatalf("a constraint violation also reads as %v", sentinel)
		}
	}
}
