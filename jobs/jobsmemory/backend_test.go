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

func TestControllerCancelsQueuedAndRequestsRunningCancellation(t *testing.T) {
	fixture := newFixture(t, 4)
	ctx := context.Background()
	queued, err := jobs.Enqueue(ctx, fixture.queue, fixture.definition, "queued")
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Advance(time.Second)
	cancelled, err := fixture.backend.Cancel(ctx, queued)
	if err != nil || cancelled.Invocation().State() != jobs.InvocationCancelled || string(cancelled.Payload().Bytes()) != "queued" {
		t.Fatalf("queued cancel = (%v, %q, %v)", cancelled.Invocation().State(), cancelled.Payload().Bytes(), err)
	}
	if _, err := fixture.backend.Cancel(ctx, queued); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("repeated cancel = %v", err)
	}
	if _, err := fixture.backend.Terminate(ctx, queued); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("terminate cancelled = %v", err)
	}

	runningID, err := jobs.Enqueue(ctx, fixture.queue, fixture.definition, "running", jobs.Unique("memory-control"))
	if err != nil {
		t.Fatal(err)
	}
	claim := fixture.claim(t, fixture.incarnation(10))
	if claim.Len() != 1 || claim.Items()[0].Lease().InvocationID() != runningID {
		t.Fatalf("running claim = %+v", claim.Items())
	}
	lease := claim.Items()[0].Lease()
	applyMemoryCommand(t, fixture.backend, mustBeginMemoryCommand(t, lease, fixture.binding, fixture.build))
	fixture.clock.Advance(time.Second)
	requested, err := fixture.backend.Cancel(ctx, runningID)
	if err != nil || requested.Invocation().State() != jobs.InvocationCancelRequested || string(requested.Payload().Bytes()) != "running" {
		t.Fatalf("running cancel = (%v, %q, %v)", requested.Invocation().State(), requested.Payload().Bytes(), err)
	}
	if stats := fixture.backend.Stats(); stats.Leased != 1 {
		t.Fatalf("cancel request leases = %d", stats.Leased)
	}
	renewRequest, err := jobs.NewRenewRequest([]jobs.LeaseRef{lease}, jobs.DefaultLeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := fixture.backend.Renew(ctx, renewRequest)
	if err != nil || renewed.Items()[0].Mutation() != jobs.DeliveryMutationApplied || renewed.Items()[0].Control() != jobs.DeliveryControlCancelRequested {
		t.Fatalf("cancel renewal = (%v, %v, %v)", renewed.Items()[0].Mutation(), renewed.Items()[0].Control(), err)
	}
	if _, err := fixture.backend.Cancel(ctx, runningID); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("repeated running cancel = %v", err)
	}
}

