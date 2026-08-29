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
	"github.com/frostgrove/vv/crud/sqlrepo"
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

// Soft deletes are a gorm-layer feature; vv sees the raw table, so the
// tombstone filter is declared once, here, and no query option can widen it.
var (
	GormTeams = sqlrepo.Define[Team, uint, TeamUpdate]("teams",
		sqlrepo.Scope(crud.IsNull("DeletedAt")))
	GormMembers = sqlrepo.Define[Member, uint, MemberUpdate]("members",
		sqlrepo.Scope(crud.IsNull("DeletedAt")))
	GormLabels = sqlrepo.Define[Label, uint, gormstore.LabelUpdate]("labels",
		sqlrepo.Scope(crud.IsNull("DeletedAt")))
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

// A gorm model maps to exactly the columns gorm itself would use.
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

// The mapping self-check every gorm project should copy: vv derives column
// names from the Go field names, gorm's schema parser knows the real ones.
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

// gorm writes, vv reads — including the DSL, relations and preloads.
func TestGormModelThroughVV(t *testing.T) {
	ctx := context.Background()
	database := gormDB(t)
	source := crudsql.Postgres(pgDB)

	teams := specs.Executor(GormTeams.Bind(source))
	members := GormMembers.Bind(source)

	// Everything below is written with gorm's own API.
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

	// …and read back through the DSL, walking the association gorm declared.
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

// The scope keeps gorm's tombstones out of every vv read.
func TestGormSoftDeletesAreInvisible(t *testing.T) {
	ctx := context.Background()
	database := gormDB(t)
	members := GormMembers.Bind(crudsql.Postgres(pgDB))

	team := Team{Name: "core"}
	if err := database.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	ann := Member{TeamID: team.ID, Name: "Ann"}
	bob := Member{TeamID: team.ID, Name: "Bob"}
	if err := database.Create(&[]*Member{&ann, &bob}).Error; err != nil {
		t.Fatal(err)
	}

	// gorm's soft delete: the row stays, deleted_at is set.
	if err := database.Delete(&ann).Error; err != nil {
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
	if err := database.Unscoped().Model(&Member{}).Count(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if raw != 2 {
		t.Fatalf("gorm sees %d rows, want 2", raw)
	}
}

// One transaction, gorm's builder and vv's repository writing into it.
func TestGormModelInsideGormTransaction(t *testing.T) {
	ctx := context.Background()
	database := gormDB(t)
	members := GormMembers.Bind(crudsql.Postgres(pgDB))

	team := Team{Name: "core"}
	if err := database.Create(&team).Error; err != nil {
		t.Fatal(err)
	}

	err := database.Transaction(func(tx *gorm.DB) error {
		txCtx := crud.WithExecutor(ctx, crudsql.From(tx.Statement.ConnPool))

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

// docs/gorm.md §14 opens with a curl that walks a has_many hop out of Team and
// preloads back along it two levels deep. Nothing ran it: only the belongs_to
// direction and the many2many were covered, so the document a reader is most
// likely to copy was the one nothing executed.
func TestTheGormGuidesHeadlineDocumentRuns(t *testing.T) {
	for _, g := range mxGorms(t) {
		t.Run(g.name, func(t *testing.T) {
			ctx := context.Background()
			teams := GormTeams.Bind(g.source)

			goLbl, rust := Label{Slug: "go"}, Label{Slug: "rust"}
			if err := g.database.Create(&[]*Label{&goLbl, &rust}).Error; err != nil {
				t.Fatal(err)
			}
			// core has a member over 30 and a "go" label; ops has neither.
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

			// The document from the guide, character for character.
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

			// Two EXISTS subqueries, ANDed: ops has a matching label but nobody
			// over 30, so exactly one team comes back — and it comes back once,
			// even though two of its labels match.
			if len(got) != 1 || got[0].Name != "core" {
				t.Fatalf("teams = %v, want just core", mxTeamNames(got))
			}
			if len(got[0].Members) != 2 {
				t.Fatalf("members = %d, want both of core's — a relation filter selects parents, not children", len(got[0].Members))
			}
			if want := []string{"go", "rust"}; !eq(mxSlugs(got[0].Labels), want) {
				t.Fatalf("labels = %v, want %v", mxSlugs(got[0].Labels), want)
			}
			// members.team is the second level, and it points back at the team it
			// came from.
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

// docs/gorm.md §16 tells a reader that gorm's hooks, callbacks and defaults do
// not run on vv writes: vv sends one statement to the driver and never
// enters gorm's callback chain. That is a promise about what does *not* happen,
// which is exactly the kind that rots unnoticed — so here it is, from both
// sides, with a hook that leaves a mark on the row.
func TestGormHooksDoNotRunOnVVWrites(t *testing.T) {
	for _, g := range mxGorms(t) {
		t.Run(g.name, func(t *testing.T) {
			ctx := context.Background()
			labels := GormLabels.Bind(g.source)

			// gorm's own Create goes through the callback chain.
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

			// vv's Save is one INSERT. The hook does not run, so the column
			// gets the zero value the model carried — which is the whole point of
			// the warning in the guide.
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

			// And the row on disk agrees — this is not a difference in what was
			// read back afterwards.
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
