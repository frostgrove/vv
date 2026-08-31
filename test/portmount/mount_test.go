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

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudfiber"
	"github.com/frostgrove/vv/crud/http/crudgin"
	"github.com/frostgrove/vv/crud/http/crudnet"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

type Widget struct {
	ID          int64      `db:"id,pk,auto" json:"id"`
	Name        string     `db:"name" json:"name"`
	Price       int        `db:"price" json:"price"`
	CreatedAt   time.Time  `db:"created_at,generated" json:"createdAt"`
	Version     int        `db:"version,version" json:"version,omitempty"`
	ServerStamp string     `db:"server_stamp,serverowned" json:"serverStamp,omitempty"`
	DeletedAt   *time.Time `db:"deleted_at,serverowned,tombstone" json:"deletedAt,omitempty"`
}

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

	err error
}

func (this *fakeRepo) Meta() *crud.Meta { return widgetMeta }

func (this *fakeRepo) record(method string, o *crud.Options) {
	this.calls = append(this.calls, repoCall{
		Method: method, Limit: o.Limit, Offset: o.Offset,
		Sorted: len(o.Sort) > 0, Filter: o.Predicate() != nil,
	})
}

func (this *fakeRepo) Get(_ context.Context, options ...crud.Option) (crud.PaginatedResponse[Widget], error) {
	this.record("Get", crud.Build(options...))
	return crud.NewPaginatedResponse([]Widget{{ID: 1, Name: "bolt", Price: 250, CreatedAt: savedAt}}, 1, 1, 1), nil
}

func (this *fakeRepo) GetAll(_ context.Context, options ...crud.Option) ([]Widget, error) {
	this.record("GetAll", crud.Build(options...))
	return nil, nil
}

func (this *fakeRepo) GetByID(_ context.Context, id int64, options ...crud.Option) (Widget, error) {
	o := crud.Build(options...)
	this.calls = append(this.calls, repoCall{
		Method: "GetByID", ID: id, Limit: o.Limit, Offset: o.Offset,
		Sorted: len(o.Sort) > 0, Filter: o.Predicate() != nil,
	})
	return Widget{ID: id, Name: "bolt", Price: 250, CreatedAt: savedAt}, nil
}

func (this *fakeRepo) Count(_ context.Context, options ...crud.Option) (int64, error) {
	this.record("Count", crud.Build(options...))
	return 5, nil
}

func (this *fakeRepo) Save(_ context.Context, m *Widget) (Widget, error) {
	this.calls = append(this.calls, repoCall{Method: "Save", Model: *m})
	if this.err != nil {
		return Widget{}, this.err
	}
	saved := *m
	saved.CreatedAt = savedAt
	if saved.ID == 0 {
		saved.ID = 7
	}
	return saved, nil
}

func (this *fakeRepo) Update(_ context.Context, id int64, _ WidgetUpdate, _ ...crud.Option) (Widget, error) {
	this.calls = append(this.calls, repoCall{Method: "Update", ID: id})
	return Widget{ID: id, Name: "patched", CreatedAt: savedAt}, nil
}

func (this *fakeRepo) Delete(_ context.Context, ids ...int64) (int64, error) {
	this.calls = append(this.calls, repoCall{Method: "Delete", IDs: ids})
	return int64(len(ids)), nil
}

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

type recorder struct {
	inner      *port.DefaultService[Widget, int64, WidgetUpdate]
	repository *fakeRepo
	got        []command
}

func newRecorder() *recorder {
	repository := &fakeRepo{}
	return &recorder{inner: port.NewService[Widget, int64, WidgetUpdate](repository), repository: repository}
}

func snap(request *query.Request) (query.Request, bool) {
	if request == nil {
		return query.Request{}, false
	}
	return *request, true
}

func (this *recorder) Meta() *crud.Meta { return this.inner.Meta() }

func (this *recorder) Paths() errs.Resolver { return this.inner.Paths() }

func (this *recorder) List(ctx context.Context, cmd port.ListCommand) (crud.PaginatedResponse[Widget], error) {
	q, ok := snap(cmd.Query)
	this.got = append(this.got, command{Verb: "List", Query: q, HasQuery: ok, Options: len(cmd.Options)})
	return this.inner.List(ctx, cmd)
}

func (this *recorder) Count(ctx context.Context, cmd port.CountCommand) (int64, error) {
	q, ok := snap(cmd.Query)
	this.got = append(this.got, command{Verb: "Count", Query: q, HasQuery: ok, Options: len(cmd.Options)})
	return this.inner.Count(ctx, cmd)
}

func (this *recorder) Get(ctx context.Context, cmd port.GetCommand[int64]) (Widget, error) {
	q, ok := snap(cmd.Query)
	this.got = append(this.got, command{Verb: "Get", Query: q, HasQuery: ok, ID: cmd.ID, Options: len(cmd.Options)})
	return this.inner.Get(ctx, cmd)
}

