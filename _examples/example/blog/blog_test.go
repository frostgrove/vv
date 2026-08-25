package blog_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shardit-io/vv/_examples/example/blog"
	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/crudtest"
	"github.com/shardit-io/vv/repo/basic"
	"github.com/shardit-io/vv/repo/decorators/specs"
)

var Articles = basic.Define[blog.Article, int64, blog.ArticleUpdate]("articles")

func sqlOf(t *testing.T, opts ...crud.Option) string {
	t.Helper()
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := Articles.Bind(rec).GetAll(context.Background(), opts...); err != nil {
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

// The generated metamodel walks relations, and the compiler checks the value
// type at every hop.
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
				`AND rx1."name" LIKE $2))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := where(sqlOf(t, specs.As(tc.spec))); got != tc.want {
				t.Fatalf("where = %s\nwant  = %s", got, tc.want)
			}
		})
	}
}

// Sorting by a related column works from the metamodel too.
func TestGeneratedMetamodelSorts(t *testing.T) {
	sql := sqlOf(t, crud.OrderBy(blog.Article_.Author.Name.Desc()), crud.Limit(5))
	want := `ORDER BY (SELECT rx1."name" FROM "authors" AS rx1 WHERE rx1."id" = "articles"."author_id" LIMIT 1) DESC`
	if !strings.Contains(sql, want) {
		t.Fatalf("sql = %s\nwant it to contain %s", sql, want)
	}
}

// The generated DTO carries the three-state semantics: absent, null, value.
func TestGeneratedUpdateDTO(t *testing.T) {
	var dto blog.ArticleUpdate
	if err := json.Unmarshal([]byte(`{"title":"new","publishedAt":null}`), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Title == nil || *dto.Title != "new" {
		t.Fatalf("title = %v", dto.Title)
	}
	if !dto.PublishedAt.IsNull() {
		t.Fatalf("publishedAt = %v, want an explicit null", dto.PublishedAt)
	}
	if dto.Rating.IsDefined() {
		t.Fatalf("rating = %v, want undefined", dto.Rating)
	}

	// The columns a client must not set are simply not in the DTO — which the
	// update planner would have rejected anyway, but at start-up rather than
	// per request.
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

// A nullable column becomes Opt, a plain one a pointer — the whole reason the
// DTO is generated rather than hand-copied.
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
	// id, author_id, title, body, views, rating, published_at, tenant_id, created_at
	return []any{int64(1), int64(7), "old", "body", 3, 4.5, nil, int64(1), nil}
}

// generateDirective is the line model.go carries, checked verbatim: change it
// without changing the command below and the staleness check would quietly be
// measuring a command nobody runs.
const generateDirective = "//go:generate go run github.com/shardit-io/vv/cmd/vv -adapter"

// The checked-in generated file must match what the generator produces now.
func TestGeneratedFileIsUpToDate(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	dir := t.TempDir()
	src, err := os.ReadFile("model.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}

	assertDirective(t, "model.go", generateDirective)

	cmd := exec.Command("go", "run", "github.com/shardit-io/vv/cmd/vv", "-dir", dir, "-adapter")
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
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(src), "\n") {
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
