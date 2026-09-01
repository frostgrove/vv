package jobs

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRedriveInvocationRebasesExecutionAndPreservesDurableIdentity(t *testing.T) {
	policy := testInvocationPolicy(t)
	spec := testInvocationSpec(t, policy)
	spec.StartBefore = spec.EligibleAt.Add(15 * time.Minute)
	original, err := NewInvocation(spec)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := original.Terminate(original.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	at := terminal.FinishedAt().Add(24 * time.Hour).In(time.FixedZone("operator", -7*60*60))
	redriven, err := RedriveInvocation(terminal, at)
	if err != nil {
		t.Fatal(err)
	}
	canonical := at.Round(0).UTC()
	legacy, legacyOK := terminal.LegacyIntent()
	redrivenLegacy, redrivenLegacyOK := redriven.LegacyIntent()
	if redriven.ID() != terminal.ID() || redriven.Namespace() != terminal.Namespace() || redriven.Partition() != terminal.Partition() || redriven.Definition() != terminal.Definition() || redriven.Queue() != terminal.Queue() || redriven.Mode() != terminal.Mode() || redriven.Intent() != terminal.Intent() || redriven.Priority() != terminal.Priority() || redriven.Policy() != terminal.Policy() || legacyOK != redrivenLegacyOK || legacy != redrivenLegacy {
		t.Fatal("redrive changed stable invocation data")
	}
	if !reflect.DeepEqual(redriven.Context().Record(), terminal.Context().Record()) {
		t.Fatal("redrive changed durable context")
	}
	if redriven.State() != InvocationQueued || redriven.IsTerminal() || redriven.CreatedAt() != canonical || redriven.EligibleAt() != canonical || redriven.MaxElapsedAt() != canonical.Add(policy.MaxElapsed()) || redriven.StartBefore() != canonical.Add(15*time.Minute) {
		t.Fatalf("redriven execution = state %v created %v eligible %v start-before %v max-elapsed %v", redriven.State(), redriven.CreatedAt(), redriven.EligibleAt(), redriven.StartBefore(), redriven.MaxElapsedAt())
	}
	if redriven.AttemptOrdinal().Value() != 0 || redriven.RetrySpent().Value() != 0 || redriven.HandlerDeferrals().Value() != 0 || redriven.DeliveryDeferrals().Value() != 0 || !redriven.FinishedAt().IsZero() || len(redriven.Attempts()) != 0 || len(redriven.History()) != 1 || redriven.Outcome().Kind() != InvocationOutcomeInitial {
		t.Fatal("redrive retained execution history")
	}
}

func TestRedriveInvocationRejectsNonterminalAndInvalidTime(t *testing.T) {
	queued := testGenesisInvocation(t)
	if _, err := RedriveInvocation(queued, queued.EligibleAt()); !errors.Is(err, ErrConflict) {
		t.Fatalf("queued redrive = %v", err)
	}
	terminal, err := queued.Terminate(queued.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RedriveInvocation(terminal, time.Time{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero-time redrive = %v", err)
	}
	if value, err := RedriveInvocation(Invocation{}, terminal.FinishedAt()); !value.IsZero() || !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero redrive = (%v, %v)", value, err)
	}
}

func TestDeliveryViewClonesPayloadAndStaysRedacted(t *testing.T) {
	invocation := testGenesisInvocation(t)
	data := []byte("private-payload")
	payload, err := NewEncodedPayload(builtinCodecID("bytes"), 1, data)
	if err != nil {
		t.Fatal(err)
	}
	view, err := NewDeliveryView(invocation, payload)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	first := view.Payload().Bytes()
	first[0] = 'Y'
	if got := view.Payload().Bytes(); !bytes.Equal(got, []byte("private-payload")) {
		t.Fatalf("view payload = %q", got)
	}
	if view.Invocation().ID() != invocation.ID() || view.IsZero() {
		t.Fatal("view lost invocation")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		formatted := fmt.Sprintf(format, view)
		if !strings.Contains(formatted, "delivery view") || strings.Contains(formatted, "private-payload") {
			t.Fatalf("format %q = %q", format, formatted)
		}
	}
	if _, err := view.MarshalJSON(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("MarshalJSON = %v", err)
	}
	if value, err := NewDeliveryView(Invocation{}, payload); !value.IsZero() || !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero view = (%v, %v)", value, err)
	}
}
