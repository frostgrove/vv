package crudsql

import (
	"errors"
	"fmt"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
)

// gate is an executor with no classifier: the sentinel gate on its own, which is
// what every test below this line is about. An executor built by Postgres or
// MySQL answers the same sentinel and also carries a code — that half is
// classify_test.go's.
var gate = From(nil)

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
			got := gate.conflict(tc.err)
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
	err := gate.conflict(pgErr{"23505"})
	for _, sentinel := range []error{crud.ErrNotFound, crud.ErrMissingID, crud.ErrReadOnly, crud.ErrForbidden} {
		if errors.Is(err, sentinel) {
			t.Fatalf("a constraint violation also reads as %v", sentinel)
		}
	}
}

// mysqlish has the shape go-sql-driver/mysql's *MySQLError has: a numeric
// Number and a [5]byte SQLState. The package may not import the driver, so the
// test cannot either — it asserts the shape the classifier reaches for.
type mysqlish struct {
	Number   uint16
	SQLState [5]byte
	Message  string
}

func (e *mysqlish) Error() string { return e.Message }

func newMySQLish(number uint16, state, msg string) *mysqlish {
	e := &mysqlish{Number: number, Message: msg}
	copy(e.SQLState[:], state)
	return e
}

func TestMySQLIntegrityErrorsOutsideClass23BecomeConflicts(t *testing.T) {
	// Measured on MySQL 8.4: a CHECK violation is 3819 and a missing column
	// default is 1364, and both arrive as HY000 rather than class 23. Before
	// this, neither was classified and a client got a bare 500 where FL-011
	// promises 409.
	for _, tc := range []struct {
		name   string
		number uint16
		msg    string
	}{
		{"check constraint", 3819, "Check constraint 'ck_age' is violated."},
		{"missing default", 1364, "Field 'nodef' doesn't have a default value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := gate.conflict(newMySQLish(tc.number, "HY000", tc.msg))
			if !errors.Is(got, crud.ErrConflict) {
				t.Fatalf("MySQL %d is an integrity violation and did not classify as one: %v", tc.number, got)
			}
		})
	}
}

func TestAnOrdinaryHY000IsStillNotAConflict(t *testing.T) {
	// The control. Without it the test above would pass for a classifier that
	// treated every HY000 as a conflict, which would turn ordinary server
	// errors into 409s.
	got := gate.conflict(newMySQLish(1146, "HY000", "Table 'x.y' doesn't exist"))
	if errors.Is(got, crud.ErrConflict) {
		t.Fatal("a missing table is not an integrity violation")
	}
}

func TestANumberIsOnlyTrustedUnderHY000(t *testing.T) {
	// A numeric Number field on some other driver's error must not be read as a
	// MySQL error code.
	got := gate.conflict(newMySQLish(3819, "08006", "connection failure"))
	if errors.Is(got, crud.ErrConflict) {
		t.Fatal("the number was trusted outside HY000, where it means nothing")
	}

	// And with no state at all it must not be read as a SQLite result code. The
	// SQLSTATE is optional in MySQL's ERR packet and go-sql-driver/mysql leaves
	// the [5]byte unset when the '#' marker is absent, so this is the shape a
	// connection-phase failure arrives in: 1043 is ER_HANDSHAKE_ERROR and 0x413
	// carries 19, SQLITE_CONSTRAINT, in its low byte.
	got = gate.conflict(newMySQLish(1043, "", "Bad handshake"))
	if errors.Is(got, crud.ErrConflict) {
		t.Fatal("a MySQL error with no SQLSTATE reached the SQLite arm: a refused handshake answers 409 with the driver's text in the body")
	}
	// The control: the arm still fires for the engine it is for. Without it the
	// line above passes for an arm nothing reaches at all.
	if !errors.Is(gate.conflict(&sqliteish{code: 2067}), crud.ErrConflict) {
		t.Fatal("the SQLite arm stopped classifying a unique violation")
	}
}

// sqliteish has the shape modernc.org/sqlite's *Error has: a Code method and no
// SQLSTATE at all. mattnish has mattn/go-sqlite3's: integer fields. Neither
// driver may be imported here, so the test asserts the shapes rather than the
// types, exactly as the pgx and MySQL cases above do.
type sqliteish struct{ code int }

func (e *sqliteish) Error() string { return "constraint failed" }
func (e *sqliteish) Code() int     { return e.code }

type mattnish struct {
	Code         int
	ExtendedCode int
}

func (e *mattnish) Error() string { return "constraint failed" }

