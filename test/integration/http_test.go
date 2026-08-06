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

	"github.com/gofiber/fiber/v3"

	"rx-crud/adapter/crudsql"
	"rx-crud/crud"
	"rx-crud/http/crudfiber"
	"rx-crud/query"
	"rx-crud/repo/decorators/specs"
)

// articleService is what a real application would put between the handler and
// the repository: it embeds the specification-decorated repo, so it satisfies
// crudfiber.Repository for free, and overrides just the method it cares about.
type articleService struct {
	specs.Repo[Article, int64, ArticleUpdate]
	blocked string
}

func (s articleService) Save(ctx context.Context, a *Article) error {
	if a.Title == s.blocked {
		return crud.ErrForbidden
	}
	return s.Repo.Save(ctx, a)
}

// The handler only ever sees the interface, so the service slots straight in.
var _ crudfiber.Repository[Article, int64, ArticleUpdate] = articleService{}

func newApp(t *testing.T, b blog) *fiber.App {
	t.Helper()
	svc := articleService{
		Repo:    specs.Executor(Articles.Bind(b.src)),
		blocked: "forbidden title",
	}
	h := crudfiber.New[Article, int64, ArticleUpdate](svc,
		crudfiber.WithQuery[Article, int64, ArticleUpdate](&query.Config{
			Preloadable: []string{"Author", "Tags", "Comments", "Comments.Author"},
			MaxPreloads: 4,
		}),
	)
	app := fiber.New()
	app.Use("/articles", h.Routes())
	return app
}

type resp struct {
	status int
	body   []byte
}

func (r resp) decode(t *testing.T, into any) {
	t.Helper()
	if err := json.Unmarshal(r.body, into); err != nil {
		t.Fatalf("decoding %s: %v", r.body, err)
	}
}

func call(t *testing.T, app *fiber.App, method, target string, body any) resp {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, body: raw}
}

func raw(t *testing.T, app *fiber.App, method, target, body string) resp {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, body: out}
}

type page struct {
	Items      []Article `json:"items"`
	Page       int       `json:"page"`
	Limit      int       `json:"limit"`
	Total      int64     `json:"total"`
	TotalPages int       `json:"totalPages"`
	HasNext    bool      `json:"hasNext"`
	HasPrev    bool      `json:"hasPrev"`
}

