//go:build integration

package auditpg

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

func TestPostgresCatalogMutationLogSurvivesRestartExactly(t *testing.T) {
	dsn := requiredLiveDSN(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	schema := Schema{Name: "auditpg_mutations_" + suffix}
	defer dropLiveSchema(t, dsn, schema)
	ctx := context.Background()
	chain := newLiveEventChain(t, "auditpg.mutations", 2)

	firstDB := openLiveDB(t, dsnWithApplicationName(t, dsn, "auditpg_mutations_before_"+suffix), 2)
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
	installGenesis := liveCatalogChange(t, "install-genesis-"+suffix)
	activateGenesis := liveCatalogChange(t, "activate-genesis-"+suffix)
	installNext := liveCatalogChange(t, "install-next-"+suffix)
	activateNext := liveCatalogChange(t, "activate-next-"+suffix)
	conflicting := liveCatalogChange(t, "conflicting-"+suffix)
	genesisProof, err := audit.NoAttemptCatalogActivation(audit.CatalogRef{}, chain.genesis.Ref())
	if err != nil {
		t.Fatal(err)
	}
	nextProof, err := audit.NoAttemptCatalogActivation(chain.genesis.Ref(), chain.active.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if err := firstDeployment.InstallCatalog(ctx, chain.genesis.Manifest(), installGenesis); err != nil {
		t.Fatal(err)
	}
	if err := firstDeployment.InstallCatalog(ctx, chain.genesis.Manifest(), installGenesis); err != nil {
		t.Fatalf("identical install replay = %v", err)
	}
	if err := firstDeployment.InstallCatalog(ctx, chain.genesis.Manifest(), conflicting); !storeFailureIs(err, audit.Conflict) {
		t.Fatalf("conflicting install replay = %v", err)
	}
	if err := firstDeployment.ActivateCatalog(ctx, audit.CatalogRef{}, chain.genesis.Ref(), activateGenesis, genesisProof); err != nil {
		t.Fatal(err)
	}
	if err := firstDeployment.ActivateCatalog(ctx, audit.CatalogRef{}, chain.genesis.Ref(), activateGenesis, genesisProof); err != nil {
		t.Fatalf("identical activation replay = %v", err)
	}
	if err := firstDeployment.ActivateCatalog(ctx, audit.CatalogRef{}, chain.genesis.Ref(), conflicting, genesisProof); !storeFailureIs(err, audit.Conflict) {
		t.Fatalf("conflicting activation replay = %v", err)
	}
	if err := firstDeployment.InstallCatalog(ctx, chain.active.Manifest(), installNext); err != nil {
		t.Fatal(err)
	}
	if err := firstDeployment.ActivateCatalog(ctx, chain.genesis.Ref(), chain.active.Ref(), activateNext, nextProof); err != nil {
		t.Fatal(err)
	}
	if err := firstDeployment.ActivateCatalog(ctx, chain.genesis.Ref(), chain.active.Ref(), activateNext, nextProof); err != nil {
		t.Fatalf("identical next activation replay = %v", err)
	}
	want := []audit.CatalogMutationView{
		audit.CatalogInstalled(chain.genesis.Ref(), installGenesis),
		audit.CatalogActivated(audit.CatalogRef{}, chain.genesis.Ref(), activateGenesis, genesisProof),
		audit.CatalogInstalled(chain.active.Ref(), installNext),
		audit.CatalogActivated(chain.genesis.Ref(), chain.active.Ref(), activateNext, nextProof),
	}
	mutations, err := firstDeployment.CatalogMutations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.VerifyCatalogMutations(firstDeployment, want, mutations); err != nil {
		t.Fatalf("verify mutation log before restart = %v", err)
	}
	var stored int
	if err := firstDB.QueryRowContext(ctx, `SELECT count(*) FROM `+quoteIdentifier(schema.Name)+`.catalog_mutations`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != len(want) {
		t.Fatalf("stored mutation count = %d, want %d", stored, len(want))
	}
	backing, log := firstDeployment.BackingID(), firstDeployment.LogID()
	if err := firstDeployment.Close(); err != nil {
		t.Fatal(err)
	}
	if err := firstDB.Close(); err != nil {
		t.Fatal(err)
	}

	freshDB := openLiveDB(t, dsnWithApplicationName(t, dsn, "auditpg_mutations_after_"+suffix), 2)
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
	if freshDeployment.BackingID() != backing || freshDeployment.LogID() != log {
		t.Fatal("mutation log origin changed across restart")
	}
	restarted, err := freshDeployment.CatalogMutations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.VerifyCatalogMutations(freshDeployment, want, restarted); err != nil {
		t.Fatalf("verify mutation log after restart = %v", err)
	}
	activeRef := chain.active.Ref()
	if _, err := freshDB.ExecContext(ctx, `INSERT INTO `+quoteIdentifier(schema.Name)+`.catalog_mutations
(kind,catalog_id,generation,digest,change_ledger,change_ref) VALUES (1,$1,$2,$3,$4,$5)`,
		string(activeRef.ID), int64(activeRef.Generation), activeRef.Digest[:],
		"auditpg.integration", "hostile-duplicate-"+suffix,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := freshDeployment.CatalogMutations(ctx); !storeFailureIs(err, audit.Corrupt) && !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("malformed persisted sequence = %v", err)
	}
}
