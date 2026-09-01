package jobspg

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/frostgrove/vv/jobs"
)

type catalogBinding struct {
	name        string
	fingerprint string
}

type catalogQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func catalogBindings(catalog jobs.Catalog) ([]catalogBinding, error) {
	declarations := catalog.Definitions()
	bindings := make([]catalogBinding, len(declarations))
	for index, declaration := range declarations {
		single, err := jobs.NewCatalog(declaration)
		if err != nil {
			return nil, fmt.Errorf("jobspg: catalog definition %d: %w", index, err)
		}
		bindings[index] = catalogBinding{name: declaration.Describe().Name.Value(), fingerprint: single.Fingerprint()}
	}
	return bindings, nil
}

func (r repository) bindCatalog(ctx context.Context, db *sql.DB, namespace jobs.Namespace, catalog jobs.Catalog) error {
	bindings, err := catalogBindings(catalog)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("jobspg: bind catalog: %w", err)
	}
	defer tx.Rollback()
	namespaceBytes := namespaceArgument(namespace)
	result, err := tx.ExecContext(ctx, `INSERT INTO `+r.catalogs+` (namespace, application, environment, fingerprint)
VALUES ($1, $2, $3, $4)
ON CONFLICT (namespace) DO NOTHING`, namespaceBytes, namespace.Application().Value(), namespace.Environment().Value(), catalog.Fingerprint())
	if err != nil {
		return fmt.Errorf("jobspg: bind catalog: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("jobspg: bind catalog result: %w", err)
	}
	var aggregate, application, environment string
	if err := tx.QueryRowContext(ctx, `SELECT fingerprint, application, environment FROM `+r.catalogs+` WHERE namespace = $1 FOR UPDATE`, namespaceBytes).Scan(&aggregate, &application, &environment); err != nil {
		return fmt.Errorf("jobspg: read catalog binding: %w", err)
	}
	if application != namespace.Application().Value() || environment != namespace.Environment().Value() {
		return fmt.Errorf("%w: namespace is already bound to another application or environment", ErrCatalogMismatch)
	}
	persisted, err := r.readCatalogBindings(ctx, tx, namespaceBytes)
	if err != nil {
		return err
	}
	if inserted == 0 && len(persisted) == 0 && aggregate != catalog.Fingerprint() {
		return fmt.Errorf("%w: legacy catalog fingerprint cannot prove an additive change", ErrCatalogMismatch)
	}
	for _, binding := range bindings {
		if fingerprint, exists := persisted[binding.name]; exists {
			if fingerprint != binding.fingerprint {
				return fmt.Errorf("%w: definition %q changed", ErrCatalogMismatch, binding.name)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+r.definitions+` (namespace, definition, fingerprint) VALUES ($1, $2, $3)`, namespaceBytes, binding.name, binding.fingerprint); err != nil {
			return fmt.Errorf("jobspg: bind definition %q: %w", binding.name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+r.catalogs+` SET fingerprint = $2 WHERE namespace = $1`, namespaceBytes, catalog.Fingerprint()); err != nil {
		return fmt.Errorf("jobspg: update catalog fingerprint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("jobspg: commit catalog binding: %w", err)
	}
	return nil
}

func (r repository) check(ctx context.Context, db *sql.DB, namespace jobs.Namespace, catalog jobs.Catalog) error {
	var version int
	if err := db.QueryRowContext(ctx, `SELECT version FROM `+r.meta+` WHERE singleton = true`).Scan(&version); err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaMismatch, err)
	}
	if version != SchemaVersion {
		return fmt.Errorf("%w: have %d, need %d", ErrSchemaMismatch, version, SchemaVersion)
	}
	if _, err := db.ExecContext(ctx, `SELECT namespace, definition, fingerprint, created_at FROM `+r.definitions+` WHERE false`); err != nil {
		return fmt.Errorf("%w: catalog definitions: %v", ErrSchemaMismatch, err)
	}
	if _, err := db.ExecContext(ctx, `SELECT namespace, id, definition, codec, codec_version, priority, state, available_at, record_size, record, payload_identity, payload_version, payload_digest, lease_owner, lease_token, lease_epoch, lease_expires_at, excluded_binding, excluded_build, created_at, updated_at FROM `+r.deliveries+` WHERE false`); err != nil {
		return fmt.Errorf("%w: deliveries: %v", ErrSchemaMismatch, err)
	}
	if _, err := db.ExecContext(ctx, `SELECT namespace, scope, revision, purpose, digest, invocation_id, created_at FROM `+r.intents+` WHERE false`); err != nil {
		return fmt.Errorf("%w: intents: %v", ErrSchemaMismatch, err)
	}
	namespaceBytes := namespaceArgument(namespace)
	var application, environment string
	if err := db.QueryRowContext(ctx, `SELECT application, environment FROM `+r.catalogs+` WHERE namespace = $1`, namespaceBytes).Scan(&application, &environment); err != nil {
		return fmt.Errorf("%w: catalog binding: %v", ErrCatalogMismatch, err)
	}
	if application != namespace.Application().Value() || environment != namespace.Environment().Value() {
		return fmt.Errorf("%w: namespace application or environment differs", ErrCatalogMismatch)
	}
	persisted, err := r.readCatalogBindings(ctx, db, namespaceBytes)
	if err != nil {
		return err
	}
	bindings, err := catalogBindings(catalog)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		fingerprint, exists := persisted[binding.name]
		if !exists || fingerprint != binding.fingerprint {
			return fmt.Errorf("%w: definition %q is missing or changed", ErrCatalogMismatch, binding.name)
		}
	}
	return nil
}

func (r repository) readCatalogBindings(ctx context.Context, queryer catalogQueryer, namespace []byte) (map[string]string, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT definition, fingerprint FROM `+r.definitions+` WHERE namespace = $1`, namespace)
	if err != nil {
		return nil, fmt.Errorf("jobspg: read catalog definitions: %w", err)
	}
	defer rows.Close()
	bindings := make(map[string]string)
	for rows.Next() {
		var name, fingerprint string
		if err := rows.Scan(&name, &fingerprint); err != nil {
			return nil, fmt.Errorf("jobspg: scan catalog definition: %w", err)
		}
		bindings[name] = fingerprint
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("jobspg: read catalog definitions: %w", err)
	}
	return bindings, nil
}

func (r repository) deleteNamespace(ctx context.Context, db *sql.DB, namespace jobs.Namespace) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	argument := namespaceArgument(namespace)
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+r.deliveries+` WHERE namespace = $1`, argument); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+r.catalogs+` WHERE namespace = $1`, argument); err != nil {
		return err
	}
	return tx.Commit()
}
