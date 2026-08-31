package jobs

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestPolicySnapshotCopiesDurableExecutionPolicy(t *testing.T) {
	policy := testQueuePolicy(t)
	snapshot, err := NewPolicySnapshot(policy)
	if err != nil {
		t.Fatal(err)
	}
	policy.Priority++
	policy.AttemptTimeout++
	policy.ProgressTimeout = time.Minute
	policy.MaxElapsed++
	policy.MaxRetries++
	policy.MaxHandlerDeferrals++
	policy.MaxDeliveryDeferrals++
	policy.Backoff.Initial++
	policy.Retention++
	policy.IntentRetention++
	policy.Payload.MaxBytes++
	if snapshot.Queue().Value() != "default" || snapshot.Priority() != 100 || snapshot.AttemptTimeout() != DefaultAttemptTimeout || snapshot.ProgressTimeout() != 0 || snapshot.MaxElapsed() != DefaultMaxElapsed {
		t.Fatalf("snapshot scalar policy changed: %+v", snapshot)
	}
	if snapshot.RetryLimit().Value() != DefaultRetries || snapshot.HandlerDeferralLimit().Value() != DefaultHandlerDeferrals || snapshot.DeliveryDeferralLimit().Value() != DefaultDeliveryDeferrals {
		t.Fatalf("snapshot limits changed: %+v", snapshot)
	}
	if snapshot.Backoff() != Exponential(DefaultRetryDelay, DefaultMaxRetryDelay, FullJitter) || snapshot.Retention() != DefaultTerminalRetention || snapshot.IntentRetention() != DefaultIntentRetention || snapshot.Payload() != DefaultPayloadLimit() {
		t.Fatalf("snapshot durable policy changed: %+v", snapshot)
	}
	if got := fmt.Sprintf("%+v", snapshot); got != "[job policy snapshot]" {
		t.Fatalf("format = %q", got)
	}
}

