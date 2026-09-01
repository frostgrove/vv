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
	schema      string
	meta        string
	catalogs    string
	definitions string
	deliveries  string
	intents     string
}

type storedDelivery struct {
	id              jobs.InvocationID
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

func (r repository) migrationStatements() []string {
	return []string{
		`CREATE SCHEMA IF NOT EXISTS ` + r.schema,
		`CREATE TABLE IF NOT EXISTS ` + r.meta + ` (
singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
version integer NOT NULL CHECK (version > 0)
)`,
		`INSERT INTO ` + r.meta + ` (singleton, version) VALUES (true, 2) ON CONFLICT (singleton) DO NOTHING`,
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
created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
PRIMARY KEY (namespace, definition),
FOREIGN KEY (namespace) REFERENCES ` + r.catalogs + ` (namespace) ON DELETE CASCADE
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
record_size integer NOT NULL CHECK (record_size > 0),
record bytea NOT NULL,
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
CHECK ((excluded_binding IS NULL) = (excluded_build IS NULL))
)`,
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
		`CREATE INDEX IF NOT EXISTS deliveries_ready_idx ON ` + r.deliveries + ` (namespace, definition, priority, available_at, id) WHERE state = 1 AND lease_token IS NULL`,
		`CREATE INDEX IF NOT EXISTS deliveries_expired_idx ON ` + r.deliveries + ` (namespace, lease_expires_at, id) WHERE lease_token IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS intents_invocation_idx ON ` + r.intents + ` (namespace, invocation_id)`,
	}
}

func MigrationStatements(schema string) ([]string, error) {
	if schema == "" {
		schema = DefaultSchema
	}
	if !validSchema(schema) {
		return nil, fmt.Errorf("jobspg: %w: invalid PostgreSQL schema", jobs.ErrInvalid)
	}
	statements := newRepository(schema).migrationStatements()
	return append([]string(nil), statements...), nil
}

func (r repository) migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range r.migrationStatements() {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("jobspg: migrate: %w", err)
		}
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM `+r.meta+` WHERE singleton = true`).Scan(&version); err != nil {
		return fmt.Errorf("jobspg: migrate version: %w", err)
	}
	if version != SchemaVersion {
		return fmt.Errorf("%w: have %d, need %d", ErrSchemaMismatch, version, SchemaVersion)
	}
	return tx.Commit()
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
