//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/frostgrove/vv/test/gormstore"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/query"
)

type (
	Team   = gormstore.Team
	Member = gormstore.Member
	Label  = gormstore.Label

	TeamUpdate   = gormstore.TeamUpdate
	MemberUpdate = gormstore.MemberUpdate
)

var (
	GormTeams   = gormstore.TeamRepository
	GormMembers = gormstore.MemberRepository
	GormLabels  = gormstore.LabelRepository
)

func gormDB(t *testing.T) *gorm.DB {
	t.Helper()
	database := openGorm(t, gormPGDialector())
	if err := database.AutoMigrate(&Team{}, &Member{}, &Label{}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"team_labels", "members", "labels", "teams"} {
		if err := database.Exec("DELETE FROM " + table).Error; err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func TestGormModelIsAVVModel(t *testing.T) {
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

	if s.PK.Name != "ID" || !s.PK.Auto {
		t.Fatalf("pk = %+v", s.PK)
	}
	if s.Field("Team") != nil {
		t.Fatal("an association must not become a column")
	}
	if s.Relation("Team") == nil {
		t.Fatal("the rel tag should have made Team a relation")
	}

	if f := s.Field("DeletedAt"); f == nil || f.Column != "deleted_at" {
		t.Fatalf("deleted_at = %+v", f)
	}
}

func TestGormMappingMatchesGorm(t *testing.T) {
	database := gormDB(t)
	s, err := crud.SchemaOf[Member]()
	if err != nil {
		t.Fatal(err)
	}
	stmt := &gorm.Statement{DB: database}
	if err := stmt.Parse(&Member{}); err != nil {
		t.Fatal(err)
	}
	for _, f := range s.Fields {
		if stmt.Schema.LookUpField(f.Column) == nil {
			t.Errorf("github.com/frostgrove/vv maps %q, gorm has no such column", f.Column)
		}
	}
	if GormMembers.Meta().Table != stmt.Schema.Table {
		t.Fatalf("table = %q, gorm says %q", GormMembers.Meta().Table, stmt.Schema.Table)
	}
}

func TestGormModelThroughVV(t *testing.T) {
	ctx := context.Background()
	database := gormDB(t)
	source := crudsql.Postgres(pgDB)

	teams := specs.Executor(GormTeams.Bind(source))
	members := GormMembers.Bind(source)

	go1 := Label{Slug: "go"}
	rust := Label{Slug: "rust"}
	if err := database.Create(&[]*Label{&go1, &rust}).Error; err != nil {
		t.Fatal(err)
	}
	core := Team{Name: "core", Labels: []Label{go1, rust}}
	ops := Team{Name: "ops", Labels: []Label{rust}}
	if err := database.Create(&[]*Team{&core, &ops}).Error; err != nil {
		t.Fatal(err)
	}
	age := 31
	if err := database.Create(&[]*Member{
		{TeamID: core.ID, Name: "Ann", Age: &age},
		{TeamID: core.ID, Name: "Bob"},
		{TeamID: ops.ID, Name: "Cid"},
	}).Error; err != nil {
		t.Fatal(err)
	}

	var request query.Request
	if err := json.Unmarshal([]byte(`{
		"filter":  {"team.name": "core"},
		"preload": ["team"],
		"sort":    ["name"],
		"unpaged": true
	}`), &request); err != nil {
		t.Fatal(err)
	}
	options, err := request.Compile(GormMembers.Meta(), unpagedOK)
	if err != nil {
		t.Fatal(err)
	}
	got, err := members.GetAll(ctx, options...)
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

func TestGormSoftDeletesAreInvisible(t *testing.T) {
	ctx := context.Background()
	database := gormDB(t)
	source := crudsql.Postgres(pgDB)
	members := GormMembers.Bind(source)

	team := Team{Name: "core"}
	if err := database.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	ann := Member{TeamID: team.ID, Name: "Ann"}
	bob := Member{TeamID: team.ID, Name: "Bob"}
	if err := database.Create(&[]*Member{&ann, &bob}).Error; err != nil {
		t.Fatal(err)
	}

	if err := database.Delete(&ann).Error; err != nil {
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
		t.Fatalf("the scope was widened: %+v", all)
	}

	var raw int64
	if err := database.Unscoped().Model(&Member{}).Count(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if raw != 2 {
		t.Fatalf("gorm sees %d rows, want 2", raw)
	}
}

func TestGormModelInsideGormTransaction(t *testing.T) {
	ctx := context.Background()
	database := gormDB(t)
	source := crudsql.Postgres(pgDB)
	members := GormMembers.Bind(source)

	team := Team{Name: "core"}
	if err := database.Create(&team).Error; err != nil {
		t.Fatal(err)
	}

	err := database.Transaction(func(tx *gorm.DB) error {
		txCtx := source.BindExecutor(ctx, tx.Statement.ConnPool)

		m := Member{TeamID: team.ID, Name: "ByGorm"}
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		name := "PatchedByVV"
		if _, err := members.Update(txCtx, m.ID, MemberUpdate{Name: &name}); err != nil {
			return err
		}
		var seen Member
		if err := tx.First(&seen, m.ID).Error; err != nil {
			return err
		}
		if seen.Name != "PatchedByVV" {
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

func TestTheGormGuidesHeadlineDocumentRuns(t *testing.T) {
	for _, g := range mxGorms(t) {
		t.Run(g.name, func(t *testing.T) {
			ctx := context.Background()
			teams := GormTeams.Bind(g.source)

			goLbl, rust := Label{Slug: "go"}, Label{Slug: "rust"}
			if err := g.database.Create(&[]*Label{&goLbl, &rust}).Error; err != nil {
				t.Fatal(err)
			}

			core := Team{Name: "core", Labels: []Label{goLbl, rust}}
			ops := Team{Name: "ops", Labels: []Label{rust}}
			if err := g.database.Create(&[]*Team{&core, &ops}).Error; err != nil {
				t.Fatal(err)
			}
			senior, junior := 31, 22
			if err := g.database.Create(&[]*Member{
				{TeamID: core.ID, Name: "Ann", Age: &senior},
				{TeamID: core.ID, Name: "Bob", Age: &junior},
				{TeamID: ops.ID, Name: "Cid", Age: &junior},
			}).Error; err != nil {
				t.Fatal(err)
			}

			var request query.Request
			if err := json.Unmarshal([]byte(`{
				"filter":  {"labels.slug": {"in": ["go","rust"]}, "members.age": {"gte": 30}},
				"preload": ["members.team", "labels"],
				"sort":    ["-createdAt", "name"]
			}`), &request); err != nil {
				t.Fatal(err)
			}
			request.Unpaged = true
			options, err := request.Compile(GormTeams.Meta(), unpagedOK)
			if err != nil {
				t.Fatal(err)
			}
			got, err := teams.GetAll(ctx, options...)
			if err != nil {
				t.Fatal(err)
			}

			if len(got) != 1 || got[0].Name != "core" {
				t.Fatalf("teams = %v, want just core", mxTeamNames(got))
			}
			if len(got[0].Members) != 2 {
				t.Fatalf("members = %d, want both of core's — a relation filter selects parents, not children", len(got[0].Members))
			}
			if want := []string{"go", "rust"}; !eq(mxSlugs(got[0].Labels), want) {
				t.Fatalf("labels = %v, want %v", mxSlugs(got[0].Labels), want)
			}

			for _, m := range got[0].Members {
				if m.Team == nil {
					t.Fatalf("member %q has no team: the nested preload did not run", m.Name)
				}
				if m.Team.Name != "core" {
					t.Fatalf("member %q belongs to %q", m.Name, m.Team.Name)
				}
			}
		})
	}
}

func mxTeamNames(teams []Team) []string {
	out := make([]string, len(teams))
	for i, t := range teams {
		out[i] = t.Name
	}
	return out
}

func TestGormHooksDoNotRunOnVVWrites(t *testing.T) {
	for _, g := range mxGorms(t) {
		t.Run(g.name, func(t *testing.T) {
			ctx := context.Background()
			labels := GormLabels.Bind(g.source)

			before := gormstore.LabelCreations.Load()
			viaGorm := Label{}
			if err := g.database.Create(&viaGorm).Error; err != nil {
				t.Fatal(err)
			}
			if gormstore.LabelCreations.Load() != before+1 {
				t.Fatal("gorm's own Create did not fire the hook, so this test cannot tell anything apart")
			}
			if viaGorm.Slug != "defaulted by the hook" {
				t.Fatalf("slug = %q, want the hook's default", viaGorm.Slug)
			}

			before = gormstore.LabelCreations.Load()
			viaVV := Label{}
			if stored, err := labels.Save(ctx, &viaVV); err != nil {
				t.Fatal(err)
			} else {
				viaVV = stored
			}
			if n := gormstore.LabelCreations.Load(); n != before {
				t.Fatalf("the hook fired %d time(s) on an vv write; docs/gorm.md §16 says it does not", n-before)
			}
			if viaVV.Slug != "" {
				t.Fatalf("slug = %q: a gorm-side default reached an vv write", viaVV.Slug)
			}

			stored, err := labels.GetByID(ctx, viaVV.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Slug != "" {
				t.Fatalf("the stored slug is %q", stored.Slug)
			}
		})
	}
}
