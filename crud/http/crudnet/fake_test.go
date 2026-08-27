package crudnet

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
)

// ---------------------------------------------------------------------------
// the model

// Widget is the model every test in this package drives the handler with. Each
// part of it earns its place: the auto primary key and the `generated` column
// are what a create request must not be allowed to dictate, Secret is what a
// presenter has to hide, and the two relations are what a preload resolves.
type Widget struct {
	ID        int64            `db:"id,pk,auto" json:"id"`
	OwnerID   int64            `db:"owner_id" json:"ownerId"`
	Name      string           `db:"name" json:"name"`
	Price     int              `db:"price" json:"price"`
	Note      crud.Opt[string] `db:"note" json:"note"`
	Secret    string           `db:"secret" json:"secret"`
	CreatedAt time.Time        `db:"created_at,generated" json:"createdAt"`

	Owner *Owner `rel:"belongs_to" json:"owner,omitempty"`
	Parts []Part `rel:"has_many" json:"parts,omitempty"`
}

type Owner struct {
	ID   int64  `db:"id,pk,auto" json:"id"`
	Name string `db:"name" json:"name"`
}

type Part struct {
	ID       int64  `db:"id,pk,auto" json:"id"`
	WidgetID int64  `db:"widget_id" json:"widgetId"`
	Label    string `db:"label" json:"label"`
}

// WidgetUpdate is the PATCH DTO: pointers for the two-state columns and an Opt
// for the nullable one, so "absent" and "explicitly null" stay distinguishable
// all the way from the JSON body to the repository.
type WidgetUpdate struct {
	Name  *string          `json:"name,omitempty"`
	Price *int             `json:"price,omitempty"`
	Note  crud.Opt[string] `json:"note,omitzero"`
}

// widgetMeta is what the fake reports from Meta(). Building it directly, rather
// than through a repository, is what keeps this module's tests database-free.
var widgetMeta = mustMeta()

func mustMeta() *crud.Meta {
	m, err := crud.NewMeta[Widget]("widgets")
	if err != nil {
		panic(err)
	}
	return m
}

// savedAt is the value the fake writes back into the `generated` column, the
// way a database default would.
var savedAt = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

// ---------------------------------------------------------------------------
// the fake repository

// recordedCall is one repository call exactly as the handler made it. Opts is
// the resolved option list — the handler's real output, since compiling a
// request into repository options is the whole of its job on a read.
type recordedCall struct {
	Method string
	Opts   *crud.Options
	ID     int64
	IDs    []int64
	Model  Widget
	DTO    WidgetUpdate
}

// fakeRepo is an in-memory Repository. It answers with canned values and
// records what it was asked for, so a test can assert the request the handler
// made instead of the SQL some database would have run.
type fakeRepo struct {
	page  crud.PaginatedResponse[Widget]
	all   []Widget
	one   Widget
	count int64

	// err, when set, fails every method — the seam for error-mapping tests.
	err error

	// onSave stands in for the database assigning a key and filling generated
	// columns; onUpdate applies the DTO the way a repository would.
	onSave   func(*Widget) error
	onUpdate func(int64, WidgetUpdate) (Widget, error)

	calls []recordedCall
}

var _ Repository[Widget, int64, WidgetUpdate] = (*fakeRepo)(nil)

func newFake() *fakeRepo {
	f := &fakeRepo{
		page: crud.NewPaginatedResponse([]Widget{
			{ID: 1, OwnerID: 7, Name: "bolt", Price: 250, Secret: "swordfish"},
			{ID: 2, OwnerID: 7, Name: "nut", Price: 120, Secret: "hunter2"},
		}, 2, 2, 5),
		one:   Widget{ID: 42, OwnerID: 7, Name: "bolt", Price: 250, Secret: "swordfish"},
		count: 5,
	}
	f.all = f.page.Items
	f.onSave = func(w *Widget) error {
		if w.ID == 0 {
			w.ID = 7
		}
		w.CreatedAt = savedAt
		return nil
	}
	f.onUpdate = func(id int64, dto WidgetUpdate) (Widget, error) {
		w := f.one
		w.ID = id
		if dto.Name != nil {
			w.Name = *dto.Name
		}
		if dto.Price != nil {
			w.Price = *dto.Price
		}
		if dto.Note.IsDefined() {
			w.Note = dto.Note
		}
		return w, nil
	}
	return f
}

