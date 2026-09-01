package jobspg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/frostgrove/vv/jobs"
)

var errIntentConflict = errors.New("jobspg: intent conflict")
var errCandidateConflict = errors.New("jobspg: candidate conflict")

type repository struct {
	rawSchema   string
	schema      string
	meta        string
	catalogs    string
	definitions string
	deliveries  string
	intents     string
}

type storedDelivery struct {
	id              jobs.InvocationID
	definition      jobs.Name
	record          []byte
	recordSize      int
	state           jobs.InvocationState
	createdAt       time.Time
	availableAt     time.Time
	payloadIdentity string
	payloadVersion  jobs.SchemaVersion
	payloadDigest   []byte
	leaseToken      []byte
}

type claimCandidate struct {
	id         jobs.InvocationID
	record     []byte
	recordSize int
}

type expiredCandidate struct {
	id         jobs.InvocationID
	record     []byte
	recordSize int
}

func newRepository(schema string) repository {
	quoted := quoteIdentifier(schema)
	return repository{
		rawSchema:   schema,
		schema:      quoted,
		meta:        quoted + ".schema_meta",
		catalogs:    quoted + ".catalogs",
		definitions: quoted + ".catalog_definitions",
		deliveries:  quoted + ".deliveries",
		intents:     quoted + ".intents",
	}
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func (r repository) migrationBootstrapStatements() []string {
	return []string{
		`CREATE SCHEMA IF NOT EXISTS ` + r.schema,
		`CREATE TABLE IF NOT EXISTS ` + r.meta + ` (
singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
version integer NOT NULL CHECK (version > 0)
)`,
		`INSERT INTO ` + r.meta + ` (singleton, version) VALUES (true, 2) ON CONFLICT (singleton) DO NOTHING`,
	}
}

func (r repository) migrationStatements() []string {
	statements := r.migrationBootstrapStatements()
	statements = append(statements, r.migrationUpgradeStatements()...)
	statements = append(statements, r.catalogEvolutionMigrationStatements()...)
	return append(statements, r.schemaHardeningMigrationStatements()...)
}

func (r repository) migrationUpgradeStatements() []string {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS ` + r.catalogs + ` (
namespace bytea PRIMARY KEY CHECK (octet_length(namespace) = 32),
application text NOT NULL,
environment text NOT NULL,
fingerprint text NOT NULL,
created_at timestamptz NOT NULL DEFAULT clock_timestamp()
)`,
		`CREATE TABLE IF NOT EXISTS ` + r.definitions + ` (
namespace bytea NOT NULL CHECK (octet_length(namespace) = 32),
definition text NOT NULL,
fingerprint text NOT NULL,
codec text,
codec_mode text,
codec_version bigint,
codec_revisions text,
partition_mode smallint,
payload_identity text,
payload_identity_version bigint,
payload_identity_automatic boolean,
created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
PRIMARY KEY (namespace, definition),
FOREIGN KEY (namespace) REFERENCES ` + r.catalogs + ` (namespace) ON DELETE CASCADE,
CONSTRAINT catalog_definitions_contract_check CHECK (
  (codec IS NULL AND codec_mode IS NULL AND codec_version IS NULL AND codec_revisions IS NULL AND partition_mode IS NULL AND payload_identity IS NULL AND payload_identity_version IS NULL AND payload_identity_automatic IS NULL)
  OR
  (codec <> '' AND codec_mode <> '' AND codec_version > 0 AND codec_revisions <> '' AND partition_mode IN (0, 1) AND payload_identity_automatic IS NOT NULL AND (
    (payload_identity IS NULL AND payload_identity_version IS NULL AND NOT payload_identity_automatic)
    OR
    (payload_identity <> '' AND payload_identity_version > 0)
  ))
)
)`,
		`UPDATE ` + r.meta + ` SET version = 2 WHERE singleton = true AND version = 1`,
		`CREATE TABLE IF NOT EXISTS ` + r.deliveries + ` (
namespace bytea NOT NULL CHECK (octet_length(namespace) = 32),
id bytea NOT NULL CHECK (octet_length(id) = 16),
definition text NOT NULL,
codec text NOT NULL,
codec_version bigint NOT NULL CHECK (codec_version > 0),
priority integer NOT NULL CHECK (priority > 0),
state smallint NOT NULL,
available_at timestamptz,
record_size integer CHECK (record_size > 0),
record bytea,
record_expires_at timestamptz,
intent_expires_at timestamptz,
payload_identity text,
payload_version bigint,
payload_digest bytea,
lease_owner bytea,
lease_token bytea,
lease_epoch bigint NOT NULL DEFAULT 0,
lease_expires_at timestamptz,
excluded_binding text,
excluded_build text,
created_at timestamptz NOT NULL,
updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
PRIMARY KEY (namespace, id),
CHECK ((payload_digest IS NULL AND payload_identity IS NULL AND payload_version IS NULL) OR (octet_length(payload_digest) = 32 AND payload_identity IS NOT NULL AND payload_version > 0)),
CHECK ((lease_token IS NULL AND lease_owner IS NULL AND lease_expires_at IS NULL) OR (octet_length(lease_token) = 32 AND octet_length(lease_owner) = 16 AND lease_expires_at IS NOT NULL)),
CHECK ((excluded_binding IS NULL) = (excluded_build IS NULL)),
CONSTRAINT deliveries_record_pair_check CHECK ((record IS NULL) = (record_size IS NULL)),
CONSTRAINT deliveries_retention_deadline_pair_check CHECK ((record_expires_at IS NULL) = (intent_expires_at IS NULL))
)`,
		`ALTER TABLE ` + r.deliveries + ` ALTER COLUMN record DROP NOT NULL`,
		`ALTER TABLE ` + r.deliveries + ` ALTER COLUMN record_size DROP NOT NULL`,
		`ALTER TABLE ` + r.deliveries + ` ADD COLUMN IF NOT EXISTS record_expires_at timestamptz`,
		`ALTER TABLE ` + r.deliveries + ` ADD COLUMN IF NOT EXISTS intent_expires_at timestamptz`,
		`DO $frostgrove$
BEGIN
IF NOT EXISTS (
  SELECT 1
  FROM pg_constraint
  WHERE conname = 'deliveries_record_pair_check'
    AND conrelid = '` + r.deliveries + `'::regclass
) THEN
  ALTER TABLE ` + r.deliveries + ` ADD CONSTRAINT deliveries_record_pair_check CHECK ((record IS NULL) = (record_size IS NULL)) NOT VALID;
END IF;
END
$frostgrove$`,
		`ALTER TABLE ` + r.deliveries + ` VALIDATE CONSTRAINT deliveries_record_pair_check`,
		`DO $frostgrove$
BEGIN
IF NOT EXISTS (
  SELECT 1
  FROM pg_constraint
  WHERE conname = 'deliveries_retention_deadline_pair_check'
    AND conrelid = '` + r.deliveries + `'::regclass
) THEN
  ALTER TABLE ` + r.deliveries + ` ADD CONSTRAINT deliveries_retention_deadline_pair_check CHECK ((record_expires_at IS NULL) = (intent_expires_at IS NULL)) NOT VALID;
END IF;
END
$frostgrove$`,
		`ALTER TABLE ` + r.deliveries + ` VALIDATE CONSTRAINT deliveries_retention_deadline_pair_check`,
		`CREATE TABLE IF NOT EXISTS ` + r.intents + ` (
namespace bytea NOT NULL CHECK (octet_length(namespace) = 32),
scope bytea NOT NULL CHECK (octet_length(scope) = 32),
revision smallint NOT NULL CHECK (revision > 0),
purpose smallint NOT NULL CHECK (purpose > 0),
digest bytea NOT NULL CHECK (octet_length(digest) = 32),
invocation_id bytea NOT NULL CHECK (octet_length(invocation_id) = 16),
created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
PRIMARY KEY (namespace, scope, revision, purpose, digest),
FOREIGN KEY (namespace, invocation_id) REFERENCES ` + r.deliveries + ` (namespace, id) ON DELETE CASCADE
)`,
	}
	return append(statements, r.operationalIndexStatements()...)
}

func (r repository) catalogEvolutionMigrationStatements() []string {
	return []string{
		`ALTER TABLE ` + r.definitions + ` ADD COLUMN IF NOT EXISTS codec text`,
		`ALTER TABLE ` + r.definitions + ` ADD COLUMN IF NOT EXISTS codec_mode text`,
		`ALTER TABLE ` + r.definitions + ` ADD COLUMN IF NOT EXISTS codec_version bigint`,
		`ALTER TABLE ` + r.definitions + ` ADD COLUMN IF NOT EXISTS codec_revisions text`,
		`ALTER TABLE ` + r.definitions + ` ADD COLUMN IF NOT EXISTS partition_mode smallint`,
		`ALTER TABLE ` + r.definitions + ` ADD COLUMN IF NOT EXISTS payload_identity text`,
		`ALTER TABLE ` + r.definitions + ` ADD COLUMN IF NOT EXISTS payload_identity_version bigint`,
		`ALTER TABLE ` + r.definitions + ` ADD COLUMN IF NOT EXISTS payload_identity_automatic boolean`,
		`DO $frostgrove$
BEGIN
IF NOT EXISTS (
  SELECT 1
  FROM pg_constraint
  WHERE conname = 'catalog_definitions_contract_check'
    AND conrelid = '` + r.definitions + `'::regclass
) THEN
  ALTER TABLE ` + r.definitions + ` ADD CONSTRAINT catalog_definitions_contract_check CHECK (
    (codec IS NULL AND codec_mode IS NULL AND codec_version IS NULL AND codec_revisions IS NULL AND partition_mode IS NULL AND payload_identity IS NULL AND payload_identity_version IS NULL AND payload_identity_automatic IS NULL)
    OR
    (codec <> '' AND codec_mode <> '' AND codec_version > 0 AND codec_revisions <> '' AND partition_mode IN (0, 1) AND payload_identity_automatic IS NOT NULL AND (
      (payload_identity IS NULL AND payload_identity_version IS NULL AND NOT payload_identity_automatic)
      OR
      (payload_identity <> '' AND payload_identity_version > 0)
    ))
  ) NOT VALID;
END IF;
END
$frostgrove$`,
		`ALTER TABLE ` + r.definitions + ` VALIDATE CONSTRAINT catalog_definitions_contract_check`,
	}
}

func MigrationStatements(schema string) ([]string, error) {
	if schema == "" {
		schema = DefaultSchema
	}
	if !validSchema(schema) {
		return nil, fmt.Errorf("jobspg: %w: invalid PostgreSQL schema", jobs.ErrInvalid)
	}
	repo := newRepository(schema)
	statements := repo.migrationStatements()
	statements = append(statements, repo.retentionIndexStatements(true)...)
	statements = append(statements, repo.retentionIndexValidationStatements()...)
	statements = append(statements, repo.retentionIndexCommentStatements()...)
	statements = append(statements, repo.operationalIndexValidationStatements()...)
	statements = append(statements, repo.schemaConstraintValidationStatements()...)
	statements = append(statements, `UPDATE `+repo.meta+` SET version = 4 WHERE singleton = true AND version IN (1, 2, 3)`)
	return append([]string(nil), statements...), nil
}

func (r repository) migrate(ctx context.Context, db *sql.DB) error {
	return r.withMigrationLock(ctx, db, func(conn *sql.Conn) error {
		return r.migrateLocked(ctx, conn)
	})
}

func (r repository) migrateLocked(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range r.migrationBootstrapStatements() {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("jobspg: migrate: %w", err)
		}
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM `+r.meta+` WHERE singleton = true`).Scan(&version); err != nil {
		return fmt.Errorf("jobspg: migrate version: %w", err)
	}
	if version < 1 || version > SchemaVersion {
		return fmt.Errorf("%w: unsupported intermediate version %d", ErrSchemaMismatch, version)
	}
	currentVersion := version == SchemaVersion
	needsRetentionMigration := version <= 2
	if !currentVersion && needsRetentionMigration {
		for _, statement := range r.migrationUpgradeStatements() {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("jobspg: migrate: %w", err)
			}
		}
		if err := tx.QueryRowContext(ctx, `SELECT version FROM `+r.meta+` WHERE singleton = true`).Scan(&version); err != nil {
			return fmt.Errorf("jobspg: migrate version: %w", err)
		}
		if version != 2 {
			return fmt.Errorf("%w: unsupported intermediate version %d", ErrSchemaMismatch, version)
		}
	}
	if !currentVersion {
		for _, statement := range r.catalogEvolutionMigrationStatements() {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("jobspg: migrate catalog contracts: %w", err)
			}
		}
	}
	for _, statement := range r.schemaHardeningMigrationStatements() {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("jobspg: harden schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if currentVersion {
		if err := r.validateSchemaConstraints(ctx, conn); err != nil {
			return err
		}
		if err := r.validateRetentionIndexes(ctx, conn); err != nil {
			return err
		}
		return r.validateOperationalIndexes(ctx, conn)
	}
	if needsRetentionMigration {
		if err := r.buildRetentionIndexes(ctx, conn); err != nil {
			return err
		}
	} else if err := r.validateRetentionIndexes(ctx, conn); err != nil {
		return err
	}
	if err := r.validateOperationalIndexes(ctx, conn); err != nil {
		return err
	}
	return r.finalizeMigration(ctx, conn)
}

