//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/shardit-io/vv/auth"
	"github.com/shardit-io/vv/auth/authjwt"
	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/decorators/security"
	"github.com/shardit-io/vv/crud/sqlrepo"
)

// The whole chain against a real database: a token is verified, its claims
// become a principal, and the principal becomes a WHERE clause on three
// engines.
//
// The unit tests assert the SQL that is built. This asserts the rows that come
// back, which is the only thing that says the two halves agree.

const (
	authIssuer   = "https://id.example.test"
	authAudience = "integration"
)

var authSecret = []byte("integration secret, long enough to be one")

var (
	// The roles the tokens carry, and what they grant.
	authRoles = auth.RoleMap{
		"editor": {"eg:read", "eg:write", "eg:delete"},
		"reader": {"eg:read"},
	}

	// The policy under test: what you may do, and which rows exist for you.
	authPolicy = security.Combine(
		security.PerAction[EgRow, int64](map[security.Action]auth.Permission{
			security.Read:   "eg:read",
			security.Create: "eg:write",
			security.Update: "eg:write",
			security.Delete: "eg:delete",
		}),
		security.ScopeAttr[EgRow, int64]("Tenant", "tenant"),
	)

	AuthRows = sqlrepo.Define[EgRow, int64, struct{}]("eg_rows")
)

// authenticate runs a token through the same authenticator a middleware would,
// and answers the context a repository will see.
func authenticate(t *testing.T, token string) context.Context {
	t.Helper()
	authn := authjwt.Standard(
		authjwt.HMAC(authSecret), authRoles,
		authjwt.Issuer(authIssuer), authjwt.Audience(authAudience),
	)
	ctx, err := auth.NewGuard(authn).Authenticate(context.Background(),
		func(name string) string {
			if name == auth.HeaderAuthorization {
				return "Bearer " + token
			}
			return ""
		})
	if err != nil {
		t.Fatalf("the token was refused: %v", err)
	}
	return ctx
}

