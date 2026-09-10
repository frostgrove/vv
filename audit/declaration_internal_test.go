package audit

import (
	"bytes"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
)

type internalDeclarationModel struct {
	ID       int64   `db:"id,pk"`
	Status   string  `db:"status"`
	Note     *string `db:"note"`
	Password string  `db:"password,secret"`
}

type internalLifecycleModel struct {
	ID        int64      `db:"id,pk"`
	Status    string     `db:"status"`
	DeletedAt *time.Time `db:"deleted_at,tombstone"`
}

func internalDeclarationPolicy(t *testing.T) *ResourcePolicy[internalDeclarationModel, int64] {
	t.Helper()
	meta, err := crud.NewMeta[internalDeclarationModel]("internal_declaration_models")
	if err != nil {
		t.Fatalf("construct internal declaration metadata: %v", err)
	}
	return Define(Policy[internalDeclarationModel, int64]{
		Model:     meta,
		Semantics: Semantics(1, PolicyGolden("fixture.one", "0101010101010101010101010101010101010101010101010101010101010101")),
		Descriptor: Descriptor{
			Resource: "test.model", Owner: "test", Purpose: "accountability",
			Retention: "test.long", Consequence: Required,
		},
		Subject: PlaintextSubject(func(id int64) string { return referenceTextValue(id) }, Internal),
		Actions: Actions(EntityCreated, EntityChanged, EntityHardDeleted),
		Fields: Fields[internalDeclarationModel](
			Reconstruct[internalDeclarationModel]("Status", "status", Text(), Internal),
			Optional[internalDeclarationModel]("Note", "note", Text(), Internal),
			Redacted[internalDeclarationModel]("Password", "password_changed", Text(), Secret),
		),
	})
}

func referenceTextValue(id int64) string {
	if id == 1 {
		return "model-1"
	}
	return "model-other"
}

func TestEntityDraftSeparatesFullValuesFromHonestChanges(t *testing.T) {
	policy := internalDeclarationPolicy(t)
	note := "present"
	created, err := policy.Created(&internalDeclarationModel{ID: 1, Status: "pending", Note: &note, Password: "secret"})
	if err != nil {
		t.Fatalf("capture created draft: %v", err)
	}
	if created.value.state != EntityFullState || len(created.value.values) != 1 || len(created.value.changes) != 3 {
		t.Fatalf("created draft shape: state=%v values=%d changes=%d", created.value.state, len(created.value.values), len(created.value.changes))
	}
	if created.value.values[0].field != "status" || created.value.values[0].state != ValuePresent {
		t.Fatalf("created reconstructable value = %+v", created.value.values[0])
	}
	before := &internalDeclarationModel{ID: 1, Status: "pending", Note: &note, Password: "secret"}
	after := &internalDeclarationModel{ID: 1, Status: "approved", Note: &note, Password: "secret"}
	delta, changed, err := policy.Changed(before, after)
	if err != nil || !changed {
		t.Fatalf("capture delta: changed=%v err=%v", changed, err)
	}
	if delta.value.state != EntityDeltaState || len(delta.value.values) != 0 || len(delta.value.changes) != 1 || delta.value.changes[0].field != "status" {
		t.Fatalf("delta draft shape: state=%v values=%d changes=%+v", delta.value.state, len(delta.value.values), delta.value.changes)
	}
	baseline, changed, err := policy.BaselineChanged(before, after)
	if err != nil || !changed {
		t.Fatalf("capture baseline: changed=%v err=%v", changed, err)
	}
	if baseline.value.state != EntityFullState || len(baseline.value.values) != 1 || len(baseline.value.changes) != 1 {
		t.Fatalf("baseline draft shape: state=%v values=%d changes=%d", baseline.value.state, len(baseline.value.values), len(baseline.value.changes))
	}
}

func TestTargetlessEventDraftCarriesNoSyntheticCoordinate(t *testing.T) {
	type occurrence struct {
		At time.Time
	}
	event := Declare(EventPolicy[occurrence]{
		Semantics: Semantics(1, PolicyGolden("fixture.one", "0101010101010101010101010101010101010101010101010101010101010101")),
		Descriptor: Descriptor{
			Resource: "test.event", Action: "test.event.happened", Owner: "test", Purpose: "accountability",
			Retention: "test.long", Consequence: Required,
		},
		Target:     NoEventTarget[occurrence](),
		Outcome:    EventOutcome(Outcomes("accepted"), func(occurrence) Outcome { return "accepted" }),
		OccurredAt: EventOccurredAt(func(value occurrence) time.Time { return value.At }),
	})
	draft, err := event.New(occurrence{At: time.Date(2026, 1, 2, 3, 4, 5, 6, time.FixedZone("offset", 7200))})
	if err != nil {
		t.Fatalf("build targetless event draft: %v", err)
	}
	if draft.value.targetPresent || draft.value.target != "" || !draft.value.occurredAt.Equal(draft.value.occurredAt.UTC()) || draft.value.occurredAt.Location() != time.UTC {
		t.Fatalf("targetless event carried a coordinate or noncanonical time: %+v", draft.value)
	}
}