func TestHTTPListAndQuery(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			seedBlog(t, b)
			app := newApp(t, b)

			// GET with the query-string DSL.
			r := call(t, app, http.MethodGet,
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

			// POST /query with the full JSON DSL, including a nested path.
			r = raw(t, app, http.MethodPost, "/articles/query", `{
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

func TestHTTPPagination(t *testing.T) {
	b := blogs(t)[0]
	seedBlog(t, b)
	app := newApp(t, b)

	r := call(t, app, http.MethodGet, "/articles?limit=2&page=1&sort=title", nil)
	var p page
	r.decode(t, &p)
	if len(p.Items) != 2 || p.Total != 3 || p.TotalPages != 2 || !p.HasNext || p.HasPrev {
		t.Fatalf("page 1 = %+v", p)
	}

	r = call(t, app, http.MethodGet, "/articles?limit=2&page=2&sort=title", nil)
	p = page{}
	r.decode(t, &p)
	if len(p.Items) != 1 || p.HasNext || !p.HasPrev {
		t.Fatalf("page 2 = %+v", p)
	}
}

func TestHTTPCount(t *testing.T) {
	b := blogs(t)[0]
	seedBlog(t, b)
	app := newApp(t, b)

	var out struct {
		Count int64 `json:"count"`
	}
	call(t, app, http.MethodGet, "/articles/count?f=views:gte:50", nil).decode(t, &out)
	if out.Count != 2 {
		t.Fatalf("count = %d", out.Count)
	}
	raw(t, app, http.MethodPost, "/articles/count", `{"filter":{"tags.slug":"rust"}}`).decode(t, &out)
	if out.Count != 2 {
		t.Fatalf("count = %d", out.Count)
	}
}

func TestHTTPCrudLifecycle(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ann, _, _, _, _ := seedBlog(t, b)
			app := newApp(t, b)

			// Create. The client tries to dictate the id and the timestamp; both
			// are ignored, because one is generated and the other is `generated`.
			r := raw(t, app, http.MethodPost, "/articles", `{
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

			// Read it back with a preload.
			r = call(t, app, http.MethodGet, "/articles/"+itoa64(created.ID)+"?preload=author", nil)
			if r.status != http.StatusOK {
				t.Fatalf("status %d: %s", r.status, r.body)
			}
			var got Article
			r.decode(t, &got)
			if got.Title != "new post" || got.Author == nil || got.Author.Name != "Ann" {
				t.Fatalf("got %+v author %+v", got.Title, got.Author)
			}

			// PATCH: an absent field is left alone, an explicit null clears.
			r = raw(t, app, http.MethodPatch, "/articles/"+itoa64(created.ID),
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

			r = raw(t, app, http.MethodPatch, "/articles/"+itoa64(created.ID), `{"PublishedAt": null}`)
			r.decode(t, &patched)
			if !patched.PublishedAt.IsNull() {
				t.Fatalf("an explicit null should clear the column, got %v", patched.PublishedAt)
			}
			if patched.Title != "renamed" {
				t.Fatalf("an absent field should be left alone, got %q", patched.Title)
			}

			// DELETE, then a 404 on the way back.
			r = call(t, app, http.MethodDelete, "/articles/"+itoa64(created.ID), nil)
			if r.status != http.StatusOK {
				t.Fatalf("status %d: %s", r.status, r.body)
			}
			r = call(t, app, http.MethodGet, "/articles/"+itoa64(created.ID), nil)
			if r.status != http.StatusNotFound {
				t.Fatalf("status %d: %s", r.status, r.body)
			}
		})
	}
}

func TestHTTPBulkDelete(t *testing.T) {
	b := blogs(t)[0]
	_, _, generics, draft, _ := seedBlog(t, b)
	app := newApp(t, b)

	var out struct {
		Deleted int64 `json:"deleted"`
	}
	call(t, app, http.MethodPost, "/articles/bulk-delete",
		map[string][]int64{"ids": {generics.ID, draft.ID}}).decode(t, &out)
	if out.Deleted != 2 {
		t.Fatalf("deleted = %d", out.Deleted)
	}
	var count struct {
		Count int64 `json:"count"`
	}
	call(t, app, http.MethodGet, "/articles/count", nil).decode(t, &count)
	if count.Count != 1 {
		t.Fatalf("count = %d", count.Count)
	}
}

// The service layer's own rules ride through the handler untouched.
func TestHTTPServiceLayerIsHonoured(t *testing.T) {
	b := blogs(t)[0]
	ann, _, _, _, _ := seedBlog(t, b)
	app := newApp(t, b)

	r := raw(t, app, http.MethodPost, "/articles",
		`{"AuthorID": `+itoa64(ann.ID)+`, "Title": "forbidden title"}`)
	if r.status != http.StatusForbidden {
		t.Fatalf("status %d: %s", r.status, r.body)
	}
}

// Every bad request is a 400 that names what was wrong — never a silently
// ignored clause and never a 500.
func TestHTTPRejections(t *testing.T) {
	b := blogs(t)[0]
	seedBlog(t, b)
	app := newApp(t, b)

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
			r := raw(t, app, tc.method, tc.target, tc.body)
			if r.status != tc.want {
				t.Fatalf("status %d (want %d): %s", r.status, tc.want, r.body)
			}
			var body crudfiber.ErrorBody
			r.decode(t, &body)
			if body.Error == "" {
				t.Fatalf("no error code in %s", r.body)
			}
		})
	}
}

// A model bound through the adapter but never declared as a repository still
// resolves its table, because Define registers it.
func TestHTTPWorksWithoutExtraDeclarations(t *testing.T) {
	b := blog{name: "postgres", db: pgDB, src: crudsql.Postgres(pgDB)}
	b.articles = Articles.Bind(b.src)
	b.authors = Authors.Bind(b.src)
	b.comments = Comments.Bind(b.src)
	b.tags = Tags.Bind(b.src)
	seedBlog(t, b)

	h := crudfiber.New[Comment, int64, CommentUpdate](Comments.Bind(b.src))
	app := fiber.New()
	app.Use("/comments", h.Routes())

	r := call(t, app, http.MethodGet, "/comments?preload=author&f=approved:eq:true&sort=body", nil)
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

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
