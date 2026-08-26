package port

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
)

// ---------------------------------------------------------------------------
// fixtures

// widget has the two things a create request is not allowed to dictate: a key
// the database generates, and a column it fills.
type widget struct {
	ID        int64     `db:"id,pk,auto" json:"id"`
	Name      string    `db:"name" json:"name"`
	Price     int       `db:"price" json:"price"`
	CreatedAt time.Time `db:"created_at,generated" json:"createdAt"`
}

type widgetUpdate struct {
	Name *string `json:"name"`
}

var widgetMeta = func() *crud.Meta {
	m, err := crud.NewMeta[widget]("widgets")
	if err != nil {
		panic(err)
	}
	return m
}()

var forged = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

type call struct {
	method string
	id     int64
	ids    []int64
	model  widget
	dto    widgetUpdate
	opts   *crud.Options
}

// fakeRepo records what the service asked for, so a test can assert the request
// the service made rather than the SQL some database would have run.
type fakeRepo struct {
	calls   []call
	deleted int64
	err     error
}

func (f *fakeRepo) Meta() *crud.Meta { return widgetMeta }

func (f *fakeRepo) Get(_ context.Context, opts ...crud.Option) (crud.PaginatedResponse[widget], error) {
	f.calls = append(f.calls, call{method: "Get", opts: crud.Build(opts...)})
	return crud.PaginatedResponse[widget]{}, f.err
}

func (f *fakeRepo) GetAll(_ context.Context, opts ...crud.Option) ([]widget, error) {
	f.calls = append(f.calls, call{method: "GetAll", opts: crud.Build(opts...)})
	return nil, f.err
}

func (f *fakeRepo) GetByID(_ context.Context, id int64, opts ...crud.Option) (widget, error) {
	f.calls = append(f.calls, call{method: "GetByID", id: id, opts: crud.Build(opts...)})
	return widget{ID: id}, f.err
}

func (f *fakeRepo) Count(_ context.Context, opts ...crud.Option) (int64, error) {
	f.calls = append(f.calls, call{method: "Count", opts: crud.Build(opts...)})
	return 0, f.err
}

func (f *fakeRepo) Save(_ context.Context, m *widget) error {
	f.calls = append(f.calls, call{method: "Save", model: *m})
	return f.err
}

func (f *fakeRepo) Update(_ context.Context, id int64, dto widgetUpdate, _ ...crud.Option) (widget, error) {
	f.calls = append(f.calls, call{method: "Update", id: id, dto: dto})
	return widget{ID: id}, f.err
}

func (f *fakeRepo) Delete(_ context.Context, ids ...int64) (int64, error) {
	f.calls = append(f.calls, call{method: "Delete", ids: ids})
	return f.deleted, f.err
}

func (f *fakeRepo) methods() []string {
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.method
	}
	return out
}

