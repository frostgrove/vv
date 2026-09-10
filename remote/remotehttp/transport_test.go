package remotehttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port/porthttp"
	"github.com/frostgrove/vv/remote"
)

func TestTheDefaultClientIsOursAndHasADeadline(t *testing.T) {
	tr, ok := Transport("http://example.invalid/widgets").(*transport)
	if !ok {
		t.Fatalf("Transport no longer answers the type this test reaches into")
	}
	if tr.client == http.DefaultClient {
		t.Fatalf("the default client is http.DefaultClient — a timeout set here would be set for the whole process")
	}
	if tr.client.Timeout <= 0 {
		t.Fatalf("the default client has no timeout, so a peer that stops answering holds the caller forever")
	}

	mine := &http.Client{}
	tr2 := Transport("http://example.invalid/widgets", WithClient(mine)).(*transport)
	if tr2.client != mine {
		t.Fatalf("WithClient did not replace the default client")
	}
}

func TestAnAnswerPastTheCapIsRefusedRatherThanBuffered(t *testing.T) {
	var sent int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := strings.Repeat("x", 4096)
		sent = len(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	tr := Transport(srv.URL, WithMaxResponse(64))
	_, err := tr.Do(context.Background(), &remote.Call{Method: remote.MethodGet, ID: "1"})
	if err == nil {
		t.Fatalf("a %d-byte answer over a 64-byte cap was accepted", sent)
	}
	if !strings.Contains(err.Error(), "larger than the 64 bytes") {
		t.Fatalf("the refusal does not say what happened: %v", err)
	}

	small := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer small.Close()

	raw, err := Transport(small.URL, WithMaxResponse(64)).
		Do(context.Background(), &remote.Call{Method: remote.MethodGet, ID: "1"})
	if err != nil {
		t.Fatalf("an 8-byte answer under a 64-byte cap was refused: %v", err)
	}
	if string(raw) != `{"id":1}` {
		t.Fatalf("the answer under the cap came back as %s", raw)
	}
}

func TestTheCallersDeadlineReachesTheRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Transport(srv.URL).Do(ctx, &remote.Call{Method: remote.MethodGet, ID: "1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled context did not stop the call: %v", err)
	}
}

func TestADirectKeyedCallRefusesEligibilityControlsItCannotCarry(t *testing.T) {
	for name, request := range map[string]*query.Request{
		"filter":        {Filter: query.RawFilter(`{"name":"bolt"}`)},
		"terms":         {Terms: []query.Term{{}}},
		"search":        {Search: "bolt"},
		"search fields": {SearchFields: query.Strings{"Name"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Transport("http://example.invalid/widgets").Do(context.Background(), &remote.Call{
				Method: remote.MethodGet,
				ID:     "42",
				Query:  request,
			})
			var option *remote.OptionError
			if !errors.As(err, &option) {
				t.Fatalf("direct keyed call = %T %v, want OptionError", err, err)
			}
		})
	}
}

