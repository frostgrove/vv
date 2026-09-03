package crudhttp

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/port"
)

type Table struct {
	Prefix string

	ReadOnly bool

	// Expose mirrors port.Rules.Expose, so a declaration is derived from the
	// same set the resource is mounted with. Empty means every route.
	Expose port.Operations
}

const (
	List       = "list"
	Get        = "get"
	Count      = "count"
	CountQuery = "count-query"
	Query      = "query"
	Create     = "create"
	Update     = "update"
	Replace    = "replace"
	Delete     = "delete"
	BulkDelete = "bulk-delete"
)

type Route struct {
	Method string
	Path   string
	Name   string
	Action crud.Action
}

type Policy interface {
	RequiredFor(action crud.Action) ([]auth.Permission, bool)
}

// The order is the order the bindings register in, and it is load-bearing on
// the two routers that match by insertion: "/count" has to be seen before
// "/:id", or a count reaches GetByID with the literal as an id.
var tableRoutes = []struct {
	operation port.Operations
	method    string
	suffix    string
	name      string
	action    crud.Action
}{
	{port.OpCreate, http.MethodPost, "", Create, crud.ActionCreate},
	{port.OpBulkDelete, http.MethodPost, "/bulk-delete", BulkDelete, crud.ActionDelete},
	{port.OpQuery, http.MethodPost, "/query", Query, crud.ActionRead},
	{port.OpCount, http.MethodGet, "/count", Count, crud.ActionRead},
	{port.OpCountQuery, http.MethodPost, "/count", CountQuery, crud.ActionRead},
	{port.OpList, http.MethodGet, "", List, crud.ActionRead},
	{port.OpGet, http.MethodGet, "/:id", Get, crud.ActionRead},
	{port.OpUpdate, http.MethodPatch, "/:id", Update, crud.ActionUpdate},
	{port.OpReplace, http.MethodPut, "/:id", Replace, crud.ActionUpdate},
	{port.OpDelete, http.MethodDelete, "/:id", Delete, crud.ActionDelete},
}

func (this Table) Routes() []Route {
	prefix := strings.TrimSuffix(this.Prefix, "/")
	rules := port.Rules{ReadOnly: this.ReadOnly, Expose: this.Expose}
	mounted := rules.Mounted()
	routes := make([]Route, 0, len(tableRoutes))
	for _, candidate := range tableRoutes {
		if !mounted.Has(candidate.operation) {
			continue
		}
		routes = append(routes, Route{candidate.method, prefix + candidate.suffix, candidate.name, candidate.action})
	}
	return routes
}

func (this Table) Guarded(read, write, del auth.Permission) []authhttp.Endpoint {
	endpoints, err := this.GuardedBy(threePermissions{read: read, write: write, remove: del})
	if err != nil {
		panic("crudhttp: " + err.Error())
	}
	return endpoints
}

func (this Table) GuardedBy(policy Policy) ([]authhttp.Endpoint, error) {
	if policy == nil {
		return nil, fmt.Errorf("crudhttp: GuardedBy needs the policy the repository is gated with; without one nothing derives a declaration")
	}
	routes := this.Routes()
	out := make([]authhttp.Endpoint, 0, len(routes))
	var undeclared []string
	for _, route := range routes {
		permissions, declared := policy.RequiredFor(route.Action)
		switch {
		case !declared:
			undeclared = append(undeclared, route.Method+" "+route.Path+" ("+route.Action.String()+")")
		case len(permissions) == 0:
			out = append(out, authhttp.Authenticated(route.Method, route.Path,
				"the policy that gates this resource asks for no permission on "+route.Action.String()))
		default:
			out = append(out, authhttp.Requires(route.Method, route.Path, permissions...))
		}
	}
	if len(undeclared) > 0 {
		return nil, fmt.Errorf("crudhttp: the policy declares no permission for %s, and %s mounts those routes: a declaration derived from it would leave them unguarded",
			strings.Join(undeclared, ", "), resourceName(this.Prefix))
	}
	return out, nil
}

func resourceName(prefix string) string {
	if prefix == "" {
		return "the table"
	}
	return prefix
}

type threePermissions struct {
	read   auth.Permission
	write  auth.Permission
	remove auth.Permission
}

func (this threePermissions) RequiredFor(action crud.Action) ([]auth.Permission, bool) {
	switch action {
	case crud.ActionCreate, crud.ActionUpdate:
		return []auth.Permission{this.write}, true
	case crud.ActionDelete:
		return []auth.Permission{this.remove}, true
	default:
		return []auth.Permission{this.read}, true
	}
}
