//go:build integration

package jobspg

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type catalogTestIdentity struct {
	id      jobs.CodecID
	version jobs.SchemaVersion
}

func (i catalogTestIdentity) ID() jobs.CodecID            { return i.id }
func (i catalogTestIdentity) Version() jobs.SchemaVersion { return i.version }
func (catalogTestIdentity) Digest(value string, _ jobs.PayloadLimit) ([32]byte, error) {
	return sha256.Sum256([]byte(value)), nil
}

func TestPostgresCatalogVersionEvolutionSupportsRollingDeploys(t *testing.T) {
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
	marker := fmt.Sprint(time.Now().UnixNano())
	schema := "jobspg_catalog_evolution_" + marker
	repo := newRepository(schema)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := db.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS `+repo.schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	namespace, err := jobs.NamespaceOf("jobspg-catalog-evolution", marker)
	if err != nil {
		t.Fatal(err)
	}
	name, err := jobs.ParseName("jobspg.catalog-evolution")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	v1 := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(1), Policy: policy, Partition: jobs.PartitionGlobal})
	v2 := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(2), Upcasters: []jobs.Upcaster{
		jobs.Upcast(jobs.String(1), jobs.String(2), func(value string) (string, error) { return value, nil }),
	}, Policy: policy, Partition: jobs.PartitionGlobal})
	v1Catalog := jobs.MustCatalog(v1)
	v2Catalog := jobs.MustCatalog(v2)
	openCatalogDriver(t, ctx, db, schema, namespace, v1Catalog)
	unboundV2, err := New(Spec{DB: db, Namespace: namespace, Catalog: v2Catalog, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if err := unboundV2.CheckSchema(ctx); !errors.Is(err, ErrCatalogMismatch) {
		t.Fatalf("v2 check before binding = %v", err)
	}
	v2Driver := openCatalogDriver(t, ctx, db, schema, namespace, v2Catalog)
	if err := unboundV2.CheckSchema(ctx); err != nil {
		t.Fatalf("v2 check after binding = %v", err)
	}
	highWaterFingerprint := catalogRow(t, ctx, db, repo, namespace, 2, "1,2")
	openCatalogDriver(t, ctx, db, schema, namespace, v1Catalog)
	if fingerprint := catalogRow(t, ctx, db, repo, namespace, 2, "1,2"); fingerprint != highWaterFingerprint {
		t.Fatalf("old replica changed high-water fingerprint: %q != %q", fingerprint, highWaterFingerprint)
	}
	changedPolicy, err := jobs.Default.With(jobs.Retries(0), jobs.RetainFor(2*time.Hour)).Build()
	if err != nil {
		t.Fatal(err)
	}
	v2PolicyChange := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(2), Upcasters: []jobs.Upcaster{
		jobs.Upcast(jobs.String(1), jobs.String(2), func(value string) (string, error) { return value, nil }),
	}, Policy: changedPolicy, Partition: jobs.PartitionGlobal})
	openCatalogDriver(t, ctx, db, schema, namespace, jobs.MustCatalog(v2PolicyChange))
	if fingerprint := catalogRow(t, ctx, db, repo, namespace, 2, "1,2"); fingerprint != highWaterFingerprint {
		t.Fatalf("policy change changed high-water fingerprint: %q != %q", fingerprint, highWaterFingerprint)
	}
	v3WithoutV1 := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(3), Upcasters: []jobs.Upcaster{
		jobs.Upcast(jobs.String(2), jobs.String(3), func(value string) (string, error) { return value, nil }),
	}, Policy: policy, Partition: jobs.PartitionGlobal})
	if err := prepareCatalogDriver(ctx, db, schema, namespace, jobs.MustCatalog(v3WithoutV1)); !errors.Is(err, ErrCatalogMismatch) {
		t.Fatalf("history-dropping deployment = %v", err)
	}
	tenantV2 := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(2), Upcasters: []jobs.Upcaster{
		jobs.Upcast(jobs.String(1), jobs.String(2), func(value string) (string, error) { return value, nil }),
	}, Policy: policy, Partition: jobs.PartitionTenantRequired})
	if err := prepareCatalogDriver(ctx, db, schema, namespace, jobs.MustCatalog(tenantV2)); !errors.Is(err, ErrCatalogMismatch) {
		t.Fatalf("partition-changing deployment = %v", err)
	}
	identityID, err := jobs.ParseCodecID("jobspg-catalog-identity")
	if err != nil {
		t.Fatal(err)
	}
	identityV2 := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(2), Upcasters: []jobs.Upcaster{
		jobs.Upcast(jobs.String(1), jobs.String(2), func(value string) (string, error) { return value, nil }),
	}, Identity: catalogTestIdentity{id: identityID, version: 1}, Policy: policy, Partition: jobs.PartitionGlobal})
	if err := prepareCatalogDriver(ctx, db, schema, namespace, jobs.MustCatalog(identityV2)); !errors.Is(err, ErrCatalogMismatch) {
		t.Fatalf("payload-identity-changing deployment = %v", err)
	}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: v2Catalog, Sender: v2Driver})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Enqueue(ctx, queue, v2, "v2-payload"); err != nil {
		t.Fatal(err)
	}
	oldTarget := catalogClaimTarget(t, v1, "catalog.old", "test:catalog-v1")
	claim, err := jobs.NewClaimRequest(jobs.ClaimRequestSpec{
		Namespace: namespace, Incarnation: postgresTestIncarnation(t, 7), Targets: []jobs.ClaimTarget{oldTarget}, MaxItems: 1, MaxBytes: jobs.MaxDeliveryRecordBytes, LeaseTTL: jobs.MinimumLeaseTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := v2Driver.Claim(ctx, claim)
	if err != nil || batch.Len() != 0 {
		t.Fatalf("old replica claimed v2 payload = (%d, %v)", batch.Len(), err)
	}
	for _, statement := range []string{
		`ALTER TABLE ` + repo.definitions + ` DROP CONSTRAINT catalog_definitions_contract_check`,
		`ALTER TABLE ` + repo.definitions + ` DROP COLUMN codec, DROP COLUMN codec_mode, DROP COLUMN codec_version, DROP COLUMN codec_revisions, DROP COLUMN partition_mode, DROP COLUMN payload_identity, DROP COLUMN payload_identity_version, DROP COLUMN payload_identity_automatic`,
		`UPDATE ` + repo.meta + ` SET version = 3 WHERE singleton`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	readLock, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readLock.ExecContext(ctx, `SELECT 1 FROM `+repo.deliveries+` WHERE false`); err != nil {
		t.Fatal(err)
	}
	migrationCtx, migrationCancel := context.WithTimeout(ctx, 2*time.Second)
	err = prepareCatalogDriver(migrationCtx, db, schema, namespace, v2Catalog)
	migrationCancel()
	if err != nil {
		t.Fatalf("v3 catalog-only migration under delivery read lock = %v", err)
	}
	if err := readLock.Rollback(); err != nil {
		t.Fatal(err)
	}
	catalogRow(t, ctx, db, repo, namespace, 2, "1,2")
}

func openCatalogDriver(t *testing.T, ctx context.Context, db *sql.DB, schema string, namespace jobs.Namespace, catalog jobs.Catalog) *Driver {
	t.Helper()
	driver, err := New(Spec{DB: db, Namespace: namespace, Catalog: catalog, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	return driver
}

func prepareCatalogDriver(ctx context.Context, db *sql.DB, schema string, namespace jobs.Namespace, catalog jobs.Catalog) error {
	driver, err := New(Spec{DB: db, Namespace: namespace, Catalog: catalog, Schema: schema})
	if err != nil {
		return err
	}
	return driver.Prepare(ctx)
}

func catalogRow(t *testing.T, ctx context.Context, db *sql.DB, repo repository, namespace jobs.Namespace, version int64, revisions string) string {
	t.Helper()
	var fingerprint, codecRevisions string
	var codecVersion int64
	err := db.QueryRowContext(ctx, `SELECT fingerprint, codec_version, codec_revisions FROM `+repo.definitions+` WHERE namespace = $1`, namespaceArgument(namespace)).Scan(&fingerprint, &codecVersion, &codecRevisions)
	if err != nil || codecVersion != version || codecRevisions != revisions {
		t.Fatalf("catalog row = (%q, %d, %q, %v), want version %d revisions %q", fingerprint, codecVersion, codecRevisions, err, version, revisions)
	}
	return fingerprint
}

func catalogClaimTarget[P any](t *testing.T, definition *jobs.Definition[P], bindingRaw, buildRaw string) jobs.ClaimTarget {
	t.Helper()
	descriptor := definition.Describe()
	revisions := make([]jobs.PayloadRevision, len(descriptor.Codec.SupportedRevisions))
	for index, version := range descriptor.Codec.SupportedRevisions {
		revision, err := jobs.NewPayloadRevision(descriptor.Codec.ID, version)
		if err != nil {
			t.Fatal(err)
		}
		revisions[index] = revision
	}
	binding, err := jobs.ParseBindingName(bindingRaw)
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID(buildRaw)
	if err != nil {
		t.Fatal(err)
	}
	target, err := jobs.NewClaimTarget(jobs.ClaimTargetSpec{Definition: descriptor.Name, Binding: binding, Build: build, SupportedRevisions: revisions, Available: 1})
	if err != nil {
		t.Fatal(err)
	}
	return target
}
