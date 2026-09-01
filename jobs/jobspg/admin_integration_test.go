//go:build integration

package jobspg

import (
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

func TestPostgresAdminGetListRedriveConflictAndPurge(t *testing.T) {
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
	namespace, err := jobs.NamespaceOf("jobspg-admin-integration", fmt.Sprint(time.Now().UnixNano()))
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
	regular := postgresTestDefinition(t, "jobspg.admin.regular")
	collapse := postgresTestDefinition(t, "jobspg.admin.collapse")
	claimLock := postgresTestDefinition(t, "jobspg.admin.claim-lock")
	unique := postgresTestDefinition(t, "jobspg.admin.unique")
	catalog := jobs.MustCatalog(regular, collapse, claimLock, unique)
	driver, err := Open(ctx, db, namespace, catalog)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver})
	if err != nil {
		t.Fatal(err)
	}
	regularID, err := jobs.Enqueue(ctx, queue, regular, "regular-payload")
	if err != nil {
		t.Fatal(err)
	}
	view, err := driver.Get(ctx, regularID)
	if err != nil || view.Invocation().State() != jobs.InvocationQueued || string(view.Payload().Bytes()) != "regular-payload" {
		t.Fatalf("Get queued = (%v, %q, %v)", view.Invocation().State(), view.Payload().Bytes(), err)
	}
	listed, err := driver.List(ctx, ListSpec{Definitions: []jobs.Name{regular.Name()}, States: []jobs.InvocationState{jobs.InvocationQueued}, Limit: 1})
	if err != nil || len(listed) != 1 || listed[0].Invocation().ID() != regularID {
		t.Fatalf("List queued = (%d, %v)", len(listed), err)
	}
	adminFinishInvocation(t, ctx, driver, namespace, regular, regularID, 1)
	redriven, err := driver.Redrive(ctx, regularID)
	if err != nil || redriven.Invocation().State() != jobs.InvocationQueued || len(redriven.Invocation().History()) != 1 {
		t.Fatalf("Redrive terminal = (%v, %v)", redriven.Invocation().State(), err)
	}
	if _, err := driver.Redrive(ctx, regularID); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("Redrive queued = %v", err)
	}
	adminFinishInvocation(t, ctx, driver, namespace, regular, regularID, 2)
	collapseID, err := jobs.Enqueue(ctx, queue, collapse, "first", jobs.Collapse("document:42"))
	if err != nil {
		t.Fatal(err)
	}
	lease := adminClaimInvocation(t, ctx, driver, namespace, collapse, collapseID, 3)
	competingID, err := jobs.Enqueue(ctx, queue, collapse, "second", jobs.Collapse("document:42"))
	if err != nil || competingID == collapseID {
		t.Fatalf("competing collapse = (%v, %v)", competingID, err)
	}
	adminFinishLease(t, ctx, driver, lease)
	if _, err := driver.Redrive(ctx, collapseID); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("Redrive with occupied collapse intent = %v", err)
	}
	conflicted, err := driver.Get(ctx, collapseID)
	if err != nil || !conflicted.Invocation().IsTerminal() {
		t.Fatalf("conflicted invocation = (%v, %v)", conflicted.Invocation().State(), err)
	}
	competing, err := driver.Get(ctx, competingID)
	if err != nil || competing.Invocation().State() != jobs.InvocationQueued {
		t.Fatalf("competing invocation = (%v, %v)", competing.Invocation().State(), err)
	}
	restorableID, err := jobs.Enqueue(ctx, queue, collapse, "restorable", jobs.Collapse("document:43"), jobs.AtPriority(1))
	if err != nil {
		t.Fatal(err)
	}
	adminFinishInvocation(t, ctx, driver, namespace, collapse, restorableID, 4)
	if _, err := driver.Redrive(ctx, restorableID); err != nil {
		t.Fatalf("Redrive released collapse intent = %v", err)
	}
	collapsedID, err := jobs.Enqueue(ctx, queue, collapse, "replacement", jobs.Collapse("document:43"))
	if err != nil || collapsedID != restorableID {
		t.Fatalf("restored collapse reservation = (%v, %v), want %v", collapsedID, err, restorableID)
	}
	rolling, err := jobs.NewIntentDigestPlan(jobs.DigestRevision2, jobs.DigestRevision1)
	if err != nil {
		t.Fatal(err)
	}
	rolling, err = jobs.WithLegacyIntentCompatibility(rolling)
	if err != nil {
		t.Fatal(err)
	}
	rollingQueue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver, Digests: rolling})
	if err != nil {
		t.Fatal(err)
	}
	claimLockID, err := jobs.Enqueue(ctx, rollingQueue, claimLock, "claim lock", jobs.Collapse("document:claim-lock"))
	if err != nil {
		t.Fatal(err)
	}
	adminAssertClaimPlacementLockOrder(t, ctx, db, repo, driver, rollingQueue, namespace, claimLock, claimLockID)
	uniqueID, err := jobs.Enqueue(ctx, queue, unique, "first", jobs.Unique("sweeper:42"))
	if err != nil {
		t.Fatal(err)
	}
	queuedUnique, err := jobs.Enqueue(ctx, queue, unique, "queued replacement", jobs.Unique("sweeper:42"))
	if err != nil || queuedUnique != uniqueID {
		t.Fatalf("queued unique duplicate = (%v, %v), want %v", queuedUnique, err, uniqueID)
	}
	uniqueLease := adminClaimInvocation(t, ctx, driver, namespace, unique, uniqueID, 5)
	runningUnique, err := jobs.Enqueue(ctx, queue, unique, "running replacement", jobs.Unique("sweeper:42"))
	if err != nil || runningUnique != uniqueID {
		t.Fatalf("running unique duplicate = (%v, %v), want %v", runningUnique, err, uniqueID)
	}
	uniqueView, err := driver.Get(ctx, uniqueID)
	if err != nil || string(uniqueView.Payload().Bytes()) != "first" {
		t.Fatalf("unique payload = (%q, %v)", uniqueView.Payload().Bytes(), err)
	}
	adminFinishLease(t, ctx, driver, uniqueLease)
	adminAssertRedrivePlacementLockOrder(t, ctx, db, repo, driver, queue, namespace, unique, uniqueID)
	adminFinishInvocation(t, ctx, driver, namespace, unique, uniqueID, 6)
	nextUnique, err := jobs.Enqueue(ctx, queue, unique, "next", jobs.Unique("sweeper:42"))
	if err != nil || nextUnique == uniqueID {
		t.Fatalf("post-terminal unique = (%v, %v), old %v", nextUnique, err, uniqueID)
	}
	if _, err := driver.Redrive(ctx, uniqueID); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("Redrive with occupied unique intent = %v", err)
	}
	onceID, _, err := jobs.EnqueueOnce(ctx, queue, regular, jobs.Intent("purge-lock"), "once")
	if err != nil {
		t.Fatal(err)
	}
	adminFinishInvocation(t, ctx, driver, namespace, regular, onceID, 7)
	purged, err := driver.PurgeTerminal(ctx, time.Now().Add(time.Hour), 10)
	if err != nil || purged != 4 {
		t.Fatalf("PurgeTerminal = (%d, %v)", purged, err)
	}
	for _, id := range []jobs.InvocationID{regularID, collapseID, uniqueID, onceID} {
		if _, err := driver.Get(ctx, id); !errors.Is(err, jobs.ErrInvocationNotFound) {
			t.Fatalf("Get purged %v = %v", id, err)
		}
	}
	if _, err := driver.Get(ctx, competingID); err != nil {
		t.Fatalf("PurgeTerminal removed queued invocation: %v", err)
	}
}

