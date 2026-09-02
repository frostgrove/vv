package crudhttp

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/crud"
)

type Table struct {
	Prefix string

	ReadOnly bool
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

func (this Table) Routes() []Route {
	prefix := strings.TrimSuffix(this.Prefix, "/")
	routes := make([]Route, 0, 10)
	if !this.ReadOnly {
		routes = append(routes,
			Route{http.MethodPost, prefix, Create, crud.ActionCreate},
			Route{http.MethodPost, prefix + "/bulk-delete", BulkDelete, crud.ActionDelete},
		)
	}
	routes = append(routes,
		Route{http.MethodPost, prefix + "/query", Query, crud.ActionRead},
		Route{http.MethodGet, prefix + "/count", Count, crud.ActionRead},
		Route{http.MethodPost, prefix + "/count", CountQuery, crud.ActionRead},
		Route{http.MethodGet, prefix, List, crud.ActionRead},
		Route{http.MethodGet, prefix + "/:id", Get, crud.ActionRead},
	)
	if !this.ReadOnly {
		routes = append(routes,
			Route{http.MethodPatch, prefix + "/:id", Update, crud.ActionUpdate},
			Route{http.MethodPut, prefix + "/:id", Replace, crud.ActionUpdate},
			Route{http.MethodDelete, prefix + "/:id", Delete, crud.ActionDelete},
		)
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