func (f *fakeRepo) Meta() *crud.Meta { return widgetMeta }

func (f *fakeRepo) Get(_ context.Context, opts ...crud.Option) (crud.PaginatedResponse[Widget], error) {
	f.calls = append(f.calls, recordedCall{Method: "Get", Opts: crud.Build(opts...)})
	if f.err != nil {
		return crud.PaginatedResponse[Widget]{}, f.err
	}
	return f.page, nil
}

func (f *fakeRepo) GetAll(_ context.Context, opts ...crud.Option) ([]Widget, error) {
	f.calls = append(f.calls, recordedCall{Method: "GetAll", Opts: crud.Build(opts...)})
	if f.err != nil {
		return nil, f.err
	}
	return f.all, nil
}

func (f *fakeRepo) GetByID(_ context.Context, id int64, opts ...crud.Option) (Widget, error) {
	f.calls = append(f.calls, recordedCall{Method: "GetByID", ID: id, Opts: crud.Build(opts...)})
	if f.err != nil {
		return Widget{}, f.err
	}
	w := f.one
	w.ID = id
	return w, nil
}

func (f *fakeRepo) Count(_ context.Context, opts ...crud.Option) (int64, error) {
	f.calls = append(f.calls, recordedCall{Method: "Count", Opts: crud.Build(opts...)})
	if f.err != nil {
		return 0, f.err
	}
	return f.count, nil
}

// Save records the model as it arrived — before onSave touches it — because
// what the handler handed over is exactly what the write tests are about.
func (f *fakeRepo) Save(_ context.Context, m *Widget) (Widget, error) {
	f.calls = append(f.calls, recordedCall{Method: "Save", Model: *m})
	if f.err != nil {
		return Widget{}, f.err
	}
	saved := *m
	if err := f.onSave(&saved); err != nil {
		return Widget{}, err
	}
	return saved, nil
}

func (f *fakeRepo) Update(_ context.Context, id int64, dto WidgetUpdate, _ ...crud.Option) (Widget, error) {
	f.calls = append(f.calls, recordedCall{Method: "Update", ID: id, DTO: dto})
	if f.err != nil {
		return Widget{}, f.err
	}
	return f.onUpdate(id, dto)
}

func (f *fakeRepo) Delete(_ context.Context, ids ...int64) (int64, error) {
	f.calls = append(f.calls, recordedCall{Method: "Delete", IDs: ids})
	if f.err != nil {
		return 0, f.err
	}
	return int64(len(ids)), nil
}

// only returns the one call the handler made to method, failing when it made
// none or several.
func (f *fakeRepo) only(t *testing.T, method string) recordedCall {
	t.Helper()
	var found []recordedCall
	for _, c := range f.calls {
		if c.Method == method {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 1:
		return found[0]
	case 0:
		t.Fatalf("the handler never called %s; it called %v", method, f.methods())
	default:
		t.Fatalf("the handler called %s %d times, expected once", method, len(found))
	}
	return recordedCall{}
}

// methods lists the repository calls in the order they were made.
func (f *fakeRepo) methods() []string {
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.Method
	}
	return out
}

// ---------------------------------------------------------------------------
// driving the handler

// mount builds a handler over a fresh fake and mounts it the way the package
// documentation says to.
func mount(t *testing.T, opts ...Option[Widget, int64, WidgetUpdate]) (*http.ServeMux, *fakeRepo) {
	t.Helper()
	f := newFake()
	mux := http.NewServeMux()
	New[Widget, int64, WidgetUpdate](f, opts...).Mount(mux, "/widgets")
	return mux, f
}

