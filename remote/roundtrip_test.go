package remote_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/http/crudhttp"
	"github.com/shardit-io/vv/remote"
)

func client(t *testing.T, base string) *remote.Resource[Widget, int64, WidgetUpdate] {
	t.Helper()
	return remote.New[Widget, int64, WidgetUpdate](crudhttp.Transport(base))
}

// clause renders a narrowing the way the database would see it, which is how
// two option lists are compared when one of them holds an opaque predicate.
func clause(t *testing.T, o *crud.Options) (string, []any) {
	t.Helper()
	sql, args, err := crud.NewSQL(crud.Postgres{}, widgetMeta).Predicate(o.Predicate()).Done()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return sql, args
}

// Every method, end to end: a real client, a real HTTP server, a real binding.
// Nothing between them is a stub, so what this proves is that the encode and
// the decode agree — which is the only thing a client test can be about.
func TestEveryMethodMakesTheRoundTrip(t *testing.T) {
	ctx := context.Background()

	t.Run("Get", func(t *testing.T) {
		f := newFake()
		page, err := client(t, serve(t, f)).Get(ctx, crud.Limit(2), crud.Page(2))
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(page.Items) != 2 || page.Items[0].Name != "bolt" || page.Total != 5 {
			t.Fatalf("the page came back as %+v", page)
		}
		if got := f.last(t); got.Method != "Get" || got.Opts.Limit != 2 || got.Opts.Page != 2 {
			t.Fatalf("the far side was asked for %+v", got)
		}
	})

	t.Run("GetAll", func(t *testing.T) {
		f := newFake()
		all, err := client(t, serve(t, f)).GetAll(ctx)
		if err != nil {
			t.Fatalf("get all: %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("%d rows came back", len(all))
		}
		if !f.last(t).Opts.Unpaged {
			t.Fatal("GetAll asked for one page")
		}
	})

	t.Run("GetByID", func(t *testing.T) {
		f := newFake()
		w, err := client(t, serve(t, f)).GetByID(ctx, 42, crud.Select("Name"), crud.Preload("Owner"))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if w.ID != 42 {
			t.Fatalf("row %d came back", w.ID)
		}
		got := f.last(t)
		if got.ID != 42 {
			t.Fatalf("the far side was asked for row %d", got.ID)
		}
		// The projection always carries the key, which crud.Select adds.
		if !contains(got.Opts.Fields, "Name") {
			t.Fatalf("the projection arrived as %v", got.Opts.Fields)
		}
		if len(got.Opts.Preloads) != 1 || got.Opts.Preloads[0].Path != "Owner" {
			t.Fatalf("the preload arrived as %v", got.Opts.Preloads)
		}
	})

	t.Run("Count", func(t *testing.T) {
		f := newFake()
		n, err := client(t, serve(t, f)).Count(ctx)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 5 {
			t.Fatalf("the count came back as %d", n)
		}
	})

	t.Run("Save creates", func(t *testing.T) {
		f := newFake()
		w := Widget{Name: "washer", Price: 10}
		if err := client(t, serve(t, f)).Save(ctx, &w); err != nil {
			t.Fatalf("save: %v", err)
		}
		// Refreshed in place with what the service generated, which is Save's
		// whole contract and the reason it takes a pointer.
		if w.ID != 7 || !w.CreatedAt.Equal(savedAt) {
			t.Fatalf("the model came back as %+v", w)
		}
		if got := f.last(t); got.Method != "Save" || got.Model.Name != "washer" {
			t.Fatalf("the far side was handed %+v", got)
		}
	})

	t.Run("Save replaces", func(t *testing.T) {
		f := newFake()
		w := Widget{ID: 42, Name: "bolt", Price: 250}
		if err := client(t, serve(t, f)).Save(ctx, &w); err != nil {
			t.Fatalf("save: %v", err)
		}
		if w.ID != 42 {
			t.Fatalf("the key moved to %d", w.ID)
		}
		// PUT loads the row first, so the repository sees a GetByID and then a
		// Save. A key that had gone out as a create would show no GetByID.
		if len(f.calls) < 2 || f.calls[0].Method != "GetByID" {
			t.Fatalf("a set key did not take the replace route: %v", methods(f))
		}
	})

	t.Run("Update", func(t *testing.T) {
		f := newFake()
		name := "spanner"
		w, err := client(t, serve(t, f)).Update(ctx, 42, WidgetUpdate{Name: &name})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if w.Name != "spanner" {
			t.Fatalf("the row came back as %+v", w)
		}
		got := f.last(t)
		if got.DTO.Name == nil || *got.DTO.Name != "spanner" {
			t.Fatalf("the patch arrived as %+v", got.DTO)
		}
		// The field nobody sent stays absent, which is the three-state property
		// a document has to keep to be worth having.
		if got.DTO.Price != nil || got.DTO.Note.IsDefined() {
			t.Fatalf("a field nobody sent arrived defined: %+v", got.DTO)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		f := newFake()
		n, err := client(t, serve(t, f)).Delete(ctx, 42)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if n != 1 {
			t.Fatalf("%d rows went away", n)
		}
		if got := f.last(t); len(got.IDs) != 1 || got.IDs[0] != 42 {
			t.Fatalf("the far side was asked to delete %v", got.IDs)
		}
	})

	t.Run("Delete many", func(t *testing.T) {
		f := newFake()
		f.del = 3
		n, err := client(t, serve(t, f)).Delete(ctx, 1, 2, 3)
		if err != nil {
			t.Fatalf("bulk delete: %v", err)
		}
		if n != 3 {
			t.Fatalf("%d rows went away", n)
		}
		if got := f.last(t); len(got.IDs) != 3 {
			t.Fatalf("the far side was asked to delete %v", got.IDs)
		}
	})

	t.Run("Delete nothing", func(t *testing.T) {
		f := newFake()
		n, err := client(t, serve(t, f)).Delete(ctx)
		if err != nil || n != 0 {
			t.Fatalf("deleting no keys answered %d, %v", n, err)
		}
		if len(f.calls) != 0 {
			t.Fatalf("a round trip was made to delete nothing: %v", methods(f))
		}
	})
}

// The point of crud.MarshalPredicate, measured where it matters: a filter
// written in Go reaches the far side as the same narrowing a local repository
// would have been given. Without it the request carries no filter at all and
// the answer is every row, over a 200.
func TestAFilterWrittenInGoArrivesAsTheSameNarrowing(t *testing.T) {
	f := newFake()
	base := serve(t, f)

	opts := []crud.Option{
		crud.Where(crud.Eq("Name", "bolt")),
		crud.Where(crud.Gte("Price", 100)),
		crud.OrderBy(crud.Desc("Price")),
	}
	if _, err := client(t, base).Get(context.Background(), opts...); err != nil {
		t.Fatalf("list: %v", err)
	}

	wantSQL, wantArgs := clause(t, crud.Build(opts...))
	gotSQL, gotArgs := clause(t, f.last(t).Opts)

	if wantSQL == "" {
		t.Fatal("the local options render nothing, so this proves nothing")
	}
	if gotSQL != wantSQL {
		t.Fatalf("the far side was asked\n  %s\nwhere a local call asks\n  %s", gotSQL, wantSQL)
	}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("%d binds arrived where %d were sent", len(gotArgs), len(wantArgs))
	}
	if got := f.last(t).Opts.Sort; len(got) != 1 || got[0].Field != "Price" || !got[0].Desc {
		t.Fatalf("the sort arrived as %v", got)
	}
}

// A refusal keeps its class, its violations and the sentinel a caller branches
// on. This is what the whole client is for: the branch a consumer wrote against
// a repository in this process keeps working when the repository moves out of
// it.
func TestAConflictArrivesAsAConflictWithItsViolations(t *testing.T) {
	f := newFake()
	f.err = errs.Conflict().
		Wrapping(crud.ErrConflict).
		Code(errs.CodeUnique).
		Field("name").Code(errs.CodeUnique).Message("that name is taken").
		Detail(errs.Detail{
			Dialect: "postgres", SQLState: "23505", Native: 1062,
			Constraint: "widgets_name_key", Table: "widgets", Columns: []string{"name"},
			Driver: errors.New(`pq: duplicate key value violates unique constraint "widgets_name_key"`),
		}).
		Fault()

	_, err := client(t, serve(t, f)).Get(context.Background())
	if err == nil {
		t.Fatal("the conflict arrived as a success")
	}
	if !errors.Is(err, crud.ErrConflict) {
		t.Fatalf("the sentinel did not survive: %v", err)
	}

	fault, ok := errs.AsFault(err)
	if !ok {
		t.Fatalf("no fault came back, only %T", err)
	}
	if fault.Kind != errs.KindConflict {
		t.Fatalf("it arrived as %v", fault.Kind)
	}
	if len(fault.Violations) != 1 {
		t.Fatalf("%d violations arrived", len(fault.Violations))
	}
	v := fault.Violations[0]
	if v.Code != errs.CodeUnique || v.Path.String() != "name" || v.Message != "that name is taken" {
		t.Fatalf("the violation arrived as %+v", v)
	}

	// The other half, and the control on the assertion above: nothing internal
	// crosses. If any of these appeared, the positive assertions would still
	// pass and the client would be a disclosure.
	for _, secret := range []string{"widgets_name_key", "23505", "1062", "duplicate key", "pq:"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("%q reached the caller", secret)
		}
		if strings.Contains(v.Message, secret) {
			t.Fatalf("%q reached the violation message", secret)
		}
	}
	if fault.Detail.Constraint != "" || fault.Detail.SQLState != "" || fault.Detail.Driver != nil {
		t.Fatalf("the driver's detail crossed the wire: %+v", fault.Detail)
	}
}

