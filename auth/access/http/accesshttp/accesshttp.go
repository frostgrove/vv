// Package accesshttp is what is HTTP *and* access: the request shapes, the
// route table every binding mounts, and the step between a decoded body and a
// use case.
//
// It exists so that `accessnet`, `accessgin` and `accessfiber` are decode, call
// and write, and nothing else. Three transports each carrying their own copy of
// "which path, and what happens when the subject type does not match" is three
// copies that disagree the first time one is fixed.
//
// The behaviour is access.Endpoints, built by access.Mount. What is here is
// only what is HTTP: the paths, and the names a binding switches on.
package accesshttp

import (
	"github.com/frostgrove/vv/auth/access"
)

// A Table is where one subject's endpoints live. A binding builds one from a
// mounted subject and mounts what it lists.
type Table struct {
	// Prefix goes in front of /auth. Empty mounts at /auth/login, which is what
	// a single-subject application wants; a second subject passes its own so
	// the two surfaces do not collide.
	Prefix string
}

// For builds the table for a mounted subject.
func For(mounted *access.MountedSubject) Table {
	return Table{Prefix: mounted.Prefix()}
}

// Path is where one of this subject's endpoints lives.
//
//	Path("/login") == "/auth/login"          // Prefix ""
//	Path("/login") == "/staff/auth/login"    // Prefix "staff"
func (this Table) Path(suffix string) string {
	if this.Prefix == "" {
		return "/auth" + suffix
	}
	return "/" + trimSlashes(this.Prefix) + "/auth" + suffix
}

func trimSlashes(prefix string) string {
	start, end := 0, len(prefix)
	for start < end && prefix[start] == '/' {
		start++
	}
	for end > start && prefix[end-1] == '/' {
		end--
	}
	return prefix[start:end]
}

// A Route is one endpoint, named so every binding mounts the same set in the
// same shape. Name is what a binding switches on; nothing parses Path.
type Route struct {
	Method string
	Path   string
	Name   string
	// Anonymous marks the two endpoints a caller with no session may reach.
	// Everything else needs a principal, and the handler asks for one.
	Anonymous bool
}

// Endpoint names. A binding maps each to one of its own handlers, and
// `make check-triplets` is what holds that the three agree.
const (
	Register     = "register"
	SignIn       = "login"
	SignOut      = "logout"
	SignOutAll   = "logout-all"
	ChangeSecret = "password"
	WhoAmI       = "me"
	ListSessions = "sessions"
	KillSession  = "session"
	Refresh      = "refresh"
)

// Routes is the endpoint set every subject has, in order.
//
// Registering is not among them — see [Table.RegisterRoute] — because it is the
// one endpoint a deployment may not have and the one whose payload is the
// application's.
func (this Table) Routes() []Route {
	return []Route{
		{"POST", this.Path("/login"), SignIn, true},
		{"POST", this.Path("/logout"), SignOut, false},
		{"POST", this.Path("/logout-all"), SignOutAll, false},
		{"POST", this.Path("/password"), ChangeSecret, false},
		{"GET", this.Path("/me"), WhoAmI, false},
		{"GET", this.Path("/sessions"), ListSessions, false},
		{"DELETE", this.Path("/sessions/:id"), KillSession, false},
	}
}

// RefreshRoute is the rotation endpoint, for a strategy that rotates.
//
// Anonymous, like sign-in: the reason to call it is that the access credential
// is gone, so requiring one would make it useless exactly when it is needed.
// What authenticates the call is the rotating credential in the body.
func (this Table) RefreshRoute() Route {
	return Route{"POST", this.Path("/refresh"), Refresh, true}
}

// RegisterRoute is the sign-up endpoint, for a subject that has one.
//
// A binding mounts it only when it was handed a sign-up use case. A route that
// exists and always refuses would tell a stranger that this deployment has a
// sign-up and that they may not use it; a path nothing serves answers 404,
// which is the honest shape of "there is no sign-up here".
func (this Table) RegisterRoute() Route {
	return Route{"POST", this.Path("/register"), Register, true}
}
