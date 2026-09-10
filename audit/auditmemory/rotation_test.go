package auditmemory

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/audit/internal/auditcrudbridge"
	"github.com/frostgrove/vv/crud"
)

type rotationEntity struct {
	ID   int64  `db:"id,pk"`
	Name string `db:"name"`
}

func TestEntityChainContinuesAcrossCatalogAndIdentityKeyRotation(t *testing.T) {
	ctx := context.Background()
	meta, err := crud.NewMeta[rotationEntity]("rotation_entities")
	if err != nil {
		t.Fatal(err)
	}
	operationContext := audit.ContextFacts(audit.GeneratedOperationFact(audit.Public, audit.AsPlaintext))
	resource := audit.Define(audit.Policy[rotationEntity, int64]{
		Model: meta, Semantics: audit.Semantics(1, audit.PolicyGolden("rotation.entity", strings.Repeat("01", 32))),
		Descriptor: audit.Descriptor{
			Resource: "rotation.entity", Owner: "rotation.team", Purpose: "business.audit",
			Retention: "business.forever", Consequence: audit.Required, Context: operationContext,
		},
		Subject: audit.PlaintextSubject(func(id int64) string { return "entity-1" }, audit.Public),
		Actions: audit.Actions(audit.EntityCreated, audit.EntityChanged),
		Fields:  audit.Fields[rotationEntity](audit.Reconstruct[rotationEntity]("Name", "name", audit.Text(), audit.Public)),
	})
	operation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "rotation.entity.write", Semantics: audit.Semantics(1), Retention: "business.forever",
		Consequence: audit.Required, Context: operationContext,
		Members: audit.OperationMembers(resource.Action(audit.EntityCreated), resource.Action(audit.EntityChanged)),
	})
	semantic, err := audit.HMACSemanticDigester("rotation-semantic", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	firstIdentities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{KeyID: "identity-old", Key: bytes.Repeat([]byte{2}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	rotatedIdentities, err := audit.HMACIdentityKeyring(
		audit.HMACIdentityKey{KeyID: "identity-old", Key: bytes.Repeat([]byte{2}, 32)},
		audit.HMACIdentityKey{KeyID: "identity-new", Key: bytes.Repeat([]byte{3}, 32), Active: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	protection, err := audit.AESGCMProtection(audit.AESGCMKey{KeyID: "protect-1", Key: bytes.Repeat([]byte{4}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	spec := audit.CatalogSpec{
		ID: "rotation.audit", Owner: "rotation.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: firstIdentities.ActiveDescription(),
		Protection: protection.Description(),
		Integrity:  audit.IntegrityOnly(),
	}
	first, err := audit.Compile(spec, resource, operation)
	if err != nil {
		t.Fatalf("compile first: %#v", err)
	}
	spec.Generation = 2
	spec.Previous = first.Ref()
	spec.Identities = rotatedIdentities.ActiveDescription()
	second, err := audit.Compile(spec, operation, resource)
	if err != nil {
		t.Fatal(err)
	}
	firstLineage, err := audit.Lineage(first)
	if err != nil {
		t.Fatal(err)
	}
	secondLineage, err := audit.Lineage(second, audit.Retain(first))
	if err != nil {
		t.Fatal(err)
	}
	log, err := NewLog(LogSpec{})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := NewDeployment(log)
	if err != nil {
		t.Fatal(err)
	}
	change, err := audit.NewCatalogChangeRef("rotation.deploy", "identity-rotation")
	if err != nil {
		t.Fatal(err)
	}
	if err := deployment.InstallAndActivate(ctx, firstLineage, change); err != nil {
		t.Fatal(err)
	}
	store, err := New(Spec{Log: log})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := audit.StaticContext(audit.Context{})
	if err != nil {
		t.Fatal(err)
	}
	firstRecorder, err := audit.New(audit.Config{
		Catalogs: firstLineage, Writer: store, Context: resolver, Semantics: semantic, Identities: firstIdentities, Protector: protection,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := resource.Created(&rotationEntity{ID: 1, Name: "before"})
	if err != nil {
		t.Fatal(err)
	}
	recordEntityMutation(t, ctx, store, firstRecorder, resource, audit.EntityCreated, created)
	if err := deployment.InstallCatalog(ctx, second.Manifest(), change); err != nil {
		t.Fatal(err)
	}
	proof, err := audit.NoAttemptCatalogActivation(first.Ref(), second.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if err := deployment.ActivateCatalog(ctx, first.Ref(), second.Ref(), change, proof); err != nil {
		t.Fatal(err)
	}
	secondRecorder, err := audit.New(audit.Config{
		Catalogs: secondLineage, Writer: store, Context: resolver, Semantics: semantic, Identities: rotatedIdentities, Protector: protection,
	})
	if err != nil {
		t.Fatal(err)
	}
	changed, ok, err := resource.Changed(&rotationEntity{ID: 1, Name: "before"}, &rotationEntity{ID: 1, Name: "after"})
	if err != nil || !ok {
		t.Fatalf("changed draft = %v, %v", ok, err)
	}
	recordEntityMutation(t, ctx, store, secondRecorder, resource, audit.EntityChanged, changed)
	var createItem, changeItem audit.ItemWireView
	for _, record := range log.value.revisions {
		item := record.revision.View().Items[0]
		switch item.Action {
		case audit.Action(audit.EntityCreated):
			createItem = item
		case audit.Action(audit.EntityChanged):
			changeItem = item
		}
	}
	if createItem.Chain == (audit.EntityChainID{}) || changeItem.Chain != createItem.Chain || changeItem.Previous != createItem.Leaf {
		t.Fatalf("rotated chain = create(%x/%x) change(%x/%x)", createItem.Chain, createItem.Leaf, changeItem.Chain, changeItem.Previous)
	}
}

func recordEntityMutation(t *testing.T, ctx context.Context, store *Store, recorder *audit.Recorder, resource *audit.ResourcePolicy[rotationEntity, int64], action audit.EntityAction, draft audit.EntityDraft) {
	t.Helper()
	subject, err := resource.SubjectRef(1)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := auditcrudbridge.NewSpec(resource, resource.Action(action), []any{subject}, false)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := auditcrudbridge.Preflight(ctx, recorder, spec)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	txContext := WithTransaction(ctx, tx)
	if err := guard.Run(txContext, func(context.Context) (auditcrudbridge.Batch, error) {
		return auditcrudbridge.NewBatch(draft)
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
