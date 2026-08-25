package crudnet

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/sqlfault"
)

// POST refuses a client-chosen key when the database generates it, and PUT is
// the other write route to the same table. It used to hand the URL's id
// straight to the repository, so /widgets/999 created row 999 — and on
// PostgreSQL an explicit insert into a serial column does not advance the
// sequence, so the next POST collides on the primary key and keeps colliding.
func TestPutIsNotAWayAroundAllowClientID(t *testing.T) {
	t.Run("POST cannot choose an id", func(t *testing.T) {
		app, fake := mount(t)
		ok(t, app, http.MethodPost, "/widgets", `{"id":999,"name":"bolt"}`, http.StatusCreated)
		if got := fake.only(t, "Save").Model.ID; got != 0 {
			t.Fatalf("the repository was asked to write id %d, want the key cleared", got)
		}
	})

	t.Run("and neither can PUT", func(t *testing.T) {
		app, fake := mount(t)
		fake.err = crud.ErrNotFound // no row 999 to replace

		r := do(t, app, http.MethodPut, "/widgets/999", `{"name":"bolt"}`)

		if r.status != http.StatusNotFound {
			t.Fatalf("PUT to a key that does not exist answered %d, want 404: %s", r.status, r.body)
		}
		if slices.Contains(fake.methods(), "Save") {
			t.Fatalf("PUT created a row at a key the client picked: %v", fake.methods())
		}
	})

	t.Run("PUT still replaces a row that is there", func(t *testing.T) {
		app, fake := mount(t)
		ok(t, app, http.MethodPut, "/widgets/42", `{"name":"replaced"}`, http.StatusOK)
		if got := fake.only(t, "Save").Model; got.ID != 42 || got.Name != "replaced" {
			t.Fatalf("the replacement wrote %+v", got)
		}
	})

	t.Run("AllowClientID hands the key space back to the client", func(t *testing.T) {
		app, fake := mount(t, AllowClientID[Widget, int64, WidgetUpdate]())

		ok(t, app, http.MethodPut, "/widgets/999", `{"name":"bolt"}`, http.StatusOK)

		if got := fake.methods(); !slices.Equal(got, []string{"Save"}) {
			t.Fatalf("the handler made %v; with the key space handed over there is nothing to look up", got)
		}
		if got := fake.only(t, "Save").Model.ID; got != 999 {
			t.Fatalf("the repository was asked to write id %d, want the 999 from the URL", got)
		}
	})
}

