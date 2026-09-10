//go:build integration

package auditpg

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/audit/audittest"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

type restartSearchOccurrence struct {
	Target string
	Value  string
	At     time.Time
}

type restartSearchRuntime struct {
	event      *audit.EventType[restartSearchOccurrence]
	catalog    *audit.Catalog
	catalogs   *audit.CatalogSet
	semantic   audit.SemanticDigester
	identities audit.IdentityKeyring
	resolver   audit.ContextResolver
	clock      *restartSearchClock
}

type restartSearchClock struct{ next time.Time }

func (c *restartSearchClock) Now() time.Time {
	result := c.next
	c.next = c.next.Add(time.Second)
	return result
}

func TestPostgresBasicHistoryConformance(t *testing.T) {
	dsn := requiredLiveDSN(t)
	audittest.BasicHistory(t, func(ctx context.Context, request audittest.HistoryStoreRequest) (audittest.HistoryStore, error) {
		suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
		schema := Schema{Name: "auditpg_history_" + suffix}
		db := openLiveDB(t, dsnWithApplicationName(t, dsn, "auditpg_history_"+suffix), 4)
		source := crudsql.Postgres(db)
		deployment, err := NewDeployment(DeploymentSpec{
			Runtime: Spec{DB: db, Source: source, Schema: schema}, SchemaManagement: ManageSchema,
		})
		if err != nil {
			_ = db.Close()
			return audittest.HistoryStore{}, err
		}
		if err := deployment.Prepare(ctx); err != nil {
			_ = deployment.Close()
			_ = db.Close()
			return audittest.HistoryStore{}, err
		}
		for _, manifest := range request.Catalogs.Manifests() {
			if err := deployment.InstallCatalog(ctx, manifest, request.Change); err != nil {
				_ = deployment.Close()
				_ = db.Close()
				return audittest.HistoryStore{}, err
			}
		}
		active := request.Catalogs.Active()
		proof, err := audit.NoAttemptCatalogActivation(audit.CatalogRef{}, active)
		if err != nil {
			_ = deployment.Close()
			_ = db.Close()
			return audittest.HistoryStore{}, err
		}
		if err := deployment.ActivateCatalog(ctx, audit.CatalogRef{}, active, request.Change, proof); err != nil {
			_ = deployment.Close()
			_ = db.Close()
			return audittest.HistoryStore{}, err
		}
		if err := deployment.VerifyCatalogs(ctx, request.Catalogs.Manifests()); err != nil {
			_ = deployment.Close()
			_ = db.Close()
			return audittest.HistoryStore{}, err
		}
		store, err := New(Spec{DB: db, Source: source, Schema: schema})
		if err != nil {
			_ = deployment.Close()
			_ = db.Close()
			return audittest.HistoryStore{}, err
		}
		if err := store.Check(ctx); err != nil {
			_ = store.Close()
			_ = deployment.Close()
			_ = db.Close()
			return audittest.HistoryStore{}, err
		}
		return audittest.HistoryStore{
			Writer: store,
			Log:    store,
			AmbientTransaction: func(ctx context.Context) (context.Context, func() error, error) {
				tx, err := source.Begin(ctx)
				if err != nil {
					return nil, nil, err
				}
				return crud.BindExecutor(ctx, source, tx), func() error { return tx.Rollback(context.Background()) }, nil
			},
			Close: func() error {
				storeErr := store.Close()
				deploymentErr := deployment.Close()
				_, dropErr := db.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+quoteIdentifier(schema.Name)+` CASCADE`)
				dbErr := db.Close()
				return errors.Join(storeErr, deploymentErr, dropErr, dbErr)
			},
		}, nil
	})
}

