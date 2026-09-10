package audit_test

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
)

type attemptStartFixture struct {
	Target audit.Reference
	Note   string
}

type attemptFinishFixture struct {
	Receipt audit.Reference
	Detail  string
}

type attemptCheckpointFixture struct{}

func attemptOperationFixture(resource audit.Resource) (*audit.EventType[declarationApproval], *audit.OperationType) {
	event := audit.Declare(audit.EventPolicy[declarationApproval]{
		Semantics: audit.Semantics(1, audit.PolicyGolden("operation.event", strings.Repeat("1a", 32))),
		Descriptor: audit.Descriptor{
			Resource: resource, Action: "commerce.order.approved", Owner: "commerce", Purpose: "accountability",
			Retention: "business.long", Consequence: audit.Required,
		},
		Target:     audit.NoEventTarget[declarationApproval](),
		Outcome:    audit.EventOutcome(audit.Outcomes("approved"), func(declarationApproval) audit.Outcome { return "approved" }),
		OccurredAt: audit.EventOccurredAt(func(value declarationApproval) time.Time { return value.At }),
	})
	operation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "commerce.order.approve", Semantics: audit.Semantics(1), Retention: "business.long",
		Consequence: audit.Required, Members: audit.OperationMembers(event),
	})
	return event, operation
}

func attemptSemantics() audit.PolicySemantics {
	return audit.Semantics(1,
		audit.AttemptStartGolden("attempt.start", strings.Repeat("21", 32)),
		audit.AttemptFinishGolden("attempt.succeeded", audit.AttemptSucceededTransition, "", strings.Repeat("22", 32)),
		audit.AttemptFinishGolden("attempt.failed.storage", audit.AttemptFailedTransition, "storage.failed", strings.Repeat("23", 32)),
		audit.AttemptFinishGolden("attempt.cancelled.caller", audit.AttemptCancelledTransition, "caller.cancelled", strings.Repeat("24", 32)),
	)
}

func attemptContext() audit.ContextPolicy {
	return audit.ContextFacts(
		audit.ActorChain(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Internal, audit.AsPlaintext),
		audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Internal, audit.AsPlaintext),
	)
}

func attemptPolicy(operation *audit.OperationType, resource audit.Resource) audit.AttemptPolicy[attemptStartFixture, attemptCheckpointFixture, attemptFinishFixture] {
	return audit.AttemptPolicy[attemptStartFixture, attemptCheckpointFixture, attemptFinishFixture]{
		Operation: operation,
		Semantics: attemptSemantics(),
		Descriptor: audit.AttemptDescriptor{
			Resource: resource, Owner: "commerce", Purpose: "accountability",
			Retention: "business.long", Consequence: audit.Required, Context: attemptContext(),
		},
		MaxOpen:       15 * time.Minute,
		MaxStateBytes: audit.MaxAttemptStateBytes,
		Continuity:    audit.AttemptOwnedBy(audit.AttemptServiceOwner, audit.AttemptEffectiveActorOwner),
		Start:         audit.AttemptStart(audit.AttemptTarget(func(value attemptStartFixture) audit.Reference { return value.Target }, audit.Internal, audit.AsToken), audit.AttemptFields(audit.AttemptValue("note", func(value attemptStartFixture) string { return value.Note }, audit.Text(), audit.Internal))),
		Checkpoints:   audit.NoAttemptCheckpoints[attemptCheckpointFixture](),
		Finish: audit.AttemptFinish(
			audit.AttemptReasons(
				audit.AttemptReasonsFor(audit.AttemptCancelledTransition, audit.Reasons("caller.cancelled")),
				audit.AttemptReasonsFor(audit.AttemptFailedTransition, audit.Reasons("storage.failed")),
			),
			audit.AttemptFinishFields(
				audit.AttemptFieldsFor(audit.AttemptSucceededTransition,
					audit.AttemptProtected("receipt", func(value attemptFinishFixture) audit.Reference { return value.Receipt }, audit.ReferenceText(), audit.Internal),
				),
				audit.AttemptFieldsFor(audit.AttemptFailedTransition,
					audit.AttemptRedacted("detail", func(value attemptFinishFixture) string { return value.Detail }, audit.Text(), audit.Personal),
				),
			),
		),
	}
}

