package sqlerr_test

import (
	"testing"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/errs/sqlerr"
)

func TestARefusalFromOneEngineDoesNotClassifyThroughAnothersParser(t *testing.T) {
	all := corpora(t)

	for _, tc := range []struct {
		engine, name string
		foreign      []string
	}{
		{"postgres", "unique", []string{"mysql", "mariadb", "sqlite"}},
		{"postgres", "check", []string{"mysql", "mariadb", "sqlite"}},
		{"postgres", "transaction_aborted", []string{"mysql", "mariadb", "sqlite"}},

		{"sqlite", "unique", []string{"postgres", "mysql", "mariadb"}},
		{"sqlite", "foreign_key", []string{"postgres", "mysql", "mariadb"}},
		{"sqlite", "lock_timeout", []string{"postgres", "mysql", "mariadb"}},

		{"mysql", "unique", []string{"postgres", "sqlite"}},
		{"mysql", "check", []string{"postgres", "sqlite"}},
		{"mysql", "lock_timeout", []string{"postgres", "sqlite"}},
		{"mariadb", "check", []string{"postgres", "sqlite"}},
	} {
		cs := find(t, all[tc.engine], tc.name)
		if cs.Err == nil {
			t.Fatalf("%s/%s carries no captured error", tc.engine, tc.name)
		}

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

func TestASQLiteResultCodeIsReadAsBytesAndNotWhole(t *testing.T) {
	all := corpora(t)

	for _, tc := range []struct {
		name string
		want errs.Code
	}{
		{"unique", errs.CodeUnique},
		{"primary_key", errs.CodeUnique},
		{"foreign_key", errs.CodeForeignKey},
		{"not_null", errs.CodeRequired},
		{"check", errs.CodeCheck},
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

	busy := &sqlerr.Err{Type: "*sqlite.Error", Native: 517, Message: "database is locked (517) (SQLITE_BUSY_SNAPSHOT)"}
	if code, _, ok := sqlerr.Classify("sqlite", busy); !ok || code != errs.CodeLockTimeout {
		t.Errorf("busy-snapshot 517 classified as (%q, %v), want lock_timeout — its low byte is SQLITE_BUSY, so a parser comparing the whole number matches no row at all and a retryable lock reaches the caller unclassified", code, ok)
	}

	vtab := &sqlerr.Err{Type: "*sqlite.Error", Native: 19 | (9 << 8)}
	if code, _, ok := sqlerr.Classify("sqlite", vtab); ok {
		t.Errorf("a constraint subcode the corpus never produced answered %q — a number from documentation is exactly what the corpus exists to stop", code)
	}
}

func TestASQLiteCodeIsOnlyReadWhereThereIsNoSQLSTATE(t *testing.T) {
	for _, state := range []string{"23505", "23000", "HY000", "42P01"} {
		e := &sqlerr.Err{Type: "*sqlite.Error", SQLState: state, Native: 2067}
		if code, _, ok := sqlerr.Classify("sqlite", e); ok {
			t.Errorf("a number under SQLSTATE %s classified as %q; SQLite has no SQLSTATE and never will", state, code)
		}
	}

	e := &sqlerr.Err{Type: "*sqlite.Error", Native: 2067}
	if code, _, ok := sqlerr.Classify("sqlite", e); !ok || code != errs.CodeUnique {
		t.Fatalf("with no SQLSTATE the same number answered (%q, %v), so the refusals above are about something else", code, ok)
	}
}
