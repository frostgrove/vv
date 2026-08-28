package remote_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/http/crudnet"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/remote"
	"github.com/frostgrove/vv/remote/remotehttp"
)

type exactCountTransport map[remote.Method]json.RawMessage

func (t exactCountTransport) Do(_ context.Context, call *remote.Call) (json.RawMessage, error) {
	return t[call.Method], nil
}

func client(t *testing.T, base string) *remote.Resource[Widget, int64, WidgetUpdate] {
	t.Helper()
	return remote.New[Widget, int64, WidgetUpdate](remotehttp.Transport(base))
}

func TestRemoteAcceptsExactStringCountsFromStructTransports(t *testing.T) {
	tr := exactCountTransport{
		remote.MethodCount:  json.RawMessage(`{"count":"9007199254740993"}`),
		remote.MethodDelete: json.RawMessage(`{"deleted":"9007199254740993"}`),
		remote.MethodList:   json.RawMessage(`{"items":[],"page":1,"limit":1,"total":"9007199254740993","totalPages":"9007199254740993","hasNext":false,"hasPrev":false}`),
	}
	r := remote.New[Widget, int64, WidgetUpdate](tr)
	if got, err := r.Count(context.Background()); err != nil || got != 9007199254740993 {
		t.Fatalf("Count = %d, %v", got, err)
	}
	if got, err := r.Delete(context.Background(), 1); err != nil || got != 9007199254740993 {
		t.Fatalf("Delete = %d, %v", got, err)
	}
	if got, err := r.Get(context.Background()); err != nil || got.Total != 9007199254740993 || got.TotalPages != 9007199254740993 {
		t.Fatalf("Get = %+v, %v", got, err)
	}
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

	t.Run("First", func(t *testing.T) {
		f := newFake()
		w, err := client(t, serve(t, f)).First(ctx, crud.Where(crud.Eq("OwnerID", int64(7))), crud.Limit(99))
		if err != nil {
			t.Fatalf("first: %v", err)
		}
		if w.ID != 1 {
			t.Fatalf("first row = %+v", w)
		}
		got := f.last(t)
		if got.Method != "Get" || got.Opts.Limit != 1 || got.Opts.Predicate() == nil {
			t.Fatalf("the far side was asked for %+v", got)
		}
	})

	t.Run("GetAll", func(t *testing.T) {
		f := newFake()
		f.page = crud.NewPaginatedResponse(f.page.Items, 1, 0, int64(len(f.page.Items)))
		all, err := client(t, serve(t, f)).GetAll(ctx)
		if err != nil {
			t.Fatalf("get all: %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("%d rows came back", len(all))
		}
		if f.last(t).Opts.Unpaged {
			t.Fatal("GetAll asked for an unpaged response instead of walking pages")
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
		saved, err := client(t, serve(t, f)).Save(ctx, &w)
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		if saved.ID != 7 || !saved.CreatedAt.Equal(savedAt) {
			t.Fatalf("the returned model came back as %+v", saved)
		}
		if w.ID != 0 || !w.CreatedAt.IsZero() {
			t.Fatalf("Save mutated its argument: %+v", w)
		}
		if got := f.last(t); got.Method != "Save" || got.Model.Name != "washer" {
			t.Fatalf("the far side was handed %+v", got)
		}
	})

	t.Run("Save replaces", func(t *testing.T) {
		f := newFake()
		w := Widget{ID: 42, Name: "bolt", Price: 250}
		saved, err := client(t, serve(t, f)).Save(ctx, &w)
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		if saved.ID != 42 {
			t.Fatalf("the key moved to %d", saved.ID)
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

func TestGetByIDPreservesANarrowingThroughTheListRoute(t *testing.T) {
	f := newFake()
	f.page = crud.NewPaginatedResponse([]Widget{{ID: 42, OwnerID: 7, Name: "bolt"}}, 1, 1, 1)
	w, err := client(t, serve(t, f)).GetByID(context.Background(), 42,
		crud.Where(crud.Eq("OwnerID", int64(7))),
	)
	if err != nil {
		t.Fatalf("GetByID() = %v", err)
	}
	if w.ID != 42 {
		t.Fatalf("GetByID() = %+v, want the keyed row", w)
	}
	got := f.last(t)
	if got.Method != "Get" {
		t.Fatalf("narrowed GetByID used %s, want the document-shaped List route", got.Method)
	}
	sql, args := clause(t, got.Opts)
	if want := `("owner_id" = $1 AND "id" = $2)`; sql != want {
		t.Fatalf("List filter = %s, want %s", sql, want)
	}
	if want := []any{int64(7), int64(42)}; !reflect.DeepEqual(args, want) {
		t.Fatalf("List binds = %#v, want %#v", args, want)
	}
}

func TestTautologicalWhereStaysAbsentOnTheRemoteWire(t *testing.T) {
	for _, p := range []crud.Predicate{crud.True(), crud.And()} {
		req, err := remote.ToRequest(crud.Where(p))
		if err != nil {
			t.Fatalf("ToRequest(%T) = %v", p, err)
		}
		if !req.Filter.IsZero() {
			t.Fatalf("ToRequest(%T) sent filter %v, want absence", p, req.Filter)
		}
	}

	f := newFake()
	r := client(t, serve(t, f))
	if _, err := r.Get(context.Background(), crud.Where(crud.True())); err != nil {
		t.Fatalf("Get(Where(True())) = %v", err)
	}
	if got := f.last(t); got.Method != "Get" {
		t.Fatalf("Get(Where(True())) reached %s", got.Method)
	}
	if _, err := r.GetByID(context.Background(), 42, crud.Where(crud.True())); err != nil {
		t.Fatalf("GetByID(Where(True())) = %v", err)
	}
	if got := f.last(t); got.Method != "GetByID" {
		t.Fatalf("GetByID(Where(True())) reached %s, want direct keyed read", got.Method)
	}
}

func TestGetByIDPreservesNarrowedAndCappedPreloadsThroughTheListRoute(t *testing.T) {
	f := newFake()
	f.page = crud.NewPaginatedResponse([]Widget{{ID: 42, OwnerID: 7, Name: "bolt"}}, 1, 1, 1)
	w, err := client(t, serve(t, f)).GetByID(context.Background(), 42,
		crud.Where(crud.Eq("OwnerID", int64(7))),
		crud.PreloadCap("Parts", 1,
			crud.Where(crud.Eq("Label", "hex")),
			crud.OrderBy(crud.Desc("ID"))),
	)
	if err != nil {
		t.Fatalf("GetByID() = %v", err)
	}
	if w.ID != 42 {
		t.Fatalf("GetByID() = %+v, want the keyed row", w)
	}
	got := f.last(t)
	if got.Method != "Get" {
		t.Fatalf("GetByID used %s, want List for its document-shaped preload", got.Method)
	}
	if len(got.Opts.Preloads) != 1 || got.Opts.Preloads[0].Path != "Parts" || got.Opts.Preloads[0].MaxRows != 1 {
		t.Fatalf("preloads = %+v, want Parts capped at 1", got.Opts.Preloads)
	}
	sub := crud.Build(got.Opts.Preloads[0].Opts...)
	if sub.PreloadRows != 1 || len(sub.Filter) != 1 || len(sub.Sort) != 1 {
		t.Fatalf("narrowed preload options = %+v", sub)
	}
	sql, args := clause(t, got.Opts)
	if want := `("owner_id" = $1 AND "id" = $2)`; sql != want {
		t.Fatalf("List filter = %s, want %s", sql, want)
	}
	if want := []any{int64(7), int64(42)}; !reflect.DeepEqual(args, want) {
		t.Fatalf("List binds = %#v, want %#v", args, want)
	}
}

func TestPreloadRowsInsideAPreloadWhereCrossesTheWire(t *testing.T) {
	req, err := remote.ToRequest(crud.PreloadWhere("Parts", crud.PreloadRows(1)))
	if err != nil {
		t.Fatalf("ToRequest() = %v", err)
	}
	if len(req.Preload) != 1 || req.Preload[0].MaxRows != 1 {
		t.Fatalf("preload document = %+v, want Parts capped at 1", req.Preload)
	}
}

func TestUnsupportedNestedPreloadOptionsAreRefusedBeforeTheyCanBeLost(t *testing.T) {
	for name, opt := range map[string]crud.Option{
		"pagination": crud.Limit(1),
		"projection": crud.Select("Label"),
		"cursor":     crud.After("edge"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := remote.ToRequest(crud.PreloadWhere("Parts", opt))
			var option *remote.OptionError
			if !errors.As(err, &option) || option.Option != "crud.PreloadWhere" {
				t.Fatalf("ToRequest() = %T %v, want a PreloadWhere OptionError", err, err)
			}
		})
	}
}

func TestGetAllRefusesAnInconsistentRemoteExport(t *testing.T) {
	f := newFake()
	f.page = crud.NewPaginatedResponse(f.page.Items[:1], 1, 1, 2)
	f.page.HasNext = false // claims the one-item page was complete despite total
	_, err := client(t, serve(t, f)).GetAll(context.Background())
	var partial *remote.PartialResultError
	if !errors.As(err, &partial) {
		t.Fatalf("GetAll() = %v, want PartialResultError", err)
	}
	if !errors.Is(err, remote.ErrPartialResult) || partial.Received != 1 || partial.Total != 2 {
		t.Fatalf("partial result = %+v, want 1 of 2", partial)
	}
	if f.last(t).Opts.Unpaged {
		t.Fatal("GetAll asked for an unpaged response instead of walking pages")
	}
}

func TestGetAllWalksARemotePageCap(t *testing.T) {
	f := newFake()
	f.pages = map[int]crud.PaginatedResponse[Widget]{
		0: crud.NewPaginatedResponse(f.page.Items[:1], 1, 1, 2),
		2: crud.NewPaginatedResponse(f.page.Items[1:], 2, 1, 2),
	}
	all, err := client(t, serve(t, f)).GetAll(context.Background())
	if err != nil {
		t.Fatalf("GetAll() = %v", err)
	}
	if len(all) != 2 || all[0].ID != 1 || all[1].ID != 2 {
		t.Fatalf("GetAll() = %+v, want both capped pages", all)
	}
	if len(f.calls) != 2 || f.calls[0].Opts.Page != 0 || f.calls[1].Opts.Page != 2 {
		t.Fatalf("remote page walk = %+v, want page 0 then page 2", f.calls)
	}
}

func TestGetAllSwitchesToCursorsBeforeAnEndpointsOffsetBudget(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows([]any{int64(1), int64(7), "bolt", 250, nil, savedAt}),
		crudtest.Rows([]any{int64(3)}),
		crudtest.Rows(
			[]any{int64(2), int64(7), "nut", 120, nil, savedAt},
			[]any{int64(3), int64(7), "screw", 30, nil, savedAt},
		),
		crudtest.Rows([]any{int64(3), int64(7), "screw", 30, nil, savedAt}),
	)
	repo := sqlrepo.Define[Widget, int64, WidgetUpdate]("widgets", sqlrepo.MaxLimit(1)).Bind(rec)
	all, err := client(t, serve(t, repo,
		crudnet.WithQuery[Widget, int64, WidgetUpdate](&query.Config{MaxOffset: 1}))).
		GetAll(context.Background())
	if err != nil {
		t.Fatalf("GetAll() = %v", err)
	}
	if len(all) != 3 || all[0].ID != 1 || all[1].ID != 2 || all[2].ID != 3 {
		t.Fatalf("GetAll() = %+v, want every row behind MaxLimit(1)", all)
	}
	if got := len(rec.Statements()); got != 5 {
		t.Fatalf("sql calls = %d, want first-page read/count, two cursor reads, and a terminal probe", got)
	}
}

func TestGetAllWalksCursorEdges(t *testing.T) {
	forward := newFake()
	first := crud.NewPaginatedResponse(forward.page.Items[:1], 0, 1, 2)
	first.NextCursor = "after-first"
	last := crud.NewPaginatedResponse(forward.page.Items[1:], 0, 1, 1)
	last.HasNext = false
	forward.cursors = map[string]crud.PaginatedResponse[Widget]{
		"after:start":       first,
		"after:after-first": last,
	}
	all, err := client(t, serve(t, forward)).GetAll(context.Background(), crud.After("start"), crud.OrderBy(crud.Asc("ID")))
	if err != nil || len(all) != 2 || all[0].ID != 1 || all[1].ID != 2 {
		t.Fatalf("forward cursor GetAll() = %+v, %v", all, err)
	}

	backward := newFake()
	near := crud.NewPaginatedResponse([]Widget{{ID: 3}, {ID: 4}}, 0, 2, 2)
	near.HasNext, near.HasPrev, near.PrevCursor = false, true, "before-first"
	far := crud.NewPaginatedResponse([]Widget{{ID: 1}, {ID: 2}}, 0, 2, 2)
	far.HasNext, far.HasPrev = false, false
	backward.cursors = map[string]crud.PaginatedResponse[Widget]{
		"before:end":          near,
		"before:before-first": far,
	}
	all, err = client(t, serve(t, backward)).GetAll(context.Background(), crud.Before("end"), crud.OrderBy(crud.Asc("ID")))
	if err != nil || len(all) != 4 || all[0].ID != 1 || all[3].ID != 4 {
		t.Fatalf("backward cursor GetAll() = %+v, %v", all, err)
	}
}

func TestGetAllRefusesAnEmptyCursorPageThatClaimsMore(t *testing.T) {
	f := newFake()
	empty := crud.NewPaginatedResponse[Widget](nil, 0, 1, 0)
	empty.HasNext, empty.NextCursor = true, "would-loop"
	f.cursors = map[string]crud.PaginatedResponse[Widget]{
		"after:start": empty,
	}
	_, err := client(t, serve(t, f)).GetAll(context.Background(),
		crud.After("start"), crud.OrderBy(crud.Asc("ID")))
	var partial *remote.PartialResultError
	if !errors.As(err, &partial) || partial.Received != 0 || partial.Total != 0 {
		t.Fatalf("GetAll() = %v, want a 0-of-0 PartialResultError", err)
	}
	if got := len(f.calls); got != 1 {
		t.Fatalf("empty cursor page made %d calls, want it refused before a second request", got)
	}
}

func TestGetAllFollowsACursorEdgeEvenWhenHasNextIsFalse(t *testing.T) {
	f := newFake()
	first := crud.NewPaginatedResponse(f.page.Items[:1], 0, 1, 1)
	first.NextCursor = "after-first" // a cursor edge is stronger than a stale HasNext flag
	last := crud.NewPaginatedResponse(f.page.Items[1:], 0, 1, 1)
	terminal := crud.NewPaginatedResponse[Widget](nil, 0, 1, 0)
	f.cursors = map[string]crud.PaginatedResponse[Widget]{
		"after:start":       first,
		"after:after-first": last,
		"after:after-last":  terminal,
	}
	last.NextCursor = "after-last"
	f.cursors["after:after-first"] = last

	all, err := client(t, serve(t, f)).GetAll(context.Background(),
		crud.After("start"), crud.OrderBy(crud.Asc("ID")))
	if err != nil || len(all) != 2 || all[0].ID != 1 || all[1].ID != 2 {
		t.Fatalf("GetAll() = %+v, %v; want both cursor pages", all, err)
	}
	if got := len(f.calls); got != 3 {
		t.Fatalf("cursor calls = %d, want both data pages and the terminal probe", got)
	}
}

func TestGetAllUnpagedOffsetWalksTheWholeSuffix(t *testing.T) {
	f := newFake()
	first := crud.NewPaginatedResponse([]Widget{{ID: 1}, {ID: 2}}, 1, 2, 3)
	second := crud.NewPaginatedResponse([]Widget{{ID: 3}}, 0, 2, 1)
	first.NextCursor = "after-first"
	terminal := crud.NewPaginatedResponse[Widget](nil, 0, 1, 0)
	second.NextCursor = "after-second"
	f.pages = map[int]crud.PaginatedResponse[Widget]{0: first}
	f.cursors = map[string]crud.PaginatedResponse[Widget]{
		"after:after-first":  second,
		"after:after-second": terminal,
	}

	all, err := client(t, serve(t, f,
		crudnet.WithQuery[Widget, int64, WidgetUpdate](&query.Config{MaxOffset: 1}))).
		GetAll(context.Background(), crud.Unpaged(), crud.Offset(2))
	if err != nil || len(all) != 1 || all[0].ID != 3 {
		t.Fatalf("GetAll() = %+v, %v; want every row after the offset", all, err)
	}
	if f.calls[0].Opts.Unpaged || f.calls[0].Opts.Offset != 0 {
		t.Fatalf("first suffix request = %+v, want a bounded zero-offset page", f.calls[0].Opts)
	}
}

func TestGetAllFollowsAnInitialOffsetPagesCursorEdgeDespiteHasNext(t *testing.T) {
	f := newFake()
	first := crud.NewPaginatedResponse(f.page.Items[:1], 1, 1, 2)
	first.HasNext, first.NextCursor = false, "after-first"
	last := crud.NewPaginatedResponse(f.page.Items[1:], 0, 1, 1)
	terminal := crud.NewPaginatedResponse[Widget](nil, 0, 1, 0)
	last.NextCursor = "after-last"
	f.pages = map[int]crud.PaginatedResponse[Widget]{0: first}
	f.cursors = map[string]crud.PaginatedResponse[Widget]{
		"after:after-first": last,
		"after:after-last":  terminal,
	}

	all, err := client(t, serve(t, f)).GetAll(context.Background())
	if err != nil || len(all) != 2 || all[0].ID != 1 || all[1].ID != 2 {
		t.Fatalf("GetAll() = %+v, %v; want both cursor-edge pages", all, err)
	}
}

func TestGetAllDistinctProjectionDoesNotInjectThePrimaryKeySort(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows([]any{"bolt"}))
	repo := sqlrepo.Define[Widget, int64, WidgetUpdate]("widgets").Bind(rec)

	all, err := client(t, serve(t, repo)).GetAll(context.Background(), crud.Distinct(), crud.Select("Name"))
	if err != nil || len(all) != 1 || all[0].Name != "bolt" {
		t.Fatalf("GetAll() = %+v, %v; want the DISTINCT projection", all, err)
	}
	if sql := crudtest.Normalize(rec.Last().SQL); strings.Contains(sql, "ORDER BY") {
		t.Fatalf("DISTINCT projection unexpectedly received an injected sort: %s", sql)
	}
}

func TestGetAllKeepsTheCompletenessTotal(t *testing.T) {
	f := newFake()
	f.page = crud.NewPaginatedResponse(f.page.Items, 1, 0, int64(len(f.page.Items)))
	if _, err := client(t, serve(t, f)).GetAll(context.Background(), crud.Unpaged(), crud.Limit(1), crud.SkipTotal()); err != nil {
		t.Fatalf("GetAll() = %v", err)
	}
	got := f.last(t).Opts
	if got.Unpaged || got.NoTotal {
		t.Fatalf("GetAll options = %+v, want an ordinary first page with total", got)
	}
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
	if _, err := remote.TryNew[Widget, int64, loose](remotehttp.Transport("http://x")); err == nil {
		t.Fatal("a DTO that would null out every unset column was accepted")
	} else if !strings.Contains(err.Error(), "omitzero") || !strings.Contains(err.Error(), "Note") {
		t.Fatalf("the refusal does not say which field or what to do: %v", err)
	}

	// Two controls. The tag the generator writes is accepted, or the check is a
	// blanket refusal; and a field that is never sent is not the check's
	// business.
	if _, err := remote.TryNew[Widget, int64, WidgetUpdate](remotehttp.Transport("http://x")); err != nil {
		t.Fatalf("a generated DTO was refused: %v", err)
	}
	type skipped struct {
		Note crud.Opt[string] `json:"-"`
	}
	if _, err := remote.TryNew[Widget, int64, skipped](remotehttp.Transport("http://x")); err != nil {
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