func attemptCatalogSpec(generation audit.CatalogGeneration, previous audit.CatalogRef) audit.CatalogSpec {
	spec := catalogSpec(generation, previous)
	spec.Integrity = audit.RequireSignature(audit.SignatureDescription{
		Algorithm: "hmac-sha256", Profile: "frostgrove.audit.signature.v1", KeyID: "signature-1",
	})
	spec.Tokens = audit.TokenDescription{
		Algorithm: "hmac-sha256", Profile: "frostgrove.audit.token.v1", KeyID: "token-1",
	}
	return spec
}

func TestAttemptDeclarationCompilesCanonicalTypedPolicyIntoManifest(t *testing.T) {
	event, operation := attemptOperationFixture("commerce.order.event")
	policy := attemptPolicy(operation, "commerce.order.attempt")
	attempt, err := audit.TryDeclareAttempt(policy)
	if err != nil {
		t.Fatalf("declare attempt: %v", err)
	}
	description := attempt.Description()
	if description.Kind != audit.AttemptDeclaration || description.Resource != "commerce.order.attempt" || description.Attempt.Operation != "commerce.order.approve" {
		t.Fatalf("attempt identity was not sealed: %+v", description)
	}
	if description.Attempt.Fingerprint == (audit.AttemptPolicyFingerprint{}) || description.Semantics.Fingerprint == (audit.PolicyFingerprint{}) {
		t.Fatal("attempt policy fingerprints are zero")
	}
	if !description.Attempt.Start.TargetPresent || description.Attempt.Start.Target.Mode != audit.AsToken || description.Attempt.MaxCheckpoints != 0 || len(description.Attempt.CheckpointCodes) != 0 {
		t.Fatalf("run-only start/checkpoint declaration is malformed: %+v", description.Attempt)
	}
	if description.Attempt.OpenReserveBytes == 0 || description.Attempt.OpenReserveBytes >= description.Attempt.MaxStateBytes {
		t.Fatalf("terminal reserve is invalid: reserve=%d state=%d", description.Attempt.OpenReserveBytes, description.Attempt.MaxStateBytes)
	}
	if !slices.Equal(description.Attempt.Continuity, []audit.AttemptOwnerFact{audit.AttemptEffectiveActorOwner, audit.AttemptServiceOwner}) {
		t.Fatalf("continuity is not canonical: %v", description.Attempt.Continuity)
	}
	wantTransitions := []audit.AttemptTransitionKind{audit.AttemptSucceededTransition, audit.AttemptFailedTransition, audit.AttemptCancelledTransition}
	if len(description.Attempt.Finish) != len(wantTransitions) {
		t.Fatalf("finish phase count = %d, want %d", len(description.Attempt.Finish), len(wantTransitions))
	}
	for index, transition := range wantTransitions {
		if description.Attempt.Finish[index].Transition != transition {
			t.Fatalf("finish phase %d transition = %v, want %v", index, description.Attempt.Finish[index].Transition, transition)
		}
	}
	if len(description.Attempt.Reasons) != 2 || description.Attempt.Reasons[0].Transition != audit.AttemptFailedTransition || description.Attempt.Reasons[1].Transition != audit.AttemptCancelledTransition {
		t.Fatalf("reason inventories are not canonical: %+v", description.Attempt.Reasons)
	}
	catalog, err := audit.Compile(attemptCatalogSpec(1, audit.CatalogRef{}), attempt, operation, event)
	if err != nil {
		t.Fatalf("compile attempt catalog: %v", err)
	}
	view := catalog.Manifest().View()
	if len(view.Declarations) != 3 || len(view.Codecs) != 2 || len(view.Contexts) != 2 || !view.Integrity.RequiresSignature {
		t.Fatalf("attempt manifest inventory is incomplete: declarations=%d codecs=%d contexts=%d signature=%v", len(view.Declarations), len(view.Codecs), len(view.Contexts), view.Integrity.RequiresSignature)
	}
	compiledReplay := audit.AttemptReplayFingerprint{}
	for _, declaration := range view.Declarations {
		if declaration.Kind == audit.AttemptDeclaration {
			compiledReplay = declaration.Attempt.Replay
		}
	}
	if compiledReplay == (audit.AttemptReplayFingerprint{}) || attempt.Description().Attempt.Replay != (audit.AttemptReplayFingerprint{}) {
		t.Fatalf("compiled replay binding = %x, reusable declaration replay = %x", compiledReplay, attempt.Description().Attempt.Replay)
	}
	firstCanonical := catalog.Manifest().Canonical()
	description.Attempt.Continuity[0] = audit.AttemptWorkloadActorOwner
	description.Attempt.Start.Fields = nil
	description.Attempt.Finish[0].Fields = nil
	description.Attempt.Reasons[0].Codes[0] = "mutated"
	again := attempt.Description()
	if again.Attempt.Continuity[0] != audit.AttemptEffectiveActorOwner || len(again.Attempt.Start.Fields) != 1 || len(again.Attempt.Finish[0].Fields) != 1 || again.Attempt.Reasons[0].Codes[0] != "storage.failed" {
		t.Fatal("returned attempt description aliases sealed declaration state")
	}
	second, err := audit.Compile(attemptCatalogSpec(1, audit.CatalogRef{}), event, attempt, operation)
	if err != nil {
		t.Fatalf("compile reordered attempt catalog: %v", err)
	}
	if catalog.Ref() != second.Ref() || !bytes.Equal(firstCanonical, second.Manifest().Canonical()) {
		t.Fatal("declaration input order changed attempt catalog bytes")
	}
}