func TestPlacementCopiesPayloadAndRejectsDigestOrQueueMismatch(t *testing.T) {
	definition := testQueueDefinition(t, "tests.place", String(SchemaVersion(1)))
	policy, err := NewPolicySnapshot(definition.Policy())
	if err != nil {
		t.Fatal(err)
	}
	payload, payloadDigest, err := definition.preparePayload("secret payload", true)
	if err != nil {
		t.Fatal(err)
	}
	id := queueTestInvocationID(t, 9)
	namespace := queueTestNamespace(t, "tests")
	partition := partitionKey(namespace, ProducerPartition{})
	durable := mustTestDurableContext(t, namespace, partition, definition.Name(), policy.Trace())
	intents, err := digestRegularIntents(CurrentIntentDigestPlan(), namespace, partition, definition.Name(), id)
	if err != nil {
		t.Fatal(err)
	}
	spec := PlacementSpec{
		Namespace:     namespace,
		Partition:     partition,
		Candidate:     id,
		Definition:    definition.Name(),
		Queue:         policy.Queue(),
		Mode:          PlacementRegular,
		Payload:       payload,
		PayloadDigest: payloadDigest,
		WireDigest:    digestWirePayload(payload),
		IntentDigests: intents,
		Priority:      policy.Priority(),
		Policy:        policy,
		Context:       durable,
	}
	placement, err := NewPlacement(spec)
	if err != nil {
		t.Fatal(err)
	}
	first := placement.Payload().Bytes()
	first[0] = 'X'
	if got := string(placement.Payload().Bytes()); got != "secret payload" {
		t.Fatalf("payload = %q", got)
	}
	original := payload.Bytes()
	original[0] = 'Y'
	if got := string(placement.Payload().Bytes()); got != "secret payload" {
		t.Fatalf("payload after source mutation = %q", got)
	}
	badDigest := spec
	badDigest.WireDigest = WireDigest{value: [32]byte{1}}
	if _, err := NewPlacement(badDigest); err == nil {
		t.Fatal("mismatched payload digest accepted")
	}
	badQueue := spec
	badQueue.Queue = queueMustQueueName("other")
	if _, err := NewPlacement(badQueue); err == nil {
		t.Fatal("policy queue mismatch accepted")
	}
	badNamespace := spec
	badNamespace.Namespace = queueTestNamespace(t, "other")
	if _, err := NewPlacement(badNamespace); !errors.Is(err, ErrInvalid) {
		t.Fatalf("partition namespace mismatch = %v", err)
	}
	badDefinition := spec
	badDefinition.Definition = queueMustName("tests.other")
	if _, err := NewPlacement(badDefinition); !errors.Is(err, ErrInvalid) {
		t.Fatalf("intent definition mismatch = %v", err)
	}
	badIntent := spec
	var raw [IntentDigestBytes]byte
	raw[0] = 9
	digest, _ := IntentDigestFromBytes(raw)
	key, _ := NewIntentKey(spec.IntentDigests.Current().Scope(), spec.IntentDigests.Current().Revision(), IntentRegular, digest)
	badIntent.IntentDigests, _ = NewIntentDigests(key)
	if _, err := NewPlacement(badIntent); !errors.Is(err, ErrInvalid) {
		t.Fatalf("regular intent mismatch = %v", err)
	}
	legacyOnRegular := spec
	legacyOnRegular.LegacyIntent = protectLegacyIntent(Intent("legacy-key"))
	if _, err := NewPlacement(legacyOnRegular); !errors.Is(err, ErrInvalid) {
		t.Fatalf("legacy regular intent = %v", err)
	}
	legacySpec := spec
	legacySpec.Mode = PlacementOnce
	legacySpec.LegacyIntent = protectLegacyIntent(Intent("legacy-key"))
	legacySpec.IntentDigests, _ = digestProducerIntents(CurrentIntentDigestPlan(), namespace, partition, definition.Name(), IntentOnce, Intent("legacy-key"))
	legacyPlacement, err := NewPlacement(legacySpec)
	if err != nil {
		t.Fatal(err)
	}
	if legacy, ok := legacyPlacement.LegacyIntent(); !ok || legacy.Value() != "legacy-key" {
		t.Fatalf("legacy placement intent = %+v", legacy)
	}
	legacySpec.LegacyIntent = protectLegacyIntent(Intent("other-key"))
	if _, err := NewPlacement(legacySpec); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched legacy intent = %v", err)
	}
	rolling, err := NewIntentDigestPlan(DigestRevision2, DigestRevision1)
	if err != nil {
		t.Fatal(err)
	}
	rollingDigests, err := digestProducerIntents(rolling, namespace, partition, definition.Name(), IntentOnce, Intent("legacy-key"))
	if err != nil {
		t.Fatal(err)
	}
	aliases := rollingDigests.ReadCandidates()
	var tamperedRaw [IntentDigestBytes]byte
	tamperedRaw[0] = 11
	tamperedDigest, _ := IntentDigestFromBytes(tamperedRaw)
	tamperedAlias, _ := NewIntentKey(aliases[1].Scope(), aliases[1].Revision(), aliases[1].Purpose(), tamperedDigest)
	tamperedAliases, _ := NewIntentDigests(aliases[0], tamperedAlias)
	legacySpec.LegacyIntent = protectLegacyIntent(Intent("legacy-key"))
	legacySpec.IntentDigests = tamperedAliases
	if _, err := NewPlacement(legacySpec); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered compatibility alias = %v", err)
	}
	if got := fmt.Sprintf("%+v", placement); got != "[job placement]" {
		t.Fatalf("format = %q", got)
	}
	if bytes.Contains([]byte(fmt.Sprint(placement)), []byte("secret")) {
		t.Fatal("placement formatting exposed payload")
	}
	if formatted := fmt.Sprintf("%+v", spec); formatted != "[job placement spec]" || bytes.Contains([]byte(formatted), []byte("secret")) {
		t.Fatalf("placement spec format = %q", formatted)
	}
}

