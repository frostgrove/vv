//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudpgx"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/errs"
)

type EgRow struct {
	ID     int64            `db:"id,pk,noauto"`
	Tenant int64            `db:"tenant,immutable"`
	Name   string           `db:"name"`
	Note   crud.Opt[string] `db:"note"`
	Score  crud.Opt[int]    `db:"score"`
	Flag   bool             `db:"flag"`
}

type EgRowUpdate struct {
	Name  *string
	Note  crud.Opt[string]
	Score crud.Opt[int]
	Flag  *bool
}

type EgAuto struct {
	ID   int64               `db:"id,pk,auto"`
	Name string              `db:"name"`
	Note crud.Opt[string]    `db:"note"`
	At   crud.Opt[time.Time] `db:"at"`

	Tag sql.Null[string] `db:"tag"`
}

type EgVer struct {
	ID      int64  `db:"id,pk,noauto"`
	Name    string `db:"name"`
	Version int    `db:"version,version"`
}

type EgVerUpdate struct {
	Name *string
}

type EgCons struct {
	ID     int64            `db:"id,pk,auto"`
	Slug   string           `db:"slug"`
	Parent crud.Opt[int64]  `db:"parent_id"`
	Tag    crud.Opt[string] `db:"tag"`
}

type EgConsUpdate struct {
	Slug   *string
	Parent crud.Opt[int64]
	Tag    crud.Opt[string]
}

type EgParent struct {
	ID   int64  `db:"id,pk,noauto"`
	Name string `db:"name"`

	Kids  []EgKid  `rel:"has_many,fk=ParentID"`
	Marks []EgMark `rel:"many_to_many,join=eg_parent_marks,joinFK=parent_id,joinRef=mark_id"`
}

type EgKid struct {
	ID       int64           `db:"id,pk,noauto"`
	ParentID crud.Opt[int64] `db:"parent_id"`
	Name     string          `db:"name"`

	Parent *EgParent `rel:"belongs_to,fk=ParentID"`
}

type EgMark struct {
	ID   int64  `db:"id,pk,noauto"`
	Slug string `db:"slug"`
}

var (
	EgRows    = sqlrepo.Define[EgRow, int64, EgRowUpdate]("eg_rows")
	EgAutos   = sqlrepo.Define[EgAuto, int64, struct{}]("eg_auto")
	EgConses  = sqlrepo.Define[EgCons, int64, EgConsUpdate]("eg_cons")
	EgVers    = sqlrepo.Define[EgVer, int64, EgVerUpdate]("eg_ver")
	EgParents = sqlrepo.Define[EgParent, int64, struct{}]("eg_parents")
	EgKids    = sqlrepo.Define[EgKid, int64, struct{}]("eg_kids")
	EgMarks   = sqlrepo.Define[EgMark, int64, struct{}]("eg_marks")

	EgShallowParents = sqlrepo.Define[EgParent, int64, struct{}]("eg_parents", sqlrepo.PreloadDepth(2))
)