type response struct {
	status int
	body   []byte
	// header is what a status carries besides a body — Retry-After on a 503 is
	// the only one the library sets, and a test that could not see it would
	// have to trust the renderer's word for it.
	header http.Header
}

func (r response) decode(t *testing.T, into any) {
	t.Helper()
	if err := json.Unmarshal(r.body, into); err != nil {
		t.Fatalf("the response body is not the JSON a client would parse: %v in %s", err, r.body)
	}
}

// do sends one request through the mounted mux. A non-empty body is sent as
// JSON, which is the only body encoding this binding accepts.
func do(t *testing.T, mux *http.ServeMux, method, target, body string) response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return response{status: w.Code, body: w.Body.Bytes(), header: w.Header()}
}

// ok sends a request and insists it succeeded, so a test that is about what
// reached the repository fails with the server's own words when it did not.
func ok(t *testing.T, mux *http.ServeMux, method, target, body string, want int) response {
	t.Helper()
	r := do(t, mux, method, target, body)
	if r.status != want {
		t.Fatalf("%s %s answered %d, want %d: %s", method, target, r.status, want, r.body)
	}
	return r
}

// ---------------------------------------------------------------------------
// inspecting compiled options

// whereSQL renders the filter that reached the repository. The predicate AST is
// closed, so its SQL is the only honest way to state what a request compiled to
// — and it is what the database would have been asked, which is the point.
func whereSQL(t *testing.T, o *crud.Options) (string, []any) {
	t.Helper()
	return predSQL(t, widgetMeta, o.Predicate())
}

func predSQL(t *testing.T, m *crud.Meta, p crud.Predicate) (string, []any) {
	t.Helper()
	if p == nil {
		return "", nil
	}
	sql, args, err := crud.NewSQL(crud.Postgres{}, m).Predicate(p).Done()
	if err != nil {
		t.Fatalf("the compiled filter does not resolve against %s: %v", m.Name, err)
	}
	return sql, args
}

func preloadPaths(o *crud.Options) []string {
	out := make([]string, len(o.Preloads))
	for i, p := range o.Preloads {
		out[i] = p.Path
	}
	return out
}

// relMeta resolves the model on the far side of a relation, so a preload's own
// filter can be rendered against the table it will actually run on.
func relMeta(t *testing.T, path string) *crud.Meta {
	t.Helper()
	rel, _, err := widgetMeta.RelationAt(path)
	if err != nil {
		t.Fatalf("Widget has no relation %s: %v", path, err)
	}
	target, _, _, err := rel.Resolve()
	if err != nil {
		t.Fatalf("relation %s does not resolve: %v", path, err)
	}
	return target
}

// sortTerms spells the compiled sort the way a reader thinks of it: "-Price"
// for descending, canonical field names throughout.
func sortTerms(o *crud.Options) []string {
	out := make([]string, len(o.Sort))
	for i, s := range o.Sort {
		out[i] = s.Field
		if s.Desc {
			out[i] = "-" + s.Field
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// the port layer

// widgetInput is a request body that is not the model: it calls the model's
// Name column "label" and leaves "price" alone. One renamed field and one not
// renamed is what makes the path-chain control in edge_test.go mean something.
type widgetInput struct {
	Label string `json:"label"`
	Price int    `json:"price"`
}

// widgetMapper is the resource adapter — it turns this transport's shape into
// the model before the service sees it.
type widgetMapper struct{}

func (widgetMapper) Model(_ context.Context, in widgetInput) (Widget, error) {
	return Widget{Name: in.Label, Price: in.Price}, nil
}

// mountHandler mounts a handler the caller built, for the tests that use a
// constructor other than New.
func mountHandler[In any](h *HandlerFor[Widget, int64, WidgetUpdate, In]) *http.ServeMux {
	mux := http.NewServeMux()
	h.Mount(mux, "/widgets")
	return mux
}