func databaseNow(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, err
	}
	return now.Round(0).UTC(), nil
}

func namespaceArgument(namespace jobs.Namespace) []byte {
	value := namespace.Digest()
	return value[:]
}

func invocationArgument(id jobs.InvocationID) []byte {
	value := id.Bytes()
	return value[:]
}

func scanInvocation(raw []byte) (jobs.InvocationID, error) {
	if len(raw) != jobs.InvocationIDBytes {
		return jobs.InvocationID{}, fmt.Errorf("jobspg: invalid invocation id")
	}
	var value [jobs.InvocationIDBytes]byte
	copy(value[:], raw)
	return jobs.InvocationIDFromBytes(value)
}

func scanIntentKey(rawScope []byte, rawRevision, rawPurpose int, rawDigest []byte) (jobs.IntentKey, error) {
	if len(rawScope) != jobs.IntentScopeBytes || len(rawDigest) != jobs.IntentDigestBytes {
		return jobs.IntentKey{}, fmt.Errorf("jobspg: invalid intent key")
	}
	var scopeBytes [jobs.IntentScopeBytes]byte
	copy(scopeBytes[:], rawScope)
	scope, err := jobs.IntentScopeBindingFromBytes(scopeBytes)
	if err != nil {
		return jobs.IntentKey{}, fmt.Errorf("jobspg: invalid intent scope: %w", err)
	}
	var digestBytes [jobs.IntentDigestBytes]byte
	copy(digestBytes[:], rawDigest)
	digest, err := jobs.IntentDigestFromBytes(digestBytes)
	if err != nil {
		return jobs.IntentKey{}, fmt.Errorf("jobspg: invalid intent digest: %w", err)
	}
	key, err := jobs.NewIntentKey(scope, jobs.DigestRevision(rawRevision), jobs.IntentPurpose(rawPurpose), digest)
	if err != nil {
		return jobs.IntentKey{}, fmt.Errorf("jobspg: invalid intent key: %w", err)
	}
	return key, nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt(value jobs.SchemaVersion) any {
	if value.IsZero() {
		return nil
	}
	return int64(value)
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
