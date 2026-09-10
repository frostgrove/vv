//go:build integration

package auditpg

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/audit/audittest"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

type liveExactCallerKey struct{}

func TestPostgresExactHistoryConformance(t *testing.T) {
	dsn := requiredLiveDSN(t)
	audittest.ExactHistory(t, func(ctx context.Context, request audittest.HistoryStoreRequest) (audittest.HistoryStore, error) {
		suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
		schema := Schema{Name: "auditpg_exact_conformance_" + suffix}
		database := openLiveDB(t, dsnWithApplicationName(t, dsn, "auditpg_exact_conformance_"+suffix), 8)
		source := crudsql.Postgres(database)
		deployment, err := NewDeployment(DeploymentSpec{
			Runtime: Spec{DB: database, Source: source, Schema: schema}, SchemaManagement: ManageSchema,
		})
		if err != nil {
			_ = database.Close()
			return audittest.HistoryStore{}, err
		}
		if err := deployment.Prepare(ctx); err != nil {
			_ = deployment.Close()
			_ = database.Close()
			return audittest.HistoryStore{}, err
		}
		for _, manifest := range request.Catalogs.Manifests() {
			if err := deployment.InstallCatalog(ctx, manifest, request.Change); err != nil {
				_ = deployment.Close()
				_ = database.Close()
				return audittest.HistoryStore{}, err
			}
		}
		active := request.Catalogs.Active()
		proof, err := audit.NoAttemptCatalogActivation(audit.CatalogRef{}, active)
		if err != nil {
			_ = deployment.Close()
			_ = database.Close()
			return audittest.HistoryStore{}, err
		}
		if err := deployment.ActivateCatalog(ctx, audit.CatalogRef{}, active, request.Change, proof); err != nil {
			_ = deployment.Close()
			_ = database.Close()
			return audittest.HistoryStore{}, err
		}
		if err := deployment.VerifyCatalogs(ctx, request.Catalogs.Manifests()); err != nil {
			_ = deployment.Close()
			_ = database.Close()
			return audittest.HistoryStore{}, err
		}
		store, err := New(Spec{DB: database, Source: source, Schema: schema})
		if err != nil {
			_ = deployment.Close()
			_ = database.Close()
			return audittest.HistoryStore{}, err
		}
		if err := store.Check(ctx); err != nil {
			_ = store.Close()
			_ = deployment.Close()
			_ = database.Close()
			return audittest.HistoryStore{}, err
		}
		return audittest.HistoryStore{
			Writer: store, Log: store, Exact: store,
			Close: func() error {
				storeErr := store.Close()
				deploymentErr := deployment.Close()
				_, dropErr := database.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+quoteIdentifier(schema.Name)+` CASCADE`)
				databaseErr := database.Close()
				return errors.Join(storeErr, deploymentErr, dropErr, databaseErr)
			},
		}, nil
	})
}

