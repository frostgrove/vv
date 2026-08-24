package crud_test

import (
	"testing"

	"github.com/shardit-io/vv/crud"
)

// The dialect is the only place SQL syntax differences are allowed to live, so
// every difference is pinned here rather than discovered against a server.
func TestDialectSyntax(t *testing.T) {
	for _, tc := range []struct {
		label      string
		d          crud.Dialect
		name       string
		markers    string // Placeholder(1), Placeholder(2), Placeholder(12)
		returning  bool
		lock       string
		quoted     string // Quote("user")
		quotedEvil string // Quote of an identifier carrying the quote character
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
			// Doubling the quote character is what keeps a column name from
			// being able to end the identifier and start SQL of its own.
			evil := "we" + string(tc.quoted[0]) + "ird"
			if got := tc.d.Quote(evil); got != tc.quotedEvil {
				t.Errorf("Quote(%s) = %s, want %s", evil, got, tc.quotedEvil)
			}
		})
	}
}

// The three upsert forms. cols are the columns to overwrite on conflict; the
// primary key is never among them.
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

// The clause is concatenated straight onto an INSERT, so it has to bring its
// own leading space with it — otherwise every upsert on every dialect is a
// syntax error.
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
