package blog_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/_examples/example/blog"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

var Articles = sqlrepo.Define[blog.Article, int64, blog.ArticleUpdate]("articles")

func sqlOf(t *testing.T, options ...crud.Option) string {
	t.Helper()
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := Articles.Bind(rec).GetAll(context.Background(), options...); err != nil {
		t.Fatal(err)
	}
	return crudtest.Normalize(rec.Last().SQL)
}

func where(sql string) string {
	_, clause, _ := strings.Cut(sql, " WHERE ")
	for _, tail := range []string{" ORDER BY ", " LIMIT ", " OFFSET "} {
		clause, _, _ = strings.Cut(clause, tail)
	}
	return clause
}

func TestGeneratedMetamodelReachesThroughRelations(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec specs.Specification[blog.Article]
		want string
	}{
		{"own column", blog.Article_.Views.Gte(100), `"views" >= $1`},
		{"belongs_to", blog.Article_.Author.Name.Eq("Ann"),
			`EXISTS (SELECT 1 FROM "authors" AS rx1 WHERE rx1."id" = "articles"."author_id" AND rx1."name" = $1)`},
		{"has_many", blog.Article_.Comments.Approved.Eq(true),
			`EXISTS (SELECT 1 FROM "comments" AS rx1 WHERE rx1."article_id" = "articles"."id" AND rx1."approved" = $1)`},
		{"many_to_many", blog.Article_.Tags.Slug.In("go", "rust"),
			`EXISTS (SELECT 1 FROM "tags" AS rx1 JOIN "article_tags" AS rx2 ON rx2."tag_id" = rx1."id" ` +
				`WHERE rx2."article_id" = "articles"."id" AND rx1."slug" IN ($1, $2))`},

		{"composed", specs.Where(blog.Article_.Views.Gte(100)).And(blog.Article_.Author.Name.Contains("an")),
			`("views" >= $1 AND EXISTS (SELECT 1 FROM "authors" AS rx1 WHERE rx1."id" = "articles"."author_id" ` +
				`AND rx1."name" LIKE $2 ESCAPE '\'))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := where(sqlOf(t, specs.As(tc.spec))); got != tc.want {
				t.Fatalf("where = %s\nwant  = %s", got, tc.want)
			}
		})
	}
}

func TestGeneratedMetamodelSorts(t *testing.T) {
	sql := sqlOf(t, crud.OrderBy(blog.Article_.Author.Name.Desc()), crud.Limit(5))
	want := `ORDER BY (SELECT rx1."name" FROM "authors" AS rx1 WHERE rx1."id" = "articles"."author_id" LIMIT 1) DESC`
	if !strings.Contains(sql, want) {
		t.Fatalf("sql = %s\nwant it to contain %s", sql, want)
	}
}

func TestGeneratedUpdateDTO(t *testing.T) {
	var dataTransferObject blog.ArticleUpdate
	if err := json.Unmarshal([]byte(`{"title":"new","publishedAt":null}`), &dataTransferObject); err != nil {
		t.Fatal(err)
	}
	if dataTransferObject.Title == nil || *dataTransferObject.Title != "new" {
		t.Fatalf("title = %v", dataTransferObject.Title)
	}
	if !dataTransferObject.PublishedAt.IsNull() {
		t.Fatalf("publishedAt = %v, want an explicit null", dataTransferObject.PublishedAt)
	}
	if dataTransferObject.Rating.IsDefined() {
		t.Fatalf("rating = %v, want undefined", dataTransferObject.Rating)
	}

	plan, err := crud.PlanFor[blog.ArticleUpdate](crud.MustSchemaOf[blog.Article]())
	if err != nil {
		t.Fatalf("the generated DTO does not fit the model: %v", err)
	}
	for _, f := range plan.Fields {
		switch f.Target.Name {
		case "ID", "CreatedAt", "TenantID", "Secret":
			t.Fatalf("%s should not be updatable", f.Target.Name)
		}
	}
}

func TestGeneratedDTOTypesFollowNullability(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(articleRow()),
		crudtest.Rows(articleRow()),
	)
	title := "renamed"
	if _, err := Articles.Bind(rec).Update(context.Background(), 1, blog.ArticleUpdate{
		Title:  &title,
		Rating: crud.Null[float64](),
	}); err != nil {
		t.Fatal(err)
	}
	set, _, _ := strings.Cut(rec.Last().SQL, " WHERE ")
	if !strings.Contains(set, `"title"`) || !strings.Contains(set, `"rating"`) {
		t.Fatalf("update = %s", set)
	}
	if strings.Contains(set, `"views"`) {
		t.Fatalf("an undefined field leaked into the statement: %s", set)
	}
}

func articleRow() []any {
	return []any{int64(1), int64(7), "old", "body", 3, 4.5, nil, int64(1), time.Time{}}
}

const generateDirective = "//go:generate go run github.com/frostgrove/vv/cmd/vv -adapter"

func TestGeneratedFileIsUpToDate(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	dir := t.TempDir()
	source, err := os.ReadFile("model.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.go"), source, 0o644); err != nil {
		t.Fatal(err)
	}

	assertDirective(t, "model.go", generateDirective)

	cmd := exec.Command("go", "run", "github.com/frostgrove/vv/cmd/vv", "-dir", dir, "-adapter")
	cmd.Dir = mustRepoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("regenerating: %v\n%s", err, out)
	}
	fresh, err := os.ReadFile(filepath.Join(dir, "vv_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile("vv_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if string(fresh) != string(current) {
		t.Fatal("vv_gen.go is stale; run `go generate ./example/blog`")
	}
}

func assertDirective(t *testing.T, file, want string) {
	t.Helper()
	source, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(source), "\n") {
		if strings.HasPrefix(line, "//go:generate") {
			if strings.TrimSpace(line) != want {
				t.Fatalf("%s carries\n  %s\nbut this test regenerates with\n  %s", file, strings.TrimSpace(line), want)
			}
			return
		}
	}
	t.Fatalf("no //go:generate line in %s", file)
}

func mustRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