var egSchema = map[string][]string{
	"postgres": {
		`DROP TABLE IF EXISTS eg_parent_marks`,
		`DROP TABLE IF EXISTS eg_kids`,
		`DROP TABLE IF EXISTS eg_marks`,
		`DROP TABLE IF EXISTS eg_parents`,
		`DROP TABLE IF EXISTS eg_cons`,
		`DROP TABLE IF EXISTS eg_odd`,
		`DROP TABLE IF EXISTS eg_ver`,
		`DROP TABLE IF EXISTS eg_auto`,
		`DROP TABLE IF EXISTS eg_rows`,
		`CREATE TABLE eg_rows (
			id     BIGINT  NOT NULL PRIMARY KEY,
			tenant BIGINT  NOT NULL,
			name   TEXT    NOT NULL,
			note   TEXT        NULL,
			score  INTEGER     NULL,
			flag   BOOLEAN NOT NULL DEFAULT FALSE
		)`,
		`CREATE TABLE eg_ver (
			id      BIGINT  NOT NULL PRIMARY KEY,
			name    TEXT    NOT NULL,
			version INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE eg_auto (
			id   BIGSERIAL   PRIMARY KEY,
			name TEXT        NOT NULL,
			note TEXT            NULL,
			at   TIMESTAMPTZ     NULL,
			tag  TEXT            NULL
		)`,
		`CREATE TABLE eg_cons (
			id        BIGSERIAL   PRIMARY KEY,
			slug      VARCHAR(64) NOT NULL UNIQUE,
			parent_id BIGINT          NULL REFERENCES eg_cons (id),
			tag       VARCHAR(64) NOT NULL
		)`,

		`CREATE TABLE eg_odd (
			id          BIGINT      NOT NULL PRIMARY KEY,
			"select"    VARCHAR(64) NOT NULL,
			"full name" VARCHAR(64) NOT NULL,
			flag        BOOLEAN     NOT NULL
		)`,
		`CREATE TABLE eg_parents (
			id   BIGINT NOT NULL PRIMARY KEY,
			name TEXT   NOT NULL
		)`,
		`CREATE TABLE eg_marks (
			id   BIGINT      NOT NULL PRIMARY KEY,
			slug VARCHAR(64) NOT NULL
		)`,
		`CREATE TABLE eg_kids (
			id        BIGINT NOT NULL PRIMARY KEY,
			parent_id BIGINT     NULL,
			name      TEXT   NOT NULL
		)`,
		`CREATE TABLE eg_parent_marks (
			parent_id BIGINT NOT NULL,
			mark_id   BIGINT NOT NULL,
			PRIMARY KEY (parent_id, mark_id)
		)`,
	},
	"mysql": {
		`DROP TABLE IF EXISTS eg_parent_marks`,
		`DROP TABLE IF EXISTS eg_kids`,
		`DROP TABLE IF EXISTS eg_marks`,
		`DROP TABLE IF EXISTS eg_parents`,
		`DROP TABLE IF EXISTS eg_cons`,
		`DROP TABLE IF EXISTS eg_odd`,
		`DROP TABLE IF EXISTS eg_ver`,
		`DROP TABLE IF EXISTS eg_auto`,
		`DROP TABLE IF EXISTS eg_rows`,
		`CREATE TABLE eg_rows (
			id     BIGINT  NOT NULL PRIMARY KEY,
			tenant BIGINT  NOT NULL,
			name   TEXT    NOT NULL,
			note   TEXT        NULL,
			score  INT         NULL,
			flag   BOOLEAN NOT NULL DEFAULT FALSE
		) ENGINE=InnoDB`,
		`CREATE TABLE eg_ver (
			id      BIGINT NOT NULL PRIMARY KEY,
			name    TEXT   NOT NULL,
			version INT    NOT NULL DEFAULT 0
		) ENGINE=InnoDB`,
		`CREATE TABLE eg_auto (
			id   BIGINT      NOT NULL AUTO_INCREMENT PRIMARY KEY,
			name TEXT        NOT NULL,
			note TEXT            NULL,
			at   DATETIME(6)     NULL,
			tag  VARCHAR(64)     NULL
		) ENGINE=InnoDB`,
		`CREATE TABLE eg_cons (
			id        BIGINT      NOT NULL AUTO_INCREMENT PRIMARY KEY,
			slug      VARCHAR(64) NOT NULL UNIQUE,
			parent_id BIGINT          NULL,
			tag       VARCHAR(64) NOT NULL,
			FOREIGN KEY (parent_id) REFERENCES eg_cons (id)
		) ENGINE=InnoDB`,
		"CREATE TABLE eg_odd (\n" +
			"  id          BIGINT      NOT NULL PRIMARY KEY,\n" +
			"  `select`    VARCHAR(64) NOT NULL,\n" +
			"  `full name` VARCHAR(64) NOT NULL,\n" +
			"  flag        BOOLEAN     NOT NULL\n" +
			") ENGINE=InnoDB",
		`CREATE TABLE eg_parents (
			id   BIGINT NOT NULL PRIMARY KEY,
			name TEXT   NOT NULL
		) ENGINE=InnoDB`,
		`CREATE TABLE eg_marks (
			id   BIGINT      NOT NULL PRIMARY KEY,
			slug VARCHAR(64) NOT NULL
		) ENGINE=InnoDB`,
		`CREATE TABLE eg_kids (
			id        BIGINT NOT NULL PRIMARY KEY,
			parent_id BIGINT     NULL,
			name      TEXT   NOT NULL
		) ENGINE=InnoDB`,
		`CREATE TABLE eg_parent_marks (
			parent_id BIGINT NOT NULL,
			mark_id   BIGINT NOT NULL,
			PRIMARY KEY (parent_id, mark_id)
		) ENGINE=InnoDB`,
	},
}

var egTables = []string{
	"eg_parent_marks", "eg_kids", "eg_marks", "eg_parents",
	"eg_cons", "eg_odd", "eg_ver", "eg_auto", "eg_rows",
}

var (
	egOnce sync.Once
	egErr  error
)

func egSetup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	egOnce.Do(func() {
		for _, tg := range egEngines() {
			for _, stmt := range egSchema[tg.database] {
				if _, err := tg.source.Exec(ctx, stmt); err != nil {
					egErr = fmt.Errorf("%s: %s: %w", tg.database, egFirstLine(stmt), err)
					return
				}
			}
		}
	})
	if egErr != nil {
		t.Fatalf("the eg tables were never built: %v", egErr)
	}
	for _, tg := range egEngines() {
		egWipe(t, tg.source)
	}
}

func egWipe(t *testing.T, source crud.Source) {
	t.Helper()
	ctx := context.Background()
	for _, table := range egTables {
		if _, err := source.Exec(ctx, "DELETE FROM "+source.Dialect().Quote(table)); err != nil {
			t.Fatalf("%s: wiping %s: %v", source.Dialect().Name(), table, err)
		}
	}
}

func egFirstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

type egTarget struct {
	name     string
	database string
	source   crud.Source

	classifies bool
}

func egTargets() []egTarget {
	return []egTarget{
		{"database/sql+postgres", "postgres", crudsql.Postgres(pgDB), true},
		{"database/sql+mysql", "mysql", crudsql.MySQL(myDB), true},
		{"pgx", "postgres", crudpgx.Open(pgPool), true},
		{"ent+postgres", "postgres", newEntSource(entClient(pgDB, dialect.Postgres), crud.Postgres{}), false},
		{"ent+mysql", "mysql", newEntSource(entClient(myDB, dialect.MySQL), crud.MySQL{}), false},
	}
}

func egEngines() []egTarget {
	return []egTarget{
		{"postgres", "postgres", crudsql.Postgres(pgDB), true},
		{"mysql", "mysql", crudsql.MySQL(myDB), true},

		{"mariadb", "mysql", crudsql.MariaDB(mariaDB), true},
	}
}

func egBeginners() []egTarget {
	return []egTarget{
		{"database/sql+postgres", "postgres", crudsql.Postgres(pgDB), true},
		{"database/sql+mysql", "mysql", crudsql.MySQL(myDB), true},
		{"pgx", "postgres", crudpgx.Open(pgPool), true},
	}
}

type egSpy struct {
	ex crud.Executor
	d  crud.Dialect

	mu     sync.Mutex
	seen   []string
	before map[string]func()
	fired  map[string]bool
}

func egWatch(source crud.Source) *egSpy {
	return &egSpy{ex: source, d: source.Dialect(), before: map[string]func(){}, fired: map[string]bool{}}
}

