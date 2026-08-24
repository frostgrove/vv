package crudfiber

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/shardit-io/rx/crud"
)

// Every route the package documents, with the verb it answers, the status it
// answers with, and the repository method it is a front door for. A route that
// stops being mounted, changes its success code or starts calling something
// else fails here first.
func TestRoutesMountEveryDocumentedEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name       string
		method     string
		target     string
		body       string
		wantStatus int
		wantCalls  []string
	}{
		{"list", http.MethodGet, "/widgets", "", http.StatusOK, []string{"Get"}},
		{"query", http.MethodPost, "/widgets/query", `{"limit":5}`, http.StatusOK, []string{"Get"}},
		{"count by query string", http.MethodGet, "/widgets/count", "", http.StatusOK, []string{"Count"}},
		{"count by document", http.MethodPost, "/widgets/count", `{"filter":{"price":{"gte":100}}}`, http.StatusOK, []string{"Count"}},
		{"one entity", http.MethodGet, "/widgets/42", "", http.StatusOK, []string{"GetByID"}},
		{"create", http.MethodPost, "/widgets", `{"name":"bolt"}`, http.StatusCreated, []string{"Save"}},
		{"partial update", http.MethodPatch, "/widgets/42", `{"name":"renamed"}`, http.StatusOK, []string{"Update"}},
		// PUT on a database-generated key replaces and never creates, so it
		// looks the row up before writing it.
		{"replace", http.MethodPut, "/widgets/42", `{"name":"replaced"}`, http.StatusOK, []string{"GetByID", "Save"}},
		{"delete one", http.MethodDelete, "/widgets/42", "", http.StatusOK, []string{"Delete"}},
		{"bulk delete", http.MethodPost, "/widgets/bulk-delete", `{"ids":[1,2]}`, http.StatusOK, []string{"Delete"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, fake := mount(t)
			r := do(t, app, tc.method, tc.target, tc.body)
			if r.status != tc.wantStatus {
				t.Fatalf("%s %s answered %d, want %d: %s", tc.method, tc.target, r.status, tc.wantStatus, r.body)
			}
			if got := fake.methods(); !slices.Equal(got, tc.wantCalls) {
				t.Fatalf("%s %s asked the repository for %v, want exactly %v", tc.method, tc.target, got, tc.wantCalls)
			}
		})
	}
}

// /count is registered before /:id, so the literal path wins instead of being
// read as an entity called "count".
func TestCountIsNotSwallowedByTheIDRoute(t *testing.T) {
	app, fake := mount(t)

	ok(t, app, http.MethodGet, "/widgets/count", "", http.StatusOK)

	if got := fake.methods(); !slices.Equal(got, []string{"Count"}) {
		t.Fatalf("GET /widgets/count asked the repository for %v; the :id route swallowed it", got)
	}
}

// Register mounts on a router that already exists — the other half of the
// documented set-up, and the one a real application with a versioned group uses.
func TestRegisterMountsOnAnExistingRouter(t *testing.T) {
	fake := newFake()
	app := fiber.New()
	New[Widget, int64, WidgetUpdate](fake).Register(app.Group("/api/widgets"))

	ok(t, app, http.MethodGet, "/api/widgets", "", http.StatusOK)
	ok(t, app, http.MethodGet, "/api/widgets/count", "", http.StatusOK)
	ok(t, app, http.MethodGet, "/api/widgets/42", "", http.StatusOK)
	ok(t, app, http.MethodPost, "/api/widgets", `{"name":"bolt"}`, http.StatusCreated)

	want := []string{"Get", "Count", "GetByID", "Save"}
	if got := fake.methods(); !slices.Equal(got, want) {
		t.Fatalf("the group answered with %v, want %v", got, want)
	}
}

func TestListCompilesQueryStringPagingAndSorting(t *testing.T) {
	app, fake := mount(t)

	ok(t, app, http.MethodGet, "/widgets?page=3&limit=25&sort=-price,name", "", http.StatusOK)

	o := fake.only(t, "Get").Opts
	if o.Page != 3 || o.Limit != 25 {
		t.Fatalf("?page=3&limit=25 reached the repository as page %d limit %d", o.Page, o.Limit)
	}
	want := []string{"-Price", "Name"}
	if got := sortTerms(o); !slices.Equal(got, want) {
		t.Fatalf("?sort=-price,name compiled to %v, want %v", got, want)
	}
}

