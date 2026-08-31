package jobs

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCatalogSortsDefinitionsAndFingerprintsDeterministically(t *testing.T) {
	alpha := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "alpha.job"), Codec: String(1), Policy: testPolicy(t)})
	beta := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "beta.job"), Codec: String(1), Policy: testPolicy(t)})
	left, err := NewCatalog(beta, alpha)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewCatalog(alpha, beta)
	if err != nil {
		t.Fatal(err)
	}
	if left.Fingerprint() != right.Fingerprint() || !strings.HasPrefix(left.Fingerprint(), "sha256:") || len(left.Fingerprint()) != len("sha256:")+64 {
		t.Fatalf("unstable fingerprint: %q, %q", left.Fingerprint(), right.Fingerprint())
	}
	description := left.Describe()
	if description.Fingerprint != left.Fingerprint() || len(description.Definitions) != 2 || description.Definitions[0].Name.String() != "alpha.job" || description.Definitions[1].Name.String() != "beta.job" {
		t.Fatalf("catalog is not canonical: %#v", description)
	}
	if found, ok := left.Lookup(alpha.Name()); !ok || found != alpha || left.Len() != 2 {
		t.Fatal("catalog lookup failed")
	}
}

func TestCatalogIsImmutableAcrossReturnedSlices(t *testing.T) {
	definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "immutable.job"), Codec: String(2), Policy: testPolicy(t)})
	catalog := MustCatalog(definition)
	wantFingerprint := catalog.Fingerprint()
	description := catalog.Describe()
	description.Definitions[0].Codec.SupportedRevisions[0] = 99
	description.Definitions[0].Policy.Overrides = append(description.Definitions[0].Policy.Overrides, "forged")
	description.Definitions = nil
	definitions := catalog.Definitions()
	definitions[0] = nil
	fresh := catalog.Describe()
	if catalog.Fingerprint() != wantFingerprint || len(fresh.Definitions) != 1 || fresh.Definitions[0].Codec.SupportedRevisions[0] != 2 {
		t.Fatalf("catalog state was mutable: %#v", fresh)
	}
}

func TestCatalogRejectsUnresolvedDuplicateNilAndOversizedSets(t *testing.T) {
	definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "duplicate.job"), Codec: String(1), Policy: testPolicy(t)})
	if _, err := NewCatalog(definition, definition); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate accepted: %v", err)
	}
	var nilDefinition *Definition[string]
	if _, err := NewCatalog(nilDefinition); !errors.Is(err, ErrInvalid) {
		t.Fatalf("typed nil accepted: %v", err)
	}
	unresolved := Auto(Handler[string](func(context.Context, string) error { return nil }))
	if _, err := NewCatalog(unresolved); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unresolved Auto accepted: %v", err)
	}
	tooMany := make([]Declaration, MaxDefinitions+1)
	for index := range tooMany {
		tooMany[index] = definition
	}
	if _, err := NewCatalog(tooMany...); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized catalog accepted: %v", err)
	}
}

func TestCatalogFingerprintIgnoresAutomaticConstructionMechanism(t *testing.T) {
	name := testJobName(t, "equivalent.job")
	manual := MustDefine(DefinitionSpec[string]{Name: name, Codec: String(1), Policy: testPolicy(t)})
	automatic := Auto(Handler[string](func(context.Context, string) error { return nil }))
	MustMaterialize(automatic, GeneratedDefinitionSpec[string]{Name: name, Codec: String(1)})
	manualCatalog := MustCatalog(manual)
	automaticCatalog := MustCatalog(automatic)
	if !automatic.Describe().Automatic {
		t.Fatal("automatic provenance disappeared from descriptor")
	}
	if manualCatalog.Fingerprint() != automaticCatalog.Fingerprint() {
		t.Fatalf("construction mechanism changed compatibility: %s != %s", manualCatalog.Fingerprint(), automaticCatalog.Fingerprint())
	}
	profilePolicy := testPolicy(t)
	rawPolicy := profilePolicy
	rawPolicy.profile = ""
	rawPolicy.overrides = 0
	raw := MustCatalog(MustDefine(DefinitionSpec[string]{Name: name, Codec: String(1), Policy: rawPolicy}))
	if raw.Fingerprint() != automaticCatalog.Fingerprint() {
		t.Fatalf("profile provenance changed semantic compatibility: %s != %s", raw.Fingerprint(), automaticCatalog.Fingerprint())
	}
}

