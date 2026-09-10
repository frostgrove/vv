package audit_test

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
)

func catalogDeclarations(t *testing.T) (*audit.ResourcePolicy[declarationOrder, int64], *audit.EventType[declarationApproval], *audit.OperationType) {
	t.Helper()
	resource := audit.Define(audit.Policy[declarationOrder, int64]{
		Model: declarationMeta(t, "catalog_orders"), Semantics: declarationPolicySemantics(),
		Descriptor: declarationDescriptor("commerce.order", ""),
		Subject:    audit.PlaintextSubject(func(id int64) string { return "order" }, audit.Internal),
		Actions:    audit.Actions(audit.EntityCreated, audit.EntityChanged),
		Fields: audit.Fields[declarationOrder](
			audit.Reconstruct[declarationOrder]("Status", "status", audit.Text(), audit.Internal),
		),
	})
	event := audit.Declare(audit.EventPolicy[declarationApproval]{
		Semantics:  declarationPolicySemantics(),
		Descriptor: declarationDescriptor("commerce.order", "commerce.order.approved"),
		Target:     audit.NoEventTarget[declarationApproval](),
		Outcome:    audit.EventOutcome(audit.Outcomes("approved"), func(declarationApproval) audit.Outcome { return "approved" }),
		OccurredAt: audit.EventOccurredAt(func(value declarationApproval) time.Time { return value.At }),
	})
	operation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "commerce.order.approve", Semantics: audit.Semantics(1), Retention: "business.long",
		Consequence: audit.Required, Members: audit.OperationMembers(resource.Action(audit.EntityChanged), event),
	})
	return resource, event, operation
}

func catalogSpec(generation audit.CatalogGeneration, previous audit.CatalogRef) audit.CatalogSpec {
	return audit.CatalogSpec{
		ID: "commerce.audit", Owner: "commerce", Generation: generation, Previous: previous,
		Retention:  audit.RetentionRules(audit.KeepFor("business.long", audit.CalendarPeriod{Years: 7})),
		Semantics:  audit.SemanticDigestDescription{Algorithm: "hmac-sha256", Profile: "frostgrove.audit.semantic.v1", KeyID: "semantic-1"},
		Identities: audit.IdentityCommitmentDescription{Algorithm: "hmac-sha256", Profile: "frostgrove.audit.identity.v1", KeyID: "identity-1"},
		Protection: audit.ProtectionDescription{Algorithm: "aes-gcm", Profile: "frostgrove.audit.protection.v1", KeyID: "protect-1"},
		Integrity:  audit.IntegrityOnly(),
	}
}

func TestCatalogCompileIsOrderIndependentImmutableAndUsable(t *testing.T) {
	resource, event, operation := catalogDeclarations(t)
	first, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), operation, resource, event)
	if err != nil {
		t.Fatalf("compile first catalog: %v", err)
	}
	second, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), event, operation, resource)
	if err != nil {
		t.Fatalf("compile reordered catalog: %v", err)
	}
	if first.Ref() != second.Ref() || !bytes.Equal(first.Manifest().Canonical(), second.Manifest().Canonical()) {
		t.Fatal("declaration order changed the canonical catalog")
	}
	manifest := first.Manifest()
	view := manifest.View()
	if len(view.Declarations) != 3 || len(view.Codecs) != 1 || len(manifest.Canonical()) == 0 {
		t.Fatalf("manifest inventory is incomplete: declarations=%d codecs=%d bytes=%d", len(view.Declarations), len(view.Codecs), len(manifest.Canonical()))
	}
	canonical := manifest.Canonical()
	canonical[0] ^= 0xff
	view.Declarations[0].Fields = nil
	view.Codecs[0].ReadVersions[0] = 99
	again := first.Manifest().View()
	if len(again.Declarations[0].Fields) == 0 || again.Codecs[0].ReadVersions[0] != 1 || bytes.Equal(canonical, first.Manifest().Canonical()) {
		t.Fatal("a returned manifest view or byte slice aliases the catalog")
	}
}