// Fiber's Queries() folds repeated parameters into a map, which would keep only
// the last `f=` term. The handler reads the raw query args instead, so every
// repeat survives: the filter is the AND of all of them, in the order sent.
func TestRepeatedFilterTermsAllSurvive(t *testing.T) {
	app, fake := mount(t)

	ok(t, app, http.MethodGet,
		"/widgets?f=price:gte:100&f=name:contains:bolt&f=ownerId:eq:7", "", http.StatusOK)

	sql, args := whereSQL(t, fake.only(t, "Get").Opts)
	wantSQL := `("price" >= $1 AND "name" LIKE $2 AND "owner_id" = $3)`
	if sql != wantSQL {
		t.Fatalf("three ?f= terms compiled to %s, want %s", sql, wantSQL)
	}
	if want := []any{100, "%bolt%", int64(7)}; !reflect.DeepEqual(args, want) {
		t.Fatalf("bound %#v, want %#v", args, want)
	}
}

// The same goes for every other repeatable parameter, not just the filter.
func TestRepeatedPreloadAndSortParametersAllSurvive(t *testing.T) {
	app, fake := mount(t)

	ok(t, app, http.MethodGet, "/widgets?preload=owner&preload=parts&sort=-price&sort=name", "", http.StatusOK)

	o := fake.only(t, "Get").Opts
	if got, want := preloadPaths(o), []string{"Owner", "Parts"}; !slices.Equal(got, want) {
		t.Fatalf("two ?preload= parameters compiled to %v, want %v", got, want)
	}
	if got, want := sortTerms(o), []string{"-Price", "Name"}; !slices.Equal(got, want) {
		t.Fatalf("two ?sort= parameters compiled to %v, want %v", got, want)
	}
}

// The full JSON DSL: filter, sort, preload and paging in one document, each
// landing as its own repository option.
func TestQueryBodyCompilesTheWholeDSL(t *testing.T) {
	app, fake := mount(t)

	ok(t, app, http.MethodPost, "/widgets/query", `{
		"page": 2,
		"limit": 5,
		"sort": ["-price", "owner.name"],
		"preload": ["owner", {"path": "parts", "filter": {"label": {"contains": "bolt"}}}],
		"filter": {
			"price": {"gte": 100},
			"owner.name": {"contains": "ac"}
		}
	}`, http.StatusOK)

	o := fake.only(t, "Get").Opts

	if o.Page != 2 || o.Limit != 5 {
		t.Fatalf("the document asked for page 2 limit 5, the repository got page %d limit %d", o.Page, o.Limit)
	}
	if got, want := sortTerms(o), []string{"-Price", "Owner.Name"}; !slices.Equal(got, want) {
		t.Fatalf("sort compiled to %v, want %v", got, want)
	}

	// A relation hop in a filter is a correlated EXISTS, never a join.
	sql, args := whereSQL(t, o)
	wantSQL := `(EXISTS (SELECT 1 FROM "owners" AS rx1 WHERE rx1."id" = "widgets"."owner_id" ` +
		`AND rx1."name" LIKE $1) AND "price" >= $2)`
	if sql != wantSQL {
		t.Fatalf("the filter compiled to %s, want %s", sql, wantSQL)
	}
	if want := []any{"%ac%", 100}; !reflect.DeepEqual(args, want) {
		t.Fatalf("bound %#v, want %#v", args, want)
	}

	if got, want := preloadPaths(o), []string{"Owner", "Parts"}; !slices.Equal(got, want) {
		t.Fatalf("preloads reached the repository as %v, want %v", got, want)
	}
	// The per-relation filter is compiled against the related model, not the root.
	partsSQL, partsArgs := predSQL(t, relMeta(t, "Parts"), crud.Build(o.Preloads[1].Opts...).Predicate())
	if partsSQL != `"label" LIKE $1` {
		t.Fatalf("the preload filter compiled to %q, want %q", partsSQL, `"label" LIKE $1`)
	}
	if want := []any{"%bolt%"}; !reflect.DeepEqual(partsArgs, want) {
		t.Fatalf("the preload filter bound %#v, want %#v", partsArgs, want)
	}
}

