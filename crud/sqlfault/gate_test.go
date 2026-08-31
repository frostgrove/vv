package sqlfault

import (
	"testing"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/errs/sqlerr"
)

func TestTheTwoGatesAnswerDifferentQuestions(t *testing.T) {
	counts := map[string]int{}

	for _, tc := range []struct {
		name     string
		engine   string
		err      error
		sentinel bool
		code     errs.Code
	}{
		{"a PostgreSQL duplicate key", "postgres", pgish("23505"), true, errs.CodeUnique},
		{"a MySQL CHECK under HY000", "mysql", myish(3819, "HY000", "Check constraint 'ck' is violated."), true, errs.CodeCheck},
		{"a SQLite unique violation", "sqlite", &sqliteish{code: 2067}, true, errs.CodeUnique},

		{"a MySQL class-23 number that is not in the table", "mysql", myish(1216, "23000", "Cannot add or update a child row"), true, ""},
		{"a SQLite constraint subcode nothing has produced", "sqlite", &sqliteish{code: 19 | (9 << 8)}, true, ""},

		{"SQLITE_BUSY", "sqlite", &sqliteish{code: 5}, false, errs.CodeLockTimeout},
		{"a PostgreSQL serialisation failure", "postgres", pgish("40001"), false, errs.CodeSerializationFailure},
		{"a value too long for its column", "postgres", pgish("22001"), false, errs.CodeTooLong},
		{"an undefined table", "postgres", pgish("42P01"), false, errs.CodeSchemaNotReady},

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

	if Integrity(myish(1043, "", "Bad handshake")) {
		t.Fatal("a MySQL error the server sent with no SQLSTATE reached the SQLite arm: a refused handshake answers 409 with the driver's text in the body")
	}

	if !Integrity(&sqliteish{code: 2067}) {
		t.Fatal("the SQLite arm stopped classifying a unique violation")
	}

	if got := Extract(myish(1043, "", "Bad handshake")).Native; got != 1043 {
		t.Fatalf("Native = %d, want 1043 recorded as it always was", got)
	}
}
