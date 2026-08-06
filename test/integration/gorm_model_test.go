//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"gorm.io/gorm"

	"rx-crud/test/gormstore"

	"rx-crud/adapter/crudsql"
	"rx-crud/crud"
	"rx-crud/query"
	"rx-crud/repo/basic"
	"rx-crud/repo/decorators/specs"
)

// The models live in test/gormstore, next to their generated DTOs and
// metamodels — exactly the layout docs/gorm.md describes.
type (
	Team   = gormstore.Team
	Member = gormstore.Member
	Label  = gormstore.Label

	TeamUpdate   = gormstore.TeamUpdate
	MemberUpdate = gormstore.MemberUpdate
)

// Soft deletes are a gorm-layer feature; rx-crud sees the raw table, so the
// tombstone filter is declared once, here, and no query option can widen it.
var (
	GormTeams = basic.Define[Team, uint, TeamUpdate]("teams",
		basic.Scope(crud.IsNull("DeletedAt")))
	GormMembers = basic.Define[Member, uint, MemberUpdate]("members",
		basic.Scope(crud.IsNull("DeletedAt")))
	GormLabels = basic.Define[Label, uint, gormstore.LabelUpdate]("labels",
		basic.Scope(crud.IsNull("DeletedAt")))
)

func gormDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openGorm(t, gormPGDialector())
	if err := db.AutoMigrate(&Team{}, &Member{}, &Label{}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"team_labels", "members", "labels", "teams"} {
		if err := db.Exec("DELETE FROM " + table).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// A gorm model maps to exactly the columns gorm itself would use.
func TestGormModelIsAnRxCrudModel(t *testing.T) {
	s, err := crud.SchemaOf[Member]()
	if err != nil {
		t.Fatalf("a gorm model should be usable as-is: %v", err)
	}
	want := map[string]bool{
		"id": true, "created_at": true, "updated_at": true, "deleted_at": true,
		"team_id": true, "name": true, "age": true,
	}
	if len(s.Columns()) != len(want) {
		t.Fatalf("columns = %v, want %v", s.Columns(), want)
	}
	for _, c := range s.Columns() {
		if !want[c] {
			t.Fatalf("unexpected column %q in %v", c, s.Columns())
		}
	}
	// gorm.Model is flattened; the association field is a relation, not a column.
	if s.PK.Name != "ID" || !s.PK.Auto {
		t.Fatalf("pk = %+v", s.PK)
	}
	if s.Field("Team") != nil {
		t.Fatal("an association must not become a column")
	}
	if s.Relation("Team") == nil {
		t.Fatal("the rel tag should have made Team a relation")
	}
	// gorm.DeletedAt is one column, not a flattened sql.NullTime.
	if f := s.Field("DeletedAt"); f == nil || f.Column != "deleted_at" {
		t.Fatalf("deleted_at = %+v", f)
	}
}

// The mapping self-check every gorm project should copy: rx-crud derives column
// names from the Go field names, gorm's schema parser knows the real ones.
func TestGormMappingMatchesGorm(t *testing.T) {
	db := gormDB(t)
	s, err := crud.SchemaOf[Member]()
	if err != nil {
		t.Fatal(err)
	}
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(&Member{}); err != nil {
		t.Fatal(err)
	}
	for _, f := range s.Fields {
		if stmt.Schema.LookUpField(f.Column) == nil {
			t.Errorf("rx-crud maps %q, gorm has no such column", f.Column)
		}
	}
	if GormMembers.Meta().Table != stmt.Schema.Table {
		t.Fatalf("table = %q, gorm says %q", GormMembers.Meta().Table, stmt.Schema.Table)
	}
}

// gorm writes, rx-crud reads — including the DSL, relations and preloads.
func TestGormModelThroughRxCrud(t *testing.T) {
	ctx := context.Background()
	db := gormDB(t)
	src := crudsql.Postgres(pgDB)

	teams := specs.Executor(GormTeams.Bind(src))
	members := GormMembers.Bind(src)

	// Everything below is written with gorm's own API.
	go1 := Label{Slug: "go"}
	rust := Label{Slug: "rust"}
	if err := db.Create(&[]*Label{&go1, &rust}).Error; err != nil {
		t.Fatal(err)
	}
	core := Team{Name: "core", Labels: []Label{go1, rust}}
	ops := Team{Name: "ops", Labels: []Label{rust}}
	if err := db.Create(&[]*Team{&core, &ops}).Error; err != nil {
		t.Fatal(err)
	}
	age := 31
	if err := db.Create(&[]*Member{
		{TeamID: core.ID, Name: "Ann", Age: &age},
		{TeamID: core.ID, Name: "Bob"},
		{TeamID: ops.ID, Name: "Cid"},
	}).Error; err != nil {
		t.Fatal(err)
	}

	// …and read back through the DSL, walking the association gorm declared.
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
		t.Fatalf("members = %+v", got)
	}
	if got[0].Team == nil || got[0].Team.Name != "core" {
		t.Fatalf("team was not preloaded: %+v", got[0].Team)
	}
	if got[0].Age == nil || *got[0].Age != 31 || got[1].Age != nil {
		t.Fatalf("ages = %v %v", got[0].Age, got[1].Age)
	}

	// many2many, through the same join table gorm created.
	found, err := teams.FindAll(ctx, specs.Of[Team](func(r specs.Root[Team], cb specs.Builder) crud.Predicate {
		return cb.Equal(r.Get("Labels.Slug"), "go")
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "core" {
		t.Fatalf("teams = %+v", found)
	}
}

// The scope keeps gorm's tombstones out of every rx-crud read.
func TestGormSoftDeletesAreInvisible(t *testing.T) {
	ctx := context.Background()
	db := gormDB(t)
	members := GormMembers.Bind(crudsql.Postgres(pgDB))

	team := Team{Name: "core"}
	if err := db.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	ann := Member{TeamID: team.ID, Name: "Ann"}
	bob := Member{TeamID: team.ID, Name: "Bob"}
	if err := db.Create(&[]*Member{&ann, &bob}).Error; err != nil {
		t.Fatal(err)
	}

	// gorm's soft delete: the row stays, deleted_at is set.
	if err := db.Delete(&ann).Error; err != nil {
		t.Fatal(err)
	}

	if n, err := members.Count(ctx); err != nil || n != 1 {
		t.Fatalf("count = %d err = %v: a tombstone leaked into the results", n, err)
	}
	if _, err := members.GetByID(ctx, ann.ID); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v: a soft-deleted row must look missing", err)
	}
	// And a caller cannot widen the scope back open.
	all, err := members.GetAll(ctx, crud.Where(crud.IsNotNull("DeletedAt")))
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("the scope was widened: %+v", all)
	}
	// The row really is still there for gorm.
	var raw int64
	if err := db.Unscoped().Model(&Member{}).Count(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if raw != 2 {
		t.Fatalf("gorm sees %d rows, want 2", raw)
	}
}

// One transaction, gorm's builder and rx-crud's repository writing into it.
func TestGormModelInsideGormTransaction(t *testing.T) {
	ctx := context.Background()
	db := gormDB(t)
	members := GormMembers.Bind(crudsql.Postgres(pgDB))

	team := Team{Name: "core"}
	if err := db.Create(&team).Error; err != nil {
		t.Fatal(err)
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		txCtx := crud.WithExecutor(ctx, crudsql.From(tx.Statement.ConnPool))

		m := Member{TeamID: team.ID, Name: "ByGorm"}
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		name := "PatchedByRxCrud"
		if _, err := members.Update(txCtx, m.ID, MemberUpdate{Name: &name}); err != nil {
			return err
		}
		var seen Member
		if err := tx.First(&seen, m.ID).Error; err != nil {
			return err
		}
		if seen.Name != "PatchedByRxCrud" {
			t.Errorf("gorm read back %q", seen.Name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := members.Count(ctx); n != 1 {
		t.Fatalf("count = %d", n)
	}
}