func TestCatalogLineageRequiresTheCompleteExactChain(t *testing.T) {
	resource, event, operation := catalogDeclarations(t)
	genesis, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), resource, event, operation)
	if err != nil {
		t.Fatalf("compile genesis: %v", err)
	}
	active, err := audit.Compile(catalogSpec(2, genesis.Ref()), operation, event, resource)
	if err != nil {
		t.Fatalf("compile active generation: %v", err)
	}
	lineage, err := audit.Lineage(active, audit.Retain(genesis))
	if err != nil {
		t.Fatalf("construct catalog lineage: %v", err)
	}
	if lineage.Active() != active.Ref() || lineage.Digest() == (audit.CatalogSetDigest{}) || len(lineage.Manifests()) != 2 {
		t.Fatalf("lineage identity is incomplete: active=%+v manifests=%d", lineage.Active(), len(lineage.Manifests()))
	}
	digest, err := audit.CatalogSetDigestOf(lineage.Manifests())
	if err != nil || digest != lineage.Digest() {
		t.Fatalf("public catalog digest disagrees with Lineage: digest=%x err=%v", digest, err)
	}
	if found, ok := lineage.Manifest(genesis.Ref()); !ok || found.Ref() != genesis.Ref() {
		t.Fatal("exact retained manifest lookup failed")
	}
	if _, err := audit.Lineage(active); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("incomplete lineage error = %v, want ErrDeclaration", err)
	}
}

func TestCatalogRejectsMissingMembersProvidersAndTypedNilDeclarations(t *testing.T) {
	resource, event, operation := catalogDeclarations(t)
	if _, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), event, operation); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("operation with a missing exact member error = %v, want ErrDeclaration", err)
	}
	withoutProtection := catalogSpec(1, audit.CatalogRef{})
	withoutProtection.Protection = audit.ProtectionDescription{}
	if _, err := audit.Compile(withoutProtection, resource, event, operation); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("protected field without provider error = %v, want ErrDeclaration", err)
	}
	tokenSubject := audit.Define(audit.Policy[declarationOrder, int64]{
		Model: declarationMeta(t, "catalog_token_orders"), Semantics: declarationPolicySemantics(),
		Descriptor: declarationDescriptor("commerce.token_order", ""),
		Subject:    audit.TokenizedSubject(func(id int64) string { return "order" }, audit.Internal),
		Actions:    audit.Actions(audit.EntityCreated),
		Fields:     audit.Fields[declarationOrder](audit.Value[declarationOrder]("Status", "status", audit.Text(), audit.Internal)),
	})
	if _, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), tokenSubject); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("tokenized subject without provider error = %v, want ErrDeclaration", err)
	}
	if _, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), resource, resource); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("duplicate declaration error = %v, want ErrDeclaration", err)
	}
	var missing *audit.EventType[declarationApproval]
	if _, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), resource, missing); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("typed-nil declaration error = %v, want ErrDeclaration", err)
	}
}

func TestControlPolicyIsSealedIntoTheManifestWithoutAHiddenResourceRegistration(t *testing.T) {
	resource, event, operation := catalogDeclarations(t)
	spec := catalogSpec(1, audit.CatalogRef{})
	spec.Control = audit.ControlPolicy{
		Resource: "audit.control", Semantics: audit.Semantics(1), Purpose: "accountability",
		Retention: "business.long", Consequence: audit.Required,
		Actions: audit.ControlActions(audit.ControlDenied),
		Reasons: audit.ControlReasons(audit.ReasonsFor(audit.ControlDenied, audit.Reasons("policy.denied"))),
	}
	catalog, err := audit.Compile(spec, resource, event, operation)
	if err != nil {
		t.Fatalf("compile catalog with control policy: %v", err)
	}
	control := catalog.Manifest().View().Control
	if control.Resource != "audit.control" || len(control.Actions) != 1 || len(control.Reasons) != 1 || control.Semantics.Fingerprint == (audit.PolicyFingerprint{}) {
		t.Fatalf("control policy manifest is incomplete: %+v", control)
	}
}

