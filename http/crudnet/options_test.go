package crudnet

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/shardit-io/go-rx-crud/crud"
	"github.com/shardit-io/go-rx-crud/query"
)

// widgetDTO is what the API is willing to show. Secret is not in it, which is
// the whole reason a presenter exists.
type widgetDTO struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Price int    `json:"price"`
}

func present(_ *http.Request, w Widget) any {
	return widgetDTO{ID: w.ID, Name: w.Name, Price: w.Price}
}

// A presenter renders single entities and page items alike: each shape has to
// arrive as the DTO — no more, so the hidden column cannot leak, and no less,
// so an empty body cannot pass for a presented one.
func TestWithTransformHidesColumnsOnEveryReadShape(t *testing.T) {
	app, _ := mount(t, WithTransform[Widget, int64, WidgetUpdate](present))

	for _, tc := range []struct {
		name, target string
		presented    func(*testing.T, response)
	}{
		{"one entity", "/widgets/42", func(t *testing.T, r response) {
			var got widgetDTO
			r.decode(t, &got)
			if want := (widgetDTO{ID: 42, Name: "bolt", Price: 250}); got != want {
				t.Fatalf("the presented entity is %+v, want the fake's row through widgetDTO %+v", got, want)
			}
		}},
		{"a page of them", "/widgets", func(t *testing.T, r response) {
			var page struct {
				Items []widgetDTO `json:"items"`
			}
			r.decode(t, &page)
			want := []widgetDTO{{ID: 1, Name: "bolt", Price: 250}, {ID: 2, Name: "nut", Price: 120}}
			if !slices.Equal(page.Items, want) {
				t.Fatalf("the presented items are %+v, want %+v", page.Items, want)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := ok(t, app, http.MethodGet, tc.target, "", http.StatusOK)
			if body := string(r.body); strings.Contains(body, "swordfish") || strings.Contains(body, "secret") {
				t.Fatalf("the presenter was bypassed, the response still carries the hidden column: %s", r.body)
			}
			tc.presented(t, r)
		})
	}
}

// Mapping a page keeps the pager intact: only the items change shape.
func TestWithTransformKeepsThePager(t *testing.T) {
	app, _ := mount(t, WithTransform[Widget, int64, WidgetUpdate](present))

	r := ok(t, app, http.MethodGet, "/widgets", "", http.StatusOK)

	var page struct {
		Items      []widgetDTO `json:"items"`
		Page       int         `json:"page"`
		Limit      int         `json:"limit"`
		Total      int64       `json:"total"`
		TotalPages int         `json:"totalPages"`
		HasNext    bool        `json:"hasNext"`
		HasPrev    bool        `json:"hasPrev"`
	}
	r.decode(t, &page)
	if len(page.Items) != 2 || page.Items[0].Name != "bolt" {
		t.Fatalf("the presented items are %+v", page.Items)
	}
	if page.Page != 2 || page.Limit != 2 || page.Total != 5 || page.TotalPages != 3 || !page.HasNext || !page.HasPrev {
		t.Fatalf("the pager was lost in the mapping: %+v", page)
	}
}

// The presenter also runs on what a write returns, so a create cannot answer
// with the columns a read is not allowed to show.
func TestWithTransformAppliesToWritesToo(t *testing.T) {
	app, _ := mount(t, WithTransform[Widget, int64, WidgetUpdate](present))

	r := ok(t, app, http.MethodPost, "/widgets", `{"name":"bolt"}`, http.StatusCreated)

	var out map[string]json.RawMessage
	r.decode(t, &out)
	got := make([]string, 0, len(out))
	for k := range out {
		got = append(got, k)
	}
	slices.Sort(got)
	if want := []string{"id", "name", "price"}; !slices.Equal(got, want) {
		t.Fatalf("the create response carries %v, want the presented %v", got, want)
	}
}

// BeforeSave is where a request-derived value is stamped onto the model; it
// runs after binding, so what it writes is what the repository stores.
func TestBeforeSaveMutationReachesTheRepository(t *testing.T) {
	stamp := BeforeSave[Widget, int64, WidgetUpdate](func(c *http.Request, w *Widget) error {
		w.OwnerID = 99
		return nil
	})

	for _, tc := range []struct {
		name, method, target, body string
		want                       int
	}{
		{"on create", http.MethodPost, "/widgets", `{"name":"bolt"}`, http.StatusCreated},
		{"on replace", http.MethodPut, "/widgets/42", `{"name":"bolt"}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, fake := mount(t, stamp)
			ok(t, app, tc.method, tc.target, tc.body, tc.want)
			if got := fake.only(t, "Save").Model.OwnerID; got != 99 {
				t.Fatalf("BeforeSave set owner 99, the repository stored %d", got)
			}
		})
	}
}

// A hook that refuses the request stops it: the repository is never asked.
func TestBeforeSaveCanRefuseTheRequest(t *testing.T) {
	app, fake := mount(t, BeforeSave[Widget, int64, WidgetUpdate](func(c *http.Request, w *Widget) error {
		return crud.ErrForbidden
	}))

	r := do(t, app, http.MethodPost, "/widgets", `{"name":"bolt"}`)
	if r.status != http.StatusForbidden {
		t.Fatalf("a refused create answered %d, want 403: %s", r.status, r.body)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("the refused create still called %v", fake.methods())
	}
}

// BeforeUpdate sees the id from the URL alongside the DTO, which is what lets
// it decide per row.
func TestBeforeUpdateSeesThePathIDAndItsMutationLands(t *testing.T) {
	var seen int64
	app, fake := mount(t, BeforeUpdate[Widget, int64, WidgetUpdate](func(c *http.Request, id int64, dto *WidgetUpdate) error {
		seen = id
		price := 999
		dto.Price = &price
		return nil
	}))

	ok(t, app, http.MethodPatch, "/widgets/42", `{"name":"renamed"}`, http.StatusOK)

	if seen != 42 {
		t.Fatalf("BeforeUpdate was given id %d, want the 42 from the URL", seen)
	}
	dto := fake.only(t, "Update").DTO
	if dto.Price == nil || *dto.Price != 999 {
		t.Fatalf("BeforeUpdate set price 999, the repository received %v", dto.Price)
	}
	if dto.Name == nil || *dto.Name != "renamed" {
		t.Fatalf("the hook clobbered the body: name = %v", dto.Name)
	}
}

// A scope is added to every read, whatever door the request came in by. It is
// ANDed in, so a client's own filter narrows further rather than replacing it.
func TestWithScopeNarrowsEveryRead(t *testing.T) {
	tenant := WithScope[Widget, int64, WidgetUpdate](func(c *http.Request) ([]crud.Option, error) {
		return []crud.Option{crud.Where(crud.Eq("OwnerID", int64(7)))}, nil
	})

	for _, tc := range []struct {
		name, method, target, body, call string
	}{
		{"list", http.MethodGet, "/widgets", "", "Get"},
		{"query", http.MethodPost, "/widgets/query", `{"limit":5}`, "Get"},
		{"count", http.MethodGet, "/widgets/count", "", "Count"},
		{"one entity", http.MethodGet, "/widgets/42", "", "GetByID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, fake := mount(t, tenant)
			ok(t, app, tc.method, tc.target, tc.body, http.StatusOK)

			sql, args := whereSQL(t, fake.only(t, tc.call).Opts)
			if sql != `"owner_id" = $1` {
				t.Fatalf("the scope did not reach the repository: filter = %q", sql)
			}
			if want := []any{int64(7)}; !reflect.DeepEqual(args, want) {
				t.Fatalf("bound %#v, want %#v", args, want)
			}
		})
	}
}

// The client's filter and the scope are ANDed: a request cannot widen the scope
// by sending a filter of its own.
func TestWithScopeIsANDedWithTheClientFilter(t *testing.T) {
	app, fake := mount(t, WithScope[Widget, int64, WidgetUpdate](func(c *http.Request) ([]crud.Option, error) {
		return []crud.Option{crud.Where(crud.Eq("OwnerID", int64(7)))}, nil
	}))

	ok(t, app, http.MethodGet, "/widgets?f=price:gte:100", "", http.StatusOK)

	sql, args := whereSQL(t, fake.only(t, "Get").Opts)
	wantSQL := `("price" >= $1 AND "owner_id" = $2)`
	if sql != wantSQL {
		t.Fatalf("filter = %s, want %s", sql, wantSQL)
	}
	if want := []any{100, int64(7)}; !reflect.DeepEqual(args, want) {
		t.Fatalf("bound %#v, want %#v", args, want)
	}
}

// A read-only handler mounts the reads and nothing else, so every write is
// refused by the router before any handler runs.
func TestReadOnlyMountsOnlyTheReadRoutes(t *testing.T) {
	app, fake := mount(t, ReadOnly[Widget, int64, WidgetUpdate]())

	ok(t, app, http.MethodGet, "/widgets", "", http.StatusOK)
	ok(t, app, http.MethodGet, "/widgets/42", "", http.StatusOK)
	ok(t, app, http.MethodGet, "/widgets/count", "", http.StatusOK)
	ok(t, app, http.MethodPost, "/widgets/query", `{"limit":5}`, http.StatusOK)

	for _, tc := range []struct {
		name, method, target, body string
	}{
		{"create", http.MethodPost, "/widgets", `{"name":"bolt"}`},
		{"update", http.MethodPatch, "/widgets/42", `{"name":"renamed"}`},
		{"replace", http.MethodPut, "/widgets/42", `{"name":"replaced"}`},
		{"delete", http.MethodDelete, "/widgets/42", ""},
		{"bulk delete", http.MethodPost, "/widgets/bulk-delete", `{"ids":[1]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := do(t, app, tc.method, tc.target, tc.body)
			if r.status != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s answered %d on a read-only handler, want 405 from the router: %s",
					tc.method, tc.target, r.status, r.body)
			}
		})
	}

	want := []string{"Get", "GetByID", "Count", "Get"}
	if got := fake.methods(); !slices.Equal(got, want) {
		t.Fatalf("a read-only handler made the calls %v, want only the reads %v", got, want)
	}
}

