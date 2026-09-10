package auditpg

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/vvdb/lock"
	"github.com/frostgrove/vv/vvdb/lock/locksql"
)

type deployment struct {
	configured configured
	management SchemaManagement
	state      atomic.Pointer[readiness]
	closed     atomic.Bool
}

type Deployment struct {
	value *deployment
}

func NewDeployment(spec DeploymentSpec) (*Deployment, error) {
	if !spec.SchemaManagement.Valid() {
		return nil, fmt.Errorf("%w: schema management is %s", ErrSpec, spec.SchemaManagement)
	}
	configured, err := configure(spec.Runtime)
	if err != nil {
		return nil, err
	}
	management := spec.SchemaManagement
	if management == UnsetSchemaManagement {
		management = VerifySchema
	}
	return &Deployment{value: &deployment{configured: configured, management: management}}, nil
}

func (d *Deployment) Capabilities() audit.Capabilities { return capabilities() }

func (d *Deployment) Limits() audit.Limits {
	if d == nil || d.value == nil {
		return audit.Limits{}
	}
	return d.value.configured.limits
}

func (d *Deployment) Backing() audit.Backing {
	if d == nil || d.value == nil || loadReadiness(&d.value.state) == nil {
		return audit.Backing{}
	}
	return d.value.configured.backing
}

func (d *Deployment) BackingID() audit.BackingID {
	if d == nil || d.value == nil || loadReadiness(&d.value.state) == nil {
		return audit.BackingID{}
	}
	return loadReadiness(&d.value.state).backingID
}

func (d *Deployment) LogID() audit.LogID {
	if d == nil || d.value == nil || loadReadiness(&d.value.state) == nil {
		return audit.LogID{}
	}
	return loadReadiness(&d.value.state).logID
}

func (d *Deployment) Catalogs() audit.StoreCatalogState {
	if d == nil || d.value == nil || loadReadiness(&d.value.state) == nil {
		return audit.NewEmptyStoreCatalogState()
	}
	return loadReadiness(&d.value.state).catalogs
}

func (d *Deployment) Schema() Schema {
	if d == nil || d.value == nil {
		return Schema{}
	}
	return d.value.configured.schema
}

func (d *Deployment) SchemaManagement() SchemaManagement {
	if d == nil || d.value == nil {
		return UnsetSchemaManagement
	}
	return d.value.management
}

func (d *Deployment) Prepare(ctx context.Context) error {
	if d == nil || d.value == nil {
		return fmt.Errorf("%w: deployment is nil", ErrSpec)
	}
	if d.value.closed.Load() {
		return audit.Failure(audit.Closed, errors.New("auditpg: deployment is closed"))
	}
	d.value.state.Store(nil)
	if d.value.management == ManageSchema {
		if err := d.Migrate(ctx); err != nil {
			return err
		}
	}
	return d.Verify(ctx)
}

