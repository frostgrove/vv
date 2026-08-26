package auth_test

import (
	"reflect"
	"testing"

	"github.com/frostgrove/vv/auth"
)

func TestClaimsAnswersMembershipAndPermissions(t *testing.T) {
	c := auth.Claims{
		Sub:         "u-1",
		Roles:       []auth.Role{"editor"},
		Permissions: []auth.Permission{"article:read"},
		Attrs:       map[string]any{"tenant": int64(7), "deputy": nil},
	}

	if !c.In("editor") || c.In("admin") {
		t.Fatal("In does not distinguish a role the principal holds from one it does not")
	}
	if !c.Has("article:read") || c.Has("article:delete") {
		t.Fatal("Has does not distinguish a granted permission from one that was never granted")
	}

	t.Run("Attr separates absent from present-and-nil", func(t *testing.T) {
		if v, ok := c.Attr("tenant"); !ok || v != int64(7) {
			t.Fatalf("Attr answered %v, %v for a claim that is there", v, ok)
		}
		if v, ok := c.Attr("deputy"); !ok || v != nil {
			t.Fatalf("a claim present with a nil value answered %v, %v — absent and nil are two states", v, ok)
		}
		if _, ok := c.Attr("nothing"); ok {
			t.Fatal("Attr reported a claim the token never carried")
		}
	})
}

func TestGrantFoldsTheRoleMapInOnce(t *testing.T) {
	m := auth.RoleMap{
		"editor": {"article:read", "article:write"},
		"admin":  {"article:read", "article:delete"},
	}

	t.Run("a role's permissions become the principal's", func(t *testing.T) {
		c := auth.Claims{Roles: []auth.Role{"editor"}}.Grant(m)
		if !c.Has("article:write") {
			t.Fatal("Grant did not expand the role, so every permission rule would refuse an editor")
		}
	})

	// The control. Without it the assertion above passes for a Grant that
	// hands out every permission in the map to everybody.
	t.Run("control: a permission no held role grants stays absent", func(t *testing.T) {
		c := auth.Claims{Roles: []auth.Role{"editor"}}.Grant(m)
		if c.Has("article:delete") {
			t.Fatal("Grant handed out a permission only admin grants, so expansion is not scoped to the roles held")
		}
	})

	t.Run("two roles granting the same permission grant it once", func(t *testing.T) {
		c := auth.Claims{Roles: []auth.Role{"editor", "admin"}}.Grant(m)
		n := 0
		for _, p := range c.Permissions {
			if p == "article:read" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("article:read appears %d times, want 1", n)
		}
	})

	t.Run("permissions already on the principal survive", func(t *testing.T) {
		c := auth.Claims{Roles: []auth.Role{"editor"}, Permissions: []auth.Permission{"own:read"}}.Grant(m)
		if !c.Has("own:read") {
			t.Fatal("Grant dropped a permission the token carried directly")
		}
	})

	t.Run("Grant does not write through to the receiver", func(t *testing.T) {
		c := auth.Claims{Roles: []auth.Role{"editor"}}
		_ = c.Grant(m)
		if len(c.Permissions) != 0 {
			t.Fatalf("Grant mutated the claims it was called on: %v", c.Permissions)
		}
	})
}

func TestTheQuantifiersDisagreeAboutTheEmptyCase(t *testing.T) {
	p := auth.Claims{Permissions: []auth.Permission{"a"}}

	if !auth.HasAll(p) {
		t.Fatal("HasAll of nothing refused, so a rule naming no permission would refuse everybody")
	}
	if auth.HasAny(p) {
		t.Fatal("HasAny of nothing accepted, so a rule naming no permission would be a licence")
	}
	if auth.InAny(p) {
		t.Fatal("InAny of nothing accepted")
	}

	t.Run("a nil principal satisfies nothing", func(t *testing.T) {
		if auth.HasAll(nil, "a") || auth.HasAny(nil, "a") || auth.InAny(nil, "r") {
			t.Fatal("a nil principal was granted something")
		}
	})

	t.Run("control: the quantifiers still answer yes for a principal that qualifies", func(t *testing.T) {
		if !auth.HasAll(p, "a") || !auth.HasAny(p, "a", "b") {
			t.Fatal("the quantifiers refuse a principal that holds the permission")
		}
	})
}

func TestScopesSplitsAnOAuthScopeClaim(t *testing.T) {
	got := auth.Scopes("read  write   admin")
	want := []auth.Permission{"read", "write", "admin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the scope claim split into %v, want %v", got, want)
	}
	if auth.Scopes("") != nil {
		t.Fatal("an empty scope claim produced permissions")
	}
}
