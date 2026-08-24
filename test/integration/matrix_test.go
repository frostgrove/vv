//go:build integration

package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	gormmysql "gorm.io/driver/mysql"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/shardit-io/go-rx-crud/adapter/crudpgx"
	"github.com/shardit-io/go-rx-crud/adapter/crudsql"
	"github.com/shardit-io/go-rx-crud/crud"
	"github.com/shardit-io/go-rx-crud/query"
	"github.com/shardit-io/go-rx-crud/repo/decorators/specs"
	entpkg "github.com/shardit-io/go-rx-crud/test/ent"
	entuser "github.com/shardit-io/go-rx-crud/test/ent/user"
	"github.com/shardit-io/go-rx-crud/test/entstore"
	"github.com/shardit-io/go-rx-crud/test/gormstore"
)

// This file is the provider matrix. Every other test in this package proves one
// driver, one ORM or one database does the right thing; these prove the answer
// does not depend on which one you picked.

// ---------------------------------------------------------------------------
// one query, every provider

// mxProvider is one supported way of reaching one of the two databases.
type mxProvider struct {
	name string
	src  crud.Source
}

// mxProviders is one entry per crud.Source these two databases can be reached
// through, and each entry is a genuinely different one. sqlx and gorm are not
// separate bindings on a read: sqlx.NewDb(db, …).DB is db, and gorm's DB()
// returns the very *sql.DB it was handed, so those rows would be the same
// struct over the same pointer as the database/sql row beside them. That both
// libraries can lend rx-crud their pool is proven where it is observable, in
// driver_sqlx_test.go and driver_gorm_test.go. The MySQL row alias is likewise
// a write-path difference — it only rewrites the upsert clause — and it is
// covered by driver_sql_test.go, which runs the whole conformance suite with it.
func mxProviders() []mxProvider {
	return []mxProvider{
		{"database/sql+postgres", crudsql.Postgres(pgDB)},
		{"database/sql+mysql", crudsql.MySQL(myDB)},
		{"pgx", crudpgx.Open(pgPool)},
		{"ent+postgres", newEntSource(entClient(pgDB, dialect.Postgres), crud.Postgres{})},
		{"ent+mysql", newEntSource(entClient(myDB, dialect.MySQL), crud.MySQL{})},
	}
}

// mxUsers is the shared fixture. The keys are assigned by hand so that a row
// read out of Postgres and the same row read out of MySQL are comparable down
// to the primary key; created_at is the one column nobody compares, because it
// is a database default and the two engines are entitled to disagree.
//
// Cid's age is explicitly null rather than left undefined, because this fixture
// is also the expected result: an Opt read back out of a NULL column is null,
// and a fixture that said "undefined" would not describe the row it wrote.
//
// The text is all one case on purpose. MySQL's default collation makes LIKE
// case-insensitive and Postgres' does not, so a fixture that mixed cases would
// be measuring the engines' collations rather than rx-crud.
func mxUsers() []User {
	return []User{
		{ID: 1, TenantID: 1, Email: "ada@x.io", Name: "Ada Lovelace", Age: crud.Set(36), Active: true},
		{ID: 2, TenantID: 1, Email: "bob@x.io", Name: "Bob Barker", Age: crud.Set(24), Active: true},
		{ID: 3, TenantID: 1, Email: "cid@x.io", Name: "Cid Highwind", Age: crud.Null[int](), Active: false},
		{ID: 4, TenantID: 2, Email: "dee@x.io", Name: "Dee Dee", Age: crud.Set(41), Active: true},
		{ID: 5, TenantID: 2, Email: "eve@x.io", Name: "Eve Adams", Age: crud.Set(24), Active: false},
		{ID: 6, TenantID: 2, Email: "fay@x.io", Name: "Fay Wray", Age: crud.Set(60), Active: true},
	}
}

func mxSeedUsers(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, src := range []crud.Source{crudsql.Postgres(pgDB), crudsql.MySQL(myDB)} {
		if _, err := src.Exec(ctx, "DELETE FROM users"); err != nil {
			t.Fatalf("%s: reset: %v", src.Dialect().Name(), err)
		}
		repo := Users.Bind(src)
		for _, u := range mxUsers() {
			if err := repo.Save(ctx, &u); err != nil {
				t.Fatalf("%s: seeding %s: %v", src.Dialect().Name(), u.Email, err)
			}
		}
	}
}

