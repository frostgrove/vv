package authjwt_test

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/authjwt"
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

// A 64-bit integer claim survives the round trip, at any nesting depth.
//
// Two hops had to be right and only one was. encoding/json decodes a JSON number
// into float64 unless told otherwise, so an id above 2^53 was rounded before this
// package saw it and `narrow` then faithfully converted the rounded value. And
// `narrow` ran one level deep, so a nested claim — `{"org":{"id":42}}`, which is
// how identity providers spell a tenant — came back as a json.Number and a
// caller comparing it to an int64 got a type mismatch and a scope that narrowed
// to nothing.
//
// Both are silent. A tenant id off by one is a row belonging to the wrong
// tenant, and nothing anywhere reports it.
func TestALargeIntegerClaimSurvivesAtAnyDepth(t *testing.T) {
	// Beyond 2^53: float64 cannot hold it exactly.
	const big int64 = 9007199254740995

	c := claims()
	c["tenant"] = big
	c["org"] = map[string]any{"id": big, "tags": []any{big}}
	tok := sign(t, jwt.SigningMethodHS256, secret, c)

	p := authjwt.New[authjwt.Claims](authjwt.HMAC(secret),
		authjwt.Issuer(issuer), authjwt.Audience(audience))
	got, err := p.Parse(t.Context(), tok)
	if err != nil {
		t.Fatal(err)
	}

	if v, ok := got.Attr("tenant"); !ok || v != any(big) {
		t.Fatalf("a top-level claim came back as %v (%T), want the int64 %d", v, v, big)
	}

	org, ok := got.Attr("org")
	if !ok {
		t.Fatal("the nested claim disappeared")
	}
	m, ok := org.(map[string]any)
	if !ok {
		t.Fatalf("the nested claim came back as %T", org)
	}
	if m["id"] != any(big) {
		t.Fatalf("a nested claim came back as %v (%T), want the int64 %d — a caller comparing it to an int64 gets a type mismatch and a scope that narrows to nothing", m["id"], m["id"], big)
	}
	list, ok := m["tags"].([]any)
	if !ok || len(list) != 1 || list[0] != any(big) {
		t.Fatalf("a claim inside a list came back as %v", m["tags"])
	}
}
