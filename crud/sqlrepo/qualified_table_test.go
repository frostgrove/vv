package sqlrepo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

type core028Event struct {
	ID    int64  `db:"id,pk,auto"`
	Label string `db:"label"`
}

type core028EventUpdate struct {
	Label *string
}

type core028Refusal struct {
	ID int64 `db:"id,pk,auto"`
}

type core028Node struct {
	ID       int64 `db:"id,pk,auto"`
	ParentID int64 `db:"parent_id"`

	Parent          *core028Node `rel:"belongs_to,fk=ParentID"`
	CanonicalParent *core028Node `rel:"belongs_to,fk=ParentID,schema=live,table=nodes"`
}

func TestDefineInSchemaRendersAcrossEverySupportedQualifierMeaning(t *testing.T) {
	bp, err := sqlrepo.TryDefineInSchema[core028Event, int64, core028EventUpdate]("analytics", "events")
	if err != nil {
		t.Fatal(err)
	}
	if got := bp.Meta().Table; got != "analytics.events" {
		t.Fatalf("diagnostic table = %q", got)
	}

	tests := []struct {
		name string
		d    crud.Dialect
		want string
	}{
		{"postgres schema", crud.Postgres{}, `SELECT "id", "label" FROM "analytics"."events"`},
		{"mysql database", crud.MySQL{}, "SELECT `id`, `label` FROM `analytics`.`events`"},
		{"sqlite attached database", crud.SQLite{}, `SELECT "id", "label" FROM "analytics"."events"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := crudtest.New(test.d).Push(crudtest.Rows())
			if _, err := bp.Bind(rec).GetAll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := rec.Last().SQL; got != test.want {
				t.Fatalf("SQL = %s, want %s", got, test.want)
			}
		})
	}
}

func TestDottedDefineFailsBeforePublishingAndNamesTheStructuredPath(t *testing.T) {
	if _, err := sqlrepo.TryDefine[core028Refusal, int64, struct{}]("analytics.events"); err == nil ||
		!strings.Contains(err.Error(), "DefineInSchema") {
		t.Fatalf("dotted Define error = %v", err)
	}
	if _, err := sqlrepo.TryDefineInSchema[core028Refusal, int64, struct{}]("analytics", "events"); err != nil {
		t.Fatalf("failed dotted declaration published state: %v", err)
	}
	if _, err := sqlrepo.TryDefineInSchema[core028Event, int64, core028EventUpdate]("", "events"); err == nil {
		t.Fatal("DefineInSchema accepted an empty qualifier")
	}
}

func TestQualifiedIndependentAndExplicitRelationTablesStayBranchLocal(t *testing.T) {
	if _, err := sqlrepo.TryDefineInSchema[core028Node, int64, struct{}]("live", "nodes"); err != nil {
		t.Fatal(err)
	}
	archive, err := sqlrepo.TryDefineInSchema[core028Node, int64, struct{}]("archive", "nodes", sqlrepo.IndependentTable())
	if err != nil {
		t.Fatal(err)
	}
	rec := crudtest.Postgres().Push(crudtest.Rows(), crudtest.Rows())
	repo := archive.Bind(rec)

	if _, err := repo.GetAll(context.Background(), crud.Where(crud.Eq("Parent.ID", int64(7)))); err != nil {
		t.Fatal(err)
	}
	want := `SELECT "id", "parent_id" FROM "archive"."nodes" WHERE ` +
		`EXISTS (SELECT 1 FROM "archive"."nodes" AS rx1 ` +
		`WHERE rx1."id" = "archive"."nodes"."parent_id" AND rx1."id" = $1)`
	if got := rec.Statements()[0].SQL; got != want {
		t.Fatalf("branch-local SQL = %s\nwant %s", got, want)
	}

	if _, err := repo.GetAll(context.Background(), crud.Where(crud.Eq("CanonicalParent.Parent.ID", int64(8)))); err != nil {
		t.Fatal(err)
	}
	want = `SELECT "id", "parent_id" FROM "archive"."nodes" WHERE ` +
		`EXISTS (SELECT 1 FROM "live"."nodes" AS rx1 ` +
		`WHERE rx1."id" = "archive"."nodes"."parent_id" AND ` +
		`EXISTS (SELECT 1 FROM "live"."nodes" AS rx2 ` +
		`WHERE rx2."id" = rx1."parent_id" AND rx2."id" = $1))`
	if got := rec.Statements()[1].SQL; got != want {
		t.Fatalf("explicit-branch SQL = %s\nwant %s", got, want)
	}
}
