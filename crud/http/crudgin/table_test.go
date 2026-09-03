package crudgin

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud/decorators/security"
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

var _ crudhttp.Policy = security.Policy[Widget, int64]{}

func TestTheAccessDeclarationIsDerivedFromTheGatesOwnPermissions(t *testing.T) {
	policy := security.PerAction[Widget, int64](map[security.Action]auth.Permission{
		security.Read:   "widget:read",
		security.Create: "widget:create",
		security.Update: "widget:write",
		security.Delete: "widget:remove",
	})

	declared, err := (crudhttp.Table{Prefix: "/widgets"}).GuardedBy(policy)
	if err != nil {
		t.Fatalf("a policy that names every action declared nothing: %v", err)
	}

	want := map[string]auth.Permission{
		"POST /widgets":             "widget:create",
		"POST /widgets/bulk-delete": "widget:remove",
		"POST /widgets/query":       "widget:read",
		"GET /widgets/count":        "widget:read",
		"POST /widgets/count":       "widget:read",
		"GET /widgets":              "widget:read",
		"GET /widgets/:id":          "widget:read",
		"PATCH /widgets/:id":        "widget:write",
		"PUT /widgets/:id":          "widget:write",
		"DELETE /widgets/:id":       "widget:remove",
	}
	if len(declared) != len(want) {
		t.Fatalf("the table mounts %d routes and the derived declaration covers %d", len(want), len(declared))
	}
	for _, endpoint := range declared {
		route := endpoint.Method + " " + endpoint.Path
		expected, mounted := want[route]
		if !mounted {
			t.Fatalf("the declaration carries %s, which the table does not mount", route)
		}
		if len(endpoint.Needs) != 1 || endpoint.Needs[0] != expected {
			t.Fatalf("%s is declared as needing %v, and the gate that answers it asks for %s",
				route, endpoint.Needs, expected)
		}
	}
}

func TestAMountedRouteThePolicyLeavesUndeclaredIsRefusedAtAssembly(t *testing.T) {
	readsOnly := security.PerAction[Widget, int64](map[security.Action]auth.Permission{
		security.Read: "widget:read",
	})

	_, err := (crudhttp.Table{Prefix: "/widgets"}).GuardedBy(readsOnly)
	if err == nil {
		t.Fatal("a policy that refuses every write produced a declaration for the write routes, which would name a permission nothing enforces")
	}
	if !strings.Contains(err.Error(), "POST /widgets") {
		t.Fatalf("the refusal does not say which route is undeclared: %v", err)
	}

	declared, err := (crudhttp.Table{Prefix: "/widgets", ReadOnly: true}).GuardedBy(readsOnly)
	if err != nil {
		t.Fatalf("the same policy over a read-only resource declares every route it mounts, and was refused: %v", err)
	}
	if len(declared) != 5 {
		t.Fatalf("a read-only resource mounts five routes and %d were declared", len(declared))
	}
}

func TestTheThreePermissionShorthandCollapsesEveryWriteOntoOnePermission(t *testing.T) {
	declared := (crudhttp.Table{Prefix: "/widgets"}).Guarded("widget:read", "widget:write", "widget:remove")

	want := map[string]auth.Permission{
		"POST /widgets":             "widget:write",
		"POST /widgets/bulk-delete": "widget:remove",
		"POST /widgets/query":       "widget:read",
		"GET /widgets/count":        "widget:read",
		"POST /widgets/count":       "widget:read",
		"GET /widgets":              "widget:read",
		"GET /widgets/:id":          "widget:read",
		"PATCH /widgets/:id":        "widget:write",
		"PUT /widgets/:id":          "widget:write",
		"DELETE /widgets/:id":       "widget:remove",
	}
	if len(declared) != len(want) {
		t.Fatalf("the table mounts %d routes and the shorthand declared %d", len(want), len(declared))
	}
	for _, endpoint := range declared {
		route := endpoint.Method + " " + endpoint.Path
		expected, mounted := want[route]
		if !mounted {
			t.Fatalf("the declaration carries %s, which the table does not mount", route)
		}
		if len(endpoint.Needs) != 1 || endpoint.Needs[0] != expected {
			t.Fatalf("%s is declared as needing %v, and the shorthand was given %s for it",
				route, endpoint.Needs, expected)
		}
	}
}
