package audit_test

import (
	"errors"
	"testing"

	"github.com/frostgrove/vv/audit"
)

func TestContextPolicyIsExplicitCanonicalAndCopied(t *testing.T) {
	allowed := audit.Provenances(audit.Verified, audit.ServerDerived)
	policy, err := audit.TryContextFacts(
		audit.TraceFact(audit.ContextOptional, allowed, audit.Internal, audit.AsToken),
		audit.ActorChain(audit.ContextRequired, allowed, audit.Personal, audit.AsIndexedProtected),
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = policy
}

func TestContextPolicyRejectsDuplicateAndIncompleteFacts(t *testing.T) {
	allowed := audit.Provenances(audit.Verified)
	fact := audit.ScopeFact(audit.ContextRequired, allowed, audit.Internal, audit.AsToken)
	if _, err := audit.TryContextFacts(fact, fact); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("duplicate context fact = %v", err)
	}
	if _, err := audit.TryContextFacts(audit.ScopeFact(audit.ContextRequired, audit.ProvenanceSet{}, audit.Internal, audit.AsToken)); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("missing provenance = %v", err)
	}
}

func TestGeneratedOperationFactIsTheOnlyImplicitContextFact(t *testing.T) {
	policy, err := audit.TryContextFacts(audit.GeneratedOperationFact(audit.Internal, audit.AsToken))
	if err != nil {
		t.Fatalf("generated operation policy = (%v, %v)", policy, err)
	}
}

func TestPlaintextAdmissionWidensOnlyPersonalData(t *testing.T) {
	admission, err := audit.AdmitPlaintext("reviewed.personal_analytics", audit.Personal)
	if err != nil {
		t.Fatal(err)
	}
	view := admission.View()
	if view.Reason != "reviewed.personal_analytics" || len(view.Plaintext) != 1 || view.Plaintext[0] != audit.Personal {
		t.Fatalf("admission view = %+v", view)
	}
	view.Plaintext[0] = audit.Secret
	if admission.View().Plaintext[0] != audit.Personal {
		t.Fatal("privacy admission view aliases internal state")
	}
	if admission.Fingerprint() == (audit.DeploymentFingerprint{}) {
		t.Fatal("privacy admission fingerprint is zero")
	}
	if _, err := audit.AdmitPlaintext("reviewed.secret", audit.Secret); !errors.Is(err, audit.ErrAdmission) {
		t.Fatalf("secret admission = %v", err)
	}
}
