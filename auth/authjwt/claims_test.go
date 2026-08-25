package authjwt_test

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/shardit-io/vv/auth"
	"github.com/shardit-io/vv/auth/authjwt"
)

func standardClaims(t *testing.T, c jwt.MapClaims) authjwt.Claims {
	t.Helper()
	got, err := parser[authjwt.Claims](t, authjwt.HMAC(secret)).Parse(t.Context(), signHS(t, c))
	if err != nil {
		t.Fatalf("the standard claims did not parse: %v", err)
	}
	return got
}

func TestClaimsIsAPrincipalOverBothSpellingsOfAPermission(t *testing.T) {
	c := claims()
	c["roles"] = []string{"editor"}
	c["permissions"] = []string{"article:read"}
	c["scope"] = "article:write profile"

	got := standardClaims(t, c)

	if got.Subject() != "u-1" {
		t.Fatalf("subject is %q, want u-1", got.Subject())
	}
	if !got.In("editor") || got.In("admin") {
		t.Fatal("In does not distinguish a role the token carried from one it did not")
	}
	if !got.Has("article:read") {
		t.Fatal("a permission from the permissions claim was not found")
	}
	if !got.Has("article:write") {
		t.Fatal("a permission from the OAuth scope string was not found")
	}
	if got.Has("article:delete") {
		t.Fatal("a permission the token never carried was granted")
	}

	var _ auth.Principal = got
}

// An integer claim must stay an integer. As a float64 a tenant id compiles into
// a float in the WHERE clause of every scoped query.
func TestAnIntegerClaimSurvivesAsAnInteger(t *testing.T) {
	c := claims()
	c["tenant"] = 7
	got := standardClaims(t, c)

	v, ok := got.Attr("tenant")
	if !ok {
		t.Fatal("the tenant claim is not reachable through Attr")
	}
	if _, isInt := v.(int64); !isInt {
		t.Fatalf("the tenant claim came back as %T (%v), want int64", v, v)
	}
	if v.(int64) != 7 {
		t.Fatalf("the tenant claim is %v, want 7", v)
	}
}

func TestAttrReachesEveryClaimAndOnlyTheOnesThatAreThere(t *testing.T) {
	c := claims()
	c["org"] = "acme"
	got := standardClaims(t, c)

	if v, ok := got.Attr("org"); !ok || v != "acme" {
		t.Fatalf("a custom claim answered %v, %v", v, ok)
	}
	if v, ok := got.Attr("iss"); !ok || v != issuer {
		t.Fatalf("a registered claim is not reachable through Attr: %v, %v", v, ok)
	}
	if _, ok := got.Attr("nothing"); ok {
		t.Fatal("Attr reported a claim the token never carried")
	}
}

func TestGrantExpandsRolesOnceIntoTheNeutralType(t *testing.T) {
	c := claims()
	c["roles"] = []string{"editor"}
	got := standardClaims(t, c).Grant(auth.RoleMap{"editor": {"article:read", "article:write"}})

	if !got.Has("article:write") {
		t.Fatal("the role map was not expanded, so every permission rule would refuse an editor")
	}
	// The control: expansion is scoped to the roles the token actually carried.
	if got.Has("article:purge") {
		t.Fatal("a permission no held role grants was handed out")
	}
	if got.Subject() != "u-1" {
		t.Fatalf("the neutral copy lost the subject: %q", got.Subject())
	}
	if v, ok := got.Attr("iss"); !ok || v != issuer {
		t.Fatal("the neutral copy lost the claims")
	}
}

func TestStandardIsTheTwoCallsInOne(t *testing.T) {
	c := claims()
	c["roles"] = []string{"editor"}
	authn := authjwt.Standard(
		authjwt.HMAC(secret),
		auth.RoleMap{"editor": {"article:read"}},
		authjwt.Issuer(issuer), authjwt.Audience(audience),
	)

	p, err := authn.Authenticate(t.Context(), auth.Credential{Scheme: auth.SchemeBearer, Token: signHS(t, c)})
	if err != nil {
		t.Fatalf("Standard refused a well-formed token: %v", err)
	}
	if !p.Has("article:read") {
		t.Fatal("Standard did not expand the role map")
	}
}

func TestAMapperMayRefuseATokenThatVerified(t *testing.T) {
	p := parser[MyClaims](t, authjwt.HMAC(secret))
	authn := authjwt.Authenticator(p, func(_ context.Context, c MyClaims) (auth.Principal, error) {
		return nil, auth.Unauthenticated("this token's tenant was deleted")
	})

	_, err := authn.Authenticate(t.Context(), auth.Credential{Token: signHS(t, claims())})
	if err == nil {
		t.Fatal("a mapper's refusal was ignored, so a valid token for a deleted tenant is accepted")
	}
}