func TestAttemptCatalogRequiresExactOperationSignatureAndNestedProviders(t *testing.T) {
	event, operation := attemptOperationFixture("commerce.order.event")
	attempt := audit.DeclareAttempt(attemptPolicy(operation, "commerce.order.attempt"))
	unsigned := attemptCatalogSpec(1, audit.CatalogRef{})
	unsigned.Integrity = audit.IntegrityOnly()
	if _, err := audit.Compile(unsigned, event, operation, attempt); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("unsigned attempt catalog error = %v, want ErrDeclaration", err)
	}
	withoutTokens := attemptCatalogSpec(1, audit.CatalogRef{})
	withoutTokens.Tokens = audit.TokenDescription{}
	if _, err := audit.Compile(withoutTokens, event, operation, attempt); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("attempt target without token provider error = %v, want ErrDeclaration", err)
	}
	withoutProtection := attemptCatalogSpec(1, audit.CatalogRef{})
	withoutProtection.Protection = audit.ProtectionDescription{}
	if _, err := audit.Compile(withoutProtection, event, operation, attempt); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("attempt finish without protection provider error = %v, want ErrDeclaration", err)
	}
	replacementEvent, replacement := attemptOperationFixture("commerce.order.replacement_event")
	if _, err := audit.Compile(attemptCatalogSpec(1, audit.CatalogRef{}), event, replacementEvent, replacement, attempt); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("attempt linked to an absent exact operation error = %v, want ErrDeclaration", err)
	}
	second := audit.DeclareAttempt(attemptPolicy(operation, "commerce.order.retry_attempt"))
	if _, err := audit.Compile(attemptCatalogSpec(1, audit.CatalogRef{}), event, operation, attempt, second); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("two attempts for one operation error = %v, want ErrDeclaration", err)
	}
}

func TestTargetlessAttemptCarriesNoTargetOrTokenProviderRequirement(t *testing.T) {
	event, operation := attemptOperationFixture("commerce.order.event")
	policy := attemptPolicy(operation, "commerce.order.attempt")
	policy.Start = audit.AttemptStart(
		audit.NoAttemptTarget[attemptStartFixture](),
		audit.AttemptFields(audit.AttemptValue("note", func(value attemptStartFixture) string { return value.Note }, audit.Text(), audit.Internal)),
	)
	attempt, err := audit.TryDeclareAttempt(policy)
	if err != nil {
		t.Fatalf("declare targetless attempt: %v", err)
	}
	description := attempt.Description().Attempt.Start
	if description.TargetPresent || description.Target != (audit.SubjectDescription{}) {
		t.Fatalf("targetless attempt contains a target description: %+v", description)
	}
	spec := attemptCatalogSpec(1, audit.CatalogRef{})
	spec.Tokens = audit.TokenDescription{}
	if _, err := audit.Compile(spec, event, operation, attempt); err != nil {
		t.Fatalf("compile targetless attempt without token provider: %v", err)
	}
}