func TestSQLiteConstraintViolationsBecomeConflicts(t *testing.T) {
	// SQLite has no SQLSTATE and never will, so a classifier keyed on SQLSTATE
	// alone cannot see any of these. Every one of them was a bare 500 — seven
	// classes on a shipped dialect — because the one live test that would have
	// caught it runs over a target list SQLite is not on.
	//
	// The numbers are extended result codes, measured against the running
	// driver, and all of them carry SQLITE_CONSTRAINT (19) in the low byte.
	for _, tc := range []struct {
		name string
		code int
	}{
		{"unique", 2067},
		{"primary key", 1555},
		{"foreign key", 787},
		{"not null", 1299},
		{"check", 275},
		{"the bare primary code", 19},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := gate.conflict(&sqliteish{code: tc.code}); !errors.Is(got, crud.ErrConflict) {
				t.Fatalf("SQLite %d is a constraint violation and did not classify as one: %v", tc.code, got)
			}
			if got := gate.conflict(&mattnish{Code: 19, ExtendedCode: tc.code}); !errors.Is(got, crud.ErrConflict) {
				t.Fatalf("SQLite %d through an integer-field driver did not classify: %v", tc.code, got)
			}
		})
	}
}

func TestAnOrdinarySQLiteErrorIsStillNotAConflict(t *testing.T) {
	// The control. Without it the test above would pass for a classifier that
	// treated every SQLite error as a conflict — and SQLITE_BUSY in particular
	// is retryable, not the caller's to fix.
	for _, tc := range []struct {
		name string
		code int
	}{
		{"busy", 5},
		{"busy snapshot", 517},
		{"cannot open", 14},
		{"generic error", 1},
		{"readonly, which shares no low byte with constraint", 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := gate.conflict(&sqliteish{code: tc.code}); errors.Is(got, crud.ErrConflict) {
				t.Fatalf("SQLite %d is not an integrity violation", tc.code)
			}
		})
	}
}

func TestASQLiteCodeIsOnlyTrustedWithoutASQLSTATE(t *testing.T) {
	// pgconn spells the SQLSTATE in a field called Code. Reading that as a
	// number would classify by coincidence, so this arm is reached only when
	// there is no SQLSTATE — which for pgconn there always is.
	got := gate.conflict(&pgErr{state: "42P01"})
	if errors.Is(got, crud.ErrConflict) {
		t.Fatal("a missing relation classified as a conflict")
	}
}

// [[D-038]]'s owed regression, at the gate rather than in the walk.
//
// errors.Unwrap returns nil for a multi-error, and fmt.Errorf("%w: %w", …) is
// what this package's own conflict() builds — so a plain loop went blind on the
// layer's own output the moment anything wrapped twice. The three readers it
// replaced all walked that way and all three are covered here, because the
// forbid is general: the MySQL number and the SQLite result code were separate
// walks with the same blindness, and they are the two arms phase 0 added.
//
// The fault leg is the idempotency guard rather than the walk: an error already
// carrying a fault is returned untouched, so the walk never runs on it. That the
// walk *can* see through errs.Fault.Unwrap() []error is
// TestADriverErrorIsFoundThroughEveryWrappingShape's, in sqlfault.
func TestASQLSTATEIsStillFoundThroughAMultiErrorAndThroughAFault(t *testing.T) {
	for _, tc := range []struct {
		name     string
		driver   error
		code     errs.Code
		unwanted error
	}{
		{"a PostgreSQL duplicate key", pgErr{"23505"}, errs.CodeUnique, pgErr{"42P01"}},
		{"a MySQL CHECK under HY000", newMySQLish(3819, "HY000", "Check constraint 'ck' is violated."),
			errs.CodeCheck, newMySQLish(1146, "HY000", "Table 'x.y' doesn't exist")},
		// The negatives have to be errors nothing classifies at all. SQLITE_BUSY
		// would not do: it is not a violation and it *is* classified —
		// lock_timeout — which is the sentinel-no/code-yes cell rather than a
		// control on this one.
		{"a SQLite unique violation", &sqliteish{code: 2067}, errs.CodeUnique, &sqliteish{code: 14}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := Postgres(nil)
			switch tc.driver.(type) {
			case *mysqlish:
				src = MySQL(nil)
			case *sqliteish:
				src = SQLite(nil)
			}

			t.Run("through a multi-error", func(t *testing.T) {
				got := src.conflict(fmt.Errorf("%w: %w", crud.ErrConflict, tc.driver))
				f, ok := errs.AsFault(got)
				if !ok {
					t.Fatalf("no code was learned, so the SQLSTATE was not found through the multi-error: %v", got)
				}
				if f.Code != tc.code {
					t.Fatalf("Code = %q, want %q", f.Code, tc.code)
				}
				if !errors.Is(got, crud.ErrConflict) || !errors.Is(got, tc.driver) {
					t.Fatalf("the sentinel or the driver error was lost: %v", got)
				}

				// The control. The same shape over an error that is not a
				// violation must learn nothing — the sentinel in it came from
				// the fixture, so errors.Is says nothing here and only the
				// absence of a code does.
				if f, ok := errs.AsFault(src.conflict(fmt.Errorf("%w: %w", crud.ErrConflict, tc.unwanted))); ok {
					t.Fatalf("a code (%q) was learned for something that is not a violation", f.Code)
				}
			})

			t.Run("through errors.Join, which carries no sentinel of its own", func(t *testing.T) {
				got := gate.conflict(errors.Join(errors.New("saving user"), tc.driver))
				if !errors.Is(got, crud.ErrConflict) {
					t.Fatalf("the violation was not found through errors.Join: %v", got)
				}
				// The control, and this one can use the sentinel: nothing in the
				// fixture carries it.
				got = gate.conflict(errors.Join(errors.New("saving user"), tc.unwanted))
				if errors.Is(got, crud.ErrConflict) {
					t.Fatalf("%v became a conflict", tc.unwanted)
				}
			})

			t.Run("an error that already carries a fault is left alone", func(t *testing.T) {
				already := errs.Conflict().Code(tc.code).Wrapping(crud.ErrConflict, tc.driver).Fault()
				got := src.conflict(already)
				if got != error(already) {
					t.Fatalf("the fault was classified a second time: %v", got)
				}
				if !errors.Is(got, crud.ErrConflict) || !errors.Is(got, tc.driver) {
					t.Fatalf("the sentinel or the driver error was lost: %v", got)
				}
			})
		})
	}
}

