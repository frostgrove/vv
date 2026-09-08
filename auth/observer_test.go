package auth_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/frostgrove/vv/auth"
)

type refusals struct {
	mutex sync.Mutex
	seen  []auth.Reason
}

func (this *refusals) Refused(_ context.Context, reason auth.Reason) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.seen = append(this.seen, reason)
}

func (this *refusals) kinds() []auth.ReasonKind {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	out := make([]auth.ReasonKind, 0, len(this.seen))
	for _, reason := range this.seen {
		out = append(out, reason.Kind)
	}
	return out
}

func TestEveryRefusalReachesTheObserverWithTheReasonTheCallerNeverSees(t *testing.T) {
	observed := &refusals{}
	presented := headers(map[string]string{"Authorization": "Bearer the-forged-token"})

	if _, err := auth.NewGuard(no("signature does not verify"), auth.Observe(observed)).
		Authenticate(t.Context(), presented); err == nil {
		t.Fatal("a credential that does not verify was accepted")
	}
	if _, err := auth.NewGuard(yes("u-1"), auth.Observe(observed)).
		Authenticate(t.Context(), headers(nil)); err == nil {
		t.Fatal("a request with no credential was accepted")
	}
	if _, err := auth.NewGuard(yes("u-1"), auth.Observe(observed)).
		Authenticate(t.Context(), presented); err != nil {
		t.Fatalf("a credential that verifies was refused: %v", err)
	}

	kinds := observed.kinds()
	if len(kinds) != 2 {
		t.Fatalf("the observer saw %v; the two refusals are its whole job and the success is not", kinds)
	}
	if kinds[0] != auth.ReasonRejected || kinds[1] != auth.ReasonNoCredential {
		t.Fatalf("the observer cannot tell the two refusals apart: %v", kinds)
	}
	for _, reason := range observed.seen {
		if reason.Detail == "" {
			t.Fatalf("a %q refusal reached the observer with nothing to log", reason.Kind)
		}
		if strings.Contains(reason.Detail, "the-forged-token") {
			t.Fatalf("the credential itself was handed to the observer: %q", reason.Detail)
		}
	}
}

func TestASampledObserverSeesOneRefusalInEveryRun(t *testing.T) {
	observed := &refusals{}
	guard := auth.NewGuard(no("signature does not verify"), auth.Observe(auth.Sampled(4, observed)))

	for range 8 {
		if _, err := guard.Authenticate(t.Context(),
			headers(map[string]string{"Authorization": "Bearer forged"})); err == nil {
			t.Fatal("a credential that does not verify was accepted")
		}
	}

	if got := len(observed.kinds()); got != 2 {
		t.Fatalf("eight refusals sampled one in four reached the observer %d times, want 2", got)
	}
}

// Observers are consumer code on the refusal path, and this fan-out ran them
// inline with nothing between them. A panic in one took the request with it and
// took every observer registered after it — so a metrics counter that blew up
// could silence the audit log next to it. Every other fan-out in this repository
// isolates its children.
type panickingObserver struct{ called bool }

func (this *panickingObserver) Refused(context.Context, auth.Reason) {
	this.called = true
	panic("an observer's own bug")
}

func TestAPanickingObserverStopsNeitherTheRefusalNorTheObserverAfterIt(t *testing.T) {
	first := &panickingObserver{}
	after := &refusals{}

	_, err := auth.NewGuard(no("signature does not verify"),
		auth.Observe(first), auth.Observe(after)).
		Authenticate(t.Context(), headers(map[string]string{"Authorization": "Bearer forged"}))

	if err == nil {
		t.Fatal("a credential that does not verify was accepted")
	}
	if !first.called {
		t.Fatal("the panicking observer was never reached, so this proves nothing")
	}
	if kinds := after.kinds(); len(kinds) != 1 || kinds[0] != auth.ReasonRejected {
		t.Fatalf("the observer after the panicking one saw %v — one consumer's bug silenced another's audit log", kinds)
	}
}