func TestCatalogReadsAreConcurrentAndReturnOwnedCopies(t *testing.T) {
	resource, event, operation := catalogDeclarations(t)
	catalog, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), resource, event, operation)
	if err != nil {
		t.Fatalf("compile concurrent-read catalog: %v", err)
	}
	lineage, err := audit.Lineage(catalog)
	if err != nil {
		t.Fatalf("construct concurrent-read lineage: %v", err)
	}
	want := catalog.Ref()
	var wait sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 100 {
				view := catalog.Manifest().View()
				view.Declarations[0].Semantics.Fixtures = nil
				canonical := catalog.Manifest().Canonical()
				canonical[0] ^= 1
				manifests := lineage.Manifests()
				manifests[0] = audit.Manifest{}
				if catalog.Ref() != want || lineage.Active() != want {
					t.Errorf("concurrent read changed catalog identity")
					return
				}
			}
		}()
	}
	wait.Wait()
}

func TestControlHoldPolicyRequiresProtectedMatterAndSearchableScope(t *testing.T) {
	resource, event, operation := catalogDeclarations(t)
	spec := catalogSpec(1, audit.CatalogRef{})
	spec.Tokens = audit.TokenDescription{Algorithm: "hmac-sha256", Profile: "frostgrove.audit.token.v1", KeyID: "token-1"}
	spec.Control = audit.ControlPolicy{
		Resource: "audit.control", Semantics: audit.Semantics(1), Purpose: "accountability",
		Retention: "business.long", Consequence: audit.Required,
		Context: audit.ContextFacts(
			audit.ScopeFact(audit.ContextRequired, audit.Provenances(audit.ServerDerived), audit.Internal, audit.AsToken),
		),
		Matter:  audit.HoldMatter(audit.ReferenceText(), audit.Secret, audit.AsProtected),
		Actions: audit.ControlActions(audit.HistoryRead, audit.HoldPlaced, audit.HoldReleased),
	}
	if _, err := audit.Compile(spec, resource, event, operation); err != nil {
		t.Fatalf("compile scoped hold control policy: %v", err)
	}
	withoutScope := spec
	withoutScope.Control.Context = audit.ContextPolicy{}
	if _, err := audit.Compile(withoutScope, resource, event, operation); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("hold control without scope error = %v, want ErrDeclaration", err)
	}
	if _, err := audit.TryHoldMatter(audit.ReferenceText(), audit.Secret, audit.AsPlaintext); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("plaintext hold matter error = %v, want ErrDeclaration", err)
	}
}

func TestHistoricalReconstructRequiresAnAuthenticatedCompatibleAncestor(t *testing.T) {
	policy := func(version audit.PolicyVersion, field audit.EntityField[declarationOrder]) *audit.ResourcePolicy[declarationOrder, int64] {
		return audit.Define(audit.Policy[declarationOrder, int64]{
			Model:      declarationMeta(t, "historical_orders"),
			Semantics:  audit.Semantics(version, audit.PolicyGolden("fixture.one", "0101010101010101010101010101010101010101010101010101010101010101")),
			Descriptor: declarationDescriptor("commerce.historical_order", ""),
			Subject:    audit.PlaintextSubject(func(int64) string { return "historical-order" }, audit.Internal),
			Actions:    audit.Actions(audit.EntityChanged), Fields: audit.Fields(field),
		})
	}
	live := policy(1, audit.Reconstruct[declarationOrder]("Status", "status", audit.Text(), audit.Internal))
	genesis, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), live)
	if err != nil {
		t.Fatalf("compile live reconstruction generation: %v", err)
	}
	historical := policy(2, audit.HistoricalReconstruct[declarationOrder, string]("status", audit.Text(), audit.Internal))
	active, err := audit.Compile(catalogSpec(2, genesis.Ref()), historical)
	if err != nil {
		t.Fatalf("compile historical reconstruction generation: %v", err)
	}
	if _, err := audit.Lineage(active, audit.Retain(genesis)); err != nil {
		t.Fatalf("compatible authenticated historical field was refused: %v", err)
	}
	if _, err := audit.Compile(catalogSpec(1, audit.CatalogRef{}), historical); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("genesis historical field error = %v, want ErrDeclaration", err)
	}
	orphan := policy(2, audit.HistoricalReconstruct[declarationOrder, string]("orphan", audit.Text(), audit.Internal))
	orphanCatalog, err := audit.Compile(catalogSpec(2, genesis.Ref()), orphan)
	if err != nil {
		t.Fatalf("compile orphan candidate: %v", err)
	}
	if _, err := audit.Lineage(orphanCatalog, audit.Retain(genesis)); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("orphan historical field error = %v, want ErrDeclaration", err)
	}
}
