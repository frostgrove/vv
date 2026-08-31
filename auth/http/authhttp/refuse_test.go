package authhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/port/porthttp"
)

const badToken = "signature does not verify"

type stubRenderer struct {
	status int
	header http.Header
	body   any

	ctx context.Context
}

func (this *stubRenderer) Render(ctx context.Context, _ error) (int, http.Header, any) {
	this.ctx = ctx
	return this.status, this.header, this.body
}

type unencodable struct{ reason string }

func (this unencodable) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("cannot encode the refusal for %s", this.reason)
}

type frenchOnly struct{}

func (frenchOnly) Message(_ context.Context, v errs.Violation, locale string) (string, bool) {
	if locale == "fr-CA" && v.Code == errs.CodeUnauthenticated {
		return "authentification requise", true
	}
	return "", false
}

func request(t *testing.T, header string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/articles", nil)
	if header != "" {
		r.Header.Set("Accept-Language", header)
	}
	return r
}

func messageOf(t *testing.T, body []byte) string {
	t.Helper()
	var env porthttp.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("the refusal body is not the envelope: %v: %s", err, body)
	}
	if len(env.Errors.General) != 1 {
		t.Fatalf("a 401 carries %d general violations, want the one synthesised from its code: %s", len(env.Errors.General), body)
	}
	return env.Errors.General[0].Message
}

func TestARefusalCarriesEveryHeaderTheRendererAskedFor(t *testing.T) {
	rd := &stubRenderer{
		status: http.StatusUnauthorized,
		header: http.Header{
			"Www-Authenticate": []string{`Bearer realm="api"`, `Basic realm="api"`},
			"X-Refusal":        []string{"one"},
		},
		body: porthttp.Internal(),
	}
	w := httptest.NewRecorder()
	authhttp.Refuse(w, request(t, ""), rd, auth.Unauthenticated(badToken))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("the refusal answered %d, want the renderer's 401", w.Code)
	}
	if got := w.Header().Values("Www-Authenticate"); len(got) != 2 {
		t.Fatalf("the renderer asked for two challenges and the response carries %v", got)
	}
	if got := w.Header().Get("X-Refusal"); got != "one" {
		t.Fatalf("a header the renderer asked for reads as %q on the response", got)
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("a refusal with a body is Content-Type %q", got)
	}

	bare := &stubRenderer{status: http.StatusUnauthorized, body: porthttp.Internal()}
	w = httptest.NewRecorder()
	authhttp.Refuse(w, request(t, ""), bare, auth.Unauthenticated(badToken))
	if got := w.Header().Get("Www-Authenticate"); got != "" {
		t.Fatalf("a refusal set WWW-Authenticate %q that no renderer asked for", got)
	}
}

func TestARefusalWithNoBodyIsTheStatusAndNothingElse(t *testing.T) {
	rd := &stubRenderer{status: http.StatusUnauthorized}
	w := httptest.NewRecorder()
	authhttp.Refuse(w, request(t, ""), rd, auth.Unauthenticated(badToken))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("a bodiless refusal answered %d, want 401", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("a nil body was written as %q", w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "" {
		t.Fatalf("a refusal with no body announced Content-Type %q", got)
	}

	rd.body = porthttp.Internal()
	w = httptest.NewRecorder()
	authhttp.Refuse(w, request(t, ""), rd, auth.Unauthenticated(badToken))
	if w.Body.Len() == 0 {
		t.Fatal("a refusal with a body wrote nothing either, so the case above proves nothing")
	}
}

func TestARefusalThatWillNotEncodeIs500AndSaysNothing(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))

	rd := &stubRenderer{status: http.StatusUnauthorized, body: unencodable{reason: badToken}}
	r := request(t, "").WithContext(port.WithLogger(context.Background(), logger))
	w := httptest.NewRecorder()
	authhttp.Refuse(w, r, rd, auth.Unauthenticated(badToken))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("a refusal that would not encode answered %d, want 500", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("the encoder's own failure was written to the client: %q", w.Body.String())
	}
	if strings.Contains(w.Body.String(), badToken) {
		t.Fatalf("the reason for the refusal reached the client: %q", w.Body.String())
	}

	if !strings.Contains(logged.String(), badToken) {
		t.Fatalf("the encoder's failure did not reach the application's logger either, so the silent body proves nothing: %q", logged.String())
	}

	rd.body = porthttp.Internal()
	w = httptest.NewRecorder()
	authhttp.Refuse(w, r, rd, auth.Unauthenticated(badToken))
	if w.Code != http.StatusUnauthorized || w.Body.Len() == 0 {
		t.Fatalf("an encodable refusal answered %d with %d bytes, want the renderer's 401 and a body", w.Code, w.Body.Len())
	}
}

func TestARefusalIsRenderedInTheLanguageTheRequestAskedFor(t *testing.T) {
	rd := authhttp.RendererFor([]porthttp.RenderOption{porthttp.WithMessages(frenchOnly{})})

	w := httptest.NewRecorder()
	authhttp.Refuse(w, request(t, "fr-CA,fr;q=0.9"), rd, auth.Unauthenticated(badToken))
	if got := messageOf(t, w.Body.Bytes()); got != "authentification requise" {
		t.Fatalf("a request asking for fr-CA was refused in %q", got)
	}

	w = httptest.NewRecorder()
	authhttp.Refuse(w, request(t, ""), rd, auth.Unauthenticated(badToken))
	if got := messageOf(t, w.Body.Bytes()); got != "authentication is required" {
		t.Fatalf("a request that asked for no language was refused in %q, want the code's default", got)
	}

}

func TestTheRendererIsHandedTheLocaleTheRequestAskedFor(t *testing.T) {
	stub := &stubRenderer{status: http.StatusUnauthorized}
	authhttp.Refuse(httptest.NewRecorder(), request(t, "de-DE,de;q=0.8"), stub, auth.Unauthenticated(badToken))
	if got := port.LocaleFrom(stub.ctx); got != "de-DE" {
		t.Fatalf("the renderer was handed the locale %q, want the first tag of the header", got)
	}

	bare := &stubRenderer{status: http.StatusUnauthorized}
	authhttp.Refuse(httptest.NewRecorder(), request(t, ""), bare, auth.Unauthenticated(badToken))
	if got := port.LocaleFrom(bare.ctx); got != "" {
		t.Fatalf("a request with no Accept-Language handed the renderer %q", got)
	}
}

func TestARefusalWithNoRequestStillRenders(t *testing.T) {
	if got := port.LocaleFrom(authhttp.Locale(nil)); got != "" {
		t.Fatalf("a refusal with no request carries the locale %q", got)
	}

	stub := &stubRenderer{status: http.StatusUnauthorized}
	w := httptest.NewRecorder()
	authhttp.Refuse(w, nil, stub, auth.Unauthenticated(badToken))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("a refusal with no request answered %d, want the renderer's status", w.Code)
	}
}

func TestRendererForKeepsOneRendererForTheOrdinaryCase(t *testing.T) {
	if authhttp.RendererFor(nil) != authhttp.RendererFor(nil) {
		t.Fatal("two middlewares with no options were handed two different renderers")
	}

	withOpts := authhttp.RendererFor([]porthttp.RenderOption{porthttp.WithMessages(frenchOnly{})})
	if withOpts == authhttp.RendererFor(nil) {
		t.Fatal("a renderer built from options is the shared default, so one wiring's options would reach another")
	}
}