// mxRow renders every column a read is allowed to differ on. Opt.String tells
// undefined from null, so a column a projection left out can never be mistaken
// for a column that came back NULL.
func mxRow(u User) string {
	return fmt.Sprintf("id=%d tenant=%d %s %q age=%s active=%t",
		u.ID, u.TenantID, u.Email, u.Name, u.Age, u.Active)
}

func mxRows(items []User) []string {
	out := make([]string, len(items))
	for i, u := range items {
		out[i] = mxRow(u)
	}
	return out
}

// mxFixtureRows is the expected answer to a whole-row read, spelled out of the
// fixture: what comes back has to be the row that went in, column for column.
// Only a projection reads something other than a whole row, so only a
// projection has to state its expectation by hand.
func mxFixtureRows(t *testing.T, emails []string) []string {
	t.Helper()
	fixture := mxUsers()
	out := make([]string, len(emails))
	for i, email := range emails {
		j := slices.IndexFunc(fixture, func(u User) bool { return u.Email == email })
		if j < 0 {
			t.Fatalf("%s is not in the fixture", email)
		}
		out[i] = mxRow(fixture[j])
	}
	return out
}

// The claim the whole DSL rests on: one document, one answer, whatever is
// underneath it. Each request is compiled once and handed to every provider,
// and each of them is held to the same literal rows — not merely to whatever
// its neighbour said — so that a regression which hits every driver equally
// still fails here.
func TestEveryProviderAnswersTheSameQuery(t *testing.T) {
	ctx := context.Background()
	mxSeedUsers(t)
	providers := mxProviders()

	for _, tc := range []struct {
		name  string
		doc   string
		want  []string // emails, in order; the rest of each row comes from the fixture
		total int64    // rows matching the filter, ignoring the page
		pages int
		rows  []string // spelled out only where the read is not a whole-row read
	}{
		{"filter and sort", `{"filter":{"active":true,"age":{"gte":30}},"sort":["email"],"unpaged":true}`,
			[]string{"ada@x.io", "dee@x.io", "fay@x.io"}, 3, 1, nil},
		{"a null column is not a zero one", `{"filter":{"age":{"isNull":true}},"unpaged":true}`,
			[]string{"cid@x.io"}, 1, 1, nil},
		{"second page", `{"sort":["email"],"page":2,"limit":2}`,
			[]string{"cid@x.io", "dee@x.io"}, 6, 3, nil},
		{"offset window", `{"sort":["email"],"offset":1,"limit":2}`,
			[]string{"bob@x.io", "cid@x.io"}, 6, 3, nil},
		{"negated set", `{"filter":{"not":{"email":{"in":["ada@x.io","bob@x.io"]}}},"sort":["email"],"unpaged":true}`,
			[]string{"cid@x.io", "dee@x.io", "eve@x.io", "fay@x.io"}, 4, 1, nil},
		{"or, sorted by two keys", `{"filter":{"or":[{"tenantID":2},{"name":{"startsWith":"Bob"}}]},"sort":["-age","email"],"unpaged":true}`,
			[]string{"fay@x.io", "dee@x.io", "bob@x.io", "eve@x.io"}, 4, 1, nil},
		{"between", `{"filter":{"age":{"between":[24,36]}},"sort":["age","email"],"unpaged":true}`,
			[]string{"bob@x.io", "eve@x.io", "ada@x.io"}, 3, 1, nil},
		{"search across two columns", `{"search":"Adams","searchFields":["name","email"],"sort":["email"],"unpaged":true}`,
			[]string{"eve@x.io"}, 1, 1, nil},
		{"flat terms", `{"terms":[{"path":"tenantID","op":"eq","values":["1"]},{"path":"active","op":"eq","values":["true"]}],"sort":["email"],"unpaged":true}`,
			[]string{"ada@x.io", "bob@x.io"}, 2, 1, nil},
		// A projection is the one read that must come back incomplete. Email and
		// active were asked for, the key comes along because a row has to stay
		// addressable, and every other column has to arrive undefined — a zero
		// there is a value a client would believe.
		{"projection", `{"select":["email","active"],"sort":["email"],"limit":3}`,
			[]string{"ada@x.io", "bob@x.io", "cid@x.io"}, 6, 2,
			[]string{
				`id=1 tenant=0 ada@x.io "" age=<undefined> active=true`,
				`id=2 tenant=0 bob@x.io "" age=<undefined> active=true`,
				`id=3 tenant=0 cid@x.io "" age=<undefined> active=false`,
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req query.Request
			if err := json.Unmarshal([]byte(tc.doc), &req); err != nil {
				t.Fatal(err)
			}
			opts, err := req.Compile(Users.Meta(), nil)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.rows
			if want == nil {
				want = mxFixtureRows(t, tc.want)
			}

			for _, p := range providers {
				repo := Users.Bind(p.src)
				page, err := repo.Get(ctx, opts...)
				if err != nil {
					t.Fatalf("%s: %v", p.name, err)
				}
				if got := mxRows(page.Items); !eq(got, want) {
					t.Fatalf("%s read:\n  %s\nwant:\n  %s",
						p.name, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
				}
				if page.Total != tc.total || page.TotalPages != tc.pages {
					t.Fatalf("%s paged %d rows over %d pages, want %d over %d",
						p.name, page.Total, page.TotalPages, tc.total, tc.pages)
				}
				n, err := repo.Count(ctx, opts...)
				if err != nil {
					t.Fatalf("%s: %v", p.name, err)
				}
				if n != tc.total {
					t.Fatalf("%s counted %d rows but paged %d", p.name, n, tc.total)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// the gorm models, on both engines

// mxGorm is one database with gorm's own schema on it, plus the one piece of
// SQL a gorm application still has to spell differently per engine.
type mxGorm struct {
	name    string
	db      *gorm.DB
	src     crud.Source
	promote string // appends " (promoted)" to teams.name
}

func mxGorms(t *testing.T) []mxGorm {
	t.Helper()
	out := []mxGorm{
		{
			name: "postgres", db: openGorm(t, gormpg.New(gormpg.Config{Conn: pgDB})),
			src: crudsql.Postgres(pgDB), promote: "name || ' (promoted)'",
		},
		{
			name: "mysql", db: openGorm(t, gormmysql.New(gormmysql.Config{Conn: myDB})),
			src: crudsql.MySQL(myDB), promote: "CONCAT(name, ' (promoted)')",
		},
	}
	for _, g := range out {
		if err := g.db.AutoMigrate(&Team{}, &Member{}, &Label{}); err != nil {
			t.Fatalf("%s: AutoMigrate: %v", g.name, err)
		}
		for _, table := range []string{"team_labels", "members", "labels", "teams"} {
			if err := g.db.Exec("DELETE FROM " + table).Error; err != nil {
				t.Fatalf("%s: reset %s: %v", g.name, table, err)
			}
		}
	}
	return out
}

func mxSlugs(labels []Label) []string {
	out := make([]string, len(labels))
	for i, l := range labels {
		out[i] = l.Slug
	}
	slices.Sort(out)
	return out
}

// gorm creates the schema and writes every row; rx-crud answers the query. The
// Postgres half of this is gorm_model_test.go — what is proven here is that
// none of it was Postgres: the same models, the same DSL document and the same
// generated metamodel work against a schema MySQL's AutoMigrate produced.
func TestGormModelThroughRxCrudOnBothEngines(t *testing.T) {
	for _, g := range mxGorms(t) {
		t.Run(g.name, func(t *testing.T) {
			ctx := context.Background()
			teams := specs.Executor(GormTeams.Bind(g.src))
			members := GormMembers.Bind(g.src)

			goLbl, rust := Label{Slug: "go"}, Label{Slug: "rust"}
			if err := g.db.Create(&[]*Label{&goLbl, &rust}).Error; err != nil {
				t.Fatal(err)
			}
			core := Team{Name: "core", Labels: []Label{goLbl, rust}}
			ops := Team{Name: "ops", Labels: []Label{rust}}
			if err := g.db.Create(&[]*Team{&core, &ops}).Error; err != nil {
				t.Fatal(err)
			}
			age := 31
			if err := g.db.Create(&[]*Member{
				{TeamID: core.ID, Name: "Ann", Age: &age},
				{TeamID: core.ID, Name: "Bob"},
				{TeamID: ops.ID, Name: "Cid"},
			}).Error; err != nil {
				t.Fatal(err)
			}

			// A filter that walks the association gorm declared, and a preload
			// back along the same edge.
			var req query.Request
			if err := json.Unmarshal([]byte(`{
				"filter":  {"team.name": "core"},
				"preload": ["team"],
				"sort":    ["name"],
				"unpaged": true
			}`), &req); err != nil {
				t.Fatal(err)
			}
			opts, err := req.Compile(GormMembers.Meta(), nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := members.GetAll(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || got[0].Name != "Ann" || got[1].Name != "Bob" {
				t.Fatalf("members of core = %+v", got)
			}
			if got[0].Team == nil || got[0].Team.Name != "core" {
				t.Fatalf("team was not preloaded: %+v", got[0].Team)
			}
			if got[0].Age == nil || *got[0].Age != 31 {
				t.Fatalf("age = %v, want 31", got[0].Age)
			}
			if got[1].Age != nil {
				t.Fatalf("a NULL age came back as %v", *got[1].Age)
			}

			// many2many through the join table AutoMigrate created: "rust" is on
			// both teams, so the filter must not duplicate a team and the preload
			// must hand the one label row to both of them.
			found, err := teams.FindAll(ctx,
				specs.Where(gormstore.Team_.Labels.Slug.Eq("rust")),
				crud.Preload("Labels"), crud.OrderBy(crud.Asc("Name")))
			if err != nil {
				t.Fatal(err)
			}
			if len(found) != 2 || found[0].Name != "core" || found[1].Name != "ops" {
				t.Fatalf("teams tagged rust = %+v", found)
			}
			if want := []string{"go", "rust"}; !eq(mxSlugs(found[0].Labels), want) {
				t.Fatalf("core's labels = %v, want %v", mxSlugs(found[0].Labels), want)
			}
			if want := []string{"rust"}; !eq(mxSlugs(found[1].Labels), want) {
				t.Fatalf("ops' labels = %v, want %v", mxSlugs(found[1].Labels), want)
			}
		})
	}
}

// gorm's tombstones are invisible to rx-crud because the repository declares a
// scope, and a scope is a property of the declaration, not of the engine.
func TestGormSoftDeletesStayInvisibleOnBothEngines(t *testing.T) {
	for _, g := range mxGorms(t) {
		t.Run(g.name, func(t *testing.T) {
			ctx := context.Background()
			members := GormMembers.Bind(g.src)

			team := Team{Name: "core"}
			if err := g.db.Create(&team).Error; err != nil {
				t.Fatal(err)
			}
			ann := Member{TeamID: team.ID, Name: "Ann"}
			bob := Member{TeamID: team.ID, Name: "Bob"}
			if err := g.db.Create(&[]*Member{&ann, &bob}).Error; err != nil {
				t.Fatal(err)
			}
			if err := g.db.Delete(&ann).Error; err != nil {
				t.Fatal(err)
			}

			if n, err := members.Count(ctx); err != nil || n != 1 {
				t.Fatalf("count = %d err = %v: a tombstone leaked into the results", n, err)
			}
			if _, err := members.GetByID(ctx, ann.ID); !errors.Is(err, crud.ErrNotFound) {
				t.Fatalf("err = %v: a soft-deleted row must look missing", err)
			}
			all, err := members.GetAll(ctx, crud.Where(crud.IsNotNull("DeletedAt")))
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 0 {
				t.Fatalf("a caller widened the scope back open: %+v", all)
			}
			var raw int64
			if err := g.db.Unscoped().Model(&Member{}).Count(&raw).Error; err != nil {
				t.Fatal(err)
			}
			if raw != 2 {
				t.Fatalf("gorm sees %d rows, want the tombstone still there", raw)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ent's generated entity, on both engines

// mxEnt is one database reached both ways: through ent's client and through
// rx-crud's source.
type mxEnt struct {
	name    string
	db      *sql.DB
	dialect string
	src     crud.Source
}

func mxEnts() []mxEnt {
	return []mxEnt{
		{"postgres", pgDB, dialect.Postgres, crudsql.Postgres(pgDB)},
		{"mysql", myDB, dialect.MySQL, crudsql.MySQL(myDB)},
	}
}

// ent's generated struct is the model, on either engine: ent writes, the DSL
// and the generated metamodel read, rx-crud patches, ent reads back.
func TestEntModelThroughRxCrudOnBothEngines(t *testing.T) {
	for _, e := range mxEnts() {
		t.Run(e.name, func(t *testing.T) {
			ctx := context.Background()
			truncate(t, e.db)
			client := entClient(e.db, e.dialect)
			users := EntUsers.Bind(e.src)
			sp := specs.Executor(users)

			for i, name := range []string{"Ann", "Bob", "Cid"} {
				if _, err := client.User.Create().
					SetTenantID(1).SetEmail(name + "@x.io").SetName(name).SetAge(30 + i).SetActive(i != 2).
					Save(ctx); err != nil {
					t.Fatal(err)
				}
			}

			var req query.Request
			if err := json.Unmarshal([]byte(
				`{"filter":{"active":true,"age":{"gte":30}},"sort":["-age"],"page":1,"limit":10}`), &req); err != nil {
				t.Fatal(err)
			}
			opts, err := req.Compile(EntUsers.Meta(), nil)
			if err != nil {
				t.Fatal(err)
			}
			page, err := users.Get(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 2 || len(page.Items) != 2 || page.Items[0].Name != "Bob" {
				t.Fatalf("page = %d of %d, first %+v", len(page.Items), page.Total, page.Items[0].Name)
			}
			if page.Items[0].Age == nil || *page.Items[0].Age != 31 {
				t.Fatalf("age = %v", page.Items[0].Age)
			}

			// The generated metamodel, over ent's own entity type.
			found, err := sp.FindAll(ctx,
				specs.Where(entstore.User_.Age.Gte(31)).And(entstore.User_.Name.StartsWith("B")))
			if err != nil {
				t.Fatal(err)
			}
			if len(found) != 1 || found[0].Name != "Bob" {
				t.Fatalf("metamodel found %+v", found)
			}

			// The write path. ent puts the created_at default in Go rather than in
			// the column, so a write that bypasses ent supplies it.
			u := entpkg.User{TenantID: 1, Email: "new@x.io", Name: "New", Active: true, CreatedAt: time.Now()}
			if err := users.Save(ctx, &u); err != nil {
				t.Fatal(err)
			}
			if u.ID == 0 {
				t.Fatal("the generated key was not read back")
			}
			back, err := client.User.Query().Where(entuser.IDEQ(u.ID)).Only(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if back.Name != "New" || back.Age != nil {
				t.Fatalf("ent read back %+v", back)
			}

			name := "Renamed"
			patched, err := users.Update(ctx, u.ID, EntUserUpdate{Name: &name, Age: crud.Set(41)})
			if err != nil {
				t.Fatal(err)
			}
			if patched.Name != "Renamed" || patched.Age == nil || *patched.Age != 41 {
				t.Fatalf("updated = %+v", patched)
			}
			if patched.Email != "new@x.io" {
				t.Fatalf("an undefined DTO field changed the row: email = %q", patched.Email)
			}
			patched, err = users.Update(ctx, u.ID, EntUserUpdate{Age: crud.Null[int]()})
			if err != nil {
				t.Fatal(err)
			}
			if patched.Age != nil {
				t.Fatalf("an explicit null should clear the column, got %v", *patched.Age)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// the usecase pattern, on both engines

// mxTeamUsecase is the usecase from usecase_test.go with its one Postgres-ism —
// `||`, which MySQL reads as a logical OR — lifted into a parameter. Everything
// that makes it a usecase (compile the DSL, let the ORM own the transaction,
// join it with crud.WithExecutor) is the same code on both engines.
type mxTeamUsecase struct {
	db      *gorm.DB
	members crud.Repo[Member, uint, MemberUpdate]
	promote string
}

func (uc mxTeamUsecase) PromoteMembers(ctx context.Context, teamID uint, req *query.Request) (promoteResult, error) {
	opts, err := req.Compile(uc.members.Meta(), nil)
	if err != nil {
		return promoteResult{}, err
	}

	var out promoteResult
	err = uc.db.Transaction(func(tx *gorm.DB) error {
		txCtx := crud.WithExecutor(ctx, crudsql.From(tx.Statement.ConnPool))

		page, err := uc.members.Get(txCtx, append(opts, crud.Where(crud.Eq("TeamID", teamID)))...)
		if err != nil {
			return err
		}
		out.Matched = page.Total

		if err := tx.Model(&Team{}).Where("id = ?", teamID).
			Update("name", gorm.Expr(uc.promote)).Error; err != nil {
			return err
		}
		for _, m := range page.Items {
			renamed := "Sr. " + m.Name
			updated, err := uc.members.Update(txCtx, m.ID, MemberUpdate{Name: &renamed})
			if err != nil {
				return err
			}
			out.Promoted = append(out.Promoted, updated.Name)
		}
		return nil
	})
	if err != nil {
		return promoteResult{}, err
	}
	return out, nil
}

func TestGormUsecaseDSLInsideTransactionOnBothEngines(t *testing.T) {
	for _, g := range mxGorms(t) {
		t.Run(g.name, func(t *testing.T) {
			ctx := context.Background()
			uc := mxTeamUsecase{db: g.db, members: GormMembers.Bind(g.src), promote: g.promote}

			team := Team{Name: "core"}
			if err := g.db.Create(&team).Error; err != nil {
				t.Fatal(err)
			}
			young, senior := 22, 41
			if err := g.db.Create(&[]*Member{
				{TeamID: team.ID, Name: "Ann", Age: &senior},
				{TeamID: team.ID, Name: "Bob", Age: &young},
				{TeamID: team.ID, Name: "Cid", Age: &senior},
			}).Error; err != nil {
				t.Fatal(err)
			}

			var req query.Request
			if err := json.Unmarshal([]byte(
				`{"filter":{"age":{"gte":30}},"sort":["name"],"limit":50}`), &req); err != nil {
				t.Fatal(err)
			}
			got, err := uc.PromoteMembers(ctx, team.ID, &req)
			if err != nil {
				t.Fatal(err)
			}
			if got.Matched != 2 || !eq(got.Promoted, []string{"Sr. Ann", "Sr. Cid"}) {
				t.Fatalf("result = %+v", got)
			}

			// Both halves of the one transaction landed.
			var reloaded Team
			if err := g.db.First(&reloaded, team.ID).Error; err != nil {
				t.Fatal(err)
			}
			if reloaded.Name != "core (promoted)" {
				t.Fatalf("the ORM's half of the transaction = %q", reloaded.Name)
			}
			var bob Member
			if err := g.db.Where("name = ?", "Bob").First(&bob).Error; err != nil {
				t.Fatalf("a member outside the filter was renamed: %v", err)
			}
		})
	}
}

// The ent usecase from usecase_test.go, not one line of it changed, pointed at
// MySQL: the pattern is ent's transaction plus crud.WithExecutor, and neither
// of those knows what a dialect is.
func TestEntUsecaseDSLInsideTransactionOnMySQL(t *testing.T) {
	ctx := context.Background()
	truncate(t, myDB)
	client := entClient(myDB, dialect.MySQL)
	uc := userUsecase{client: client, users: EntUsers.Bind(crudsql.MySQL(myDB))}

	for i, name := range []string{"Ann", "Bob", "Cid"} {
		if _, err := client.User.Create().
			SetTenantID(1).SetEmail(name + "@x.io").SetName(name).SetAge(28 + i*5).SetActive(true).
			Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.User.Create().
		SetTenantID(2).SetEmail("other@x.io").SetName("Other").SetAge(50).SetActive(true).
		Save(ctx); err != nil {
		t.Fatal(err)
	}

	var req query.Request
	if err := json.Unmarshal([]byte(
		`{"filter":{"age":{"gte":33}},"sort":["email"],"limit":50}`), &req); err != nil {
		t.Fatal(err)
	}
	got, err := uc.DeactivateUsers(ctx, 1, &req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Matched != 2 || len(got.Deactivated) != 2 {
		t.Fatalf("result = %+v", got)
	}

	still, err := client.User.Query().Where(entuser.ActiveEQ(true)).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if still != 2 {
		t.Fatalf("%d users still active, want Ann and the other tenant", still)
	}
	renamed, err := client.User.Query().Where(entuser.NameEQ("deactivated")).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if renamed != 2 {
		t.Fatalf("%d renamed by the repository half of the transaction, want 2", renamed)
	}
	other, err := client.User.Query().Where(entuser.TenantIDEQ(2)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !other.Active || other.Name != "Other" {
		t.Fatalf("another tenant's row was touched: %+v", other)
	}
}

// ---------------------------------------------------------------------------
// preloads at the edges

// A preload is keyed by the rows on the page, so paging must not lose a child,
// and a child shared with another page must still arrive on this one.
//
// That the IN list carries the page's keys and nothing else is a property of
// the statement rather than of the result, and it is pinned where it can be
// read: TestPreloadBelongsToIsOneBatchedQuery in crud/preload_test.go.
func TestPreloadsSurvivePaging(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedBlog(t, b)

			var req query.Request
			if err := json.Unmarshal([]byte(
				`{"preload":["author","tags"],"sort":["title"],"page":1,"limit":2}`), &req); err != nil {
				t.Fatal(err)
			}
			opts, err := req.Compile(Articles.Meta(), nil)
			if err != nil {
				t.Fatal(err)
			}

			first, err := b.articles.Get(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if first.Total != 3 || first.TotalPages != 2 {
				t.Fatalf("pager = %d rows over %d pages", first.Total, first.TotalPages)
			}
			if want := []string{"draft", "go generics"}; !eq(titles(first.Items), want) {
				t.Fatalf("page 1 = %v, want %v", titles(first.Items), want)
			}
			for _, a := range first.Items {
				if a.Author == nil || a.Author.Name != "Ann" {
					t.Fatalf("%q lost its author on a paged read: %+v", a.Title, a.Author)
				}
			}
			if n := len(first.Items[0].Tags); n != 0 {
				t.Fatalf("the draft has no tags, the preload found %d", n)
			}
			if n := len(first.Items[1].Tags); n != 2 {
				t.Fatalf("go generics has 2 tags, the preload found %d", n)
			}

			req.Page = 2
			opts, err = req.Compile(Articles.Meta(), nil)
			if err != nil {
				t.Fatal(err)
			}
			second, err := b.articles.Get(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"rust traits"}; !eq(titles(second.Items), want) {
				t.Fatalf("page 2 = %v, want %v", titles(second.Items), want)
			}
			// "rust" is shared with page 1's article, so a preload that cached or
			// deduped by child rather than by parent would drop it here.
			if got := second.Items[0]; got.Author == nil || got.Author.Name != "Bob" || len(got.Tags) != 1 {
				t.Fatalf("page 2's row = author %+v tags %+v", got.Author, got.Tags)
			}
			if second.Items[0].Tags[0].Slug != "rust" {
				t.Fatalf("tag = %q", second.Items[0].Tags[0].Slug)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// preloads past a batch boundary

// mxRelTarget is one database with the matrix-only relation on it.
type mxRelTarget struct {
	name   string
	src    crud.Source
	owners crud.Repo[MxOwner, int64, struct{}]
	items  crud.Repo[MxItem, int64, struct{}]
}

func mxRelTargets(t *testing.T) []mxRelTarget {
	t.Helper()
	ctx := context.Background()
	out := []mxRelTarget{
		{name: "postgres", src: crudsql.Postgres(pgDB)},
		{name: "mysql", src: crudsql.MySQL(myDB)},
	}
	for i, stmts := range [][]string{schemaMatrixPostgres, schemaMatrixMySQL} {
		for _, stmt := range stmts {
			if _, err := out[i].src.Exec(ctx, stmt); err != nil {
				t.Fatalf("%s: %s: %v", out[i].name, stmt, err)
			}
		}
		out[i].owners = MxOwners.Bind(out[i].src)
		out[i].items = MxItems.Bind(out[i].src)
	}
	return out
}

// mxBulkInsert writes the fixture a few hundred rows per statement. The whole
// point of this fixture is its size, and one INSERT per row would be the slowest
// thing in the suite by an order of magnitude.
func mxBulkInsert(t *testing.T, src crud.Source, table string, cols []string, rows [][]any) {
	t.Helper()
	ctx := context.Background()
	d := src.Dialect()
	for start := 0; start < len(rows); start += 200 {
		end := min(start+200, len(rows))
		var b strings.Builder
		b.WriteString("INSERT INTO " + d.Quote(table) + " (")
		for i, c := range cols {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(d.Quote(c))
		}
		b.WriteString(") VALUES ")
		args := make([]any, 0, (end-start)*len(cols))
		for r := start; r < end; r++ {
			if r > start {
				b.WriteString(", ")
			}
			b.WriteString("(")
			for i := range cols {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(d.Placeholder(len(args) + i + 1))
			}
			b.WriteString(")")
			args = append(args, rows[r]...)
		}
		if _, err := src.Exec(ctx, b.String(), args...); err != nil {
			t.Fatalf("seeding %s: %v", table, err)
		}
	}
}

// mxOwnerCount is deliberately past the 900-key preload batch, so both
// directions of the relation need more than one statement per level.
const mxOwnerCount = 1000

// The one number a preload's correctness depends on is how many parent keys go
// into a single IN list. Below the batch size every implementation looks right;
// past it, a loop that forgets to run its last chunk drops the tail of the
// result silently. So: more parents than fit in one batch, in both directions.
func TestPreloadBatchesSpanTheChunkBoundary(t *testing.T) {
	for _, tg := range mxRelTargets(t) {
		t.Run(tg.name, func(t *testing.T) {
			ctx := context.Background()

			owners := make([][]any, 0, mxOwnerCount)
			items := make([][]any, 0, mxOwnerCount)
			for i := 1; i <= mxOwnerCount; i++ {
				owners = append(owners, []any{int64(i), fmt.Sprintf("owner-%04d", i)})
				items = append(items, []any{int64(i), int64(i), fmt.Sprintf("item-%04d", i)})
			}
			// A handful of extra rows on one owner, so that one parent's share
			// of the children is not one row like everybody else's: chunking
			// must not decide how many children a parent ends up with.
			const extras = 5
			for i := 1; i <= extras; i++ {
				items = append(items, []any{int64(mxOwnerCount + i), int64(1), fmt.Sprintf("extra-%d", i)})
			}
			mxBulkInsert(t, tg.src, "mx_owners", []string{"id", "name"}, owners)
			mxBulkInsert(t, tg.src, "mx_items", []string{"id", "owner_id", "label"}, items)

			// has_many, from the side with more keys than one batch holds.
			gotOwners, err := tg.owners.GetAll(ctx, crud.Preload("Items"), crud.OrderBy(crud.Asc("ID")))
			if err != nil {
				t.Fatal(err)
			}
			if len(gotOwners) != mxOwnerCount {
				t.Fatalf("%d owners, want %d", len(gotOwners), mxOwnerCount)
			}
			total := 0
			for _, o := range gotOwners {
				if len(o.Items) == 0 {
					t.Fatalf("owner %d came back with no items: a preload batch was dropped", o.ID)
				}
				total += len(o.Items)
			}
			if want := mxOwnerCount + extras; total != want {
				t.Fatalf("%d items across all owners, want %d", total, want)
			}
			if n := len(gotOwners[0].Items); n != 1+extras {
				t.Fatalf("the owner with %d items got %d: a has_many did not hand one parent all of its children",
					1+extras, n)
			}

			// belongs_to, from the side whose keys repeat. Folding those repeats
			// into one IN list is counted in bind args by
			// TestPreloadBelongsToIsOneBatchedQuery in crud/preload_test.go; what
			// is on trial here is only the chunking.
			gotItems, err := tg.items.GetAll(ctx, crud.Preload("Owner"), crud.OrderBy(crud.Asc("ID")))
			if err != nil {
				t.Fatal(err)
			}
			if want := mxOwnerCount + extras; len(gotItems) != want {
				t.Fatalf("%d items, want %d", len(gotItems), want)
			}
			for _, it := range gotItems {
				if it.Owner == nil {
					t.Fatalf("item %d (owner %d) came back without its owner", it.ID, it.OwnerID)
				}
				if it.Owner.ID != it.OwnerID {
					t.Fatalf("item %d was given owner %d", it.ID, it.Owner.ID)
				}
			}
		})
	}
}