func TestPostgresHistorySearchSurvivesRestart(t *testing.T) {
	dsn := requiredLiveDSN(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	schema := Schema{Name: "auditpg_search_restart_" + suffix}
	defer dropLiveSchema(t, dsn, schema)
	ctx := context.Background()
	runtime := newRestartSearchRuntime(t)

	firstDB := openLiveDB(t, dsnWithApplicationName(t, dsn, "auditpg_search_before_"+suffix), 4)
	defer firstDB.Close()
	firstSource := crudsql.Postgres(firstDB)
	firstDeployment, err := NewDeployment(DeploymentSpec{
		Runtime: Spec{DB: firstDB, Source: firstSource, Schema: schema}, SchemaManagement: ManageSchema,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := firstDeployment.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	installAndActivateLiveCatalog(t, ctx, firstDeployment, runtime.catalog, "search-before-"+suffix)
	if err := firstDeployment.VerifyCatalogs(ctx, runtime.catalogs.Manifests()); err != nil {
		t.Fatal(err)
	}
	firstStore, err := New(Spec{DB: firstDB, Source: firstSource, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if err := firstStore.Check(ctx); err != nil {
		t.Fatal(err)
	}
	backingID, logID := firstStore.BackingID(), firstStore.LogID()
	firstRecorder := newRestartSearchRecorder(t, firstStore, runtime)
	recordRestartSearchEvent(t, ctx, firstRecorder, runtime.event, "invoice:a", "before-a", 1)
	recordRestartSearchEvent(t, ctx, firstRecorder, runtime.event, "invoice:b", "before-b", 2)
	if err := firstStore.Close(); err != nil {
		t.Fatal(err)
	}
	if err := firstDeployment.Close(); err != nil {
		t.Fatal(err)
	}
	if err := firstDB.Close(); err != nil {
		t.Fatal(err)
	}

	freshDB := openLiveDB(t, dsnWithApplicationName(t, dsn, "auditpg_search_after_"+suffix), 4)
	defer freshDB.Close()
	freshSource := crudsql.Postgres(freshDB)
	freshDeployment, err := NewDeployment(DeploymentSpec{
		Runtime: Spec{DB: freshDB, Source: freshSource, Schema: schema}, SchemaManagement: VerifySchema,
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
	freshStore, err := New(Spec{DB: freshDB, Source: freshSource, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	defer freshStore.Close()
	if err := freshStore.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if freshStore.BackingID() != backingID || freshStore.LogID() != logID {
		t.Fatalf("restart changed search identity: backing=%x/%x log=%x/%x", backingID, freshStore.BackingID(), logID, freshStore.LogID())
	}
	history := newRestartSearchHistory(t, freshStore, runtime)
	query := audit.Query{
		Purpose: "history.read", Role: "auditor", Scope: audit.CurrentScope(),
		Fields: audit.AllFields(), Context: audit.AllContext(), Direction: audit.OldestFirst, Limit: 10,
	}
	page, err := runtime.event.History(history).Events(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if got := restartSearchValues(page); !slices.Equal(got, []string{"before-a", "before-b"}) {
		t.Fatalf("history after restart = %v", got)
	}
	freshRecorder := newRestartSearchRecorder(t, freshStore, runtime)
	recordRestartSearchEvent(t, ctx, freshRecorder, runtime.event, "invoice:c", "after-c", 3)
	page, err = runtime.event.History(history).Events(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if got := restartSearchValues(page); !slices.Equal(got, []string{"before-a", "before-b", "after-c"}) {
		t.Fatalf("history after restart append = %v", got)
	}
}

func newRestartSearchRuntime(t *testing.T) restartSearchRuntime {
	t.Helper()
	semantic, err := audit.HMACSemanticDigester("search-live-semantic", bytes.Repeat([]byte{41}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{KeyID: "search-live-identity", Key: bytes.Repeat([]byte{42}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	contextPolicy := audit.ContextFacts(
		audit.ScopeFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
		audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
	)
	event := audit.Declare(audit.EventPolicy[restartSearchOccurrence]{
		Semantics: audit.Semantics(1, audit.PolicyGolden("auditpg.search.restart", strings.Repeat("0f", 32))),
		Descriptor: audit.Descriptor{
			Resource: "billing.invoice", Action: "invoice.searched", Owner: "auditpg.team", Purpose: "history.read",
			Retention: "business.forever", Consequence: audit.Required, Context: contextPolicy,
		},
		Target:     audit.EventTarget(func(value restartSearchOccurrence) audit.Reference { return audit.Reference(value.Target) }, audit.Public, audit.AsPlaintext),
		Outcome:    audit.EventOutcome(audit.Outcomes("accepted"), func(restartSearchOccurrence) audit.Outcome { return "accepted" }),
		OccurredAt: audit.EventOccurredAt(func(value restartSearchOccurrence) time.Time { return value.At }),
		Fields: audit.EventFields(
			audit.EventValue("value", func(value restartSearchOccurrence) string { return value.Value }, audit.Text(), audit.Public),
		),
	})
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "search.restart", Owner: "auditpg.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(), Integrity: audit.IntegrityOnly(),
	}, event)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := audit.Lineage(catalog)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := audit.NewContextValue(audit.ScopedReference{Scope: "tenant", Reference: "north"}, audit.Verified)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := audit.NewContextValue(audit.OperationID{71}, audit.Verified)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := audit.StaticContext(audit.Context{Scope: scope, Operation: operation})
	if err != nil {
		t.Fatal(err)
	}
	return restartSearchRuntime{
		event: event, catalog: catalog, catalogs: catalogs, semantic: semantic, identities: identities,
		resolver: resolver, clock: &restartSearchClock{next: time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
}

func newRestartSearchRecorder(t *testing.T, store *Store, runtime restartSearchRuntime) *audit.Recorder {
	t.Helper()
	recorder, err := audit.New(audit.Config{
		Catalogs: runtime.catalogs, Writer: store, Context: runtime.resolver,
		Semantics: runtime.semantic, Identities: runtime.identities, Clock: runtime.clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	return recorder
}

func newRestartSearchHistory(t *testing.T, store *Store, runtime restartSearchRuntime) *audit.History {
	t.Helper()
	recorder := newRestartSearchRecorder(t, store, runtime)
	authority := audit.AccessAuthorityFunc(func(_ context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
		view := request.View()
		grant := audit.AccessGrantSpec{
			Roles: []audit.Reference{view.Query.Role}, Catalogs: view.Catalogs,
			Resources: view.Target.Resources, Actions: view.Query.Actions,
			Fields: view.Query.Fields.Fields, Context: view.Query.Context.Facts,
			Classifications: []audit.Classification{audit.Public}, Direction: view.Query.Direction,
			ExpiresAt: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), MaxRevisions: view.Query.Limit,
			MaxPages: 1, MaxBytes: 1 << 20,
		}
		if view.Query.Scope.Kind == audit.ScopeExact {
			grant.Scopes = []audit.ScopedReference{view.Query.Scope.Reference}
		}
		return audit.AllowAccess(request, grant)
	})
	history, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: store, Access: authority,
	})
	if err != nil {
		t.Fatal(err)
	}
	return history
}

func recordRestartSearchEvent(t *testing.T, ctx context.Context, recorder *audit.Recorder, event *audit.EventType[restartSearchOccurrence], target, value string, second int64) {
	t.Helper()
	draft, err := event.New(restartSearchOccurrence{Target: target, Value: value, At: time.Unix(2_000_000_000+second, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	result, err := recorder.Record(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	if receipt, ok := result.Receipt(); !ok || receipt.Disposition() != audit.Inserted || receipt.Settlement() != audit.Committed {
		t.Fatalf("search fixture receipt = (%+v, %v)", receipt, ok)
	}
}

func restartSearchValues(page audit.Page) []string {
	revisions := page.Revisions()
	values := make([]string, 0, len(revisions))
	for _, revision := range revisions {
		if len(revision.Items) == 0 || len(revision.Items[0].Values) == 0 {
			values = append(values, "")
			continue
		}
		values = append(values, string(revision.Items[0].Values[0].Canonical))
	}
	return values
}