func (f *fakeRepo) only(t *testing.T, method string) call {
	t.Helper()
	var found []call
	for _, c := range f.calls {
		if c.method == method {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the service called %s %d times, expected once; it called %v", method, len(found), f.methods())
	}
	return found[0]
}

// fakeService is a Service that is not the default one — what a generator will
// write, and what the mount control mounts on three bindings.
type fakeService struct {
	*DefaultService[widget, int64, widgetUpdate]
	paths errs.Resolver
}

func (s *fakeService) Paths() errs.Resolver { return s.paths }

// mappingIn is a mapper that also declares its hop, which is the adapter's half
// of the path chain.
type mappingIn struct{}

func (mappingIn) Model(_ context.Context, in widget) (widget, error) { return in, nil }

func (mappingIn) Resolve(p errs.Path) (errs.Path, bool) {
	return append(errs.Path{errs.Named("payload")}, p...), true
}

// recordingHop is a resolver that says whether it ran, for asserting that a
// pass-through hop does not stop the chain.
type recordingHop struct {
	prefix errs.Step
	ran    bool
}

func (h *recordingHop) Resolve(p errs.Path) (errs.Path, bool) {
	h.ran = true
	return append(errs.Path{h.prefix}, p...), true
}

// ---------------------------------------------------------------------------

// The whole of the write orchestration in one place, in the order the rest of
// the library documents. Every step is asserted against what the hook saw and
// what the repository was handed, so a reordering shows up as a wrong value
// rather than as a passing test.
func TestTheDefaultServiceAppliesTheRulesInOrder(t *testing.T) {
	t.Run("create clears the server-owned fields before the hook", func(t *testing.T) {
		repo := &fakeRepo{}
		svc := NewService[widget, int64, widgetUpdate](repo)

		var seen widget
		got, err := svc.Create(context.Background(), CreateCommand[widget]{
			Model: widget{ID: 999, Name: "bolt", CreatedAt: forged},
			Before: func(m *widget) error {
				seen = *m
				m.Price = 7
				return nil
			},
		})
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		if seen.ID != 0 {
			t.Fatalf("the hook was handed a client-chosen key of %d; it runs after the clearing, so it must see none", seen.ID)
		}
		if !seen.CreatedAt.IsZero() {
			t.Fatalf("the hook was handed a forged %v in a generated column", seen.CreatedAt)
		}
		stored := repo.only(t, "Save").model
		if stored.Price != 7 {
			t.Fatalf("the repository stored %+v; what the hook wrote is what is saved", stored)
		}
		if got.Price != 7 {
			t.Fatalf("the service answered %+v, want the row it saved", got)
		}
	})

	t.Run("and the control: with the key space handed over, the hook sees the client's key", func(t *testing.T) {
		repo := &fakeRepo{}
		svc := NewService[widget, int64, widgetUpdate](repo, AllowClientID())

		var seen widget
		if _, err := svc.Create(context.Background(), CreateCommand[widget]{
			Model:  widget{ID: 999, Name: "bolt", CreatedAt: forged},
			Before: func(m *widget) error { seen = *m; return nil },
		}); err != nil {
			t.Fatalf("creating: %v", err)
		}
		if seen.ID != 999 {
			t.Fatalf("with AllowClientID the hook saw id %d, want the 999 the client sent — otherwise the subtest above proves nothing about ordering", seen.ID)
		}
		// The generated column is cleared either way: AllowClientID is about
		// the key and nothing else.
		if !seen.CreatedAt.IsZero() {
			t.Fatalf("AllowClientID let a forged %v through in a generated column", seen.CreatedAt)
		}
	})

	t.Run("a hook that refuses stops the write", func(t *testing.T) {
		repo := &fakeRepo{}
		svc := NewService[widget, int64, widgetUpdate](repo)
		boom := errors.New("no")

		if _, err := svc.Create(context.Background(), CreateCommand[widget]{
			Before: func(*widget) error { return boom },
		}); !errors.Is(err, boom) {
			t.Fatalf("a refused create answered %v, want the hook's own error", err)
		}
		if len(repo.calls) != 0 {
			t.Fatalf("a refused create still reached the repository: %v", repo.methods())
		}
	})

	t.Run("replace looks the row up first, then clears, then sets the key, then hooks", func(t *testing.T) {
		repo := &fakeRepo{}
		svc := NewService[widget, int64, widgetUpdate](repo)

		var seen widget
		if _, err := svc.Replace(context.Background(), ReplaceCommand[int64, widget]{
			ID:     42,
			Model:  widget{ID: 999, Name: "replaced", CreatedAt: forged},
			Before: func(m *widget) error { seen = *m; return nil },
		}); err != nil {
			t.Fatalf("replacing: %v", err)
		}
		if got := repo.methods(); !slices.Equal(got, []string{"GetByID", "Save"}) {
			t.Fatalf("replace made %v, want the existence probe and then the write", got)
		}
		if seen.ID != 42 {
			t.Fatalf("the hook saw id %d, want the 42 the command named rather than the 999 in the body", seen.ID)
		}
		if !seen.CreatedAt.IsZero() {
			t.Fatalf("the hook was handed a forged %v in a generated column", seen.CreatedAt)
		}
	})

	t.Run("a replace of a row that is not there never writes", func(t *testing.T) {
		repo := &fakeRepo{err: crud.ErrNotFound}
		svc := NewService[widget, int64, widgetUpdate](repo)

		if _, err := svc.Replace(context.Background(), ReplaceCommand[int64, widget]{ID: 999}); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("replacing a missing row answered %v, want ErrNotFound", err)
		}
		if slices.Contains(repo.methods(), "Save") {
			t.Fatalf("a replace created a row at a key the client picked: %v", repo.methods())
		}
	})

	t.Run("the patch hook runs before the update", func(t *testing.T) {
		repo := &fakeRepo{}
		svc := NewService[widget, int64, widgetUpdate](repo)

		name := "from the hook"
		if _, err := svc.Update(context.Background(), UpdateCommand[int64, widgetUpdate]{
			ID:     42,
			Before: func(u *widgetUpdate) error { u.Name = &name; return nil },
		}); err != nil {
			t.Fatalf("updating: %v", err)
		}
		c := repo.only(t, "Update")
		if c.id != 42 || c.dto.Name == nil || *c.dto.Name != name {
			t.Fatalf("the repository was asked to update %+v; the hook's mutation did not land", c)
		}
	})
}

// Removing nothing is two different answers, and which one depends on what was
// asked for: one row that was not there is a miss, an empty set is an empty
// set.
func TestDeletingNothingIsAMissForOneRowAndZeroForASet(t *testing.T) {
	t.Run("one row", func(t *testing.T) {
		repo := &fakeRepo{deleted: 0}
		svc := NewService[widget, int64, widgetUpdate](repo)
		if _, err := svc.Delete(context.Background(), DeleteCommand[int64]{ID: 7}); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("deleting a row that was not there answered %v, want ErrNotFound", err)
		}

		// The control: a row that was there is not a miss, so the arm above is
		// reading the count rather than always refusing.
		repo = &fakeRepo{deleted: 1}
		svc = NewService[widget, int64, widgetUpdate](repo)
		if n, err := svc.Delete(context.Background(), DeleteCommand[int64]{ID: 7}); err != nil || n != 1 {
			t.Fatalf("deleting a row that was there answered %d, %v", n, err)
		}
	})

	t.Run("a set", func(t *testing.T) {
		repo := &fakeRepo{}
		svc := NewService[widget, int64, widgetUpdate](repo)
		n, err := svc.DeleteMany(context.Background(), BulkDeleteCommand[int64]{})
		if err != nil || n != 0 {
			t.Fatalf("deleting an empty set answered %d, %v, want 0 and no error", n, err)
		}
		if len(repo.calls) != 0 {
			t.Fatalf("an empty set reached the repository as %v", repo.methods())
		}
	})
}

