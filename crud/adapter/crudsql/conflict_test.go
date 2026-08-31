package crudsql

import (
	"errors"
	"fmt"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
)

var gate = From(nil)

type pgErr struct{ state string }

func (this pgErr) Error() string    { return "pq: " + this.state }
func (this pgErr) SQLState() string { return this.state }

type myErr struct {
	Number   uint16
	SQLState [5]byte
	Message  string
}

func (this *myErr) Error() string { return this.Message }

type oddErr struct{ SQLState int }

func (this oddErr) Error() string { return "odd" }

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

			if !errors.Is(got, tc.err) {
				t.Fatalf("the driver error was replaced rather than wrapped: %v", got)
			}
		})
	}
}

func TestAClassifiedConflictIsNotAnyOtherSentinel(t *testing.T) {
	err := gate.conflict(pgErr{"23505"})
	for _, sentinel := range []error{crud.ErrNotFound, crud.ErrMissingID, crud.ErrReadOnly, crud.ErrForbidden} {
		if errors.Is(err, sentinel) {
			t.Fatalf("a constraint violation also reads as %v", sentinel)
		}
	}
}

type mysqlish struct {
	Number   uint16
	SQLState [5]byte
	Message  string
}

func (this *mysqlish) Error() string { return this.Message }

func newMySQLish(number uint16, state, message string) *mysqlish {
	e := &mysqlish{Number: number, Message: message}
	copy(e.SQLState[:], state)
	return e
}

func TestMySQLIntegrityErrorsOutsideClass23BecomeConflicts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		number  uint16
		message string
	}{
		{"check constraint", 3819, "Check constraint 'ck_age' is violated."},
		{"missing default", 1364, "Field 'nodef' doesn't have a default value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := gate.conflict(newMySQLish(tc.number, "HY000", tc.message))
			if !errors.Is(got, crud.ErrConflict) {
				t.Fatalf("MySQL %d is an integrity violation and did not classify as one: %v", tc.number, got)
			}
		})
	}
}

func TestAnOrdinaryHY000IsStillNotAConflict(t *testing.T) {
	got := gate.conflict(newMySQLish(1146, "HY000", "Table 'x.y' doesn't exist"))
	if errors.Is(got, crud.ErrConflict) {
		t.Fatal("a missing table is not an integrity violation")
	}
}

func TestANumberIsOnlyTrustedUnderHY000(t *testing.T) {
	got := gate.conflict(newMySQLish(3819, "08006", "connection failure"))
	if errors.Is(got, crud.ErrConflict) {
		t.Fatal("the number was trusted outside HY000, where it means nothing")
	}

	got = gate.conflict(newMySQLish(1043, "", "Bad handshake"))
	if errors.Is(got, crud.ErrConflict) {
		t.Fatal("a MySQL error with no SQLSTATE reached the SQLite arm: a refused handshake answers 409 with the driver's text in the body")
	}

	if !errors.Is(gate.conflict(&sqliteish{code: 2067}), crud.ErrConflict) {
		t.Fatal("the SQLite arm stopped classifying a unique violation")
	}
}

type sqliteish struct{ code int }

func (this *sqliteish) Error() string { return "constraint failed" }
func (this *sqliteish) Code() int     { return this.code }

type mattnish struct {
	Code         int
	ExtendedCode int
}

func (this *mattnish) Error() string { return "constraint failed" }

func TestSQLiteConstraintViolationsBecomeConflicts(t *testing.T) {
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
	got := gate.conflict(&pgErr{state: "42P01"})
	if errors.Is(got, crud.ErrConflict) {
		t.Fatal("a missing relation classified as a conflict")
	}
}

func TestASQLSTATEIsStillFoundThroughAMultiErrorAndThroughAFault(t *testing.T) {
	for _, tc := range []struct {
		name     string
		driver   error
		code     errs.Code
		unwanted error
	}{
		{"a PostgreSQL duplicate key", pgErr{"23505"}, errs.CodeUnique, pgErr{"42601"}},
		{"a MySQL CHECK under HY000", newMySQLish(3819, "HY000", "Check constraint 'ck' is violated."),
			errs.CodeCheck, newMySQLish(1146, "HY000", "Table 'x.y' doesn't exist")},

		{"a SQLite unique violation", &sqliteish{code: 2067}, errs.CodeUnique, &sqliteish{code: 14}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := Postgres(nil)
			switch tc.driver.(type) {
			case *mysqlish:
				source = MySQL(nil)
			case *sqliteish:
				source = SQLite(nil)
			}

			t.Run("through a multi-error", func(t *testing.T) {
				got := source.conflict(fmt.Errorf("%w: %w", crud.ErrConflict, tc.driver))
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

				if f, ok := errs.AsFault(source.conflict(fmt.Errorf("%w: %w", crud.ErrConflict, tc.unwanted))); ok {
					t.Fatalf("a code (%q) was learned for something that is not a violation", f.Code)
				}
			})

			t.Run("through errors.Join, which carries no sentinel of its own", func(t *testing.T) {
				got := gate.conflict(errors.Join(errors.New("saving user"), tc.driver))
				if !errors.Is(got, crud.ErrConflict) {
					t.Fatalf("the violation was not found through errors.Join: %v", got)
				}

				got = gate.conflict(errors.Join(errors.New("saving user"), tc.unwanted))
				if errors.Is(got, crud.ErrConflict) {
					t.Fatalf("%v became a conflict", tc.unwanted)
				}
			})

			t.Run("an error that already carries a fault is left alone", func(t *testing.T) {
				already := errs.Conflict().Code(tc.code).Wrapping(crud.ErrConflict, tc.driver).Fault()
				got := source.conflict(already)
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

type pgconnish struct {
	Code                                string
	Message                             string
	ConstraintName, TableName           string
	SchemaName, ColumnName              string
	DataTypeName, Detail, Hint, Routine string
}

func (this *pgconnish) Error() string    { return this.Message }
func (this *pgconnish) SQLState() string { return this.Code }

type pqish struct {
	state                     string
	Constraint, Table, Column string
}

func (this *pqish) Error() string    { return "pq: duplicate key" }
func (this *pqish) SQLState() string { return this.state }

func TestTheExtractorReachesTheStructuredFieldsByShape(t *testing.T) {
	driver := &pgconnish{
		Code:           "23505",
		Message:        `duplicate key value violates unique constraint "users_email_key"`,
		ConstraintName: "users_email_key",
		TableName:      "users",
		SchemaName:     "public",
	}
	got := Postgres(nil).conflict(driver)

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

	got = Postgres(nil).conflict(&pqish{state: "23505", Constraint: "users_email_key", Table: "users", Column: "email"})
	f, ok = errs.AsFault(got)
	if !ok {
		t.Fatalf("a lib/pq-shaped error stopped classifying at all: %v", got)
	}
	if source := f.Violations[0].Source; source.Constraint != "" || source.Table != "" || source.Schema != "" || source.Columns != nil {
		t.Fatalf("Source = %+v: lib/pq's spellings are read, and no capture exists for them", source)
	}
	if f.Detail.Constraint != "" || f.Detail.Table != "" {
		t.Fatalf("Detail = %+v, want nothing carried across", f.Detail)
	}
}