func (this *egSpy) Dialect() crud.Dialect { return this.d }

func (this *egSpy) UnwrapSource() crud.Source {
	source, _ := this.ex.(crud.Source)
	return source
}

func (this *egSpy) record(q string) {
	this.mu.Lock()
	this.seen = append(this.seen, q)
	var hook func()
	for key, fn := range this.before {
		if !this.fired[key] && strings.Contains(q, key) {
			this.fired[key] = true
			hook = fn
			break
		}
	}
	this.mu.Unlock()
	if hook != nil {
		hook()
	}
}

func (this *egSpy) Exec(ctx context.Context, q string, args ...any) (crud.Result, error) {
	this.record(q)
	return this.ex.Exec(ctx, q, args...)
}

func (this *egSpy) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	this.record(q)
	return this.ex.Query(ctx, q, args...)
}

func (this *egSpy) beforeFirst(key string, fn func()) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.before[key] = fn
}

func (this *egSpy) statements() []string {
	this.mu.Lock()
	defer this.mu.Unlock()
	return append([]string(nil), this.seen...)
}

func (this *egSpy) matching(sub string) []string {
	var out []string
	for _, q := range this.statements() {
		if strings.Contains(q, sub) {
			out = append(out, q)
		}
	}
	return out
}

func (this *egSpy) forget() {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.seen = nil
}

var _ crud.Source = (*egSpy)(nil)

func egSeed(t *testing.T, repository *crud.Repo[EgRow, int64, EgRowUpdate], rows ...EgRow) {
	t.Helper()
	for _, r := range rows {
		if stored, err := repository.Save(context.Background(), &r); err != nil {
			t.Fatalf("seeding %d: %v", r.ID, err)
		} else {
			r = stored
		}
	}
}

func egNames(rows []EgRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

func TestMySQLLiteralLikeHelpersSurviveNoBackslashEscapes(t *testing.T) {
	ctx := context.Background()
	for _, target := range []struct {
		name     string
		database *sql.DB
	}{
		{"mysql", myDB},
		{"mariadb", mariaDB},
	} {
		t.Run(target.name, func(t *testing.T) {
			conn, err := target.database.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })

			var originalMode string
			if err := conn.QueryRowContext(ctx, "SELECT @@SESSION.sql_mode").Scan(&originalMode); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := conn.ExecContext(ctx, "SET SESSION sql_mode = ?", originalMode); err != nil {
					t.Errorf("restoring sql_mode: %v", err)
				}
			})
			if _, err := conn.ExecContext(ctx,
				"SET SESSION sql_mode = CONCAT(@@SESSION.sql_mode, ',NO_BACKSLASH_ESCAPES')"); err != nil {
				t.Fatal(err)
			}

			source := crudsql.Source(conn, crud.MySQL{})
			if _, err := source.Exec(ctx, "DELETE FROM `users`"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := source.Exec(ctx, "DELETE FROM `users`"); err != nil {
					t.Errorf("cleaning users: %v", err)
				}
			})
			repository := Users.Bind(source)
			for _, user := range []User{
				{TenantID: 1, Email: "match@x.io", Name: "100%_raw"},
				{TenantID: 1, Email: "other@x.io", Name: "1005xraw"},
				{TenantID: 1, Email: "slash@x.io", Name: `path\file`},
			} {
				if err := repository.SaveOnly(ctx, &user); err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct {
				pred crud.Predicate
				want string
			}{
				{crud.Contains("Name", "%_"), "100%_raw"},
				{crud.StartsWith("Name", "100%"), "100%_raw"},
				{crud.EndsWith("Name", "_raw"), "100%_raw"},
				{crud.Contains("Name", `path\file`), `path\file`},
				{crud.ContainsIgnoreCase("Name", "%_RAW"), "100%_raw"},
				{crud.StartsWithIgnoreCase("Name", "100%"), "100%_raw"},
				{crud.EndsWithIgnoreCase("Name", "_RAW"), "100%_raw"},
			} {
				got, err := repository.GetAll(ctx, crud.Where(tc.pred))
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 1 || got[0].Name != tc.want {
					t.Fatalf("%T matched %#v, want only %q", tc.pred, got, tc.want)
				}
			}
		})
	}
}

func egPager[T any](p crud.PaginatedResponse[T]) string {
	return fmt.Sprintf("items=%d page=%d limit=%d total=%d pages=%d next=%v prev=%v",
		len(p.Items), p.Page, p.Limit, p.Total, p.TotalPages, p.HasNext, p.HasPrev)
}