func TestCatalogFingerprintIgnoresAutomaticWorkerConcurrency(t *testing.T) {
	leftProfile := Default
	rightProfile := Default
	rightProfile.workerConcurrency++
	handler := Handler[string](func(context.Context, string) error { return nil })
	name := testJobName(t, "equivalent.worker-concurrency")
	left := Auto(handler, leftProfile)
	right := Auto(handler, rightProfile)
	MustMaterialize(left, GeneratedDefinitionSpec[string]{Name: name, Codec: String(1)})
	MustMaterialize(right, GeneratedDefinitionSpec[string]{Name: name, Codec: String(1)})
	if leftProfile.workerConcurrency == rightProfile.workerConcurrency {
		t.Fatal("test profiles have the same worker concurrency")
	}
	if !reflect.DeepEqual(left.Describe(), right.Describe()) {
		t.Fatal("worker concurrency changed the durable definition descriptor")
	}
	leftCatalog := MustCatalog(left)
	rightCatalog := MustCatalog(right)
	if leftCatalog.Fingerprint() != rightCatalog.Fingerprint() {
		t.Fatalf("worker concurrency changed catalog compatibility: %s != %s", leftCatalog.Fingerprint(), rightCatalog.Fingerprint())
	}
}

func TestCatalogFingerprintChangesForProtocolAndPolicyChanges(t *testing.T) {
	name := testJobName(t, "changing.job")
	base := MustCatalog(MustDefine(DefinitionSpec[string]{Name: name, Codec: String(1), Policy: testPolicy(t)})).Fingerprint()
	changedVersion := MustCatalog(MustDefine(DefinitionSpec[string]{Name: name, Codec: String(2), Policy: testPolicy(t)})).Fingerprint()
	changedPolicy := MustCatalog(MustDefine(DefinitionSpec[string]{Name: name, Codec: String(1), Policy: testPolicy(t, Retries(0))})).Fingerprint()
	changedProgress := MustCatalog(MustDefine(DefinitionSpec[string]{Name: name, Codec: String(1), Policy: testPolicy(t, ProgressTimeout(time.Minute))})).Fingerprint()
	changedDeliveryDeferrals := MustCatalog(MustDefine(DefinitionSpec[string]{Name: name, Codec: String(1), Policy: testPolicy(t, MaxDeliveryDeferrals(0))})).Fingerprint()
	changedAckModes := MustCatalog(MustDefine(DefinitionSpec[string]{Name: name, Codec: String(1), Policy: testPolicy(t, AcceptAckModes(AckLocalPersistence))})).Fingerprint()
	changedProtectedFailures := MustCatalog(MustDefine(DefinitionSpec[string]{Name: name, Codec: String(1), Policy: testPolicy(t, ProtectAcknowledgedEnqueuesFrom(FailureProcessCrash))})).Fingerprint()
	tenantCatalog := MustCatalog(MustDefine(DefinitionSpec[string]{Name: name, Codec: String(1), Policy: testPolicy(t), Partition: PartitionTenantRequired}))
	customIdentity := MustCatalog(MustDefine(DefinitionSpec[string]{Name: name, Codec: &changingStringCodec{}, Identity: stringPayloadIdentity{id: builtinCodecID("custom-semantics"), version: 1}, Policy: testPolicy(t)})).Fingerprint()
	explicitBuiltinIdentity := MustCatalog(MustDefine(DefinitionSpec[string]{Name: name, Codec: String(1), Identity: stringPayloadIdentity{id: builtinCodecID("string"), version: 1}, Policy: testPolicy(t)})).Fingerprint()
	if base == changedVersion || base == changedPolicy || base == changedProgress || base == changedDeliveryDeferrals || base == changedAckModes || base == changedProtectedFailures || changedAckModes == changedProtectedFailures || base == tenantCatalog.Fingerprint() || base == customIdentity || base == explicitBuiltinIdentity || changedVersion == changedPolicy || changedVersion == changedProgress || changedVersion == changedDeliveryDeferrals || changedPolicy == changedProgress || changedPolicy == changedDeliveryDeferrals || changedProgress == changedDeliveryDeferrals {
		t.Fatalf("fingerprint missed a contract change: %s %s %s %s %s %s %s %s %s %s", base, changedVersion, changedPolicy, changedProgress, changedDeliveryDeferrals, changedAckModes, changedProtectedFailures, tenantCatalog.Fingerprint(), customIdentity, explicitBuiltinIdentity)
	}
	if !tenantCatalog.RequiresTenantPartition() || tenantCatalog.Describe().Definitions[0].Partition != PartitionTenantRequired {
		t.Fatal("tenant partition requirement disappeared")
	}
}