// Without the option the key is cleared on create; with it, the client's key is
// handed to the repository untouched.
func TestAllowClientIDLetsTheClientChooseTheKey(t *testing.T) {
	app, fake := mount(t, AllowClientID[Widget, int64, WidgetUpdate]())

	ok(t, app, http.MethodPost, "/widgets", `{"id":9999,"name":"bolt"}`, http.StatusCreated)

	if got := fake.only(t, "Save").Model.ID; got != 9999 {
		t.Fatalf("the repository was handed id %d, want the client's 9999", got)
	}
}

// MaxBulk is a bound on one request, not a filter: at the cap the delete goes
// through, past it the repository is never asked at all.
func TestMaxBulkCapsOneRequest(t *testing.T) {
	t.Run("at the cap", func(t *testing.T) {
		app, fake := mount(t, MaxBulk[Widget, int64, WidgetUpdate](2))
		ok(t, app, http.MethodPost, "/widgets/bulk-delete", `{"ids":[1,2]}`, http.StatusOK)
		if ids := fake.only(t, "Delete").IDs; !slices.Equal(ids, []int64{1, 2}) {
			t.Fatalf("the repository was asked to delete %v, want [1 2]", ids)
		}
	})

	t.Run("past the cap", func(t *testing.T) {
		app, fake := mount(t, MaxBulk[Widget, int64, WidgetUpdate](2))
		r := do(t, app, http.MethodPost, "/widgets/bulk-delete", `{"ids":[1,2,3]}`)
		if r.status != http.StatusBadRequest {
			t.Fatalf("three ids past a cap of two answered %d, want 400: %s", r.status, r.body)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("an over-sized bulk delete still called %v", fake.methods())
		}
	})
}

