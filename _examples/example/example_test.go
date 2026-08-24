package example_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/shardit-io/ordo/_examples/example"
	"github.com/shardit-io/ordo/crud"
	"github.com/shardit-io/ordo/crud/crudtest"
)

var (
	published = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	created   = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

func row(id, author int64, title string, views int, pub any) []any {
	return []any{id, author, title, "body", views, pub, created}
}

// A partial update writes only the columns that actually changed, and the
// gate's scope rides along on every statement — including the UPDATE's own
// WHERE, so a row that leaves the scope between the load and the write is not
// written after all.
func Example_partialUpdate() {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(row(1, 7, "Old", 10, nil)), // the gate loads within scope
		crudtest.Rows(row(1, 7, "Old", 10, nil)), // the repository loads to diff
		crudtest.Rows(row(1, 7, "New", 10, published)),
	)
	repo := example.Open(rec)
	ctx := example.WithAuthor(context.Background(), 7)

	title := "New"
	if _, err := repo.Update(ctx, 1, example.ArticleUpdate{
		Title:       &title,
		Body:        ptr("body"), // unchanged: never reaches the statement
		PublishedAt: crud.Set(published),
	}); err != nil {
		fmt.Println(err)
		return
	}
	for _, s := range rec.SQL() {
		fmt.Println(s)
	}
	// Output:
	// SELECT "id", "author_id", "title", "body", "views", "published_at", "created_at" FROM "articles" WHERE ("author_id" = $1 AND "id" = $2) LIMIT 1
	// SELECT "id", "author_id", "title", "body", "views", "published_at", "created_at" FROM "articles" WHERE ("id" = $1 AND "author_id" = $2) LIMIT 1
	// UPDATE "articles" SET "title" = $1, "published_at" = $2 WHERE ("id" = $3 AND "author_id" = $4) RETURNING "id", "author_id", "title", "body", "views", "published_at", "created_at"
}

// Unpublishing writes NULL; leaving the field out would have written nothing.
func Example_nullVersusAbsent() {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(row(1, 7, "T", 10, published)),
		crudtest.Rows(row(1, 7, "T", 10, published)),
		crudtest.Rows(row(1, 7, "T", 10, nil)),
	)
	repo := example.Open(rec)
	ctx := example.WithAuthor(context.Background(), 7)

	a, err := repo.Unpublish(ctx, 1)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(rec.Last().SQL)
	fmt.Println("arg is nil:", rec.Last().Args[0] == nil)
	fmt.Println("published:", a.PublishedAt.IsSet())
	// Output:
	// UPDATE "articles" SET "published_at" = $1 WHERE ("id" = $2 AND "author_id" = $3) RETURNING "id", "author_id", "title", "body", "views", "published_at", "created_at"
	// arg is nil: true
	// published: false
}

// A specification, a page and the gate's scope all end up in one statement.
func Example_specificationsAndPaging() {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(row(1, 7, "A", 5000, published)),
		crudtest.Rows([]any{int64(41)}),
	)
	repo := example.Open(rec)
	ctx := example.WithAuthor(context.Background(), 7)

	page, err := repo.Feed(ctx, 2)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(rec.SQL()[0])
	fmt.Printf("page %d of %d, total %d, next %v\n",
		page.Page, page.TotalPages, page.Total, page.HasNext)
	// Output:
	// SELECT "id", "author_id", "title", "body", "views", "published_at", "created_at" FROM "articles" WHERE ("author_id" = $1 AND "published_at" IS NOT NULL AND "views" >= $2) ORDER BY "views" DESC, "id" ASC LIMIT 20 OFFSET 20
	// page 2 of 3, total 41, next true
}

// Another author's article is missing, not forbidden.
func Example_scopeHidesForeignRows() {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	repo := example.Open(rec)

	_, err := repo.GetByID(example.WithAuthor(context.Background(), 7), 99)
	fmt.Println(err)
	// Output:
	// crud: not found
}

// Writing into another author's scope is refused before anything reaches the
// database. (A Save that carries an id costs one lookup first, so the gate can
// tell an insert from an overwrite.)
func Example_frozenColumn() {
	rec := crudtest.Postgres()
	repo := example.Open(rec)
	ctx := example.WithAuthor(context.Background(), 7)

	a := example.Article{AuthorID: 8, Title: "theirs"}
	fmt.Println(repo.Save(ctx, &a))
	fmt.Println("statements:", len(rec.Statements()))
	// Output:
	// security: forbidden: create: row belongs to a different AuthorID
	// statements: 0
}

// The DTO is a JSON PATCH body: absent, null and set are three different things.
func Example_patchSemantics() {
	for _, body := range []string{`{}`, `{"title":"T"}`, `{"publishedAt":null}`} {
		var dto example.ArticleUpdate
		if err := json.Unmarshal([]byte(body), &dto); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("%-24s title=%v publishedAt defined=%v null=%v\n",
			body, dto.Title != nil, dto.PublishedAt.IsDefined(), dto.PublishedAt.IsNull())
	}
	// Output:
	// {}                       title=false publishedAt defined=false null=false
	// {"title":"T"}            title=true publishedAt defined=false null=false
	// {"publishedAt":null}     title=false publishedAt defined=true null=true
}

func ptr[T any](v T) *T { return &v }
