//go:build integration

package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"rx-crud/adapter/crudsql"
	"rx-crud/crud"
	"rx-crud/query"
)

// blog is one database's worth of bound repositories.
type blog struct {
	name     string
	db       *sql.DB
	src      crud.Source
	articles crud.Repo[Article, int64, ArticleUpdate]
	authors  crud.Repo[Author, int64, struct{}]
	comments crud.Repo[Comment, int64, CommentUpdate]
	tags     crud.Repo[Tag, int64, struct{}]
}

func blogs(t *testing.T) []blog {
	t.Helper()
	out := []blog{
		{name: "postgres", db: pgDB, src: crudsql.Postgres(pgDB)},
		{name: "mysql", db: myDB, src: crudsql.MySQL(myDB)},
	}
	for i := range out {
		out[i].articles = Articles.Bind(out[i].src)
		out[i].authors = Authors.Bind(out[i].src)
		out[i].comments = Comments.Bind(out[i].src)
		out[i].tags = Tags.Bind(out[i].src)
	}
	return out
}

// seedBlog builds a small, fully-linked graph:
//
//	Ann   -> "go generics" (100 views, published) [go, rust]  + 2 comments
//	Ann   -> "draft"       (  1 view,  unpublished)
//	Bob   -> "rust traits" ( 50 views, published) [rust]      + 1 comment
func seedBlog(t *testing.T, b blog) (ann, bob Author, generics, draft, traits Article) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{"article_tags", "comments", "articles", "authors", "tags"} {
		if _, err := b.db.Exec("DELETE FROM " + stmt); err != nil {
			t.Fatalf("%s: reset %s: %v", b.name, stmt, err)
		}
	}

	ann = Author{Name: "Ann"}
	bob = Author{Name: "Bob"}
	for _, a := range []*Author{&ann, &bob} {
		if err := b.authors.Save(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC().Truncate(time.Second)
	generics = Article{AuthorID: ann.ID, Title: "go generics", Body: "about generics", Views: 100, PublishedAt: crud.Set(now)}
	draft = Article{AuthorID: ann.ID, Title: "draft", Body: "wip", Views: 1}
	traits = Article{AuthorID: bob.ID, Title: "rust traits", Body: "about traits", Views: 50, PublishedAt: crud.Set(now)}
	for _, a := range []*Article{&generics, &draft, &traits} {
		if err := b.articles.Save(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	goTag, rustTag := Tag{Slug: "go"}, Tag{Slug: "rust"}
	for _, tg := range []*Tag{&goTag, &rustTag} {
		if err := b.tags.Save(ctx, tg); err != nil {
			t.Fatal(err)
		}
	}
	for _, link := range [][2]int64{
		{generics.ID, goTag.ID}, {generics.ID, rustTag.ID}, {traits.ID, rustTag.ID},
	} {
		if _, err := b.src.Exec(ctx,
			insertPair(b.name, "article_tags", "article_id", "tag_id"), link[0], link[1]); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []*Comment{
		{ArticleID: generics.ID, AuthorID: bob.ID, Body: "nice write-up", Approved: true},
		{ArticleID: generics.ID, AuthorID: ann.ID, Body: "thanks", Approved: false},
		{ArticleID: traits.ID, AuthorID: ann.ID, Body: "clear", Approved: true},
	} {
		if err := b.comments.Save(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	return ann, bob, generics, draft, traits
}

func insertPair(dialect, table, a, bcol string) string {
	if dialect == "mysql" {
		return fmt.Sprintf("INSERT INTO %s (%s, %s) VALUES (?, ?)", table, a, bcol)
	}
	return fmt.Sprintf("INSERT INTO %s (%s, %s) VALUES ($1, $2)", table, a, bcol)
}

func titles(items []Article) []string {
	out := make([]string, len(items))
	for i, a := range items {
		out[i] = a.Title
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Filtering across a relation runs as a correlated EXISTS, so a to-many match
// never duplicates a parent row and never inflates the count.
func TestNestedFiltersAgainstDatabases(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedBlog(t, b)

			for _, tc := range []struct {
				name string
				doc  string
				want []string
			}{
				{"belongs_to", `{"filter":{"author.name":"Ann"},"sort":["title"]}`,
					[]string{"draft", "go generics"}},
				{"has_many", `{"filter":{"comments.approved":true},"sort":["title"]}`,
					[]string{"go generics", "rust traits"}},
				{"many_to_many", `{"filter":{"tags.slug":"go"}}`,
					[]string{"go generics"}},
				{"many_to_many in", `{"filter":{"tags.slug":{"in":["go","rust"]}},"sort":["title"]}`,
					[]string{"go generics", "rust traits"}},
				{"two hops", `{"filter":{"comments.author.name":"Bob"}}`,
					[]string{"go generics"}},
				{"nested and root", `{"filter":{"author.name":"Ann","views":{"gte":10}}}`,
					[]string{"go generics"}},
				{"negation means none match", `{"filter":{"not":{"comments.approved":true}}}`,
					[]string{"draft"}},
				{"nested or", `{"filter":{"or":[{"author.name":"Bob"},{"tags.slug":"go"}]},"sort":["title"]}`,
					[]string{"go generics", "rust traits"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var req query.Request
					if err := json.Unmarshal([]byte(tc.doc), &req); err != nil {
						t.Fatal(err)
					}
					req.Unpaged = true
					opts, err := req.Compile(Articles.Meta(), nil)
					if err != nil {
						t.Fatal(err)
					}
					got, err := b.articles.GetAll(ctx, opts...)
					if err != nil {
						t.Fatal(err)
					}
					if !eq(titles(got), tc.want) {
						t.Fatalf("got %v, want %v", titles(got), tc.want)
					}
				})
			}
		})
	}
}

// The bug a join-based implementation walks into: an article with two matching
// tags must still be one row, and the total must still be one.
func TestToManyFilterDoesNotDuplicateOrInflateCount(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedBlog(t, b)

			var req query.Request
			_ = json.Unmarshal([]byte(`{"filter":{"tags.slug":{"in":["go","rust"]}},"limit":10}`), &req)
			opts, err := req.Compile(Articles.Meta(), nil)
			if err != nil {
				t.Fatal(err)
			}
			page, err := b.articles.Get(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 2 || page.Total != 2 {
				t.Fatalf("items=%d total=%d, want 2/2 — a join would have said 3",
					len(page.Items), page.Total)
			}
			n, err := b.articles.Count(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if n != 2 {
				t.Fatalf("count = %d, want 2", n)
			}
		})
	}
}

func TestNestedSortAgainstDatabases(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedBlog(t, b)

			var req query.Request
			_ = json.Unmarshal([]byte(`{"sort":["author.name","-views"],"unpaged":true}`), &req)
			opts, err := req.Compile(Articles.Meta(), nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := b.articles.GetAll(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			// Ann's two articles first (by views desc), then Bob's.
			if want := []string{"go generics", "draft", "rust traits"}; !eq(titles(got), want) {
				t.Fatalf("got %v, want %v", titles(got), want)
			}

			// Sorting through a to-many has no single value and is refused.
			var bad query.Request
			_ = json.Unmarshal([]byte(`{"sort":["comments.body"]}`), &bad)
			opts, err = bad.Compile(Articles.Meta(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.articles.GetAll(ctx, opts...); err == nil {
				t.Fatal("sorting through a has_many should fail")
			}
		})
	}
}

func TestPreloadsAgainstDatabases(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			_, _, generics, draft, _ := seedBlog(t, b)

			var req query.Request
			_ = json.Unmarshal([]byte(
				`{"preload":["author","tags","comments.author"],"sort":["title"],"unpaged":true}`), &req)
			opts, err := req.Compile(Articles.Meta(), nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := b.articles.GetAll(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 3 {
				t.Fatalf("%d articles", len(got))
			}

			byTitle := map[string]Article{}
			for _, a := range got {
				byTitle[a.Title] = a
			}

			g := byTitle["go generics"]
			if g.Author == nil || g.Author.Name != "Ann" {
				t.Fatalf("author = %+v", g.Author)
			}
			if len(g.Tags) != 2 {
				t.Fatalf("tags = %+v", g.Tags)
			}
			if len(g.Comments) != 2 {
				t.Fatalf("comments = %+v", g.Comments)
			}
			// Two hops deep, wired into the copies that live in the parent slice.
			names := map[string]bool{}
			for _, c := range g.Comments {
				if c.Author == nil {
					t.Fatalf("comment %d has no author", c.ID)
				}
				names[c.Author.Name] = true
			}
			if !names["Ann"] || !names["Bob"] {
				t.Fatalf("comment authors = %v", names)
			}

			d := byTitle["draft"]
			if len(d.Comments) != 0 || d.Comments == nil {
				t.Fatalf("an article with no comments should carry an empty slice, got %#v", d.Comments)
			}
			if len(d.Tags) != 0 || d.Tags == nil {
				t.Fatalf("tags = %#v", d.Tags)
			}
			_ = generics
			_ = draft
		})
	}
}

// A preload on a single entity works the same way.
func TestPreloadOnGetByID(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			_, _, generics, _, _ := seedBlog(t, b)

			got, err := b.articles.GetByID(ctx, generics.ID, crud.Preload("Author", "Tags"))
			if err != nil {
				t.Fatal(err)
			}
			if got.Author == nil || got.Author.Name != "Ann" || len(got.Tags) != 2 {
				t.Fatalf("article = %+v author = %+v tags = %+v", got.Title, got.Author, got.Tags)
			}
		})
	}
}

func TestFilteredPreloadAgainstDatabases(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedBlog(t, b)

			var req query.Request
			_ = json.Unmarshal([]byte(`{
				"preload": [{"path":"comments","filter":{"approved":true},"sort":["body"]}],
				"filter":  {"title":"go generics"},
				"unpaged": true
			}`), &req)
			opts, err := req.Compile(Articles.Meta(), nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := b.articles.GetAll(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("%d articles", len(got))
			}
			if len(got[0].Comments) != 1 || got[0].Comments[0].Body != "nice write-up" {
				t.Fatalf("comments = %+v", got[0].Comments)
			}
		})
	}
}