// The query config is an allow-list: what it names compiles, what it does not
// name never becomes a repository call.
func TestWithQueryBoundsWhatClientsMayAskFor(t *testing.T) {
	cfg := WithQuery[Widget, int64, WidgetUpdate](&query.Config{
		Preloadable: []string{"Owner"},
		Sortable:    []string{"Name"},
	})

	t.Run("what the config allows", func(t *testing.T) {
		app, fake := mount(t, cfg)
		ok(t, app, http.MethodGet, "/widgets?preload=owner&sort=name", "", http.StatusOK)

		o := fake.only(t, "Get").Opts
		if got, want := preloadPaths(o), []string{"Owner"}; !slices.Equal(got, want) {
			t.Fatalf("preloads = %v, want %v", got, want)
		}
		if got, want := sortTerms(o), []string{"Name"}; !slices.Equal(got, want) {
			t.Fatalf("sort = %v, want %v", got, want)
		}
	})

	for _, tc := range []struct {
		name, target string
	}{
		{"a preload it does not name", "/widgets?preload=parts"},
		{"a sort it does not name", "/widgets?sort=price"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, fake := mount(t, cfg)
			r := do(t, app, http.MethodGet, tc.target, "")
			if r.status != http.StatusBadRequest {
				t.Fatalf("%s answered %d, want 400: %s", tc.target, r.status, r.body)
			}
			if len(fake.calls) != 0 {
				t.Fatalf("a rejected request still reached the repository: %v", fake.methods())
			}
		})
	}
}

// The error handler is the only place a repository error becomes a response, so
// replacing it replaces every failure shape at once.
func TestWithErrorHandlerReplacesTheMapping(t *testing.T) {
	app, fake := mount(t, WithErrorHandler[Widget, int64, WidgetUpdate](
		func(w http.ResponseWriter, _ *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json")
			if errors.Is(err, crud.ErrNotFound) {
				w.WriteHeader(http.StatusGone)
				_, _ = w.Write([]byte(`{"gone":true}`))
				return
			}
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte(`{"gone":false}`))
		}))
	fake.err = crud.ErrNotFound

	r := do(t, app, http.MethodGet, "/widgets/42", "")
	if r.status != http.StatusGone {
		t.Fatalf("the replacement handler answered %d, want 410: %s", r.status, r.body)
	}
	var out struct {
		Gone bool `json:"gone"`
	}
	r.decode(t, &out)
	if !out.Gone {
		t.Fatalf("the body came from the default handler: %s", r.body)
	}
}

// The documented one-line set-up: New takes no explicit type arguments, because
// all three are inferred from the repository it is handed, and an unconfigured
// handler is already a working API.
func TestNewInfersItsTypeParametersFromTheRepository(t *testing.T) {
	fake := newFake()
	app := http.NewServeMux()
	New(fake).Mount(app, "/widgets")

	ok(t, app, http.MethodGet, "/widgets?preload=owner&f=price:gte:100", "", http.StatusOK)

	if got := fake.methods(); !slices.Equal(got, []string{"Get"}) {
		t.Fatalf("an unconfigured handler called %v, want [Get]", got)
	}
	if got := preloadPaths(fake.only(t, "Get").Opts); !slices.Equal(got, []string{"Owner"}) {
		t.Fatalf("without a query config every mapped relation is preloadable, got %v", got)
	}
}
