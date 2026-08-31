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

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/http/crudfiber"
	"github.com/frostgrove/vv/crud/http/crudgin"
	"github.com/frostgrove/vv/crud/http/crudnet"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/port"
)

type articlePort struct {
	*port.DefaultService[Article, int64, ArticleUpdate]
	blocked string
}

func (this articlePort) Create(ctx context.Context, cmd port.CreateCommand[Article]) (Article, error) {
	if cmd.Model.Title == this.blocked {
		return Article{}, crud.ErrForbidden
	}
	return this.DefaultService.Create(ctx, cmd)
}

var (
	_ crudfiber.Service[Article, int64, ArticleUpdate] = articlePort{}
	_ crudgin.Service[Article, int64, ArticleUpdate]   = articlePort{}
	_ crudnet.Service[Article, int64, ArticleUpdate]   = articlePort{}
)

func newPortService(b blog) articlePort {
	return articlePort{
		DefaultService: port.NewService[Article, int64, ArticleUpdate](
			specs.Executor(Articles.Bind(b.source)),
			port.WithQuery(&query.Config{
				Preloadable: []string{"Author", "Tags", "Comments", "Comments.Author"},
				MaxPreloads: 4,
			}),
		),
		blocked: "forbidden title",
	}
}

type portBinding struct {
	name  string
	serve func(t *testing.T, service articlePort, method, target, body string) response
}

func portRequest(method, target, body string) *http.Request {
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}
	request := httptest.NewRequest(method, target, rdr)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

var portBindings = []portBinding{
	{"crudnet", func(t *testing.T, service articlePort, method, target, body string) response {
		t.Helper()
		mux := http.NewServeMux()
		crudnet.Serving[Article, int64, ArticleUpdate](service).Mount(mux, "/articles")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, portRequest(method, target, body))
		return response{status: w.Code, body: w.Body.Bytes()}
	}},
	{"crudgin", func(t *testing.T, service articlePort, method, target, body string) response {
		t.Helper()
		app := gin.New()
		crudgin.Serving[Article, int64, ArticleUpdate](service).Mount(app, "/articles")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, portRequest(method, target, body))
		return response{status: w.Code, body: w.Body.Bytes()}
	}},
	{"crudfiber", func(t *testing.T, service articlePort, method, target, body string) response {
		t.Helper()
		app := fiber.New()
		app.Use("/articles", crudfiber.Serving[Article, int64, ArticleUpdate](service).Routes())
		httpResponse, err := app.Test(portRequest(method, target, body), fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatalf("crudfiber: %s %s: %v", method, target, err)
		}
		defer httpResponse.Body.Close()
		raw, err := io.ReadAll(httpResponse.Body)
		if err != nil {
			t.Fatalf("crudfiber: reading the response: %v", err)
		}
		return response{status: httpResponse.StatusCode, body: raw}
	}},
}

func TestOnePortServiceMountsOnAllThreeBindings(t *testing.T) {
	for _, b := range blogs(t) {
		t.Run(b.name, func(t *testing.T) {
			ann, _, generics, _, _ := seedBlog(t, b)
			service := newPortService(b)

			t.Run("the same read is byte-identical on all three", func(t *testing.T) {
				var first portBinding
				var want response
				for i, bind := range portBindings {
					got := bind.serve(t, service, http.MethodGet, "/articles/"+itoa64(generics.ID)+"?preload=author", "")
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
					got := bind.serve(t, service, http.MethodPost, "/articles",
						`{"AuthorID": `+itoa64(ann.ID)+`, "Title": "forbidden title"}`)
					if got.status != http.StatusForbidden {
						t.Fatalf("%s answered %d for the title the service refuses: %s", bind.name, got.status, got.body)
					}
				}
			})

			t.Run("and the control: a title it allows is written", func(t *testing.T) {
				for _, bind := range portBindings {
					got := bind.serve(t, service, http.MethodPost, "/articles",
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

					if out.ID == 999999 {
						t.Fatalf("%s let the client choose its own key", bind.name)
					}
				}
			})
		})
	}
}
