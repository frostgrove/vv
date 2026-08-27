package sqlfault

import (
	"testing"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/errs/sqlerr"
)

// The two gates answer different questions, and phase 3 owes the test that says
// which answers what ([[D-046]]).
//
// Integrity decides crud.ErrConflict and is deliberately the wider of the two: a
// class-23 number nobody provoked is a conflict here and unclassified next door,
// because narrowing the sentinel onto the parser's table would turn a duplicate
// key into a 500 on an engine nobody has run yet. sqlerr.Classify decides the
// code and covers classes the sentinel refuses on purpose — a value too long is
// the caller's to fix and is not a collision.
//
// All four cells are populated, and the counters are what keep them that way. If
// either gate widens or narrows into the other, a cell empties and a table with
// three live rows would otherwise still pass.
func TestTheTwoGatesAnswerDifferentQuestions(t *testing.T) {
	counts := map[string]int{}

	for _, tc := range []struct {
		name     string
		engine   string
		err      error
		sentinel bool
		code     errs.Code
	}{
		// Both. The ordinary case, and the only cell a naive implementation gets
		// right.
		{"a PostgreSQL duplicate key", "postgres", pgish("23505"), true, errs.CodeUnique},
		{"a MySQL CHECK under HY000", "mysql", myish(3819, "HY000", "Check constraint 'ck' is violated."), true, errs.CodeCheck},
		{"a SQLite unique violation", "sqlite", &sqliteish{code: 2067}, true, errs.CodeUnique},

		// The sentinel and no code. A number nobody provoked, and a constraint
		// subcode no probe produced: a 409 with no code rather than a 500.
		{"a MySQL class-23 number that is not in the table", "mysql", myish(1216, "23000", "Cannot add or update a child row"), true, ""},
		{"a SQLite constraint subcode nothing has produced", "sqlite", &sqliteish{code: 19 | (9 << 8)}, true, ""},

		// A code and no sentinel — the dangerous direction. A fault exists, and
		// it must not become a 409 by being a fault. The missing-table case is
		// operational rather than request-shaped, but follows the same rule.
		{"SQLITE_BUSY", "sqlite", &sqliteish{code: 5}, false, errs.CodeLockTimeout},
		{"a PostgreSQL serialisation failure", "postgres", pgish("40001"), false, errs.CodeSerializationFailure},
		{"a value too long for its column", "postgres", pgish("22001"), false, errs.CodeTooLong},
		{"an undefined table", "postgres", pgish("42P01"), false, errs.CodeSchemaNotReady},

		// Neither.
		{"a PostgreSQL syntax error", "postgres", pgish("42601"), false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Integrity(tc.err); got != tc.sentinel {
				t.Fatalf("Integrity = %v, want %v — this is the gate crud.ErrConflict is decided by", got, tc.sentinel)
			}
			code, _, ok := sqlerr.Classify(tc.engine, Extract(tc.err))
			if !ok {
				code = ""
			}
			if code != tc.code {
				t.Fatalf("sqlerr.Classify answered %q, want %q", code, tc.code)
			}
			counts[cell(tc.sentinel, tc.code != "")]++
		})
	}

	// The control. Each cell has to have been walked, or the grid is a list.
	for _, sentinel := range []bool{true, false} {
		for _, code := range []bool{true, false} {
			if counts[cell(sentinel, code)] == 0 {
				t.Errorf("no case in the %s cell: one gate has moved into the other and the table stopped saying so",
					cell(sentinel, code))
			}
		}
	}
}

func cell(sentinel, code bool) string {
	return map[bool]string{true: "sentinel yes", false: "sentinel no"}[sentinel] +
		map[bool]string{true: " / code yes", false: " / code no"}[code]
}

// The number is only read once the state has said which engine is speaking, and
// the SQLite arm is only reached when there is no state at all. Both are
// [[D-046]]'s forbids, and both were live bugs.
func TestANumberIsOnlyReadOnceTheStateSaysWhichEngineItIs(t *testing.T) {
	if Integrity(myish(3819, "08006", "connection failure")) {
		t.Fatal("3819 was trusted outside HY000, where it means nothing")
	}
	if Integrity(pgish("42P01")) {
		t.Fatal("a PostgreSQL error reached the SQLite arm — its Code field holds a string, not a number")
	}
	if Integrity(&sqliteish{code: 517}) {
		t.Fatal("busy-snapshot was compared whole rather than by its low byte")
	}

	// And the arm with no state behind it, which is the one with no engine to go
	// on. "MySQL always carries a state" is not true of the driver: the SQLSTATE
	// is optional in the ERR packet and go-sql-driver/mysql leaves the [5]byte
	// unset when the '#' marker is absent. 1043 is ER_HANDSHAKE_ERROR and 0x413
	// has 19 — SQLITE_CONSTRAINT — in its low byte, so nothing about the number
	// itself separates a refused handshake from a SQLite constraint violation.
	if Integrity(myish(1043, "", "Bad handshake")) {
		t.Fatal("a MySQL error the server sent with no SQLSTATE reached the SQLite arm: a refused handshake answers 409 with the driver's text in the body")
	}
	// The first control. Without it the line above passes for an arm that was
	// deleted rather than narrowed.
	if !Integrity(&sqliteish{code: 2067}) {
		t.Fatal("the SQLite arm stopped classifying a unique violation")
	}
	// The second, and the one that keeps the narrowing honest: the number is
	// still recorded. Blanking Native would satisfy the assertion above and rot
	// every MySQL and MariaDB entry in the corpus instead — it is the gate that
	// must not read it as SQLite's, not the extraction that must stop reading it.
	if got := Extract(myish(1043, "", "Bad handshake")).Native; got != 1043 {
		t.Fatalf("Native = %d, want 1043 recorded as it always was", got)
	}
}
