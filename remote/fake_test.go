package remote_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudnet"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/port"
)

// The model and the fake are crudnet's, name for name. A client test that
// invented its own would be proving that the client agrees with itself; using
// the binding's own fixture means the two halves are being compared.

type Widget struct {
	ID        int64            `db:"id,pk,auto" json:"id"`
	OwnerID   int64            `db:"owner_id" json:"ownerId"`
	Name      string           `db:"name" json:"name"`
	Price     int              `db:"price" json:"price"`
	Note      crud.Opt[string] `db:"note" json:"note"`
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

// The tags are the ones cmd/vv writes: omitempty on the pointers, omitzero on
// the Opt. The Opt's is load-bearing rather than tidy — without it an undefined
// Opt marshals to null and a patch empties the column — and remote.New refuses
// a DTO that lacks it.
type WidgetUpdate struct {
	Name  *string          `json:"name,omitempty"`
	Price *int             `json:"price,omitempty"`
	Note  crud.Opt[string] `json:"note,omitzero"`
}

var widgetMeta = mustMeta()

func mustMeta() *crud.Meta {
	m, err := crud.NewMeta[Widget]("widgets")
	if err != nil {
		panic(err)
	}
	return m
}

var savedAt = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

// recordedCall is one repository call as the far side received it. Opts is the
// resolved option list, which is what a read test is about: whether the filter
// the caller wrote in Go arrived as the same narrowing.
type recordedCall struct {
	Method string
	Opts   *crud.Options
	ID     int64
	IDs    []int64
	Model  Widget
	DTO    WidgetUpdate
}

type fakeRepo struct {
	page    crud.PaginatedResponse[Widget]
	pages   map[int]crud.PaginatedResponse[Widget]
	offsets map[int]crud.PaginatedResponse[Widget]
	cursors map[string]crud.PaginatedResponse[Widget]
	all     []Widget
	one     Widget
	count   int64
	del     int64

	err error

	calls []recordedCall
}

var _ port.Repository[Widget, int64, WidgetUpdate] = (*fakeRepo)(nil)

func newFake() *fakeRepo {
	f := &fakeRepo{
		page: crud.NewPaginatedResponse([]Widget{
			{ID: 1, OwnerID: 7, Name: "bolt", Price: 250},
			{ID: 2, OwnerID: 7, Name: "nut", Price: 120},
		}, 2, 2, 5),
		one:   Widget{ID: 42, OwnerID: 7, Name: "bolt", Price: 250},
		count: 5,
		del:   1,
	}
	f.all = f.page.Items
	return f
}

func (f *fakeRepo) Meta() *crud.Meta { return widgetMeta }

func (f *fakeRepo) Get(_ context.Context, opts ...crud.Option) (crud.PaginatedResponse[Widget], error) {
	o := crud.Build(opts...)
	f.calls = append(f.calls, recordedCall{Method: "Get", Opts: o})
	if f.err != nil {
		return crud.PaginatedResponse[Widget]{}, f.err
	}
	if page, ok := f.cursors[cursorKey(o)]; ok {
		return page, nil
	}
	if page, ok := f.offsets[o.Offset]; ok {
		return page, nil
	}
	if page, ok := f.pages[o.Page]; ok {
		return page, nil
	}
	return f.page, nil
}

func cursorKey(o *crud.Options) string {
	if o.After != "" {
		return "after:" + o.After
	}
	if o.Before != "" {
		return "before:" + o.Before
	}
	return ""
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

func (f *fakeRepo) Save(_ context.Context, m *Widget) error {
	f.calls = append(f.calls, recordedCall{Method: "Save", Model: *m})
	if f.err != nil {
		return f.err
	}
	if m.ID == 0 {
		m.ID = 7
	}
	m.CreatedAt = savedAt
	return nil
}

func (f *fakeRepo) Update(_ context.Context, id int64, dto WidgetUpdate, _ ...crud.Option) (Widget, error) {
	f.calls = append(f.calls, recordedCall{Method: "Update", ID: id, DTO: dto})
	if f.err != nil {
		return Widget{}, f.err
	}
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

func (f *fakeRepo) Delete(_ context.Context, ids ...int64) (int64, error) {
	f.calls = append(f.calls, recordedCall{Method: "Delete", IDs: ids})
	if f.err != nil {
		return 0, f.err
	}
	return f.del, nil
}

// last is the call the far side received most recently.
func (f *fakeRepo) last(t *testing.T) recordedCall {
	t.Helper()
	if len(f.calls) == 0 {
		t.Fatal("the request never reached the repository")
	}
	return f.calls[len(f.calls)-1]
}

// serve mounts a real binding on a real server. Nothing here is a stub between
// the client and the handler: what the client writes is what net/http parses.
//
// There is no separate "every row" route over HTTP. remote.GetAll walks the
// ordinary List pages, which means an endpoint with a page cap remains
// consumable without granting unpaged reads.
func serve(t *testing.T, repo port.Repository[Widget, int64, WidgetUpdate], opts ...crudnet.Option[Widget, int64, WidgetUpdate]) string {
	t.Helper()
	mux := http.NewServeMux()
	opts = append([]crudnet.Option[Widget, int64, WidgetUpdate]{
		crudnet.WithQuery[Widget, int64, WidgetUpdate](&query.Config{AllowDistinct: true}),
	}, opts...)
	crudnet.New(repo, opts...).Mount(mux, "/widgets")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/widgets"
}
