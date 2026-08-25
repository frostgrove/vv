// Package portmount holds [[D-045]]'s control: one port.Service value mounts on
// all three bindings, and all three answer identically.
//
// It lives in the test module because that is the only place the three can be
// imported together — crudfiber and crudgin are satellites and crudnet is in
// the library. It needs no database, so `make unit` runs it.
package portmount

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/http/crudfiber"
	"github.com/shardit-io/vv/crud/http/crudgin"
	"github.com/shardit-io/vv/crud/http/crudnet"
	"github.com/shardit-io/vv/crud/query"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/port"
)

// TestMain silences Gin's start-up banner and per-route debug lines.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// the model

// Widget carries the two things a create request may not dictate: a key the
// database generates and a column it fills.
type Widget struct {
	ID        int64     `db:"id,pk,auto" json:"id"`
	Name      string    `db:"name" json:"name"`
	Price     int       `db:"price" json:"price"`
	CreatedAt time.Time `db:"created_at,generated" json:"createdAt"`
}

// WidgetUpdate is the PATCH DTO.
type WidgetUpdate struct {
	Name *string `json:"name"`
}

var widgetMeta = func() *crud.Meta {
	m, err := crud.NewMeta[Widget]("widgets")
	if err != nil {
		panic(err)
	}
	return m
}()

var savedAt = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

// ---------------------------------------------------------------------------
// the repository under the service

// repoCall is one call the service made, which is what says the rules ran below
// the binding rather than inside it.
type repoCall struct {
	Method string
	ID     int64
	IDs    []int64
	Model  Widget
	Limit  int
	Offset int
	Sorted bool
	Filter bool
}

type fakeRepo struct {
	calls []repoCall
	// err is what Save answers, for the path-translation test: the whole chain
	// below the binding has to be real for the field it renders to mean
	// anything.
	err error
}

func (f *fakeRepo) Meta() *crud.Meta { return widgetMeta }

func (f *fakeRepo) record(method string, o *crud.Options) {
	f.calls = append(f.calls, repoCall{
		Method: method, Limit: o.Limit, Offset: o.Offset,
		Sorted: len(o.Sort) > 0, Filter: o.Predicate() != nil,
	})
}

func (f *fakeRepo) Get(_ context.Context, opts ...crud.Option) (crud.PaginatedResponse[Widget], error) {
	f.record("Get", crud.Build(opts...))
	return crud.NewPaginatedResponse([]Widget{{ID: 1, Name: "bolt", Price: 250, CreatedAt: savedAt}}, 1, 1, 1), nil
}

func (f *fakeRepo) GetAll(_ context.Context, opts ...crud.Option) ([]Widget, error) {
	f.record("GetAll", crud.Build(opts...))
	return nil, nil
}

func (f *fakeRepo) GetByID(_ context.Context, id int64, opts ...crud.Option) (Widget, error) {
	o := crud.Build(opts...)
	f.calls = append(f.calls, repoCall{
		Method: "GetByID", ID: id, Limit: o.Limit, Offset: o.Offset,
		Sorted: len(o.Sort) > 0, Filter: o.Predicate() != nil,
	})
	return Widget{ID: id, Name: "bolt", Price: 250, CreatedAt: savedAt}, nil
}

func (f *fakeRepo) Count(_ context.Context, opts ...crud.Option) (int64, error) {
	f.record("Count", crud.Build(opts...))
	return 5, nil
}

func (f *fakeRepo) Save(_ context.Context, m *Widget) error {
	f.calls = append(f.calls, repoCall{Method: "Save", Model: *m})
	if f.err != nil {
		return f.err
	}
	m.CreatedAt = savedAt
	if m.ID == 0 {
		m.ID = 7
	}
	return nil
}

func (f *fakeRepo) Update(_ context.Context, id int64, _ WidgetUpdate, _ ...crud.Option) (Widget, error) {
	f.calls = append(f.calls, repoCall{Method: "Update", ID: id})
	return Widget{ID: id, Name: "patched", CreatedAt: savedAt}, nil
}

