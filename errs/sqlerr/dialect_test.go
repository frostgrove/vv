package sqlerr_test

import (
	"testing"

	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/errs/sqlerr"
)

// The dialect is part of the key, and this is where that is worth something.
//
// Not every cell of the grid refuses, and asserting that it did would be a
// false claim held down by a test: 22001, 22003 and 40001 are genuinely
// portable, so MySQL's too_long classifies the same way through PostgreSQL's
// table as through its own. That agreement is asserted below rather than left
// as a surprise for whoever expected the grid to be empty.
//
// What is written out here are the cells where the engines answer for the same
// condition in *different* keys. Each must refuse through the foreign parser,
// and each must classify through its own — the diagonal runs in the same loop,
// because a parser answering false unconditionally passes every off-diagonal
// cell and fails every diagonal one.
//
// The mysql↔mariadb pair is excluded by name and belongs to the test below:
// they share a driver, a dialect and nine table rows, and the two rows where
// they differ are the whole reason there are two files.
func TestARefusalFromOneEngineDoesNotClassifyThroughAnothersParser(t *testing.T) {
	all := corpora(t)

	for _, tc := range []struct {
		engine, name string
		foreign      []string
	}{
		// PostgreSQL's states are its own: nothing else spells a duplicate key
		// 23505, and SQLite has no state to spell it with.
		{"postgres", "unique", []string{"mysql", "mariadb", "sqlite"}},
		{"postgres", "check", []string{"mysql", "mariadb", "sqlite"}},
		{"postgres", "transaction_aborted", []string{"mysql", "mariadb", "sqlite"}},
		// A number with no state at all. The postgres arm must not read it, and
		// the mysql arm must not either.
		{"sqlite", "unique", []string{"postgres", "mysql", "mariadb"}},
		{"sqlite", "foreign_key", []string{"postgres", "mysql", "mariadb"}},
		{"sqlite", "lock_timeout", []string{"postgres", "mysql", "mariadb"}},
		// Class 23 with a number: PostgreSQL has no arm for 23000 and SQLite
		// refuses anything carrying a state.
		{"mysql", "unique", []string{"postgres", "sqlite"}},
		{"mysql", "check", []string{"postgres", "sqlite"}},
		{"mysql", "lock_timeout", []string{"postgres", "sqlite"}},
		{"mariadb", "check", []string{"postgres", "sqlite"}},
	} {
		cs := find(t, all[tc.engine], tc.name)
		if cs.Err == nil {
			t.Fatalf("%s/%s carries no captured error", tc.engine, tc.name)
		}
		// The diagonal, first: this is what stops the whole test passing for a
		// Classify that refuses everything.
		if _, _, ok := sqlerr.Classify(tc.engine, cs.Err); !ok {
			t.Fatalf("%s/%s does not classify through its own dialect, so the refusals below say nothing",
				tc.engine, tc.name)
		}
		for _, foreign := range tc.foreign {
			if code, _, ok := sqlerr.Classify(foreign, cs.Err); ok {
				t.Errorf("%s's %s (%s) classified as %q through the %s parser",
					tc.engine, tc.name, cs.Err.Key(), code, foreign)
			}
		}
	}

	// PostgreSQL's native number is zero on all twenty entries, so nothing in
	// its own corpus exercises the number half of the key at all. These are what
	// make its table's refusal of a stateless error and of another engine's
	// class-23 states mean something.

	// And the other side of the same coin, recorded rather than assumed: where
	// the two vocabularies really do agree, they agree.
	for _, name := range []string{"too_long", "out_of_range"} {
		my := find(t, all["mysql"], name)
		pg := find(t, all["postgres"], name)
		mine, _, ok := sqlerr.Classify("postgres", my.Err)
		if !ok {
			t.Errorf("MySQL's %s (%s) does not classify through PostgreSQL's table, and %s is one state both engines spell the same",
				name, my.Err.Key(), my.Err.SQLState)
			continue
		}
		if theirs, _, _ := sqlerr.Classify("postgres", pg.Err); mine != theirs {
			t.Errorf("through one table, MySQL's %s is %q and PostgreSQL's is %q", name, mine, theirs)
		}
	}
}

// The two rows where MySQL and MariaDB disagree, and the only thing in the tree
// that forces mariadb.go to exist. Merge the two tables and everything else in
// this package stays green.
//
// Each half is the other's control: both engines classifying their own pair is
// what stops this passing for a parser that classifies nothing, and each
// refusing the other's is what stops it passing for one merged table.
func TestMySQLAndMariaDBDoNotAnswerForEachOtherWhereTheyDiffer(t *testing.T) {
	all := corpora(t)

	for _, tc := range []struct {
		name          string
		mine, foreign string
		want          errs.Code
	}{
		{"check", "mysql", "mariadb", errs.CodeCheck},
		{"check", "mariadb", "mysql", errs.CodeCheck},
		{"bad_type", "mysql", "mariadb", errs.CodeInvalidFormat},
		{"bad_type", "mariadb", "mysql", errs.CodeInvalidFormat},
	} {
		cs := find(t, all[tc.mine], tc.name)
		if cs.Err == nil {
			t.Fatalf("%s/%s carries no captured error", tc.mine, tc.name)
		}
		other := find(t, all[tc.foreign], tc.name)
		if cs.Err.SQLState == other.Err.SQLState && cs.Err.Native == other.Err.Native {
			t.Fatalf("%s and %s now report %s with the same key %s — the two tables no longer disagree here and this test proves nothing",
				tc.mine, tc.foreign, tc.name, cs.Err.Key())
		}

		code, _, ok := sqlerr.Classify(tc.mine, cs.Err)
		if !ok || code != tc.want {
			t.Errorf("%s/%s (%s) classified as (%q, %v), want %q", tc.mine, tc.name, cs.Err.Key(), code, ok, tc.want)
		}
		if code, _, ok := sqlerr.Classify(tc.foreign, cs.Err); ok {
			t.Errorf("%s's %s (%s) classified as %q through the %s table — the two are one table again",
				tc.mine, tc.name, cs.Err.Key(), code, tc.foreign)
		}
	}
}

