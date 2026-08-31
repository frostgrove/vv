//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/http/crudnet"
	"github.com/frostgrove/vv/crud/query"
)

var _ crudnet.Repository[Article, int64, ArticleUpdate] = articleService{}

func newNetApp(t *testing.T, b blog) *http.ServeMux {
	t.Helper()
	service := articleService{
		Repo:    specs.Executor(Articles.Bind(b.source)),
		blocked: "forbidden title",
	}
	h := crudnet.New[Article, int64, ArticleUpdate](service,
		crudnet.WithQuery[Article, int64, ArticleUpdate](&query.Config{
			Preloadable: []string{"Author", "Tags", "Comments", "Comments.Author"},
			MaxPreloads: 4,
		}),
	)
	mux := http.NewServeMux()
	h.Mount(mux, "/articles")
	return mux
}

func netCall(t *testing.T, app *http.ServeMux, method, target string, body any) response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, target, rdr)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, request)
	return response{status: w.Code, body: w.Body.Bytes()}
}

func netRaw(t *testing.T, app *http.ServeMux, method, target, body string) response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}
	request := httptest.NewRequest(method, target, rdr)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, request)
	return response{status: w.Code, body: w.Body.Bytes()}
}

func TestNetHTTPListAndQuery(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			seedBlog(t, b)
			app := newNetApp(t, b)

			r := netCall(t, app, http.MethodGet,
				"/articles?f=views:gte:50&sort=-views&preload=author,tags&limit=10", nil)
			if r.status != http.StatusOK {
				t.Fatalf("status %d: %s", r.status, r.body)
			}
			var p page
			r.decode(t, &p)
			if len(p.Items) != 2 || p.Total != 2 || p.TotalPages != 1 {
				t.Fatalf("page = %+v", p)
			}
			if p.Items[0].Title != "go generics" {
				t.Fatalf("order = %v", titles(p.Items))
			}
			if p.Items[0].Author == nil || p.Items[0].Author.Name != "Ann" {
				t.Fatalf("author was not preloaded: %+v", p.Items[0].Author)
			}
			if len(p.Items[0].Tags) != 2 {
				t.Fatalf("tags = %+v", p.Items[0].Tags)
			}

			r = netRaw(t, app, http.MethodPost, "/articles/query", `{
				"filter": {
					"or": [{"tags.slug": "go"}, {"comments.author.name": "Ann"}],
					"publishedAt": {"isNotNull": true}
				},
				"preload": [{"path": "comments", "filter": {"approved": true}}],
				"sort": ["title"],
				"limit": 10
			}`)
			if r.status != http.StatusOK {
				t.Fatalf("status %d: %s", r.status, r.body)
			}
			p = page{}
			r.decode(t, &p)
			if want := []string{"go generics", "rust traits"}; !eq(titles(p.Items), want) {
				t.Fatalf("titles = %v, want %v", titles(p.Items), want)
			}
			for _, a := range p.Items {
				for _, c := range a.Comments {
					if !c.Approved {
						t.Fatalf("the preload filter was ignored: %+v", c)
					}
				}
			}
		})
	}
}

func TestNetHTTPPagination(t *testing.T) {
	b := blogs(t)[0]
	seedBlog(t, b)
	app := newNetApp(t, b)

	r := netCall(t, app, http.MethodGet, "/articles?limit=2&page=1&sort=title", nil)
	var p page
	r.decode(t, &p)
	if len(p.Items) != 2 || p.Total != 3 || p.TotalPages != 2 || !p.HasNext || p.HasPrev {
		t.Fatalf("page 1 = %+v", p)
	}

	r = netCall(t, app, http.MethodGet, "/articles?limit=2&page=2&sort=title", nil)
	p = page{}
	r.decode(t, &p)
	if len(p.Items) != 1 || p.HasNext || !p.HasPrev {
		t.Fatalf("page 2 = %+v", p)
	}
}

func TestNetHTTPCount(t *testing.T) {
	b := blogs(t)[0]
	seedBlog(t, b)
	app := newNetApp(t, b)

	var out struct {
		Count int64 `json:"count"`
	}
	netCall(t, app, http.MethodGet, "/articles/count?f=views:gte:50", nil).decode(t, &out)
	if out.Count != 2 {
		t.Fatalf("count = %d", out.Count)
	}
	netRaw(t, app, http.MethodPost, "/articles/count", `{"filter":{"tags.slug":"rust"}}`).decode(t, &out)
	if out.Count != 2 {
		t.Fatalf("count = %d", out.Count)
	}
}