func (f *fakeRepo) Delete(_ context.Context, ids ...int64) (int64, error) {
	f.calls = append(f.calls, repoCall{Method: "Delete", IDs: ids})
	return int64(len(ids)), nil
}

// ---------------------------------------------------------------------------
// the service every binding is handed

// command is one command exactly as a binding handed it over.
//
// The query document is copied rather than referenced. The service narrows it
// in place, so a binding that narrowed it first would be indistinguishable from
// one that did not by the time the call returned — which is precisely the
// drift this test exists to catch.
type command struct {
	Verb     string
	Query    query.Request
	HasQuery bool
	ID       int64
	IDs      []int64
	Model    Widget
	Patched  bool
	Options  int
	Hook     bool
}

// recorder is a Service that records what it was handed and then behaves like
// the default one. It is the seam [[D-045]] is about: three bindings, one value.
type recorder struct {
	inner *port.DefaultService[Widget, int64, WidgetUpdate]
	repo  *fakeRepo
	got   []command
}

func newRecorder() *recorder {
	repo := &fakeRepo{}
	return &recorder{inner: port.NewService[Widget, int64, WidgetUpdate](repo), repo: repo}
}

func snap(req *query.Request) (query.Request, bool) {
	if req == nil {
		return query.Request{}, false
	}
	return *req, true
}

func (s *recorder) Meta() *crud.Meta { return s.inner.Meta() }

func (s *recorder) Paths() errs.Resolver { return s.inner.Paths() }

func (s *recorder) List(ctx context.Context, cmd port.ListCommand) (crud.PaginatedResponse[Widget], error) {
	q, ok := snap(cmd.Query)
	s.got = append(s.got, command{Verb: "List", Query: q, HasQuery: ok, Options: len(cmd.Options)})
	return s.inner.List(ctx, cmd)
}

func (s *recorder) Count(ctx context.Context, cmd port.CountCommand) (int64, error) {
	q, ok := snap(cmd.Query)
	s.got = append(s.got, command{Verb: "Count", Query: q, HasQuery: ok, Options: len(cmd.Options)})
	return s.inner.Count(ctx, cmd)
}

func (s *recorder) Get(ctx context.Context, cmd port.GetCommand[int64]) (Widget, error) {
	q, ok := snap(cmd.Query)
	s.got = append(s.got, command{Verb: "Get", Query: q, HasQuery: ok, ID: cmd.ID, Options: len(cmd.Options)})
	return s.inner.Get(ctx, cmd)
}

func (s *recorder) Create(ctx context.Context, cmd port.CreateCommand[Widget]) (Widget, error) {
	s.got = append(s.got, command{Verb: "Create", Model: cmd.Model, Hook: cmd.Before != nil})
	return s.inner.Create(ctx, cmd)
}

func (s *recorder) Update(ctx context.Context, cmd port.UpdateCommand[int64, WidgetUpdate]) (Widget, error) {
	s.got = append(s.got, command{Verb: "Update", ID: cmd.ID, Patched: cmd.Patch.Name != nil, Hook: cmd.Before != nil})
	return s.inner.Update(ctx, cmd)
}

func (s *recorder) Replace(ctx context.Context, cmd port.ReplaceCommand[int64, Widget]) (Widget, error) {
	s.got = append(s.got, command{Verb: "Replace", ID: cmd.ID, Model: cmd.Model, Hook: cmd.Before != nil})
	return s.inner.Replace(ctx, cmd)
}

func (s *recorder) Delete(ctx context.Context, cmd port.DeleteCommand[int64]) (int64, error) {
	s.got = append(s.got, command{Verb: "Delete", ID: cmd.ID})
	return s.inner.Delete(ctx, cmd)
}

func (s *recorder) DeleteMany(ctx context.Context, cmd port.BulkDeleteCommand[int64]) (int64, error) {
	s.got = append(s.got, command{Verb: "DeleteMany", IDs: cmd.IDs})
	return s.inner.DeleteMany(ctx, cmd)
}

// ---------------------------------------------------------------------------
// the three bindings

