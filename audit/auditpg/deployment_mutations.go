package auditpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/frostgrove/vv/audit"
)

const (
	catalogMutationLogFixedBytes = 100
	catalogMutationFixedBytes    = 312
)

func (d *Deployment) CatalogMutations(ctx context.Context) (audit.CatalogMutationLog, error) {
	ready, err := d.ready()
	if err != nil {
		return audit.CatalogMutationLog{}, err
	}
	if err := ctx.Err(); err != nil {
		return audit.CatalogMutationLog{}, err
	}
	tx, err := d.value.configured.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return audit.CatalogMutationLog{}, classifySQL(err, false)
	}
	defer func() { _ = tx.Rollback() }()
	q := quoteIdentifier(d.value.configured.schema.Name)
	var singleton bool
	if err := tx.QueryRowContext(ctx, `SELECT singleton FROM `+q+`.settings WHERE singleton FOR SHARE`).Scan(&singleton); err != nil {
		return audit.CatalogMutationLog{}, classifySQL(err, false)
	}
	if !singleton {
		return audit.CatalogMutationLog{}, audit.Failure(audit.Corrupt, errors.New("auditpg: catalog mutation readiness lock failed"))
	}
	actual, err := readReady(ctx, tx, d.value.configured.schema)
	if err != nil {
		return audit.CatalogMutationLog{}, audit.Failure(audit.Corrupt, err)
	}
	if actual.backingID != ready.backingID || actual.logID != ready.logID {
		return audit.CatalogMutationLog{}, audit.Failure(audit.Conflict, errors.New("auditpg: deployment identity changed"))
	}
	count, bytes, err := mutationPreflight(ctx, tx, q)
	if err != nil {
		return audit.CatalogMutationLog{}, err
	}
	mutations, err := readCatalogMutations(ctx, tx, q, count)
	if err != nil {
		return audit.CatalogMutationLog{}, err
	}
	result, err := audit.NewCatalogMutationLog(actual.backingID, actual.logID, mutations)
	if err != nil {
		return audit.CatalogMutationLog{}, audit.Failure(audit.Corrupt, err)
	}
	if mutationLogSize(mutations) != bytes {
		return audit.CatalogMutationLog{}, audit.Failure(audit.Corrupt, errors.New("auditpg: catalog mutation size preflight disagrees with materialized evidence"))
	}
	if err := tx.Commit(); err != nil {
		return audit.CatalogMutationLog{}, classifySQL(err, false)
	}
	d.value.state.Store(&actual)
	return result, nil
}

func mutationPreflight(ctx context.Context, tx *sql.Tx, schema string) (int, int64, error) {
	var count int64
	var bytes int64
	err := tx.QueryRowContext(ctx, `WITH bounded AS MATERIALIZED (
	SELECT kind, catalog_id, expected_catalog_id, change_ledger, change_ref
	FROM `+schema+`.catalog_mutations ORDER BY position LIMIT $1
)
SELECT count(*), $2::bigint+COALESCE(sum(
	$3::bigint+octet_length(change_ledger)+octet_length(change_ref)
	+CASE kind WHEN 1 THEN octet_length(catalog_id)
		WHEN 2 THEN 2*octet_length(catalog_id)+2*octet_length(COALESCE(expected_catalog_id,''))
		ELSE $4::bigint END
),0)::bigint
FROM bounded`, audit.MaxCatalogMutations+1, catalogMutationLogFixedBytes, catalogMutationFixedBytes, audit.MaxCatalogMutationBytes+1).Scan(&count, &bytes)
	if err != nil {
		return 0, 0, classifySQL(err, false)
	}
	if count < 0 || count > audit.MaxCatalogMutations || bytes < catalogMutationLogFixedBytes || bytes > audit.MaxCatalogMutationBytes {
		return 0, 0, audit.Failure(audit.Corrupt, fmt.Errorf("%w: persisted catalog mutation log exceeds its bounds", audit.ErrTooLarge))
	}
	return int(count), bytes, nil
}

func replayCatalogActivation(ctx context.Context, tx *sql.Tx, schema string, current readiness, expected, next audit.CatalogRef, change audit.CatalogChangeRef) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT position, kind, catalog_id, generation, digest,
	expected_catalog_id, expected_generation, expected_digest, expected_digest IS NOT NULL,
	change_ledger, change_ref
FROM `+schema+`.catalog_mutations
WHERE kind=2 AND catalog_id=$1 AND generation=$2
ORDER BY position LIMIT 2`, string(next.ID), int64(next.Generation))
	if err != nil {
		return false, classifySQL(err, false)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, classifySQL(err, false)
		}
		return false, nil
	}
	mutation, _, err := scanCatalogMutation(rows)
	if err != nil {
		return false, err
	}
	if rows.Next() {
		return false, audit.Failure(audit.Corrupt, errors.New("auditpg: catalog activation mutation is duplicated"))
	}
	if err := rows.Err(); err != nil {
		return false, classifySQL(err, false)
	}
	if mutation.Active.ID != next.ID || mutation.Active.Generation != next.Generation || mutation.Active.Digest != next.Digest {
		return false, audit.Failure(audit.Corrupt, errors.New("auditpg: persisted catalog activation reference disagrees with the catalog"))
	}
	if mutation.Expected != expected {
		return false, audit.Failure(audit.Conflict, errors.New("auditpg: catalog activation transition conflicts"))
	}
	if mutation.Change.View() != change.View() {
		return false, audit.Failure(audit.Conflict, errors.New("auditpg: catalog activation change conflicts"))
	}
	if !current.catalogs.HasActive() || current.catalogs.Active().ID != next.ID || current.catalogs.Active().Generation < next.Generation {
		return false, audit.Failure(audit.Corrupt, errors.New("auditpg: persisted activation is ahead of active catalog state"))
	}
	return true, nil
}

func readCatalogMutations(ctx context.Context, tx *sql.Tx, schema string, expectedCount int) ([]audit.CatalogMutationView, error) {
	rows, err := tx.QueryContext(ctx, `SELECT position, kind, catalog_id, generation, digest,
	expected_catalog_id, expected_generation, expected_digest, expected_digest IS NOT NULL,
	change_ledger, change_ref
