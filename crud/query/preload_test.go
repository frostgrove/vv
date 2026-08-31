package query_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/query"
)

func articleRow(id, authorID int64, title string) []any {
	return []any{id, authorID, title, "body", 0, nil, time.Time{}}
}

func TestPreloadBatchesAndWires(t *testing.T) {
	rec := crudtest.Postgres().Push(

		crudtest.Rows(
			articleRow(1, 10, "first"),
			articleRow(2, 11, "second"),
			articleRow(3, 10, "third"),
		),

		crudtest.Rows(
			[]any{int64(10), "Ann"},
			[]any{int64(11), "Bob"},
		),

		crudtest.Rows(
			[]any{int64(100), int64(1), int64(11), "nice", true},
			[]any{int64(101), int64(1), int64(10), "ok", false},
			[]any{int64(102), int64(3), int64(11), "hm", true},
		),

		crudtest.Rows([]any{int64(10), "Ann"}, []any{int64(11), "Bob"}),
	)

	var request query.Request
	_ = json.Unmarshal([]byte(`{"preload":["author","comments.author"],"unpaged":true}`), &request)
	options, err := request.Compile(Articles.Meta(), exports)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Articles.Bind(rec).GetAll(context.Background(), options...)
	if err != nil {
		t.Fatal(err)
	}

	sql := rec.SQL()
	if len(sql) != 4 {
		t.Fatalf("%d statements, want 4 (page + author + comments + comment authors):\n%s",
			len(sql), strings.Join(sql, "\n"))
	}

	if want := `SELECT "id", "name" FROM "authors" WHERE "id" IN ($1, $2) LIMIT 1001`; crudtest.Normalize(sql[1]) != want {
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

func TestFilteredPreload(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(articleRow(1, 10, "first")),
		crudtest.Rows([]any{int64(100), int64(1), int64(11), "nice", true}),
	)
	var request query.Request
	if err := json.Unmarshal([]byte(`{"preload":[
		{"path":"comments","filter":{"approved":true},"sort":["-body"]}
	],"unpaged":true}`), &request); err != nil {
		t.Fatal(err)
	}
	options, err := request.Compile(Articles.Meta(), exports)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Articles.Bind(rec).GetAll(context.Background(), options...)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "id", "article_id", "author_id", "body", "approved" FROM "comments" ` +
		`WHERE "article_id" IN ($1) AND "approved" = $2 ORDER BY "body" DESC LIMIT 1001`
	if got := crudtest.Normalize(rec.SQL()[1]); got != want {
		t.Fatalf("preload = %s\nwant %s", got, want)
	}
	if len(got[0].Comments) != 1 {
		t.Fatalf("comments = %+v", got[0].Comments)
	}
}

func TestPreloadFilterIsScopedToTheTarget(t *testing.T) {
	var request query.Request
	_ = json.Unmarshal([]byte(`{"preload":[{"path":"comments","filter":{"views":1}}]}`), &request)
	_, err := request.Compile(Articles.Meta(), exports)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("err = %v, want an unknown-field rejection", err)
	}
}

func TestPreloadCannotBePaginated(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(articleRow(1, 10, "first")))
	_, err := Articles.Bind(rec).GetAll(context.Background(),
		crud.PreloadWhere("Comments", crud.Limit(5)))
	if err == nil || !strings.Contains(err.Error(), "cannot be paginated") {
		t.Fatalf("err = %v", err)
	}
}

func TestQueryPreloadRowsAreCappedWithoutPartialRelations(t *testing.T) {
	var request query.Request
	if err := json.Unmarshal([]byte(`{"preload":["comments"]}`), &request); err != nil {
		t.Fatal(err)
	}
	options, err := request.Compile(Articles.Meta(), &query.Config{MaxPreloadRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	rec := crudtest.Postgres().Push(
		crudtest.Rows(articleRow(1, 10, "first")),
		crudtest.Rows(
			[]any{int64(100), int64(1), int64(11), "one", true},
			[]any{int64(101), int64(1), int64(11), "two", true},
		),
	)
	if _, err := Articles.Bind(rec).GetAll(context.Background(), options...); err == nil || !strings.Contains(err.Error(), "preload exceeds") {
		t.Fatalf("err = %v, want a row-cap refusal rather than a partial relation", err)
	}
	if got := rec.Last().SQL; !strings.Contains(got, "LIMIT 2") {
		t.Fatalf("preload SQL = %s, want cap plus one for exact detection", got)
	}
}

func TestNestedQueryPreloadCapsEveryRelationHop(t *testing.T) {
	var request query.Request
	if err := json.Unmarshal([]byte(`{"preload":["comments.author"]}`), &request); err != nil {
		t.Fatal(err)
	}
	options, err := request.Compile(Articles.Meta(), &query.Config{MaxPreloadRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	rec := crudtest.Postgres().Push(
		crudtest.Rows(articleRow(1, 10, "first")),
		crudtest.Rows(
			[]any{int64(100), int64(1), int64(11), "one", true},
			[]any{int64(101), int64(1), int64(12), "two", true},
		),
	)
	if _, err := Articles.Bind(rec).GetAll(context.Background(), options...); err == nil || !strings.Contains(err.Error(), "preload exceeds") {
		t.Fatalf("err = %v, want the intermediate Comments cap to refuse", err)
	}
	if sql := rec.Last().SQL; !strings.Contains(sql, "LIMIT 2") {
		t.Fatalf("intermediate preload SQL = %s, want cap plus one", sql)
	}
}

func TestPreloadBudgetCountsEveryRelationHop(t *testing.T) {
	var request query.Request
	if err := json.Unmarshal([]byte(`{"preload":["comments.author"]}`), &request); err != nil {
		t.Fatal(err)
	}
	_, err := request.Compile(Articles.Meta(), &query.Config{MaxPreloads: 1})
	if err == nil || !strings.Contains(err.Error(), "at most 1 relations") {
		t.Fatalf("err = %v, want the nested preload to consume two relation slots", err)
	}
}

func TestNestedPreloadsRequireEveryHopAndShareTheDepthAndSortBudgets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config *query.Config
		doc    string
		want   string
	}{
		{
			name:   "intermediate hop has no grant",
			config: &query.Config{Preloadable: []string{"Comments.Author"}},
			doc:    `{"preload":["comments.author"]}`,
			want:   "Comments cannot be preloaded",
		},
		{
			name:   "path is deeper than the endpoint budget",
			config: &query.Config{MaxDepth: 1},
			doc:    `{"preload":["comments.author"]}`,
			want:   "deeper",
		},
		{
			name:   "preload sort has the same cap",
			config: &query.Config{MaxSort: 1},
			doc:    `{"preload":[{"path":"comments","sort":["body","approved"]}]}`,
			want:   "at most 1 sort terms",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request query.Request
			if err := json.Unmarshal([]byte(tc.doc), &request); err != nil {
				t.Fatal(err)
			}
			if _, err := request.Compile(Articles.Meta(), tc.config); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
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
