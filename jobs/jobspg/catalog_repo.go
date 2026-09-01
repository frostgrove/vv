package jobspg

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/frostgrove/vv/jobs"
)

type catalogBinding struct {
	name              string
	fingerprint       string
	codec             string
	codecMode         string
	codecVersion      jobs.SchemaVersion
	codecRevisions    []jobs.SchemaVersion
	partition         jobs.PartitionMode
	identity          string
	identityVersion   jobs.SchemaVersion
	identityAvailable bool
	identityAutomatic bool
}

type storedCatalogBinding struct {
	name              string
	fingerprint       string
	codec             sql.NullString
	codecMode         sql.NullString
	codecVersion      sql.NullInt64
	codecRevisions    sql.NullString
	partition         sql.NullInt64
	identity          sql.NullString
	identityVersion   sql.NullInt64
	identityAutomatic sql.NullBool
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
		descriptor := declaration.Describe()
		for _, upcast := range descriptor.Codec.Upcasts {
			if upcast.SourceCodec != descriptor.Codec.ID || upcast.TargetCodec != descriptor.Codec.ID {
				return nil, fmt.Errorf("%w: definition %q changes codec identity", ErrCatalogMismatch, descriptor.Name)
			}
		}
		identity := ""
		if descriptor.PayloadIdentity.Available {
			identity = descriptor.PayloadIdentity.ID.Value()
		}
		bindings[index] = catalogBinding{
			name:              descriptor.Name.Value(),
			fingerprint:       single.Fingerprint(),
			codec:             descriptor.Codec.ID.Value(),
			codecMode:         string(descriptor.Codec.Mode),
			codecVersion:      descriptor.Codec.CurrentVersion,
			codecRevisions:    append([]jobs.SchemaVersion(nil), descriptor.Codec.SupportedRevisions...),
			partition:         descriptor.Partition,
			identity:          identity,
			identityVersion:   descriptor.PayloadIdentity.Version,
			identityAvailable: descriptor.PayloadIdentity.Available,
			identityAutomatic: descriptor.PayloadIdentity.Automatic,
		}
	}
	return bindings, nil
}

func (b catalogBinding) compatibleWith(stored storedCatalogBinding) (bool, error) {
	if stored.legacy() {
		if stored.fingerprint != b.fingerprint {
			return false, fmt.Errorf("%w: legacy definition %q cannot prove compatibility", ErrCatalogMismatch, b.name)
		}
		return true, nil
	}
	revisions, err := stored.revisions()
	if err != nil {
		return false, err
	}
	storedVersion, err := schemaVersion(stored.codecVersion.Int64)
	if err != nil {
		return false, err
	}
	partition := jobs.PartitionMode(stored.partition.Int64)
	if !partition.Valid() || stored.partition.Int64 != int64(partition) {
		return false, fmt.Errorf("%w: definition %q partition metadata is invalid", ErrSchemaMismatch, b.name)
	}
	if stored.codec.String != b.codec || stored.codecMode.String != b.codecMode || partition != b.partition || stored.identity.Valid != b.identityAvailable || stored.identity.String != b.identity || stored.identityVersion.Valid != b.identityAvailable || stored.identityAutomatic.Bool != b.identityAutomatic {
		return false, fmt.Errorf("%w: definition %q wire contract changed", ErrCatalogMismatch, b.name)
	}
	if b.identityAvailable {
		identityVersion, identityErr := schemaVersion(stored.identityVersion.Int64)
		if identityErr != nil || identityVersion != b.identityVersion {
			return false, fmt.Errorf("%w: definition %q payload identity changed", ErrCatalogMismatch, b.name)
		}
	}
	switch {
	case b.codecVersion < storedVersion:
		if !containsRevision(revisions, b.codecVersion) || !revisionSubset(b.codecRevisions, revisions) {
			return false, fmt.Errorf("%w: definition %q is not a known rolling revision", ErrCatalogMismatch, b.name)
		}
		return false, nil
	case b.codecVersion == storedVersion:
		if !slices.Equal(b.codecRevisions, revisions) {
			return false, fmt.Errorf("%w: definition %q changed revisions without increasing its version", ErrCatalogMismatch, b.name)
		}
		return false, nil
	default:
		if !revisionSubset(revisions, b.codecRevisions) {
			return false, fmt.Errorf("%w: definition %q no longer consumes every persisted revision", ErrCatalogMismatch, b.name)
		}
		return true, nil
	}
}

func (s storedCatalogBinding) legacy() bool {
	return !s.codec.Valid && !s.codecMode.Valid && !s.codecVersion.Valid && !s.codecRevisions.Valid && !s.partition.Valid && !s.identity.Valid && !s.identityVersion.Valid && !s.identityAutomatic.Valid
}

