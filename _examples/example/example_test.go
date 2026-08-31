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

func Example_partialUpdate() {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(row(1, 7, "Old", 10, nil)),
		crudtest.Rows(row(1, 7, "Old", 10, nil)),
		crudtest.Rows(row(1, 7, "New", 10, published)),
	)
	repository := example.Open(rec)
	ctx := example.WithAuthor(context.Background(), 7)

	title := "New"
	if _, err := repository.Update(ctx, 1, example.ArticleUpdate{
		Title:       &title,
		Body:        ptr("body"),
		PublishedAt: crud.Set(published),
	}); err != nil {
		fmt.Println(err)
		return
	}

	for _, s := range rec.SQL() {
		fmt.Println(s)
	}

}

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

}

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

}

func Example_scopeHidesForeignRows() {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	repository := example.Open(rec)

	_, err := repository.GetByID(example.WithAuthor(context.Background(), 7), 99)
	fmt.Println(err)

}

func Example_frozenColumn() {
	rec := crudtest.Postgres()
	repository := example.Open(rec)
	ctx := example.WithAuthor(context.Background(), 7)

	a := example.Article{AuthorID: 8, Title: "theirs"}
	_, err := repository.Save(ctx, &a)
	fmt.Println(err)
	fmt.Println("statements:", len(rec.Statements()))

}

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

}

func ptr[T any](v T) *T { return &v }
