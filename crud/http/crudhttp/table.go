package crudhttp

import (
	"net/http"
	"strings"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
)

// A Table is where one resource's routes live.
//
// It is the same list `Register` walks, said once so that everything else which
// needs to know the shape of a CRUD resource — an access declaration, a
// generated client, a route dump — reads it from here instead of keeping a copy
// that goes stale the first time a route is added.
//
// It exists for the reason [accesshttp.Table] does: the paths are not the
// interesting part of a security declaration and they are the part that rots.
// What a reviewer has to get right is which permission guards which route, and
// that is still written by hand — see [Table.Guarded].
type Table struct {
	// Prefix is where the resource is mounted, without a trailing slash:
	// "/roles". Empty mounts at the router's own root, which is what a handler
	// registered on its own group does.
	Prefix string
	// ReadOnly leaves out the four routes a handler built with ReadOnly does not
	// register, so a POST to the collection is a 405 rather than a 403.
	ReadOnly bool
}

// Route names. A caller switches on these rather than on a method and a path,
// because the pair is spelled differently on the four transports and the name
// is not.
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

// A Need is what kind of access a route takes.
//
// Three and not one permission per route: every application that has mounted one
// of these resources so far has had exactly this split, and a table naming ten
// permissions would make the common case ten lines of the same word.
type Need uint8

const (
	// NeedRead is every route that answers with rows and changes nothing.
	NeedRead Need = iota
	NeedWrite
	// NeedDelete is separate from NeedWrite because removing a row and editing
	// one are different things to hand out — the usual shape is an editor who
	// may not delete.
	NeedDelete
)

// A Route is one of a resource's endpoints.
//
// Path parameters are spelled `:id`, which is Fiber's and Gin's spelling and the
// canonical one here. crudnet rewrites it, the way accessnet does, because it is
// the one thing the bindings' route tables disagree about.
type Route struct {
	Method string
	Path   string
	Name   string
	Need   Need
}

// Routes is what a handler registers, in the order it registers it.
//
// `/count` comes before `/:id` for the reason `Register` puts it there: a
// parameter route mounted first swallows it.
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

// Guarded is this resource's access declaration, for the boot gate in
// [authhttp.Verify].
//
// The paths come from the table and the permissions come from the caller, and
// that split is the point. A declaration written out by hand agrees with the
// router only until somebody adds a route; a declaration derived from the router
// agrees with it always, including when both are wrong. What is worth stating
// twice is which permission guards what — so that is what a consumer writes —
// and the paths are checked against the real routing table anyway.
//
// A read-only table names no write or delete permission, so passing one is
// harmless and passing an empty one is normal:
//
//	crudhttp.Table{Prefix: "/permissions", ReadOnly: true}.Guarded(read, "", "")
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
