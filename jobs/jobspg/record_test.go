package jobspg

import (
	"bytes"
	"context"
	"database/sql"
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
	meta, err := jobs.NewDeliveryMeta(jobs.DeliveryMetaSpec{
		Invocation:      application.Invocation().ID(),
		Definition:      application.Invocation().Definition(),
		Binding:         binding,
		Build:           build,
		Attempt:         application.Invocation().AttemptOrdinal(),
		CreatedAt:       application.Invocation().CreatedAt(),
		EligibleAt:      application.Invocation().EligibleAt(),
		StartedAt:       now.Add(time.Second),
		AttemptDeadline: application.Invocation().Attempts()[0].Deadline(),
		MaxElapsedAt:    application.Invocation().MaxElapsedAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	matched, err := matchesFence(catalog, encoded, meta)
	if err != nil || !matched {
		t.Fatalf("current attempt fence matched=%t err=%v", matched, err)
	}
	otherBuild, _ := jobs.ParseBuildID("other-build")
	stale, err := jobs.NewDeliveryMeta(jobs.DeliveryMetaSpec{
		Invocation:      meta.InvocationID(),
		Definition:      meta.Definition(),
		Binding:         meta.Binding(),
		Build:           otherBuild,
		Attempt:         meta.AttemptOrdinal(),
		CreatedAt:       meta.CreatedAt(),
		EligibleAt:      meta.EligibleAt(),
		StartedAt:       meta.StartedAt(),
		AttemptDeadline: meta.AttemptDeadline(),
		MaxElapsedAt:    meta.MaxElapsedAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	matched, err = matchesFence(catalog, encoded, stale)
	if err != nil || matched {
		t.Fatalf("stale attempt fence matched=%t err=%v", matched, err)
	}
}

func TestConstructorsSeparateMagicPreparationFromManualWiring(t *testing.T) {
	namespace, catalog, placement := testPlacement(t)
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if driver.Description().IsZero() || driver.Description().Capabilities() != (jobs.Capabilities{Priority: true, Debounce: true, Scheduled: true}) {
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
	for _, required := range []string{`"frostgrove_jobs".schema_meta`, `"frostgrove_jobs".catalogs`, `"frostgrove_jobs".deliveries`, `"frostgrove_jobs".intents`} {
		if !strings.Contains(joined, required) {
			t.Fatalf("migration is missing %s", required)
		}
	}
	if strings.Contains(joined, "priority DESC") {
		t.Fatal("PostgreSQL queue priority is not ascending")
	}
	if _, err := MigrationStatements("Public"); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("unsafe schema accepted: %v", err)
	}
}

func testPlacement(t *testing.T) (jobs.Namespace, jobs.Catalog, jobs.Placement) {
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
	description, _ := jobs.NewBackendDescription(backend, durability, jobs.Capabilities{Priority: true, Debounce: true, Scheduled: true})
	sender := &captureSender{description: description}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: sender, Entropy: bytes.NewReader(bytes.Repeat([]byte{7}, 16))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Enqueue(context.Background(), queue, definition, "payload", jobs.After(time.Second), jobs.AtPriority(500)); err != nil {
		t.Fatal(err)
	}
	return namespace, catalog, sender.placement
}
