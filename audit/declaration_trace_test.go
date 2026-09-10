package audit_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
)

func traceResource(t *testing.T, field audit.FieldName) *audit.ResourcePolicy[declarationOrder, int64] {
	t.Helper()
	return audit.Define(audit.Policy[declarationOrder, int64]{
		Model: declarationMeta(t, "trace_orders"), Semantics: declarationPolicySemantics(),
		Descriptor: declarationDescriptor("trace.order", ""),
		Subject:    audit.PlaintextSubject(func(int64) string { return "trace-order" }, audit.Internal),
		Actions:    audit.Actions(audit.EntityCreated, audit.EntityChanged),
		Fields: audit.Fields[declarationOrder](
			audit.Reconstruct[declarationOrder]("Status", field, audit.Text(), audit.Internal),
		),
	})
}

func traceEvent(action audit.Action, fieldCalls *int) *audit.EventType[declarationApproval] {
	return audit.Declare(audit.EventPolicy[declarationApproval]{
		Semantics:  declarationPolicySemantics(),
		Descriptor: declarationDescriptor("trace.order", action),
		Target:     audit.NoEventTarget[declarationApproval](),
		Outcome:    audit.EventOutcome(audit.Outcomes("accepted"), func(value declarationApproval) audit.Outcome { return value.Outcome }),
		OccurredAt: audit.EventOccurredAt(func(value declarationApproval) time.Time { return value.At }),
		Fields: audit.EventFields(
			audit.EventValue("rule", func(value declarationApproval) string {
				if fieldCalls != nil {
					(*fieldCalls)++
				}
				return value.Rule
			}, audit.Text(), audit.Internal),
		),
	})
}

func TestAT001S1Contract(t *testing.T) {
	resource := traceResource(t, "status")
	genesis, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), resource)
	if err != nil {
		t.Fatalf("compile S1 contract genesis: %v", err)
	}
	active, err := audit.Compile(catalogSpec(2, genesis.Ref()), resource)
	if err != nil {
		t.Fatalf("compile S1 contract active catalog: %v", err)
	}
	lineage, err := audit.Lineage(active, audit.Retain(genesis))
	if err != nil {
		t.Fatalf("construct S1 contract lineage: %v", err)
	}
	if lineage.Active() != active.Ref() || lineage.Digest() == (audit.CatalogSetDigest{}) || len(lineage.Manifests()) != 2 {
		t.Fatalf("S1 catalog contract is incomplete: active=%+v manifests=%d", lineage.Active(), len(lineage.Manifests()))
	}
}

func TestAT002S1Contract(t *testing.T) {
	resource := traceResource(t, "status")
	if _, err := resource.Created(&declarationOrder{ID: 1, Status: "pending"}); err != nil {
		t.Fatalf("capture S1 resource draft: %v", err)
	}
	calls := 0
	event := traceEvent("trace.order.accepted", &calls)
	if _, err := event.New(declarationApproval{Outcome: "accepted", Rule: "default", At: time.Now()}); err != nil {
		t.Fatalf("capture S1 event draft: %v", err)
	}
	if calls != 1 || resource.Description().Semantics.Fingerprint == (audit.PolicyFingerprint{}) || event.Description().Semantics.Fingerprint == (audit.PolicyFingerprint{}) {
		t.Fatalf("S1 declaration contract lost extraction or semantics: calls=%d", calls)
	}
}

func TestAT001KillsCatalogLineageMutant(t *testing.T) {
	resource := traceResource(t, "status")
	genesis, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), resource)
	if err != nil {
		t.Fatalf("compile lineage control: %v", err)
	}
	third := catalogSpec(3, genesis.Ref())
	if _, err := audit.Compile(third, resource); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("skipped generation error = %v, want ErrDeclaration", err)
	}
	active, err := audit.Compile(catalogSpec(2, genesis.Ref()), resource)
	if err != nil {
		t.Fatalf("compile direct child: %v", err)
	}
	if _, err := audit.Lineage(active); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("missing genesis error = %v, want ErrDeclaration", err)
	}
}

