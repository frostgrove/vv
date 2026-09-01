package jobspg

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
)

type captureSender struct {
	description jobs.BackendDescription
	placement   jobs.Placement
}

func (s *captureSender) Description() jobs.BackendDescription { return s.description }

func (s *captureSender) Place(_ context.Context, placement jobs.Placement) (jobs.PlacementResult, error) {
	s.placement = placement
	return jobs.NewPlacementResult(placement.Candidate(), jobs.PlacementCreated)
}

func TestRecordRoundTripPreservesRestorableAttemptLedger(t *testing.T) {
	namespace, catalog, placement := testPlacement(t)
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	insert, err := driver.newPlacement(placement, placement.Candidate(), now)
	if err != nil {
		t.Fatal(err)
	}
	record, err := decodeRecord(insert.record)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := jobs.RestoreDeliveryRecord(catalog, record)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := jobs.NewLeaseRef(driver.Description().ID(), restored.Invocation().ID(), bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := jobs.ParseBindingName("tests.worker")
	build, _ := jobs.ParseBuildID("tests-build")
	command, err := jobs.BeginAttemptCommand(lease, binding, build)
	if err != nil {
		t.Fatal(err)
	}
	application, err := jobs.ApplyDeliveryCommand(restored.Invocation(), command, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := jobs.NewDeliveryRecord(application.Invocation(), restored.Payload(), restored.WireDigest(), restored.PayloadDigest())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeRecord(updated)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeRecord(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := encodeRecord(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatal("record changed during durable round trip")
	}
	if _, err := jobs.RestoreDeliveryRecord(catalog, decoded); err != nil {
		t.Fatal(err)
	}
}

func TestUniqueRecordRoundTripPreservesPlacementModeAndIntent(t *testing.T) {
	namespace, catalog, placement := testPlacementWith(t, jobs.Unique("sweeper"))
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	insert, err := driver.newPlacement(placement, placement.Candidate(), now)
	if err != nil {
		t.Fatal(err)
	}
	record, err := decodeRecord(insert.record)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := jobs.RestoreDeliveryRecord(catalog, record)
	if err != nil {
		t.Fatal(err)
	}
	invocation := restored.Invocation()
	if invocation.Mode() != jobs.PlacementUnique || invocation.Intent().Purpose() != jobs.IntentCollapse || string(restored.Payload().Bytes()) != "payload" {
		t.Fatal("unique placement changed during PostgreSQL record round trip")
	}
}

func TestEncodeRecordAcceptsMaximumPayloadAndRejectsEncodedOverflowBoundary(t *testing.T) {
	escaped, err := json.Marshal(strings.Repeat("<", 1024))
	if err != nil {
		t.Fatal(err)
	}
	if len(escaped) != 2+1024*maximumJSONByteExpansion {
		t.Fatalf("maximum JSON expansion = %d, encoded bytes = %d", maximumJSONByteExpansion, len(escaped))
	}
	if maxEncodedDeliveryRecordBytes != maximumJSONByteExpansion*jobs.MaxDeliveryRecordBytes {
		t.Fatalf("encoded record bound = %d", maxEncodedDeliveryRecordBytes)
	}
	if err := validateEncodedRecordSize(maxEncodedDeliveryRecordBytes); err != nil {
		t.Fatalf("exact encoded record bound was rejected: %v", err)
	}
	if err := validateEncodedRecordSize(maxEncodedDeliveryRecordBytes + 1); !errors.Is(err, jobs.ErrTooLarge) {
		t.Fatalf("encoded record above bound = %v", err)
	}
	namespace, catalog, placement := testMaximumPayloadPlacement(t)
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	insert, err := driver.newPlacement(placement, placement.Candidate(), time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(insert.record) <= jobs.MaxPayloadBytes || len(insert.record) >= maxEncodedDeliveryRecordBytes {
		t.Fatalf("maximum-payload record encoded bytes = %d", len(insert.record))
	}
	record, err := decodeRecord(insert.record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.RestoreDeliveryRecord(catalog, record); err != nil {
		t.Fatal(err)
	}
}

func TestConstructorsSeparateMagicPreparationFromManualWiring(t *testing.T) {
	namespace, catalog, placement := testPlacement(t)
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if driver.Description().IsZero() || driver.Description().Capabilities() != (jobs.Capabilities{Priority: true, Debounce: true, Unique: true, Scheduled: true}) {
		t.Fatal("default PostgreSQL description is incomplete")
	}
	if _, err := driver.Place(context.Background(), placement); !errors.Is(err, ErrNotReady) {
		t.Fatalf("manual driver operated before readiness check: %v", err)
	}
	statements, err := MigrationStatements("")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(statements, "\n")
	for _, required := range []string{`"frostgrove_jobs".schema_meta`, `"frostgrove_jobs".catalogs`, `"frostgrove_jobs".deliveries`, `"frostgrove_jobs".intents`, `record_expires_at`, `intent_expires_at`, `ALTER COLUMN record DROP NOT NULL`, `deliveries_record_pair_check`, `deliveries_retention_deadline_pair_check`} {
		if !strings.Contains(joined, required) {
			t.Fatalf("migration is missing %s", required)
		}
	}
	if strings.Contains(joined, "priority DESC") {
		t.Fatal("PostgreSQL queue priority is not ascending")
	}
	indexAt := strings.Index(joined, `CREATE INDEX CONCURRENTLY IF NOT EXISTS "deliveries_retention_idx"`)
	validationAt := strings.Index(joined, "retention index deliveries_retention_idx schema mismatch")
	commentAt := strings.Index(joined, `COMMENT ON INDEX "frostgrove_jobs"."deliveries_retention_idx"`)
	versionAt := strings.LastIndex(joined, `SET version = 4`)
	if indexAt < 0 || validationAt <= indexAt || commentAt <= validationAt || versionAt <= commentAt {
		t.Fatalf("manual retention migration phases are unordered: index=%d validation=%d comment=%d version=%d", indexAt, validationAt, commentAt, versionAt)
	}
	if _, err := MigrationStatements("Public"); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("unsafe schema accepted: %v", err)
	}
}

func testPlacement(t *testing.T) (jobs.Namespace, jobs.Catalog, jobs.Placement) {
	return testPlacementWith(t, jobs.After(time.Second), jobs.AtPriority(500))
}

func testPlacementWith(t *testing.T, options ...jobs.EnqueueOption) (jobs.Namespace, jobs.Catalog, jobs.Placement) {
	t.Helper()
	name, err := jobs.ParseName("jobspg.roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	definition := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(1), Policy: policy, Partition: jobs.PartitionGlobal})
	catalog := jobs.MustCatalog(definition)
	namespace, err := jobs.NamespaceOf("jobspg-tests", "test")
	if err != nil {
		t.Fatal(err)
	}
	failures, _ := jobs.Failures(jobs.FailureProcessCrash)
	durability, _ := jobs.NewDurabilityProfile(jobs.AckLocalPersistence, jobs.AcknowledgedLossExcludedForDeclaredFailures, failures)
	var backendBytes [jobs.BackendIDBytes]byte
	backendBytes[0] = 1
	backend, _ := jobs.BackendIDFromBytes(backendBytes)
	description, _ := jobs.NewBackendDescription(backend, durability, jobs.Capabilities{Priority: true, Debounce: true, Unique: true, Scheduled: true})
	sender := &captureSender{description: description}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: sender, Entropy: bytes.NewReader(bytes.Repeat([]byte{7}, 16))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Enqueue(context.Background(), queue, definition, "payload", options...); err != nil {
		t.Fatal(err)
	}
	return namespace, catalog, sender.placement
}

func testMaximumPayloadPlacement(t *testing.T) (jobs.Namespace, jobs.Catalog, jobs.Placement) {
	t.Helper()
	name, err := jobs.ParseName("jobspg.maximum-payload")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.With(jobs.MaxBytes(jobs.MaxPayloadBytes), jobs.MaxDecodedPayloadBytes(jobs.MaxPayloadBytes)).Build()
	if err != nil {
		t.Fatal(err)
	}
	definition := jobs.MustDefine(jobs.DefinitionSpec[[]byte]{Name: name, Codec: jobs.Bytes(1), Policy: policy, Partition: jobs.PartitionGlobal})
	catalog := jobs.MustCatalog(definition)
	namespace, err := jobs.NamespaceOf("jobspg-tests", "maximum-payload")
	if err != nil {
		t.Fatal(err)
	}
	failures, _ := jobs.Failures(jobs.FailureProcessCrash)
	durability, _ := jobs.NewDurabilityProfile(jobs.AckLocalPersistence, jobs.AcknowledgedLossExcludedForDeclaredFailures, failures)
	var backendBytes [jobs.BackendIDBytes]byte
	backendBytes[0] = 2
	backend, _ := jobs.BackendIDFromBytes(backendBytes)
	description, _ := jobs.NewBackendDescription(backend, durability, jobs.Capabilities{Priority: true, Debounce: true, Unique: true, Scheduled: true})
	sender := &captureSender{description: description}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: sender, Entropy: bytes.NewReader(bytes.Repeat([]byte{8}, 16))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Enqueue(context.Background(), queue, definition, bytes.Repeat([]byte{0xff}, jobs.MaxPayloadBytes)); err != nil {
		t.Fatal(err)
	}
	return namespace, catalog, sender.placement
}
