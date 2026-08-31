package crudgin

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud/http/crudhttp"
)

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

	if r := do(t, app, http.MethodGet, "/widgets", ""); !reachedAHandler(r.status) {
		t.Fatalf("a read-only resource stopped serving its reads (%d), so the refusals above prove nothing", r.status)
	}
}
