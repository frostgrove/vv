package crudhttp

import (
	"net/http"
	"strings"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
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

type Need uint8

const (
	NeedRead Need = iota
	NeedWrite

	NeedDelete
)

type Route struct {
	Method string
	Path   string
	Name   string
	Need   Need
}

func (this Table) Routes() []Route {
	prefix := strings.TrimSuffix(this.Prefix, "/")
	routes := make([]Route, 0, 10)
	if !this.ReadOnly {
		routes = append(routes,
			Route{http.MethodPost, prefix, Create, NeedWrite},
			Route{http.MethodPost, prefix + "/bulk-delete", BulkDelete, NeedDelete},
		)
	}
	routes = append(routes,
		Route{http.MethodPost, prefix + "/query", Query, NeedRead},
		Route{http.MethodGet, prefix + "/count", Count, NeedRead},
		Route{http.MethodPost, prefix + "/count", CountQuery, NeedRead},
		Route{http.MethodGet, prefix, List, NeedRead},
		Route{http.MethodGet, prefix + "/:id", Get, NeedRead},
	)
	if !this.ReadOnly {
		routes = append(routes,
			Route{http.MethodPatch, prefix + "/:id", Update, NeedWrite},
			Route{http.MethodPut, prefix + "/:id", Replace, NeedWrite},
			Route{http.MethodDelete, prefix + "/:id", Delete, NeedDelete},
		)
	}
	return routes
}

func (this Table) Guarded(read, write, del auth.Permission) []authhttp.Endpoint {
	routes := this.Routes()
	out := make([]authhttp.Endpoint, 0, len(routes))
	for _, route := range routes {
		permission := read
		switch route.Need {
		case NeedWrite:
			permission = write
		case NeedDelete:
			permission = del
		}
		out = append(out, authhttp.Requires(route.Method, route.Path, permission))
	}
	return out
}