func authToken(t *testing.T, role string, tenant int64, mutate func(jwt.MapClaims)) string {
	t.Helper()
	c := jwt.MapClaims{
		"sub":    "u-" + role,
		"iss":    authIssuer,
		"aud":    authAudience,
		"exp":    time.Now().Add(time.Hour).Unix(),
		"roles":  []string{role},
		"tenant": tenant,
	}
	if mutate != nil {
		mutate(c)
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(authSecret)
	if err != nil {
		t.Fatalf("signing the fixture: %v", err)
	}
	return s
}

// authSeed writes two tenants' rows, so a narrowing that does nothing is
// visible as extra rows rather than as no rows.
func authSeed(t *testing.T, src crud.Source) {
	t.Helper()
	rows := AuthRows.Bind(src)
	for _, r := range []EgRow{
		{ID: 1, Tenant: 1, Name: "t1-first"},
		{ID: 2, Tenant: 1, Name: "t1-second"},
		{ID: 3, Tenant: 2, Name: "t2-only"},
	} {
		if err := rows.Save(context.Background(), &r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestATokensTenantClaimNarrowsTheStatement(t *testing.T) {
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.src)
			authSeed(t, tg.src)
			gated := AuthRows.Bind(tg.src, security.Gate(authPolicy))

			t.Run("a list carries only the token's tenant", func(t *testing.T) {
				ctx := authenticate(t, authToken(t, "editor", 1, nil))
				got, err := gated.GetAll(ctx, crud.OrderBy(crud.Asc("ID")))
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
					t.Fatalf("rows = %+v, want only tenant 1's two rows", got)
				}
			})

			// The control. Without it the assertion above passes for a gate
			// that returns nothing at all, and for a database that happens to
			// hold only tenant 1's rows.
			t.Run("control: the other tenant's token sees the other row", func(t *testing.T) {
				ctx := authenticate(t, authToken(t, "editor", 2, nil))
				got, err := gated.GetAll(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 1 || got[0].ID != 3 {
					t.Fatalf("rows = %+v, want only tenant 2's one row", got)
				}
			})

			// D-008: a row that is there but invisible must not be
			// distinguishable from one that is not there.
			t.Run("another tenant's id is not found, not forbidden", func(t *testing.T) {
				ctx := authenticate(t, authToken(t, "editor", 1, nil))
				_, err := gated.GetByID(ctx, 3)
				if !errors.Is(err, crud.ErrNotFound) {
					t.Fatalf("reading another tenant's row answered %v, want ErrNotFound", err)
				}
				if errors.Is(err, crud.ErrForbidden) {
					t.Fatal("the answer confirmed the row exists")
				}
			})

			t.Run("a create into another tenant is refused", func(t *testing.T) {
				ctx := authenticate(t, authToken(t, "editor", 1, nil))
				row := EgRow{ID: 99, Tenant: 2, Name: "smuggled"}
				if err := gated.Save(ctx, &row); !errors.Is(err, crud.ErrForbidden) {
					t.Fatalf("writing into another tenant answered %v, want a denial", err)
				}
				// And it really did not land.
				if _, err := AuthRows.Bind(tg.src).GetByID(context.Background(), 99); !errors.Is(err, crud.ErrNotFound) {
					t.Fatal("the refused row is in the table")
				}
			})

			t.Run("control: a create into the token's own tenant lands", func(t *testing.T) {
				ctx := authenticate(t, authToken(t, "editor", 1, nil))
				row := EgRow{ID: 98, Tenant: 1, Name: "mine"}
				if err := gated.Save(ctx, &row); err != nil {
					t.Fatalf("writing into the caller's own tenant was refused: %v", err)
				}
				if _, err := AuthRows.Bind(tg.src).GetByID(context.Background(), 98); err != nil {
					t.Fatalf("the accepted row is not in the table: %v", err)
				}
			})

			t.Run("a role that grants no delete is refused", func(t *testing.T) {
				ctx := authenticate(t, authToken(t, "reader", 1, nil))
				if _, err := gated.Delete(ctx, 1); !errors.Is(err, crud.ErrForbidden) {
					t.Fatalf("a reader deleted a row, or answered %v", err)
				}
				if _, err := AuthRows.Bind(tg.src).GetByID(context.Background(), 1); err != nil {
					t.Fatalf("the row a reader was refused is gone anyway: %v", err)
				}
			})

			t.Run("control: a role that grants delete succeeds", func(t *testing.T) {
				ctx := authenticate(t, authToken(t, "editor", 1, nil))
				n, err := gated.Delete(ctx, 2)
				if err != nil || n != 1 {
					t.Fatalf("an editor's delete answered %d, %v", n, err)
				}
			})
		})
	}
}

// Everything the token cannot supply fails closed, against a real connection
// and with nothing executed.
func TestAGatedRepositoryRefusesWhatTheTokenDoesNotCarry(t *testing.T) {
	egSetup(t)
	tg := egEngines()[0]
	egWipe(t, tg.src)
	authSeed(t, tg.src)
	gated := AuthRows.Bind(tg.src, security.Gate(authPolicy))

	t.Run("no principal at all is unauthenticated", func(t *testing.T) {
		_, err := gated.GetAll(context.Background())
		if !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("an unauthenticated read answered %v", err)
		}
	})

	t.Run("a token with no tenant claim is a denial, not tenant zero", func(t *testing.T) {
		ctx := authenticate(t, authToken(t, "editor", 1, func(c jwt.MapClaims) {
			delete(c, "tenant")
		}))
		if _, err := gated.GetAll(ctx); !errors.Is(err, crud.ErrForbidden) {
			t.Fatalf("a token with no tenant claim answered %v, want a denial", err)
		}
	})

	t.Run("an expired token never reaches the repository", func(t *testing.T) {
		expired := authToken(t, "editor", 1, func(c jwt.MapClaims) {
			c["exp"] = time.Now().Add(-time.Hour).Unix()
		})
		authn := authjwt.Standard(authjwt.HMAC(authSecret), authRoles,
			authjwt.Issuer(authIssuer), authjwt.Audience(authAudience))
		_, err := authn.Authenticate(context.Background(),
			auth.Credential{Scheme: auth.SchemeBearer, Token: expired})
		if !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("an expired token answered %v", err)
		}
	})
}