func TestPlacementDigestsAreDomainSeparatedAndLengthPrefixed(t *testing.T) {
	namespace := queueTestNamespace(t, "tests")
	otherNamespace := queueTestNamespace(t, "other")
	partition := partitionKey(namespace, ProducerPartition{})
	otherPartition := partitionKey(namespace, Partition("tenant"))
	leftDefinition := queueMustName("tests.left")
	rightDefinition := queueMustName("tests.right")
	intent := Intent("a:b")
	left, _ := digestProducerIntents(CurrentIntentDigestPlan(), namespace, partition, leftDefinition, IntentOnce, intent)
	right, _ := digestProducerIntents(CurrentIntentDigestPlan(), namespace, partition, rightDefinition, IntentOnce, intent)
	if left.Current() == right.Current() {
		t.Fatal("definition did not scope intent")
	}
	otherScope, _ := digestProducerIntents(CurrentIntentDigestPlan(), otherNamespace, partitionKey(otherNamespace, ProducerPartition{}), leftDefinition, IntentOnce, intent)
	if left.Current() == otherScope.Current() {
		t.Fatal("namespace did not scope intent")
	}
	otherIntent, _ := digestProducerIntents(CurrentIntentDigestPlan(), namespace, partition, leftDefinition, IntentOnce, Intent("a"))
	if left.Current() == otherIntent.Current() {
		t.Fatal("raw intent did not affect digest")
	}
	otherTenant, _ := digestProducerIntents(CurrentIntentDigestPlan(), namespace, otherPartition, leftDefinition, IntentOnce, intent)
	if left.Current() == otherTenant.Current() {
		t.Fatal("tenant partition did not scope intent")
	}
	rolling, _ := NewIntentDigestPlan(DigestRevision2, DigestRevision1)
	aliases, _ := digestProducerIntents(rolling, namespace, partition, leftDefinition, IntentOnce, intent)
	if len(aliases.ReadCandidates()) != 2 || aliases.Current().Revision() != DigestRevision2 || aliases.ReadCandidates()[1].Revision() != DigestRevision1 {
		t.Fatal("rolling digest aliases are incomplete")
	}
	payloadOne, err := NewEncodedPayload(queueMustCodecID("string"), SchemaVersion(1), []byte("value"))
	if err != nil {
		t.Fatal(err)
	}
	payloadTwo, err := NewEncodedPayload(queueMustCodecID("string"), SchemaVersion(2), []byte("value"))
	if err != nil {
		t.Fatal(err)
	}
	if digestWirePayload(payloadOne) == digestWirePayload(payloadTwo) {
		t.Fatal("schema version did not affect payload digest")
	}
	if digestWirePayload(payloadOne).String() != "[job wire digest]" || fmt.Sprintf("%x", digestWirePayload(payloadOne)) != "[job wire digest]" {
		t.Fatal("payload digest formatting is not redacted")
	}
}

func testQueuePolicy(t *testing.T) Policy {
	t.Helper()
	policy, err := Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func testQueueDefinition[P any](t *testing.T, rawName string, codec Codec[P]) *Definition[P] {
	t.Helper()
	definition, err := Define(DefinitionSpec[P]{Name: queueMustName(rawName), Codec: codec, Policy: testQueuePolicy(t)})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func queueTestInvocationID(t *testing.T, value byte) InvocationID {
	t.Helper()
	var raw [InvocationIDBytes]byte
	raw[0] = value
	id, err := InvocationIDFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func queueMustName(raw string) Name {
	name, err := ParseName(raw)
	if err != nil {
		panic(err)
	}
	return name
}

func queueMustQueueName(raw string) QueueName {
	name, err := ParseQueueName(raw)
	if err != nil {
		panic(err)
	}
	return name
}

func queueMustCodecID(raw string) CodecID {
	id, err := ParseCodecID(raw)
	if err != nil {
		panic(err)
	}
	return id
}