func TestControllerTerminateFencesLeaseAndReleasesUniqueIntent(t *testing.T) {
	fixture := newFixture(t, 4)
	ctx := context.Background()
	id, err := jobs.Enqueue(ctx, fixture.queue, fixture.definition, "running", jobs.Unique("memory-terminate"))
	if err != nil {
		t.Fatal(err)
	}
	lease := fixture.claim(t, fixture.incarnation(11)).Items()[0].Lease()
	applyMemoryCommand(t, fixture.backend, mustBeginMemoryCommand(t, lease, fixture.binding, fixture.build))
	fixture.clock.Advance(time.Second)
	terminated, err := fixture.backend.Terminate(ctx, id)
	if err != nil || terminated.Invocation().State() != jobs.InvocationTerminated || terminated.Invocation().Attempts()[0].Disposition().Kind() != jobs.DispositionTerminated {
		t.Fatalf("terminate = (%v, %v)", terminated.Invocation().State(), err)
	}
	if stats := fixture.backend.Stats(); stats.Leased != 0 || stats.Records != 1 {
		t.Fatalf("terminate stats = %+v", stats)
	}
	renewRequest, err := jobs.NewRenewRequest([]jobs.LeaseRef{lease}, jobs.DefaultLeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := fixture.backend.Renew(ctx, renewRequest)
	if err != nil || renewed.Items()[0].Mutation() != jobs.DeliveryMutationLeaseLost || renewed.Items()[0].Control() != jobs.DeliveryControlTerminated {
		t.Fatalf("terminated renewal = (%v, %v, %v)", renewed.Items()[0].Mutation(), renewed.Items()[0].Control(), err)
	}
	finish, err := jobs.FinishAttemptCommand(lease, jobs.SuccessDisposition(), 0, jobs.MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	stale := applyMemoryCommand(t, fixture.backend, finish)
	if stale.Result().Mutation() != jobs.DeliveryMutationLeaseLost || stale.Result().Control() != jobs.DeliveryControlTerminated {
		t.Fatalf("stale finish = (%v, %v)", stale.Result().Mutation(), stale.Result().Control())
	}
	next, err := jobs.Enqueue(ctx, fixture.queue, fixture.definition, "next", jobs.Unique("memory-terminate"))
	if err != nil || next == id {
		t.Fatalf("released unique = (%v, %v), old %v", next, err, id)
	}
	if _, err := fixture.backend.Terminate(ctx, id); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("repeated terminate = %v", err)
	}
}

func TestControllerCapacityReservationKeepsControlAvailableAtByteLimit(t *testing.T) {
	fixture := newFixture(t, 1)
	id, err := jobs.Enqueue(t.Context(), fixture.queue, fixture.definition, "capacity")
	if err != nil {
		t.Fatal(err)
	}
	fixture.backend.mu.Lock()
	item := fixture.backend.entries[id]
	if item.charge <= item.size {
		fixture.backend.mu.Unlock()
		t.Fatalf("control reserve = size %d charge %d", item.size, item.charge)
	}
	before := fixture.backend.bytes + fixture.backend.reserved
	fixture.backend.limits.MaxBytes = before
	fixture.backend.mu.Unlock()
	fixture.clock.Advance(time.Second)
	if _, err := fixture.backend.Cancel(t.Context(), id); err != nil {
		t.Fatalf("cancel at byte limit = %v", err)
	}
	fixture.backend.mu.Lock()
	defer fixture.backend.mu.Unlock()
	item = fixture.backend.entries[id]
	if item.charge != item.size || fixture.backend.bytes != int64(item.size) || fixture.backend.reserved != 0 || fixture.backend.bytes > before {
		t.Fatalf("terminal accounting = size %d charge %d bytes %d reserved %d before %d", item.size, item.charge, fixture.backend.bytes, fixture.backend.reserved, before)
	}
}

func TestControllerSerializesWithRenewAndFinish(t *testing.T) {
	t.Run("cancel and renew", func(t *testing.T) {
		fixture := newFixture(t, 1)
		id, err := jobs.Enqueue(t.Context(), fixture.queue, fixture.definition, "cancel-renew")
		if err != nil {
			t.Fatal(err)
		}
		lease := fixture.claim(t, fixture.incarnation(12)).Items()[0].Lease()
		applyMemoryCommand(t, fixture.backend, mustBeginMemoryCommand(t, lease, fixture.binding, fixture.build))
		fixture.clock.Advance(time.Second)
		renewRequest, err := jobs.NewRenewRequest([]jobs.LeaseRef{lease}, jobs.DefaultLeaseTTL)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		cancelDone := make(chan error, 1)
		renewDone := make(chan struct {
			result jobs.RenewResult
			err    error
		}, 1)
		go func() {
			<-start
			_, cancelErr := fixture.backend.Cancel(context.Background(), id)
			cancelDone <- cancelErr
		}()
		go func() {
			<-start
			result, renewErr := fixture.backend.Renew(context.Background(), renewRequest)
			renewDone <- struct {
				result jobs.RenewResult
				err    error
			}{result: result, err: renewErr}
		}()
		close(start)
		if err := <-cancelDone; err != nil {
			t.Fatal(err)
		}
		renewed := <-renewDone
		if renewed.err != nil || renewed.result.Items()[0].Mutation() != jobs.DeliveryMutationApplied {
			t.Fatalf("concurrent renew = (%v, %v)", renewed.result, renewed.err)
		}
		fixture.backend.mu.Lock()
		item := fixture.backend.entries[id]
		state := item.invocation.State()
		current := item.lease.reference
		fixture.backend.mu.Unlock()
		if state != jobs.InvocationCancelRequested || !sameLease(current, renewed.result.Items()[0].Current()) {
			t.Fatalf("post-race state = %v lease = %v", state, current)
		}
	})

	t.Run("terminate and finish", func(t *testing.T) {
		fixture := newFixture(t, 1)
		id, _, err := jobs.EnqueueOnce(t.Context(), fixture.queue, fixture.definition, jobs.Intent("terminate-finish"), "payload")
		if err != nil {
			t.Fatal(err)
		}
		lease := fixture.claim(t, fixture.incarnation(13)).Items()[0].Lease()
		applyMemoryCommand(t, fixture.backend, mustBeginMemoryCommand(t, lease, fixture.binding, fixture.build))
		fixture.clock.Advance(time.Second)
		finish, err := jobs.FinishAttemptCommand(lease, jobs.SuccessDisposition(), 0, jobs.MinRetryDelay)
		if err != nil {
			t.Fatal(err)
		}
		finishRequest, err := jobs.NewApplyRequest(finish)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		terminateDone := make(chan error, 1)
		finishDone := make(chan struct {
			result jobs.ApplyResult
			err    error
		}, 1)
		go func() {
			<-start
			_, terminateErr := fixture.backend.Terminate(context.Background(), id)
			terminateDone <- terminateErr
		}()
		go func() {
			<-start
			result, finishErr := fixture.backend.Apply(context.Background(), finishRequest)
			finishDone <- struct {
				result jobs.ApplyResult
				err    error
			}{result: result, err: finishErr}
		}()
		close(start)
		terminateErr := <-terminateDone
		finished := <-finishDone
		if finished.err != nil {
			t.Fatal(finished.err)
		}
		if terminateErr == nil {
			if finished.result.Result().Mutation() != jobs.DeliveryMutationLeaseLost || finished.result.Result().Control() != jobs.DeliveryControlTerminated {
				t.Fatalf("finish after terminate = (%v, %v)", finished.result.Result().Mutation(), finished.result.Result().Control())
			}
		} else if !errors.Is(terminateErr, jobs.ErrConflict) || finished.result.Result().Mutation() != jobs.DeliveryMutationApplied {
			t.Fatalf("terminate after finish = (%v, %v)", terminateErr, finished.result.Result().Mutation())
		}
		fixture.backend.mu.Lock()
		item := fixture.backend.entries[id]
		state := item.invocation.State()
		leased := item.lease != nil
		fixture.backend.mu.Unlock()
		if !state.Terminal() || leased {
			t.Fatalf("post-race state = %v leased = %t", state, leased)
		}
	})
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