// The page a client receives: the items, then every field a pager is drawn
// from, under the names the wire format promises.
func TestListAnswersWithThePageEnvelope(t *testing.T) {
	app, _ := mount(t)

	r := ok(t, app, http.MethodGet, "/widgets", "", http.StatusOK)

	var envelope map[string]json.RawMessage
	r.decode(t, &envelope)
	want := []string{"hasNext", "hasPrev", "items", "limit", "page", "total", "totalPages"}
	got := make([]string, 0, len(envelope))
	for k := range envelope {
		got = append(got, k)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("the page envelope carries %v, want %v", got, want)
	}

	var page struct {
		Items      []Widget `json:"items"`
		Page       int      `json:"page"`
		Limit      int      `json:"limit"`
		Total      int64    `json:"total"`
		TotalPages int      `json:"totalPages"`
		HasNext    bool     `json:"hasNext"`
		HasPrev    bool     `json:"hasPrev"`
	}
	r.decode(t, &page)
	if len(page.Items) != 2 || page.Items[0].Name != "bolt" {
		t.Fatalf("items = %+v", page.Items)
	}
	if page.Page != 2 || page.Limit != 2 || page.Total != 5 || page.TotalPages != 3 {
		t.Fatalf("pager = page %d of %d, limit %d, total %d", page.Page, page.TotalPages, page.Limit, page.Total)
	}
	if !page.HasNext || !page.HasPrev {
		t.Fatalf("page 2 of 3 reported hasNext=%v hasPrev=%v", page.HasNext, page.HasPrev)
	}
}

// A count is a single number, so paging, sorting and preloads are dropped
// before the request reaches the repository — they would cost a join or a
// second round trip and could not change the answer.
func TestCountKeepsTheFilterAndDropsEverythingElse(t *testing.T) {
	app, fake := mount(t)

	r := ok(t, app, http.MethodGet,
		"/widgets/count?f=price:gte:100&page=2&limit=5&sort=name&preload=owner&select=name", "", http.StatusOK)

	var out struct {
		Count int64 `json:"count"`
	}
	r.decode(t, &out)
	if out.Count != 5 {
		t.Fatalf("count = %d, want the 5 the repository reported", out.Count)
	}

	o := fake.only(t, "Count").Opts
	if o.Page != 0 || o.Limit != 0 || o.Offset != 0 {
		t.Fatalf("a count was paginated: page %d limit %d offset %d", o.Page, o.Limit, o.Offset)
	}
	if len(o.Sort) != 0 || len(o.Preloads) != 0 || len(o.Fields) != 0 {
		t.Fatalf("a count carried sort %v, preloads %v, projection %v", o.Sort, preloadPaths(o), o.Fields)
	}
	if sql, _ := whereSQL(t, o); sql != `"price" >= $1` {
		t.Fatalf("the count lost its filter: %q", sql)
	}
}

func TestCountAcceptsTheJSONDocumentToo(t *testing.T) {
	app, fake := mount(t)

	ok(t, app, http.MethodPost, "/widgets/count",
		`{"filter":{"owner.name":"acme"},"limit":5}`, http.StatusOK)

	o := fake.only(t, "Count").Opts
	if o.Limit != 0 {
		t.Fatalf("a count was paginated: limit %d", o.Limit)
	}
	sql, args := whereSQL(t, o)
	wantSQL := `EXISTS (SELECT 1 FROM "owners" AS rx1 WHERE rx1."id" = "widgets"."owner_id" AND rx1."name" = $1)`
	if sql != wantSQL {
		t.Fatalf("the count filter compiled to %s, want %s", sql, wantSQL)
	}
	if want := []any{"acme"}; !reflect.DeepEqual(args, want) {
		t.Fatalf("bound %#v, want %#v", args, want)
	}
}

// GET /:id identifies the row by the path parameter, so only the shaping
// clauses are worth passing on; a filter or a sort could only contradict it.
func TestGetByIDPassesThePathIDAndOnlyShapingOptions(t *testing.T) {
	app, fake := mount(t)

	r := ok(t, app, http.MethodGet,
		"/widgets/42?preload=owner&select=name&f=price:gte:100&sort=name&page=3&limit=5", "", http.StatusOK)

	call := fake.only(t, "GetByID")
	if call.ID != 42 {
		t.Fatalf("the repository was asked for id %d, want 42", call.ID)
	}
	if got, want := preloadPaths(call.Opts), []string{"Owner"}; !slices.Equal(got, want) {
		t.Fatalf("preloads = %v, want %v", got, want)
	}
	if got, want := call.Opts.Fields, []string{"Name"}; !slices.Equal(got, want) {
		t.Fatalf("projection = %v, want %v", got, want)
	}
	if sql, _ := whereSQL(t, call.Opts); sql != "" {
		t.Fatalf("a filter reached a lookup by id: %s", sql)
	}
	if len(call.Opts.Sort) != 0 || call.Opts.Page != 0 || call.Opts.Limit != 0 {
		t.Fatalf("sorting or paging reached a lookup by id: sort %v page %d limit %d",
			call.Opts.Sort, call.Opts.Page, call.Opts.Limit)
	}

	var got Widget
	r.decode(t, &got)
	if got.ID != 42 || got.Name != "bolt" {
		t.Fatalf("the entity came back as %+v", got)
	}
}

