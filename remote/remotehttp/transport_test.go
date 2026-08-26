package remotehttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frostgrove/vv/remote"
)

// The client a Transport builds for itself is never the process-wide one, and
// it is never without a deadline.
//
// Both halves matter and they fail differently. http.DefaultClient has no
// timeout, so a peer that accepts the connection and then says nothing holds
// the caller until something else gives up — which, inside a request handler
// with no deadline of its own, is never. And it belongs to the whole binary, so
// a consumer setting a timeout on it for this transport sets one for every
// other library that reached for the same value.
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

	// The control. WithClient still wins, or the constructor would be quietly
	// ignoring the one option that exists to replace this.
	mine := &http.Client{}
	tr2 := Transport("http://example.invalid/widgets", WithClient(mine)).(*transport)
	if tr2.client != mine {
		t.Fatalf("WithClient did not replace the default client")
	}
}

// An answer past the cap is refused rather than buffered.
//
// A remote resource is another service, and another service can be wrong: a
// paging bug on the far side, a proxy substituting an HTML page, a peer that has
// been taken over. Reading it whole turns any of those into this process
// running out of memory, which is the one failure a client cannot report.
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
	_, err := tr.Do(context.Background(), remote.Call{Method: remote.MethodGet, ID: "1"})
	if err == nil {
		t.Fatalf("a %d-byte answer over a 64-byte cap was accepted", sent)
	}
	if !strings.Contains(err.Error(), "larger than the 64 bytes") {
		t.Fatalf("the refusal does not say what happened: %v", err)
	}

	// The control. Everything above would hold just as well if this transport
	// refused every answer, so one under the cap has to get through.
	small := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer small.Close()

	raw, err := Transport(small.URL, WithMaxResponse(64)).
		Do(context.Background(), remote.Call{Method: remote.MethodGet, ID: "1"})
	if err != nil {
		t.Fatalf("an 8-byte answer under a 64-byte cap was refused: %v", err)
	}
	if string(raw) != `{"id":1}` {
		t.Fatalf("the answer under the cap came back as %s", raw)
	}
}

// The context's deadline still reaches the request, so a caller that bounds the
// call itself is not overridden by the client's own backstop.
func TestTheCallersDeadlineReachesTheRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Transport(srv.URL).Do(ctx, remote.Call{Method: remote.MethodGet, ID: "1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled context did not stop the call: %v", err)
	}
}
