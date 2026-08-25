package authjwt

import (
	"encoding/json"
	"strings"

	"github.com/shardit-io/vv/auth"
)

// Claims is the ready-made claims type, for the ordinary shape of token: a
// subject, some roles, some permissions or an OAuth scope string, and whatever
// else the issuer put in.
//
// It implements [auth.Principal], so the zero-config path is [Standard] and
// there is no mapping function to write. A token that does not look like this
// gets its own struct and [Authenticator] — that is what the parser being
// generic is for.
type Claims struct {
	// Sub is the subject. It is spelled the short way because [Claims.Subject]
	// is the method that implements [auth.Principal], and a field and a method
	// cannot share a name.
	Sub         string   `json:"sub"`
	Issuer      string   `json:"iss"`
	Roles       []string `json:"roles,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
	// Scope is the OAuth 2.0 spelling: one string, space-separated. Both it and
	// Permissions are read, because issuers disagree about which to send and a
	// consumer should not have to care which one theirs uses.
	Scope string `json:"scope,omitempty"`

	// Extra is every claim in the payload, including the ones above. It is what
	// [Claims.Attr] reads, so a tenant, an organisation or an email needs no
	// field here.
	Extra map[string]any `json:"-"`
}

// UnmarshalJSON fills the named fields and keeps the whole payload in Extra.
//
// The numbers are decoded through json.Number and then narrowed, so an integer
// claim stays an integer. Left as the float64 encoding/json would produce, a
// tenant id read out of [Claims.Attr] compiles into a float in the WHERE
// clause of every scoped query.
func (c *Claims) UnmarshalJSON(b []byte) error {
	type plain Claims
	var named plain
	if err := json.Unmarshal(b, &named); err != nil {
		return err
	}
	*c = Claims(named)

	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	raw := map[string]any{}
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	c.Extra = make(map[string]any, len(raw))
	for k, v := range raw {
		c.Extra[k] = narrow(v)
	}
	return nil
}

// narrow turns a json.Number into the Go value a caller expects, leaving
// everything else alone. An integral number is an int64 because that is what a
// key column holds; anything else stays a float64.
func narrow(v any) any {
	n, ok := v.(json.Number)
	if !ok {
		return v
	}
	if i, err := n.Int64(); err == nil {
		return i
	}
	if f, err := n.Float64(); err == nil {
		return f
	}
	return n.String()
}

// Subject implements [auth.Principal].
func (c Claims) Subject() string { return c.Sub }

// In implements [auth.Principal].
func (c Claims) In(r auth.Role) bool {
	for _, has := range c.Roles {
		if auth.Role(has) == r {
			return true
		}
	}
	return false
}

// Has implements [auth.Principal], reading both spellings of a permission.
func (c Claims) Has(p auth.Permission) bool {
	for _, has := range c.Permissions {
		if auth.Permission(has) == p {
			return true
		}
	}
	for _, has := range auth.Scopes(c.Scope) {
		if has == p {
			return true
		}
	}
	return false
}

// Attr implements [auth.Principal] over the whole payload.
func (c Claims) Attr(name string) (any, bool) {
	v, ok := c.Extra[name]
	return v, ok
}

// Grant is the neutral copy, with a role map expanded into permissions.
//
// It exists because roles are the thing an identity provider puts in a token
// and permissions are the thing an authorization rule should name. Expanding
// once, here, is what keeps the same token from meaning two things in one
// process ([[D-055]]).
func (c Claims) Grant(m auth.RoleMap) auth.Claims {
	roles := make([]auth.Role, 0, len(c.Roles))
	for _, r := range c.Roles {
		roles = append(roles, auth.Role(r))
	}
	perms := make([]auth.Permission, 0, len(c.Permissions))
	for _, p := range c.Permissions {
		perms = append(perms, auth.Permission(p))
	}
	perms = append(perms, auth.Scopes(c.Scope)...)

	return auth.Claims{
		Sub:         c.Sub,
		Roles:       roles,
		Permissions: perms,
		Attrs:       c.Extra,
	}.Grant(m)
}