func adminAssertClaimPlacementLockOrder(t *testing.T, ctx context.Context, db *sql.DB, repo repository, driver *Driver, queue *jobs.Queue, namespace jobs.Namespace, definition *jobs.Definition[string], id jobs.InvocationID) {
	t.Helper()
	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	intents, err := repo.intentKeysForDeliveries(ctx, blocker, namespace, []jobs.InvocationID{id})
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 2 {
		t.Fatalf("rolling collapse intents = %d", len(intents))
	}
	if err := repo.lockIntentRowsForDeliveries(ctx, blocker, namespace, []jobs.InvocationID{id}); err != nil {
		t.Fatal(err)
	}
	target := postgresTestClaimTarget(t, definition, "jobspg.admin.claim-lock")
	request, err := jobs.NewClaimRequest(jobs.ClaimRequestSpec{Namespace: namespace, Incarnation: postgresTestIncarnation(t, 8), Targets: []jobs.ClaimTarget{target}, MaxItems: 1, MaxBytes: jobs.MaxDeliveryRecordBytes, LeaseTTL: jobs.DefaultLeaseTTL})
	if err != nil {
		t.Fatal(err)
	}
	type claimResult struct {
		batch jobs.ClaimBatch
		err   error
	}
	type placementResult struct {
		id  jobs.InvocationID
		err error
	}
	claimDone := make(chan claimResult, 1)
	go func() {
		batch, claimErr := driver.Claim(ctx, request)
		claimDone <- claimResult{batch: batch, err: claimErr}
	}()
	for _, intent := range intents {
		adminWaitForIntentLock(t, ctx, db, repo, namespace, intent)
	}
	placementDone := make(chan placementResult, 1)
	go func() {
		placed, placementErr := jobs.Enqueue(ctx, queue, definition, "claim replacement", jobs.Collapse("document:claim-lock"))
		placementDone <- placementResult{id: placed, err: placementErr}
	}()
	select {
	case placed := <-placementDone:
		t.Fatalf("collapse placement bypassed claim intent locks: (%v, %v)", placed.id, placed.err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	claimed := <-claimDone
	if claimed.err != nil || claimed.batch.Len() != 1 || claimed.batch.Items()[0].Record().Genesis.ID != id {
		t.Fatalf("claim = (%d, %v)", claimed.batch.Len(), claimed.err)
	}
	placed := <-placementDone
	if placed.err != nil || placed.id == id {
		t.Fatalf("collapse replacement = (%v, %v), old %v", placed.id, placed.err, id)
	}
}

func adminAssertRedrivePlacementLockOrder(t *testing.T, ctx context.Context, db *sql.DB, repo repository, driver *Driver, queue *jobs.Queue, namespace jobs.Namespace, definition *jobs.Definition[string], id jobs.InvocationID) {
	t.Helper()
	terminal, err := driver.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err := repo.lockDeliveryRecord(ctx, blocker, namespace, id); err != nil {
		t.Fatal(err)
	}
	type redriveResult struct {
		view jobs.DeliveryView
		err  error
	}
	type placementResult struct {
		id  jobs.InvocationID
		err error
	}
	redriveDone := make(chan redriveResult, 1)
	go func() {
		view, redriveErr := driver.Redrive(ctx, id)
		redriveDone <- redriveResult{view: view, err: redriveErr}
	}()
	adminWaitForIntentLock(t, ctx, db, repo, namespace, terminal.Invocation().Intent())
	placementDone := make(chan placementResult, 1)
	go func() {
		placed, placementErr := jobs.Enqueue(ctx, queue, definition, "redrive replacement", jobs.Unique("sweeper:42"))
		placementDone <- placementResult{id: placed, err: placementErr}
	}()
	var early *placementResult
	select {
	case result := <-placementDone:
		early = &result
	case <-time.After(150 * time.Millisecond):
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	redriven := <-redriveDone
	placed := placementResult{}
	if early != nil {
		placed = *early
	} else {
		placed = <-placementDone
	}
	if early != nil {
		t.Fatalf("unique placement bypassed a redrive waiting on its delivery lock: (%v, %v)", placed.id, placed.err)
	}
	if redriven.err != nil || redriven.view.Invocation().State() != jobs.InvocationQueued {
		t.Fatalf("Redrive released unique intent = (%v, %v)", redriven.view.Invocation().State(), redriven.err)
	}
	if placed.err != nil || placed.id != id {
		t.Fatalf("restored unique reservation = (%v, %v), want %v", placed.id, placed.err, id)
	}
}

func adminWaitForIntentLock(t *testing.T, ctx context.Context, db *sql.DB, repo repository, namespace jobs.Namespace, key jobs.IntentKey) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		probe, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		probeCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		err = repo.lockIntentKeys(probeCtx, probe, namespace, []jobs.IntentKey{key})
		cancel()
		_ = probe.Rollback()
		if errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("operation did not acquire the intent lock")
}

func adminFinishInvocation(t *testing.T, ctx context.Context, driver *Driver, namespace jobs.Namespace, definition *jobs.Definition[string], id jobs.InvocationID, marker byte) {
	t.Helper()
	lease := adminClaimInvocation(t, ctx, driver, namespace, definition, id, marker)
	adminFinishLease(t, ctx, driver, lease)
}

func adminClaimInvocation(t *testing.T, ctx context.Context, driver *Driver, namespace jobs.Namespace, definition *jobs.Definition[string], id jobs.InvocationID, marker byte) jobs.LeaseRef {
	t.Helper()
	target := postgresTestClaimTarget(t, definition, fmt.Sprintf("jobspg.admin.worker.%d", marker))
	request, err := jobs.NewClaimRequest(jobs.ClaimRequestSpec{Namespace: namespace, Incarnation: postgresTestIncarnation(t, marker), Targets: []jobs.ClaimTarget{target}, MaxItems: 1, MaxBytes: jobs.MaxDeliveryRecordBytes, LeaseTTL: jobs.DefaultLeaseTTL})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := driver.Claim(ctx, request)
	if err != nil || batch.Len() != 1 {
		t.Fatalf("Claim = (%d, %v)", batch.Len(), err)
	}
	item := batch.Items()[0]
	if item.Record().Genesis.ID != id {
		t.Fatalf("claimed %v, want %v", item.Record().Genesis.ID, id)
	}
	begin, err := jobs.BeginAttemptCommand(item.Lease(), target.Binding(), target.Build())
	if err != nil {
		t.Fatal(err)
	}
	apply, err := jobs.NewApplyRequest(begin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Apply(ctx, apply); err != nil {
		t.Fatal(err)
	}
	return item.Lease()
}

func adminFinishLease(t *testing.T, ctx context.Context, driver *Driver, lease jobs.LeaseRef) {
	t.Helper()
	finish, err := jobs.FinishAttemptCommand(lease, jobs.SuccessDisposition(), 0, jobs.DefaultRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	apply, err := jobs.NewApplyRequest(finish)
	if err != nil {
		t.Fatal(err)
	}
	result, err := driver.Apply(ctx, apply)
	if err != nil || result.Application().Invocation().State() != jobs.InvocationSucceeded {
		t.Fatalf("finish = (%v, %v)", result.Application().Invocation().State(), err)
	}
}