func (s storedCatalogBinding) revisions() ([]jobs.SchemaVersion, error) {
	if !s.codec.Valid || !s.codecMode.Valid || !s.codecVersion.Valid || !s.codecRevisions.Valid || !s.partition.Valid || !s.identityAutomatic.Valid || s.identity.Valid != s.identityVersion.Valid {
		return nil, fmt.Errorf("%w: definition %q compatibility metadata is incomplete", ErrSchemaMismatch, s.name)
	}
	parts := strings.Split(s.codecRevisions.String, ",")
	if len(parts) == 0 || len(parts) > jobs.MaxSupportedRevisions {
		return nil, fmt.Errorf("%w: definition %q revisions are invalid", ErrSchemaMismatch, s.name)
	}
	revisions := make([]jobs.SchemaVersion, len(parts))
	for index, part := range parts {
		value, err := strconv.ParseUint(part, 10, 32)
		if err != nil || value == 0 {
			return nil, fmt.Errorf("%w: definition %q revisions are invalid", ErrSchemaMismatch, s.name)
		}
		revisions[index] = jobs.SchemaVersion(value)
		if index > 0 && revisions[index-1] >= revisions[index] {
			return nil, fmt.Errorf("%w: definition %q revisions are invalid", ErrSchemaMismatch, s.name)
		}
	}
	version, err := schemaVersion(s.codecVersion.Int64)
	if err != nil || revisions[len(revisions)-1] != version || encodeRevisions(revisions) != s.codecRevisions.String {
		return nil, fmt.Errorf("%w: definition %q revisions are invalid", ErrSchemaMismatch, s.name)
	}
	return revisions, nil
}

func schemaVersion(value int64) (jobs.SchemaVersion, error) {
	version := jobs.SchemaVersion(value)
	if value <= 0 || int64(version) != value {
		return 0, ErrSchemaMismatch
	}
	return version, nil
}

func encodeRevisions(revisions []jobs.SchemaVersion) string {
	values := make([]string, len(revisions))
	for index, revision := range revisions {
		values[index] = strconv.FormatUint(uint64(revision), 10)
	}
	return strings.Join(values, ",")
}

func containsRevision(revisions []jobs.SchemaVersion, target jobs.SchemaVersion) bool {
	_, found := slices.BinarySearch(revisions, target)
	return found
}

func revisionSubset(subset, superset []jobs.SchemaVersion) bool {
	for _, revision := range subset {
		if !containsRevision(superset, revision) {
			return false
		}
	}
	return true
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
	var bootstrapFingerprint, application, environment string
	if err := tx.QueryRowContext(ctx, `SELECT fingerprint, application, environment FROM `+r.catalogs+` WHERE namespace = $1 FOR UPDATE`, namespaceBytes).Scan(&bootstrapFingerprint, &application, &environment); err != nil {
		return fmt.Errorf("jobspg: read catalog binding: %w", err)
	}
	if application != namespace.Application().Value() || environment != namespace.Environment().Value() {
		return fmt.Errorf("%w: namespace is already bound to another application or environment", ErrCatalogMismatch)
	}
	persisted, err := r.readCatalogBindings(ctx, tx, namespaceBytes)
	if err != nil {
		return err
	}
	if inserted == 0 && len(persisted) == 0 && bootstrapFingerprint != catalog.Fingerprint() {
		return fmt.Errorf("%w: legacy catalog fingerprint cannot prove an additive change", ErrCatalogMismatch)
	}
	for _, binding := range bindings {
		stored, exists := persisted[binding.name]
		if !exists {
			if err := r.insertCatalogBinding(ctx, tx, namespaceBytes, binding); err != nil {
				return err
			}
			continue
		}
		upgrade, err := binding.compatibleWith(stored)
		if err != nil {
			return err
		}
		if stored.legacy() {
			if err := r.backfillCatalogBinding(ctx, tx, namespaceBytes, binding); err != nil {
				return err
			}
			continue
		}
		if upgrade {
			if err := r.upgradeCatalogBinding(ctx, tx, namespaceBytes, binding); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("jobspg: commit catalog binding: %w", err)
	}
	return nil
}

func (r repository) insertCatalogBinding(ctx context.Context, tx *sql.Tx, namespace []byte, binding catalogBinding) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO `+r.definitions+` (namespace, definition, fingerprint, codec, codec_mode, codec_version, codec_revisions, partition_mode, payload_identity, payload_identity_version, payload_identity_automatic)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, namespace, binding.name, binding.fingerprint, binding.codec, binding.codecMode, int64(binding.codecVersion), encodeRevisions(binding.codecRevisions), int64(binding.partition), nullableIdentity(binding), nullableIdentityVersion(binding), binding.identityAutomatic)
	if err != nil {
		return fmt.Errorf("jobspg: bind definition %q: %w", binding.name, err)
	}
	return nil
}

