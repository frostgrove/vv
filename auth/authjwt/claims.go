package authjwt

import (
	"encoding/json"
	"strings"

	"github.com/frostgrove/vv/auth"
)

type Claims struct {
	Sub         string   `json:"sub"`
	Issuer      string   `json:"iss"`
	Roles       []string `json:"roles,omitempty"`
	Permissions []string `json:"permissions,omitempty"`

	Scope string `json:"scope,omitempty"`

	Extra map[string]any `json:"-"`
}

func (this *Claims) UnmarshalJSON(b []byte) error {
	type plain Claims
	var named plain
	if err := json.Unmarshal(b, &named); err != nil {
		return err
	}
	*this = Claims(named)

	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	raw := map[string]any{}
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	this.Extra = make(map[string]any, len(raw))
	for k, v := range raw {
		this.Extra[k] = narrow(v)
	}
	return nil
}

func narrow(v any) any {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
		if f, err := n.Float64(); err == nil {
			return f
		}
		return n.String()
	case map[string]any:
		for k, e := range n {
			n[k] = narrow(e)
		}
		return n
	case []any:
		for i, e := range n {
			n[i] = narrow(e)
		}
		return n
	default:
		return v
	}
}

func (this Claims) Subject() string { return this.Sub }

func (this Claims) In(r auth.Role) bool {
	for _, has := range this.Roles {
		if auth.Role(has) == r {
			return true
		}
	}
	return false
}

func (this Claims) Has(p auth.Permission) bool {
	for _, has := range this.Permissions {
		if auth.Permission(has) == p {
			return true
		}
	}
	for _, has := range auth.Scopes(this.Scope) {
		if has == p {
			return true
		}
	}
	return false
}

func (this Claims) Attr(name string) (any, bool) {
	v, ok := this.Extra[name]
	return v, ok
}

func (this Claims) Grant(m auth.RoleMap) auth.Claims {
	roles := make([]auth.Role, 0, len(this.Roles))
	for _, r := range this.Roles {
		roles = append(roles, auth.Role(r))
	}
	perms := make([]auth.Permission, 0, len(this.Permissions))
	for _, p := range this.Permissions {
		perms = append(perms, auth.Permission(p))
	}
	perms = append(perms, auth.Scopes(this.Scope)...)

	return auth.Claims{
		Sub:         this.Sub,
		Roles:       roles,
		Permissions: perms,
		Attrs:       this.Extra,
	}.Grant(m)
}
