package crudgin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/shardit-io/rx/crud"
	"github.com/shardit-io/rx/query"
)

// failed decodes the error envelope every failing request answers with. A body
// that is not that envelope is itself the bug: a client cannot branch on a
// stack trace.
func failed(t *testing.T, r response) ErrorBody {
	t.Helper()
	var body ErrorBody
	r.decode(t, &body)
	if body.Error == "" {
		t.Fatalf("a %d answered without naming the error: %s", r.status, r.body)
	}
	return body
}

// ---------------------------------------------------------------------------
// bad input

// A body that is not the shape the route expects is the client's mistake, and
// it is caught during binding — before the repository is asked to do anything
// with the half-decoded value.
func TestAMalformedBodyIsRejectedWithoutTouchingTheRepository(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
	}{
		{"truncated JSON on create", http.MethodPost, "/widgets", `{`},
		{"an array where an entity was expected", http.MethodPost, "/widgets", `[{"name":"bolt"}]`},
		{"a bare string where an entity was expected", http.MethodPost, "/widgets", `"bolt"`},
		{"an array where an update DTO was expected", http.MethodPatch, "/widgets/42", `[]`},
		{"prose where a replacement row was expected", http.MethodPut, "/widgets/42", `not json`},
		{"an array where a query document was expected", http.MethodPost, "/widgets/query", `[1,2]`},
		{"a query field of the wrong type", http.MethodPost, "/widgets/query", `{"limit":"lots"}`},
		{"prose where a count document was expected", http.MethodPost, "/widgets/count", `oops`},
		{"an id list of the wrong type", http.MethodPost, "/widgets/bulk-delete", `{"ids":["nope"]}`},
		{"an array where a bulk-delete document was expected", http.MethodPost, "/widgets/bulk-delete", `[1,2]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, fake := mount(t)

			r := do(t, app, tc.method, tc.target, tc.body)

			if r.status != http.StatusBadRequest {
				t.Fatalf("%s %s with %s answered %d, want 400: %s", tc.method, tc.target, tc.body, r.status, r.body)
			}
			if body := failed(t, r); body.Error != "bad_request" || body.Message == "" {
				t.Fatalf("the envelope was %+v, want a bad_request that says what went wrong", body)
			}
			if len(fake.calls) != 0 {
				t.Fatalf("a body that never parsed still reached the repository: %v", fake.methods())
			}
		})
	}
}

// The :id path parameter is converted to the repository's key type before
// anything else happens, so a key that cannot exist is a 400 rather than a
// lookup for the zero value.
func TestAnIDThatDoesNotParseIsRefusedBeforeTheRepository(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
	}{
		{"read", http.MethodGet, "/widgets/abc", ""},
		{"update", http.MethodPatch, "/widgets/abc", `{"name":"renamed"}`},
		{"replace", http.MethodPut, "/widgets/abc", `{"name":"replaced"}`},
		{"delete", http.MethodDelete, "/widgets/abc", ""},
		{"a number too large for the key type", http.MethodGet, "/widgets/9223372036854775808", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, fake := mount(t)

			r := do(t, app, tc.method, tc.target, tc.body)

			if r.status != http.StatusBadRequest {
				t.Fatalf("%s %s answered %d, want 400: %s", tc.method, tc.target, r.status, r.body)
			}
			body := failed(t, r)
			id := strings.TrimPrefix(strings.TrimSuffix(tc.target, "/"), "/widgets/")
			if !strings.Contains(body.Message, id) {
				t.Fatalf("the message %q never mentions the id it rejected", body.Message)
			}
			if len(fake.calls) != 0 {
				t.Fatalf("an id that does not parse still reached the repository: %v", fake.methods())
			}
		})
	}
}

// A query naming something the model does not have is a client mistake, so it
// answers 400 — and it says which part of the request was wrong, because "bad
// request" alone leaves the caller guessing. It is not a 500: nothing broke.
func TestAQueryThatNamesSomethingTheModelLacksIsABadRequest(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
		path, message              string
	}{
		{"an unknown filter field", http.MethodGet, "/widgets?f=nope:eq:1", "",
			"filter", `unknown field "nope" on model Widget`},
		{"an unknown sort field", http.MethodGet, "/widgets?sort=nope", "",
			"sort", `unknown field "nope" on model Widget`},
		{"an unknown projection field", http.MethodGet, "/widgets?select=nope", "",
			"select", `unknown field "nope" on model Widget`},
		{"an unknown relation", http.MethodGet, "/widgets?preload=nope", "",
			"preload", `unknown field "nope" on model Widget`},
		{"an unknown filter field in a document", http.MethodPost, "/widgets/query", `{"filter":{"nope":1}}`,
			"filter.nope", `unknown field "nope" on model Widget`},
		{"a page number that is not a number", http.MethodGet, "/widgets?limit=abc", "",
			"limit", `"abc" is not a number`},
		{"an operator that does not exist", http.MethodGet, "/widgets?f=price:bogus:1", "",
			"filter.Price", `unknown operator "bogus"`},
		{"a value the column cannot hold", http.MethodGet, "/widgets?f=price:gte:abc", "",
			"filter.Price", `"abc" is not a valid int`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, fake := mount(t)

			r := do(t, app, tc.method, tc.target, tc.body)

			if r.status != http.StatusBadRequest {
				t.Fatalf("%s answered %d, want 400: %s", tc.target, r.status, r.body)
			}
			body := failed(t, r)
			if body.Error != "bad_request" || body.Path != tc.path || body.Message != tc.message {
				t.Fatalf("the envelope was %+v, want bad_request at %q saying %q", body, tc.path, tc.message)
			}
			if len(fake.calls) != 0 {
				t.Fatalf("a request that never compiled still reached the repository: %v", fake.methods())
			}
		})
	}
}

// A bulk delete with nothing to delete is not an error, and it is the one shape
// that must never be read as "delete everything". The empty list is covered by
// TestBulkDeleteWithNoIDsNeverReachesTheRepository; these are the two spellings
// a client reaches for instead.
func TestABulkDeleteWithNoIDsAtAllIsAnEmptySuccess(t *testing.T) {
	for _, body := range []string{`{}`, `{"ids":null}`} {
		t.Run(body, func(t *testing.T) {
			app, fake := mount(t)

			r := ok(t, app, http.MethodPost, "/widgets/bulk-delete", body, http.StatusOK)

			var out struct {
				Deleted int64 `json:"deleted"`
			}
			r.decode(t, &out)
			if out.Deleted != 0 {
				t.Fatalf("deleted = %d, want 0", out.Deleted)
			}
			if len(fake.calls) != 0 {
				t.Fatalf("a bulk delete with no ids called %v", fake.methods())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// errors on the way back

// The repository speaks in sentinels and the transport speaks in status codes;
// this is the whole of the translation. Matching is by errors.Is, so a
// repository that adds context to a sentinel still maps to the same code.
func TestRepositoryErrorsBecomeStatusCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		tag    string
	}{
		{"a missing row", crud.ErrNotFound, http.StatusNotFound, "not_found"},
		{"an access decision", crud.ErrForbidden, http.StatusForbidden, "forbidden"},
		{"a collision", crud.ErrConflict, http.StatusConflict, "conflict"},
		{"a save with no key", crud.ErrMissingID, http.StatusBadRequest, "bad_request"},
		{"a field the model lacks", &crud.UnknownFieldError{Model: "Widget", Field: "nope"},
			http.StatusBadRequest, "bad_request"},
		{"a sentinel with context wrapped around it", fmt.Errorf("loading widget 42: %w", crud.ErrNotFound),
			http.StatusNotFound, "not_found"},
		{"anything else", errors.New("the disk is on fire"), http.StatusInternalServerError, "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, fake := mount(t)
			fake.err = tc.err

			r := do(t, app, http.MethodGet, "/widgets/42", "")

			if r.status != tc.status {
				t.Fatalf("%v answered %d, want %d: %s", tc.err, r.status, tc.status, r.body)
			}
			if got := failed(t, r).Error; got != tc.tag {
				t.Fatalf("the envelope names the error %q, want %q", got, tc.tag)
			}
		})
	}
}

// Every route funnels its failures through the same handler, so a policy that
// refuses a request is a 403 whichever door the request came in by.
func TestEveryRouteMapsARefusalTheSameWay(t *testing.T) {
	for _, tc := range []struct{ name, method, target, body string }{
		{"list", http.MethodGet, "/widgets", ""},
		{"query", http.MethodPost, "/widgets/query", `{"limit":5}`},
		{"count", http.MethodGet, "/widgets/count", ""},
		{"one entity", http.MethodGet, "/widgets/42", ""},
		{"create", http.MethodPost, "/widgets", `{"name":"bolt"}`},
		{"update", http.MethodPatch, "/widgets/42", `{"name":"renamed"}`},
		{"replace", http.MethodPut, "/widgets/42", `{"name":"replaced"}`},
		{"delete", http.MethodDelete, "/widgets/42", ""},
		{"bulk delete", http.MethodPost, "/widgets/bulk-delete", `{"ids":[1]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, fake := mount(t)
			fake.err = crud.ErrForbidden

			r := do(t, app, tc.method, tc.target, tc.body)

			if r.status != http.StatusForbidden {
				t.Fatalf("%s %s answered %d, want 403: %s", tc.method, tc.target, r.status, r.body)
			}
			if got := failed(t, r).Error; got != "forbidden" {
				t.Fatalf("the envelope names the error %q, want forbidden", got)
			}
		})
	}
}

