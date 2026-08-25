package crudnet

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/port"
	"github.com/shardit-io/vv/query"
)

// wireViolation is one entry of the envelope exactly as a client reads it.
//
// The tests decode the wire shape rather than errs.Violation, and that is not a
// stylistic choice: the Go type marshals into this and has no UnmarshalJSON, so
// decoding a response into it would answer the zero value for every field and
// every assertion below would pass against an empty body.
type wireViolation struct {
	Field   []any  `json:"field"`
	Code    string `json:"error_code"`
	Message string `json:"message"`
}

// path renders the field array the dotted way, so a test can say
// "filter.Price" instead of building a slice.
func (v wireViolation) path() string {
	var b strings.Builder
	for i, step := range v.Field {
		if n, ok := step.(float64); ok {
			fmt.Fprintf(&b, "[%d]", int(n))
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		fmt.Fprintf(&b, "%v", step)
	}
	return b.String()
}

type wireEnvelope struct {
	Type    string `json:"type"`
	Partial bool   `json:"partial"`
	Errors  struct {
		Validation []wireViolation `json:"validation"`
		General    []wireViolation `json:"general"`
	} `json:"errors"`
}

// envelope decodes the body every failing request answers with. A body that is
// not that envelope is itself the bug: a client cannot branch on a stack trace.
func envelope(t *testing.T, r response) wireEnvelope {
	t.Helper()
	var env wireEnvelope
	r.decode(t, &env)
	if env.Type != "error" {
		t.Fatalf("a %d answered a body that is not the error envelope: %s", r.status, r.body)
	}
	return env
}

// failed is the single violation almost every test here is about.
func failed(t *testing.T, r response) wireViolation {
	t.Helper()
	env := envelope(t, r)
	vs := append(append([]wireViolation{}, env.Errors.Validation...), env.Errors.General...)
	if len(vs) == 0 {
		t.Fatalf("a %d answered without naming the error: %s", r.status, r.body)
	}
	if vs[0].Code == "" {
		t.Fatalf("a %d answered without an error_code: %s", r.status, r.body)
	}
	return vs[0]
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
			if body := failed(t, r); body.Code != "malformed_body" || body.Message == "" {
				t.Fatalf("the envelope was %+v, want a malformed_body that says what went wrong", body)
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
			if body.Code != "bad_query" || body.path() != tc.path || body.Message != tc.message {
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
		{"a collision nothing finer was learned about", crud.ErrConflict, http.StatusConflict, "conflict"},
		{"a stale write", crud.ErrStaleVersion, http.StatusConflict, "stale_version"},
		{"a save with no key", crud.ErrMissingID, http.StatusBadRequest, "invalid_id"},
		{"a field the model lacks", &crud.UnknownFieldError{Model: "Widget", Field: "nope"},
			http.StatusBadRequest, "unknown_field"},
		{"a sentinel with context wrapped around it", fmt.Errorf("loading widget 42: %w", crud.ErrNotFound),
			http.StatusNotFound, "not_found"},
		{"anything else", errors.New("the disk is on fire"), http.StatusInternalServerError, "internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, fake := mount(t)
			fake.err = tc.err

			r := do(t, app, http.MethodGet, "/widgets/42", "")

			if r.status != tc.status {
				t.Fatalf("%v answered %d, want %d: %s", tc.err, r.status, tc.status, r.body)
			}
			if got := failed(t, r).Code; got != tc.tag {
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
			if got := failed(t, r).Code; got != "forbidden" {
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

	// The same secret, arriving through a fault instead of a bare error: a
	// classified failure carries Detail and Params, and those are the two
	// channels a renderer could copy into a body without ever touching
	// err.Error(). [[D-044]] owed this extension to phase 4.
	rich := errs.Internal().Op("Save").Entity("Widget").Code(errs.CodeInternal).
		Message(secret.Error()).
		Field("Name").Code(errs.CodeInternal).Message(secret.Error()).
		Params(errs.P{"host": "10.0.0.5", "user": "reporting"}).
		Source(errs.Source{Table: "widgets", Schema: "prod", Constraint: "widgets_name_key", Columns: []string{"name"}}).
		Detail(errs.Detail{Dialect: "postgres", SQLState: "28P01", Constraint: "widgets_name_key", Table: "widgets", Driver: secret}).
		Wrapping(secret).Fault()

	for _, tc := range []struct{ name, method, target, body string }{
		{"list", http.MethodGet, "/widgets", ""},
		{"one entity", http.MethodGet, "/widgets/42", ""},
		{"count", http.MethodGet, "/widgets/count", ""},
		{"create", http.MethodPost, "/widgets", `{"name":"bolt"}`},
		{"update", http.MethodPatch, "/widgets/42", `{"name":"renamed"}`},
		{"delete", http.MethodDelete, "/widgets/42", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, arrival := range []struct {
				how string
				err error
			}{
				{"a bare error", fmt.Errorf("querying widgets: %w", secret)},
				{"a fault carrying Detail and Params", rich},
			} {
				app, fake := mount(t)
				fake.err = arrival.err

				r := do(t, app, tc.method, tc.target, tc.body)

				if r.status != http.StatusInternalServerError {
					t.Fatalf("%s %s with %s answered %d, want 500: %s", tc.method, tc.target, arrival.how, r.status, r.body)
				}
				if got := string(r.body); got != `{"type":"error","errors":{"general":[{"error_code":"internal"}]}}` {
					t.Fatalf("a 500 answered %s, want nothing but the status", got)
				}
				for _, fragment := range leaks {
					if strings.Contains(string(r.body), fragment) {
						t.Fatalf("the response leaks %q from the internal error: %s", fragment, r.body)
					}
				}
				_ = fake
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
		{"a classified value violation", errs.Validation().Code(errs.CodeTooLong).Fault(), http.StatusUnprocessableEntity},
		{"a lock the engine gave up on", errs.Retryable().Code(errs.CodeLockTimeout).Fault(), http.StatusServiceUnavailable},
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
	newApp := func(t *testing.T) (*http.ServeMux, *fakeRepo) {
		t.Helper()
		fake := newFake()
		app := http.NewServeMux()
		New[Widget, int64, WidgetUpdate](deletesNothing{fake}).Mount(app, "/widgets")
		return app, fake
	}

	t.Run("one row", func(t *testing.T) {
		app, fake := newApp(t)

		r := do(t, app, http.MethodDelete, "/widgets/42", "")

		if r.status != http.StatusNotFound {
			t.Fatalf("deleting a row that was not there answered %d, want 404: %s", r.status, r.body)
		}
		if got := failed(t, r).Code; got != "not_found" {
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
		app, fake := mount(t, WithScope[Widget, int64, WidgetUpdate](func(*http.Request) ([]crud.Option, error) {
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
		app, fake := mount(t, WithScope[Widget, int64, WidgetUpdate](func(*http.Request) ([]crud.Option, error) {
			return nil, errors.New("redis: dial tcp 10.0.0.5:6379: connection refused")
		}))

		r := do(t, app, http.MethodGet, "/widgets", "")

		if r.status != http.StatusInternalServerError {
			t.Fatalf("a scope that could not run answered %d, want 500: %s", r.status, r.body)
		}
		if got := string(r.body); got != `{"type":"error","errors":{"general":[{"error_code":"internal"}]}}` {
			t.Fatalf("a 500 answered %s, want nothing but the status", got)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("a request whose scope never resolved still reached the repository: %v", fake.methods())
		}
	})
}

// pathService is a Service that declares its own hop of the path chain — the
// model's field names to the ones its commands use. It is what a generated
// service will be, and the reason Serving exists.
type pathService struct {
	*port.DefaultService[Widget, int64, WidgetUpdate]
	fields port.Fields
}

func (s *pathService) Paths() errs.Resolver { return s.fields }

// The service's hop of the path chain reaches the rendered body: a violation
// the repository raised at a model field arrives as the key the client actually
// sent, because the service declared the mapping and the renderer applies it
// before the raw-body fallback ([[D-043]]).
func TestAServicePathHopReachesTheRenderedField(t *testing.T) {
	mounted := func(t *testing.T, field string) response {
		t.Helper()
		fake := newFake()
		fake.err = errs.Conflict().Code(errs.CodeUnique).
			Field(field).Code(errs.CodeUnique).Fault()
		svc := &pathService{
			DefaultService: port.NewService[Widget, int64, WidgetUpdate](fake),
			fields:         port.Fields{"Name": errs.Path{errs.Named("label")}},
		}
		app := mountHandler(ServingFor(Service[Widget, int64, WidgetUpdate](svc), widgetMapper{}))
		r := do(t, app, http.MethodPost, "/widgets", `{"label":"bolt","price":250}`)
		if r.status != http.StatusConflict {
			t.Fatalf("a duplicate key answered %d, want 409: %s", r.status, r.body)
		}
		return r
	}

	t.Run("a declared field is rewritten", func(t *testing.T) {
		if got := failed(t, mounted(t, "Name")).path(); got != "label" {
			t.Fatalf("the violation names %q, want the key the client sent", got)
		}
	})

	// The control, and the reason an undeclared head passes through instead of
	// declining: the hop behind it still runs. A declining hop would poison the
	// chain, the raw-body fallback would never see the path, and the client
	// would get the model's own "Price" back.
	t.Run("and the control: an undeclared field reaches the body index", func(t *testing.T) {
		if got := failed(t, mounted(t, "Price")).path(); got != "price" {
			t.Fatalf("the violation names %q, want the lower-case key the client sent; %q means the service hop stopped the chain", got, "Price")
		}
	})
}
