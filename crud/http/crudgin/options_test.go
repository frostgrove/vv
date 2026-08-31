package crudgin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

// widgetDTO is what the API is willing to show. Secret is not in it, which is
// the whole reason a presenter exists.
type widgetDTO struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Price int    `json:"price"`
}

func present(_ *gin.Context, w Widget) any {
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
	stamp := BeforeSave[Widget, int64, WidgetUpdate](func(c *gin.Context, w *Widget) error {
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
	app, fake := mount(t, BeforeSave[Widget, int64, WidgetUpdate](func(c *gin.Context, w *Widget) error {
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
	app, fake := mount(t, BeforeUpdate[Widget, int64, WidgetUpdate](func(c *gin.Context, id int64, dataTransferObject *WidgetUpdate) error {
		seen = id
		price := 999
		dataTransferObject.Price = &price
		return nil
	}))

	ok(t, app, http.MethodPatch, "/widgets/42", `{"name":"renamed"}`, http.StatusOK)

	if seen != 42 {
		t.Fatalf("BeforeUpdate was given id %d, want the 42 from the URL", seen)
	}
	dataTransferObject := fake.only(t, "Update").DTO
	if dataTransferObject.Price == nil || *dataTransferObject.Price != 999 {
		t.Fatalf("BeforeUpdate set price 999, the repository received %v", dataTransferObject.Price)
	}
	if dataTransferObject.Name == nil || *dataTransferObject.Name != "renamed" {
		t.Fatalf("the hook clobbered the body: name = %v", dataTransferObject.Name)
	}
}

// A scope is added to every read, whatever door the request came in by. It is
// ANDed in, so a client's own filter narrows further rather than replacing it.
func TestWithScopeNarrowsEveryRead(t *testing.T) {
	tenant := WithScope[Widget, int64, WidgetUpdate](func(c *gin.Context) ([]crud.Option, error) {
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
	app, fake := mount(t, WithScope[Widget, int64, WidgetUpdate](func(c *gin.Context) ([]crud.Option, error) {
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
	config := WithQuery[Widget, int64, WidgetUpdate](&query.Config{
		Preloadable: []string{"Owner"},
		Sortable:    []string{"Name"},
	})

	t.Run("what the config allows", func(t *testing.T) {
		app, fake := mount(t, config)
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
			app, fake := mount(t, config)
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
	app, fake := mount(t, WithErrorHandler[Widget, int64, WidgetUpdate](func(c *gin.Context, err error) {
		if errors.Is(err, crud.ErrNotFound) {
			c.AbortWithStatusJSON(http.StatusGone, gin.H{"gone": true})
			return
		}
		c.AbortWithStatusJSON(http.StatusTeapot, gin.H{"gone": false})
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
	app := gin.New()
	New(fake).Mount(app, "/widgets")

	ok(t, app, http.MethodGet, "/widgets?preload=owner&f=price:gte:100", "", http.StatusOK)

	if got := fake.methods(); !slices.Equal(got, []string{"Get"}) {
		t.Fatalf("an unconfigured handler called %v, want [Get]", got)
	}
	if got := preloadPaths(fake.only(t, "Get").Opts); !slices.Equal(got, []string{"Owner"}) {
		t.Fatalf("without a query config every mapped relation is preloadable, got %v", got)
	}
}

// NewFor's fourth type parameter is inferred like the other three, from the
// mapper rather than from the repository. The twin above,
// TestNewInfersItsTypeParametersFromTheRepository, is the half a fourth
// parameter on New would have broken ([[D-022]]).
func TestNewForInfersItsInputFromTheMapper(t *testing.T) {
	fake := newFake()
	app := mountHandler(NewFor(fake, widgetMapper{}))

	ok(t, app, http.MethodPost, "/widgets", `{"label":"bolt","price":250}`, http.StatusCreated)

	if got := fake.only(t, "Save").Model; got.Name != "bolt" || got.Price != 250 {
		t.Fatalf("the mapper's output reached the repository as %+v, want the label as the name", got)
	}
}

// The hook runs after the fields a client may not choose have been cleared, and
// it stayed that way when the clearing moved into the service ([[UC-013]]
// guarantee 7). A hook that ran first would be handed a client-chosen key and a
// forged timestamp, and whatever it stamped on top of them would be saved.
func TestTheHookStillRunsAfterTheServerOwnedFieldsAreCleared(t *testing.T) {
	var seen Widget
	hook := BeforeSave[Widget, int64, WidgetUpdate](func(_ *gin.Context, w *Widget) error {
		seen = *w
		return nil
	})

	t.Run("on create", func(t *testing.T) {
		seen = Widget{}
		app, _ := mount(t, hook)

		ok(t, app, http.MethodPost, "/widgets", `{"id":999,"name":"bolt","createdAt":"2001-02-03T04:05:06Z","version":99,"deletedAt":"2001-02-03T04:05:06Z"}`, http.StatusCreated)

		if seen.ID != 0 {
			t.Fatalf("the hook was handed id %d; it runs after the clearing, so a client-chosen key must not reach it", seen.ID)
		}
		if !seen.CreatedAt.IsZero() {
			t.Fatalf("the hook was handed a forged %v in a generated column", seen.CreatedAt)
		}
		if seen.Version != 0 || seen.DeletedAt != nil {
			t.Fatalf("the hook was handed repository-owned state: version=%d deletedAt=%v", seen.Version, seen.DeletedAt)
		}
	})

	t.Run("on replace, where the key comes from the path", func(t *testing.T) {
		seen = Widget{}
		app, _ := mount(t, hook)

		ok(t, app, http.MethodPut, "/widgets/42", `{"id":999,"name":"bolt","createdAt":"2001-02-03T04:05:06Z","version":99,"deletedAt":"2001-02-03T04:05:06Z"}`, http.StatusOK)

		if seen.ID != 42 {
			t.Fatalf("the hook was handed id %d, want the 42 from the path", seen.ID)
		}
		if !seen.CreatedAt.IsZero() {
			t.Fatalf("the hook was handed a forged %v in a generated column", seen.CreatedAt)
		}
		if seen.Version != 0 || seen.DeletedAt != nil {
			t.Fatalf("replace handed repository-owned state to the hook: version=%d deletedAt=%v", seen.Version, seen.DeletedAt)
		}
	})

	t.Run("and the control: with AllowClientID it sees the client's key", func(t *testing.T) {
		seen = Widget{}
		app, _ := mount(t, hook, AllowClientID[Widget, int64, WidgetUpdate]())

		ok(t, app, http.MethodPost, "/widgets", `{"id":999,"name":"bolt"}`, http.StatusCreated)

		if seen.ID != 999 {
			t.Fatalf("with the key space handed over the hook saw id %d, want 999 — without this leg the two above would pass for a hook that never sees anything", seen.ID)
		}
	})
}

// Serving is handed a service that is already built, so an option that
// configures one has nowhere to go. It is refused by name at declaration rather
// than ignored at run time: an API whose author believed it was bounded and is
// not is the failure [[D-021]] says must happen at start-up.
func TestAServiceShapedOptionOnServingIsRefusedAtDeclaration(t *testing.T) {
	service := port.NewService[Widget, int64, WidgetUpdate](newFake())

	for _, tc := range []struct {
		name string
		opt  Option[Widget, int64, WidgetUpdate]
	}{
		{"WithQuery", WithQuery[Widget, int64, WidgetUpdate](&query.Config{})},
		{"AllowClientID", AllowClientID[Widget, int64, WidgetUpdate]()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				p := recover()
				if p == nil {
					t.Fatal("Serving accepted an option that configures the service; it would have been silently ignored")
				}
				if !strings.Contains(fmt.Sprint(p), tc.name) {
					t.Fatalf("the refusal does not name the option that has to be moved: %v", p)
				}
			}()
			Serving[Widget, int64, WidgetUpdate](service, tc.opt)
		})
	}

	// The control: through New the same two options are honoured. Without it
	// the legs above would pass for a binding that refused every option.
	t.Run("and the control: through New both are honoured", func(t *testing.T) {
		app, fake := mount(t, AllowClientID[Widget, int64, WidgetUpdate]())
		ok(t, app, http.MethodPost, "/widgets", `{"id":999,"name":"bolt"}`, http.StatusCreated)
		if got := fake.only(t, "Save").Model.ID; got != 999 {
			t.Fatalf("AllowClientID through New was ignored: the repository was asked to write id %d", got)
		}

		bounded, _ := mount(t, WithQuery[Widget, int64, WidgetUpdate](&query.Config{Filterable: []string{"Name"}}))
		if r := do(t, bounded, http.MethodGet, "/widgets?f=price:gte:100", ""); r.status != http.StatusBadRequest {
			t.Fatalf("WithQuery through New was ignored: a filter outside the allow-list answered %d", r.status)
		}
	})
}

// localeCatalogue declares one key in two locales, so the test can say which
// one the ladder was asked for. The real errs.Messages rather than a fake, so
// the locale ladder — fr-CA, then fr, then the default — is the one a consumer
// gets.
func localeCatalogue(t *testing.T) *errs.Messages {
	t.Helper()
	m := errs.NewMessages(nil)
	for _, e := range []struct{ locale, text string }{
		{"fr", "cette adresse est deja prise"},
		{"", "that address is taken"},
	} {
		if err := m.Add(e.locale, "name.unique", e.text); err != nil {
			t.Fatalf("declaring the %q message: %v", e.locale, err)
		}
	}
	return m
}

// The locale a client asked for reaches the message ladder. Nothing tested this
// end to end through a binding before phase 9, and it is the arm the gRPC
// metadata test mirrors.
func TestTheRequestLocaleReachesTheMessageLadder(t *testing.T) {
	taken := errs.Conflict().Code(errs.CodeUnique).
		Field("Name").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()

	message := func(t *testing.T, header string) string {
		t.Helper()
		app, f := mount(t, WithRenderer[Widget, int64, WidgetUpdate](
			crudhttp.NewRenderer(crudhttp.WithMessages(localeCatalogue(t)))))
		f.err = taken
		r := localeRequest(t, app, header)
		if r.status != http.StatusConflict {
			t.Fatalf("a duplicate key answered %d: %s", r.status, r.body)
		}
		var env struct {
			Errors struct {
				Validation []struct {
					Message string `json:"message"`
				} `json:"validation"`
			} `json:"errors"`
		}
		r.decode(t, &env)
		if len(env.Errors.Validation) != 1 {
			t.Fatalf("the body carries %d validation violations: %s", len(env.Errors.Validation), r.body)
		}
		return env.Errors.Validation[0].Message
	}

	if got := message(t, "fr-CA,fr;q=0.9"); got != "cette adresse est deja prise" {
		t.Fatalf("with Accept-Language fr-CA the message is %q; the first tag is what the ladder is asked for", got)
	}

	// The control: with no Accept-Language the default entry wins. Without it a
	// catalogue that answered the same sentence whatever it was asked would
	// pass the leg above.
	if got := message(t, ""); got != "that address is taken" {
		t.Fatalf("with no Accept-Language the message is %q, want the default-locale entry", got)
	}
}

// localeRequest sends one create through the mounted mux with the header under
// test, or without it when the header is empty.
func localeRequest(t *testing.T, r *gin.Engine, header string) response {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/widgets", bytes.NewReader([]byte(`{"name":"bolt"}`)))
	request.Header.Set("Content-Type", "application/json")
	if header != "" {
		request.Header.Set("Accept-Language", header)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, request)
	return response{status: w.Code, body: w.Body.Bytes(), header: w.Header()}
}

// An unconfigured bulk delete is capped too.
//
// MaxBulk used to mean "unlimited" at zero, which is what every binding read an
// unset field as — so the cardinality of a bulk delete was bounded only by the
// request body, and every id becomes a bound parameter. PostgreSQL refuses a
// statement past 65535 of them, so the honest 400 arrived from the driver, as a
// 500, after the statement was built. port.Rules.BulkCap is the one place the
// four transports read it from, so they cannot disagree about it again.
func TestAnUnconfiguredBulkDeleteIsStillCapped(t *testing.T) {
	app, fake := mount(t)

	ids := make([]string, port.DefaultMaxBulk+1)
	for i := range ids {
		ids[i] = strconv.Itoa(i + 1)
	}
	r := do(t, app, http.MethodPost, "/widgets/bulk-delete", `{"ids":[`+strings.Join(ids, ",")+`]}`)
	if r.status != http.StatusBadRequest {
		t.Fatalf("%d ids on an unconfigured handler answered %d, want 400: %s", len(ids), r.status, r.body)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("the request was refused and the repository was still asked: %v", fake.methods())
	}

	// The control. All of that would hold for a handler that refused every bulk
	// delete, so a request at the default cap has to get through.
	app2, _ := mount(t)
	small := `{"ids":[1,2,3]}`
	if r := do(t, app2, http.MethodPost, "/widgets/bulk-delete", small); r.status != http.StatusOK {
		t.Fatalf("three ids under the default cap answered %d: %s", r.status, r.body)
	}
}
