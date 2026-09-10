package audit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/audit"
)

const fixtureDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestPolicySemanticsAcceptsTypedCanonicalGoldens(t *testing.T) {
	semantics, err := audit.TrySemantics(2,
		audit.PolicyGolden("resource", fixtureDigest),
		audit.AttemptStartGolden("start", fixtureDigest),
		audit.AttemptCheckpointGolden("checkpoint", "copied", fixtureDigest),
		audit.AttemptFinishGolden("finished", audit.AttemptSucceededTransition, "", fixtureDigest),
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = semantics
}

func TestPolicySemanticsRejectsAmbiguousOrMalformedGoldens(t *testing.T) {
	valid := audit.PolicyGolden("fixture", fixtureDigest)
	if _, err := audit.TrySemantics(0, valid); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("zero version = %v", err)
	}
	if _, err := audit.TrySemantics(1, valid, valid); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("duplicate fixture = %v", err)
	}
	if _, err := audit.TryPolicyGolden("fixture", strings.ToUpper(fixtureDigest)); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("uppercase fingerprint = %v", err)
	}
	if _, err := audit.TryAttemptCheckpointGolden("fixture", "", fixtureDigest); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("empty checkpoint = %v", err)
	}
	if _, err := audit.TryAttemptFinishGolden("fixture", audit.AttemptCheckpointTransition, "", fixtureDigest); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("nonterminal finish = %v", err)
	}
}