// A binding is the whole of what a transport is allowed to be: a way to mount
// the same service and a way to send it a request.
type binding struct {
	name  string
	serve func(t *testing.T, svc port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte)
	// mappedServe mounts the same service behind a generated mapper, which is
	// the only difference between the two halves of the path-translation test.
	mappedServe func(t *testing.T, svc port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte)
}

func request(method, target, body string) *http.Request {
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

var bindings = []binding{
	{
		name: "crudnet",
		serve: func(t *testing.T, svc port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte) {
			t.Helper()
			mux := http.NewServeMux()
			crudnet.Serving(svc).Mount(mux, "/widgets")
			return throughMux(mux, method, target, body)
		},
		mappedServe: func(t *testing.T, svc port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte) {
			t.Helper()
			mux := http.NewServeMux()
			crudnet.ServingFor(svc, WidgetMapper{}).Mount(mux, "/widgets")
			return throughMux(mux, method, target, body)
		},
	},
	{
		name: "crudgin",
		serve: func(t *testing.T, svc port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte) {
			t.Helper()
			r := gin.New()
			crudgin.Serving(svc).Mount(r, "/widgets")
			return throughMux(r, method, target, body)
		},
		mappedServe: func(t *testing.T, svc port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte) {
			t.Helper()
			r := gin.New()
			crudgin.ServingFor(svc, WidgetMapper{}).Mount(r, "/widgets")
			return throughMux(r, method, target, body)
		},
	},
	{
		name: "crudfiber",
		serve: func(t *testing.T, svc port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte) {
			t.Helper()
			app := fiber.New()
			app.Use("/widgets", crudfiber.Serving(svc).Routes())
			return throughFiber(t, app, method, target, body)
		},
		mappedServe: func(t *testing.T, svc port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte) {
			t.Helper()
			app := fiber.New()
			app.Use("/widgets", crudfiber.ServingFor(svc, WidgetMapper{}).Routes())
			return throughFiber(t, app, method, target, body)
		},
	},
}

func throughMux(h http.Handler, method, target, body string) (int, []byte) {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request(method, target, body))
	return w.Code, w.Body.Bytes()
}

func throughFiber(t *testing.T, app *fiber.App, method, target, body string) (int, []byte) {
	t.Helper()
	res, err := app.Test(request(method, target, body), fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("crudfiber: %s %s: %v", method, target, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("crudfiber: reading the response: %v", err)
	}
	return res.StatusCode, raw
}

// ---------------------------------------------------------------------------

// [[D-045]]'s control. One service value, three bindings, and the claim is not
// that they compile — it is that they say the same thing.
//
// Three assertions per request, and the second is the one with teeth. Equal
// statuses would pass for two bindings that both answered 200 with different
// bodies. Equal bodies would pass for two bindings that reached the service by
// different routes. The command is what says the transport did nothing but
// route, decode and write: the moment one binding re-derives a rule — narrows a
// count itself, coerces a key differently, clears a field before handing it
// over — the recorded command diverges and this fails.
func TestOneServiceMountsOnAllThreeBindings(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
	}{
		{"a list with paging and a filter", http.MethodGet, "/widgets?page=2&limit=5&f=price:gte:100&sort=-price", ""},
		{"a list through the JSON document", http.MethodPost, "/widgets/query", `{"limit":3,"sort":["-price"]}`},
		{"a count with the paging a count must drop", http.MethodGet, "/widgets/count?page=2&limit=5&sort=-price&f=price:gte:100", ""},
		{"a count through the JSON document", http.MethodPost, "/widgets/count", `{"page":4,"limit":9}`},
		{"one entity, with the shaping a keyed read keeps", http.MethodGet, "/widgets/42?select=name&f=price:gte:100&limit=9", ""},
		{"a create carrying what it may not choose", http.MethodPost, "/widgets", `{"id":999,"name":"bolt","price":250,"createdAt":"2001-02-03T04:05:06Z"}`},
		{"a patch", http.MethodPatch, "/widgets/42", `{"name":"patched"}`},
		{"a replace", http.MethodPut, "/widgets/42", `{"id":999,"name":"replaced"}`},
		{"a delete", http.MethodDelete, "/widgets/42", ""},
		{"a bulk delete", http.MethodPost, "/widgets/bulk-delete", `{"ids":[1,2,3]}`},
		{"a key that does not parse", http.MethodGet, "/widgets/nope", ""},
		{"a filter on a field the model lacks", http.MethodGet, "/widgets?f=nope:eq:1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				first  binding
				status int
				body   []byte
				cmds   []command
				calls  []repoCall
			)
			for i, b := range bindings {
				svc := newRecorder()
				gotStatus, gotBody := b.serve(t, svc, tc.method, tc.target, tc.body)
				if i == 0 {
					first, status, body, cmds, calls = b, gotStatus, gotBody, svc.got, svc.repo.calls
					continue
				}
				if gotStatus != status {
					t.Fatalf("%s answered %d and %s answered %d for the same request",
						first.name, status, b.name, gotStatus)
				}
				if !bytes.Equal(gotBody, body) {
					t.Fatalf("%s answered %s and %s answered %s — the same service, byte for byte, is the claim",
						first.name, body, b.name, gotBody)
				}
				if !reflect.DeepEqual(svc.got, cmds) {
					t.Fatalf("%s handed the service %+v and %s handed it %+v — one of them is re-deriving a rule the service owns",
						first.name, cmds, b.name, svc.got)
				}
				if !reflect.DeepEqual(svc.repo.calls, calls) {
					t.Fatalf("under %s the service called the repository with %+v and under %s with %+v",
						first.name, calls, b.name, svc.repo.calls)
				}
			}
		})
	}
}