func TestDegenerateInputsAnswerEmptyOnEveryProvider(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egTargets() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			s := egWatch(tg.source)
			rows := EgRows.Bind(s)

			t.Run("an empty table", func(t *testing.T) {
				all, err := rows.GetAll(ctx)
				if err != nil || len(all) != 0 {
					t.Fatalf("GetAll on an empty table = %d rows, err = %v", len(all), err)
				}
				if n, err := rows.Count(ctx); err != nil || n != 0 {
					t.Fatalf("Count = %d, err = %v", n, err)
				}
				if ok, err := rows.Exists(ctx); err != nil || ok {
					t.Fatalf("Exists on an empty table = %v, err = %v", ok, err)
				}
				if n, err := rows.DeleteAll(ctx); err != nil || n != 0 {
					t.Fatalf("DeleteAll on an empty table = %d, err = %v", n, err)
				}
				page, err := rows.Get(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) != 0 || page.Total != 0 || page.TotalPages != 0 || page.HasNext || page.HasPrev {
					t.Fatalf("the first page of nothing = %s", egPager(page))
				}
				if page.Items == nil {
					t.Fatal("an empty page must carry an empty slice, not nil: it is serialised straight to JSON")
				}
			})

			t.Run("Delete with no ids does not reach the database", func(t *testing.T) {
				s.forget()
				n, err := rows.Delete(ctx)
				if err != nil || n != 0 {
					t.Fatalf("Delete() = %d, err = %v", n, err)
				}
				if sent := s.statements(); len(sent) != 0 {
					t.Fatalf("Delete() with no ids still sent %v", sent)
				}
			})

			egSeed(t, rows,
				EgRow{ID: 1, Tenant: 1, Name: "one", Score: crud.Set(1)},
				EgRow{ID: 2, Tenant: 1, Name: "two", Score: crud.Set(2)},
				EgRow{ID: 3, Tenant: 1, Name: "three", Score: crud.Set(3)},
			)

			t.Run("a page past the end", func(t *testing.T) {
				page, err := rows.Get(ctx, crud.Page(999), crud.Limit(10))
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) != 0 {
					t.Fatalf("page 999 of three rows returned %d items", len(page.Items))
				}

				if page.Total != 3 || page.TotalPages != 1 || !page.HasPrev || page.HasNext {
					t.Fatalf("page 999 = %s, want an empty page that still knows about 3 rows on 1 page", egPager(page))
				}
			})

			t.Run("Limit(0) means the repository default", func(t *testing.T) {
				page, err := rows.Get(ctx, crud.Limit(0))
				if err != nil {
					t.Fatal(err)
				}
				if page.Limit != sqlrepo.DefaultPageSize || len(page.Items) != 3 {
					t.Fatalf("Limit(0) = %s, want limit %d and all three rows", egPager(page), sqlrepo.DefaultPageSize)
				}
			})

			t.Run("an empty set", func(t *testing.T) {
				n, err := rows.Count(ctx, crud.Where(crud.InAny("Name", []string{})))
				if err != nil || n != 0 {
					t.Fatalf("IN () matched %d rows, err = %v: an empty set contains nothing", n, err)
				}
				n, err = rows.Count(ctx, crud.Where(crud.NotInAny("Name", []string{})))
				if err != nil || n != 3 {
					t.Fatalf("NOT IN () matched %d rows, err = %v: everything is outside an empty set", n, err)
				}
			})

			t.Run("a filter that matches nothing", func(t *testing.T) {
				page, err := rows.Get(ctx, crud.Where(crud.Eq("Name", "nobody")))
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) != 0 || page.Total != 0 || page.TotalPages != 0 {
					t.Fatalf("page = %s", egPager(page))
				}
				if n, err := rows.DeleteAll(ctx, crud.Where(crud.Eq("Name", "nobody"))); err != nil || n != 0 {
					t.Fatalf("DeleteAll matched %d rows, err = %v", n, err)
				}
				if n, err := rows.Delete(ctx, 404, 405); err != nil || n != 0 {
					t.Fatalf("deleting ids that were never there = %d, err = %v", n, err)
				}
				if n, err := rows.Count(ctx); err != nil || n != 3 {
					t.Fatalf("count = %d, err = %v: a no-op delete took rows with it", n, err)
				}
			})

			t.Run("an update with nothing in it writes nothing", func(t *testing.T) {
				s.forget()
				got, err := rows.Update(ctx, 1, EgRowUpdate{})
				if err != nil {
					t.Fatal(err)
				}
				if got.Name != "one" || got.Score.String() != "1" {
					t.Fatalf("an empty DTO changed the row: %+v", got)
				}
				if stmts := s.matching("UPDATE"); len(stmts) != 0 {
					t.Fatalf("an empty DTO still issued %v", stmts)
				}
			})

			t.Run("a model that is entirely zero", func(t *testing.T) {
				if _, err := rows.Save(ctx, &EgRow{}); !errors.Is(err, crud.ErrMissingID) {
					t.Fatalf("err = %v, want ErrMissingID", err)
				}

				autos := EgAutos.Bind(tg.source)
				var blank EgAuto
				if stored, err := autos.Save(ctx, &blank); err != nil {
					t.Fatal(err)
				} else {
					blank = stored
				}
				if blank.ID == 0 {
					t.Fatal("the generated key was not read back for an otherwise empty model")
				}
				back, err := autos.GetByID(ctx, blank.ID)
				if err != nil {
					t.Fatal(err)
				}
				if back.Name != "" || !back.Note.IsNull() || !back.At.IsNull() {
					t.Fatalf("an all-zero model came back as %+v, want an empty name and two NULLs", back)
				}
				if back.Tag.Valid {
					t.Fatalf("tag = %+v, want an invalid sql.Null for a column nobody wrote", back.Tag)
				}

				valued := EgAuto{Name: "tagged", Tag: sql.Null[string]{V: "beta", Valid: true}}
				if stored, err := autos.Save(ctx, &valued); err != nil {
					t.Fatal(err)
				} else {
					valued = stored
				}
				got, err := autos.GetByID(ctx, valued.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !got.Tag.Valid || got.Tag.V != "beta" {
					t.Fatalf("tag = %+v, want beta", got.Tag)
				}

				n, err := autos.Count(ctx, crud.Where(crud.Eq("Tag", "beta")))
				if err != nil {
					t.Fatal(err)
				}
				if n != 1 {
					t.Fatalf("count = %d, want the one tagged row", n)
				}
			})
		})
	}
}

