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

	"github.com/shardit-io/vv/crud"
)

// ---------------------------------------------------------------------------
// the model

// Widget is the model every test in this package drives the handler with, the
// same shape the three HTTP bindings use: the auto primary key and the
// `generated` column are what a create request must not be allowed to dictate,
// Secret is what a presenter has to hide, and Note is the nullable column whose
// three states have to survive a fourth transport.
type Widget struct {
	ID        int64            `db:"id,pk,auto" json:"id"`
	Name      string           `db:"name" json:"name"`
	Price     int              `db:"price" json:"price"`
	Note      crud.Opt[string] `db:"note" json:"note"`
	Secret    string           `db:"secret" json:"secret"`
	CreatedAt time.Time        `db:"created_at,generated" json:"createdAt"`
}

// WidgetUpdate is the patch DTO: a pointer for the two-state column and an Opt
// for the nullable one, so "absent" and "explicitly null" stay distinguishable
// all the way from the request document to the repository.
type WidgetUpdate struct {
	Name  *string          `json:"name"`
	Price *int             `json:"price"`
	Note  crud.Opt[string] `json:"note"`
}

var widgetMeta = func() *crud.Meta {
	m, err := crud.NewMeta[Widget]("widgets")
	if err != nil {
		panic(err)
	}
	return m
}()

// savedAt is what the fake writes into the `generated` column, the way a
// database default would.
var savedAt = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

// ---------------------------------------------------------------------------
// the fake repository

type recordedCall struct {
	Method string
	Opts   *crud.Options
	ID     int64
	IDs    []int64
	Model  Widget
	DTO    WidgetUpdate
}

type fakeRepo struct {
	page  crud.PaginatedResponse[Widget]
	one   Widget
	count int64

	// err, when set, fails every method — the seam for error-mapping tests.
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
	return f.page.Items, nil
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

// Save records the model as it arrived — before the key is filled in — because
// what the handler handed over is exactly what the write tests are about.
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

func (f *fakeRepo) methods() []string {
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.Method
	}
	return out
}

// ---------------------------------------------------------------------------
// driving the handler over a real connection

// The resource name every test registers under, so a full method name is
// spelled once.
const resource = "Widget"

// serve runs a handler on an in-process gRPC server and answers a client that
// calls it by full method name — no generated stub, which is the point of the
// Struct shape.
func serve(t *testing.T, desc *grpc.ServiceDesc, opts ...grpc.ServerOption) *client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(opts...)
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

// mount builds a handler over a fresh fake and serves it the way the package
// documentation says to.
func mount(t *testing.T, opts ...Option[Widget, int64, WidgetUpdate]) (*client, *fakeRepo) {
	t.Helper()
	f := newFake()
	return serve(t, New[Widget, int64, WidgetUpdate](f, opts...).Desc(resource)), f
}

type client struct {
	t       *testing.T
	conn    *grpc.ClientConn
	service string
	md      metadata.MD
}

// with answers a client that sends the given metadata on every call.
func (c *client) with(md metadata.MD) *client {
	out := *c
	out.md = md
	return &out
}

// call sends one request and hands back the answer and the status. Both, always:
// a test that only ever saw the error could not tell an empty answer from a
// missing one.
func (c *client) call(method string, in *structpb.Struct) (*structpb.Struct, *status.Status) {
	c.t.Helper()
	ctx := context.Background()
	if len(c.md) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, c.md)
	}
	out := &structpb.Struct{}
	err := c.conn.Invoke(ctx, "/"+c.service+"/"+method, in, out)
	if err == nil {
		return out, nil
	}
	st, ok := status.FromError(err)
	if !ok {
		c.t.Fatalf("%s answered an error with no status: %v", method, err)
	}
	return nil, st
}

// ok sends a request and insists it succeeded, so a test that is about the
// answer fails with the server's own words when there was none.
func (c *client) ok(method string, in *structpb.Struct) *structpb.Struct {
	c.t.Helper()
	out, st := c.call(method, in)
	if st != nil {
		c.t.Fatalf("%s answered %s: %s", method, st.Code(), st.Message())
	}
	return out
}

// fails sends a request and insists it did not succeed.
func (c *client) fails(method string, in *structpb.Struct) *status.Status {
	c.t.Helper()
	out, st := c.call(method, in)
	if st == nil {
		c.t.Fatalf("%s succeeded with %v, want a failure", method, out.AsMap())
	}
	return st
}

// doc builds a request document out of literal JSON, so a test reads like the
// call a client makes.
func doc(t *testing.T, raw string) *structpb.Struct {
	t.Helper()
	st := &structpb.Struct{}
	if err := st.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatalf("the fixture %s is not a JSON object: %v", raw, err)
	}
	return st
}