func TestAT001PreservesCatalogLineageMutant(t *testing.T) {
	resource := traceResource(t, "status")
	genesis, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), resource)
	if err != nil {
		t.Fatalf("compile valid genesis: %v", err)
	}
	active, err := audit.Compile(catalogSpec(2, genesis.Ref()), resource)
	if err != nil {
		t.Fatalf("compile valid direct child: %v", err)
	}
	lineage, err := audit.Lineage(active, audit.Retain(genesis))
	if err != nil || len(lineage.Manifests()) != 2 {
		t.Fatalf("valid lineage was not preserved: manifests=%d err=%v", len(lineage.Manifests()), err)
	}
}

func TestAT002KillsUndeclaredCodeMutant(t *testing.T) {
	fieldCalls := 0
	event := traceEvent("trace.order.code_refusal", &fieldCalls)
	_, err := event.New(declarationApproval{Outcome: "undeclared", Rule: "must-not-run", At: time.Now()})
	if !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("undeclared outcome error = %v, want ErrInvalid", err)
	}
	if fieldCalls != 0 {
		t.Fatalf("field extractor ran %d times after code refusal", fieldCalls)
	}
}

func TestAT002PreservesUndeclaredCodeMutant(t *testing.T) {
	fieldCalls := 0
	event := traceEvent("trace.order.code_acceptance", &fieldCalls)
	if _, err := event.New(declarationApproval{Outcome: "accepted", Rule: "runs", At: time.Now()}); err != nil {
		t.Fatalf("declared outcome was refused: %v", err)
	}
	if fieldCalls != 1 {
		t.Fatalf("declared event field ran %d times, want once", fieldCalls)
	}
}

func TestAT002KillsSingleEvaluationMutant(t *testing.T) {
	calls := map[string]int{}
	event := audit.Declare(audit.EventPolicy[declarationApproval]{
		Semantics:  declarationPolicySemantics(),
		Descriptor: declarationDescriptor("trace.order", "trace.order.single"),
		Target: audit.EventTarget(func(declarationApproval) audit.Reference {
			calls["target"]++
			return "trace-order"
		}, audit.Internal, audit.AsToken),
		Outcome: audit.EventOutcome(audit.Outcomes("accepted"), func(declarationApproval) audit.Outcome {
			calls["outcome"]++
			return "accepted"
		}),
		Reason: audit.EventReason(audit.Reasons("policy.allowed"), func(declarationApproval) audit.Reason {
			calls["reason"]++
			return "policy.allowed"
		}),
		OccurredAt: audit.EventOccurredAt(func(declarationApproval) time.Time {
			calls["time"]++
			return time.Now()
		}),
		Fields: audit.EventFields(
			audit.EventValue("rule", func(declarationApproval) string {
				calls["field"]++
				return "default"
			}, audit.Text(), audit.Internal),
		),
	})
	if _, err := event.New(declarationApproval{}); err != nil {
		t.Fatalf("capture single-evaluation event: %v", err)
	}
	for _, name := range []string{"target", "outcome", "reason", "time", "field"} {
		if calls[name] != 1 {
			t.Fatalf("%s extractor ran %d times, want once", name, calls[name])
		}
	}
}

func TestAT002PreservesSingleEvaluationMutant(t *testing.T) {
	targetCalls := 0
	event := audit.Declare(audit.EventPolicy[declarationApproval]{
		Semantics:  declarationPolicySemantics(),
		Descriptor: declarationDescriptor("trace.order", "trace.order.targetless"),
		Target:     audit.NoEventTarget[declarationApproval](),
		Outcome:    audit.EventOutcome(audit.Outcomes("accepted"), func(declarationApproval) audit.Outcome { return "accepted" }),
		OccurredAt: audit.EventOccurredAt(func(declarationApproval) time.Time { return time.Now() }),
		Fields: audit.EventFields(
			audit.EventValue("rule", func(declarationApproval) string {
				targetCalls++
				return "default"
			}, audit.Text(), audit.Internal),
		),
	})
	if _, err := event.New(declarationApproval{}); err != nil || targetCalls != 1 {
		t.Fatalf("targetless positive control failed: field calls=%d err=%v", targetCalls, err)
	}
}

