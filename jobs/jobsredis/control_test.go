package jobsredis

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/frostgrove/vv/jobs"
	"github.com/redis/go-redis/v9"
)

type controllerFixture struct {
	server     *miniredis.Miniredis
	driver     *Driver
	queue      *jobs.Queue
	definition *jobs.Definition[string]
	namespace  jobs.Namespace
	target     jobs.ClaimTarget
	now        time.Time
}

func newControllerFixture(t *testing.T) controllerFixture {
	t.Helper()
	server := miniredis.RunT(t)
	now := time.Date(2037, 4, 5, 6, 7, 8, 0, time.UTC)
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
	return controllerFixture{
		server:     server,
		driver:     driver,
		queue:      queue,
		definition: definition,
		namespace:  namespace,
		target:     redisTestTarget(t, definition),
		now:        now,
	}
}

func (fixture *controllerFixture) advance(delta time.Duration) {
	fixture.now = fixture.now.Add(delta)
	fixture.server.SetTime(fixture.now)
}

func TestControllerCancelQueuedAndRunning(t *testing.T) {
	fixture := newControllerFixture(t)
	queuedID, err := jobs.Enqueue(t.Context(), fixture.queue, fixture.definition, "queued")
	if err != nil {
		t.Fatal(err)
	}
	fixture.advance(time.Millisecond)
	cancelled, err := fixture.driver.Cancel(t.Context(), queuedID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Invocation().State() != jobs.InvocationCancelled || cancelled.Invocation().Outcome().Kind() != jobs.InvocationOutcomeDeliveryTerminal || cancelled.Invocation().Outcome().TerminalReason() != jobs.ReasonCancelRequested || cancelled.Invocation().FinishedAt() != fixture.now || string(cancelled.Payload().Bytes()) != "queued" {
		t.Fatalf("queued cancellation = state %v outcome %v at %v payload %q", cancelled.Invocation().State(), cancelled.Invocation().Outcome(), cancelled.Invocation().FinishedAt(), cancelled.Payload().Bytes())
	}
	stored, found, err := fixture.driver.repo.entry(t.Context(), queuedID.String())
	if err != nil || !found || stored.State != jobs.InvocationCancelled || len(stored.LeaseToken) != 0 || len(stored.Intents) != 0 {
		t.Fatalf("stored queued cancellation = (%+v, %t, %v)", stored, found, err)
	}
	if _, err := fixture.driver.Cancel(t.Context(), queuedID); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("repeated queued cancellation = %v", err)
	}

	runningID, err := jobs.Enqueue(t.Context(), fixture.queue, fixture.definition, "running")
	if err != nil {
		t.Fatal(err)
	}
	claim := redisTestClaim(t, fixture.driver, fixture.namespace, fixture.target, 21)
	if claim.Len() != 1 || claim.Items()[0].Record().Genesis.ID != runningID {
		t.Fatalf("running claim = %+v", claim.Items())
	}
	lease := claim.Items()[0].Lease()
	applyRedisCommand(t, fixture.driver, mustRedisBegin(t, lease, fixture.target))
	fixture.advance(time.Millisecond)
	requested, err := fixture.driver.Cancel(t.Context(), runningID)
	if err != nil {
		t.Fatal(err)
	}
	if requested.Invocation().State() != jobs.InvocationCancelRequested || requested.Invocation().Outcome().Kind() != jobs.InvocationOutcomeCancelRequested || requested.Invocation().CancelRequestedAt() != fixture.now || string(requested.Payload().Bytes()) != "running" {
		t.Fatalf("running cancellation = state %v outcome %v at %v payload %q", requested.Invocation().State(), requested.Invocation().Outcome(), requested.Invocation().CancelRequestedAt(), requested.Payload().Bytes())
	}
	stored, found, err = fixture.driver.repo.entry(t.Context(), runningID.String())
	if err != nil || !found || stored.State != jobs.InvocationCancelRequested || !bytes.Equal(stored.LeaseToken, lease.DriverToken()) {
		t.Fatalf("stored running cancellation = (%+v, %t, %v)", stored, found, err)
	}
	renewRequest, err := jobs.NewRenewRequest([]jobs.LeaseRef{lease}, jobs.MinimumLeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	renewResult, err := fixture.driver.Renew(t.Context(), renewRequest)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := jobs.ValidateRenewResult(fixture.driver.Description(), renewRequest, renewResult)
	if err != nil || renewed.Len() != 1 || renewed.Items()[0].Mutation() != jobs.DeliveryMutationApplied || renewed.Items()[0].Control() != jobs.DeliveryControlCancelRequested {
		t.Fatalf("cancel renewal = (%v, %v)", renewed, err)
	}
	if _, err := fixture.driver.Cancel(t.Context(), runningID); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("repeated running cancellation = %v", err)
	}
	fixture.advance(time.Millisecond)
	disposition, err := jobs.CancelledDisposition(jobs.ReasonCancelRequested)
	if err != nil {
		t.Fatal(err)
	}
	finish, err := jobs.FinishAttemptCommand(renewed.Items()[0].Current(), disposition, 0, jobs.MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	result := applyRedisCommand(t, fixture.driver, finish)
	if result.Application().Invocation().State() != jobs.InvocationCancelled {
		t.Fatalf("cooperative cancellation = %v", result.Application().Invocation().State())
	}
}

func TestControllerTerminateFencesRunningLeaseAndReleasesUniqueIntent(t *testing.T) {
	fixture := newControllerFixture(t)
	id, err := jobs.Enqueue(t.Context(), fixture.queue, fixture.definition, "unique", jobs.Unique("controller-key"))
	if err != nil {
		t.Fatal(err)
	}
	claim := redisTestClaim(t, fixture.driver, fixture.namespace, fixture.target, 22)
	if claim.Len() != 1 || claim.Items()[0].Record().Genesis.ID != id {
		t.Fatalf("unique claim = %+v", claim.Items())
	}
	lease := claim.Items()[0].Lease()
	applyRedisCommand(t, fixture.driver, mustRedisBegin(t, lease, fixture.target))
	fixture.advance(time.Millisecond)
	terminated, err := fixture.driver.Terminate(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	attempts := terminated.Invocation().Attempts()
	if terminated.Invocation().State() != jobs.InvocationTerminated || terminated.Invocation().FinishedAt() != fixture.now || len(attempts) != 1 || attempts[0].Disposition().Kind() != jobs.DispositionTerminated || attempts[0].Disposition().Reason() != jobs.ReasonOperatorTerminated || string(terminated.Payload().Bytes()) != "unique" {
		t.Fatalf("running termination = state %v attempts %+v at %v payload %q", terminated.Invocation().State(), attempts, terminated.Invocation().FinishedAt(), terminated.Payload().Bytes())
	}
	stored, found, err := fixture.driver.repo.entry(t.Context(), id.String())
	if err != nil || !found || stored.State != jobs.InvocationTerminated || len(stored.LeaseToken) != 0 || len(stored.Intents) != 0 {
		t.Fatalf("stored termination = (%+v, %t, %v)", stored, found, err)
	}
	renewRequest, err := jobs.NewRenewRequest([]jobs.LeaseRef{lease}, jobs.MinimumLeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	renewResult, err := fixture.driver.Renew(t.Context(), renewRequest)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := jobs.ValidateRenewResult(fixture.driver.Description(), renewRequest, renewResult)
	if err != nil || renewed.Len() != 1 || renewed.Items()[0].Mutation() != jobs.DeliveryMutationLeaseLost || renewed.Items()[0].Control() != jobs.DeliveryControlTerminated {
		t.Fatalf("terminated renewal = (%v, %v)", renewed, err)
	}
	progress, err := jobs.ProgressCommand(lease)
	if err != nil {
		t.Fatal(err)
	}
	progressRequest, err := jobs.NewApplyRequest(progress)
	if err != nil {
		t.Fatal(err)
	}
	progressResult, err := fixture.driver.Apply(t.Context(), progressRequest)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := jobs.ValidateApplyResult(fixture.driver.Description(), progressRequest, progressResult)
	if err != nil || validated.Result().Mutation() != jobs.DeliveryMutationLeaseLost || validated.Result().Control() != jobs.DeliveryControlTerminated {
		t.Fatalf("terminated apply = (%v, %v)", validated, err)
	}
	next, err := jobs.Enqueue(t.Context(), fixture.queue, fixture.definition, "replacement", jobs.Unique("controller-key"))
	if err != nil || next == id {
		t.Fatalf("post-termination unique = (%v, %v), old %v", next, err, id)
	}
	if _, err := fixture.driver.Terminate(t.Context(), id); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("repeated termination = %v", err)
	}
}

func TestControllerKeepsOnceIntentAndFencesClaimedQueuedLease(t *testing.T) {
	fixture := newControllerFixture(t)
	onceID, outcome, err := jobs.EnqueueOnce(t.Context(), fixture.queue, fixture.definition, jobs.Intent("controller-once"), "once")
	if err != nil || outcome != jobs.EnqueueCreated {
		t.Fatalf("once enqueue = (%v, %v, %v)", onceID, outcome, err)
	}
	fixture.advance(time.Millisecond)
	if _, err := fixture.driver.Cancel(t.Context(), onceID); err != nil {
		t.Fatal(err)
	}
	stored, found, err := fixture.driver.repo.entry(t.Context(), onceID.String())
	if err != nil || !found || stored.State != jobs.InvocationCancelled || len(stored.Intents) != 1 {
		t.Fatalf("stored once cancellation = (%+v, %t, %v)", stored, found, err)
	}
	existing, outcome, err := jobs.EnqueueOnce(t.Context(), fixture.queue, fixture.definition, jobs.Intent("controller-once"), "once")
	if err != nil || outcome != jobs.EnqueueExistingSamePayload || existing != onceID {
		t.Fatalf("once after cancellation = (%v, %v, %v), want %v", existing, outcome, err, onceID)
	}

	queuedID, err := jobs.Enqueue(t.Context(), fixture.queue, fixture.definition, "claimed queued")
	if err != nil {
		t.Fatal(err)
	}
	claim := redisTestClaim(t, fixture.driver, fixture.namespace, fixture.target, 23)
	if claim.Len() != 1 || claim.Items()[0].Record().Genesis.ID != queuedID {
		t.Fatalf("queued claim = %+v", claim.Items())
	}
	lease := claim.Items()[0].Lease()
	fixture.advance(time.Millisecond)
	terminated, err := fixture.driver.Terminate(t.Context(), queuedID)
	if err != nil {
		t.Fatal(err)
	}
	if terminated.Invocation().State() != jobs.InvocationTerminated || len(terminated.Invocation().Attempts()) != 0 || terminated.Invocation().Outcome().Kind() != jobs.InvocationOutcomeDeliveryTerminal {
		t.Fatalf("claimed queued termination = state %v attempts %d outcome %v", terminated.Invocation().State(), len(terminated.Invocation().Attempts()), terminated.Invocation().Outcome())
	}
	begin := mustRedisBegin(t, lease, fixture.target)
	beginRequest, err := jobs.NewApplyRequest(begin)
	if err != nil {
		t.Fatal(err)
	}
	beginResult, err := fixture.driver.Apply(t.Context(), beginRequest)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := jobs.ValidateApplyResult(fixture.driver.Description(), beginRequest, beginResult)
	if err != nil || validated.Result().Mutation() != jobs.DeliveryMutationLeaseLost || validated.Result().Control() != jobs.DeliveryControlTerminated {
		t.Fatalf("claimed queued stale begin = (%v, %v)", validated, err)
	}
}

func TestControllerUsesServerTimeAfterMutationLockWait(t *testing.T) {
	fixture := newControllerFixture(t)
	id, err := jobs.Enqueue(t.Context(), fixture.queue, fixture.definition, "blocked")
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := fixture.driver.repo.lock(t.Context(), []byte("controller-blocker"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		view jobs.DeliveryView
		err  error
	}
	done := make(chan result, 1)
	go func() {
		view, cancelErr := fixture.driver.Cancel(context.Background(), id)
		done <- result{view: view, err: cancelErr}
	}()
	fixture.advance(time.Second)
	unlock()
	controlled := <-done
	if controlled.err != nil || controlled.view.Invocation().FinishedAt() != fixture.now {
		t.Fatalf("post-lock control time = (%v, %v), want %v", controlled.view.Invocation().FinishedAt(), controlled.err, fixture.now)
	}
}

func TestControllerValidatesReadinessIdentityAndPresence(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	_, _, namespace := testDefinition(t)
	driver, err := New(Spec{Client: client, Namespace: namespace})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Cancel(t.Context(), jobs.InvocationID{}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("unready cancellation = %v", err)
	}
	if err := driver.Prepare(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Cancel(t.Context(), jobs.InvocationID{}); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("zero cancellation = %v", err)
	}
	var raw [jobs.InvocationIDBytes]byte
	raw[0] = 1
	missing, err := jobs.InvocationIDFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Terminate(t.Context(), missing); !errors.Is(err, jobs.ErrInvocationNotFound) {
		t.Fatalf("missing termination = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := driver.Terminate(cancelled, missing); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled termination = %v", err)
	}
}
