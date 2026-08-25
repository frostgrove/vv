package auth

import "strings"

// A Role is a named bundle of permissions. It is what an identity provider
// usually puts in a token.
type Role string

// A Permission is one thing a caller may do. It is the unit an authorization
// rule should be written against: a rule that names a role has to be edited
// when the roles change, and a rule that names a permission does not.
type Permission string

// A Principal is one authenticated caller.
//
// Four methods, all of them answers rather than lists, because the gate asks
// them once per operation and a slice returned per call is an allocation per
// request for nothing. Enumeration is [Claims]'s, not the interface's.
//
// Implement it over your own identity type when you have one. [Claims] is here
// for when you do not.
type Principal interface {
	// Subject is the stable identifier of the caller — a user id, a service
	// account name. It is what an audit trail records and what ScopeSubject
	// narrows rows by.
	Subject() string

	// In reports membership. Roles are expanded to permissions when the
	// principal is built, so this is for the rules that genuinely are about a
	// role rather than about what it grants.
	In(r Role) bool

	// Has reports one permission, roles already expanded.
	Has(p Permission) bool

	// Attr is everything else the provider knew: a tenant, an organisation, an
	// email. The second result separates absent from present-and-nil, which is
	// the same distinction crud.Opt draws for a column ([[D-002]]).
	Attr(name string) (any, bool)
}

// Claims is the ready-made [Principal].
//
// The slices are scanned linearly rather than indexed into a set. A principal
// carries a handful of roles and rarely more than a few dozen permissions, and
// building two maps per request to avoid scanning ten strings costs more than
// it saves. If yours is genuinely large, implement [Principal] over a type that
// holds the map.
type Claims struct {
	Sub         string
	Roles       []Role
	Permissions []Permission
	Attrs       map[string]any
}

// Subject implements [Principal].
func (c Claims) Subject() string { return c.Sub }

// In implements [Principal].
func (c Claims) In(r Role) bool {
	for _, has := range c.Roles {
		if has == r {
			return true
		}
	}
	return false
}

// Has implements [Principal].
func (c Claims) Has(p Permission) bool {
	for _, has := range c.Permissions {
		if has == p {
			return true
		}
	}
	return false
}

// Attr implements [Principal].
func (c Claims) Attr(name string) (any, bool) {
	v, ok := c.Attrs[name]
	return v, ok
}

// Grant returns a copy with the role map's permissions folded in, and is what a
// provider calls once it knows the roles.
//
// Expanding here rather than in [Claims.Has] is deliberate: a principal that
// answered Has by walking a role map would answer differently depending on
// which map happened to be reachable at the call site, and the same token would
// mean two things in one process.
func (c Claims) Grant(m RoleMap) Claims {
	if len(m) == 0 || len(c.Roles) == 0 {
		return c
	}
	out := c
	out.Permissions = append(append([]Permission(nil), c.Permissions...), m.Expand(c.Roles...)...)
	out.Permissions = dedupe(out.Permissions)
	return out
}

// A RoleMap expands roles into permissions. It is a value the application
// wires, never a registry this package holds — see the refusals in the package
// documentation.
type RoleMap map[Role][]Permission

// Expand returns every permission the roles grant, without duplicates.
func (m RoleMap) Expand(roles ...Role) []Permission {
	if len(m) == 0 {
		return nil
	}
	var out []Permission
	for _, r := range roles {
		out = append(out, m[r]...)
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

// HasAll reports whether the principal holds every permission. No permissions
// is true, which is what makes a rule that names none a rule that refuses
// nothing.
func HasAll(p Principal, ps ...Permission) bool {
	if p == nil {
		return false
	}
	for _, want := range ps {
		if !p.Has(want) {
			return false
		}
	}
	return true
}

// HasAny reports whether the principal holds at least one. No permissions is
// false: "any of nothing" is not satisfiable, and returning true here would
// turn an empty rule into a licence rather than a no-op. HasAll draws the line
// the other way for the same reason — both answers are the safe one for their
// own quantifier.
func HasAny(p Principal, ps ...Permission) bool {
	if p == nil {
		return false
	}
	for _, want := range ps {
		if p.Has(want) {
			return true
		}
	}
	return false
}

// InAny reports membership of at least one role, and is false for no roles for
// the same reason [HasAny] is.
func InAny(p Principal, rs ...Role) bool {
	if p == nil {
		return false
	}
	for _, want := range rs {
		if p.In(want) {
			return true
		}
	}
	return false
}

// Scopes splits an OAuth 2.0 scope claim — one string, space-separated — into
// permissions. Providers spell it that way and a caller should not have to.
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