// The other half: what the three agree on is right, not merely equal. Three
// bindings that all forgot to narrow a count would pass the test above.
func TestTheServiceIsWhereTheRulesRan(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
		want                       func(*testing.T, []command, []repoCall)
	}{
		{
			name: "the binding hands over the document it parsed, unnarrowed",
			// A count is where the two would differ: the binding used to narrow
			// it and the service does now.
			method: http.MethodGet, target: "/widgets/count?page=2&limit=5&sort=-price",
			want: func(t *testing.T, cmds []command, calls []repoCall) {
				if len(cmds) != 1 || cmds[0].Verb != "Count" {
					t.Fatalf("the route made %+v, want one Count command", cmds)
				}
				if cmds[0].Query.Limit != 5 || len(cmds[0].Query.Sort) != 1 {
					t.Fatalf("the binding handed over %+v; narrowing is the service's and a binding that did it first would hand over a document that is already empty", cmds[0].Query)
				}
				if len(calls) != 1 || calls[0].Limit != 0 || calls[0].Sorted {
					t.Fatalf("the repository was counted with %+v; a page of a count is not a count", calls)
				}
			},
		},
		{
			name:   "a keyed read keeps the shaping and drops the rest",
			method: http.MethodGet, target: "/widgets/42?f=price:gte:100&limit=9",
			want: func(t *testing.T, cmds []command, calls []repoCall) {
				if len(cmds) != 1 || cmds[0].ID != 42 {
					t.Fatalf("the route made %+v, want one Get for 42", cmds)
				}
				if len(calls) != 1 || calls[0].Filter || calls[0].Limit != 0 {
					t.Fatalf("the repository was asked for one row with %+v", calls)
				}
			},
		},
		{
			name:   "a create is cleared below the binding",
			method: http.MethodPost, target: "/widgets",
			body: `{"id":999,"name":"bolt","createdAt":"2001-02-03T04:05:06Z"}`,
			want: func(t *testing.T, cmds []command, calls []repoCall) {
				if len(cmds) != 1 || cmds[0].Model.ID != 999 || cmds[0].Model.CreatedAt.IsZero() {
					t.Fatalf("the binding handed over %+v; clearing is the service's, and a binding that cleared first would hand over a zeroed model", cmds)
				}
				if len(calls) != 1 || calls[0].Model.ID != 0 || !calls[0].Model.CreatedAt.IsZero() {
					t.Fatalf("the repository was asked to write %+v, want the key and the generated column cleared", calls)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, b := range bindings {
				svc := newRecorder()
				if _, body := b.serve(t, svc, tc.method, tc.target, tc.body); len(body) == 0 {
					t.Fatalf("%s answered nothing", b.name)
				}
				t.Run(b.name, func(t *testing.T) { tc.want(t, svc.got, svc.repo.calls) })
			}
		})
	}
}