// A stale write is a conflict and a stale write, and the finer branch is the
// one a caller re-reads the row from. Over HTTP the code is recovered from the
// violation, because the envelope carries no separate field for the fault's own.
func TestAStaleWriteKeepsTheBranchACallerRereadsFrom(t *testing.T) {
	f := newFake()
	f.err = crud.ErrStaleVersion

	_, err := client(t, serve(t, f)).Get(context.Background())
	if !errors.Is(err, crud.ErrStaleVersion) {
		t.Fatalf("the finer branch is gone: %v", err)
	}
	if !errors.Is(err, crud.ErrConflict) {
		t.Fatalf("the coarse branch is gone: %v", err)
	}
}

// A 500 says nothing, and the client must not invent anything to fill it. The
// fixture is a failure built out of everything that must never cross.
func TestAnInternalFailureArrivesEmpty(t *testing.T) {
	f := newFake()
	f.err = errors.New(`pq: password authentication failed for user "vv" on host db.internal:5432`)

	_, err := client(t, serve(t, f)).Get(context.Background())
	if err == nil {
		t.Fatal("the failure arrived as a success")
	}
	fault, ok := errs.AsFault(err)
	if !ok {
		t.Fatalf("no fault came back, only %T", err)
	}
	if fault.Kind != errs.KindInternal {
		t.Fatalf("it arrived as %v", fault.Kind)
	}
	for _, secret := range []string{"password", "db.internal", "5432", "pq:", "vv"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("%q reached the caller", secret)
		}
	}
	// The control: an internal failure still has to be an error a caller can
	// see, or the assertions above would pass for a client that swallowed it.
	if len(err.Error()) == 0 {
		t.Fatal("the failure arrived with no text at all")
	}
}