func (this *recorder) Create(ctx context.Context, cmd port.CreateCommand[Widget]) (Widget, error) {
	this.got = append(this.got, command{Verb: "Create", Model: cmd.Model, Hook: cmd.Before != nil})
	return this.inner.Create(ctx, cmd)
}

func (this *recorder) Update(ctx context.Context, cmd port.UpdateCommand[int64, WidgetUpdate]) (Widget, error) {
	this.got = append(this.got, command{Verb: "Update", ID: cmd.ID, Patched: cmd.Patch.Name != nil, Hook: cmd.Before != nil})
	return this.inner.Update(ctx, cmd)
}

func (this *recorder) Replace(ctx context.Context, cmd port.ReplaceCommand[int64, Widget]) (Widget, error) {
	this.got = append(this.got, command{Verb: "Replace", ID: cmd.ID, Model: cmd.Model, Hook: cmd.Before != nil})
	return this.inner.Replace(ctx, cmd)
}

func (this *recorder) Delete(ctx context.Context, cmd port.DeleteCommand[int64]) (int64, error) {
	this.got = append(this.got, command{Verb: "Delete", ID: cmd.ID})
	return this.inner.Delete(ctx, cmd)
}

func (this *recorder) DeleteMany(ctx context.Context, cmd port.BulkDeleteCommand[int64]) (int64, error) {
	this.got = append(this.got, command{Verb: "DeleteMany", IDs: cmd.IDs})
	return this.inner.DeleteMany(ctx, cmd)
}

type binding struct {
	name  string
	serve func(t *testing.T, service port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte)

	mappedServe func(t *testing.T, service port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte)
}