func TestNetHTTPCrudLifecycle(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ann, _, _, _, _ := seedBlog(t, b)
			app := newNetApp(t, b)

			r := netRaw(t, app, http.MethodPost, "/articles", `{
				"ID": 9999, "AuthorID": `+itoa64(ann.ID)+`,
				"Title": "new post", "Body": "hello", "Views": 3,
				"CreatedAt": "1999-01-01T00:00:00Z"
			}`)
			if r.status != http.StatusCreated {
				t.Fatalf("status %d: %s", r.status, r.body)
			}
			var created Article
			r.decode(t, &created)
			if created.ID == 0 || created.ID == 9999 {
				t.Fatalf("id = %d: the client should not choose it", created.ID)
			}
			if created.CreatedAt.Year() == 1999 {
				t.Fatalf("created_at = %v: a generated column should not be client-set", created.CreatedAt)
			}

			r = netCall(t, app, http.MethodGet, "/articles/"+itoa64(created.ID)+"?preload=author", nil)
			if r.status != http.StatusOK {
				t.Fatalf("status %d: %s", r.status, r.body)
			}
			var got Article
			r.decode(t, &got)
			if got.Title != "new post" || got.Author == nil || got.Author.Name != "Ann" {
				t.Fatalf("got %+v author %+v", got.Title, got.Author)
			}

			r = netRaw(t, app, http.MethodPatch, "/articles/"+itoa64(created.ID),
				`{"Title": "renamed", "PublishedAt": "2026-02-03T04:05:06Z"}`)
			if r.status != http.StatusOK {
				t.Fatalf("status %d: %s", r.status, r.body)
			}
			var patched Article
			r.decode(t, &patched)
			if patched.Title != "renamed" || patched.Body != "hello" || patched.Views != 3 {
				t.Fatalf("patched = %+v", patched)
			}
			if !patched.PublishedAt.IsSet() {
				t.Fatalf("publishedAt = %v", patched.PublishedAt)
			}

			r = netRaw(t, app, http.MethodPatch, "/articles/"+itoa64(created.ID), `{"PublishedAt": null}`)
			r.decode(t, &patched)
			if !patched.PublishedAt.IsNull() {
				t.Fatalf("an explicit null should clear the column, got %v", patched.PublishedAt)
			}
			if patched.Title != "renamed" {
				t.Fatalf("an absent field should be left alone, got %q", patched.Title)
			}

			r = netCall(t, app, http.MethodDelete, "/articles/"+itoa64(created.ID), nil)
			if r.status != http.StatusOK {
				t.Fatalf("status %d: %s", r.status, r.body)
			}
			r = netCall(t, app, http.MethodGet, "/articles/"+itoa64(created.ID), nil)
			if r.status != http.StatusNotFound {
				t.Fatalf("status %d: %s", r.status, r.body)
			}
		})
	}
}

func TestNetHTTPReplace(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ann, _, generics, _, _ := seedBlog(t, b)
			app := newNetApp(t, b)

			r := netRaw(t, app, http.MethodPut, "/articles/"+itoa64(generics.ID), `{
				"ID": 424242, "AuthorID": `+itoa64(ann.ID)+`,
				"Title": "replaced", "Body": "whole new body", "Views": 7
			}`)
			if r.status != http.StatusOK {
				t.Fatalf("status %d: %s", r.status, r.body)
			}
			var replaced Article
			r.decode(t, &replaced)
			if replaced.ID != generics.ID {
				t.Fatalf("id = %d, want the URL's %d", replaced.ID, generics.ID)
			}
			if replaced.Title != "replaced" || replaced.Body != "whole new body" || replaced.Views != 7 {
				t.Fatalf("replaced = %+v", replaced)
			}

			if replaced.PublishedAt.IsSet() {
				t.Fatalf("publishedAt = %v: PUT replaces the row, so an absent field is cleared", replaced.PublishedAt)
			}
			if !replaced.CreatedAt.Equal(generics.CreatedAt) {
				t.Fatalf("created_at %v -> %v: an immutable column was rewritten by a replace",
					generics.CreatedAt, replaced.CreatedAt)
			}
			if n, err := b.articles.Count(context.Background()); err != nil || n != 3 {
				t.Fatalf("count = %d err = %v: the replace inserted a row instead of replacing one", n, err)
			}

			r = netRaw(t, app, http.MethodPut, "/articles/999999",
				`{"AuthorID": `+itoa64(ann.ID)+`, "Title": "smuggled", "Body": "x"}`)
			if r.status != http.StatusNotFound {
				t.Fatalf("status %d: %s — PUT at an unused id must not create on a generated key", r.status, r.body)
			}

			r = netRaw(t, app, http.MethodPost, "/articles",
				`{"AuthorID": `+itoa64(ann.ID)+`, "Title": "after the put", "Body": "y"}`)
			if r.status != http.StatusCreated {
				t.Fatalf("status %d: %s — the key sequence was stranded by the replace", r.status, r.body)
			}
			var created Article
			r.decode(t, &created)
			if created.ID == 0 || created.ID == generics.ID {
				t.Fatalf("the new row took id %d", created.ID)
			}
		})
	}
}

