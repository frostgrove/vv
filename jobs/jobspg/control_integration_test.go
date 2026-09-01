//go:build integration

package jobspg

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type postgresControllerFixture struct {
	db         *sql.DB
	driver     *Driver
	repo       repository
	queue      *jobs.Queue
	definition *jobs.Definition[string]
	namespace  jobs.Namespace
	ctx        context.Context
}

type postgresControlRow struct {
	state           jobs.InvocationState
	leaseOwner      []byte
	leaseToken      []byte
	leaseEpoch      int64
	leaseExpiresAt  sql.NullTime
	recordExpiresAt sql.NullTime
	intentExpiresAt sql.NullTime
}

func TestPostgresControllerTransitionsAndFences(t *testing.T) {
	fixture := newPostgresControllerFixture(t, "transitions")
	ctx := fixture.ctx
	if _, err := fixture.driver.Cancel(ctx, jobs.InvocationID{}); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("zero cancellation = %v", err)
	}
	missing, err := jobs.NewInvocationID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.driver.Terminate(ctx, missing); !errors.Is(err, jobs.ErrInvocationNotFound) {
		t.Fatalf("missing termination = %v", err)
	}

	queuedID, err := jobs.Enqueue(ctx, fixture.queue, fixture.definition, "queued", jobs.Unique("postgres-control-queued"))
	if err != nil {
		t.Fatal(err)
	}
	queuedLease := postgresControlClaim(t, ctx, fixture.driver, fixture.namespace, fixture.definition, queuedID, 31)
	queuedBefore := fixture.row(t, queuedID)
	cancelled, err := fixture.driver.Cancel(ctx, queuedID)
	if err != nil || cancelled.Invocation().State() != jobs.InvocationCancelled || string(cancelled.Payload().Bytes()) != "queued" {
		t.Fatalf("queued cancellation = (%v, %q, %v)", cancelled.Invocation().State(), cancelled.Payload().Bytes(), err)
	}
	queuedAfter := fixture.row(t, queuedID)
	if queuedAfter.state != jobs.InvocationCancelled || len(queuedAfter.leaseOwner) != 0 || len(queuedAfter.leaseToken) != 0 || queuedAfter.leaseExpiresAt.Valid || queuedAfter.leaseEpoch != queuedBefore.leaseEpoch+1 || !queuedAfter.recordExpiresAt.Valid || !queuedAfter.intentExpiresAt.Valid {
		t.Fatalf("queued cancellation row = %+v, before %+v", queuedAfter, queuedBefore)
	}
	begin := postgresControlBegin(t, queuedLease, fixture.definition, "jobspg.control.queued")
	staleBegin := postgresControlApply(t, ctx, fixture.driver, begin)
	if staleBegin.Result().Mutation() != jobs.DeliveryMutationLeaseLost || staleBegin.Result().Control() != jobs.DeliveryControlCancelRequested {
		t.Fatalf("stale queued begin = (%v, %v)", staleBegin.Result().Mutation(), staleBegin.Result().Control())
	}
	nextQueued, err := jobs.Enqueue(ctx, fixture.queue, fixture.definition, "next", jobs.Unique("postgres-control-queued"))
	if err != nil || nextQueued == queuedID {
		t.Fatalf("released queued unique = (%v, %v), old %v", nextQueued, err, queuedID)
	}
	if _, err := fixture.driver.Cancel(ctx, nextQueued); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.driver.Cancel(ctx, queuedID); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("repeated cancellation = %v", err)
	}

	runningID, err := jobs.Enqueue(ctx, fixture.queue, fixture.definition, "running", jobs.Unique("postgres-control-running"), jobs.AtPriority(1))
	if err != nil {
		t.Fatal(err)
	}
	runningLease := adminClaimInvocation(t, ctx, fixture.driver, fixture.namespace, fixture.definition, runningID, 32)
	runningBefore := fixture.row(t, runningID)
	requested, err := fixture.driver.Cancel(ctx, runningID)
	if err != nil || requested.Invocation().State() != jobs.InvocationCancelRequested || string(requested.Payload().Bytes()) != "running" {
		t.Fatalf("running cancellation = (%v, %q, %v)", requested.Invocation().State(), requested.Payload().Bytes(), err)
	}
	runningAfter := fixture.row(t, runningID)
	if runningAfter.state != jobs.InvocationCancelRequested || !bytes.Equal(runningAfter.leaseOwner, runningBefore.leaseOwner) || !bytes.Equal(runningAfter.leaseToken, runningBefore.leaseToken) || runningAfter.leaseEpoch != runningBefore.leaseEpoch || !sameNullTime(runningAfter.leaseExpiresAt, runningBefore.leaseExpiresAt) || runningAfter.recordExpiresAt.Valid || runningAfter.intentExpiresAt.Valid {
		t.Fatalf("running cancellation row = %+v, before %+v", runningAfter, runningBefore)
	}
	renewRequest, err := jobs.NewRenewRequest([]jobs.LeaseRef{runningLease}, jobs.MinimumLeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	renewResult, err := fixture.driver.Renew(ctx, renewRequest)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := jobs.ValidateRenewResult(fixture.driver.Description(), renewRequest, renewResult)
	if err != nil || renewed.Len() != 1 || renewed.Items()[0].Mutation() != jobs.DeliveryMutationApplied || renewed.Items()[0].Control() != jobs.DeliveryControlCancelRequested {
		t.Fatalf("cancel renewal = (%v, %v)", renewed, err)
	}
	currentLease := renewed.Items()[0].Current()
	beforeTerminate := fixture.row(t, runningID)
	terminated, err := fixture.driver.Terminate(ctx, runningID)
	if err != nil || terminated.Invocation().State() != jobs.InvocationTerminated || len(terminated.Invocation().Attempts()) != 1 || terminated.Invocation().Attempts()[0].Disposition().Kind() != jobs.DispositionTerminated {
		t.Fatalf("running termination = (%v, %+v, %v)", terminated.Invocation().State(), terminated.Invocation().Attempts(), err)
	}
	afterTerminate := fixture.row(t, runningID)
	if afterTerminate.state != jobs.InvocationTerminated || len(afterTerminate.leaseOwner) != 0 || len(afterTerminate.leaseToken) != 0 || afterTerminate.leaseExpiresAt.Valid || afterTerminate.leaseEpoch != beforeTerminate.leaseEpoch+1 || !afterTerminate.recordExpiresAt.Valid || !afterTerminate.intentExpiresAt.Valid {
		t.Fatalf("termination row = %+v, before %+v", afterTerminate, beforeTerminate)
	}
	staleRenewRequest, err := jobs.NewRenewRequest([]jobs.LeaseRef{currentLease}, jobs.MinimumLeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	staleRenew, err := fixture.driver.Renew(ctx, staleRenewRequest)
	if err != nil || staleRenew.Items()[0].Mutation() != jobs.DeliveryMutationLeaseLost || staleRenew.Items()[0].Control() != jobs.DeliveryControlTerminated {
		t.Fatalf("stale renewal = (%v, %v, %v)", staleRenew.Items()[0].Mutation(), staleRenew.Items()[0].Control(), err)
	}
	progress, err := jobs.ProgressCommand(currentLease)
	if err != nil {
		t.Fatal(err)
	}
	staleProgress := postgresControlApply(t, ctx, fixture.driver, progress)
	if staleProgress.Result().Mutation() != jobs.DeliveryMutationLeaseLost || staleProgress.Result().Control() != jobs.DeliveryControlTerminated {
		t.Fatalf("stale progress = (%v, %v)", staleProgress.Result().Mutation(), staleProgress.Result().Control())
	}
	nextRunning, err := jobs.Enqueue(ctx, fixture.queue, fixture.definition, "replacement", jobs.Unique("postgres-control-running"), jobs.AtPriority(1))
	if err != nil || nextRunning == runningID {
		t.Fatalf("released running unique = (%v, %v), old %v", nextRunning, err, runningID)
	}
	if _, err := fixture.driver.Terminate(ctx, runningID); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("repeated termination = %v", err)
	}
}