// The has_many side of the same edge, walked from the other model.
func TestReverseRelation(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedBlog(t, b)

			var req query.Request
			_ = json.Unmarshal([]byte(`{"filter":{"articles.views":{"gte":100}},"preload":["articles"]}`), &req)
			opts, err := req.Compile(Authors.Meta(), nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := b.authors.GetAll(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Name != "Ann" {
				t.Fatalf("authors = %+v", got)
			}
			if len(got[0].Articles) != 2 {
				t.Fatalf("Ann should have 2 articles, got %d", len(got[0].Articles))
			}
		})
	}
}

// Search must stay inside its own parentheses; if it leaked out of the AND, the
// unpublished draft would come back too.
func TestSearchDoesNotEscapeItsScope(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedBlog(t, b)

			var req query.Request
			_ = json.Unmarshal([]byte(`{
				"filter":  {"publishedAt": {"isNotNull": true}},
				"search":  "generics",
				"searchFields": ["title", "body"],
				"unpaged": true
			}`), &req)
			opts, err := req.Compile(Articles.Meta(), nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := b.articles.GetAll(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if !eq(titles(got), []string{"go generics"}) {
				t.Fatalf("got %v, want just the published match", titles(got))
			}
		})
	}
}

func TestQueryStringAgainstDatabases(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedBlog(t, b)

			req, err := query.ParseQuery(values(
				"f=views:gte:50", "f=tags.slug:in:go,rust", "sort=-views", "preload=author", "unpaged=1"))
			if err != nil {
				t.Fatal(err)
			}
			opts, err := req.Compile(Articles.Meta(), nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := b.articles.GetAll(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"go generics", "rust traits"}; !eq(titles(got), want) {
				t.Fatalf("got %v, want %v", titles(got), want)
			}
			if got[0].Author == nil || got[0].Author.Name != "Ann" {
				t.Fatalf("author = %+v", got[0].Author)
			}
		})
	}
}

func values(pairs ...string) map[string][]string {
	v := map[string][]string{}
	for _, p := range pairs {
		k, val, _ := strings.Cut(p, "=")
		v[k] = append(v[k], val)
	}
	return v
}
