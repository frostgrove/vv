//go:build integration

package jobspg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresVerticalSlice(t *testing.T) {
	dsn := os.Getenv("FROSTGROVE_JOBSPG_TEST_DSN")
	if dsn == "" {
		t.Skip("FROSTGROVE_JOBSPG_TEST_DSN is not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("jobspg_it_%d", time.Now().UnixNano())
	namespace, err := jobs.NamespaceOf("jobspg-integration", schema)
	if err != nil {
		t.Fatal(err)
	}
	repo := newRepository(DefaultSchema)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := repo.deleteNamespace(cleanupCtx, db, namespace); err != nil {
			t.Errorf("delete test namespace: %v", err)
		}
	})
	success := postgresTestDefinition(t, "jobspg.success")
	retry := postgresTestDefinition(t, "jobspg.retry", jobs.Retries(1), jobs.RetryBackoff(jobs.Exponential(jobs.MinRetryDelay, jobs.MinRetryDelay, jobs.NoJitter)))
	staged := postgresTestDefinition(t, "jobspg.staged")
	recovery := postgresTestDefinition(t, "jobspg.recovery")
	baseCatalog := jobs.MustCatalog(success)
	if _, err := Open(ctx, db, namespace, baseCatalog); err != nil {
		t.Fatal(err)
	}
	catalog := jobs.MustCatalog(success, retry, staged, recovery)
	driver, err := Open(ctx, db, namespace, catalog)
	if err != nil {
		t.Fatalf("additive catalog open: %v", err)
	}
	changedPolicy, err := jobs.Default.With(jobs.Retries(0)).Build()
	if err != nil {
		t.Fatal(err)
	}
	changedSuccess := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: success.Name(), Codec: jobs.String(1), Policy: changedPolicy, Partition: jobs.PartitionGlobal})
	if _, err := Open(ctx, db, namespace, jobs.MustCatalog(changedSuccess)); !errors.Is(err, ErrCatalogMismatch) {
		t.Fatalf("changed definition open = %v", err)
	}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Enqueue(ctx, queue, success, "success"); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Enqueue(ctx, queue, retry, "retry"); err != nil {
		t.Fatal(err)
	}
	rollbackTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	rollbackStager, err := driver.Stager(rollbackTx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.EnqueueIn(ctx, queue, rollbackStager, staged, "rollback"); err != nil {
		t.Fatal(err)
	}
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	commitTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	commitStager, err := driver.Stager(commitTx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.EnqueueIn(ctx, queue, commitStager, staged, "commit"); err != nil {
		t.Fatal(err)
	}
	if err := commitTx.Commit(); err != nil {
		t.Fatal(err)
	}
	events := make(chan string, 4)
	var retryAttempts atomic.Int32
	consumers := []jobs.Consumer{
		jobs.On(success, jobs.Handler[string](func(_ context.Context, payload string) error {
			events <- payload
			return nil
		}), jobs.Concurrency(1)),
		jobs.On(retry, jobs.Handler[string](func(_ context.Context, payload string) error {
			if retryAttempts.Add(1) == 1 {
				return errors.New("retry once")
			}
			events <- payload
			return nil
		}), jobs.Concurrency(1)),
		jobs.On(staged, jobs.Handler[string](func(_ context.Context, payload string) error {
			events <- payload
			return nil
		}), jobs.Concurrency(1)),
	}
	workers := postgresTestWorkers(t, namespace, catalog, driver, consumers...)
	runCtx, stopRun := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- workers.Run(runCtx) }()
	want := map[string]bool{"success": false, "retry": false, "commit": false}
	for range want {
		select {
		case event := <-events:
			if _, exists := want[event]; !exists || want[event] {
				t.Fatalf("unexpected worker event %q", event)
			}
			want[event] = true
		case err := <-runResult:
			t.Fatalf("workers stopped before handling jobs: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if retryAttempts.Load() != 2 {
		t.Fatalf("retry attempts = %d", retryAttempts.Load())
	}
	select {
	case event := <-events:
		t.Fatalf("rolled back job was handled: %q", event)
	case <-time.After(300 * time.Millisecond):
	}
	drainCtx, stopDrain := context.WithTimeout(context.Background(), 5*time.Second)
	if err := workers.Drain(drainCtx); err != nil {
		t.Fatal(err)
	}
	stopDrain()
	stopRun()
	select {
	case err := <-runResult:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("workers did not stop")
	}
	recoveryID, err := jobs.Enqueue(ctx, queue, recovery, "recovery")
	if err != nil {
		t.Fatal(err)
	}
	target := postgresTestClaimTarget(t, recovery, "jobspg.recovery.worker")
	firstIncarnation := postgresTestIncarnation(t, 1)
	claim, err := jobs.NewClaimRequest(jobs.ClaimRequestSpec{
		Namespace: namespace, Incarnation: firstIncarnation, Targets: []jobs.ClaimTarget{target}, MaxItems: 1, MaxBytes: jobs.MaxDeliveryRecordBytes, LeaseTTL: jobs.MinimumLeaseTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := driver.Claim(ctx, claim)
	if err != nil || claimed.Len() != 1 {
		t.Fatalf("claim len=%d err=%v", claimed.Len(), err)
	}
	time.Sleep(jobs.MinimumLeaseTTL + 100*time.Millisecond)
	recoverRequest, err := jobs.NewRecoverRequest(jobs.RecoverRequestSpec{
		Namespace: namespace, Incarnation: postgresTestIncarnation(t, 2), MaxItems: 1, MaxBytes: jobs.MaxDeliveryRecordBytes, LeaseTTL: jobs.MinimumLeaseTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := driver.Recover(ctx, recoverRequest)
	if err != nil || len(recovered.Items()) != 1 {
		t.Fatalf("recover len=%d err=%v", len(recovered.Items()), err)
	}
	if recovered.Items()[0].Record().Genesis.ID != recoveryID {
		t.Fatal("recovery returned another invocation")
	}
}

func postgresTestDefinition(t *testing.T, raw string, options ...jobs.Option) *jobs.Definition[string] {
	t.Helper()
	name, err := jobs.ParseName(raw)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.With(options...).Build()
	if err != nil {
		t.Fatal(err)
	}
	return jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(1), Policy: policy, Partition: jobs.PartitionGlobal})
}

func postgresTestWorkers(t *testing.T, namespace jobs.Namespace, catalog jobs.Catalog, driver *Driver, consumers ...jobs.Consumer) *jobs.Workers {
	t.Helper()
	build, err := jobs.ParseBuildID("test:jobspg")
	if err != nil {
		t.Fatal(err)
	}
	restorer := jobs.TrustedIdentityRestorerFunc(func(ctx context.Context, _ jobs.IdentityRestoreRequest) (jobs.RestoredIdentity, error) {
		return jobs.NewRestoredIdentity(ctx, jobs.ProducerPartition{}, jobs.ProducerActor{})
	})
	workers, err := jobs.NewWorkers(jobs.WorkersSpec{
		Namespace: namespace, Catalog: catalog, Driver: driver, Build: build, Identity: restorer, PollInterval: jobs.MinimumPollInterval,
	}, consumers...)
	if err != nil {
		t.Fatal(err)
	}
	return workers
}

func postgresTestClaimTarget(t *testing.T, definition *jobs.Definition[string], bindingRaw string) jobs.ClaimTarget {
	t.Helper()
	descriptor := definition.Describe()
	revision, err := jobs.NewPayloadRevision(descriptor.Codec.ID, descriptor.Codec.CurrentVersion)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := jobs.ParseBindingName(bindingRaw)
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("test:jobspg-recovery")
	if err != nil {
		t.Fatal(err)
	}
	target, err := jobs.NewClaimTarget(jobs.ClaimTargetSpec{
		Definition: descriptor.Name, Binding: binding, Build: build, SupportedRevisions: []jobs.PayloadRevision{revision}, Available: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func postgresTestIncarnation(t *testing.T, marker byte) jobs.WorkerIncarnation {
	t.Helper()
	var raw [jobs.WorkerIncarnationBytes]byte
	raw[0] = marker
	incarnation, err := jobs.WorkerIncarnationFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	return incarnation
}
