package auth

import (
	"strings"

	"github.com/frostgrove/vv/internal/nilvalue"
)

type Role string

type Permission string

type Principal interface {
	Subject() string

	In(r Role) bool

	Has(p Permission) bool

	Attr(name string) (any, bool)
}

type Claims struct {
	Sub         string
	Roles       []Role
	Permissions []Permission
	Attrs       map[string]any
}

func (this Claims) Subject() string { return this.Sub }

func (this Claims) In(r Role) bool {
	for _, has := range this.Roles {
		if has == r {
			return true
		}
	}
	return false
}

func (this Claims) Has(p Permission) bool {
	for _, has := range this.Permissions {
		if has == p {
			return true
		}
	}
	return false
}

func (this Claims) Attr(name string) (any, bool) {
	v, ok := this.Attrs[name]
	return v, ok
}

func (this Claims) Grant(m RoleMap) Claims {
	if len(m) == 0 || len(this.Roles) == 0 {
		return this
	}
	out := this
	out.Permissions = append(append([]Permission(nil), this.Permissions...), m.Expand(this.Roles...)...)
	out.Permissions = dedupe(out.Permissions)
	return out
}

type RoleMap map[Role][]Permission

func (this RoleMap) Expand(roles ...Role) []Permission {
	if len(this) == 0 {
		return nil
	}
	var out []Permission
	for _, r := range roles {
		out = append(out, this[r]...)
	}
	return dedupe(out)
}

func dedupe(ps []Permission) []Permission {
	if len(ps) < 2 {
		return ps
	}
	seen := make(map[Permission]struct{}, len(ps))
	out := ps[:0]
	for _, p := range ps {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func HasAll(p Principal, ps ...Permission) bool {
	if nilvalue.Is(p) {
		return false
	}
	for _, want := range ps {
		if !p.Has(want) {
			return false
		}
	}
	return true
}

func HasAny(p Principal, ps ...Permission) bool {
	if nilvalue.Is(p) {
		return false
	}
	for _, want := range ps {
		if p.Has(want) {
			return true
		}
	}
	return false
}

func InAny(p Principal, rs ...Role) bool {
	if nilvalue.Is(p) {
		return false
	}
	for _, want := range rs {
		if p.In(want) {
			return true
		}
	}
	return false
}

func Scopes(scope string) []Permission {
	if scope == "" {
		return nil
	}
	fields := strings.Fields(scope)
	out := make([]Permission, 0, len(fields))
	for _, f := range fields {
		out = append(out, Permission(f))
	}
	return out
}
