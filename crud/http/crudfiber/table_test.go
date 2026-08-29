package crudfiber

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud/http/crudhttp"
)

// The table is what everything outside this package reads to learn the shape of
// a CRUD resource — an access declaration most of all. A table that names a
// route Register does not mount produces a declaration guarding nothing, and the
// boot gate that compares the two would then be comparing one wrong list against
// another. So the two are checked against each other here, by asking the router.

// exercise is a request that reaches each named route. The bodies are the
// smallest ones that get past binding; what the handler answers is not the
// point, only that something other than the router answered.
func exercise(route crudhttp.Route) (target, body string) {
	target = strings.ReplaceAll(route.Path, ":id", "1")
	switch route.Name {
	case crudhttp.Create:
		body = `{"name":"x"}`
	case crudhttp.BulkDelete:
		body = `{"ids":[1]}`
	case crudhttp.Query:
		body = `{"limit":5}`
	case crudhttp.CountQuery:
		body = `{}`
	case crudhttp.Update:
		body = `{"name":"y"}`
	case crudhttp.Replace:
		body = `{"name":"y","price":1}`
	}
	return target, body
}

func reachedAHandler(status int) bool {
	return status != http.StatusNotFound && status != http.StatusMethodNotAllowed
}

func TestEveryRouteInTheTableIsMounted(t *testing.T) {
	app, _ := mount(t)

	for _, route := range (crudhttp.Table{Prefix: "/widgets"}).Routes() {
		target, body := exercise(route)
		if r := do(t, app, route.Method, target, body); !reachedAHandler(r.status) {
			t.Fatalf("the table names %s %s and Register does not mount it (%d): a declaration built from the table would guard a route that is not there",
				route.Method, route.Path, r.status)
		}
	}

	// The control. The router really does answer 404 for a path nobody
	// registered, so the loop above is finding routes rather than passing
	// because every request reaches something.
	if r := do(t, app, http.MethodGet, "/widgets/1/nowhere", ""); reachedAHandler(r.status) {
		t.Fatalf("a path nobody registered answered %d, so this test cannot tell a mounted route from an unmounted one", r.status)
	}
}

func TestAReadOnlyResourceMountsNothingTheTableOmits(t *testing.T) {
	app, _ := mount(t, ReadOnly[Widget, int64, WidgetUpdate]())

	var kept []string
	for _, route := range (crudhttp.Table{Prefix: "/widgets", ReadOnly: true}).Routes() {
		kept = append(kept, route.Name)
	}

	for _, route := range (crudhttp.Table{Prefix: "/widgets"}).Routes() {
		if slices.Contains(kept, route.Name) {
			continue
		}
		target, body := exercise(route)
		if r := do(t, app, route.Method, target, body); reachedAHandler(r.status) {
			t.Fatalf("a read-only resource answered %s %s with %d: the table leaves it out, so nothing declares it and the gate never sees it",
				route.Method, route.Path, r.status)
		}
	}

	// The control. The reads a read-only table does keep are still mounted, so
	// the loop above is not passing on a router that answers nothing at all.
	if r := do(t, app, http.MethodGet, "/widgets", ""); !reachedAHandler(r.status) {
		t.Fatalf("a read-only resource stopped serving its reads (%d), so the refusals above prove nothing", r.status)
	}
}