// A create answers 201 with the row as the repository stored it — including the
// key the database chose, which is the only part the client cannot know.
func TestCreateReturns201WithTheStoredRow(t *testing.T) {
	app, fake := mount(t)

	r := ok(t, app, http.MethodPost, "/widgets",
		`{"ownerId":7,"name":"bolt","price":250}`, http.StatusCreated)

	saved := fake.only(t, "Save").Model
	if saved.Name != "bolt" || saved.OwnerID != 7 || saved.Price != 250 {
		t.Fatalf("the body reached the repository as %+v", saved)
	}

	var created Widget
	r.decode(t, &created)
	if created.ID != 7 {
		t.Fatalf("the response carries id %d, not the 7 the repository assigned", created.ID)
	}
}

// The two things a client may not decide for itself: a database-generated key
// and any `generated` column.
func TestCreateRefusesAClientChosenKeyAndGeneratedColumns(t *testing.T) {
	app, fake := mount(t)

	r := ok(t, app, http.MethodPost, "/widgets",
		`{"id":9999,"name":"bolt","createdAt":"1999-01-01T00:00:00Z"}`, http.StatusCreated)

	saved := fake.only(t, "Save").Model
	if saved.ID != 0 {
		t.Fatalf("the client picked its own key: the repository was handed id %d", saved.ID)
	}
	if !saved.CreatedAt.IsZero() {
		t.Fatalf("a generated column was client-set: created_at = %v", saved.CreatedAt)
	}

	var created Widget
	r.decode(t, &created)
	if !created.CreatedAt.Equal(savedAt) {
		t.Fatalf("the response should echo the stored row, got created_at = %v", created.CreatedAt)
	}
}

// A PATCH body carries only the fields it means to change; everything else must
// arrive at the repository undefined, and the row must come back unchanged in
// those columns.
func TestUpdateForwardsOnlyTheFieldsTheBodyCarried(t *testing.T) {
	app, fake := mount(t)

	r := ok(t, app, http.MethodPatch, "/widgets/42", `{"name":"renamed"}`, http.StatusOK)

	call := fake.only(t, "Update")
	if call.ID != 42 {
		t.Fatalf("the update was aimed at id %d, want 42", call.ID)
	}
	if call.DTO.Name == nil || *call.DTO.Name != "renamed" {
		t.Fatalf("name did not reach the DTO: %v", call.DTO.Name)
	}
	if call.DTO.Price != nil {
		t.Fatalf("an absent DTO field was filled in: price = %v", *call.DTO.Price)
	}
	if call.DTO.Note.IsDefined() {
		t.Fatalf("an absent DTO field was defined: note = %v", call.DTO.Note)
	}

	var patched Widget
	r.decode(t, &patched)
	if patched.Name != "renamed" {
		t.Fatalf("name = %q, want the updated value", patched.Name)
	}
	if patched.Price != 250 {
		t.Fatalf("an untouched column changed: price = %d, want 250", patched.Price)
	}
}

// An explicit null is a value, not an omission: it reaches the DTO as null and
// the row comes back with the column cleared.
func TestUpdateCarriesAnExplicitNullThrough(t *testing.T) {
	app, fake := mount(t)

	r := ok(t, app, http.MethodPatch, "/widgets/42", `{"note":null}`, http.StatusOK)

	note := fake.only(t, "Update").DTO.Note
	if !note.IsNull() {
		t.Fatalf(`{"note":null} reached the DTO as %v, want an explicit null`, note)
	}

	var patched Widget
	r.decode(t, &patched)
	if patched.Note.IsSet() {
		t.Fatalf("the column was not cleared: note = %v", patched.Note)
	}
}

// PUT replaces the whole row, and the URL decides which row that is: an id in
// the payload is overwritten rather than obeyed.
func TestReplaceTakesTheIDFromThePathNotTheBody(t *testing.T) {
	app, fake := mount(t)

	ok(t, app, http.MethodPut, "/widgets/42",
		`{"id":9999,"name":"replaced","createdAt":"1999-01-01T00:00:00Z"}`, http.StatusOK)

	saved := fake.only(t, "Save").Model
	if saved.ID != 42 {
		t.Fatalf("the row written was id %d, want the 42 from the URL", saved.ID)
	}
	if saved.Name != "replaced" {
		t.Fatalf("the body did not reach the repository: %+v", saved)
	}
	if !saved.CreatedAt.IsZero() {
		t.Fatalf("a generated column was client-set on replace: created_at = %v", saved.CreatedAt)
	}
}