// The nastiest failure this client has, and the one a status-only decoder gets
// wrong: a base URL that points at nothing gets the router's own 404 in plain
// text. Read as a status it is crud.ErrNotFound, and a misconfigured service
// then reports an empty table for as long as nobody looks.
func TestARouters404IsNotAMissingRow(t *testing.T) {
	f := newFake()
	base := serve(t, f)

	wrong := client(t, base+"-typo")
	_, err := wrong.GetByID(context.Background(), 42)
	if err == nil {
		t.Fatal("a call to a resource that is not there succeeded")
	}
	if errors.Is(err, crud.ErrNotFound) {
		t.Fatal("a wrong base URL arrived as a missing row, which is how an outage reads as an empty table")
	}
	var pe *remote.ProtocolError
	if !errors.As(err, &pe) {
		t.Fatalf("it arrived as %T, which a caller cannot tell from a real answer: %v", err, err)
	}

	// And the harder half. A plain-text 404 fails to parse as JSON and would be
	// caught by that alone; an API gateway, a service mesh or a load balancer
	// answers JSON, and that body parses. What tells it apart is Envelope.Type,
	// which is why the field exists.
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no route matched","requestId":"abc123"}`))
	}))
	t.Cleanup(gateway.Close)

	_, err = client(t, gateway.URL+"/widgets").GetByID(context.Background(), 42)
	if errors.Is(err, crud.ErrNotFound) {
		t.Fatal("a gateway's own JSON 404 arrived as a missing row")
	}
	if !errors.As(err, &pe) {
		t.Fatalf("a gateway's JSON 404 arrived as %T: %v", err, err)
	}

	// The control. The same status from the same server, on a row the handler
	// really did not find, must still be crud.ErrNotFound — otherwise the
	// assertions above would pass for a client that never classifies anything.
	f.err = crud.ErrNotFound
	if _, err := client(t, base).GetByID(context.Background(), 42); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("a real missing row arrived as %v", err)
	}
}

