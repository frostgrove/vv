package audit_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
)

type declarationApproval struct {
	OrderID int64
	Outcome audit.Outcome
	Reason  audit.Reason
	Rule    string
	At      time.Time
}

func TestEventDeclarationExtractsEveryExplicitValueOnce(t *testing.T) {
	calls := map[string]int{}
	policy := audit.EventPolicy[declarationApproval]{
		Semantics:  declarationPolicySemantics(),
		Descriptor: declarationDescriptor("commerce.order", "commerce.order.approved"),
		Target: audit.EventTarget(func(value declarationApproval) audit.Reference {
			calls["target"]++
			return audit.Reference("order-1")
		}, audit.Internal, audit.AsToken),
		Outcome: audit.EventOutcome(audit.Outcomes("approved", "rejected"), func(value declarationApproval) audit.Outcome {
			calls["outcome"]++
			return value.Outcome
		}),
		Reason: audit.OptionalEventReason(audit.Reasons("manual.override", "policy.approved"), func(value declarationApproval) audit.Reason {
			calls["reason"]++
			return value.Reason
		}),
		OccurredAt: audit.EventOccurredAt(func(value declarationApproval) time.Time {
			calls["time"]++
			return value.At
		}),
		Fields: audit.EventFields(
			audit.EventProtected("rule", func(value declarationApproval) string {
				calls["field"]++
				return value.Rule
			}, audit.Text(), audit.Personal),
		),
	}
	event, err := audit.TryDeclare(policy)
	if err != nil {
		t.Fatalf("declare event: %v", err)
	}
	value := declarationApproval{OrderID: 1, Outcome: "approved", Reason: "policy.approved", Rule: "standard", At: time.Date(2026, 4, 5, 6, 7, 8, 9, time.FixedZone("fixture", 3600))}
	if _, err := event.New(value); err != nil {
		t.Fatalf("build event draft: %v", err)
	}
	for _, name := range []string{"target", "outcome", "reason", "time", "field"} {
		if calls[name] != 1 {
			t.Fatalf("%s extractor ran %d times, want once", name, calls[name])
		}
	}
	description := event.Description()
	if !description.TargetPresent || description.Target.Mode != audit.AsToken || !description.ReasonOptional {
		t.Fatalf("event description lost target/reason policy: %+v", description)
	}
	description.Outcomes[0] = "mutated"
	description.Fields[0].Codec.ReadVersions[0] = 99
	again := event.Description()
	if again.Outcomes[0] != "approved" || again.Fields[0].Codec.ReadVersions[0] != 1 {
		t.Fatal("mutating an event description changed the sealed declaration")
	}
	fingerprint, err := audit.ComputeEventFixtureFingerprint(event, "fixture.one", value)
	if err != nil || fingerprint == (audit.PolicyFixtureFingerprint{}) {
		t.Fatalf("compute event fixture fingerprint: fingerprint=%x err=%v", fingerprint, err)
	}
}

func TestEventDeclarationRejectsUnknownCodesBeforeEncodingFields(t *testing.T) {
	fieldCalls := 0
	event := audit.Declare(audit.EventPolicy[declarationApproval]{
		Semantics:  declarationPolicySemantics(),
		Descriptor: declarationDescriptor("commerce.order", "commerce.order.refused"),
		Target:     audit.NoEventTarget[declarationApproval](),
		Outcome: audit.EventOutcome(audit.Outcomes("accepted"), func(value declarationApproval) audit.Outcome {
			return value.Outcome
		}),
		Reason: audit.EventReason(audit.Reasons("policy.allowed"), func(value declarationApproval) audit.Reason {
			return value.Reason
		}),
		OccurredAt: audit.EventOccurredAt(func(value declarationApproval) time.Time { return value.At }),
		Fields: audit.EventFields(
			audit.EventValue("rule", func(value declarationApproval) string {
				fieldCalls++
				return value.Rule
			}, audit.Text(), audit.Internal),
		),
	})
	_, err := event.New(declarationApproval{Outcome: "unknown", Reason: "policy.allowed", At: time.Now()})
	if !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("unknown outcome error = %v, want ErrInvalid", err)
	}
	if fieldCalls != 0 {
		t.Fatalf("field extractor ran %d times after an unknown outcome", fieldCalls)
	}
	_, err = event.New(declarationApproval{Outcome: "accepted", Reason: "unknown", At: time.Now()})
	if !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("unknown reason error = %v, want ErrInvalid", err)
	}
	if fieldCalls != 0 {
		t.Fatalf("field extractor ran %d times after an unknown reason", fieldCalls)
	}
}

func TestEventCodeSetsAreCanonicalCopiedAndRejectDuplicates(t *testing.T) {
	input := []audit.Outcome{"zeta", "alpha"}
	codes := audit.Outcomes(input...)
	input[0] = "changed"
	event := audit.Declare(audit.EventPolicy[declarationApproval]{
		Semantics:  declarationPolicySemantics(),
		Descriptor: declarationDescriptor("commerce.order", "commerce.order.codes"),
		Target:     audit.NoEventTarget[declarationApproval](),
		Outcome:    audit.EventOutcome(codes, func(value declarationApproval) audit.Outcome { return value.Outcome }),
		OccurredAt: audit.EventOccurredAt(func(value declarationApproval) time.Time { return value.At }),
	})
	if got := event.Description().Outcomes; len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("canonical outcomes = %v", got)
	}
	if _, err := audit.TryOutcomes("same", "same"); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("duplicate outcomes error = %v, want ErrDeclaration", err)
	}
	if _, err := audit.TryReasons(audit.Reason(strings.Repeat("x", audit.MaxNameBytes+1))); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("oversized reason error = %v, want ErrDeclaration", err)
	}
}