func TestBoundaryValuesRoundTripOnEveryProvider(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	const awkward = `O'Brien \ said "hi" -- ; é🚀`
	long := strings.Repeat("saturn", 2000)

	for _, tg := range egTargets() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			rows := EgRows.Bind(tg.source)

			t.Run("the largest key an int64 can hold", func(t *testing.T) {
				big := EgRow{ID: math.MaxInt64, Tenant: 1, Name: "big"}
				if _, err := rows.Save(ctx, &big); err != nil {
					t.Fatal(err)
				}
				got, err := rows.GetByID(ctx, math.MaxInt64)
				if err != nil {
					t.Fatalf("reading back id %d: %v", int64(math.MaxInt64), err)
				}
				if got.ID != math.MaxInt64 {
					t.Fatalf("id came back as %d: the key lost precision somewhere between Go and the column", got.ID)
				}
				n, err := rows.Count(ctx, crud.Where(crud.InAny("ID", []int64{math.MaxInt64})))
				if err != nil || n != 1 {
					t.Fatalf("an IN list holding the largest int64 matched %d rows, err = %v", n, err)
				}
				if n, err := rows.Delete(ctx, math.MaxInt64); err != nil || n != 1 {
					t.Fatalf("deleting it = %d, err = %v", n, err)
				}
			})

			t.Run("an empty string is not NULL", func(t *testing.T) {
				egSeed(t, rows,
					EgRow{ID: 1, Tenant: 1, Name: "empty", Note: crud.Set("")},
					EgRow{ID: 2, Tenant: 1, Name: "null", Note: crud.Null[string]()},
				)
				empty, err := rows.GetByID(ctx, 1)
				if err != nil {
					t.Fatal(err)
				}
				if note, ok := empty.Note.Get(); !ok || note != "" {
					t.Fatalf(`note = %s, want the empty string as a value`, empty.Note)
				}
				null, err := rows.GetByID(ctx, 2)
				if err != nil {
					t.Fatal(err)
				}
				if !null.Note.IsNull() {
					t.Fatalf("note = %s, want NULL", null.Note)
				}
				n, err := rows.Count(ctx, crud.Where(crud.IsNull("Note")))
				if err != nil || n != 1 {
					t.Fatalf("IS NULL matched %d rows, err = %v: an empty string was mistaken for NULL", n, err)
				}
				n, err = rows.Count(ctx, crud.Where(crud.Eq("Note", "")))
				if err != nil || n != 1 {
					t.Fatalf(`= '' matched %d rows, err = %v`, n, err)
				}
			})

			t.Run("LIKE metacharacters in a value are literal", func(t *testing.T) {
				egWipe(t, tg.source)
				egSeed(t, rows,
					EgRow{ID: 1, Tenant: 1, Name: "100%_raw"},
					EgRow{ID: 2, Tenant: 1, Name: "1005xraw"},
					EgRow{ID: 3, Tenant: 1, Name: "plain"},
				)

				for _, tc := range []struct {
					name string
					pred crud.Predicate
				}{
					{"Contains", crud.Contains("Name", "%_")},
					{"StartsWith", crud.StartsWith("Name", "100%")},
					{"EndsWith", crud.EndsWith("Name", "_raw")},
					{"a lone underscore", crud.Contains("Name", "_")},
				} {
					got, err := rows.GetAll(ctx, crud.Where(tc.pred), crud.OrderBy(crud.Asc("ID")))
					if err != nil {
						t.Fatal(err)
					}
					if want := []string{"100%_raw"}; !eq(egNames(got), want) {
						t.Fatalf("%s matched %v, want only %v: a wildcard escaped into the pattern",
							tc.name, egNames(got), want)
					}
				}
			})

			t.Run("quotes, backslashes, an emoji and a long string", func(t *testing.T) {
				egWipe(t, tg.source)
				egSeed(t, rows, EgRow{ID: 1, Tenant: 1, Name: awkward, Note: crud.Set(long)})

				got, err := rows.GetByID(ctx, 1)
				if err != nil {
					t.Fatal(err)
				}
				if got.Name != awkward {
					t.Fatalf("name came back as %q, want %q", got.Name, awkward)
				}
				note, _ := got.Note.Get()
				if len(note) != len(long) || note != long {
					t.Fatalf("a %d-byte string came back %d bytes long", len(long), len(note))
				}

				n, err := rows.Count(ctx, crud.Where(crud.Eq("Name", awkward)))
				if err != nil || n != 1 {
					t.Fatalf("looking the row up by its own name matched %d rows, err = %v", n, err)
				}
				n, err = rows.Count(ctx, crud.Where(crud.Contains("Name", `\ said "hi"`)))
				if err != nil || n != 1 {
					t.Fatalf("a LIKE over the backslash matched %d rows, err = %v", n, err)
				}
			})

			t.Run("the zero time", func(t *testing.T) {
				autos := EgAutos.Bind(tg.source)
				z := EgAuto{Name: "zero", At: crud.Set(time.Time{})}
				if stored, err := autos.Save(ctx, &z); err != nil {
					t.Fatalf("storing the zero time: %v", err)
				} else {
					z = stored
				}
				back, err := autos.GetByID(ctx, z.ID)
				if err != nil {
					t.Fatal(err)
				}
				at, ok := back.At.Get()
				if !ok {
					t.Fatalf("at = %s, want the instant that went in", back.At)
				}

				if !at.Equal(time.Time{}) {
					t.Fatalf("the zero time came back as %s", at.UTC())
				}
			})
		})
	}
}