func TestRedactedValuesRemainPrivateSemanticInputs(t *testing.T) {
	type occurrence struct {
		Secret string
		At     time.Time
	}
	event := Declare(EventPolicy[occurrence]{
		Semantics: Semantics(1, PolicyGolden("fixture.one", "0101010101010101010101010101010101010101010101010101010101010101")),
		Descriptor: Descriptor{
			Resource: "test.redacted", Action: "test.redacted.recorded", Owner: "test", Purpose: "accountability",
			Retention: "test.long", Consequence: Required,
		},
		Target:     NoEventTarget[occurrence](),
		Outcome:    EventOutcome(Outcomes("accepted"), func(occurrence) Outcome { return "accepted" }),
		OccurredAt: EventOccurredAt(func(value occurrence) time.Time { return value.At }),
		Fields: EventFields(
			EventRedacted("secret", func(value occurrence) string { return value.Secret }, Text(), Secret),
		),
	})
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	left, err := event.New(occurrence{Secret: "alpha", At: at})
	if err != nil {
		t.Fatalf("build first redacted draft: %v", err)
	}
	right, err := event.New(occurrence{Secret: "bravo", At: at})
	if err != nil {
		t.Fatalf("build second redacted draft: %v", err)
	}
	if len(left.value.values) != 1 || left.value.values[0].state != ValueRedacted || len(left.value.values[0].canonical) == 0 {
		t.Fatalf("redacted value lost its private semantic input: %+v", left.value.values)
	}
	leftSemantic := semanticDraftBytes("test.record", OperationID{}, Context{}, ContextPolicy{}, []draft{left.value})
	rightSemantic := semanticDraftBytes("test.record", OperationID{}, Context{}, ContextPolicy{}, []draft{right.value})
	if bytes.Equal(leftSemantic, rightSemantic) {
		t.Fatal("distinct redacted values produced the same semantic input")
	}
}

func TestCatalogSetAcceptsOnlyItsExactActiveDeclarationPointers(t *testing.T) {
	left := internalDeclarationPolicy(t)
	right := internalDeclarationPolicy(t)
	if left.Description().Semantics.Fingerprint != right.Description().Semantics.Fingerprint {
		t.Fatal("membership control declarations are not semantically identical")
	}
	spec := CatalogSpec{
		ID: "test.audit", Owner: "test", Generation: 1,
		Retention:  RetentionRules(KeepForever("test.long")),
		Semantics:  SemanticDigestDescription{Algorithm: "hmac-sha256", Profile: "audit.semantic.v1", KeyID: "semantic-1"},
		Identities: IdentityCommitmentDescription{Algorithm: "hmac-sha256", Profile: "audit.identity.v1", KeyID: "identity-1"},
		Protection: ProtectionDescription{Algorithm: "aes-gcm", Profile: "audit.protection.v1", KeyID: "protect-1"},
		Integrity:  IntegrityOnly(),
	}
	catalog, err := Compile(spec, left)
	if err != nil {
		t.Fatalf("compile pointer-membership catalog: %v", err)
	}
	lineage, err := Lineage(catalog)
	if err != nil {
		t.Fatalf("construct pointer-membership lineage: %v", err)
	}
	if !lineage.accepts(left) {
		t.Fatal("catalog set rejected its exact active declaration")
	}
	if lineage.accepts(right) {
		t.Fatal("catalog set accepted a separately compiled identical declaration")
	}
}

func TestEntityLifecycleDraftsCaptureAbsentTransitions(t *testing.T) {
	meta, err := crud.NewMeta[internalLifecycleModel]("internal_lifecycle_models")
	if err != nil {
		t.Fatalf("construct lifecycle metadata: %v", err)
	}
	policy := Define(Policy[internalLifecycleModel, int64]{
		Model:     meta,
		Semantics: Semantics(1, PolicyGolden("fixture.one", "0101010101010101010101010101010101010101010101010101010101010101")),
		Descriptor: Descriptor{
			Resource: "test.lifecycle", Owner: "test", Purpose: "accountability",
			Retention: "test.long", Consequence: Required,
		},
		Subject: PlaintextSubject(func(id int64) string { return referenceTextValue(id) }, Internal),
		Actions: Actions(EntitySoftDeleted, EntityRestored),
		Fields: Fields[internalLifecycleModel](
			Reconstruct[internalLifecycleModel]("Status", "status", Text(), Internal),
		),
	})
	model := &internalLifecycleModel{ID: 1, Status: "active"}
	deleted, err := policy.Deleted(model)
	if err != nil {
		t.Fatalf("capture soft delete: %v", err)
	}
	if len(deleted.value.changes) != 1 || deleted.value.changes[0].before.state != ValuePresent || deleted.value.changes[0].after.state != ValueAbsent ||
		len(deleted.value.values) != 1 || deleted.value.values[0].state != ValueAbsent {
		t.Fatalf("soft delete transition = %+v", deleted.value)
	}
	restored, err := policy.Restored(model)
	if err != nil {
		t.Fatalf("capture restore: %v", err)
	}
	if len(restored.value.changes) != 1 || restored.value.changes[0].before.state != ValueAbsent || restored.value.changes[0].after.state != ValuePresent ||
		len(restored.value.values) != 1 || restored.value.values[0].state != ValuePresent {
		t.Fatalf("restore transition = %+v", restored.value)
	}
}
