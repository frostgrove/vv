package query_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shardit-io/go-rx-crud/crud"
	"github.com/shardit-io/go-rx-crud/crud/crudtest"
	"github.com/shardit-io/go-rx-crud/query"
)

func articleRow(id, authorID int64, title string) []any {
	return []any{id, authorID, title, "body", 0, nil, time.Time{}}
}

// preloads run as one batched statement per relation per level — never one per
// row, and never a join that would multiply the parents.
func TestPreloadBatchesAndWires(t *testing.T) {
	rec := crudtest.Postgres().Push(
		// the page itself
		crudtest.Rows(
			articleRow(1, 10, "first"),
			articleRow(2, 11, "second"),
			articleRow(3, 10, "third"),
		),
		// authors, one statement for all three articles
		crudtest.Rows(
			[]any{int64(10), "Ann"},
			[]any{int64(11), "Bob"},
		),
		// comments, one statement
		crudtest.Rows(
			[]any{int64(100), int64(1), int64(11), "nice", true},
			[]any{int64(101), int64(1), int64(10), "ok", false},
			[]any{int64(102), int64(3), int64(11), "hm", true},
		),
		// the comments' authors, one statement
		crudtest.Rows([]any{int64(10), "Ann"}, []any{int64(11), "Bob"}),
	)

	var req query.Request
	_ = json.Unmarshal([]byte(`{"preload":["author","comments.author"],"unpaged":true}`), &req)
	opts, err := req.Compile(Articles.Meta(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Articles.Bind(rec).GetAll(context.Background(), opts...)
	if err != nil {
		t.Fatal(err)
	}

	sql := rec.SQL()
	if len(sql) != 4 {
		t.Fatalf("%d statements, want 4 (page + author + comments + comment authors):\n%s",
			len(sql), strings.Join(sql, "\n"))
	}
	// Note the deduplicated key list: two articles share author 10.
	if want := `SELECT "id", "name" FROM "authors" WHERE "id" IN ($1, $2)`; crudtest.Normalize(sql[1]) != want {
		t.Fatalf("author preload = %s\nwant %s", sql[1], want)
	}
	if !strings.Contains(sql[2], `WHERE "article_id" IN ($1, $2, $3)`) {
		t.Fatalf("comment preload = %s", sql[2])
	}

	if len(got) != 3 {
		t.Fatalf("%d articles", len(got))
	}
	if got[0].Author == nil || got[0].Author.Name != "Ann" {
		t.Fatalf("article 1 author = %+v", got[0].Author)
	}
	if got[1].Author == nil || got[1].Author.Name != "Bob" {
		t.Fatalf("article 2 author = %+v", got[1].Author)
	}
	if len(got[0].Comments) != 2 || len(got[1].Comments) != 0 || len(got[2].Comments) != 1 {
		t.Fatalf("comment counts = %d/%d/%d", len(got[0].Comments), len(got[1].Comments), len(got[2].Comments))
	}
	// A to-many relation with no rows is an empty slice, not nil, so it renders
	// as [] rather than null.
	if got[1].Comments == nil {
		t.Fatal("an empty relation should still be an empty slice")
	}
	if got[0].Comments[0].Author == nil || got[0].Comments[0].Author.Name != "Bob" {
		t.Fatalf("nested author = %+v", got[0].Comments[0].Author)
	}
}

func TestPreloadManyToMany(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(articleRow(1, 10, "first"), articleRow(2, 10, "second")),
		// the join column comes back first, then the target columns
		crudtest.Rows(
			[]any{int64(1), int64(50), "go"},
			[]any{int64(1), int64(51), "rust"},
			[]any{int64(2), int64(50), "go"},
		),
	)
	got, err := Articles.Bind(rec).GetAll(context.Background(), crud.Preload("Tags"))
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT rxj."article_id", rxt."id", rxt."slug" FROM "tags" AS rxt ` +
		`JOIN "article_tags" AS rxj ON rxj."tag_id" = rxt."id" WHERE rxj."article_id" IN ($1, $2)`
	if got := crudtest.Normalize(rec.SQL()[1]); got != want {
		t.Fatalf("m2m preload = %s\nwant %s", got, want)
	}
	if len(got[0].Tags) != 2 || got[0].Tags[0].Slug != "go" || got[0].Tags[1].Slug != "rust" {
		t.Fatalf("article 1 tags = %+v", got[0].Tags)
	}
	if len(got[1].Tags) != 1 || got[1].Tags[0].Slug != "go" {
		t.Fatalf("article 2 tags = %+v", got[1].Tags)
	}
}

// A preload can carry its own filter and sort, validated against the related
// model rather than the root.
func TestFilteredPreload(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(articleRow(1, 10, "first")),
		crudtest.Rows([]any{int64(100), int64(1), int64(11), "nice", true}),
	)
	var req query.Request
	if err := json.Unmarshal([]byte(`{"preload":[
		{"path":"comments","filter":{"approved":true},"sort":["-body"]}
	],"unpaged":true}`), &req); err != nil {
		t.Fatal(err)
	}
	opts, err := req.Compile(Articles.Meta(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Articles.Bind(rec).GetAll(context.Background(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "id", "article_id", "author_id", "body", "approved" FROM "comments" ` +
		`WHERE "article_id" IN ($1) AND "approved" = $2 ORDER BY "body" DESC`
	if got := crudtest.Normalize(rec.SQL()[1]); got != want {
		t.Fatalf("preload = %s\nwant %s", got, want)
	}
	if len(got[0].Comments) != 1 {
		t.Fatalf("comments = %+v", got[0].Comments)
	}
}

// A preload filter is checked against the related model, so a root-only field
// is a rejection rather than a confusing SQL error.
func TestPreloadFilterIsScopedToTheTarget(t *testing.T) {
	var req query.Request
	_ = json.Unmarshal([]byte(`{"preload":[{"path":"comments","filter":{"views":1}}]}`), &req)
	_, err := req.Compile(Articles.Meta(), nil)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("err = %v, want an unknown-field rejection", err)
	}
}

// Pagination inside a preload would truncate some parents' children and not
// others, so it is refused rather than silently wrong.
func TestPreloadCannotBePaginated(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(articleRow(1, 10, "first")))
	_, err := Articles.Bind(rec).GetAll(context.Background(),
		crud.PreloadWhere("Comments", crud.Limit(5)))
	if err == nil || !strings.Contains(err.Error(), "cannot be paginated") {
		t.Fatalf("err = %v", err)
	}
}

func TestPreloadDepthIsCapped(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(articleRow(1, 10, "x")))
	_, err := Articles.Bind(rec).GetAll(context.Background(),
		crud.Preload("Comments.Author.Nope.Deeper.Deeper.Deeper"))
	if err == nil {
		t.Fatal("an over-deep preload should be refused")
	}
}

// Selecting a narrow projection still brings back the columns a preload needs
// to join on.
func TestProjectionKeepsPreloadKeys(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows([]any{int64(1), "first", int64(10)}),
		crudtest.Rows([]any{int64(10), "Ann"}),
	)
	got, err := Articles.Bind(rec).GetAll(context.Background(),
		crud.Select("Title"), crud.Preload("Author"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id", "title", "author_id" FROM "articles"`; !strings.HasPrefix(rec.SQL()[0], want) {
		t.Fatalf("projection = %s\nwant it to start with %s", rec.SQL()[0], want)
	}
	if got[0].Author == nil || got[0].Author.Name != "Ann" {
		t.Fatalf("author = %+v", got[0].Author)
	}
}