func TestForUpdateMakesTwoTransactionsTakeTurns(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egBeginners() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			rows := EgRows.Bind(tg.source)
			egSeed(t, rows, EgRow{ID: 1, Tenant: 1, Name: "contended", Score: crud.Set(0)})

			first, err := tg.source.(crud.Beginner).Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Rollback(ctx)
			firstCtx := crud.BindExecutor(ctx, tg.source, first)
			if _, err := rows.GetByID(firstCtx, 1, crud.ForUpdate()); err != nil {
				t.Fatal(err)
			}

			read := make(chan int, 1)
			done := make(chan error, 1)

			started := make(chan struct{})
			go func() {
				second, err := tg.source.(crud.Beginner).Begin(ctx)
				if err != nil {
					close(started)
					done <- err
					return
				}
				secondCtx := crud.BindExecutor(ctx, tg.source, second)
				close(started)
				got, err := rows.GetByID(secondCtx, 1, crud.ForUpdate())
				if err != nil {
					_ = second.Rollback(ctx)
					done <- err
					return
				}
				score, _ := got.Score.Get()
				read <- score
				if _, err := rows.Update(secondCtx, 1, EgRowUpdate{Score: crud.Set(score + 10)}); err != nil {
					_ = second.Rollback(ctx)
					done <- err
					return
				}
				done <- second.Commit(ctx)
			}()

			<-started
			select {
			case score := <-read:
				t.Fatalf("the second transaction read score = %d while the first still held the row locked", score)
			case err := <-done:
				t.Fatalf("the second transaction: %v", err)
			case <-time.After(300 * time.Millisecond):
			}

			if _, err := rows.Update(firstCtx, 1, EgRowUpdate{Score: crud.Set(5)}); err != nil {
				t.Fatal(err)
			}
			if err := first.Commit(ctx); err != nil {
				t.Fatal(err)
			}

			select {
			case score := <-read:
				if score != 5 {
					t.Fatalf("the second transaction read score = %d, want the 5 the first one committed", score)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("the second transaction never got the lock")
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}

			got, err := rows.GetByID(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if score, _ := got.Score.Get(); score != 15 {
				t.Fatalf("score = %d, want 15: one of the two updates was lost", score)
			}
		})
	}
}

func TestATransactionThatFailsHalfwayLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			cons := EgConses.Bind(tg.source)

			var ids []int64
			for _, slug := range []string{"a", "b", "c"} {
				row := EgCons{Slug: slug, Tag: crud.Set("t")}
				if stored, err := cons.Save(ctx, &row); err != nil {
					t.Fatal(err)
				} else {
					row = stored
				}
				ids = append(ids, row.ID)
			}

			var applied int
			err := cons.Tx(ctx, func(ctx context.Context) error {
				applied = 0
				for i, id := range ids {
					slug := fmt.Sprintf("renamed-%d", i)
					if i == len(ids)-1 {
						slug = "renamed-0"
					}
					if _, err := cons.Update(ctx, id, EgConsUpdate{Slug: &slug}); err != nil {
						return err
					}
					applied++
				}
				return nil
			})
			if err == nil {
				t.Fatal("renaming two rows onto one unique slug was accepted")
			}
			if applied != len(ids)-1 {
				t.Fatalf("%d renames went through before the collision, want %d: "+
					"there was nothing half-written for the rollback to undo", applied, len(ids)-1)
			}

			got, err := cons.GetAll(ctx, crud.OrderBy(crud.Asc("ID")))
			if err != nil {
				t.Fatal(err)
			}
			var slugs []string
			for _, g := range got {
				slugs = append(slugs, g.Slug)
			}
			if want := []string{"a", "b", "c"}; !eq(slugs, want) {
				t.Fatalf("slugs = %v, want %v: the rows updated before the failure were not rolled back", slugs, want)
			}
		})
	}
}

func TestAFinishedTransactionInTheContextIsNotIgnored(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egBeginners() {
		t.Run(tg.name, func(t *testing.T) {
			for _, tc := range []struct {
				name  string
				close func(crud.Tx) error
			}{
				{"committed", func(tx crud.Tx) error { return tx.Commit(ctx) }},
				{"rolled back", func(tx crud.Tx) error { return tx.Rollback(ctx) }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					egWipe(t, tg.source)
					rows := EgRows.Bind(tg.source)

					tx, err := tg.source.(crud.Beginner).Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					txCtx := crud.BindExecutor(ctx, tg.source, tx)
					inside := EgRow{ID: 1, Tenant: 1, Name: "inside"}
					if _, err := rows.Save(txCtx, &inside); err != nil {
						t.Fatal(err)
					}
					if err := tc.close(tx); err != nil {
						t.Fatal(err)
					}

					after := EgRow{ID: 2, Tenant: 1, Name: "after"}
					if _, err := rows.Save(txCtx, &after); err == nil {
						t.Fatal("a write on a finished transaction was accepted")
					}
					if _, err := rows.GetByID(txCtx, 1); err == nil {
						t.Fatal("a read on a finished transaction was answered")
					}

					n, err := rows.Count(ctx, crud.Where(crud.Eq("Name", "after")))
					if err != nil {
						t.Fatal(err)
					}
					if n != 0 {
						t.Fatal("the write fell back onto the pool and landed outside the transaction it named")
					}
				})
			}
		})
	}
}

func TestSavepointsRollBackAndReleaseIndependently(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egBeginners() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			rows := EgRows.Bind(tg.source)

			err := crud.InTx(ctx, tg.source, func(ctx context.Context) error {
				outer := EgRow{ID: 1, Tenant: 1, Name: "outer"}
				if _, err := rows.Save(ctx, &outer); err != nil {
					return err
				}
				ex, _ := crud.ExecutorFrom(ctx)

				undone, err := ex.(crud.Beginner).Begin(ctx)
				if err != nil {
					return err
				}
				doomed := EgRow{ID: 2, Tenant: 1, Name: "doomed"}
				if _, err := rows.Save(crud.BindExecutor(ctx, tg.source, undone), &doomed); err != nil {
					return err
				}
				if err := undone.Rollback(ctx); err != nil {
					return err
				}

				kept, err := ex.(crud.Beginner).Begin(ctx)
				if err != nil {
					return err
				}
				saved := EgRow{ID: 3, Tenant: 1, Name: "released"}
				if _, err := rows.Save(crud.BindExecutor(ctx, tg.source, kept), &saved); err != nil {
					return err
				}
				return kept.Commit(ctx)
			})
			if err != nil {
				t.Fatal(err)
			}

			got, err := rows.GetAll(ctx, crud.OrderBy(crud.Asc("ID")))
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"outer", "released"}; !eq(egNames(got), want) {
				t.Fatalf("rows = %v, want %v: a savepoint took the wrong half with it", egNames(got), want)
			}
		})
	}
}