func TestPostgresExactHistorySurvivesRestartAndRejectsCorruptEvidence(t *testing.T) {
	dsn := requiredLiveDSN(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	schema := Schema{Name: "auditpg_exact_" + suffix}
	defer dropLiveSchema(t, dsn, schema)
	ctx := context.Background()
	runtime := newRestartSearchRuntime(t)

	firstDatabase := openLiveDB(t, dsnWithApplicationName(t, dsn, "auditpg_exact_before_"+suffix), 4)
	firstSource := crudsql.Postgres(firstDatabase)
	firstDeployment, err := NewDeployment(DeploymentSpec{
		Runtime: Spec{DB: firstDatabase, Source: firstSource, Schema: schema}, SchemaManagement: ManageSchema,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := firstDeployment.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	installAndActivateLiveCatalog(t, ctx, firstDeployment, runtime.catalog, "exact-before-"+suffix)
	if err := firstDeployment.VerifyCatalogs(ctx, runtime.catalogs.Manifests()); err != nil {
		t.Fatal(err)
	}
	firstStore, err := New(Spec{DB: firstDatabase, Source: firstSource, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if err := firstStore.Check(ctx); err != nil {
		t.Fatal(err)
	}
	backingID, logID := firstStore.BackingID(), firstStore.LogID()
	firstRecorder := newRestartSearchRecorder(t, firstStore, runtime)
	receipt := recordLiveExactEvent(t, ctx, firstRecorder, runtime.event)
	if err := firstStore.Close(); err != nil {
		t.Fatal(err)
	}
	if err := firstDeployment.Close(); err != nil {
		t.Fatal(err)
	}
	if err := firstDatabase.Close(); err != nil {
		t.Fatal(err)
	}

	freshDatabase := openLiveDB(t, dsnWithApplicationName(t, dsn, "auditpg_exact_after_"+suffix), 4)
	defer freshDatabase.Close()
	freshSource := crudsql.Postgres(freshDatabase)
	freshDeployment, err := NewDeployment(DeploymentSpec{
		Runtime: Spec{DB: freshDatabase, Source: freshSource, Schema: schema}, SchemaManagement: VerifySchema,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer freshDeployment.Close()
	if err := freshDeployment.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := freshDeployment.VerifyCatalogs(ctx, runtime.catalogs.Manifests()); err != nil {
		t.Fatal(err)
	}
	freshStore, err := New(Spec{DB: freshDatabase, Source: freshSource, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	defer freshStore.Close()
	if err := freshStore.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if freshStore.BackingID() != backingID || freshStore.LogID() != logID {
		t.Fatalf("restart changed exact store identity: backing=%x/%x log=%x/%x", backingID, freshStore.BackingID(), logID, freshStore.LogID())
	}
	freshRecorder := newRestartSearchRecorder(t, freshStore, runtime)
	var callerValueLeaked atomic.Bool
	history := newLiveExactHistory(t, freshStore, freshRecorder, 1<<20, &callerValueLeaked)
	caller := context.WithValue(ctx, liveExactCallerKey{}, "must-not-reach-collaborators")
	reference := audit.RevisionRef{Catalog: runtime.catalog.Ref(), Revision: receipt.RevisionID()}
	query := liveExactAccessQuery()

	revisionResult, err := history.Revision(caller, reference, query)
	if err != nil {
		t.Fatal(err)
	}
	revision := revisionResult.Revision()
	if revision.Ref != reference || len(revision.Items) != 1 || revision.Items[0].Action != "invoice.searched" || len(revision.Items[0].Values) != 1 || revision.Items[0].Values[0].Knowledge != audit.FieldKnown || string(revision.Items[0].Values[0].Canonical) != "exact-value" {
		t.Fatalf("exact revision after restart = %+v", revision)
	}
	itemResult, err := history.Item(caller, audit.ItemRef{Revision: reference, Ordinal: 0}, query)
	if err != nil {
		t.Fatal(err)
	}
	if item := itemResult.Item(); item.Ordinal != 0 || item.Action != "invoice.searched" || len(item.Values) != 1 || string(item.Values[0].Canonical) != "exact-value" {
		t.Fatalf("exact item after restart = %+v", item)
	}
	missing := reference
	missing.Revision[0] ^= 0xff
	if _, err := history.Revision(caller, missing, query); !errors.Is(err, audit.ErrNotFound) {
		t.Fatalf("missing exact revision = %v", err)
	}
	if _, err := history.Item(caller, audit.ItemRef{Revision: reference, Ordinal: 1}, query); !errors.Is(err, audit.ErrNotFound) {
		t.Fatalf("missing exact item = %v", err)
	}
	if callerValueLeaked.Load() {
		t.Fatal("exact access authority received caller context values")
	}

	tooSmall := newLiveExactHistory(t, freshStore, freshRecorder, 1, nil)
	if _, err := tooSmall.Revision(ctx, reference, query); !errors.Is(err, audit.ErrRefused) {
		t.Fatalf("undersized exact grant = %v", err)
	}

	if _, err := freshDatabase.ExecContext(ctx, `DROP TRIGGER revisions_immutable ON `+quoteIdentifier(schema.Name)+`.revisions`); err != nil {
		t.Fatal(err)
	}
	if _, err := freshDatabase.ExecContext(ctx, `UPDATE `+quoteIdentifier(schema.Name)+`.revisions SET wire=$1 WHERE revision_id=$2`, []byte{0xff}, reference.Revision[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Revision(ctx, reference, query); !errors.Is(err, audit.ErrIntegrity) {
		t.Fatalf("corrupt exact revision = %v", err)
	}
}

func recordLiveExactEvent(t *testing.T, ctx context.Context, recorder *audit.Recorder, event *audit.EventType[restartSearchOccurrence]) audit.Receipt {
	t.Helper()
	draft, err := event.New(restartSearchOccurrence{
		Target: "invoice:exact", Value: "exact-value", At: time.Unix(2_100_000_001, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := recorder.Record(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok := result.Receipt()
	if !ok || receipt.Disposition() != audit.Inserted || receipt.Settlement() != audit.Committed {
		t.Fatalf("exact fixture receipt = (%+v, %v)", receipt, ok)
	}
	return receipt
}

func newLiveExactHistory(t *testing.T, store *Store, recorder *audit.Recorder, maxBytes uint64, leaked *atomic.Bool) *audit.History {
	t.Helper()
	authority := audit.AccessAuthorityFunc(func(ctx context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
		if ctx.Value(liveExactCallerKey{}) != nil && leaked != nil {
			leaked.Store(true)
		}
		view := request.View()
		grant := audit.AccessGrantSpec{
			Roles: []audit.Reference{view.Query.Role}, Catalogs: view.Catalogs,
			Resources: view.Target.Resources, Actions: view.Query.Actions,
			Fields: view.Query.Fields.Fields, Context: view.Query.Context.Facts,
			Classifications: []audit.Classification{audit.Public}, Direction: view.Query.Direction,
			ExpiresAt:    time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
			MaxRevisions: view.Query.Limit, MaxPages: 1, MaxBytes: maxBytes,
		}
		if view.Query.Scope.Kind == audit.ScopeExact {
			grant.Scopes = []audit.ScopedReference{view.Query.Scope.Reference}
		}
		return audit.AllowAccess(request, grant)
	})
	history, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: store, Exact: store, Access: authority,
	})
	if err != nil {
		t.Fatal(err)
	}
	return history
}

func liveExactAccessQuery() audit.ExactAccessQuery {
	return audit.ExactAccessQuery{
		Resources: []audit.Resource{"billing.invoice"}, Classifications: []audit.Classification{audit.Public},
		Query: audit.Query{
			Purpose: "history.read", Role: "auditor", Scope: audit.CurrentScope(),
			Actions: []audit.Action{"invoice.searched"}, Fields: audit.AllFields(), Context: audit.AllContext(),
		},
	}
}
