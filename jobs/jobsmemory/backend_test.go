package jobsmemory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeClock) Advance(delta time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(delta)
	clock.mu.Unlock()
}

func TestQueueClaimApplyAndFence(t *testing.T) {
	fixture := newFixture(t, 4)
	id, err := jobs.Enqueue(context.Background(), fixture.queue, fixture.definition, "payload")
	if err != nil {
		t.Fatal(err)
	}
	claim := fixture.claim(t, fixture.incarnation(1))
	if claim.Len() != 1 || claim.Items()[0].Lease().InvocationID() != id || string(claim.Items()[0].Record().Payload.Data) != "payload" {
		t.Fatalf("claim = %#v", claim.Items())
	}
	lease := claim.Items()[0].Lease()
	begin, err := jobs.BeginAttemptCommand(lease, fixture.binding, fixture.build)
	if err != nil {
		t.Fatal(err)
	}
	beginRequest, err := jobs.NewApplyRequest(begin)
	if err != nil {
		t.Fatal(err)
	}
	beginResult, err := fixture.backend.Apply(context.Background(), beginRequest)
	if err != nil {
		t.Fatal(err)
	}
	validatedBegin, err := jobs.ValidateApplyResult(fixture.backend.Description(), beginRequest, beginResult)
	if err != nil || !validatedBegin.HandlerReady() {
		t.Fatalf("begin = (%v, %v)", validatedBegin, err)
	}
	fixture.clock.Advance(time.Second)
	finish, err := jobs.FinishAttemptCommand(lease, jobs.SuccessDisposition(), 0, jobs.MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	finishRequest, err := jobs.NewApplyRequest(finish)
	if err != nil {
		t.Fatal(err)
	}
	finishResult, err := fixture.backend.Apply(context.Background(), finishRequest)
	if err != nil {
		t.Fatal(err)
	}
	validatedFinish, err := jobs.ValidateApplyResult(fixture.backend.Description(), finishRequest, finishResult)
	if err != nil || validatedFinish.Result().Mutation() != jobs.DeliveryMutationApplied || validatedFinish.Application().Invocation().State() != jobs.InvocationSucceeded {
		t.Fatalf("finish = (%v, %v)", validatedFinish, err)
	}
	staleResult, err := fixture.backend.Apply(context.Background(), beginRequest)
	if err != nil || staleResult.Result().Mutation() != jobs.DeliveryMutationLeaseLost {
		t.Fatalf("stale apply = (%v, %v)", staleResult, err)
	}
	if stats := fixture.backend.Stats(); stats.Records != 0 || stats.Leased != 0 || stats.Bytes != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestRenewRotatesFenceAndRecoverFindsUncertainClaim(t *testing.T) {
	fixture := newFixture(t, 4)
	if _, err := jobs.Enqueue(context.Background(), fixture.queue, fixture.definition, "first"); err != nil {
		t.Fatal(err)
	}
	incarnation := fixture.incarnation(2)
	claim := fixture.claim(t, incarnation)
	oldLease := claim.Items()[0].Lease()
	renewRequest, err := jobs.NewRenewRequest([]jobs.LeaseRef{oldLease}, jobs.DefaultLeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := fixture.backend.Renew(context.Background(), renewRequest)
	if err != nil {
		t.Fatal(err)
	}
	validatedRenewal, err := jobs.ValidateRenewResult(fixture.backend.Description(), renewRequest, renewed)
	if err != nil || validatedRenewal.Len() != 1 || validatedRenewal.Items()[0].Mutation() != jobs.DeliveryMutationApplied {
		t.Fatalf("renew = (%v, %v)", validatedRenewal, err)
	}
	newLease := validatedRenewal.Items()[0].Current()
	if string(oldLease.DriverToken()) == string(newLease.DriverToken()) {
		t.Fatal("renew did not rotate lease")
	}
	stale, err := jobs.BeginAttemptCommand(oldLease, fixture.binding, fixture.build)
	if err != nil {
		t.Fatal(err)
	}
	staleRequest, _ := jobs.NewApplyRequest(stale)
	staleResult, err := fixture.backend.Apply(context.Background(), staleRequest)
	if err != nil || staleResult.Result().Mutation() != jobs.DeliveryMutationLeaseLost {
		t.Fatalf("stale result = (%v, %v)", staleResult, err)
	}
	recoverRequest, err := jobs.NewRecoverRequest(jobs.RecoverRequestSpec{
		Namespace: fixture.namespace, Incarnation: incarnation, MaxItems: 1,
		MaxBytes: jobs.MaxDeliveryRecordBytes, LeaseTTL: jobs.DefaultLeaseTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := fixture.backend.Recover(context.Background(), recoverRequest)
	if err != nil {
		t.Fatal(err)
	}
	validatedRecovery, err := jobs.ValidateRecoverResult(fixture.backend.Description(), recoverRequest, recovered)
	if err != nil || len(validatedRecovery.Items()) != 1 || validatedRecovery.Items()[0].Lease().InvocationID() != newLease.InvocationID() {
		t.Fatalf("recover = (%v, %v)", validatedRecovery, err)
	}
}

func TestEnqueueOnceAndCapacity(t *testing.T) {
	fixture := newFixture(t, 1)
	intent := jobs.Intent("request-1")
	first, outcome, err := jobs.EnqueueOnce(context.Background(), fixture.queue, fixture.definition, intent, "same")
	if err != nil || outcome != jobs.EnqueueCreated {
		t.Fatalf("first = (%v, %v, %v)", first, outcome, err)
	}
	second, outcome, err := jobs.EnqueueOnce(context.Background(), fixture.queue, fixture.definition, intent, "same")
	if err != nil || outcome != jobs.EnqueueExistingSamePayload || second != first {
		t.Fatalf("same = (%v, %v, %v)", second, outcome, err)
	}
	conflict, outcome, err := jobs.EnqueueOnce(context.Background(), fixture.queue, fixture.definition, intent, "different")
	if err != nil || outcome != jobs.EnqueueConflict || conflict != first {
		t.Fatalf("conflict = (%v, %v, %v)", conflict, outcome, err)
	}
	if _, err := jobs.Enqueue(context.Background(), fixture.queue, fixture.definition, "overflow"); !errors.Is(err, jobs.ErrSaturated) {
		t.Fatalf("capacity = %v", err)
	}
}

func TestUniqueSurvivesClaimRecoveryRetryAndDeferralUntilTerminal(t *testing.T) {
	fixture := newFixture(t, 4)
	ctx := context.Background()
	first, err := jobs.Enqueue(ctx, fixture.queue, fixture.definition, "first", jobs.Unique("sweeper"))
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := jobs.Enqueue(ctx, fixture.queue, fixture.definition, "queued replacement", jobs.Unique("sweeper"))
	if err != nil || duplicate != first {
		t.Fatalf("queued duplicate = (%v, %v), want %v", duplicate, err, first)
	}
	claim := fixture.claim(t, fixture.incarnation(6))
	if claim.Len() != 1 || claim.Items()[0].Record().Genesis.Mode != jobs.PlacementUnique || string(claim.Items()[0].Record().Payload.Data) != "first" {
		t.Fatalf("unique claim = %+v", claim.Items())
	}
	if duplicate, err = jobs.Enqueue(ctx, fixture.queue, fixture.definition, "running replacement", jobs.Unique("sweeper")); err != nil || duplicate != first {
		t.Fatalf("running duplicate = (%v, %v), want %v", duplicate, err, first)
	}
	fixture.clock.Advance(jobs.DefaultLeaseTTL)
	recoverRequest, err := jobs.NewRecoverRequest(jobs.RecoverRequestSpec{
		Namespace: fixture.namespace, Incarnation: fixture.incarnation(7), MaxItems: 1,
		MaxBytes: jobs.MaxDeliveryRecordBytes, LeaseTTL: jobs.DefaultLeaseTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := fixture.backend.Recover(ctx, recoverRequest)
	if err != nil || len(recovered.Items()) != 1 || recovered.Items()[0].Record().Genesis.ID != first {
		t.Fatalf("unique recovery = (%v, %v)", recovered, err)
	}
	lease := recovered.Items()[0].Lease()
	applyMemoryCommand(t, fixture.backend, mustBeginMemoryCommand(t, lease, fixture.binding, fixture.build))
	fixture.clock.Advance(time.Millisecond)
	retry, err := jobs.RetryDisposition(jobs.ReasonHandlerFailure, jobs.PublicFailure{}, jobs.MinRetryDelay, jobs.RetryCostCharged)
	if err != nil {
		t.Fatal(err)
	}
	finishRetry, err := jobs.FinishAttemptCommand(lease, retry, jobs.MinRetryDelay, jobs.MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	applyMemoryCommand(t, fixture.backend, finishRetry)
	if duplicate, err = jobs.Enqueue(ctx, fixture.queue, fixture.definition, "retry replacement", jobs.Unique("sweeper")); err != nil || duplicate != first {
		t.Fatalf("retry duplicate = (%v, %v), want %v", duplicate, err, first)
	}
	fixture.clock.Advance(jobs.MinRetryDelay)
	lease = fixture.claim(t, fixture.incarnation(8)).Items()[0].Lease()
	applyMemoryCommand(t, fixture.backend, mustBeginMemoryCommand(t, lease, fixture.binding, fixture.build))
	fixture.clock.Advance(time.Millisecond)
	deferred, err := jobs.DeferredDisposition(jobs.PublicFailure{}, jobs.MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	finishDeferred, err := jobs.FinishAttemptCommand(lease, deferred, jobs.MinRetryDelay, jobs.MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	applyMemoryCommand(t, fixture.backend, finishDeferred)
	if duplicate, err = jobs.Enqueue(ctx, fixture.queue, fixture.definition, "deferred replacement", jobs.Unique("sweeper")); err != nil || duplicate != first {
		t.Fatalf("deferred duplicate = (%v, %v), want %v", duplicate, err, first)
	}
	fixture.clock.Advance(jobs.MinRetryDelay)
	lease = fixture.claim(t, fixture.incarnation(9)).Items()[0].Lease()
	applyMemoryCommand(t, fixture.backend, mustBeginMemoryCommand(t, lease, fixture.binding, fixture.build))
	fixture.clock.Advance(time.Millisecond)
	finishSuccess, err := jobs.FinishAttemptCommand(lease, jobs.SuccessDisposition(), 0, jobs.MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	result := applyMemoryCommand(t, fixture.backend, finishSuccess)
	if result.Application().Invocation().State() != jobs.InvocationSucceeded {
		t.Fatalf("terminal state = %v", result.Application().Invocation().State())
	}
	next, err := jobs.Enqueue(ctx, fixture.queue, fixture.definition, "next", jobs.Unique("sweeper"))
	if err != nil || next == first {
		t.Fatalf("post-terminal unique = (%v, %v), old %v", next, err, first)
	}
}

func TestNamespaceIsolationAndExpiredRecovery(t *testing.T) {
	fixture := newFixture(t, 4)
	if _, err := jobs.Enqueue(context.Background(), fixture.queue, fixture.definition, "payload"); err != nil {
		t.Fatal(err)
	}
	claim := fixture.claim(t, fixture.incarnation(3))
	fixture.clock.Advance(jobs.DefaultLeaseTTL)
	otherNamespace, err := jobs.NamespaceOf("memory-other", "test")
	if err != nil {
		t.Fatal(err)
	}
	otherRequest, err := jobs.NewRecoverRequest(jobs.RecoverRequestSpec{
		Namespace: otherNamespace, Incarnation: fixture.incarnation(4), MaxItems: 1,
		MaxBytes: jobs.MaxDeliveryRecordBytes, LeaseTTL: jobs.DefaultLeaseTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := fixture.backend.Recover(context.Background(), otherRequest)
	if err != nil || len(other.Items()) != 0 {
		t.Fatalf("other namespace = (%v, %v)", other, err)
	}
	recoverRequest, err := jobs.NewRecoverRequest(jobs.RecoverRequestSpec{
		Namespace: fixture.namespace, Incarnation: fixture.incarnation(5), MaxItems: 1,
		MaxBytes: jobs.MaxDeliveryRecordBytes, LeaseTTL: jobs.DefaultLeaseTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := fixture.backend.Recover(context.Background(), recoverRequest)
	if err != nil || len(recovered.Items()) != 1 || recovered.Items()[0].Lease().InvocationID() != claim.Items()[0].Lease().InvocationID() {
		t.Fatalf("expired recovery = (%v, %v)", recovered, err)
	}
}

type fixture struct {
	backend    *Backend
	clock      *fakeClock
	queue      *jobs.Queue
	definition *jobs.Definition[string]
	namespace  jobs.Namespace
	binding    jobs.BindingName
	build      jobs.BuildID
}

func newFixture(t *testing.T, maxRecords int) fixture {
	t.Helper()
	clock := &fakeClock{now: time.Date(2035, 1, 2, 3, 4, 5, 0, time.UTC)}
	backend, err := New(Limits{MaxRecords: maxRecords, MaxBytes: int64(maxRecords) * int64(jobs.MaxDeliveryRecordBytes)}, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	name, err := jobs.ParseName("memory.delivery")
	if err != nil {
		t.Fatal(err)
	}
	definition := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(1), Policy: policy, Partition: jobs.PartitionGlobal})
	namespace, err := jobs.NamespaceOf("memory", "test")
	if err != nil {
		t.Fatal(err)
	}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: jobs.MustCatalog(definition), Sender: backend})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := jobs.ParseBindingName("memory.primary")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("memory-build")
	if err != nil {
		t.Fatal(err)
	}
	return fixture{backend: backend, clock: clock, queue: queue, definition: definition, namespace: namespace, binding: binding, build: build}
}

func (fixture fixture) claim(t *testing.T, incarnation jobs.WorkerIncarnation) jobs.ClaimBatch {
	t.Helper()
	descriptor := fixture.definition.Describe()
	revision, err := jobs.NewPayloadRevision(descriptor.Codec.ID, descriptor.Codec.CurrentVersion)
	if err != nil {
		t.Fatal(err)
	}
	target, err := jobs.NewClaimTarget(jobs.ClaimTargetSpec{
		Definition: fixture.definition.Name(), Binding: fixture.binding, Build: fixture.build,
		SupportedRevisions: []jobs.PayloadRevision{revision}, Available: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := jobs.NewClaimRequest(jobs.ClaimRequestSpec{
		Namespace: fixture.namespace, Incarnation: incarnation, Targets: []jobs.ClaimTarget{target},
		MaxItems: 1, MaxBytes: jobs.MaxDeliveryRecordBytes, LeaseTTL: jobs.DefaultLeaseTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := fixture.backend.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := jobs.ValidateClaimBatch(fixture.backend.Description(), request, batch)
	if err != nil {
		t.Fatal(err)
	}
	return validated
}

func (fixture fixture) incarnation(value byte) jobs.WorkerIncarnation {
	var raw [jobs.WorkerIncarnationBytes]byte
	raw[0] = value
	incarnation, err := jobs.WorkerIncarnationFromBytes(raw)
	if err != nil {
		panic(err)
	}
	return incarnation
}

func mustBeginMemoryCommand(t *testing.T, lease jobs.LeaseRef, binding jobs.BindingName, build jobs.BuildID) jobs.DeliveryCommand {
	t.Helper()
	command, err := jobs.BeginAttemptCommand(lease, binding, build)
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func applyMemoryCommand(t *testing.T, backend *Backend, command jobs.DeliveryCommand) jobs.ApplyResult {
	t.Helper()
	request, err := jobs.NewApplyRequest(command)
	if err != nil {
		t.Fatal(err)
	}
	result, err := backend.Apply(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := jobs.ValidateApplyResult(backend.Description(), request, result)
	if err != nil {
		t.Fatal(err)
	}
	return validated
}
