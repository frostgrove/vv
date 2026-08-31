package crud_test

import (
	"testing"

	"github.com/frostgrove/vv/crud"
)

func TestDialectSyntax(t *testing.T) {
	for _, tc := range []struct {
		label      string
		d          crud.Dialect
		name       string
		markers    string
		returning  bool
		lock       string
		quoted     string
		quotedEvil string
	}{
		{"postgres", crud.Postgres{}, "postgres", `$1 $2 $12`, true, " FOR UPDATE", `"user"`, `"we""ird"`},
		{"mysql", crud.MySQL{}, "mysql", `? ? ?`, false, " FOR UPDATE", "`user`", "`we``ird`"},
		{"mysql row alias", crud.MySQL{RowAlias: true}, "mysql", `? ? ?`, false, " FOR UPDATE", "`user`", "`we``ird`"},
		{"sqlite", crud.SQLite{}, "sqlite", `? ? ?`, true, "", `"user"`, `"we""ird"`},
	} {
		t.Run(tc.label, func(t *testing.T) {
			if tc.d.Name() != tc.name {
				t.Errorf("Name = %q, want %q", tc.d.Name(), tc.name)
			}
			markers := tc.d.Placeholder(1) + " " + tc.d.Placeholder(2) + " " + tc.d.Placeholder(12)
			if markers != tc.markers {
				t.Errorf("bind markers = %q, want %q", markers, tc.markers)
			}
			if tc.d.SupportsReturning() != tc.returning {
				t.Errorf("SupportsReturning = %v, want %v", tc.d.SupportsReturning(), tc.returning)
			}
			if tc.d.LockClause() != tc.lock {
				t.Errorf("LockClause = %q, want %q", tc.d.LockClause(), tc.lock)
			}
			if got := tc.d.Quote("user"); got != tc.quoted {
				t.Errorf("Quote(user) = %s, want %s", got, tc.quoted)
			}

			evil := "we" + string(tc.quoted[0]) + "ird"
			if got := tc.d.Quote(evil); got != tc.quotedEvil {
				t.Errorf("Quote(%s) = %s, want %s", evil, got, tc.quotedEvil)
			}
		})
	}
}

func TestBindLimitsAreDialectOwnedAndExternalDialectsStayPortable(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    crud.Dialect
		want int
	}{
		{"postgres", crud.Postgres{}, 65_535},
		{"mysql", crud.MySQL{}, 65_535},
		{"sqlite", crud.SQLite{}, crud.PortableBindLimit},
		{"external dialect", other{}, crud.PortableBindLimit},
		{"invalid external declaration", invalidBudgetDialect{}, crud.PortableBindLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := crud.BindLimit(tc.d); got != tc.want {
				t.Fatalf("BindLimit = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDefaultValuesClausesAreDialectOwnedAndNormalised(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    crud.Dialect
		want string
	}{
		{"postgres standard form", crud.Postgres{}, " DEFAULT VALUES"},
		{"sqlite standard form", crud.SQLite{}, " DEFAULT VALUES"},
		{"mysql empty tuple form", crud.MySQL{}, " () VALUES ()"},
		{"external dialect gets the standard form", other{}, " DEFAULT VALUES"},
		{"empty custom declaration falls back", customDefaultDialect{}, " DEFAULT VALUES"},
		{"custom declaration receives its leading space", customDefaultDialect{clause: "VALUES (DEFAULT)"}, " VALUES (DEFAULT)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := crud.DefaultValuesClause(tc.d); got != tc.want {
				t.Fatalf("DefaultValuesClause = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLiteralLikeEscapingDefaultsForAnExternalDialect(t *testing.T) {
	checkRender(t, other{}, articleMeta(t), crud.Contains("Title", "50%_\\"),
		"title LIKE ? ESCAPE '\\'", []any{"%50\\%\\_\\\\%"})
}

func TestDialectUpsert(t *testing.T) {
	cols := []string{"name", "qty"}

	for _, tc := range []struct {
		name string
		d    crud.Dialect
		cols []string
		want string
	}{
		{"postgres updates from EXCLUDED", crud.Postgres{}, cols,
			` ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name", "qty" = EXCLUDED."qty"`},
		{"postgres with nothing to overwrite does nothing", crud.Postgres{}, nil,
			` ON CONFLICT ("id") DO NOTHING`},

		{"mysql uses the VALUES() function", crud.MySQL{}, cols,
			" ON DUPLICATE KEY UPDATE `name` = VALUES(`name`), `qty` = VALUES(`qty`)"},
		{"mysql with nothing to overwrite assigns the key to itself", crud.MySQL{}, nil,
			" ON DUPLICATE KEY UPDATE `id` = `id`"},

		{"mysql row alias reads from the new row", crud.MySQL{RowAlias: true}, cols,
			" AS new ON DUPLICATE KEY UPDATE `name` = new.`name`, `qty` = new.`qty`"},
		{"mysql row alias with nothing to overwrite still declares the alias", crud.MySQL{RowAlias: true}, nil,
			" AS new ON DUPLICATE KEY UPDATE `id` = `id`"},

		{"sqlite speaks the postgres form", crud.SQLite{}, cols,
			` ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name", "qty" = EXCLUDED."qty"`},
		{"sqlite with nothing to overwrite does nothing", crud.SQLite{}, nil,
			` ON CONFLICT ("id") DO NOTHING`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.Upsert("id", tc.cols); got != tc.want {
				t.Fatalf("upsert clause = %s\nwant             = %s", got, tc.want)
			}
		})
	}
}

func TestUpsertClauseCarriesItsOwnLeadingSpace(t *testing.T) {
	for _, d := range []crud.Dialect{crud.Postgres{}, crud.MySQL{}, crud.MySQL{RowAlias: true}, crud.SQLite{}} {
		for _, cols := range [][]string{nil, {"name"}} {
			clause := d.Upsert("id", cols)
			if clause == "" || clause[0] != ' ' {
				t.Errorf("%s: upsert clause %q must start with a space", d.Name(), clause)
			}
		}
	}
}

func TestOnlyADialectThatSaysSoSwallowsThePrimaryKeyOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    crud.Dialect
		want bool
	}{
		{"postgres", crud.Postgres{}, true},
		{"sqlite", crud.SQLite{}, true},

		{"mysql", crud.MySQL{}, false},

		{"a dialect that never heard of the question", other{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			us, ok := tc.d.(crud.UpsertScope)
			got := ok && us.UpsertSwallowsPrimaryKeyOnly()
			if got != tc.want {
				t.Fatalf("primary key only = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOnlyADialectThatSaysSoRollsBackTheStatementAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    crud.Dialect
		want bool
	}{
		{"mysql", crud.MySQL{}, true},
		{"sqlite", crud.SQLite{}, true},

		{"postgres", crud.Postgres{}, false},
		{"a dialect that never heard of the question", other{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sr, ok := tc.d.(crud.StatementRollback)
			got := ok && sr.RollsBackStatementOnly()
			if got != tc.want {
				t.Fatalf("statement-scoped rollback = %v, want %v", got, tc.want)
			}
		})
	}
}

type other struct{}

func (other) Name() string                   { return "other" }
func (other) Placeholder(int) string         { return "?" }
func (other) Quote(ident string) string      { return ident }
func (other) Upsert(string, []string) string { return "" }
func (other) SupportsReturning() bool        { return false }
func (other) LockClause() string             { return "" }

type invalidBudgetDialect struct{ other }

func (invalidBudgetDialect) MaxBindValues() int { return 0 }

type customDefaultDialect struct {
	other
	clause string
}

func (this customDefaultDialect) DefaultValuesClause() string { return this.clause }