// A 500 is the one status where the error came from inside the house: the
// message could be a driver's connection string or a fragment of SQL, so the
// body says nothing but "something broke here".
func TestA500NeverEchoesTheInternalError(t *testing.T) {
	secret := errors.New(`pq: password authentication failed for user "reporting" (host=10.0.0.5 db=prod)`)
	leaks := []string{"pq:", "password", "reporting", "10.0.0.5", "prod"}

	for _, tc := range []struct{ name, method, target, body string }{
		{"list", http.MethodGet, "/widgets", ""},
		{"one entity", http.MethodGet, "/widgets/42", ""},
		{"count", http.MethodGet, "/widgets/count", ""},
		{"create", http.MethodPost, "/widgets", `{"name":"bolt"}`},
		{"update", http.MethodPatch, "/widgets/42", `{"name":"renamed"}`},
		{"delete", http.MethodDelete, "/widgets/42", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, fake := mount(t)
			fake.err = fmt.Errorf("querying widgets: %w", secret)

			r := do(t, app, tc.method, tc.target, tc.body)

			if r.status != http.StatusInternalServerError {
				t.Fatalf("%s %s answered %d, want 500: %s", tc.method, tc.target, r.status, r.body)
			}
			if got := string(r.body); got != `{"error":"internal_error"}` {
				t.Fatalf("a 500 answered %s, want nothing but the status", got)
			}
			for _, fragment := range leaks {
				if strings.Contains(string(r.body), fragment) {
					t.Fatalf("the response leaks %q from the internal error: %s", fragment, r.body)
				}
			}
		})
	}
}