func TestASavepointInsideASavepointUnwindsOneLevelAtATime(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egBeginners() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			rows := EgRows.Bind(tg.source)

			err := crud.InTx(ctx, tg.source, func(ctx context.Context) error {
				if _, err := rows.Save(ctx, &EgRow{ID: 1, Tenant: 1, Name: "outer"}); err != nil {
					return err
				}
				ex, _ := crud.ExecutorFrom(ctx)

				middle, err := ex.(crud.Beginner).Begin(ctx)
				if err != nil {
					return err
				}
				midCtx := crud.BindExecutor(ctx, tg.source, middle)
				if _, err := rows.Save(midCtx, &EgRow{ID: 2, Tenant: 1, Name: "middle"}); err != nil {
					return err
				}

				inner, err := middle.(crud.Beginner).Begin(midCtx)
				if err != nil {
					return err
				}
				if _, err := rows.Save(crud.BindExecutor(midCtx, tg.source, inner), &EgRow{ID: 3, Tenant: 1, Name: "inner"}); err != nil {
					return err
				}

				if err := inner.Rollback(midCtx); err != nil {
					return err
				}

				if _, err := rows.Save(midCtx, &EgRow{ID: 4, Tenant: 1, Name: "after the inner rollback"}); err != nil {
					return err
				}
				return middle.Commit(ctx)
			})
			if err != nil {
				t.Fatal(err)
			}

			got, err := rows.GetAll(ctx, crud.OrderBy(crud.Asc("ID")))
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"outer", "middle", "after the inner rollback"}
			if !eq(egNames(got), want) {
				t.Fatalf("rows = %v, want %v: the inner rollback took a level it did not own", egNames(got), want)
			}
		})
	}
}

func TestANestedCommitOnAPoisonedTransactionCarriesItsCode(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egBeginners() {
		if tg.database != "postgres" {
			continue
		}
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)

			tx, err := tg.source.(crud.Beginner).Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)

			healthy, err := tx.(crud.Beginner).Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := healthy.Commit(ctx); err != nil {
				t.Fatalf("a nested commit over a healthy transaction was refused: %v", err)
			}

			inner, err := tx.(crud.Beginner).Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := inner.Exec(ctx, "SELECT 1/0"); err == nil {
				t.Fatal("the division was accepted, so nothing poisoned the transaction")
			}
			err = inner.Commit(ctx)
			if err == nil {
				t.Fatal("the nested commit succeeded on a transaction the server had already aborted")
			}
			f, ok := errs.AsFault(err)
			if !ok {
				t.Fatalf("the nested commit came back unclassified, so 25P02 reaches a caller as an anonymous 500: %T: %v", err, err)
			}
			if f.Code != errs.CodeTransactionAborted || f.Kind != errs.KindRetryable {
				t.Fatalf("the nested commit says %v/%v, want %v/%v", f.Kind, f.Code, errs.KindRetryable, errs.CodeTransactionAborted)
			}
		})
	}
}