// SQLite's result code is read as two bytes and never compared whole.
//
// Every SQLITE_CONSTRAINT_* is 19 | (n<<8), so the low byte says what kind of
// failure it is and the high byte says which. Comparing the whole number is how
// busy-snapshot (517) becomes a constraint violation the day the codes shift,
// which is [[D-046]]'s own words.
func TestASQLiteResultCodeIsReadAsBytesAndNotWhole(t *testing.T) {
	all := corpora(t)

	// The captured half: five extended codes, five high bytes, three answers.
	for _, tc := range []struct {
		name string
		want errs.Code
	}{
		{"unique", errs.CodeUnique},          // 2067
		{"primary_key", errs.CodeUnique},     // 1555
		{"foreign_key", errs.CodeForeignKey}, // 787
		{"not_null", errs.CodeRequired},      // 1299
		{"check", errs.CodeCheck},            // 275
		{"lock_timeout", errs.CodeLockTimeout},
	} {
		cs := find(t, all["sqlite"], tc.name)
		if cs.Err == nil {
			t.Fatalf("sqlite/%s carries no captured error", tc.name)
		}
		if cs.Err.Native&0xff != 19 && tc.want != errs.CodeLockTimeout {
			t.Fatalf("sqlite/%s is now %d, whose low byte is not SQLITE_CONSTRAINT", tc.name, cs.Err.Native)
		}
		if code, _, ok := sqlerr.Classify("sqlite", cs.Err); !ok || code != tc.want {
			t.Errorf("sqlite/%s (%d) classified as (%q, %v), want %q", tc.name, cs.Err.Native, code, ok, tc.want)
		}
	}

	// The busy half, and the reason the whole code is never compared. 517 is not
	// in the corpus and is not a row in any table: it is asserted here as a
	// consequence of the low-byte rule, which is the only way it can be right
	// without adding a number from documentation.
	busy := &sqlerr.Err{Type: "*sqlite.Error", Native: 517, Message: "database is locked (517) (SQLITE_BUSY_SNAPSHOT)"}
	if code, _, ok := sqlerr.Classify("sqlite", busy); !ok || code != errs.CodeLockTimeout {
		t.Errorf("busy-snapshot 517 classified as (%q, %v), want lock_timeout — its low byte is SQLITE_BUSY, so a parser comparing the whole number matches no row at all and a retryable lock reaches the caller unclassified", code, ok)
	}

	// And an extended constraint code nobody has provoked stays unclassified
	// rather than being guessed. High byte 9 is SQLITE_CONSTRAINT_VTAB (2323),
	// which no probe in the corpus produces — this library creates no virtual
	// tables, so the case stays honest however the corpus grows.
	vtab := &sqlerr.Err{Type: "*sqlite.Error", Native: 19 | (9 << 8)}
	if code, _, ok := sqlerr.Classify("sqlite", vtab); ok {
		t.Errorf("a constraint subcode the corpus never produced answered %q — a number from documentation is exactly what the corpus exists to stop", code)
	}
}

// SQLite's number is read only where there is no SQLSTATE.
//
// pgconn spells the SQLSTATE in a field named Code, so an extractor asking by
// shape can hand a PostgreSQL error to this arm with a number that means
// nothing. This is the parser-level twin of
// TestASQLiteCodeIsOnlyTrustedWithoutASQLSTATE in adapter/crudsql.
func TestASQLiteCodeIsOnlyReadWhereThereIsNoSQLSTATE(t *testing.T) {
	for _, state := range []string{"23505", "23000", "HY000", "42P01"} {
		e := &sqlerr.Err{Type: "*sqlite.Error", SQLState: state, Native: 2067}
		if code, _, ok := sqlerr.Classify("sqlite", e); ok {
			t.Errorf("a number under SQLSTATE %s classified as %q; SQLite has no SQLSTATE and never will", state, code)
		}
	}

	// The control: the same number with the state cleared is a unique violation,
	// so what is refused above is the state and not the number.
	e := &sqlerr.Err{Type: "*sqlite.Error", Native: 2067}
	if code, _, ok := sqlerr.Classify("sqlite", e); !ok || code != errs.CodeUnique {
		t.Fatalf("with no SQLSTATE the same number answered (%q, %v), so the refusals above are about something else", code, ok)
	}
}