func TestAttemptCatalogReservesEveryTransitionIdentity(t *testing.T) {
	event, operation := attemptOperationFixture("commerce.order.event")
	attempt := audit.DeclareAttempt(attemptPolicy(operation, "commerce.order.attempt"))
	for _, action := range []audit.Action{
		audit.AttemptStartedAction,
		audit.AttemptCheckpointAction,
		audit.AttemptOutcomeUnknownAction,
		audit.AttemptSucceededAction,
		audit.AttemptFailedAction,
		audit.AttemptCancelledAction,
		audit.AttemptAbandonedAction,
	} {
		collision := audit.Declare(audit.EventPolicy[declarationApproval]{
			Semantics: audit.Semantics(1, audit.PolicyGolden("collision.event", strings.Repeat("31", 32))),
			Descriptor: audit.Descriptor{
				Resource: "commerce.order.attempt", Action: action, Owner: "commerce", Purpose: "accountability",
				Retention: "business.long", Consequence: audit.Required,
			},
			Target:     audit.NoEventTarget[declarationApproval](),
			Outcome:    audit.EventOutcome(audit.Outcomes("observed"), func(declarationApproval) audit.Outcome { return "observed" }),
			OccurredAt: audit.EventOccurredAt(func(value declarationApproval) time.Time { return value.At }),
		})
		if _, err := audit.Compile(attemptCatalogSpec(1, audit.CatalogRef{}), event, operation, attempt, collision); !errors.Is(err, audit.ErrDeclaration) {
			t.Fatalf("transition identity %q collision error = %v, want ErrDeclaration", action, err)
		}
	}
}

func TestAttemptDeclarationRejectsUnstableContextIncompleteFinishAndCheckpointPolicy(t *testing.T) {
	_, operation := attemptOperationFixture("commerce.order.event")
	valid := attemptPolicy(operation, "commerce.order.attempt")
	bestEffort := valid
	bestEffort.Descriptor.Consequence = audit.BestEffort
	if _, err := audit.TryDeclareAttempt(bestEffort); err != nil {
		t.Fatalf("declare explicit best-effort attempt: %v", err)
	}
	checks := []struct {
		name   string
		mutate func(*audit.AttemptPolicy[attemptStartFixture, attemptCheckpointFixture, attemptFinishFixture])
	}{
		{"optional operation", func(policy *audit.AttemptPolicy[attemptStartFixture, attemptCheckpointFixture, attemptFinishFixture]) {
			policy.Descriptor.Context = audit.ContextFacts(
				audit.ActorChain(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Internal, audit.AsPlaintext),
				audit.OperationFact(audit.ContextOptional, audit.Provenances(audit.Verified), audit.Internal, audit.AsPlaintext),
			)
		}},
		{"generated operation", func(policy *audit.AttemptPolicy[attemptStartFixture, attemptCheckpointFixture, attemptFinishFixture]) {
			policy.Descriptor.Context = audit.ContextFacts(
				audit.ActorChain(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Internal, audit.AsPlaintext),
				audit.GeneratedOperationFact(audit.Internal, audit.AsToken),
			)
		}},
		{"missing owner context", func(policy *audit.AttemptPolicy[attemptStartFixture, attemptCheckpointFixture, attemptFinishFixture]) {
			policy.Descriptor.Context = audit.ContextFacts(
				audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Internal, audit.AsPlaintext),
			)
		}},
		{"checkpoint count", func(policy *audit.AttemptPolicy[attemptStartFixture, attemptCheckpointFixture, attemptFinishFixture]) {
			policy.MaxCheckpoints = 1
		}},
		{"checkpoint policy", func(policy *audit.AttemptPolicy[attemptStartFixture, attemptCheckpointFixture, attemptFinishFixture]) {
			policy.Checkpoints = audit.AttemptCheckpointPolicy[attemptCheckpointFixture]{}
		}},
		{"state reserve", func(policy *audit.AttemptPolicy[attemptStartFixture, attemptCheckpointFixture, attemptFinishFixture]) {
			policy.MaxStateBytes = 1
		}},
		{"declaration golden", func(policy *audit.AttemptPolicy[attemptStartFixture, attemptCheckpointFixture, attemptFinishFixture]) {
			policy.Semantics = audit.Semantics(1, audit.PolicyGolden("wrong.phase", strings.Repeat("41", 32)))
		}},
		{"missing finish golden", func(policy *audit.AttemptPolicy[attemptStartFixture, attemptCheckpointFixture, attemptFinishFixture]) {
			policy.Semantics = audit.Semantics(1,
				audit.AttemptStartGolden("attempt.start", strings.Repeat("42", 32)),
				audit.AttemptFinishGolden("attempt.succeeded", audit.AttemptSucceededTransition, "", strings.Repeat("43", 32)),
			)
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			policy := valid
			check.mutate(&policy)
			if _, err := audit.TryDeclareAttempt(policy); !errors.Is(err, audit.ErrDeclaration) && !errors.Is(err, audit.ErrTooLarge) {
				t.Fatalf("invalid attempt policy error = %v, want declaration or size refusal", err)
			}
		})
	}
	if _, err := audit.TryAttemptTarget[attemptStartFixture](nil, audit.Internal, audit.AsToken); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("nil target extractor error = %v, want ErrDeclaration", err)
	}
	if _, err := audit.TryAttemptTarget(func(attemptStartFixture) audit.Reference { return "target" }, audit.Internal, audit.AsProtected); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("non-searchable target error = %v, want ErrDeclaration", err)
	}
	if _, err := audit.TryAttemptOwnedBy(audit.AttemptServiceOwner, audit.AttemptServiceOwner); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("duplicate owner fact error = %v, want ErrDeclaration", err)
	}
	if _, err := audit.TryAttemptFieldsFor[attemptFinishFixture](audit.AttemptOutcomeUnknownTransition); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("run-only unknown finish fields error = %v, want ErrDeclaration", err)
	}
	if _, err := audit.TryAttemptReasonsFor(audit.AttemptSucceededTransition, audit.Reasons("unexpected")); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("successful reason inventory error = %v, want ErrDeclaration", err)
	}
}

