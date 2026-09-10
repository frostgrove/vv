package audit_test

import (
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
)

func TestOperationDeclarationSealsExactCompatibleMembers(t *testing.T) {
	resource, event, _ := catalogDeclarations(t)
	members := audit.OperationMembers(resource.Action(audit.EntityChanged), event)
	operation, err := audit.TryDeclareOperation(audit.OperationPolicy{
		Name: "commerce.order.review", Semantics: audit.Semantics(1), Retention: "business.long",
		Consequence: audit.Required, Members: members,
	})
	if err != nil {
		t.Fatalf("declare operation: %v", err)
	}
	members[0] = nil
	description := operation.Description()
	if len(description.Members) != 2 || description.Members[0] >= description.Members[1] {
		t.Fatalf("operation members are not canonical and copied: %v", description.Members)
	}
	description.Members[0] = "mutated"
	if operation.Description().Members[0] == "mutated" {
		t.Fatal("operation description aliases its sealed member inventory")
	}
	history := &audit.History{}
	if resource.History(history) == nil || event.History(history) == nil || operation.History(history) == nil {
		t.Fatal("typed history binding did not preserve a valid declaration")
	}
}

func TestOperationDeclarationRejectsDuplicateOrIncompatibleMembers(t *testing.T) {
	resource, event, _ := catalogDeclarations(t)
	duplicate := audit.OperationPolicy{
		Name: "commerce.order.duplicate", Semantics: audit.Semantics(1), Retention: "business.long",
		Consequence: audit.Required, Members: audit.OperationMembers(event, event),
	}
	if _, err := audit.TryDeclareOperation(duplicate); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("duplicate operation member error = %v, want ErrDeclaration", err)
	}
	bestEffort := audit.Declare(audit.EventPolicy[declarationApproval]{
		Semantics: declarationPolicySemantics(),
		Descriptor: audit.Descriptor{
			Resource: "commerce.order", Action: "commerce.order.notified", Owner: "commerce", Purpose: "diagnostics",
			Retention: "business.long", Consequence: audit.BestEffort,
		},
		Target:     audit.NoEventTarget[declarationApproval](),
		Outcome:    audit.EventOutcome(audit.Outcomes("sent"), func(declarationApproval) audit.Outcome { return "sent" }),
		OccurredAt: audit.EventOccurredAt(func(declarationApproval) time.Time { return time.Now() }),
	})
	incompatible := audit.OperationPolicy{
		Name: "commerce.order.incompatible", Semantics: audit.Semantics(1), Retention: "business.long",
		Consequence: audit.Required, Members: audit.OperationMembers(resource.Action(audit.EntityChanged), bestEffort),
	}
	if _, err := audit.TryDeclareOperation(incompatible); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("incompatible operation member error = %v, want ErrDeclaration", err)
	}
}
