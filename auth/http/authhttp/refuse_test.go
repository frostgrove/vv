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

	"github.com/shardit-io/vv/auth"
	"github.com/shardit-io/vv/auth/http/authhttp"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/port"
	"github.com/shardit-io/vv/port/porthttp"
)

// ---------------------------------------------------------------------------
// fixtures

// The reason a refusal was made, and the string no response body may contain.
const badToken = "signature does not verify"

// stubRenderer answers what a test hands it and keeps the context it was
// rendered in, which is how the locale hop is observed from outside.
type stubRenderer struct {
	status int
	header http.Header
	body   any

	ctx context.Context
}

func (s *stubRenderer) Render(ctx context.Context, _ error) (int, http.Header, any) {
	s.ctx = ctx
	return s.status, s.header, s.body
}

// unencodable is a body the encoder refuses, and its refusal names the secret.
// A renderer is an interface precisely so a consumer can supply one of these by
// accident; what matters is what the client is told when they do.
type unencodable struct{ reason string }

func (u unencodable) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("cannot encode the refusal for %s", u.reason)
}

// frenchOnly answers one sentence and only in French, so a body carrying it is
// proof the request's language reached the message ladder.
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

// ---------------------------------------------------------------------------

// Whatever the renderer asked for is on the response, every value of it — a
// renderer that answers two challenges gets two, not the last one.
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

	// The control, and the one this package's doc comment owes: a renderer that
	// asks for no headers gets none invented for it. Without it the assertion
	// above would also pass for a Refuse that writes a challenge of its own —
	// which is the disclosure [[D-056]] exists to prevent, since a bearer
	// challenge's error= parameter says which half of the token was wrong.
	bare := &stubRenderer{status: http.StatusUnauthorized, body: porthttp.Internal()}
	w = httptest.NewRecorder()
	authhttp.Refuse(w, request(t, ""), bare, auth.Unauthenticated(badToken))
	if got := w.Header().Get("Www-Authenticate"); got != "" {
		t.Fatalf("a refusal set WWW-Authenticate %q that no renderer asked for", got)
	}
}

// A nil body means "write no body", and a status with no Content-Type on it is
// what a client reads.
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

	// The control. The same code path with a body does write one, so the empty
	// body above is the nil arm rather than a recorder that never saw bytes.
	rd.body = porthttp.Internal()
	w = httptest.NewRecorder()
	authhttp.Refuse(w, request(t, ""), rd, auth.Unauthenticated(badToken))
	if w.Body.Len() == 0 {
		t.Fatal("a refusal with a body wrote nothing either, so the case above proves nothing")
	}
}

// The reason a refusal was made never leaves the process — including when the
// reason is that this library could not encode its own envelope ([[D-044]],
// [[D-056]]). The client gets 500 and no sentence.
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

	// The control, and the half that makes the two assertions above mean
	// something: the secret existed and was reachable at that point, because
	// the line the operator gets does name it. A body that says nothing because
	// there was nothing to say would pass without this.
	if !strings.Contains(logged.String(), badToken) {
		t.Fatalf("the encoder's failure did not reach the application's logger either, so the silent body proves nothing: %q", logged.String())
	}

	// And the other control: the same renderer with an encodable body answers
	// its own status, so the 500 is about the encoding rather than about this
	// path always failing.
	rd.body = porthttp.Internal()
	w = httptest.NewRecorder()
	authhttp.Refuse(w, r, rd, auth.Unauthenticated(badToken))
	if w.Code != http.StatusUnauthorized || w.Body.Len() == 0 {
		t.Fatalf("an encodable refusal answered %d with %d bytes, want the renderer's 401 and a body", w.Code, w.Body.Len())
	}
}

// The locale is a rendering parameter read off the request, never a field on
// the fault — so the same 401 answers in the language this request asked for.
func TestARefusalIsRenderedInTheLanguageTheRequestAskedFor(t *testing.T) {
	rd := authhttp.RendererFor([]porthttp.RenderOption{porthttp.WithMessages(frenchOnly{})})

	w := httptest.NewRecorder()
	authhttp.Refuse(w, request(t, "fr-CA,fr;q=0.9"), rd, auth.Unauthenticated(badToken))
	if got := messageOf(t, w.Body.Bytes()); got != "authentification requise" {
		t.Fatalf("a request asking for fr-CA was refused in %q", got)
	}

	// The control: with no Accept-Language the catalogue does not answer and
	// the code's declared default does, so the leg above measures the header
	// rather than a catalogue that answers whatever it is asked.
	w = httptest.NewRecorder()
	authhttp.Refuse(w, request(t, ""), rd, auth.Unauthenticated(badToken))
	if got := messageOf(t, w.Body.Bytes()); got != "authentication is required" {
		t.Fatalf("a request that asked for no language was refused in %q, want the code's default", got)
	}

}

// The same hop seen from the renderer's side, which is what a binding that
// supplies its own renderer depends on: the locale reaches it in the context,
// not on the fault.
func TestTheRendererIsHandedTheLocaleTheRequestAskedFor(t *testing.T) {
	stub := &stubRenderer{status: http.StatusUnauthorized}
	authhttp.Refuse(httptest.NewRecorder(), request(t, "de-DE,de;q=0.8"), stub, auth.Unauthenticated(badToken))
	if got := port.LocaleFrom(stub.ctx); got != "de-DE" {
		t.Fatalf("the renderer was handed the locale %q, want the first tag of the header", got)
	}

	// The control: with no header the renderer is handed no locale, so the leg
	// above measures the header rather than a context that always carries one.
	bare := &stubRenderer{status: http.StatusUnauthorized}
	authhttp.Refuse(httptest.NewRecorder(), request(t, ""), bare, auth.Unauthenticated(badToken))
	if got := port.LocaleFrom(bare.ctx); got != "" {
		t.Fatalf("a request with no Accept-Language handed the renderer %q", got)
	}
}

// A refusal written without a request at all — a guard that failed before one
// was parsed — renders instead of panicking.
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

// The zero-configuration case is one renderer for the whole process: a renderer
// holds a vocabulary and a catalogue and nothing per-request, and building one
// per middleware would make the free case cost an allocation and a copy of both.
// The first leg pins the shared-value optimisation rather than an answer a
// caller can observe — a refactor that returned a fresh equivalent renderer each
// time would fail it while breaking nothing. It is here because the second leg
// is what the hazard actually is, and the two read as one claim.
func TestRendererForKeepsOneRendererForTheOrdinaryCase(t *testing.T) {
	if authhttp.RendererFor(nil) != authhttp.RendererFor(nil) {
		t.Fatal("two middlewares with no options were handed two different renderers")
	}

	// The one that matters. Options are not folded into that shared value — a second
	// wiring's catalogue must not become the first one's.
	withOpts := authhttp.RendererFor([]porthttp.RenderOption{porthttp.WithMessages(frenchOnly{})})
	if withOpts == authhttp.RendererFor(nil) {
		t.Fatal("a renderer built from options is the shared default, so one wiring's options would reach another")
	}
}