// An option that cannot cross has to say so before anything is sent. A relation
// scope is the one that matters: it is what an access-control gate uses to hide
// rows a preload would otherwise reach, and dropping it is a leak rather than a
// slow query.
func TestAnOptionThatCannotCrossIsRefusedBeforeAnythingIsSent(t *testing.T) {
	scoped := crud.NarrowRelations(new(crud.RelationScopes).AtPath("Parts", crud.Eq("Label", "hex")))

	cases := map[string]struct {
		opt  crud.Option
		want string
	}{
		"relation scope": {scoped, "crud.NarrowRelations"},
		"row lock":       {crud.ForUpdate(), "crud.ForUpdate"},
		"aggregate":      {crud.GroupBy("Name"), "crud.Aggregate"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			_, err := client(t, serve(t, f)).Get(context.Background(), c.opt)
			if err == nil {
				t.Fatal("it was accepted, so whatever it asked for was silently dropped")
			}
			var oe *remote.OptionError
			if !errors.As(err, &oe) {
				t.Fatalf("refused with %T, which a caller cannot branch on: %v", err, err)
			}
			if oe.Option != c.want {
				t.Fatalf("blamed %s, and the call site wrote %s", oe.Option, c.want)
			}
			if len(f.calls) != 0 {
				t.Fatalf("the call went out anyway: %v", methods(f))
			}
		})
	}

	// The control: an option that *can* cross is not refused, or the cases
	// above would pass for a client that refused everything.
	f := newFake()
	if _, err := client(t, serve(t, f)).Get(context.Background(), crud.Distinct(), crud.SkipTotal()); err != nil {
		t.Fatalf("an option that travels fine was refused: %v", err)
	}
}

// crud.Raw is the one refusal that is a security answer rather than a missing
// word: it is SQL, and a filter document carries field paths and values.
func TestRawSQLIsNeverPutOnTheWire(t *testing.T) {
	f := newFake()
	_, err := client(t, serve(t, f)).Get(context.Background(), crud.Where(crud.Raw("1 = 1")))
	if err == nil {
		t.Fatal("crud.Raw was accepted")
	}
	var pe *crud.PredicateError
	if !errors.As(err, &pe) || pe.Node != "crud.Raw" {
		t.Fatalf("refused with %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("something was sent: %v", methods(f))
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func methods(f *fakeRepo) []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.Method)
	}
	return out
}

// An update DTO whose crud.Opt fields carry no `omitzero` cannot be sent: every
// field the caller left undefined would arrive as an explicit null and empty
// the column. It is refused when the resource is built, because by the time a
// request has been made the damage is in the database.
func TestAPatchDtoThatWouldEmptyAColumnIsRefusedAtStartup(t *testing.T) {
	type loose struct {
		Name *string          `json:"name,omitempty"`
		Note crud.Opt[string] `json:"note"` // the missing tag
	}
	if _, err := remote.TryNew[Widget, int64, loose](crudhttp.Transport("http://x")); err == nil {
		t.Fatal("a DTO that would null out every unset column was accepted")
	} else if !strings.Contains(err.Error(), "omitzero") || !strings.Contains(err.Error(), "Note") {
		t.Fatalf("the refusal does not say which field or what to do: %v", err)
	}

	// Two controls. The tag the generator writes is accepted, or the check is a
	// blanket refusal; and a field that is never sent is not the check's
	// business.
	if _, err := remote.TryNew[Widget, int64, WidgetUpdate](crudhttp.Transport("http://x")); err != nil {
		t.Fatalf("a generated DTO was refused: %v", err)
	}
	type skipped struct {
		Note crud.Opt[string] `json:"-"`
	}
	if _, err := remote.TryNew[Widget, int64, skipped](crudhttp.Transport("http://x")); err != nil {
		t.Fatalf("a field that is never sent was refused: %v", err)
	}
}

// A remote resource is a port.Repository, so a binding mounts it and the
// service in front of it becomes a gateway over the one behind. Two hops, and
// what comes out the far end is what went in the near one.
//
// This is what the interface is for. A client that only had methods of its own
// could not be re-exposed, decorated, or stood in for a local repository.
func TestARemoteResourceMountsAsAGateway(t *testing.T) {
	ctx := context.Background()
	origin := newFake()

	// The gateway holds a client to the origin and serves it as its own API.
	gateway := serve(t, client(t, serve(t, origin)))

	page, err := client(t, gateway).Get(ctx, crud.Where(crud.Eq("Name", "bolt")), crud.Limit(2))
	if err != nil {
		t.Fatalf("through the gateway: %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].Name != "bolt" {
		t.Fatalf("the page came back as %+v", page)
	}

	// The filter made it through both hops. One that stopped at the gateway
	// would still answer 200 with the same canned page, which is why this is
	// asserted at the origin rather than on the response.
	sql, _ := clause(t, origin.last(t).Opts)
	if !strings.Contains(sql, `"name"`) {
		t.Fatalf("the origin was asked %q, so the filter stopped at the gateway", sql)
	}

	// And so does a refusal, sentinel included.
	origin.err = crud.ErrNotFound
	if _, err := client(t, gateway).GetByID(ctx, 42); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("a missing row two hops away arrived as %v", err)
	}
}
