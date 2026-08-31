//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/authjwt"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

const (
	authIssuer   = "https://id.example.test"
	authAudience = "integration"
)

var authSecret = []byte("integration secret, long enough to be one")

var (
	authRoles = auth.RoleMap{
		"editor": {"eg:read", "eg:write", "eg:delete"},
		"reader": {"eg:read"},
	}

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

func authSeed(t *testing.T, source crud.Source) {
	t.Helper()
	rows := AuthRows.Bind(source)
	for _, r := range []EgRow{
		{ID: 1, Tenant: 1, Name: "t1-first"},
		{ID: 2, Tenant: 1, Name: "t1-second"},
		{ID: 3, Tenant: 2, Name: "t2-only"},
	} {
		if _, err := rows.Save(context.Background(), &r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestATokensTenantClaimNarrowsTheStatement(t *testing.T) {
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			authSeed(t, tg.source)
			gated := AuthRows.Bind(tg.source, security.Gate(authPolicy))

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
				if _, err := gated.Save(ctx, &row); !errors.Is(err, crud.ErrForbidden) {
					t.Fatalf("writing into another tenant answered %v, want a denial", err)
				}

				if _, err := AuthRows.Bind(tg.source).GetByID(context.Background(), 99); !errors.Is(err, crud.ErrNotFound) {
					t.Fatal("the refused row is in the table")
				}
			})

			t.Run("control: a create into the token's own tenant lands", func(t *testing.T) {
				ctx := authenticate(t, authToken(t, "editor", 1, nil))
				row := EgRow{ID: 98, Tenant: 1, Name: "mine"}
				if _, err := gated.Save(ctx, &row); err != nil {
					t.Fatalf("writing into the caller's own tenant was refused: %v", err)
				}
				if _, err := AuthRows.Bind(tg.source).GetByID(context.Background(), 98); err != nil {
					t.Fatalf("the accepted row is not in the table: %v", err)
				}
			})

			t.Run("a role that grants no delete is refused", func(t *testing.T) {
				ctx := authenticate(t, authToken(t, "reader", 1, nil))
				if _, err := gated.Delete(ctx, 1); !errors.Is(err, crud.ErrForbidden) {
					t.Fatalf("a reader deleted a row, or answered %v", err)
				}
				if _, err := AuthRows.Bind(tg.source).GetByID(context.Background(), 1); err != nil {
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

func TestAGatedRepositoryRefusesWhatTheTokenDoesNotCarry(t *testing.T) {
	egSetup(t)
	tg := egEngines()[0]
	egWipe(t, tg.source)
	authSeed(t, tg.source)
	gated := AuthRows.Bind(tg.source, security.Gate(authPolicy))

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
