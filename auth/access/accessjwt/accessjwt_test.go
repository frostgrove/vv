package accessjwt

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/auth/authjwt"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const testSecret = "a signing key nobody outside the test holds"

func testSpec() Spec {
	return Spec{
		Method:     jwt.SigningMethodHS256,
		Key:        []byte(testSecret),
		Verify:     authjwt.HMAC([]byte(testSecret)),
		Issuer:     "example.test",
		AccessTTL:  5 * time.Minute,
		RefreshTTL: 24 * time.Hour,
	}
}

func testDeps(source crud.Source, config access.Config) access.StrategyDeps {
	store := access.NewStore(source)
	return access.StrategyDeps{
		Store:  store,
		Source: source,
		Config: config,
		Logger: slog.New(slog.DiscardHandler),
	}
}

func TestBuildRefusesALifetimeMatrixThatOutlivesTheSession(t *testing.T) {
	sessions := access.SessionConfig{TTL: 24 * time.Hour, IdleTTL: time.Hour}
	config := access.Config{Session: sessions}

	admissible := testSpec()
	admissible.RefreshTTL = 24 * time.Hour
	admissible.AccessTTL = time.Minute
	if _, err := Strategy(admissible).Build(testDeps(crudtest.Postgres(), config)); err != nil {
		t.Fatalf("the control matrix was refused, so the refusals below prove nothing: %v", err)
	}

	for name, broken := range map[string]func(Spec) Spec{
		"a lineage outliving the session row": func(spec Spec) Spec {
			spec.RefreshTTL = 48 * time.Hour
			return spec
		},
		"an access token outliving what can renew it": func(spec Spec) Spec {
			spec.RefreshTTL = 30 * time.Second
			spec.AccessTTL = time.Minute
			return spec
		},
		"an access token outliving the idle deadline": func(spec Spec) Spec {
			spec.AccessTTL = 2 * time.Hour
			return spec
		},
		"a grace window no shorter than the lineage": func(spec Spec) Spec {
			spec.RefreshGrace = 48 * time.Hour
			return spec
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Strategy(broken(admissible)).Build(testDeps(crudtest.Postgres(), config)); err == nil {
				t.Fatal("start-up accepted a lifetime matrix in which a credential outlives the thing that bounds it")
			}
		})
	}
}

func TestTheDefaultLifetimeMatrixStartsUp(t *testing.T) {
	spec := testSpec()
	spec.AccessTTL, spec.RefreshTTL, spec.RefreshGrace = 0, 0, 0
	if _, err := Strategy(spec).Build(testDeps(crudtest.Postgres(), access.Config{})); err != nil {
		t.Fatalf("a spec that names no lifetime at all was refused: %v", err)
	}
}

func TestALifetimeBelowZeroIsRefusedWhereAnOmittedOneTakesTheDefault(t *testing.T) {
	config := access.Config{Session: access.SessionConfig{TTL: 24 * time.Hour, IdleTTL: time.Hour}}

	for name, written := range map[string]struct {
		spec  func(Spec) Spec
		value string
	}{
		"an access lifetime that came out backwards": {
			spec:  func(spec Spec) Spec { spec.AccessTTL = -5 * time.Minute; return spec },
			value: "-5m0s",
		},
		"a lineage lifetime that came out backwards": {
			spec:  func(spec Spec) Spec { spec.RefreshTTL = -1 * time.Hour; return spec },
			value: "-1h0m0s",
		},
		"a grace window that came out backwards": {
			spec:  func(spec Spec) Spec { spec.RefreshGrace = -1 * time.Second; return spec },
			value: "-1s",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Strategy(written.spec(testSpec())).Build(testDeps(crudtest.Postgres(), config))
			if err == nil {
				t.Fatal("start-up took a negative lifetime, replaced it with a default nobody asked for, " +
					"and now mints tokens on a lifetime the deployment cannot read anywhere")
			}
			if !strings.Contains(err.Error(), written.value) {
				t.Fatalf("the refusal never says which value was written: %v", err)
			}
		})
	}
}

func TestARefreshMintsNoAccessTokenThatOutlivesItsSession(t *testing.T) {
	moment := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	config := access.Config{
		Session: access.SessionConfig{TTL: 24 * time.Hour, IdleTTL: time.Hour},
		Clock:   func() time.Time { return moment },
	}

	for name, tc := range map[string]struct {
		sessionEnds time.Duration
		wantExpiry  time.Duration
	}{
		"the session ends before the access token would": {sessionEnds: 30 * time.Second, wantExpiry: 30 * time.Second},
		"the session outlasts it":                        {sessionEnds: time.Hour, wantExpiry: 5 * time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			source := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
			source.Push(crudtest.Rows(sessionRow(moment, moment.Add(tc.sessionEnds))))

			directories := access.MustDirectories(rotationDirectory{})
			deps := testDeps(source, config)
			deps.Grants = access.NewGrants(deps.Store, directories)

			issued, err := Strategy(testSpec()).Build(deps)
			if err != nil {
				t.Fatalf("building the strategy: %v", err)
			}

			response, err := issued.Refresher.Refresh(t.Context(), "the-refresh-credential", access.Agent{})
			if err != nil {
				t.Fatalf("rotating a live session: %v\nstatements: %v", err, source.SQL())
			}
			if want := moment.Add(tc.wantExpiry); !response.ExpiresAt.Equal(want) {
				t.Fatalf("the access token expires at %s; the session it belongs to ends at %s",
					response.ExpiresAt, moment.Add(tc.sessionEnds))
			}
			if !strings.Contains(response.Token, ".") {
				t.Fatalf("no token was minted: %q", response.Token)
			}
		})
	}
}

type rotationDirectory struct{}

func (rotationDirectory) SubjectType() access.SubjectType { return "user" }

func (rotationDirectory) Active(_ context.Context, _ uuid.UUID) (bool, error) { return true, nil }

func (rotationDirectory) Describe(_ context.Context, _ uuid.UUID) (access.Profile, error) {
	return access.Profile{}, nil
}

func (rotationDirectory) Touch(_ context.Context, _ uuid.UUID) error { return nil }

func sessionRow(lastUsed, expires time.Time) []any {
	return []any{
		uuid.New().String(), "user", uuid.New().String(),
		access.HashToken("the-refresh-credential"), "",
		"", "", lastUsed, lastUsed, nil, expires, nil, "",
	}
}

func TestARefreshRefusesASessionAbandonedPastTheIdleDeadline(t *testing.T) {
	moment := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	config := access.Config{
		Session: access.SessionConfig{TTL: 24 * time.Hour, IdleTTL: time.Hour},
		Clock:   func() time.Time { return moment },
	}

	source := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	source.Push(crudtest.Rows(sessionRow(moment.Add(-2*time.Hour), moment.Add(24*time.Hour))))

	deps := testDeps(source, config)
	deps.Grants = access.NewGrants(deps.Store, access.MustDirectories(rotationDirectory{}))

	issued, err := Strategy(testSpec()).Build(deps)
	if err != nil {
		t.Fatalf("building the strategy: %v", err)
	}

	response, err := issued.Refresher.Refresh(t.Context(), "the-refresh-credential", access.Agent{})
	if err == nil {
		t.Fatalf("a session untouched for two hours rotated under a one-hour idle deadline: %+v", response)
	}
	for _, statement := range source.SQL() {
		if strings.HasPrefix(strings.ToUpper(crudtest.Normalize(statement)), "UPDATE") {
			t.Fatalf("the refused rotation still wrote to the session: %s", statement)
		}
	}
}
