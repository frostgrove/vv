package crudfiber

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/errs"
)

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
		fake.err = crud.ErrNotFound

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

func TestWithScopeReachesTheReadsAndSaysNothingAboutTheWrites(t *testing.T) {
	scoped := WithScope[Widget, int64, WidgetUpdate](func(fiber.Ctx) ([]crud.Option, error) {
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

func TestARowHiddenFromReadsIsStillDeletableByID(t *testing.T) {
	app, fake := mount(t, WithScope[Widget, int64, WidgetUpdate](func(fiber.Ctx) ([]crud.Option, error) {
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

func TestAMisspelledQueryKeyIs400(t *testing.T) {
	app, fake := mount(t)

	response := ok(t, app, http.MethodPost, "/widgets/query", `{"filtr":{"name":"x"}}`, http.StatusBadRequest)
	if !strings.Contains(string(response.body), "filtr") {
		t.Fatalf("the body does not name the offending key: %s", response.body)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("%d repository calls: the statement went out anyway", len(fake.calls))
	}
}

func TestAMisspelledQueryParameterIs400(t *testing.T) {
	app, fake := mount(t)

	ok(t, app, http.MethodGet, "/widgets?filtr=name:eq:x", "", http.StatusBadRequest)
	if len(fake.calls) != 0 {
		t.Fatalf("%d repository calls: the unfiltered read went out anyway", len(fake.calls))
	}
}

type leaky struct {
	Code                                  string
	Message                               string
	ConstraintName, TableName, SchemaName string
	ColumnName                            string
}

func (this *leaky) Error() string    { return this.Message }
func (this *leaky) SQLState() string { return this.Code }

type mysqlish struct {
	Number   uint16
	SQLState [5]byte
	Message  string
}

func (this *mysqlish) Error() string { return this.Message }

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

	if got.Code != "unique" {
		t.Fatalf("the 409 answered error_code %q, and a client has to be able to branch on it", got.Code)
	}
	if got.Message == "" {
		t.Fatalf("the 409 carries no message: %s", r.body)
	}

	my := &mysqlish{Number: 1062, Message: "Duplicate entry 'bolt' for key 'widgets.name'"}
	copy(my.SQLState[:], "23000")
	app, fake = mount(t)
	fake.err = sqlfault.Wrap(sqlfault.New("mysql"), my)

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

	app, fake = mount(t)
	fake.err = errs.Conflict().Code(errs.CodeUnique).Field("Name").Code(errs.CodeUnique).
		Wrapping(crud.ErrConflict).Fault()
	if r := do(t, app, http.MethodPost, "/widgets", `{"name":"bolt"}`); r.status != http.StatusConflict {
		t.Fatalf("a collision answered %d, want 409: %s", r.status, r.body)
	}
}

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

	app, fake = mount(t)
	fake.err = errs.Conflict().Code(errs.CodeUnique).Wrapping(crud.ErrConflict).Fault()
	r = do(t, app, http.MethodPost, "/widgets", `{"name":"bolt"}`)
	if got := r.header.Get("Retry-After"); got != "" {
		t.Fatalf("a 409 advertises Retry-After: %q", got)
	}
}

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

	app, fake = mount(t)
	fake.err = enriched(t, "audit_log", "name")
	r = do(t, app, http.MethodPost, "/widgets", `{"name":"bolt","price":250}`)
	if got := failed(t, r); len(got.Field) != 0 {
		t.Fatalf("a violation on another table named %v as a field of this one", got.Field)
	}
}

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