// pgconnish is the pgconn shape: a SQLState method and named string fields. The
// spellings are pgconn's own — ConstraintName, TableName, SchemaName — and
// getting one wrong produces a silently empty Source that no compiler catches.
type pgconnish struct {
	Code                                string
	Message                             string
	ConstraintName, TableName           string
	SchemaName, ColumnName              string
	DataTypeName, Detail, Hint, Routine string
}

func (e *pgconnish) Error() string    { return e.Message }
func (e *pgconnish) SQLState() string { return e.Code }

// pqish is lib/pq's shape, which spells the same three fields differently. No
// capture exists for it, so nothing here reads them — deliberately, and this is
// where that refusal is written down rather than left to look like an oversight
// ([[D-046]]: provoke it, capture it, let the entry be the citation).
type pqish struct {
	state                     string
	Constraint, Table, Column string
}

func (e *pqish) Error() string    { return "pq: duplicate key" }
func (e *pqish) SQLState() string { return e.state }

func TestTheExtractorReachesTheStructuredFieldsByShape(t *testing.T) {
	driver := &pgconnish{
		Code:           "23505",
		Message:        `duplicate key value violates unique constraint "users_email_key"`,
		ConstraintName: "users_email_key",
		TableName:      "users",
		SchemaName:     "public",
	}
	got := Postgres(nil).conflict(driver)

	// The three assertions, in the form this module can write them: errors.As
	// against a driver type is a name adapter/crudsql may not spell.
	if !errors.Is(got, crud.ErrConflict) {
		t.Fatalf("not a conflict: %v", got)
	}
	if !errors.Is(got, error(driver)) {
		t.Fatalf("the driver error was replaced rather than wrapped: %v", got)
	}
	f, ok := errs.AsFault(got)
	if !ok {
		t.Fatalf("no fault: %v", got)
	}
	if f.Detail.Constraint != "users_email_key" || f.Detail.Table != "users" {
		t.Fatalf("Detail = %+v, want the constraint and table pgconn named", f.Detail)
	}
	if f.Violations[0].Source.Schema != "public" {
		t.Fatalf("Source = %+v, want the schema pgconn named", f.Violations[0].Source)
	}

	// The control: the other driver's spellings classify — the SQLState method
	// still works — and fill in nothing. What is pinned is the refusal.
	got = Postgres(nil).conflict(&pqish{state: "23505", Constraint: "users_email_key", Table: "users", Column: "email"})
	f, ok = errs.AsFault(got)
	if !ok {
		t.Fatalf("a lib/pq-shaped error stopped classifying at all: %v", got)
	}
	if src := f.Violations[0].Source; src.Constraint != "" || src.Table != "" || src.Schema != "" || src.Columns != nil {
		t.Fatalf("Source = %+v: lib/pq's spellings are read, and no capture exists for them", src)
	}
	if f.Detail.Constraint != "" || f.Detail.Table != "" {
		t.Fatalf("Detail = %+v, want nothing carried across", f.Detail)
	}
}
