//go:build integration

package auditpg

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type continuityOccurrence struct {
	Number string
	At     time.Time
}

type liveEventChain struct {
	event      *audit.EventType[continuityOccurrence]
	semantic   audit.SemanticDigester
	identities audit.IdentityKeyring
	genesis    *audit.Catalog
	active     *audit.Catalog
	catalogs   *audit.CatalogSet
}

func TestPostgresCatalogActivationSerializesWithAppendReadiness(t *testing.T) {
	dsn := requiredLiveDSN(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	appendApplication := "auditpg_append_" + suffix
	activationApplication := "auditpg_activate_" + suffix
	appendDB := openLiveDB(t, dsnWithApplicationName(t, dsn, appendApplication), 1)
	defer appendDB.Close()
	activationDB := openLiveDB(t, dsnWithApplicationName(t, dsn, activationApplication), 1)
	defer activationDB.Close()
	ctx := context.Background()
	schema := Schema{Name: "auditpg_race_" + suffix}
	defer dropLiveSchema(t, dsn, schema)

	appendSource := crudsql.Postgres(appendDB)
	deployment, err := NewDeployment(DeploymentSpec{
		Runtime: Spec{DB: appendDB, Source: appendSource, Schema: schema}, SchemaManagement: ManageSchema,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := deployment.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	chain := newLiveEventChain(t, "auditpg.race", 2)
	installAndActivateLiveCatalog(t, ctx, deployment, chain.genesis, "race-genesis-"+suffix)
	installLiveCatalog(t, ctx, deployment, chain.active, "race-next-"+suffix)

	activationSource := crudsql.Postgres(activationDB)
	activator, err := NewDeployment(DeploymentSpec{
		Runtime: Spec{DB: activationDB, Source: activationSource, Schema: schema}, SchemaManagement: VerifySchema,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := activator.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := activator.VerifyCatalogs(ctx, chain.catalogs.Manifests()); !storeFailureIs(err, audit.Conflict) {
		t.Fatalf("verification before final activation = %v, want catalog conflict", err)
	}

	store, err := New(Spec{DB: appendDB, Source: appendSource, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Check(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := appendSource.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Rollback(context.Background())
	bound, err := store.BindTransaction(root)
	if err != nil {
		t.Fatal(err)
	}
	boundExecution, ok := bound.(*execution)
	if !ok {
		t.Fatalf("bound execution = %T", bound)
	}
	if err := boundExecution.ensure(ctx); err != nil {
		t.Fatal(err)
	}

	proof, err := audit.NoAttemptCatalogActivation(chain.genesis.Ref(), chain.active.Ref())
	if err != nil {
		t.Fatal(err)
	}
	change := liveCatalogChange(t, "race-activate-"+suffix)
	activationContext, cancelActivation := context.WithTimeout(ctx, 10*time.Second)
	defer cancelActivation()
	activationDone := make(chan error, 1)
	go func() {
		activationDone <- activator.ActivateCatalog(activationContext, chain.genesis.Ref(), chain.active.Ref(), change, proof)
	}()
	raw, ok := crudsql.TopLevelTransaction(root)
	if !ok {
		t.Fatal("append root lost its top-level sql transaction")
	}
	waitForSettingsLock(t, raw, activationApplication)
	select {
	case activationErr := <-activationDone:
		t.Fatalf("catalog activation passed a transaction-held readiness lock: %v", activationErr)
	default:
	}
	if err := root.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case activationErr := <-activationDone:
		if activationErr != nil {
			t.Fatalf("serialized catalog activation: %v", activationErr)
		}
	case <-activationContext.Done():
		t.Fatalf("serialized catalog activation did not resume: %v", activationContext.Err())
	}

	staleRoot, err := appendSource.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer staleRoot.Rollback(context.Background())
	staleBound, err := store.BindTransaction(staleRoot)
	if err != nil {
		t.Fatal(err)
	}
	staleExecution, ok := staleBound.(*execution)
	if !ok {
		t.Fatalf("stale bound execution = %T", staleBound)
	}
	if err := staleExecution.ensure(ctx); !storeFailureIs(err, audit.Conflict) {
		t.Fatalf("execution bound before readiness refresh = %v, want stale conflict", err)
	}
	if err := staleRoot.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if got := store.Catalogs(); !got.HasActive() || got.Active() != chain.active.Ref() || got.SetDigest() != chain.catalogs.Digest() {
		t.Fatalf("refreshed store catalogs = %+v", got)
	}
	if err := activator.VerifyCatalogs(ctx, chain.catalogs.Manifests()); err != nil {
		t.Fatalf("verification after serialized activation: %v", err)
	}
}

func TestPostgresRestartPreservesIdentityCatalogsAndEvidence(t *testing.T) {
	dsn := requiredLiveDSN(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	schema := Schema{Name: "auditpg_restart_" + suffix}
	defer dropLiveSchema(t, dsn, schema)
	ctx := context.Background()
	chain := newLiveEventChain(t, "auditpg.restart", 1)

	firstDB := openLiveDB(t, dsnWithApplicationName(t, dsn, "auditpg_before_"+suffix), 4)
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
	installAndActivateLiveCatalog(t, ctx, firstDeployment, chain.active, "restart-genesis-"+suffix)
	if err := firstDeployment.VerifyCatalogs(ctx, chain.catalogs.Manifests()); err != nil {
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
	beforeRecorder := newLiveEventRecorder(t, firstStore, chain, 31)
	before := recordLiveEvent(t, ctx, beforeRecorder, chain.event, "before-restart", 1)
	beforeKey, ok := before.ReconcileKey()
	if !ok {
		t.Fatal("pre-restart receipt has no reconcile key")
	}
	if err := firstStore.Close(); err != nil {
		t.Fatal(err)
	}
	if err := firstDeployment.Close(); err != nil {
		t.Fatal(err)
	}
	if err := firstDB.Close(); err != nil {
		t.Fatal(err)
	}

	freshDB := openLiveDB(t, dsnWithApplicationName(t, dsn, "auditpg_after_"+suffix), 4)
	defer freshDB.Close()
	freshSource := crudsql.Postgres(freshDB)
	freshDeployment, err := NewDeployment(DeploymentSpec{
		Runtime: Spec{DB: freshDB, Source: freshSource, Schema: schema}, SchemaManagement: VerifySchema,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := freshDeployment.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if freshDeployment.BackingID() != backingID || freshDeployment.LogID() != logID {
		t.Fatalf("restart deployment identity changed: backing=%x/%x log=%x/%x", backingID, freshDeployment.BackingID(), logID, freshDeployment.LogID())
	}
	if got := freshDeployment.Catalogs(); !got.HasActive() || got.Active() != chain.active.Ref() || got.SetDigest() != chain.catalogs.Digest() {
		t.Fatalf("restart deployment catalogs = %+v", got)
	}
	if err := freshDeployment.VerifyCatalogs(ctx, chain.catalogs.Manifests()); err != nil {
		t.Fatalf("restart catalog verification: %v", err)
	}
	freshStore, err := New(Spec{DB: freshDB, Source: freshSource, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if err := freshStore.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if freshStore.BackingID() != backingID || freshStore.LogID() != logID {
		t.Fatalf("restart store identity changed: backing=%x/%x log=%x/%x", backingID, freshStore.BackingID(), logID, freshStore.LogID())
	}
	afterRecorder := newLiveEventRecorder(t, freshStore, chain, 32)
	lookup, err := afterRecorder.Lookup(ctx, beforeKey)
	if err != nil || lookup.State() != audit.Found {
		t.Fatalf("pre-restart evidence lookup = (%v, %v)", lookup.State(), err)
	}
	restored, ok := lookup.Receipt()
	if !ok || restored.RevisionID() != before.RevisionID() {
		t.Fatalf("pre-restart receipt = (%+v, %v)", restored, ok)
	}
	recordLiveEvent(t, ctx, afterRecorder, chain.event, "after-restart", 2)
	var revisions int
	if err := freshDB.QueryRowContext(ctx, `SELECT count(*) FROM `+quoteIdentifier(schema.Name)+`.revisions`).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if revisions != 2 {
		t.Fatalf("persisted revisions after restart = %d, want 2", revisions)
	}
}

func newLiveEventChain(t *testing.T, id audit.CatalogID, generations int) liveEventChain {
	t.Helper()
	semantic, err := audit.HMACSemanticDigester("auditpg-live-semantic", bytes.Repeat([]byte{11}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{
		KeyID: "auditpg-live-identity", Key: bytes.Repeat([]byte{12}, 32), Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	contextPolicy := audit.ContextFacts(audit.OperationFact(
		audit.ContextRequired, audit.Provenances(audit.Verified), audit.Internal, audit.AsPlaintext,
	))
	event := audit.Declare(audit.EventPolicy[continuityOccurrence]{
		Semantics: audit.Semantics(1, audit.PolicyGolden("auditpg.live.event", strings.Repeat("0d", 32))),
		Descriptor: audit.Descriptor{
			Resource: "auditpg.continuity", Action: "auditpg.continuity.recorded", Owner: "auditpg.team",
			Purpose: "business.audit", Retention: "business.forever", Consequence: audit.Required, Context: contextPolicy,
		},
		Target:     audit.NoEventTarget[continuityOccurrence](),
		Outcome:    audit.EventOutcome(audit.Outcomes("recorded"), func(continuityOccurrence) audit.Outcome { return "recorded" }),
		OccurredAt: audit.EventOccurredAt(func(value continuityOccurrence) time.Time { return value.At }),
		Fields: audit.EventFields(
			audit.EventValue("number", func(value continuityOccurrence) string { return value.Number }, audit.Text(), audit.Public),
		),
	})
	spec := audit.CatalogSpec{
		ID: id, Owner: "auditpg.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(), Integrity: audit.IntegrityOnly(),
	}
	genesis, err := audit.Compile(spec, event)
	if err != nil {
		t.Fatal(err)
	}
	active := genesis
	var catalogs *audit.CatalogSet
	switch generations {
	case 1:
		catalogs, err = audit.Lineage(genesis)
	case 2:
		spec.Generation = 2
		spec.Previous = genesis.Ref()
		active, err = audit.Compile(spec, event)
		if err == nil {
			catalogs, err = audit.Lineage(active, audit.Retain(genesis))
		}
	default:
		t.Fatalf("unsupported live catalog generation count %d", generations)
	}
	if err != nil {
		t.Fatal(err)
	}
	return liveEventChain{event: event, semantic: semantic, identities: identities, genesis: genesis, active: active, catalogs: catalogs}
}

func newLiveEventRecorder(t *testing.T, store *Store, chain liveEventChain, operationByte byte) *audit.Recorder {
	t.Helper()
	operation, err := audit.NewContextValue(audit.OperationID{operationByte}, audit.Verified)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := audit.StaticContext(audit.Context{Operation: operation})
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := audit.New(audit.Config{
		Catalogs: chain.catalogs, Writer: store, Context: resolver,
		Semantics: chain.semantic, Identities: chain.identities,
	})
	if err != nil {
		t.Fatal(err)
	}
	return recorder
}

func recordLiveEvent(t *testing.T, ctx context.Context, recorder *audit.Recorder, event *audit.EventType[continuityOccurrence], number string, second int64) audit.Receipt {
	t.Helper()
	draft, err := event.New(continuityOccurrence{Number: number, At: time.Unix(1_800_000_000+second, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	result, err := recorder.Record(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok := result.Receipt()
	if !ok || receipt.Disposition() != audit.Inserted || receipt.Settlement() != audit.Committed {
		t.Fatalf("live event receipt = (%+v, %v)", receipt, ok)
	}
	return receipt
}

func installAndActivateLiveCatalog(t *testing.T, ctx context.Context, deployment *Deployment, catalog *audit.Catalog, changeName string) {
	t.Helper()
	change := liveCatalogChange(t, changeName)
	if err := deployment.InstallCatalog(ctx, catalog.Manifest(), change); err != nil {
		t.Fatal(err)
	}
	proof, err := audit.NoAttemptCatalogActivation(catalog.Manifest().Previous(), catalog.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if err := deployment.ActivateCatalog(ctx, catalog.Manifest().Previous(), catalog.Ref(), change, proof); err != nil {
		t.Fatal(err)
	}
}

func installLiveCatalog(t *testing.T, ctx context.Context, deployment *Deployment, catalog *audit.Catalog, changeName string) {
	t.Helper()
	if err := deployment.InstallCatalog(ctx, catalog.Manifest(), liveCatalogChange(t, changeName)); err != nil {
		t.Fatal(err)
	}
}

func liveCatalogChange(t *testing.T, changeName string) audit.CatalogChangeRef {
	t.Helper()
	change, err := audit.NewCatalogChangeRef("auditpg.integration", audit.DeploymentChange(changeName))
	if err != nil {
		t.Fatal(err)
	}
	return change
}

func requiredLiveDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("FROSTGROVE_AUDITPG_TEST_DSN")
	if dsn == "" {
		t.Fatal("FROSTGROVE_AUDITPG_TEST_DSN is required for the live auditpg suite")
	}
	return dsn
}

func dsnWithApplicationName(t *testing.T, dsn, application string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	values := parsed.Query()
	values.Set("application_name", application)
	parsed.RawQuery = values.Encode()
	return parsed.String()
}

func openLiveDB(t *testing.T, dsn string, connections int) *sql.DB {
	t.Helper()
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(connections)
	if err := database.PingContext(context.Background()); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	return database
}

func dropLiveSchema(t *testing.T, dsn string, schema Schema) {
	t.Helper()
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Errorf("open cleanup database: %v", err)
		return
	}
	defer database.Close()
	if _, err := database.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+quoteIdentifier(schema.Name)+` CASCADE`); err != nil {
		t.Errorf("drop scratch schema: %v", err)
	}
}

func waitForSettingsLock(t *testing.T, transaction *sql.Tx, application string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waitEvent, query string
		err := transaction.QueryRowContext(ctx, `SELECT COALESCE(wait_event_type,''), COALESCE(query,'')
FROM pg_stat_activity WHERE application_name=$1 AND state='active'
ORDER BY query_start DESC LIMIT 1`, application).Scan(&waitEvent, &query)
		if err == nil && waitEvent == "Lock" && strings.Contains(query, ".settings") && strings.Contains(query, "FOR UPDATE") {
			return
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("inspect activation lock: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("activation did not wait on the settings lock: wait=%q query=%q err=%v", waitEvent, query, err)
		case <-ticker.C:
		}
	}
}

func storeFailureIs(err error, outcome audit.StoreOutcome) bool {
	return err != nil && err.Error() == "audit: store reported "+outcome.String()
}