func TestADirectKeyedCallWithNoIDNeverReachesTheCollectionRoute(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()

	for _, method := range []remote.Method{
		remote.MethodGet,
		remote.MethodUpdate,
		remote.MethodReplace,
		remote.MethodDelete,
	} {
		t.Run(string(method), func(t *testing.T) {
			_, err := Transport(srv.URL).Do(context.Background(), &remote.Call{Method: method})
			if err == nil || !strings.Contains(err.Error(), "non-empty id") {
				t.Fatalf("empty %s = %v, want a local key refusal", method, err)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("%d empty keyed calls reached the server", calls)
	}
}

func TestADirectKeyedMutationWithNoBodyNeverReachesTheServer(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()

	for _, tc := range []struct {
		method remote.Method
		body   json.RawMessage
	}{
		{remote.MethodUpdate, nil},
		{remote.MethodReplace, nil},
		{remote.MethodUpdate, json.RawMessage("null")},
		{remote.MethodReplace, json.RawMessage("null")},
	} {
		t.Run(string(tc.method)+string(tc.body), func(t *testing.T) {
			_, err := Transport(srv.URL).Do(context.Background(), &remote.Call{
				Method: tc.method, ID: "42", Body: tc.body,
			})
			if err == nil || !strings.Contains(err.Error(), "non-null body") {
				t.Fatalf("empty %s body = %v, want a local refusal", tc.method, err)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("%d empty keyed mutations reached the server", calls)
	}
}

func TestADirectKeyedMutationWithNoJSONObjectNeverReachesTheServer(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()

	for _, tc := range []struct {
		method remote.Method
		body   json.RawMessage
	}{
		{remote.MethodUpdate, json.RawMessage("[]")},
		{remote.MethodReplace, json.RawMessage("false")},
		{remote.MethodUpdate, json.RawMessage(`"text"`)},
		{remote.MethodReplace, json.RawMessage(`{"broken":`)},
	} {
		t.Run(string(tc.method)+string(tc.body), func(t *testing.T) {
			_, err := Transport(srv.URL).Do(context.Background(), &remote.Call{
				Method: tc.method, ID: "42", Body: tc.body,
			})
			if err == nil || !strings.Contains(err.Error(), "JSON object") {
				t.Fatalf("invalid %s body = %v, want a local object refusal", tc.method, err)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("%d invalid keyed mutations reached the server", calls)
	}
}

func TestADirectBulkDeleteWithNoIDsUsesTheEmptySetSpelling(t *testing.T) {
	method, path, body, err := (&transport{}).route(&remote.Call{Method: remote.MethodBulkDelete})
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/bulk-delete" || string(body) != `{"ids":null}` {
		t.Fatalf("route = %s %s %s, want POST /bulk-delete {\"ids\":null}", method, path, body)
	}
}

func TestALocalizedHTTPViolationReachesTheRemoteCallerWhole(t *testing.T) {
	envelope := porthttp.Envelope{
		Type: "error",
		Errors: porthttp.Groups{
			Validation: []errs.Violation{{
				Path:          errs.Path{errs.Named("email")},
				Code:          errs.CodeUnique,
				Message:       "déjà pris",
				MessageLocale: "fr",
			}},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Language", "fr")
		w.WriteHeader(http.StatusConflict)
		if err := json.NewEncoder(w).Encode(envelope); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	_, err := Transport(server.URL).Do(context.Background(), &remote.Call{Method: remote.MethodGet, ID: "42"})
	fault, ok := errs.AsFault(err)
	if !ok {
		t.Fatalf("localized response = %T %v, want fault", err, err)
	}
	if len(fault.Violations) != 1 {
		t.Fatalf("localized response carried %d violations", len(fault.Violations))
	}
	violation := fault.Violations[0]
	if violation.Path.String() != "email" || violation.Code != errs.CodeUnique || violation.Message != "déjà pris" || violation.MessageLocale != "fr" {
		t.Fatalf("localized violation = %+v", violation)
	}
}

func TestMalformedOrForeignHTTPEnvelopesRemainProtocolErrors(t *testing.T) {
	cases := map[string]string{
		"foreign error object": `{"type":"error","message":"no route matched"}`,
		"invalid locale":       `{"type":"error","errors":{"general":[{"error_code":"internal","message":"x","message_locale":"fr_CA"}]}}`,
		"locale without text":  `{"type":"error","errors":{"general":[{"error_code":"internal","message_locale":"fr"}]}}`,
		"duplicate code":       `{"type":"error","errors":{"general":[{"error_code":"internal","error_code":"conflict"}]}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			err := fault(remote.MethodGet, "GET http://peer.invalid/widgets/42", "404 Not Found", http.StatusNotFound, []byte(body))
			var protocol *remote.ProtocolError
			if !errors.As(err, &protocol) {
				t.Fatalf("malformed peer body = %T %v, want ProtocolError", err, err)
			}
		})
	}
}

func TestRemoteHTTPReconstructionKeepsTheHardGlobalViolationBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		validation int
		general    int
		partial    bool
	}{
		{"exactly one hundred", porthttp.MaxViolations, 0, false},
		{"one hundred and one", porthttp.MaxViolations + 1, 0, true},
		{"split across groups", porthttp.MaxViolations - 1, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := remoteEnvelope(tc.validation, tc.general)
			err := fault(remote.MethodGet, "GET http://peer.invalid/widgets/42", "422 Unprocessable Entity", http.StatusUnprocessableEntity, raw)
			decoded, ok := errs.AsFault(err)
			if !ok {
				t.Fatalf("bounded peer body = %T %v, want fault", err, err)
			}
			if len(decoded.Violations) != porthttp.MaxViolations || decoded.Partial != tc.partial {
				t.Fatalf("%d+%d violations became %d with partial=%v", tc.validation, tc.general, len(decoded.Violations), decoded.Partial)
			}
			if decoded.Violations[0].Code != "v000" {
				t.Fatalf("first reconstructed code = %q, want v000", decoded.Violations[0].Code)
			}
			wantLast := errs.Code("v099")
			if tc.validation == porthttp.MaxViolations-1 {
				wantLast = "g000"
			}
			if got := decoded.Violations[len(decoded.Violations)-1].Code; got != wantLast {
				t.Fatalf("last reconstructed code = %q, want %q", got, wantLast)
			}
		})
	}
}

func remoteEnvelope(validation, general int) []byte {
	var body strings.Builder
	body.WriteString(`{"type":"error","errors":{"validation":[`)
	for i := 0; i < validation; i++ {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"field":["f%d"],"error_code":"v%03d"}`, i, i)
	}
	body.WriteString(`],"general":[`)
	for i := 0; i < general; i++ {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"error_code":"g%03d"}`, i)
	}
	body.WriteString(`]}}`)
	return []byte(body.String())
}
