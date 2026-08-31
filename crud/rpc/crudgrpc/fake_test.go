package crudgrpc

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostgrove/vv/crud"
)

type Widget struct {
	ID        int64            `db:"id,pk,auto" json:"id"`
	Name      string           `db:"name" json:"name"`
	Price     int              `db:"price" json:"price"`
	Note      crud.Opt[string] `db:"note" json:"note"`
	Secret    string           `db:"secret" json:"secret"`
	CreatedAt time.Time        `db:"created_at,generated" json:"createdAt"`
	Parts     []Part           `rel:"has_many" json:"-"`
}

type Part struct {
	ID       int64  `db:"id,pk,auto" json:"id"`
	WidgetID int64  `db:"widget_id" json:"widgetId"`
	Label    string `db:"label" json:"label"`
}

type WidgetUpdate struct {
	Name  *string          `json:"name,omitempty"`
	Price *int             `json:"price,omitempty"`
	Note  crud.Opt[string] `json:"note,omitzero"`
}

var widgetMeta = func() *crud.Meta {
	m, err := crud.NewMeta[Widget]("widgets")
	if err != nil {
		panic(err)
	}
	return m
}()

var savedAt = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

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
	cursors map[string]crud.PaginatedResponse[Widget]
	one     Widget
	count   int64

	err error

	calls []recordedCall
}

var _ Repository[Widget, int64, WidgetUpdate] = (*fakeRepo)(nil)

func newFake() *fakeRepo {
	f := &fakeRepo{
		page: crud.NewPaginatedResponse([]Widget{
			{ID: 1, Name: "bolt", Price: 250, Secret: "swordfish"},
			{ID: 2, Name: "nut", Price: 120, Secret: "hunter2"},
		}, 2, 2, 5),
		one:   Widget{ID: 42, Name: "bolt", Price: 250, Secret: "swordfish"},
		count: 5,
	}
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
	return this.page.Items, nil
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
	return int64(len(ids)), nil
}

func (this *fakeRepo) only(t *testing.T, method string) recordedCall {
	t.Helper()
	var found []recordedCall
	for _, c := range this.calls {
		if c.Method == method {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 1:
		return found[0]
	case 0:
		t.Fatalf("the handler never called %s; it called %v", method, this.methods())
	default:
		t.Fatalf("the handler called %s %d times, expected once", method, len(found))
	}
	return recordedCall{}
}

func (this *fakeRepo) methods() []string {
	out := make([]string, len(this.calls))
	for i, c := range this.calls {
		out[i] = c.Method
	}
	return out
}

const resource = "Widget"

func serve(t *testing.T, desc *grpc.ServiceDesc, options ...grpc.ServerOption) *client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(options...)
	srv.RegisterService(desc, nil)
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialling the in-process server: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
		_ = lis.Close()
	})
	return &client{t: t, conn: conn, service: desc.ServiceName}
}

func mount(t *testing.T, options ...Option[Widget, int64, WidgetUpdate]) (*client, *fakeRepo) {
	t.Helper()
	f := newFake()
	return serve(t, New[Widget, int64, WidgetUpdate](f, options...).Desc(resource)), f
}

type client struct {
	t       *testing.T
	conn    *grpc.ClientConn
	service string
	md      metadata.MD
}

func (this *client) with(md metadata.MD) *client {
	out := *this
	out.md = md
	return &out
}

func (this *client) call(method string, in *structpb.Struct) (*structpb.Struct, *status.Status) {
	this.t.Helper()
	ctx := context.Background()
	if len(this.md) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, this.md)
	}
	out := &structpb.Struct{}
	err := this.conn.Invoke(ctx, "/"+this.service+"/"+method, in, out)
	if err == nil {
		return out, nil
	}
	st, ok := status.FromError(err)
	if !ok {
		this.t.Fatalf("%s answered an error with no status: %v", method, err)
	}
	return nil, st
}

func (this *client) ok(method string, in *structpb.Struct) *structpb.Struct {
	this.t.Helper()
	out, st := this.call(method, in)
	if st != nil {
		this.t.Fatalf("%s answered %s: %s", method, st.Code(), st.Message())
	}
	return out
}

func (this *client) fails(method string, in *structpb.Struct) *status.Status {
	this.t.Helper()
	out, st := this.call(method, in)
	if st == nil {
		this.t.Fatalf("%s succeeded with %v, want a failure", method, out.AsMap())
	}
	return st
}

func doc(t *testing.T, raw string) *structpb.Struct {
	t.Helper()
	st := &structpb.Struct{}
	if err := st.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatalf("the fixture %s is not a JSON object: %v", raw, err)
	}
	return st
}