func request(method, target, body string) *http.Request {
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

var bindings = []binding{
	{
		name: "crudnet",
		serve: func(t *testing.T, service port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte) {
			t.Helper()
			mux := http.NewServeMux()
			crudnet.Serving(service).Mount(mux, "/widgets")
			return throughMux(mux, method, target, body)
		},
		mappedServe: func(t *testing.T, service port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte) {
			t.Helper()
			mux := http.NewServeMux()
			crudnet.ServingFor(service, WidgetMapper{}).Mount(mux, "/widgets")
			return throughMux(mux, method, target, body)
		},
	},
	{
		name: "crudgin",
		serve: func(t *testing.T, service port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte) {
			t.Helper()
			r := gin.New()
			crudgin.Serving(service).Mount(r, "/widgets")
			return throughMux(r, method, target, body)
		},
		mappedServe: func(t *testing.T, service port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte) {
			t.Helper()
			r := gin.New()
			crudgin.ServingFor(service, WidgetMapper{}).Mount(r, "/widgets")
			return throughMux(r, method, target, body)
		},
	},
	{
		name: "crudfiber",
		serve: func(t *testing.T, service port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte) {
			t.Helper()
			app := fiber.New()
			app.Use("/widgets", crudfiber.Serving(service).Routes())
			return throughFiber(t, app, method, target, body)
		},
		mappedServe: func(t *testing.T, service port.Service[Widget, int64, WidgetUpdate], method, target, body string) (int, []byte) {
			t.Helper()
			app := fiber.New()
			app.Use("/widgets", crudfiber.ServingFor(service, WidgetMapper{}).Routes())
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
	response, err := app.Test(request(method, target, body), fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("crudfiber: %s %s: %v", method, target, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("crudfiber: reading the response: %v", err)
	}
	return response.StatusCode, raw
}

func TestOneServiceMountsOnAllThreeBindings(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
	}{
		{"a list with paging and a filter", http.MethodGet, "/widgets?page=2&limit=5&f=price:gte:100&sort=-price", ""},
		{"a list through the JSON document", http.MethodPost, "/widgets/query", `{"limit":3,"sort":["-price"]}`},
		{"a count with the paging a count must drop", http.MethodGet, "/widgets/count?page=2&limit=5&sort=-price&f=price:gte:100", ""},
		{"a count through the JSON document", http.MethodPost, "/widgets/count", `{"page":4,"limit":9}`},
		{"one entity, with the shaping a keyed read keeps", http.MethodGet, "/widgets/42?select=name&f=price:gte:100&limit=9", ""},
		{"a create carrying what it may not choose", http.MethodPost, "/widgets", `{"id":999,"name":"bolt","price":250,"createdAt":"2001-02-03T04:05:06Z","version":99,"serverStamp":"forged","deletedAt":"2001-02-03T04:05:06Z"}`},
		{"a patch", http.MethodPatch, "/widgets/42", `{"name":"patched"}`},
		{"a replace", http.MethodPut, "/widgets/42", `{"id":999,"name":"replaced","version":99,"serverStamp":"forged","deletedAt":"2001-02-03T04:05:06Z"}`},
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
				service := newRecorder()
				gotStatus, gotBody := b.serve(t, service, tc.method, tc.target, tc.body)
				if i == 0 {
					first, status, body, cmds, calls = b, gotStatus, gotBody, service.got, service.repository.calls
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
				if !reflect.DeepEqual(service.got, cmds) {
					t.Fatalf("%s handed the service %+v and %s handed it %+v — one of them is re-deriving a rule the service owns",
						first.name, cmds, b.name, service.got)
				}
				if !reflect.DeepEqual(service.repository.calls, calls) {
					t.Fatalf("under %s the service called the repository with %+v and under %s with %+v",
						first.name, calls, b.name, service.repository.calls)
				}
			}
		})
	}
}

func TestTheServiceIsWhereTheRulesRan(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
		want                       func(*testing.T, []command, []repoCall)
	}{
		{
			name: "the binding hands over the document it parsed, unnarrowed",

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
			name:   "a keyed read keeps its filter and drops paging",
			method: http.MethodGet, target: "/widgets/42?f=price:gte:100&limit=9",
			want: func(t *testing.T, cmds []command, calls []repoCall) {
				if len(cmds) != 1 || cmds[0].ID != 42 {
					t.Fatalf("the route made %+v, want one Get for 42", cmds)
				}
				if len(calls) != 1 || !calls[0].Filter || calls[0].Limit != 0 {
					t.Fatalf("the repository was asked for one row with %+v", calls)
				}
			},
		},
		{
			name:   "a create is cleared below the binding",
			method: http.MethodPost, target: "/widgets",
			body: `{"id":999,"name":"bolt","createdAt":"2001-02-03T04:05:06Z","version":99,"serverStamp":"forged","deletedAt":"2001-02-03T04:05:06Z"}`,
			want: func(t *testing.T, cmds []command, calls []repoCall) {
				if len(cmds) != 1 || cmds[0].Model.ID != 999 || cmds[0].Model.CreatedAt.IsZero() ||
					cmds[0].Model.Version != 99 || cmds[0].Model.ServerStamp != "forged" || cmds[0].Model.DeletedAt == nil {
					t.Fatalf("the binding handed over %+v; clearing is the service's, and a binding that cleared first would hand over a zeroed model", cmds)
				}
				if len(calls) != 1 || calls[0].Model.ID != 0 || !calls[0].Model.CreatedAt.IsZero() ||
					calls[0].Model.Version != 0 || calls[0].Model.ServerStamp != "" || calls[0].Model.DeletedAt != nil {
					t.Fatalf("the repository was asked to write %+v, want every non-client field cleared", calls)
				}
			},
		},
		{
			name:   "a replace is sanitised below the binding before its path key wins",
			method: http.MethodPut, target: "/widgets/42",
			body: `{"id":999,"name":"replaced","version":99,"serverStamp":"forged","deletedAt":"2001-02-03T04:05:06Z"}`,
			want: func(t *testing.T, cmds []command, calls []repoCall) {
				if len(cmds) != 1 || cmds[0].Model.ID != 999 || cmds[0].Model.Version != 99 ||
					cmds[0].Model.ServerStamp != "forged" || cmds[0].Model.DeletedAt == nil {
					t.Fatalf("the binding did not hand the raw replacement to the application service: %+v", cmds)
				}
				if len(calls) != 2 || calls[0].Method != "GetByID" || calls[1].Method != "Save" ||
					calls[1].Model.ID != 42 || calls[1].Model.Version != 0 ||
					calls[1].Model.ServerStamp != "" || calls[1].Model.DeletedAt != nil {
					t.Fatalf("replace repository calls = %+v, want probe then a sanitised row keyed from the path", calls)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, b := range bindings {
				service := newRecorder()
				if _, body := b.serve(t, service, tc.method, tc.target, tc.body); len(body) == 0 {
					t.Fatalf("%s answered nothing", b.name)
				}
				t.Run(b.name, func(t *testing.T) { tc.want(t, service.got, service.repository.calls) })
			}
		})
	}
}

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

func TestAGeneratedResourceResolvesTheSameFieldOnAllThreeBindings(t *testing.T) {
	fault := func() error {
		return errs.Conflict().Code(errs.CodeUnique).
			Field("Name").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()
	}
	const body = `{"label":"bolt","price":250}`

	mapped := map[string]string{}
	for _, b := range bindings {
		service := newRecorder()
		service.repository.err = fault()
		status, raw := b.mappedServe(t, service, http.MethodPost, "/widgets", body)
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

	for _, b := range bindings {
		service := newRecorder()
		service.repository.err = fault()
		status, raw := b.serve(t, service, http.MethodPost, "/widgets", body)
		if status != http.StatusConflict {
			t.Fatalf("%s answered %d for a duplicate key: %s", b.name, status, raw)
		}
		if got := fieldOf(t, b.name, raw); got != "Name" {
			t.Fatalf("%s without a map rendered %q; the generated map answering %q proves nothing unless the two differ",
				b.name, got, mapped[b.name])
		}
	}
}

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
