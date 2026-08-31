package crud_test

import (
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
)

type core028QualifiedTag struct {
	ID   int64  `db:"id,pk,auto"`
	Name string `db:"name"`
}

type core028QualifiedArticle struct {
	ID int64 `db:"id,pk,auto"`

	Tags []core028QualifiedTag `rel:"many_to_many,schema=catalog,table=tags,join=article_tags,joinSchema=content,joinFK=article_id,joinRef=tag_id"`
}

type core028BadRelationTarget struct {
	ID int64 `db:"id,pk,auto"`
}

type core028BadDottedRelation struct {
	ID       int64                     `db:"id,pk,auto"`
	TargetID int64                     `db:"target_id"`
	Target   *core028BadRelationTarget `rel:"belongs_to,fk=TargetID,table=analytics.events"`
}

type core028MissingRelationTable struct {
	ID       int64                     `db:"id,pk,auto"`
	TargetID int64                     `db:"target_id"`
	Target   *core028BadRelationTarget `rel:"belongs_to,fk=TargetID,schema=analytics"`
}

type core028BadDottedJoin struct {
	ID      int64                      `db:"id,pk,auto"`
	Targets []core028BadRelationTarget `rel:"many_to_many,join=analytics.owner_targets"`
}

func TestQualifiedRelationAndJoinTablesRenderEveryComponent(t *testing.T) {
	m, err := crud.NewMetaInSchema[core028QualifiedArticle]("content", "articles")
	if err != nil {
		t.Fatal(err)
	}
	rel := m.Relation("Tags")
	if rel == nil {
		t.Fatal("Tags relation was not declared")
	}
	target, _, _, err := rel.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if target.TableReference() != (crud.TableRef{Schema: "catalog", Name: "tags"}) {
		t.Fatalf("target ref = %#v", target.TableReference())
	}
	if rel.JoinTableReference() != (crud.TableRef{Schema: "content", Name: "article_tags"}) {
		t.Fatalf("join ref = %#v", rel.JoinTableReference())
	}

	joinCopy := rel.JoinTableReference()
	joinCopy.Schema = "retargeted"
	rel.JoinTable = "retargeted"
	target.Table = "retargeted"

	b := crud.NewSQL(crud.Postgres{}, m).
		Raw("SELECT ").Columns(m.Fields).Raw(" FROM ").Table().
		Where(crud.Eq("Tags.Name", "go"))
	q, _, err := b.Done()
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "id" FROM "content"."articles" WHERE ` +
		`EXISTS (SELECT 1 FROM "catalog"."tags" AS rx1 ` +
		`JOIN "content"."article_tags" AS rx2 ON rx2."tag_id" = rx1."id" ` +
		`WHERE rx2."article_id" = "content"."articles"."id" AND rx1."name" = $1)`
	if q != want {
		t.Fatalf("SQL = %s\nwant %s", q, want)
	}
}

func TestQualifiedManyToManyPreloadUsesTheSameReferences(t *testing.T) {
	m, err := crud.NewMetaInSchema[core028QualifiedArticle]("content", "articles")
	if err != nil {
		t.Fatal(err)
	}
	rec := crudtest.Postgres().Push(crudtest.Rows(
		[]any{int64(1), int64(2), "go"},
	))
	articles := []core028QualifiedArticle{{ID: 1}}
	runPreloads(t, rec, m, articles, specs("Tags")...)

	want := `SELECT rxj."article_id", rxt."id", rxt."name" FROM "catalog"."tags" AS rxt ` +
		`JOIN "content"."article_tags" AS rxj ON rxj."tag_id" = rxt."id" ` +
		`WHERE rxj."article_id" IN ($1)`
	if got := crudtest.Normalize(rec.Last().SQL); got != want {
		t.Fatalf("SQL = %s\nwant %s", got, want)
	}
	if len(articles[0].Tags) != 1 || articles[0].Tags[0].Name != "go" {
		t.Fatalf("preloaded tags = %+v", articles[0].Tags)
	}
}

func TestRelationTagsRefuseAmbiguousDottedStringsAtSchemaBuild(t *testing.T) {
	tests := []struct {
		name  string
		build func() error
		want  string
	}{
		{"target", schemaErrOf[core028BadDottedRelation](), "separate components"},
		{"missing target component", schemaErrOf[core028MissingRelationTable](), "needs an explicit table"},
		{"join", schemaErrOf[core028BadDottedJoin](), "separate components"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.build()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("schema error = %v, want %q", err, test.want)
			}
		})
	}
}
