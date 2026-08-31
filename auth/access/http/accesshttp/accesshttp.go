package accesshttp

import (
	"github.com/frostgrove/vv/auth/access"
)

type Table struct {
	Prefix string
}

func For(mounted *access.MountedSubject) Table {
	return Table{Prefix: mounted.Prefix()}
}

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

type Route struct {
	Method string
	Path   string
	Name   string

	Anonymous bool
}

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

func (this Table) RefreshRoute() Route {
	return Route{"POST", this.Path("/refresh"), Refresh, true}
}

func (this Table) RegisterRoute() Route {
	return Route{"POST", this.Path("/register"), Register, true}
}