func TestPostgresControllerKeepsOnceIntent(t *testing.T) {
	fixture := newPostgresControllerFixture(t, "once")
	id, outcome, err := jobs.EnqueueOnce(fixture.ctx, fixture.queue, fixture.definition, jobs.Intent("postgres-control-once"), "once")
	if err != nil || outcome != jobs.EnqueueCreated {
		t.Fatalf("once enqueue = (%v, %v, %v)", id, outcome, err)
	}
	if _, err := fixture.driver.Cancel(fixture.ctx, id); err != nil {
		t.Fatal(err)
	}
	if count := fixture.intentCount(t, id); count != 1 {
		t.Fatalf("once intent rows = %d", count)
	}
	existing, outcome, err := jobs.EnqueueOnce(fixture.ctx, fixture.queue, fixture.definition, jobs.Intent("postgres-control-once"), "once")
	if err != nil || outcome != jobs.EnqueueExistingSamePayload || existing != id {
		t.Fatalf("once after cancellation = (%v, %v, %v), want %v", existing, outcome, err, id)
	}
}

func TestPostgresControllerUsesDatabaseTimeAfterLockWait(t *testing.T) {
	fixture := newPostgresControllerFixture(t, "clock")
	id, err := jobs.Enqueue(fixture.ctx, fixture.queue, fixture.definition, "blocked", jobs.Unique("postgres-control-clock"))
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := fixture.db.BeginTx(fixture.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	stored, err := fixture.repo.loadDelivery(fixture.ctx, blocker, fixture.namespace, id)
	if err != nil {
		t.Fatal(err)
	}
	type controlResult struct {
		view jobs.DeliveryView
		err  error
	}
	done := make(chan controlResult, 1)
	go func() {
		view, cancelErr := fixture.driver.Cancel(context.Background(), id)
		done <- controlResult{view: view, err: cancelErr}
	}()
	for _, key := range stored.intentKeys {
		adminWaitForIntentLock(t, fixture.ctx, fixture.db, fixture.repo, fixture.namespace, key)
	}
	var releasedAt time.Time
	if err := fixture.db.QueryRowContext(fixture.ctx, `SELECT clock_timestamp()`).Scan(&releasedAt); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || result.view.Invocation().FinishedAt().Before(releasedAt.Round(0).UTC()) {
		t.Fatalf("post-lock control time = (%v, %v), release %v", result.view.Invocation().FinishedAt(), result.err, releasedAt)
	}
}

func TestPostgresControllerRacesTerminationWithCompletion(t *testing.T) {
	fixture := newPostgresControllerFixture(t, "race")
	for index := 0; index < 12; index++ {
		id, err := jobs.Enqueue(fixture.ctx, fixture.queue, fixture.definition, fmt.Sprintf("race-%d", index), jobs.AtPriority(1))
		if err != nil {
			t.Fatal(err)
		}
		lease := adminClaimInvocation(t, fixture.ctx, fixture.driver, fixture.namespace, fixture.definition, id, byte(40+index))
		finish, err := jobs.FinishAttemptCommand(lease, jobs.SuccessDisposition(), 0, jobs.MinRetryDelay)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		terminated := make(chan error, 1)
		completed := make(chan jobs.ApplyResult, 1)
		completionErr := make(chan error, 1)
		go func() {
			<-start
			_, terminateErr := fixture.driver.Terminate(context.Background(), id)
			terminated <- terminateErr
		}()
		go func() {
			<-start
			request, requestErr := jobs.NewApplyRequest(finish)
			if requestErr != nil {
				completionErr <- requestErr
				return
			}
			result, applyErr := fixture.driver.Apply(context.Background(), request)
			if applyErr != nil {
				completionErr <- applyErr
				return
			}
			completed <- result
		}()
		close(start)
		terminateErr := <-terminated
		var completion jobs.ApplyResult
		select {
		case completion = <-completed:
		case err := <-completionErr:
			t.Fatal(err)
		}
		view, err := fixture.driver.Get(fixture.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		switch view.Invocation().State() {
		case jobs.InvocationTerminated:
			if terminateErr != nil || completion.Result().Mutation() != jobs.DeliveryMutationLeaseLost || completion.Result().Control() != jobs.DeliveryControlTerminated {
				t.Fatalf("termination winner = terminate %v completion (%v, %v)", terminateErr, completion.Result().Mutation(), completion.Result().Control())
			}
		case jobs.InvocationSucceeded:
			if !errors.Is(terminateErr, jobs.ErrConflict) || completion.Result().Mutation() != jobs.DeliveryMutationApplied {
				t.Fatalf("completion winner = terminate %v completion %v", terminateErr, completion.Result().Mutation())
			}
		default:
			t.Fatalf("race final state = %v", view.Invocation().State())
		}
	}
}

func newPostgresControllerFixture(t *testing.T, suffix string) postgresControllerFixture {
	t.Helper()
	dsn := os.Getenv("FROSTGROVE_JOBSPG_TEST_DSN")
	if dsn == "" {
		t.Skip("FROSTGROVE_JOBSPG_TEST_DSN is not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	namespace, err := jobs.NamespaceOf("jobspg-control-integration", suffix+fmt.Sprint(time.Now().UnixNano()))
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
	definition := postgresTestDefinition(t, "jobspg.control."+suffix)
	catalog := jobs.MustCatalog(definition)
	driver, err := Open(ctx, db, namespace, catalog)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver})
	if err != nil {
		t.Fatal(err)
	}
	return postgresControllerFixture{db: db, driver: driver, repo: repo, queue: queue, definition: definition, namespace: namespace, ctx: ctx}
}

func (fixture postgresControllerFixture) row(t *testing.T, id jobs.InvocationID) postgresControlRow {
	t.Helper()
	var rawState int
	var row postgresControlRow
	err := fixture.db.QueryRowContext(fixture.ctx, `SELECT state, lease_owner, lease_token, lease_epoch, lease_expires_at, record_expires_at, intent_expires_at FROM `+fixture.repo.deliveries+` WHERE namespace = $1 AND id = $2`, namespaceArgument(fixture.namespace), invocationArgument(id)).Scan(&rawState, &row.leaseOwner, &row.leaseToken, &row.leaseEpoch, &row.leaseExpiresAt, &row.recordExpiresAt, &row.intentExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	row.state = jobs.InvocationState(rawState)
	return row
}

func (fixture postgresControllerFixture) intentCount(t *testing.T, id jobs.InvocationID) int {
	t.Helper()
	var count int
	if err := fixture.db.QueryRowContext(fixture.ctx, `SELECT count(*) FROM `+fixture.repo.intents+` WHERE namespace = $1 AND invocation_id = $2`, namespaceArgument(fixture.namespace), invocationArgument(id)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func postgresControlBegin(t *testing.T, lease jobs.LeaseRef, definition *jobs.Definition[string], binding string) jobs.DeliveryCommand {
	t.Helper()
	target := postgresTestClaimTarget(t, definition, binding)
	command, err := jobs.BeginAttemptCommand(lease, target.Binding(), target.Build())
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func postgresControlClaim(t *testing.T, ctx context.Context, driver *Driver, namespace jobs.Namespace, definition *jobs.Definition[string], id jobs.InvocationID, marker byte) jobs.LeaseRef {
	t.Helper()
	target := postgresTestClaimTarget(t, definition, fmt.Sprintf("jobspg.control.claim.%d", marker))
	request, err := jobs.NewClaimRequest(jobs.ClaimRequestSpec{Namespace: namespace, Incarnation: postgresTestIncarnation(t, marker), Targets: []jobs.ClaimTarget{target}, MaxItems: 1, MaxBytes: jobs.MaxDeliveryRecordBytes, LeaseTTL: jobs.DefaultLeaseTTL})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := driver.Claim(ctx, request)
	if err != nil || batch.Len() != 1 || batch.Items()[0].Record().Genesis.ID != id {
		t.Fatalf("claim = (%d, %v)", batch.Len(), err)
	}
	return batch.Items()[0].Lease()
}

func postgresControlApply(t *testing.T, ctx context.Context, driver *Driver, command jobs.DeliveryCommand) jobs.ApplyResult {
	t.Helper()
	request, err := jobs.NewApplyRequest(command)
	if err != nil {
		t.Fatal(err)
	}
	result, err := driver.Apply(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := jobs.ValidateApplyResult(driver.Description(), request, result)
	if err != nil {
		t.Fatal(err)
	}
	return validated
}

func sameNullTime(left, right sql.NullTime) bool {
	return left.Valid == right.Valid && (!left.Valid || left.Time.Equal(right.Time))
}