func TestWithTxOptionsReachesTheDriver(t *testing.T) {
	ctx := context.Background()
	egSetup(t)
	source := crudsql.Postgres(pgDB).WithTxOptions(&sql.TxOptions{Isolation: sql.LevelSerializable})
	egWipe(t, source)

	rows := EgRows.Bind(source)
	egSeed(t, rows, EgRow{ID: 1, Tenant: 1, Name: "a", Score: crud.Set(1)})

	first, err := source.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx)
	second, err := source.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback(ctx)

	for _, tx := range []crud.Tx{first, second} {
		if _, err := rows.GetAll(crud.BindExecutor(ctx, source, tx), crud.Where(crud.Eq("Tenant", int64(1)))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rows.UpdateAll(crud.BindExecutor(ctx, source, first),
		EgRowUpdate{Name: ptr("first")}, crud.Where(crud.Eq("ID", int64(1)))); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	_, err = rows.UpdateAll(crud.BindExecutor(ctx, source, second),
		EgRowUpdate{Name: ptr("second")}, crud.Where(crud.Eq("ID", int64(1))))
	if err == nil {
		err = second.Commit(ctx)
	}
	if err == nil {
		t.Fatal("both serializable transactions committed a write to the same row: the isolation level never reached the driver")
	}
	t.Logf("the second transaction was refused with %v", err)

	after, err := rows.GetByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "first" {
		t.Fatalf("name = %q, want the committed transaction's value", after.Name)
	}
}

func TestPreloadEdgesAgainstDatabases(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			t.Run("a parent with no children", func(t *testing.T) {
				egWipe(t, tg.source)
				parents := EgParents.Bind(tg.source)
				for _, p := range []EgParent{{ID: 1, Name: "lonely"}, {ID: 2, Name: "also lonely"}} {
					if stored, err := parents.Save(ctx, &p); err != nil {
						t.Fatal(err)
					} else {
						p = stored
					}
				}

				got, err := parents.GetAll(ctx, crud.Preload("Kids", "Marks"), crud.OrderBy(crud.Asc("ID")))
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 2 {
					t.Fatalf("%d parents", len(got))
				}
				for _, p := range got {
					if p.Kids == nil || len(p.Kids) != 0 {
						t.Fatalf("parent %d has no children; the preload left %#v", p.ID, p.Kids)
					}
					if p.Marks == nil || len(p.Marks) != 0 {
						t.Fatalf("parent %d has no marks; the preload left %#v", p.ID, p.Marks)
					}
				}
			})

			t.Run("a child whose foreign key is NULL", func(t *testing.T) {
				egWipe(t, tg.source)
				parents, kids := EgParents.Bind(tg.source), EgKids.Bind(tg.source)
				p := EgParent{ID: 1, Name: "p"}
				if _, err := parents.Save(ctx, &p); err != nil {
					t.Fatal(err)
				}
				for _, k := range []EgKid{
					{ID: 1, ParentID: crud.Set(int64(1)), Name: "attached"},
					{ID: 2, ParentID: crud.Null[int64](), Name: "orphan"},
				} {
					if _, err := kids.Save(ctx, &k); err != nil {
						t.Fatal(err)
					}
				}

				got, err := kids.GetAll(ctx, crud.Preload("Parent"), crud.OrderBy(crud.Asc("ID")))
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 2 {
					t.Fatalf("%d kids", len(got))
				}
				if got[0].Parent == nil || got[0].Parent.Name != "p" {
					t.Fatalf("the attached child lost its parent: %+v", got[0].Parent)
				}
				if got[1].Parent != nil {
					t.Fatalf("a NULL foreign key was given parent %+v", got[1].Parent)
				}

				back, err := parents.GetAll(ctx, crud.Preload("Kids"))
				if err != nil {
					t.Fatal(err)
				}
				if len(back) != 1 || len(back[0].Kids) != 1 || back[0].Kids[0].Name != "attached" {
					t.Fatalf("parent's children = %+v, want just the attached one", back[0].Kids)
				}
			})

			t.Run("when every foreign key is NULL nothing is asked", func(t *testing.T) {
				egWipe(t, tg.source)
				s := egWatch(tg.source)
				kids := EgKids.Bind(s)
				for _, k := range []EgKid{
					{ID: 1, ParentID: crud.Null[int64](), Name: "a"},
					{ID: 2, ParentID: crud.Null[int64](), Name: "b"},
				} {
					if _, err := kids.Save(ctx, &k); err != nil {
						t.Fatal(err)
					}
				}
				s.forget()

				got, err := kids.GetAll(ctx, crud.Preload("Parent"))
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 2 || got[0].Parent != nil || got[1].Parent != nil {
					t.Fatalf("kids = %+v, want both without a parent", got)
				}
				if stmts := s.matching("eg_parents"); len(stmts) != 0 {
					t.Fatalf("with no key to look up, the preload still ran %v", stmts)
				}
			})

			t.Run("a many_to_many past the batch boundary", func(t *testing.T) {
				egWipe(t, tg.source)
				s := egWatch(tg.source)
				parents := EgParents.Bind(s)

				const n = 1000
				owners := make([][]any, 0, n)
				links := make([][]any, 0, n)
				for i := 1; i <= n; i++ {
					owners = append(owners, []any{int64(i), fmt.Sprintf("p-%04d", i)})
					if i < n {
						links = append(links, []any{int64(i), int64(1)})
					}
				}
				mxBulkInsert(t, tg.source, "eg_parents", []string{"id", "name"}, owners)
				mxBulkInsert(t, tg.source, "eg_marks", []string{"id", "slug"}, [][]any{{int64(1), "shared"}})
				mxBulkInsert(t, tg.source, "eg_parent_marks", []string{"parent_id", "mark_id"}, links)
				s.forget()

				got, err := parents.GetAll(ctx, crud.Preload("Marks"), crud.OrderBy(crud.Asc("ID")))
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != n {
					t.Fatalf("%d parents, want %d", len(got), n)
				}
				for _, p := range got[:n-1] {
					if len(p.Marks) != 1 || p.Marks[0].Slug != "shared" {
						t.Fatalf("parent %d got %+v, want the one shared mark: a preload batch was dropped",
							p.ID, p.Marks)
					}
				}
				if last := got[n-1]; last.Marks == nil || len(last.Marks) != 0 {
					t.Fatalf("the parent with no join rows got %#v, want an empty slice", last.Marks)
				}

				if stmts := s.matching("eg_parent_marks"); len(stmts) != 2 {
					t.Fatalf("%d statements against the join table, want 2 batches for %d keys", len(stmts), n)
				}
			})

			t.Run("a preload path deeper than the budget", func(t *testing.T) {
				egWipe(t, tg.source)
				parents := EgParents.Bind(tg.source)
				shallow := EgShallowParents.Bind(tg.source)
				p := EgParent{ID: 1, Name: "p"}
				if _, err := parents.Save(ctx, &p); err != nil {
					t.Fatal(err)
				}
				k := EgKid{ID: 1, ParentID: crud.Set(int64(1)), Name: "k"}
				if _, err := EgKids.Bind(tg.source).Save(ctx, &k); err != nil {
					t.Fatal(err)
				}

				if _, err := shallow.GetAll(ctx, crud.Preload("Kids.Parent")); err != nil {
					t.Fatalf("two hops is what the budget allows: %v", err)
				}
				_, err := shallow.GetAll(ctx, crud.Preload("Kids.Parent.Kids"))
				var se *crud.SchemaError
				if !errors.As(err, &se) {
					t.Fatalf("err = %T %v, want a *crud.SchemaError naming the budget", err, err)
				}

				deep, err := parents.GetAll(ctx, crud.Preload("Kids.Parent.Kids.Parent.Kids"))
				if err != nil {
					t.Fatal(err)
				}
				if len(deep) != 1 || len(deep[0].Kids) != 1 {
					t.Fatalf("parents = %+v", deep)
				}
				leaf := deep[0].Kids[0].Parent
				if leaf == nil || len(leaf.Kids) != 1 || leaf.Kids[0].Parent == nil {
					t.Fatalf("the chain stopped early: %+v", leaf)
				}
				if bottom := leaf.Kids[0].Parent.Kids; len(bottom) != 1 || bottom[0].Name != "k" {
					t.Fatalf("the fifth hop = %+v, want the one child", bottom)
				}
			})
		})
	}
}