// Status is exported for handlers that render their own bodies, so the mapping
// has to hold on its own — including the branches no route reaches with a real
// repository behind it.
func TestStatusMapsWhatItPromisesTo(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"no error at all", nil, http.StatusOK},
		{"a rejected query document", &query.Error{Path: "filter", Reason: "unknown field"}, http.StatusBadRequest},
		{"a declaration that does not hold together", &crud.SchemaError{Model: "Widget", Reason: "no primary key"},
			http.StatusBadRequest},
		{"an error nobody recognises", errors.New("boom"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Status(tc.err); got != tc.want {
				t.Fatalf("Status(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// deleting nothing

// deletesNothing is the repository of a row that somebody else already removed:
// the statement runs, and it matches no rows.
type deletesNothing struct{ *fakeRepo }

func (d deletesNothing) Delete(ctx context.Context, ids ...int64) (int64, error) {
	_, err := d.fakeRepo.Delete(ctx, ids...)
	return 0, err
}

// DELETE /:id names one row, so removing none of them means that row was not
// there: 404. A bulk delete names a set, and an empty result is a truthful
// answer about a set — it reports zero and succeeds.
func TestDeletingNothingIs404ForOneRowAndZeroForASet(t *testing.T) {
	newApp := func(t *testing.T) (*gin.Engine, *fakeRepo) {
		t.Helper()
		fake := newFake()
		app := gin.New()
		New[Widget, int64, WidgetUpdate](deletesNothing{fake}).Mount(app, "/widgets")
		return app, fake
	}

	t.Run("one row", func(t *testing.T) {
		app, fake := newApp(t)

		r := do(t, app, http.MethodDelete, "/widgets/42", "")

		if r.status != http.StatusNotFound {
			t.Fatalf("deleting a row that was not there answered %d, want 404: %s", r.status, r.body)
		}
		if got := failed(t, r).Error; got != "not_found" {
			t.Fatalf("the envelope names the error %q, want not_found", got)
		}
		if ids := fake.only(t, "Delete").IDs; len(ids) != 1 || ids[0] != 42 {
			t.Fatalf("the repository was asked to delete %v, want [42]", ids)
		}
	})

	t.Run("a set", func(t *testing.T) {
		app, _ := newApp(t)

		r := ok(t, app, http.MethodPost, "/widgets/bulk-delete", `{"ids":[1,2]}`, http.StatusOK)

		var out struct {
			Deleted int64 `json:"deleted"`
		}
		r.decode(t, &out)
		if out.Deleted != 0 {
			t.Fatalf("deleted = %d, want the 0 the repository reported", out.Deleted)
		}
	})
}

// ---------------------------------------------------------------------------
// a scope that fails

// A scope runs per request, so it can fail for two very different reasons, and
// the difference is visible to the client: a refusal is the caller's answer, an
// outage is not.
func TestAScopeThatFailsIsMappedLikeAnyOtherError(t *testing.T) {
	t.Run("a refusal is a 403", func(t *testing.T) {
		app, fake := mount(t, WithScope[Widget, int64, WidgetUpdate](func(*gin.Context) ([]crud.Option, error) {
			return nil, crud.ErrForbidden
		}))

		r := do(t, app, http.MethodGet, "/widgets", "")

		if r.status != http.StatusForbidden {
			t.Fatalf("a refused scope answered %d, want 403: %s", r.status, r.body)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("a request whose scope refused it still reached the repository: %v", fake.methods())
		}
	})

	t.Run("an outage is a silent 500", func(t *testing.T) {
		app, fake := mount(t, WithScope[Widget, int64, WidgetUpdate](func(*gin.Context) ([]crud.Option, error) {
			return nil, errors.New("redis: dial tcp 10.0.0.5:6379: connection refused")
		}))

		r := do(t, app, http.MethodGet, "/widgets", "")

		if r.status != http.StatusInternalServerError {
			t.Fatalf("a scope that could not run answered %d, want 500: %s", r.status, r.body)
		}
		if got := string(r.body); got != `{"error":"internal_error"}` {
			t.Fatalf("a 500 answered %s, want nothing but the status", got)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("a request whose scope never resolved still reached the repository: %v", fake.methods())
		}
	})
}
