package accessjwt

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/auth/authjwt"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func issuedFor(t *testing.T, spec Spec) (access.Issued, *crudtest.Recorder) {
	t.Helper()

	source := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	deps := testDeps(source, access.Config{Session: access.SessionConfig{TTL: 24 * time.Hour}})
	deps.Subject = access.Subject{Type: "user"}
	deps.Grants = access.NewGrants(deps.Store, access.MustDirectories(rotationDirectory{}))

	issued, err := Strategy(spec).Build(deps)
	if err != nil {
		t.Fatalf("building the strategy: %v", err)
	}
	return issued, source
}

func minted(t *testing.T, spec Spec) string {
	t.Helper()

	issued, source := issuedFor(t, spec)
	moment := time.Now()
	source.Push(crudtest.Rows(sessionRow(moment, moment.Add(24*time.Hour))))
	return mint(t, issued)
}

func authenticating(t *testing.T, spec Spec) auth.Authenticator {
	t.Helper()

	issued, _ := issuedFor(t, spec)
	return issued.Authenticator
}

func mint(t *testing.T, issued access.Issued) string {
	t.Helper()

	response, err := issued.Issuer.Issue(t.Context(),
		access.SubjectRef{Type: "user", ID: uuid.New()}, access.Agent{})
	if err != nil {
		t.Fatalf("issuing a session: %v", err)
	}
	return response.Token
}

func audienceOf(t *testing.T, token string) any {
	t.Helper()

	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		t.Fatalf("reading the minted token: %v", err)
	}
	return claims["aud"]
}

func TestAnAccessTokenIsRefusedByAServiceItWasNotMintedFor(t *testing.T) {
	ours := testSpec()
	ours.Audience = "billing.example.test"
	theirs := testSpec()
	theirs.Audience = "reporting.example.test"

	token := minted(t, ours)
	if got := audienceOf(t, token); got != ours.Audience {
		t.Fatalf("the token names the audience %v, want %q", got, ours.Audience)
	}

	credential := auth.Credential{Scheme: auth.SchemeBearer, Token: token}
	if _, err := authenticating(t, ours).Authenticate(t.Context(), credential); err != nil {
		t.Fatalf("the service the token was minted for refused it, so the refusal below proves nothing: %v", err)
	}
	if _, err := authenticating(t, theirs).Authenticate(t.Context(), credential); err == nil {
		t.Fatal("a service accepted an access token minted for another audience; " +
			"one signing key shared between two services makes every session of the first a session of the second")
	}
}

func TestAnAudienceNobodyNamedIsTheIssuerRatherThanNone(t *testing.T) {
	spec := testSpec()
	if got := audienceOf(t, minted(t, spec)); got != spec.Issuer {
		t.Fatalf("a spec that names no audience minted aud=%v, want the issuer %q", got, spec.Issuer)
	}
}

func TestAnAudienceIsWaivedOnlyByNamingTheRisk(t *testing.T) {
	waived := testSpec()
	waived.UnsafeAnyAudience = true

	token := minted(t, waived)
	if got := audienceOf(t, token); got != nil {
		t.Fatalf("a waived audience still minted aud=%v", got)
	}

	elsewhere := testSpec()
	elsewhere.Audience = "somebody.else.test"
	foreign := auth.Credential{Scheme: auth.SchemeBearer, Token: minted(t, elsewhere)}
	if _, err := authenticating(t, waived).Authenticate(t.Context(), foreign); err != nil {
		t.Fatalf("the waiver refused a foreign audience it exists to accept: %v", err)
	}

	contradiction := testSpec()
	contradiction.Audience, contradiction.UnsafeAnyAudience = "billing.example.test", true
	if _, err := Strategy(contradiction).Build(testDeps(crudtest.Postgres(), access.Config{})); err == nil {
		t.Fatal("a spec that both names an audience and waives every audience started up")
	}
}

func aTokenTheKeySetWouldHaveToDecide(t *testing.T) string {
	t.Helper()

	part := func(document string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(document))
	}
	return strings.Join([]string{
		part(`{"alg":"RS256","typ":"JWT","kid":"the-key-the-provider-holds"}`),
		part(fmt.Sprintf(`{"sub":%q,"sty":"user","sid":%q,"iss":"example.test","aud":"example.test","exp":%d}`,
			uuid.New(), uuid.New(), time.Now().Add(time.Hour).Unix())),
		part("a signature only the provider's key could confirm"),
	}, ".")
}

func TestAKeyProviderOutageIsNotAnExpiredSession(t *testing.T) {
	outage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(outage.Close)

	spec := testSpec()
	spec.Verify = authjwt.JWKS(outage.URL)
	token := aTokenTheKeySetWouldHaveToDecide(t)

	_, err := authenticating(t, spec).Authenticate(t.Context(),
		auth.Credential{Scheme: auth.SchemeBearer, Token: token})
	if !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
		t.Fatalf("a key provider outage answered %v; a caller reading that as 401 signs out every live session", err)
	}
	if errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("the outage was folded into an authentication refusal: %v", err)
	}
}

func TestACancelledRequestIsNotAnAuthenticationRefusal(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	t.Cleanup(blocked.Close)

	spec := testSpec()
	spec.Verify = authjwt.JWKS(blocked.URL)
	token := aTokenTheKeySetWouldHaveToDecide(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := authenticating(t, spec).Authenticate(ctx,
		auth.Credential{Scheme: auth.SchemeBearer, Token: token})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled request answered %v, want its own cancellation", err)
	}
	if errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("a cancelled request was told its credential is bad: %v", err)
	}
}