func (r repository) backfillCatalogBinding(ctx context.Context, tx *sql.Tx, namespace []byte, binding catalogBinding) error {
	result, err := tx.ExecContext(ctx, `UPDATE `+r.definitions+`
SET codec = $3, codec_mode = $4, codec_version = $5, codec_revisions = $6, partition_mode = $7, payload_identity = $8, payload_identity_version = $9, payload_identity_automatic = $10
WHERE namespace = $1 AND definition = $2 AND codec IS NULL`, namespace, binding.name, binding.codec, binding.codecMode, int64(binding.codecVersion), encodeRevisions(binding.codecRevisions), int64(binding.partition), nullableIdentity(binding), nullableIdentityVersion(binding), binding.identityAutomatic)
	if err != nil {
		return fmt.Errorf("jobspg: backfill definition %q: %w", binding.name, err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return fmt.Errorf("%w: definition %q was concurrently changed", ErrCatalogMismatch, binding.name)
	}
	return nil
}

func (r repository) upgradeCatalogBinding(ctx context.Context, tx *sql.Tx, namespace []byte, binding catalogBinding) error {
	result, err := tx.ExecContext(ctx, `UPDATE `+r.definitions+`
SET fingerprint = $3, codec_version = $4, codec_revisions = $5
WHERE namespace = $1 AND definition = $2 AND codec_version < $4`, namespace, binding.name, binding.fingerprint, int64(binding.codecVersion), encodeRevisions(binding.codecRevisions))
	if err != nil {
		return fmt.Errorf("jobspg: upgrade definition %q: %w", binding.name, err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return fmt.Errorf("%w: definition %q was concurrently changed", ErrCatalogMismatch, binding.name)
	}
	return nil
}

func nullableIdentity(binding catalogBinding) any {
	if !binding.identityAvailable {
		return nil
	}
	return binding.identity
}

func nullableIdentityVersion(binding catalogBinding) any {
	if !binding.identityAvailable {
		return nil
	}
	return int64(binding.identityVersion)
}

func (r repository) check(ctx context.Context, db *sql.DB, namespace jobs.Namespace, catalog jobs.Catalog) error {
	var version int
	if err := db.QueryRowContext(ctx, `SELECT version FROM `+r.meta+` WHERE singleton = true`).Scan(&version); err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaMismatch, err)
	}
	if version != SchemaVersion {
		return fmt.Errorf("%w: have %d, need %d", ErrSchemaMismatch, version, SchemaVersion)
	}
	if _, err := db.ExecContext(ctx, `SELECT namespace, definition, fingerprint, codec, codec_mode, codec_version, codec_revisions, partition_mode, payload_identity, payload_identity_version, payload_identity_automatic, created_at FROM `+r.definitions+` WHERE false`); err != nil {
		return fmt.Errorf("%w: catalog definitions: %v", ErrSchemaMismatch, err)
	}
	if _, err := db.ExecContext(ctx, `SELECT namespace, id, definition, codec, codec_version, priority, state, available_at, record_size, record, record_expires_at, intent_expires_at, intent_keys, payload_identity, payload_version, payload_digest, lease_owner, lease_token, lease_epoch, lease_expires_at, excluded_binding, excluded_build, created_at, updated_at FROM `+r.deliveries+` WHERE false`); err != nil {
		return fmt.Errorf("%w: deliveries: %v", ErrSchemaMismatch, err)
	}
	if _, err := db.ExecContext(ctx, `SELECT namespace, scope, revision, purpose, digest, invocation_id, created_at FROM `+r.intents+` WHERE false`); err != nil {
		return fmt.Errorf("%w: intents: %v", ErrSchemaMismatch, err)
	}
	if err := r.validateIntentKeysColumn(ctx, db); err != nil {
		return err
	}
	if err := r.validateSchemaConstraints(ctx, db); err != nil {
		return err
	}
	if err := r.validateOperationalIndexes(ctx, db); err != nil {
		return err
	}
	if err := r.validateRetentionIndexes(ctx, db); err != nil {
		return err
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
		stored, exists := persisted[binding.name]
		if !exists || stored.legacy() {
			return fmt.Errorf("%w: definition %q is missing compatibility metadata", ErrCatalogMismatch, binding.name)
		}
		upgrade, err := binding.compatibleWith(stored)
		if err != nil {
			return err
		}
		if upgrade {
			return fmt.Errorf("%w: definition %q schema version is not bound", ErrCatalogMismatch, binding.name)
		}
	}
	return nil
}

func (r repository) readCatalogBindings(ctx context.Context, queryer catalogQueryer, namespace []byte) (map[string]storedCatalogBinding, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT definition, fingerprint, codec, codec_mode, codec_version, codec_revisions, partition_mode, payload_identity, payload_identity_version, payload_identity_automatic FROM `+r.definitions+` WHERE namespace = $1`, namespace)
	if err != nil {
		return nil, fmt.Errorf("jobspg: read catalog definitions: %w", err)
	}
	defer rows.Close()
	bindings := make(map[string]storedCatalogBinding)
	for rows.Next() {
		var binding storedCatalogBinding
		if err := rows.Scan(&binding.name, &binding.fingerprint, &binding.codec, &binding.codecMode, &binding.codecVersion, &binding.codecRevisions, &binding.partition, &binding.identity, &binding.identityVersion, &binding.identityAutomatic); err != nil {
			return nil, fmt.Errorf("jobspg: scan catalog definition: %w", err)
		}
		if _, exists := bindings[binding.name]; exists {
			return nil, fmt.Errorf("%w: duplicate definition %q", ErrSchemaMismatch, binding.name)
		}
		bindings[binding.name] = binding
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
