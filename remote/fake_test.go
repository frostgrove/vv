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

func (this *fakeRepo) Meta() *crud.Meta { return widgetMeta }

func (this *fakeRepo) Get(_ context.Context, options ...crud.Option) (crud.PaginatedResponse[Widget], error) {
	o := crud.Build(options...)
	this.calls = append(this.calls, recordedCall{Method: "Get", Opts: o})
	if this.err != nil {
		return crud.PaginatedResponse[Widget]{}, this.err
	}
	if page, ok := this.cursors[cursorKey(o)]; ok {
		return page, nil
	}
	if page, ok := this.offsets[o.Offset]; ok {
		return page, nil
	}
	if page, ok := this.pages[o.Page]; ok {
		return page, nil
	}
	return this.page, nil
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

func (this *fakeRepo) GetAll(_ context.Context, options ...crud.Option) ([]Widget, error) {
	this.calls = append(this.calls, recordedCall{Method: "GetAll", Opts: crud.Build(options...)})
	if this.err != nil {
		return nil, this.err
	}
	return this.all, nil
}

func (this *fakeRepo) GetByID(_ context.Context, id int64, options ...crud.Option) (Widget, error) {
	this.calls = append(this.calls, recordedCall{Method: "GetByID", ID: id, Opts: crud.Build(options...)})
	if this.err != nil {
		return Widget{}, this.err
	}
	w := this.one
	w.ID = id
	return w, nil
}

func (this *fakeRepo) Count(_ context.Context, options ...crud.Option) (int64, error) {
	this.calls = append(this.calls, recordedCall{Method: "Count", Opts: crud.Build(options...)})
	if this.err != nil {
		return 0, this.err
	}
	return this.count, nil
}

func (this *fakeRepo) Save(_ context.Context, m *Widget) (Widget, error) {
	this.calls = append(this.calls, recordedCall{Method: "Save", Model: *m})
	if this.err != nil {
		return Widget{}, this.err
	}
	saved := *m
	if saved.ID == 0 {
		saved.ID = 7
	}
	saved.CreatedAt = savedAt
	return saved, nil
}

func (this *fakeRepo) Update(_ context.Context, id int64, dataTransferObject WidgetUpdate, _ ...crud.Option) (Widget, error) {
	this.calls = append(this.calls, recordedCall{Method: "Update", ID: id, DTO: dataTransferObject})
	if this.err != nil {
		return Widget{}, this.err
	}
	w := this.one
	w.ID = id
	if dataTransferObject.Name != nil {
		w.Name = *dataTransferObject.Name
	}
	if dataTransferObject.Price != nil {
		w.Price = *dataTransferObject.Price
	}
	if dataTransferObject.Note.IsDefined() {
		w.Note = dataTransferObject.Note
	}
	return w, nil
}

func (this *fakeRepo) Delete(_ context.Context, ids ...int64) (int64, error) {
	this.calls = append(this.calls, recordedCall{Method: "Delete", IDs: ids})
	if this.err != nil {
		return 0, this.err
	}
	return this.del, nil
}

// last is the call the far side received most recently.
func (this *fakeRepo) last(t *testing.T) recordedCall {
	t.Helper()
	if len(this.calls) == 0 {
		t.Fatal("the request never reached the repository")
	}
	return this.calls[len(this.calls)-1]
}

// serve mounts a real binding on a real server. Nothing here is a stub between
// the client and the handler: what the client writes is what net/http parses.
//
// There is no separate "every row" route over HTTP. remote.GetAll walks the
// ordinary List pages, which means an endpoint with a page cap remains
// consumable without granting unpaged reads.
func serve(t *testing.T, repository port.Repository[Widget, int64, WidgetUpdate], options ...crudnet.Option[Widget, int64, WidgetUpdate]) string {
	t.Helper()
	mux := http.NewServeMux()
	options = append([]crudnet.Option[Widget, int64, WidgetUpdate]{
		crudnet.WithQuery[Widget, int64, WidgetUpdate](&query.Config{AllowDistinct: true}),
	}, options...)
	crudnet.New(repository, options...).Mount(mux, "/widgets")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/widgets"
}