FROM `+schema+`.catalog_mutations ORDER BY position LIMIT $1`, audit.MaxCatalogMutations+1)
	if err != nil {
		return nil, classifySQL(err, false)
	}
	defer rows.Close()
	mutations := make([]audit.CatalogMutationView, 0, expectedCount)
	var previousPosition int64
	for rows.Next() {
		if len(mutations) >= expectedCount {
			return nil, audit.Failure(audit.Corrupt, errors.New("auditpg: catalog mutation cohort changed within one snapshot"))
		}
		mutation, position, err := scanCatalogMutation(rows)
		if err != nil {
			return nil, err
		}
		if position <= 0 || position <= previousPosition {
			return nil, audit.Failure(audit.Corrupt, errors.New("auditpg: catalog mutation positions are malformed"))
		}
		previousPosition = position
		mutations = append(mutations, mutation)
	}
	if err := rows.Err(); err != nil {
		return nil, classifySQL(err, false)
	}
	if len(mutations) != expectedCount {
		return nil, audit.Failure(audit.Corrupt, errors.New("auditpg: catalog mutation cohort changed within one snapshot"))
	}
	return mutations, nil
}

func scanCatalogMutation(row interface{ Scan(...any) error }) (audit.CatalogMutationView, int64, error) {
	var position, kind, generation int64
	var catalogID, ledger, change string
	var digest, expectedDigest []byte
	var expectedID sql.NullString
	var expectedGeneration sql.NullInt64
	var expectedDigestPresent bool
	if err := row.Scan(
		&position, &kind, &catalogID, &generation, &digest,
		&expectedID, &expectedGeneration, &expectedDigest, &expectedDigestPresent,
		&ledger, &change,
	); err != nil {
		return audit.CatalogMutationView{}, 0, classifySQL(err, false)
	}
	activeDigest, ok := digest32[audit.CatalogDigest](digest)
	if !ok || generation <= 0 {
		return audit.CatalogMutationView{}, 0, audit.Failure(audit.Corrupt, errors.New("auditpg: catalog mutation reference is malformed"))
	}
	active := audit.CatalogRef{ID: audit.CatalogID(catalogID), Generation: audit.CatalogGeneration(generation), Digest: activeDigest}
	changeRef, err := audit.NewCatalogChangeRef(audit.DeploymentLedger(ledger), audit.DeploymentChange(change))
	if err != nil {
		return audit.CatalogMutationView{}, 0, audit.Failure(audit.Corrupt, err)
	}
	expectedPresent := expectedID.Valid || expectedGeneration.Valid || expectedDigestPresent
	if expectedPresent && (!expectedID.Valid || !expectedGeneration.Valid || !expectedDigestPresent) {
		return audit.CatalogMutationView{}, 0, audit.Failure(audit.Corrupt, errors.New("auditpg: catalog mutation expected tuple is incomplete"))
	}
	switch audit.CatalogMutationKind(kind) {
	case audit.CatalogInstallMutation:
		if expectedPresent {
			return audit.CatalogMutationView{}, 0, audit.Failure(audit.Corrupt, errors.New("auditpg: catalog install mutation has an expected tuple"))
		}
		return audit.CatalogInstalled(active, changeRef), position, nil
	case audit.CatalogActivateMutation:
		var expected audit.CatalogRef
		if expectedPresent {
			expectedValue, valid := digest32[audit.CatalogDigest](expectedDigest)
			if !valid || expectedGeneration.Int64 <= 0 {
				return audit.CatalogMutationView{}, 0, audit.Failure(audit.Corrupt, errors.New("auditpg: catalog activation expected tuple is malformed"))
			}
			expected = audit.CatalogRef{
				ID: audit.CatalogID(expectedID.String), Generation: audit.CatalogGeneration(expectedGeneration.Int64), Digest: expectedValue,
			}
		}
		proof, err := audit.NoAttemptCatalogActivation(expected, active)
		if err != nil {
			return audit.CatalogMutationView{}, 0, audit.Failure(audit.Corrupt, err)
		}
		return audit.CatalogActivated(expected, active, changeRef, proof), position, nil
	default:
		return audit.CatalogMutationView{}, 0, audit.Failure(audit.Corrupt, errors.New("auditpg: catalog mutation kind is malformed"))
	}
}

func mutationLogSize(mutations []audit.CatalogMutationView) int64 {
	bytes := int64(catalogMutationLogFixedBytes)
	for _, mutation := range mutations {
		view := mutation.Change.View()
		bytes += catalogMutationFixedBytes + int64(len(view.Ledger)+len(view.Change))
		if mutation.Kind == audit.CatalogInstallMutation {
			bytes += int64(len(mutation.Catalog.ID))
			continue
		}
		bytes += int64(2*len(mutation.Active.ID) + 2*len(mutation.Expected.ID))
	}
	return bytes
}

var _ audit.CatalogAdmin = (*Deployment)(nil)