func TestDeleteReportsTheNumberOfRowsRemoved(t *testing.T) {
	app, fake := mount(t)

	r := ok(t, app, http.MethodDelete, "/widgets/42", "", http.StatusOK)

	if ids := fake.only(t, "Delete").IDs; !slices.Equal(ids, []int64{42}) {
		t.Fatalf("the repository was asked to delete %v, want [42]", ids)
	}
	var out struct {
		Deleted int64 `json:"deleted"`
	}
	r.decode(t, &out)
	if out.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", out.Deleted)
	}
}

// A bulk delete is one repository call carrying every id, not one call per id.
func TestBulkDeletePassesEveryIDInOneCall(t *testing.T) {
	app, fake := mount(t)

	r := ok(t, app, http.MethodPost, "/widgets/bulk-delete", `{"ids":[4,8,15]}`, http.StatusOK)

	if ids := fake.only(t, "Delete").IDs; !slices.Equal(ids, []int64{4, 8, 15}) {
		t.Fatalf("the repository was asked to delete %v, want [4 8 15]", ids)
	}
	var out struct {
		Deleted int64 `json:"deleted"`
	}
	r.decode(t, &out)
	if out.Deleted != 3 {
		t.Fatalf("deleted = %d, want 3", out.Deleted)
	}
}

// An empty id list is answered without touching the repository — a DELETE with
// no keys is the one shape that could turn into "delete everything".
func TestBulkDeleteWithNoIDsNeverReachesTheRepository(t *testing.T) {
	app, fake := mount(t)

	r := ok(t, app, http.MethodPost, "/widgets/bulk-delete", `{"ids":[]}`, http.StatusOK)

	if len(fake.calls) != 0 {
		t.Fatalf("an empty bulk delete still called %v", fake.methods())
	}
	var out struct {
		Deleted int64 `json:"deleted"`
	}
	r.decode(t, &out)
	if out.Deleted != 0 {
		t.Fatalf("deleted = %d, want 0", out.Deleted)
	}
}

// A count with no body at all is a valid request: it counts every row.
func TestCountPostAcceptsAnEmptyBody(t *testing.T) {
	app, fake := mount(t)

	ok(t, app, http.MethodPost, "/widgets/count", "", http.StatusOK)

	o := fake.only(t, "Count").Opts
	if sql, _ := whereSQL(t, o); sql != "" {
		t.Fatalf("an empty count document produced a filter: %s", sql)
	}
}

// The DSL's flags are options in their own right, and a list request that asks
// for everything must not silently keep paginating.
func TestListHonoursUnpagedAndSkipTotal(t *testing.T) {
	app, fake := mount(t)

	ok(t, app, http.MethodGet, "/widgets?unpaged=true&skipTotal=true&distinct=true", "", http.StatusOK)

	o := fake.only(t, "Get").Opts
	if !o.Unpaged || !o.NoTotal || !o.Distinct {
		t.Fatalf("unpaged=%v skipTotal=%v distinct=%v, want all three set", o.Unpaged, o.NoTotal, o.Distinct)
	}
}

// The handler never reaches past the Repository interface, which is what lets a
// service layer with its own rules stand in for the repository.
func TestAServiceLayerCanStandInForTheRepository(t *testing.T) {
	fake := newFake()
	svc := &widgetService{fakeRepo: fake}
	app := fiber.New()
	app.Use("/widgets", New[Widget, int64, WidgetUpdate](svc).Routes())

	ok(t, app, http.MethodPost, "/widgets", `{"name":"bolt"}`, http.StatusCreated)

	if !svc.saved {
		t.Fatal("the handler bypassed the service and called the repository directly")
	}
	if got := fake.only(t, "Save").Model.Name; got != "BOLT" {
		t.Fatalf("the service's own rule did not run: the repository stored %q", got)
	}
}

// widgetService is what an application puts between the handler and the
// repository: it embeds one and overrides only the method it cares about.
type widgetService struct {
	*fakeRepo
	saved bool
}

func (s *widgetService) Save(ctx context.Context, w *Widget) error {
	s.saved = true
	w.Name = strings.ToUpper(w.Name)
	return s.fakeRepo.Save(ctx, w)
}
