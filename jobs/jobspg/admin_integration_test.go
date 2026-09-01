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
	catalog := jobs.MustCatalog(regular, collapse)
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
	purged, err := driver.PurgeTerminal(ctx, time.Now().Add(time.Hour), 10)
	if err != nil || purged != 2 {
		t.Fatalf("PurgeTerminal = (%d, %v)", purged, err)
	}
	for _, id := range []jobs.InvocationID{regularID, collapseID} {
		if _, err := driver.Get(ctx, id); !errors.Is(err, jobs.ErrInvocationNotFound) {
			t.Fatalf("Get purged %v = %v", id, err)
		}
	}
	if _, err := driver.Get(ctx, competingID); err != nil {
		t.Fatalf("PurgeTerminal removed queued invocation: %v", err)
	}
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
