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

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/http/crudfiber"
	"github.com/shardit-io/vv/http/crudgin"
	"github.com/shardit-io/vv/http/crudnet"
	"github.com/shardit-io/vv/port"
	"github.com/shardit-io/vv/query"
	"github.com/shardit-io/vv/repo/decorators/specs"
)

// articlePort is the same rule articleService enforces, stated at the service
// seam instead of at the repository one: it embeds the default service and
// overrides the one command it cares about.
//
// It is what a generated service will be, and it is [[D-045]]'s control against
// a live database — one value, three bindings, no glue.
type articlePort struct {
	*port.DefaultService[Article, int64, ArticleUpdate]
	blocked string
}

func (s articlePort) Create(ctx context.Context, cmd port.CreateCommand[Article]) (Article, error) {
	if cmd.Model.Title == s.blocked {
		return Article{}, crud.ErrForbidden
	}
	return s.DefaultService.Create(ctx, cmd)
}

// The three bindings hold one Service type, because a generic alias is the same
// type. This is the compile-time half of the claim; the test below is the half
// that runs.
var (
	_ crudfiber.Service[Article, int64, ArticleUpdate] = articlePort{}
	_ crudgin.Service[Article, int64, ArticleUpdate]   = articlePort{}
	_ crudnet.Service[Article, int64, ArticleUpdate]   = articlePort{}
)

func newPortService(b blog) articlePort {
	return articlePort{
		DefaultService: port.NewService[Article, int64, ArticleUpdate](
			specs.Executor(Articles.Bind(b.src)),
			port.WithQuery(&query.Config{
				Preloadable: []string{"Author", "Tags", "Comments", "Comments.Author"},
				MaxPreloads: 4,
			}),
		),
		blocked: "forbidden title",
	}
}

// portBinding is one transport's whole job: mount the service, send a request.
type portBinding struct {
	name  string
	serve func(t *testing.T, svc articlePort, method, target, body string) resp
}

func portRequest(method, target, body string) *http.Request {
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

var portBindings = []portBinding{
	{"crudnet", func(t *testing.T, svc articlePort, method, target, body string) resp {
		t.Helper()
		mux := http.NewServeMux()
		crudnet.Serving[Article, int64, ArticleUpdate](svc).Mount(mux, "/articles")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, portRequest(method, target, body))
		return resp{status: w.Code, body: w.Body.Bytes()}
	}},
	{"crudgin", func(t *testing.T, svc articlePort, method, target, body string) resp {
		t.Helper()
		app := gin.New()
		crudgin.Serving[Article, int64, ArticleUpdate](svc).Mount(app, "/articles")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, portRequest(method, target, body))
		return resp{status: w.Code, body: w.Body.Bytes()}
	}},
	{"crudfiber", func(t *testing.T, svc articlePort, method, target, body string) resp {
		t.Helper()
		app := fiber.New()
		app.Use("/articles", crudfiber.Serving[Article, int64, ArticleUpdate](svc).Routes())
		res, err := app.Test(portRequest(method, target, body), fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatalf("crudfiber: %s %s: %v", method, target, err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("crudfiber: reading the response: %v", err)
		}
		return resp{status: res.StatusCode, body: raw}
	}},
}

// [[D-045]]'s control, live: one port.Service value mounted on all three
// bindings answers the same status, the same bytes and the same rule against a
// real database.
func TestOnePortServiceMountsOnAllThreeBindings(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ann, _, generics, _, _ := seedBlog(t, b)
			svc := newPortService(b)

			t.Run("the same read is byte-identical on all three", func(t *testing.T) {
				var first portBinding
				var want resp
				for i, bind := range portBindings {
					got := bind.serve(t, svc, http.MethodGet, "/articles/"+itoa64(generics.ID)+"?preload=author", "")
					if got.status != http.StatusOK {
						t.Fatalf("%s answered %d: %s", bind.name, got.status, got.body)
					}
					if i == 0 {
						first, want = bind, got
						continue
					}
					if !bytes.Equal(got.body, want.body) {
						t.Fatalf("%s answered %s and %s answered %s", first.name, want.body, bind.name, got.body)
					}
				}
			})

			t.Run("the service's own rule holds on all three", func(t *testing.T) {
				for _, bind := range portBindings {
					got := bind.serve(t, svc, http.MethodPost, "/articles",
						`{"AuthorID": `+itoa64(ann.ID)+`, "Title": "forbidden title"}`)
					if got.status != http.StatusForbidden {
						t.Fatalf("%s answered %d for the title the service refuses: %s", bind.name, got.status, got.body)
					}
				}
			})

			// The control: the same route with a title the service allows
			// writes a row. Without it the leg above passes for a service that
			// refuses everything, or for three bindings whose create route was
			// never mounted at all.
			t.Run("and the control: a title it allows is written", func(t *testing.T) {
				for _, bind := range portBindings {
					got := bind.serve(t, svc, http.MethodPost, "/articles",
						`{"AuthorID": `+itoa64(ann.ID)+`, "Title": "through the port", "ID": 999999}`)
					if got.status != http.StatusCreated {
						t.Fatalf("%s answered %d: %s", bind.name, got.status, got.body)
					}
					var out Article
					if err := json.Unmarshal(got.body, &out); err != nil {
						t.Fatalf("%s answered a body that is not the row: %v in %s", bind.name, err, got.body)
					}
					if out.Title != "through the port" {
						t.Fatalf("%s stored %q", bind.name, out.Title)
					}
					// And the clearing still runs, at the service now rather
					// than in each binding: the key the client sent is gone.
					if out.ID == 999999 {
						t.Fatalf("%s let the client choose its own key", bind.name)
					}
				}
			})
		})
	}
}
