package example_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/frostgrove/vv/_examples/example"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
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
	repository := example.Open(rec)
	ctx := example.WithAuthor(context.Background(), 7)

	title := "New"
	if _, err := repository.Update(ctx, 1, example.ArticleUpdate{
		Title:       &title,
		Body:        ptr("body"), // unchanged: never reaches the statement
		PublishedAt: crud.Set(published),
	}); err != nil {
		fmt.Println(err)
		return
	}
	// The long WHERE on the second and third statements is the gate pinning them
	// to the exact row it approved. A scope only says where a row may be; the
	// policy's Inspect may decide from any field, so repeating the scope alone
	// would let a concurrent change turn an approved update into a forbidden
	// one. The key and the scope appear twice because the snapshot names every
	// column, including those two.
	for _, s := range rec.SQL() {
		fmt.Println(s)
	}
	// Output:
	// SELECT "id", "author_id", "title", "body", "views", "published_at", "created_at" FROM "articles" WHERE ("author_id" = $1 AND "id" = $2) LIMIT 1
	// SELECT "id", "author_id", "title", "body", "views", "published_at", "created_at" FROM "articles" WHERE ("id" = $1 AND "author_id" = $2 AND "id" = $3 AND "author_id" = $4 AND "title" = $5 AND "body" = $6 AND "views" = $7 AND "published_at" = $8 AND "created_at" = $9) LIMIT 1
	// UPDATE "articles" SET "title" = $1, "published_at" = $2 WHERE ("id" = $3 AND "author_id" = $4 AND "id" = $5 AND "author_id" = $6 AND "title" = $7 AND "body" = $8 AND "views" = $9 AND "published_at" = $10 AND "created_at" = $11) RETURNING "id", "author_id", "title", "body", "views", "published_at", "created_at"
}

// Unpublishing writes NULL; leaving the field out would have written nothing.
func Example_nullVersusAbsent() {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(row(1, 7, "T", 10, published)),
		crudtest.Rows(row(1, 7, "T", 10, published)),
		crudtest.Rows(row(1, 7, "T", 10, nil)),
	)
	repository := example.Open(rec)
	ctx := example.WithAuthor(context.Background(), 7)

	a, err := repository.Unpublish(ctx, 1)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(rec.Last().SQL)
	fmt.Println("arg is nil:", rec.Last().Args[0] == nil)
	fmt.Println("published:", a.PublishedAt.IsSet())
	// Output:
	// UPDATE "articles" SET "published_at" = $1 WHERE ("id" = $2 AND "author_id" = $3 AND "id" = $4 AND "author_id" = $5 AND "title" = $6 AND "body" = $7 AND "views" = $8 AND "published_at" = $9 AND "created_at" = $10) RETURNING "id", "author_id", "title", "body", "views", "published_at", "created_at"
	// arg is nil: true
	// published: false
}

// A specification, a page and the gate's scope all end up in one statement.
func Example_specificationsAndPaging() {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(row(1, 7, "A", 5000, published)),
		crudtest.Rows([]any{int64(41)}),
	)
	repository := example.Open(rec)
	ctx := example.WithAuthor(context.Background(), 7)

	page, err := repository.Feed(ctx, 2)
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
	repository := example.Open(rec)

	_, err := repository.GetByID(example.WithAuthor(context.Background(), 7), 99)
	fmt.Println(err)
	// Output:
	// crud: not found
}

// Writing into another author's scope is refused before anything reaches the
// database. (A Save that carries an id costs one lookup first, so the gate can
// tell an insert from an overwrite.)
func Example_frozenColumn() {
	rec := crudtest.Postgres()
	repository := example.Open(rec)
	ctx := example.WithAuthor(context.Background(), 7)

	a := example.Article{AuthorID: 8, Title: "theirs"}
	_, err := repository.Save(ctx, &a)
	fmt.Println(err)
	fmt.Println("statements:", len(rec.Statements()))
	// Output:
	// security: forbidden: create: row belongs to a different AuthorID
	// statements: 0
}

// The DTO is a JSON PATCH body: absent, null and set are three different things.
func Example_patchSemantics() {
	for _, body := range []string{`{}`, `{"title":"T"}`, `{"publishedAt":null}`} {
		var dataTransferObject example.ArticleUpdate
		if err := json.Unmarshal([]byte(body), &dataTransferObject); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("%-24s title=%v publishedAt defined=%v null=%v\n",
			body, dataTransferObject.Title != nil, dataTransferObject.PublishedAt.IsDefined(), dataTransferObject.PublishedAt.IsNull())
	}
	// Output:
	// {}                       title=false publishedAt defined=false null=false
	// {"title":"T"}            title=true publishedAt defined=false null=false
	// {"publishedAt":null}     title=false publishedAt defined=true null=true
}

func ptr[T any](v T) *T { return &v }
