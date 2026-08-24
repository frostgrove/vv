package crudnet

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/shardit-io/qq/crud"
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
	if got := failed(t, r); got.Error != "conflict" || got.Message == "" {
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