func TestAttemptCatalogRotationRefusesUntilAttemptActivationGatesExist(t *testing.T) {
	event, operation := attemptOperationFixture("commerce.order.event")
	attempt := audit.DeclareAttempt(attemptPolicy(operation, "commerce.order.attempt"))
	genesis, err := audit.Compile(attemptCatalogSpec(1, audit.CatalogRef{}), event, operation, attempt)
	if err != nil {
		t.Fatalf("compile genesis attempt catalog: %v", err)
	}
	if _, err := audit.Lineage(genesis); err != nil {
		t.Fatalf("single-generation attempt catalog should be usable: %v", err)
	}
	next, err := audit.Compile(attemptCatalogSpec(2, genesis.Ref()), attempt, operation, event)
	if err != nil {
		t.Fatalf("compile next attempt manifest for activation review: %v", err)
	}
	if _, err := audit.Lineage(next, audit.Retain(genesis)); !errors.Is(err, audit.ErrUnsupported) {
		t.Fatalf("attempt catalog rotation error = %v, want ErrUnsupported", err)
	}
	if _, err := audit.CatalogSetDigestOf([]audit.Manifest{genesis.Manifest(), next.Manifest()}); !errors.Is(err, audit.ErrUnsupported) {
		t.Fatalf("attempt catalog set rotation error = %v, want ErrUnsupported", err)
	}
	plainEvent, plainOperation := attemptOperationFixture("commerce.order.plain_event")
	plainGenesis, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), plainEvent, plainOperation)
	if err != nil {
		t.Fatalf("compile ordinary genesis catalog: %v", err)
	}
	plainNext, err := audit.Compile(catalogSpec(2, plainGenesis.Ref()), plainOperation, plainEvent)
	if err != nil {
		t.Fatalf("compile ordinary next catalog: %v", err)
	}
	if _, err := audit.Lineage(plainNext, audit.Retain(plainGenesis)); err != nil {
		t.Fatalf("ordinary catalog rotation was accidentally disabled: %v", err)
	}
}