func TestNetHTTPBulkDelete(t *testing.T) {
	b := blogs(t)[0]
	_, _, generics, draft, _ := seedBlog(t, b)
	app := newNetApp(t, b)

	var out struct {
		Deleted int64 `json:"deleted"`
	}
	netCall(t, app, http.MethodPost, "/articles/bulk-delete",
		map[string][]int64{"ids": {generics.ID, draft.ID}}).decode(t, &out)
	if out.Deleted != 2 {
		t.Fatalf("deleted = %d", out.Deleted)
	}
	var count struct {
		Count int64 `json:"count"`
	}
	netCall(t, app, http.MethodGet, "/articles/count", nil).decode(t, &count)
	if count.Count != 1 {
		t.Fatalf("count = %d", count.Count)
	}
}

func TestNetHTTPServiceLayerIsHonoured(t *testing.T) {
	b := blogs(t)[0]
	ann, _, _, _, _ := seedBlog(t, b)
	app := newNetApp(t, b)

	r := netRaw(t, app, http.MethodPost, "/articles",
		`{"AuthorID": `+itoa64(ann.ID)+`, "Title": "forbidden title"}`)
	if r.status != http.StatusForbidden {
		t.Fatalf("status %d: %s", r.status, r.body)
	}
}

func TestNetHTTPRejections(t *testing.T) {
	b := blogs(t)[0]
	seedBlog(t, b)
	app := newNetApp(t, b)

	for _, tc := range []struct {
		name, method, target, body string
		want                       int
	}{
		{"unknown filter field", http.MethodPost, "/articles/query", `{"filter":{"nope":1}}`, 400},
		{"unknown nested field", http.MethodPost, "/articles/query", `{"filter":{"author.nope":1}}`, 400},
		{"bad value type", http.MethodPost, "/articles/query", `{"filter":{"views":"lots"}}`, 400},
		{"unknown sort", http.MethodGet, "/articles?sort=nope", "", 400},
		{"preload not allowed", http.MethodGet, "/articles?preload=comments.author.articles", "", 400},
		{"bad id", http.MethodGet, "/articles/not-a-number", "", 400},
		{"missing row", http.MethodGet, "/articles/999999", "", 404},
		{"malformed json", http.MethodPost, "/articles/query", `{`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := netRaw(t, app, tc.method, tc.target, tc.body)
			if r.status != tc.want {
				t.Fatalf("status %d (want %d): %s", r.status, tc.want, r.body)
			}
			var body wireEnvelope
			r.decode(t, &body)

			vs := append(append([]wireViolation{}, body.Errors.Validation...), body.Errors.General...)
			if len(vs) == 0 {
				t.Fatalf("no violations in %s", r.body)
			}
			for _, v := range vs {
				if v.Code == "" {
					t.Fatalf("a violation with no error_code in %s", r.body)
				}
			}
		})
	}
}

func TestNetHTTPWorksWithoutExtraDeclarations(t *testing.T) {
	b := newBlog("postgres", pgDB, crudsql.Postgres(pgDB))
	seedBlog(t, b)

	h := crudnet.New[Comment, int64, CommentUpdate](Comments.Bind(b.source))
	app := http.NewServeMux()
	h.Mount(app, "/comments")

	r := netCall(t, app, http.MethodGet, "/comments?preload=author&f=approved:eq:true&sort=body", nil)
	if r.status != http.StatusOK {
		t.Fatalf("status %d: %s", r.status, r.body)
	}
	var out struct {
		Items []Comment `json:"items"`
		Total int64     `json:"total"`
	}
	r.decode(t, &out)
	if out.Total != 2 {
		t.Fatalf("total = %d", out.Total)
	}
	for _, c := range out.Items {
		if c.Author == nil {
			t.Fatalf("comment %d has no preloaded author", c.ID)
		}
	}
}