func (d *Deployment) Migrate(ctx context.Context) error {
	if d == nil || d.value == nil {
		return fmt.Errorf("%w: deployment is nil", ErrSpec)
	}
	if d.value.management != ManageSchema {
		return fmt.Errorf("%w: migration requires %s", ErrSpec, ManageSchema)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.value.closed.Load() {
		return audit.Failure(audit.Closed, errors.New("auditpg: deployment is closed"))
	}
	d.value.state.Store(nil)
	statements, err := MigrationStatements(d.value.configured.schema)
	if err != nil {
		return err
	}
	tx, err := d.value.configured.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auditpg: begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := locksql.Take(ctx, tx, lock.Policy{},
		lock.Exclusively(lock.KeyFrom(migrationLock(d.value.configured.schema.Name)))); err != nil {
		return fmt.Errorf("auditpg: lock migration: %w", err)
	}
	for index, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("auditpg: migration statement %d of %d: %w", index+1, len(statements), err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("auditpg: commit migration: %w", err)
	}
	return nil
}

func (d *Deployment) Verify(ctx context.Context) error {
	if d == nil || d.value == nil {
		return fmt.Errorf("%w: deployment is nil", ErrSpec)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.value.closed.Load() {
		return audit.Failure(audit.Closed, errors.New("auditpg: deployment is closed"))
	}
	d.value.state.Store(nil)
	conn, err := d.value.configured.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("auditpg: verify checkout: %w", err)
	}
	defer func() { _ = conn.Close() }()
	ready, err := readReady(ctx, conn, d.value.configured.schema)
	if err != nil {
		return err
	}
	d.value.state.Store(&ready)
	return nil
}

func (d *Deployment) InstallCatalog(ctx context.Context, manifest audit.Manifest, change audit.CatalogChangeRef) error {
	ready, err := d.ready()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ref, previous, canonical := manifest.Ref(), manifest.Previous(), manifest.Canonical()
	changeView := change.View()
	if ref == (audit.CatalogRef{}) || len(canonical) == 0 || len(canonical) > audit.MaxCatalogManifestBytes || changeView.Ledger == "" || changeView.Change == "" {
		return audit.Failure(audit.Refused, errors.New("auditpg: catalog installation is invalid"))
	}
	tx, err := d.value.configured.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQL(err, false)
	}
	defer func() { _ = tx.Rollback() }()
	q := quoteIdentifier(d.value.configured.schema.Name)
	if _, err := tx.ExecContext(ctx, `SELECT singleton FROM `+q+`.settings WHERE singleton FOR UPDATE`); err != nil {
		return classifySQL(err, false)
	}
	actual, err := readReady(ctx, tx, d.value.configured.schema)
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	if actual.backingID != ready.backingID || actual.logID != ready.logID {
		return audit.Failure(audit.Conflict, errors.New("auditpg: deployment identity changed"))
	}
	var existing []byte
	var existingLedger, existingChange string
	err = tx.QueryRowContext(ctx, `SELECT canonical, change_ledger, change_ref FROM `+q+`.catalogs WHERE catalog_id = $1 AND generation = $2`, string(ref.ID), int64(ref.Generation)).Scan(&existing, &existingLedger, &existingChange)
	if err == nil {
		if !bytes.Equal(existing, canonical) || existingLedger != string(changeView.Ledger) || existingChange != string(changeView.Change) {
			return audit.Failure(audit.Conflict, errors.New("auditpg: catalog generation already has another meaning"))
		}
		if err := tx.Commit(); err != nil {
			return classifySQL(err, true)
		}
		d.value.state.Store(&actual)
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return classifySQL(err, false)
	}
	var latestID sql.NullString
	var latestGeneration sql.NullInt64
	var latestDigest []byte
	err = tx.QueryRowContext(ctx, `SELECT catalog_id, generation, digest FROM `+q+`.catalogs ORDER BY generation DESC LIMIT 1 FOR UPDATE`).Scan(&latestID, &latestGeneration, &latestDigest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return classifySQL(err, false)
	}
	if !latestID.Valid {
		if ref.Generation != 1 || previous != (audit.CatalogRef{}) {
			return audit.Failure(audit.Refused, errors.New("auditpg: first catalog is not genesis"))
		}
	} else {
		lastDigest, ok := digest32[audit.CatalogDigest](latestDigest)
		last := audit.CatalogRef{ID: audit.CatalogID(latestID.String), Generation: audit.CatalogGeneration(latestGeneration.Int64), Digest: lastDigest}
		if !ok || previous != last || ref.ID != last.ID || ref.Generation != last.Generation+1 {
			return audit.Failure(audit.Refused, errors.New("auditpg: catalog is not the direct next generation"))
		}
	}
	var previousID any
	var previousGeneration any
	var previousDigest any
	if previous != (audit.CatalogRef{}) {
		previousID, previousGeneration, previousDigest = string(previous.ID), int64(previous.Generation), previous.Digest[:]
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+q+`.catalogs
(catalog_id, generation, digest, previous_catalog_id, previous_generation, previous_digest, canonical, change_ledger, change_ref)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, string(ref.ID), int64(ref.Generation), ref.Digest[:], previousID, previousGeneration, previousDigest, canonical, string(changeView.Ledger), string(changeView.Change)); err != nil {
		return classifySQL(err, false)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+q+`.catalog_mutations
(kind, catalog_id, generation, digest, change_ledger, change_ref) VALUES (1,$1,$2,$3,$4,$5)`, string(ref.ID), int64(ref.Generation), ref.Digest[:], string(changeView.Ledger), string(changeView.Change)); err != nil {
		return classifySQL(err, false)
	}
	digest, err := installedDigest(ctx, tx, q, ref.ID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+q+`.settings SET catalog_set_digest = $1 WHERE singleton`, digest[:]); err != nil {
		return classifySQL(err, false)
	}
	if err := tx.Commit(); err != nil {
		return classifySQL(err, true)
	}
	next := actual
	if actual.catalogs.HasActive() {
		next.catalogs, err = audit.NewStoreCatalogState(actual.catalogs.Active(), digest)
	} else {
		next.catalogs, err = audit.NewInactiveStoreCatalogState(digest)
	}
	if err != nil {
		d.value.state.Store(nil)
		return audit.Failure(audit.Corrupt, err)
	}
	d.value.state.Store(&next)
	return nil
}

func installedDigest(ctx context.Context, tx *sql.Tx, schema string, id audit.CatalogID) (audit.CatalogSetDigest, error) {
	rows, err := tx.QueryContext(ctx, `SELECT canonical FROM `+schema+`.catalogs WHERE catalog_id = $1 ORDER BY generation`, string(id))
	if err != nil {
		return audit.CatalogSetDigest{}, classifySQL(err, false)
	}
	defer rows.Close()
	var manifests [][]byte
	for rows.Next() {
		var canonical []byte
		if err := rows.Scan(&canonical); err != nil {
			return audit.CatalogSetDigest{}, classifySQL(err, false)
		}
		manifests = append(manifests, bytes.Clone(canonical))
	}
	if err := rows.Err(); err != nil {
		return audit.CatalogSetDigest{}, classifySQL(err, false)
	}
	digest, err := catalogSetDigest(manifests)
	if err != nil {
		return audit.CatalogSetDigest{}, audit.Failure(audit.Refused, err)
	}
	return digest, nil
}

func (d *Deployment) ActivateCatalog(ctx context.Context, expected, next audit.CatalogRef, change audit.CatalogChangeRef, proof audit.CatalogActivationProof) error {
	ready, err := d.ready()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	changeView := change.View()
	if !proof.ValidFor(expected, next) || changeView.Ledger == "" || changeView.Change == "" {
		return audit.Failure(audit.Refused, errors.New("auditpg: catalog activation is invalid"))
	}
	tx, err := d.value.configured.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQL(err, false)
	}
	defer func() { _ = tx.Rollback() }()
	q := quoteIdentifier(d.value.configured.schema.Name)
	if _, err := tx.ExecContext(ctx, `SELECT singleton FROM `+q+`.settings WHERE singleton FOR UPDATE`); err != nil {
		return classifySQL(err, false)
	}
	actual, err := readReady(ctx, tx, d.value.configured.schema)
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	if actual.backingID != ready.backingID || actual.logID != ready.logID {
		return audit.Failure(audit.Conflict, errors.New("auditpg: deployment identity changed"))
	}
	if actual.catalogs.HasActive() != (expected != (audit.CatalogRef{})) || actual.catalogs.Active() != expected {
		replayed, replayErr := replayCatalogActivation(ctx, tx, q, actual, expected, next, change)
		if replayErr != nil {
			return replayErr
		}
		if !replayed {
			return audit.Failure(audit.Conflict, errors.New("auditpg: active catalog changed"))
		}
		if err := tx.Commit(); err != nil {
			return classifySQL(err, false)
		}
		d.value.state.Store(&actual)
		return nil
	}
	var digest []byte
	if err := tx.QueryRowContext(ctx, `SELECT digest FROM `+q+`.catalogs WHERE catalog_id=$1 AND generation=$2`, string(next.ID), int64(next.Generation)).Scan(&digest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return audit.Failure(audit.Missing, errors.New("auditpg: catalog is not installed"))
		}
		return classifySQL(err, false)
	}
	if !bytes.Equal(digest, next.Digest[:]) {
		return audit.Failure(audit.Conflict, errors.New("auditpg: installed catalog digest differs"))
	}
	var expectedID any
	var expectedGeneration any
	var expectedDigest any
	if expected != (audit.CatalogRef{}) {
		expectedID, expectedGeneration, expectedDigest = string(expected.ID), int64(expected.Generation), expected.Digest[:]
	}
	result, err := tx.ExecContext(ctx, `UPDATE `+q+`.settings
SET active_catalog_id=$1, active_generation=$2, active_digest=$3
WHERE singleton AND active_catalog_id IS NOT DISTINCT FROM $4 AND active_generation IS NOT DISTINCT FROM $5 AND active_digest IS NOT DISTINCT FROM $6`, string(next.ID), int64(next.Generation), next.Digest[:], expectedID, expectedGeneration, expectedDigest)
	if err != nil {
		return classifySQL(err, false)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return audit.Failure(audit.Conflict, errors.New("auditpg: catalog activation CAS failed"))
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+q+`.catalog_mutations
(kind, catalog_id, generation, digest, expected_catalog_id, expected_generation, expected_digest, change_ledger, change_ref)
VALUES (2,$1,$2,$3,$4,$5,$6,$7,$8)`, string(next.ID), int64(next.Generation), next.Digest[:], expectedID, expectedGeneration, expectedDigest, string(changeView.Ledger), string(changeView.Change)); err != nil {
		return classifySQL(err, false)
	}
	if err := tx.Commit(); err != nil {
		return classifySQL(err, true)
	}
	state := actual
	state.catalogs, err = audit.NewStoreCatalogState(next, actual.catalogs.SetDigest())
	if err != nil {
		d.value.state.Store(nil)
		return audit.Failure(audit.Corrupt, err)
	}
	d.value.state.Store(&state)
	return nil
}

func (d *Deployment) VerifyCatalogs(ctx context.Context, manifests []audit.Manifest) error {
	ready, err := d.ready()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(manifests) == 0 || len(manifests) > audit.MaxCatalogs {
		return audit.Failure(audit.Refused, errors.New("auditpg: catalog inventory is invalid"))
	}
	expectedSet, err := audit.CatalogSetDigestOf(manifests)
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	tx, err := d.value.configured.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQL(err, false)
	}
	defer func() { _ = tx.Rollback() }()
	q := quoteIdentifier(d.value.configured.schema.Name)
	var singleton bool
	if err := tx.QueryRowContext(ctx, `SELECT singleton FROM `+q+`.settings WHERE singleton FOR SHARE`).Scan(&singleton); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return audit.Failure(audit.Refused, ErrSchemaMismatch)
		}
		return classifySQL(err, false)
	}
	if !singleton {
		return audit.Failure(audit.Refused, errors.New("auditpg: catalog readiness lock failed"))
	}
	actual, err := readReady(ctx, tx, d.value.configured.schema)
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	if actual.backingID != ready.backingID || actual.logID != ready.logID {
		return audit.Failure(audit.Conflict, errors.New("auditpg: deployment identity changed"))
	}
	rows, err := tx.QueryContext(ctx, `SELECT catalog_id, generation, digest, canonical FROM `+q+`.catalogs ORDER BY generation`)
	if err != nil {
		return classifySQL(err, false)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		if index >= len(manifests) {
			return audit.Failure(audit.Conflict, errors.New("auditpg: extra installed catalog"))
		}
		var id string
		var generation int64
		var digest, canonical []byte
		if err := rows.Scan(&id, &generation, &digest, &canonical); err != nil {
			return classifySQL(err, false)
		}
		want := manifests[index]
		wantRef := want.Ref()
		if id != string(wantRef.ID) || generation != int64(wantRef.Generation) || !bytes.Equal(digest, wantRef.Digest[:]) || !bytes.Equal(canonical, want.Canonical()) {
			return audit.Failure(audit.Conflict, errors.New("auditpg: installed catalog differs"))
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return classifySQL(err, false)
	}
	if index != len(manifests) {
		return audit.Failure(audit.Missing, errors.New("auditpg: installed catalog is incomplete"))
	}
	expectedActive := manifests[len(manifests)-1].Ref()
	if !actual.catalogs.HasActive() || actual.catalogs.Active() != expectedActive || actual.catalogs.SetDigest() != expectedSet {
		return audit.Failure(audit.Conflict, errors.New("auditpg: active catalog does not match the verified inventory"))
	}
	if err := tx.Commit(); err != nil {
		return classifySQL(err, false)
	}
	d.value.state.Store(&actual)
	return nil
}

func (d *Deployment) ready() (readiness, error) {
	if d == nil || d.value == nil {
		return readiness{}, fmt.Errorf("%w: deployment is nil", ErrSpec)
	}
	current := loadReadiness(&d.value.state)
	if err := requireReady(current, d.value.closed.Load()); err != nil {
		return readiness{}, err
	}
	return *current, nil
}

func (d *Deployment) Close() error {
	if d == nil || d.value == nil {
		return fmt.Errorf("%w: deployment is nil", ErrSpec)
	}
	if !d.value.closed.CompareAndSwap(false, true) {
		return audit.Failure(audit.Closed, errors.New("auditpg: deployment is closed"))
	}
	d.value.state.Store(nil)
	return nil
}