// WithScope reaches the reads and nothing else, because Save, Update and Delete
// take no options — there is nowhere for a predicate to go. That is documented,
// and pinned here so it cannot quietly drift into looking like protection: with
// a read scope in place the same id is 404 on GET and 200 on DELETE.
func TestWithScopeReachesTheReadsAndSaysNothingAboutTheWrites(t *testing.T) {
	scoped := WithScope[Widget, int64, WidgetUpdate](func(*http.Request) ([]crud.Option, error) {
		return []crud.Option{crud.Where(crud.Eq("OwnerID", int64(7)))}, nil
	})

	for _, tc := range []struct {
		name       string
		method     string
		target     string
		body       string
		wantScoped bool
	}{
		{"list", http.MethodGet, "/widgets", "", true},
		{"query", http.MethodPost, "/widgets/query", `{"limit":5}`, true},
		{"count", http.MethodGet, "/widgets/count", "", true},
		{"one entity", http.MethodGet, "/widgets/42", "", true},
		{"delete", http.MethodDelete, "/widgets/42", "", false},
		{"bulk delete", http.MethodPost, "/widgets/bulk-delete", `{"ids":[1]}`, false},
		{"update", http.MethodPatch, "/widgets/42", `{"name":"x"}`, false},
		{"create", http.MethodPost, "/widgets", `{"name":"x"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, fake := mount(t, scoped)
			do(t, app, tc.method, tc.target, tc.body)

			var scopedCalls int
			for _, c := range fake.calls {
				if c.Opts != nil && len(c.Opts.Filter) > 0 {
					scopedCalls++
				}
			}
			if tc.wantScoped && scopedCalls == 0 {
				t.Fatalf("%s %s reached the repository unscoped", tc.method, tc.target)
			}
			if !tc.wantScoped && scopedCalls > 0 {
				t.Fatalf("%s %s carried a scope; if writes are scoped now, WithScope's "+
					"documentation and the 404/200 asymmetry it warns about both need rewriting",
					tc.method, tc.target)
			}
		})
	}
}

// The asymmetry, stated as the outcome a reader would meet: a row a read scope
// hides is still deletable by id through the same handler.
func TestARowHiddenFromReadsIsStillDeletableByID(t *testing.T) {
	app, fake := mount(t, WithScope[Widget, int64, WidgetUpdate](func(*http.Request) ([]crud.Option, error) {
		return []crud.Option{crud.Where(crud.Eq("OwnerID", int64(7)))}, nil
	}))

	r := ok(t, app, http.MethodDelete, "/widgets/1", "", http.StatusOK)

	var body map[string]int64
	r.decode(t, &body)
	if body["deleted"] != 1 {
		t.Fatalf("delete answered %s", r.body)
	}
	if got := fake.only(t, "Delete").IDs; !slices.Equal(got, []int64{1}) {
		t.Fatalf("the repository was asked to delete %v", got)
	}
}

// A conflict is a client-visible outcome, so it must not fall into the 500 that
// deliberately says nothing. The adapters classify a driver's integrity errors
// into crud.ErrConflict; this is the other end of that wire.
func TestAnIntegrityConflictIsA409WithAMessage(t *testing.T) {
	app, fake := mount(t)
	fake.err = errors.Join(crud.ErrConflict, errors.New("duplicate key value violates unique constraint"))

	r := do(t, app, http.MethodPost, "/widgets", `{"name":"bolt"}`)

	if r.status != http.StatusConflict {
		t.Fatalf("a duplicate key answered %d, want 409: %s", r.status, r.body)
	}
	if got := failed(t, r); got.Code != "conflict" || got.Message == "" {
		t.Fatalf("the envelope was %+v; a 409 names the error and says something about it", got)
	}
}

// A misspelled key in the query document used to parse into an empty request:
// the endpoint answered 200 with the whole table, which is the one failure a
// client cannot see. The transport half of that fix is here — the refusal has to
// survive Fiber's binding and arrive as a 400 naming the key.
func TestAMisspelledQueryKeyIs400(t *testing.T) {
	app, fake := mount(t)

	res := ok(t, app, http.MethodPost, "/widgets/query", `{"filtr":{"name":"x"}}`, http.StatusBadRequest)
	if !strings.Contains(string(res.body), "filtr") {
		t.Fatalf("the body does not name the offending key: %s", res.body)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("%d repository calls: the statement went out anyway", len(fake.calls))
	}
}

// And the query-string half, for a parameter that is one typo from one of ours.
func TestAMisspelledQueryParameterIs400(t *testing.T) {
	app, fake := mount(t)

	ok(t, app, http.MethodGet, "/widgets?filtr=name:eq:x", "", http.StatusBadRequest)
	if len(fake.calls) != 0 {
		t.Fatalf("%d repository calls: the unfiltered read went out anyway", len(fake.calls))
	}
}

// ---------------------------------------------------------------------------
// what a classified conflict tells the client

// leaky is a driver error shaped like PostgreSQL's, and everything on it is
// something a response body must not carry.
type leaky struct {
	Code                                  string
	Message                               string
	ConstraintName, TableName, SchemaName string
	ColumnName                            string
}

func (e *leaky) Error() string    { return e.Message }
func (e *leaky) SQLState() string { return e.Code }

// mysqlish is the other shape, and it is here for one assertion the PostgreSQL
// one cannot make: the engine's own number. Searching a body for a zero would be
// matched by any digit it prints.
type mysqlish struct {
	Number   uint16
	SQLState [5]byte
	Message  string
}

func (e *mysqlish) Error() string { return e.Message }

// [[D-044]] on the wire, and [[D-038]]'s seam control against a real produced
// fault: the fault still answers 409 because the sentinel it wraps is still
// reachable, and the body carries a code and a sentence and nothing a driver
// said.
//
// The control this test used to carry has inverted, and that is the phase-4
// change in one place. It used to assert that the *unclassified* conflict
// beside it still leaked the constraint name, so that something closing the
// leak would redden here and say the positive leg had stopped measuring
// anything. Phase 4 is that something: nothing reads err.Error() any more, so
// the leak is closed for classified and unclassified alike. What replaces it is
// the same idea one step earlier — the secrets are asserted present on the Go
// value before the body is searched for them.
func TestAClassifiedConflictsBodyCarriesNothingInternal(t *testing.T) {
	driver := &leaky{
		Code:           "23505",
		Message:        `ERROR: duplicate key value violates unique constraint "widgets_name_key" (INSERT INTO widgets (name) VALUES ($1)) [host=db.internal user=vv password=hunter2]`,
		ConstraintName: "widgets_name_key",
		TableName:      "widgets",
		SchemaName:     "public",
		ColumnName:     "name",
	}
	leaks := map[string]string{
		"the constraint":        "widgets_name_key",
		"the schema":            "public",
		"the SQLSTATE":          "23505",
		"the statement":         "INSERT INTO widgets",
		"the connection string": "host=db.internal",
	}
	// The control: every fragment has to be in the fixture, or finding it absent
	// from the body proves nothing.
	for name, leak := range leaks {
		var found bool
		for _, said := range []string{driver.Message, driver.Code, driver.ConstraintName, driver.TableName, driver.SchemaName, driver.ColumnName} {
			found = found || strings.Contains(said, leak)
		}
		if !found {
			t.Fatalf("%s (%q) is not in the fixture, so searching the body for it says nothing", name, leak)
		}
	}

	app, fake := mount(t)
	fake.err = sqlfault.Wrap(sqlfault.New("postgres"), driver)

	r := do(t, app, http.MethodPost, "/widgets", `{"name":"bolt"}`)
	if r.status != http.StatusConflict {
		t.Fatalf("a classified duplicate key answered %d, want 409: %s", r.status, r.body)
	}
	got := failed(t, r)
	for name, leak := range leaks {
		if strings.Contains(got.Message, leak) {
			t.Fatalf("the 409 body names %s: %s", name, r.body)
		}
	}
	// And it is not merely empty: a client still learns what went wrong, and
	// learns it from the code rather than from a sentence it would have to
	// parse. A renderer emitting {} passes "names nothing" perfectly.
	if got.Code != "unique" {
		t.Fatalf("the 409 answered error_code %q, and a client has to be able to branch on it", got.Code)
	}
	if got.Message == "" {
		t.Fatalf("the 409 carries no message: %s", r.body)
	}

	// The engine's own number, guarded separately: it is an int, and zero
	// searched for as "0" would be matched by any digit the body prints.
	my := &mysqlish{Number: 1062, Message: "Duplicate entry 'bolt' for key 'widgets.name'"}
	copy(my.SQLState[:], "23000")
	app, fake = mount(t)
	fake.err = sqlfault.Wrap(sqlfault.New("mysql"), my)
	// The control this leg needs, and the one the loop above gets for free: the
	// number has to be in a produced fault. Unclassified, the sentinel gate
	// alone answers the same 409 and the body carries the driver's message,
	// which never named the number either — so the assertion below would pass
	// with MySQL classification deleted.
	mf, isFault := errs.AsFault(fake.err)
	if !isFault {
		t.Fatal("the MySQL fixture did not classify, so the 409 below comes from the sentinel gate and searching its body says nothing")
	}
	if mf.Detail.Native != 1062 {
		t.Fatalf("the fault's Detail.Native = %d, so searching the body for 1062 proves nothing", mf.Detail.Native)
	}
	r = do(t, app, http.MethodPost, "/widgets", `{"name":"bolt"}`)
	if r.status != http.StatusConflict {
		t.Fatalf("a classified MySQL duplicate key answered %d, want 409: %s", r.status, r.body)
	}
	if strings.Contains(failed(t, r).Message, "1062") {
		t.Fatalf("the 409 body names the engine's own number: %s", r.body)
	}

	// The second control, and the one that keeps this test honest now that the
	// old one has inverted: the *unclassified* conflict — the same driver error
	// with no engine declared — used to answer 409 with the constraint name in
	// the body, and must not any more. Its secrets are asserted reachable on the
	// Go error first, so finding them absent from the body measures the
	// renderer rather than an empty fixture.
	app, fake = mount(t)
	fake.err = errors.Join(crud.ErrConflict, driver)
	if !strings.Contains(fake.err.Error(), "widgets_name_key") {
		t.Fatal("the unclassified fixture does not carry the constraint name in its own text, so searching the body for it says nothing")
	}
	if _, classified := errs.AsFault(fake.err); classified {
		t.Fatal("the unclassified fixture classified, so it is not the unclassified leg at all")
	}
	r = do(t, app, http.MethodPost, "/widgets", `{"name":"bolt"}`)
	if r.status != http.StatusConflict {
		t.Fatalf("an unclassified duplicate key answered %d, want 409: %s", r.status, r.body)
	}
	got = failed(t, r)
	for name, leak := range leaks {
		if strings.Contains(got.Message, leak) {
			t.Fatalf("an unclassified 409 still names %s: %s", name, r.body)
		}
	}
	if got.Code != "conflict" {
		t.Fatalf("an unclassified conflict answered error_code %q; a client still has to be able to branch", got.Code)
	}
}

// The 422 arm. A value the engine refused for what it *is* rather than for
// colliding with something is not a conflict, and a client can now tell the two
// apart from the status alone.
func TestAValidationFaultIsA422(t *testing.T) {
	app, fake := mount(t)
	fake.err = errs.Validation().Op("Save").Entity("Widget").Code(errs.CodeTooLong).
		Field("Name").Code(errs.CodeTooLong).Origin(errs.OriginState).Fault()

	r := do(t, app, http.MethodPost, "/widgets", `{"name":"bolt"}`)

	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("a value the engine refused answered %d, want 422: %s", r.status, r.body)
	}
	if got := failed(t, r); got.Code != "too_long" || got.path() != "name" {
		t.Fatalf("the envelope was %+v, want too_long at name", got)
	}

	// The control: a collision on the same route is still a 409. Without it a
	// table answering 422 to everything passes.
	app, fake = mount(t)
	fake.err = errs.Conflict().Code(errs.CodeUnique).Field("Name").Code(errs.CodeUnique).
		Wrapping(crud.ErrConflict).Fault()
	if r := do(t, app, http.MethodPost, "/widgets", `{"name":"bolt"}`); r.status != http.StatusConflict {
		t.Fatalf("a collision answered %d, want 409: %s", r.status, r.body)
	}
}

// [[D-040]]'s 503, owed since phase 3. A deadlock is not the client's mistake
// and the same request succeeds unmodified a moment later, so a 4xx would tell
// it to change something it has no way to change.
func TestARetryableFailureIsA503WithRetryAfter(t *testing.T) {
	app, fake := mount(t)
	fake.err = errs.Retryable().Op("Save").Entity("Widget").Code(errs.CodeDeadlock).Fault()

	r := do(t, app, http.MethodPost, "/widgets", `{"name":"bolt"}`)

	if r.status != http.StatusServiceUnavailable {
		t.Fatalf("a deadlock answered %d, want 503: %s", r.status, r.body)
	}
	if got := r.header.Get("Retry-After"); got == "" {
		t.Fatalf("the 503 carries no Retry-After: %v", r.header)
	}
	if got := failed(t, r).Code; got != "deadlock" {
		t.Fatalf("the envelope names the error %q, want deadlock", got)
	}

	// The control: the 409 beside it carries no Retry-After. Without it, a
	// renderer that stamped the header on every response would pass.
	app, fake = mount(t)
	fake.err = errs.Conflict().Code(errs.CodeUnique).Wrapping(crud.ErrConflict).Fault()
	r = do(t, app, http.MethodPost, "/widgets", `{"name":"bolt"}`)
	if got := r.header.Get("Retry-After"); got != "" {
		t.Fatalf("a 409 advertises Retry-After: %q", got)
	}
}

// The whole point of the chain, end to end: a column the client never sent
// arrives as the key it did send. The decorator resolves the column to the
// model field ([[D-043]]'s first hop) and the renderer's raw-body fallback
// carries it the rest of the way.
func TestAConstraintViolationNamesTheFieldTheClientSent(t *testing.T) {
	app, fake := mount(t)
	fake.err = enriched(t, "widgets", "name")

	r := do(t, app, http.MethodPost, "/widgets", `{"name":"bolt","price":250}`)

	if r.status != http.StatusConflict {
		t.Fatalf("a duplicate key answered %d, want 409: %s", r.status, r.body)
	}
	got := failed(t, r)
	if got.path() != "name" {
		t.Fatalf("the violation names %q, want the key the client sent: %s", got.path(), r.body)
	}
	if strings.Contains(string(r.body), "widgets") || strings.Contains(string(r.body), "_key") {
		t.Fatalf("the body names the table or the constraint: %s", r.body)
	}

	// The control: a fault whose table is not this model's yields no field at
	// all. Without it, a decorator that translated every column it was handed
	// — including another table's `name` — would pass the leg above.
	app, fake = mount(t)
	fake.err = enriched(t, "audit_log", "name")
	r = do(t, app, http.MethodPost, "/widgets", `{"name":"bolt","price":250}`)
	if got := failed(t, r); len(got.Field) != 0 {
		t.Fatalf("a violation on another table named %v as a field of this one", got.Field)
	}
}

// enriched runs a driver error through the two layers that fill a path in: the
// adapter's classifier and the faults decorator.
func enriched(t *testing.T, table, column string) error {
	t.Helper()
	f, ok := errs.AsFault(sqlfault.Wrap(sqlfault.New("postgres"), &leaky{
		Code:           "23505",
		Message:        `ERROR: duplicate key value violates unique constraint "widgets_name_key"`,
		ConstraintName: "widgets_name_key",
		TableName:      table,
		ColumnName:     column,
	}))
	if !ok {
		t.Fatal("the fixture did not classify, so nothing downstream has a Source to translate")
	}
	// The decorator's own hop, applied here rather than through a repository:
	// this package has no crud.Core to wrap, and repo/decorators/faults is
	// where that path is tested.
	g := *f
	g.Violations = append([]errs.Violation(nil), f.Violations...)
	for i := range g.Violations {
		v := &g.Violations[i]
		if v.Source.Table != "widgets" {
			v.Approximate = true
			continue
		}
		if fld := widgetMeta.Schema.Field(v.Source.Columns[0]); fld != nil {
			v.Path = errs.Path{errs.Named(fld.Name)}
		}
	}
	return &g
}
