package jobsredis

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/frostgrove/vv/jobs"
	"github.com/redis/go-redis/v9"
)

func TestRecoveryWindowIsBounded(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repo := newRepository(client, "jobs:{test}")
	var raw [jobs.WorkerIncarnationBytes]byte
	raw[0] = 1
	incarnation, err := jobs.WorkerIncarnationFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Round(0).UTC()
	for index := range 20 {
		if err := client.ZAdd(context.Background(), repo.leasedKey(), redis.Z{Score: float64(now.Add(-time.Second).UnixMilli()), Member: fmt.Sprintf("expired-%d", index)}).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.ZAdd(context.Background(), repo.incarnationKey(raw), redis.Z{Score: float64(now.Add(time.Minute).UnixMilli()), Member: fmt.Sprintf("owned-%d", index)}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := repo.recoveryIDs(context.Background(), incarnation, now, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) > 6 {
		t.Fatalf("recovery window = %d, want at most 6", len(ids))
	}
}

func TestQueueAndWorkersEndToEnd(t *testing.T) {
	server := miniredis.RunT(t)
	serverTime := time.Now().Round(0).UTC()
	server.SetTime(serverTime)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	definition, catalog, namespace := testDefinition(t)
	driver, err := Open(context.Background(), client, namespace)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.Enqueue(context.Background(), queue, definition, "normal", jobs.AtPriority(50)); err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.Enqueue(context.Background(), queue, definition, "urgent", jobs.AtPriority(1)); err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.Enqueue(context.Background(), queue, definition, "delayed", jobs.After(time.Second)); err != nil {
		t.Fatal(err)
	}

	handled := make(chan string, 3)
	consumer := jobs.On(definition, jobs.Handler[string](func(_ context.Context, payload string) error {
		handled <- payload
		return nil
	}), jobs.Concurrency(1))
	build, err := jobs.ParseBuildID("test:jobsredis")
	if err != nil {
		t.Fatal(err)
	}
	restorer := jobs.TrustedIdentityRestorerFunc(func(ctx context.Context, _ jobs.IdentityRestoreRequest) (jobs.RestoredIdentity, error) {
		return jobs.NewRestoredIdentity(ctx, jobs.ProducerPartition{}, jobs.ProducerActor{})
	})
	workers, err := jobs.NewWorkers(jobs.WorkersSpec{
		Namespace:    namespace,
		Catalog:      catalog,
		Driver:       driver,
		Build:        build,
		Identity:     restorer,
		PollInterval: jobs.MinimumPollInterval,
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stopRun := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- workers.Run(runCtx) }()
	for _, expected := range []string{"urgent", "normal"} {
		select {
		case actual := <-handled:
			if actual != expected {
				t.Fatalf("handled %q, want %q", actual, expected)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %q", expected)
		}
	}
	select {
	case value := <-handled:
		t.Fatalf("handled delayed job early: %q", value)
	case <-time.After(30 * time.Millisecond):
	}
	server.SetTime(serverTime.Add(time.Second))
	select {
	case actual := <-handled:
		if actual != "delayed" {
			t.Fatalf("handled %q, want delayed", actual)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for delayed job")
	}
	drainCtx, stopDrain := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopDrain()
	if err := workers.Drain(drainCtx); err != nil {
		t.Fatal(err)
	}
	stopRun()
	select {
	case err := <-runResult:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("workers did not stop")
	}
}

func TestEnqueueOnceSurvivesDriverReopen(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	definition, catalog, namespace := testDefinition(t)
	first, err := Open(context.Background(), client, namespace)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: first})
	if err != nil {
		t.Fatal(err)
	}
	id, outcome, err := jobs.EnqueueOnce(context.Background(), queue, definition, jobs.Intent("invoice:42"), "payload")
	if err != nil || outcome != jobs.EnqueueCreated {
		t.Fatalf("first enqueue = %s, %v", outcome, err)
	}
	second, err := Open(context.Background(), client, namespace)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: second})
	if err != nil {
		t.Fatal(err)
	}
	existing, outcome, err := jobs.EnqueueOnce(context.Background(), reopened, definition, jobs.Intent("invoice:42"), "payload")
	if err != nil {
		t.Fatal(err)
	}
	if existing != id || outcome != jobs.EnqueueExistingSamePayload {
		t.Fatalf("reopened enqueue = %s %s, want %s %s", existing, outcome, id, jobs.EnqueueExistingSamePayload)
	}
}

func TestUniqueSurvivesClaimRecoveryRetryAndDeferralUntilTerminal(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Now().Round(0).UTC()
	server.SetTime(now)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	definition, catalog, namespace := testDefinition(t)
	driver, err := Open(context.Background(), client, namespace)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver})
	if err != nil {
		t.Fatal(err)
	}
	first, err := jobs.Enqueue(context.Background(), queue, definition, "first", jobs.Unique("sweeper"))
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := jobs.Enqueue(context.Background(), queue, definition, "queued replacement", jobs.Unique("sweeper"))
	if err != nil || duplicate != first {
		t.Fatalf("queued duplicate = (%v, %v), want %v", duplicate, err, first)
	}
	target := redisTestTarget(t, definition)
	claim := redisTestClaim(t, driver, namespace, target, 1)
	if claim.Len() != 1 || claim.Items()[0].Record().Genesis.Mode != jobs.PlacementUnique || string(claim.Items()[0].Record().Payload.Data) != "first" {
		t.Fatalf("unique claim = %+v", claim.Items())
	}
	if duplicate, err = jobs.Enqueue(context.Background(), queue, definition, "running replacement", jobs.Unique("sweeper")); err != nil || duplicate != first {
		t.Fatalf("running duplicate = (%v, %v), want %v", duplicate, err, first)
	}
	now = now.Add(jobs.MinimumLeaseTTL)
	server.SetTime(now)
	recoverRequest, err := jobs.NewRecoverRequest(jobs.RecoverRequestSpec{
		Namespace: namespace, Incarnation: redisTestIncarnation(t, 2), MaxItems: 1,
		MaxBytes: jobs.MaxDeliveryRecordBytes, LeaseTTL: jobs.MinimumLeaseTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := driver.Recover(context.Background(), recoverRequest)
	if err != nil || len(recovered.Items()) != 1 || recovered.Items()[0].Record().Genesis.ID != first {
		t.Fatalf("unique recovery = (%v, %v)", recovered, err)
	}
	lease := recovered.Items()[0].Lease()
	applyRedisCommand(t, driver, mustRedisBegin(t, lease, target))
	now = now.Add(time.Millisecond)
	server.SetTime(now)
	retry, err := jobs.RetryDisposition(jobs.ReasonHandlerFailure, jobs.PublicFailure{}, jobs.MinRetryDelay, jobs.RetryCostCharged)
	if err != nil {
		t.Fatal(err)
	}
	finishRetry, err := jobs.FinishAttemptCommand(lease, retry, jobs.MinRetryDelay, jobs.MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	applyRedisCommand(t, driver, finishRetry)
	if duplicate, err = jobs.Enqueue(context.Background(), queue, definition, "retry replacement", jobs.Unique("sweeper")); err != nil || duplicate != first {
		t.Fatalf("retry duplicate = (%v, %v), want %v", duplicate, err, first)
	}
	now = now.Add(jobs.MinRetryDelay)
	server.SetTime(now)
	lease = redisTestClaim(t, driver, namespace, target, 3).Items()[0].Lease()
	applyRedisCommand(t, driver, mustRedisBegin(t, lease, target))
	now = now.Add(time.Millisecond)
	server.SetTime(now)
	deferred, err := jobs.DeferredDisposition(jobs.PublicFailure{}, jobs.MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	finishDeferred, err := jobs.FinishAttemptCommand(lease, deferred, jobs.MinRetryDelay, jobs.MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	applyRedisCommand(t, driver, finishDeferred)
	if duplicate, err = jobs.Enqueue(context.Background(), queue, definition, "deferred replacement", jobs.Unique("sweeper")); err != nil || duplicate != first {
		t.Fatalf("deferred duplicate = (%v, %v), want %v", duplicate, err, first)
	}
	now = now.Add(jobs.MinRetryDelay)
	server.SetTime(now)
	lease = redisTestClaim(t, driver, namespace, target, 4).Items()[0].Lease()
	applyRedisCommand(t, driver, mustRedisBegin(t, lease, target))
	now = now.Add(time.Millisecond)
	server.SetTime(now)
	finishSuccess, err := jobs.FinishAttemptCommand(lease, jobs.SuccessDisposition(), 0, jobs.MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	result := applyRedisCommand(t, driver, finishSuccess)
	if result.Application().Invocation().State() != jobs.InvocationSucceeded {
		t.Fatalf("terminal state = %v", result.Application().Invocation().State())
	}
	next, err := jobs.Enqueue(context.Background(), queue, definition, "next", jobs.Unique("sweeper"))
	if err != nil || next == first {
		t.Fatalf("post-terminal unique = (%v, %v), old %v", next, err, first)
	}
}

func redisTestTarget(t *testing.T, definition *jobs.Definition[string]) jobs.ClaimTarget {
	t.Helper()
	description := definition.Describe()
	revision, err := jobs.NewPayloadRevision(description.Codec.ID, description.Codec.CurrentVersion)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := jobs.ParseBindingName("redis.unique")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("redis-unique-build")
	if err != nil {
		t.Fatal(err)
	}
	target, err := jobs.NewClaimTarget(jobs.ClaimTargetSpec{Definition: definition.Name(), Binding: binding, Build: build, SupportedRevisions: []jobs.PayloadRevision{revision}, Available: 1})
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func redisTestClaim(t *testing.T, driver *Driver, namespace jobs.Namespace, target jobs.ClaimTarget, marker byte) jobs.ClaimBatch {
	t.Helper()
	request, err := jobs.NewClaimRequest(jobs.ClaimRequestSpec{
		Namespace: namespace, Incarnation: redisTestIncarnation(t, marker), Targets: []jobs.ClaimTarget{target},
		MaxItems: 1, MaxBytes: jobs.MaxDeliveryRecordBytes, LeaseTTL: jobs.MinimumLeaseTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := driver.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := jobs.ValidateClaimBatch(driver.Description(), request, batch)
	if err != nil {
		t.Fatal(err)
	}
	return validated
}

func redisTestIncarnation(t *testing.T, marker byte) jobs.WorkerIncarnation {
	t.Helper()
	var raw [jobs.WorkerIncarnationBytes]byte
	raw[0] = marker
	incarnation, err := jobs.WorkerIncarnationFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	return incarnation
}

func mustRedisBegin(t *testing.T, lease jobs.LeaseRef, target jobs.ClaimTarget) jobs.DeliveryCommand {
	t.Helper()
	command, err := jobs.BeginAttemptCommand(lease, target.Binding(), target.Build())
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func applyRedisCommand(t *testing.T, driver *Driver, command jobs.DeliveryCommand) jobs.ApplyResult {
	t.Helper()
	request, err := jobs.NewApplyRequest(command)
	if err != nil {
		t.Fatal(err)
	}
	result, err := driver.Apply(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := jobs.ValidateApplyResult(driver.Description(), request, result)
	if err != nil {
		t.Fatal(err)
	}
	return validated
}

func testDefinition(t *testing.T) (*jobs.Definition[string], jobs.Catalog, jobs.Namespace) {
	t.Helper()
	name, err := jobs.ParseName("example.deliver")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	definition := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(1), Policy: policy})
	catalog := jobs.MustCatalog(definition)
	namespace, err := jobs.NamespaceOf("example", "test")
	if err != nil {
		t.Fatal(err)
	}
	return definition, catalog, namespace
}