// The reads narrow the query document before they compile it, and the caller's
// options are appended afterwards so a scope ANDs with the client's filter
// rather than replacing it ([[D-004]]).
func TestTheReadsNarrowTheDocumentAndAppendTheCallersOptions(t *testing.T) {
	tenant := crud.Where(crud.Eq("Price", 1))

	t.Run("count drops the paging", func(t *testing.T) {
		repo := &fakeRepo{}
		svc := NewService[widget, int64, widgetUpdate](repo)

		req := &query.Request{Page: 3, Limit: 10, Sort: query.Sorts{{Field: "Name"}}}
		if _, err := svc.Count(context.Background(), CountCommand{Query: req, Options: []crud.Option{tenant}}); err != nil {
			t.Fatalf("counting: %v", err)
		}
		o := repo.only(t, "Count").opts
		if o.Limit != 0 || o.Offset != 0 || len(o.Sort) != 0 {
			t.Fatalf("the count carried limit=%d offset=%d sort=%v; a page of a count is not a count", o.Limit, o.Offset, o.Sort)
		}
		if o.Predicate() == nil {
			t.Fatal("the caller's options never reached the repository")
		}
	})

	t.Run("a keyed read drops the filter", func(t *testing.T) {
		repo := &fakeRepo{}
		svc := NewService[widget, int64, widgetUpdate](repo)

		req := &query.Request{Page: 2, Limit: 5, Terms: []query.Term{{Path: "Name", Op: "eq", Values: query.Strings{"bolt"}}}}
		if _, err := svc.Get(context.Background(), GetCommand[int64]{ID: 42, Query: req}); err != nil {
			t.Fatalf("reading: %v", err)
		}
		c := repo.only(t, "GetByID")
		if c.id != 42 {
			t.Fatalf("the repository was asked for id %d, want 42", c.id)
		}
		if c.opts.Predicate() != nil || c.opts.Limit != 0 {
			t.Fatalf("a keyed read carried a filter or a page: %+v", c.opts)
		}
	})

	t.Run("a list keeps both, in that order", func(t *testing.T) {
		repo := &fakeRepo{}
		svc := NewService[widget, int64, widgetUpdate](repo)

		req := &query.Request{Terms: []query.Term{{Path: "Name", Op: "eq", Values: query.Strings{"bolt"}}}}
		if _, err := svc.List(context.Background(), ListCommand{Query: req, Options: []crud.Option{tenant}}); err != nil {
			t.Fatalf("listing: %v", err)
		}
		o := repo.only(t, "Get").opts
		sql, args, err := crud.NewSQL(crud.Postgres{}, widgetMeta).Predicate(o.Predicate()).Done()
		if err != nil {
			t.Fatalf("the compiled filter does not resolve: %v", err)
		}
		if want := `("name" = $1 AND "price" = $2)`; sql != want {
			t.Fatalf("the filter compiled to %s, want %s — the client's terms ANDed with the caller's options", sql, want)
		}
		if want := []any{"bolt", 1}; !reflect.DeepEqual(args, want) {
			t.Fatalf("bound %#v, want %#v", args, want)
		}
	})

	t.Run("no document at all is a plain read", func(t *testing.T) {
		repo := &fakeRepo{}
		svc := NewService[widget, int64, widgetUpdate](repo)
		if _, err := svc.List(context.Background(), ListCommand{}); err != nil {
			t.Fatalf("a command with no query document answered %v", err)
		}
		if o := repo.only(t, "Get").opts; o.Predicate() != nil {
			t.Fatalf("a command with no query document compiled to a filter: %+v", o)
		}
	})
}

// A query config bounds what a client may ask for, and it is the service's
// rather than the transport's — which is why Serving refuses the option.
func TestWithQueryBoundsTheServiceAndNotTheTransport(t *testing.T) {
	repo := &fakeRepo{}
	cfg := &query.Config{Filterable: []string{"Name"}}
	svc := NewService[widget, int64, widgetUpdate](repo, WithQuery(cfg))

	req := &query.Request{Terms: []query.Term{{Path: "Price", Op: "eq", Values: query.Strings{"1"}}}}
	if _, err := svc.List(context.Background(), ListCommand{Query: req}); err == nil {
		t.Fatal("a filter outside the allow-list was accepted")
	}

	// The control: the field that is on the list still works, so the refusal
	// above is the allow-list and not a service that refuses everything.
	req = &query.Request{Terms: []query.Term{{Path: "Name", Op: "eq", Values: query.Strings{"bolt"}}}}
	if _, err := svc.List(context.Background(), ListCommand{Query: req}); err != nil {
		t.Fatalf("a filter on the allow-list was refused: %v", err)
	}
}