// ---------------------------------------------------------------------------
// a generated resource

// WidgetInput, WidgetMapper and widgetPaths are what `vv -adapter` writes: a
// wire shape of the resource's own, a mapper onto the model, and the inverse of
// that mapping. Written out by hand here because the generator cannot run
// against a model declared inside a test file.
type WidgetInput struct {
	ID    int64  `json:"id"`
	Name  string `json:"label"`
	Price int    `json:"price"`
}

type WidgetMapper struct{}

func (WidgetMapper) Model(_ context.Context, in WidgetInput) (Widget, error) {
	return Widget{ID: in.ID, Name: in.Name, Price: in.Price}, nil
}

func (WidgetMapper) Resolve(p errs.Path) (errs.Path, bool) { return widgetPaths.Resolve(p) }

var widgetPaths = port.MustPathMap[Widget](port.PathMap{
	"ID":    port.At("id"),
	"Name":  port.At("label"),
	"Price": port.At("price"),
})

// [[D-050]]'s control on the transports: the generated hop is wired the same
// way by all three bindings, so the same violation names the same client key
// wherever it is mounted.
//
// It lives here because this is the only package that can import Fiber, Gin and
// net/http at once, and it needs no database.
func TestAGeneratedResourceResolvesTheSameFieldOnAllThreeBindings(t *testing.T) {
	fault := func() error {
		return errs.Conflict().Code(errs.CodeUnique).
			Field("Name").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()
	}
	const body = `{"label":"bolt","price":250}`

	// The mapper's key is what the client sent, on every binding.
	mapped := map[string]string{}
	for _, b := range bindings {
		svc := newRecorder()
		svc.repo.err = fault()
		status, raw := b.mappedServe(t, svc, http.MethodPost, "/widgets", body)
		if status != http.StatusConflict {
			t.Fatalf("%s answered %d for a duplicate key: %s", b.name, status, raw)
		}
		mapped[b.name] = fieldOf(t, b.name, raw)
	}
	for name, got := range mapped {
		if got != "label" {
			t.Fatalf("%s rendered the field as %q, want the key the client sent", name, got)
		}
	}

	// The control. Mounted with New — Identity, no map — the same violation on
	// the same body has nothing to translate it: the body carries no key that
	// folds to "Name", so the raw-body index declines and the client is handed
	// the model's own field name back. Without this the test above passes for a
	// binding that never wired the hop at all.
	for _, b := range bindings {
		svc := newRecorder()
		svc.repo.err = fault()
		status, raw := b.serve(t, svc, http.MethodPost, "/widgets", body)
		if status != http.StatusConflict {
			t.Fatalf("%s answered %d for a duplicate key: %s", b.name, status, raw)
		}
		if got := fieldOf(t, b.name, raw); got != "Name" {
			t.Fatalf("%s without a map rendered %q; the generated map answering %q proves nothing unless the two differ",
				b.name, got, mapped[b.name])
		}
	}
}

// fieldOf reads the dotted field path out of the first validation violation.
func fieldOf(t *testing.T, binding string, raw []byte) string {
	t.Helper()
	var env struct {
		Errors struct {
			Validation []struct {
				Field []any `json:"field"`
			} `json:"validation"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("%s answered a body that is not the envelope: %v\n%s", binding, err, raw)
	}
	if len(env.Errors.Validation) != 1 {
		t.Fatalf("%s rendered %d validation violations, want one: %s", binding, len(env.Errors.Validation), raw)
	}
	parts := make([]string, 0, len(env.Errors.Validation[0].Field))
	for _, step := range env.Errors.Validation[0].Field {
		parts = append(parts, fmt.Sprint(step))
	}
	return strings.Join(parts, ".")
}