func TestAT002KillsHoldMatterPolicyMutant(t *testing.T) {
	if _, err := audit.TryHoldMatter(audit.ReferenceText(), audit.Secret, audit.AsPlaintext); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("plaintext hold matter error = %v, want ErrDeclaration", err)
	}
	resource := traceResource(t, "status")
	spec := catalogSpec(1, audit.CatalogRef{})
	spec.Control = audit.ControlPolicy{
		Resource: "audit.control", Semantics: audit.Semantics(1), Purpose: "accountability",
		Retention: "business.long", Consequence: audit.Required,
		Matter:  audit.HoldMatter(audit.ReferenceText(), audit.Secret, audit.AsProtected),
		Actions: audit.ControlActions(audit.HoldPlaced),
	}
	if _, err := audit.Compile(spec, resource); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("unscoped hold control error = %v, want ErrDeclaration", err)
	}
}

func TestAT002PreservesHoldMatterPolicyMutant(t *testing.T) {
	resource := traceResource(t, "status")
	spec := catalogSpec(1, audit.CatalogRef{})
	spec.Tokens = audit.TokenDescription{Algorithm: "hmac-sha256", Profile: "frostgrove.audit.token.v1", KeyID: "token-1"}
	spec.Control = audit.ControlPolicy{
		Resource: "audit.control", Semantics: audit.Semantics(1), Purpose: "accountability",
		Retention: "business.long", Consequence: audit.Required,
		Context: audit.ContextFacts(
			audit.ScopeFact(audit.ContextRequired, audit.Provenances(audit.ServerDerived), audit.Internal, audit.AsToken),
		),
		Matter:  audit.HoldMatter(audit.ReferenceText(), audit.Secret, audit.AsProtected),
		Actions: audit.ControlActions(audit.HoldPlaced),
	}
	catalog, err := audit.Compile(spec, resource)
	if err != nil || catalog.Manifest().View().Control.Matter.Mode != audit.AsProtected {
		t.Fatalf("valid hold matter policy was not preserved: err=%v", err)
	}
}

func TestAT001KillsSemanticReuseMutant(t *testing.T) {
	status := traceResource(t, "status")
	renamed := traceResource(t, "status.renamed")
	if status.Description().Semantics.Fingerprint == renamed.Description().Semantics.Fingerprint {
		t.Fatal("changing a declared field identity reused the policy fingerprint")
	}
	first, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), status)
	if err != nil {
		t.Fatalf("compile first semantic catalog: %v", err)
	}
	second, err := audit.Compile(catalogSpec(2, first.Ref()), renamed)
	if err != nil {
		t.Fatalf("compile changed semantic catalog: %v", err)
	}
	if first.Ref().Digest == second.Ref().Digest {
		t.Fatal("changing a declared field identity reused the catalog digest")
	}
	if _, err := audit.Lineage(second, audit.Retain(first)); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("same-version changed policy lineage error = %v, want ErrDeclaration", err)
	}
}

func TestAT001PreservesSemanticReuseMutant(t *testing.T) {
	left := traceResource(t, "status")
	right := traceResource(t, "status")
	if left == right {
		t.Fatal("semantic positive control reused a declaration pointer")
	}
	if left.Description().Semantics.Fingerprint != right.Description().Semantics.Fingerprint {
		t.Fatal("identical declarations with distinct callbacks changed the policy fingerprint")
	}
	first, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), left)
	if err != nil {
		t.Fatalf("compile first identical declaration: %v", err)
	}
	second, err := audit.Compile(catalogSpec(2, first.Ref()), right)
	if err != nil {
		t.Fatalf("compile second identical declaration: %v", err)
	}
	lineage, err := audit.Lineage(second, audit.Retain(first))
	if err != nil {
		t.Fatalf("identical same-version declaration lineage was refused: %v", err)
	}
	if lineage.Active() != second.Ref() || bytes.Equal(first.Manifest().Canonical(), second.Manifest().Canonical()) {
		t.Fatal("distinct declaration pointers did not preserve a valid direct-child lineage")
	}
}
