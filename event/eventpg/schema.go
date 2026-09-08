package eventpg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/frostgrove/vv/event"
)

const (
	DefaultSchema = "frostgrove_events"
	SchemaVersion = 1

	DefaultMaxPayload = 64 << 10
	DefaultMaxKey     = 512
	DefaultMaxBatch   = 64
	DefaultPage       = 256
)

const fingerprintPrefix = "sha256:"

// The half of the configuration that is written into the deployed schema. Its
// zero value is the defaults. MaxPayload and MaxKey are CHECK constraint
// operands, which is why they are here and not beside the Go-side ceilings on
// Spec: a number that changes the deployed schema cannot be set from the same
// struct field as one that touches no row.
type Schema struct {
	Name       string
	MaxPayload int
	MaxKey     int
}

func (this Schema) Resolved() (Schema, error) {
	if this.Name == "" {
		this.Name = DefaultSchema
	}
	if this.MaxPayload == 0 {
		this.MaxPayload = DefaultMaxPayload
	}
	if this.MaxKey == 0 {
		this.MaxKey = DefaultMaxKey
	}
	if !validSchemaName(this.Name) {
		return Schema{}, fmt.Errorf("%w: schema name %q is not 1 to 63 bytes of [a-z][a-z0-9_]*. Every statement quotes the name, so PostgreSQL would take more; this store narrows it to the names that read the same quoted and unquoted, because an operator has to be able to type it back", ErrSpec, this.Name)
	}
	if err := within("Schema.MaxPayload", this.MaxPayload, event.MaxPayloadBytes); err != nil {
		return Schema{}, err
	}
	if err := within("Schema.MaxKey", this.MaxKey, event.MaxKeyBytes); err != nil {
		return Schema{}, err
	}
	return this, nil
}

func (this Schema) Fingerprint() (string, error) {
	resolved, err := this.Resolved()
	if err != nil {
		return "", err
	}
	return fingerprintOf(expected(resolved).rendering()), nil
}

func fingerprintOf(rendering string) string {
	digest := sha256.Sum256([]byte(rendering))
	return fingerprintPrefix + hex.EncodeToString(digest[:])
}

func within(name string, chosen, ceiling int) error {
	if chosen <= 0 {
		return fmt.Errorf("%w: %s is %d, and a bound the schema enforces is a positive number of bytes", ErrSpec, name, chosen)
	}
	if chosen > ceiling {
		return fmt.Errorf("%w: %s is %d, above the kernel ceiling of %d", ErrSpec, name, chosen, ceiling)
	}
	return nil
}

func validSchemaName(value string) bool {
	if len(value) == 0 || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		current := value[index]
		switch {
		case current >= 'a' && current <= 'z':
		case current >= '0' && current <= '9':
		case current == '_':
		default:
			return false
		}
	}
	return true
}

// What this build expects the deployed schema to be. One model, two independent
// renderings: rendering() below is what the fingerprint digests, and
// migrationStatements is what a deployment runs. A change to the model moves
// both; a change to either rendering moves only itself, which is why the
// statement list is pinned byte for byte by testdata/migration.golden and
// testdata/migration_narrow.golden and not by the fingerprint.
type expectation struct {
	schema    Schema
	tables    []expectedTable
	functions []expectedFunction
}

type expectedTable struct {
	name     string
	columns  []expectedColumn
	pk       []string
	uniques  []expectedUnique
	foreign  []expectedForeignKey
	checks   []expectedCheck
	triggers []expectedTrigger
}

type expectedColumn struct {
	name      string
	dataType  string
	notNull   bool
	byDefault string
	identity  expectedIdentity
}

type expectedIdentity struct {
	generation string
	increment  int
	cache      int
	cycle      bool
}

type expectedUnique struct {
	name    string
	columns []string
}

type expectedForeignKey struct {
	name       string
	columns    []string
	table      string
	references []string
}

type expectedCheck struct {
	name       string
	expression string
}

type expectedTrigger struct {
	name     string
	timing   string
	events   string
	level    string
	function string
}

// The body is what the trigger does, and it is data of the model for the same
// reason a check expression is: the deployment writes it, the fingerprint
// digests it and level 3 reads it back out of pg_proc. A build that changes one
// of these statements changes what the schema enforces, and the digest moves
// with it.
type expectedFunction struct {
	name string
	body []string
}

const (
	metaTable    = "schema_meta"
	streamsTable = "streams"
	eventsTable  = "events"

	appendOnlyFunction = "events_are_append_only"
	writerXidFunction  = "events_assign_writer_xid"

	textType      = "text"
	byteaType     = "bytea"
	integerType   = "integer"
	bigintType    = "bigint"
	booleanType   = "boolean"
	timestampType = "timestamp with time zone"
)

// Written the way PostgreSQL keeps it rather than the way it reads best.
// BETWEEN is expanded by the planner and pg_get_constraintdef renders the
// expansion, so a model that said BETWEEN would have to carry a second spelling
// of every check for verification to compare against — and the two would drift.
func bytesBetween(column string, high string) string {
	return "octet_length(" + column + ") >= 1 AND octet_length(" + column + ") <= " + high
}

func expected(resolved Schema) expectation {
	key := strconv.Itoa(resolved.MaxKey)
	name := strconv.Itoa(event.MaxNameBytes)
	return expectation{
		schema: resolved,
		tables: []expectedTable{
			{
				name: metaTable,
				columns: []expectedColumn{
					{name: "singleton", dataType: booleanType, notNull: true},
					{name: "version", dataType: integerType, notNull: true},
					{name: "log", dataType: byteaType, notNull: true},
					{name: "fingerprint", dataType: textType, notNull: true},
					{name: "created_at", dataType: timestampType, notNull: true, byDefault: "clock_timestamp()"},
				},
				pk: []string{"singleton"},
				checks: []expectedCheck{
					{name: "schema_meta_singleton_check", expression: "singleton"},
					{name: "schema_meta_version_check", expression: "version > 0"},
					{name: "schema_meta_log_check", expression: "octet_length(log) = 16"},
				},
			},
			{
				name: streamsTable,
				columns: []expectedColumn{
					{name: "family", dataType: textType, notNull: true},
					{name: "key", dataType: textType, notNull: true},
					{name: "version", dataType: bigintType, notNull: true},
				},
				pk: []string{"family", "key"},
				checks: []expectedCheck{
					{name: "streams_family_check", expression: bytesBetween("family", name)},
					{name: "streams_key_check", expression: bytesBetween("key", key)},
					{name: "streams_version_check", expression: "version > 0"},
				},
			},
			{
				name: eventsTable,
				columns: []expectedColumn{
					{name: "position", dataType: bigintType, notNull: true, identity: expectedIdentity{generation: "always", increment: 1, cache: 1}},
					{name: "family", dataType: textType, notNull: true},
					{name: "key", dataType: textType, notNull: true},
					{name: "version", dataType: bigintType, notNull: true},
					{name: "type", dataType: textType, notNull: true},
					{name: "revision", dataType: integerType, notNull: true},
					{name: "payload", dataType: byteaType, notNull: true},
					{name: "recorded_at", dataType: timestampType, notNull: true},
				},
				pk:      []string{"position"},
				uniques: []expectedUnique{{name: "events_stream_version_key", columns: []string{"family", "key", "version"}}},
				foreign: []expectedForeignKey{{
					name:       "events_stream_fkey",
					columns:    []string{"family", "key"},
					table:      streamsTable,
					references: []string{"family", "key"},
				}},
				checks: []expectedCheck{
					{name: "events_family_check", expression: bytesBetween("family", name)},
					{name: "events_key_check", expression: bytesBetween("key", key)},
					{name: "events_version_check", expression: "version > 0"},
					{name: "events_type_check", expression: bytesBetween("type", name)},
					{name: "events_revision_check", expression: "revision > 0"},
					{name: "events_payload_check", expression: "octet_length(payload) <= " + strconv.Itoa(resolved.MaxPayload)},
				},
				triggers: []expectedTrigger{
					{name: "events_append_only_row", timing: "BEFORE", events: "UPDATE OR DELETE", level: "ROW", function: appendOnlyFunction},
					{name: "events_append_only_truncate", timing: "BEFORE", events: "TRUNCATE", level: "STATEMENT", function: appendOnlyFunction},
					{name: "events_position_needs_xid", timing: "BEFORE", events: "INSERT", level: "STATEMENT", function: writerXidFunction},
				},
			},
		},
		functions: []expectedFunction{
			{name: appendOnlyFunction, body: []string{
				"RAISE EXCEPTION 'eventpg: % on an event row: history is append-only', TG_OP;",
			}},
			{name: writerXidFunction, body: []string{
				"PERFORM pg_current_xact_id();",
				"RETURN NULL;",
			}},
		},
	}
}

func (this expectedTable) primaryKeyName() string { return this.name + "_pkey" }

// The rendering the fingerprint digests. Three header lines, then one line per
// object sorted as text, so a reordering of the model above is not a new
// schema and a changed constraint is a diff a person reads rather than a digest
// nobody can account for. Nothing here is read back out of pg_catalog: a
// fingerprint computed from the database would agree with the database by
// construction. Version 1 creates no index that a constraint does not create
// with its table, so the rendering carries no index line.
func (this expectation) rendering() string {
	lines := []string{
		"eventpg/schema/v" + strconv.Itoa(SchemaVersion),
		"schema " + this.schema.Name,
		"bounds maxpayload=" + strconv.Itoa(this.schema.MaxPayload) + " maxkey=" + strconv.Itoa(this.schema.MaxKey),
	}
	var objects []string
	for _, table := range this.tables {
		for ordinal, column := range table.columns {
			objects = append(objects, "column "+table.name+" "+column.name+" "+strconv.Itoa(ordinal+1)+" "+
				column.dataType+" "+strconv.FormatBool(column.notNull)+" "+
				orDash(column.byDefault)+" "+orDash(column.identity.generation))
			if column.identity.present() {
				objects = append(objects, "sequence "+table.name+" "+column.name+" "+column.identity.rendering())
			}
		}
		objects = append(objects, "pk "+table.name+" ("+strings.Join(table.pk, ", ")+")")
		for _, unique := range table.uniques {
			objects = append(objects, "unique "+table.name+" "+unique.name+" ("+strings.Join(unique.columns, ", ")+")")
		}
		for _, foreign := range table.foreign {
			objects = append(objects, "fk "+table.name+" "+foreign.name+" ("+strings.Join(foreign.columns, ", ")+
				") -> "+foreign.table+" ("+strings.Join(foreign.references, ", ")+")")
		}
		for _, check := range table.checks {
			objects = append(objects, "check "+table.name+" "+check.name+" "+collapsed(check.definition()))
		}
		for _, trigger := range table.triggers {
			objects = append(objects, "trigger "+table.name+" "+trigger.name+" "+trigger.timing+" "+
				trigger.events+" "+trigger.level+" "+trigger.function+" columns=all condition=none")
		}
		objects = append(objects, "rule "+table.name+" none", "rls "+table.name+" disabled",
			"persistence "+table.name+" permanent")
	}
	for _, function := range this.functions {
		objects = append(objects, "function "+function.name+" "+collapsed(function.source()))
	}
	slices.Sort(objects)

	var out strings.Builder
	for _, line := range append(lines, objects...) {
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

func (this expectedCheck) definition() string { return "CHECK (" + this.expression + ")" }

// What pg_proc.prosrc holds once the deployment has run createFunction: the
// block between the dollar quotes, whitespace aside.
func (this expectedFunction) source() string {
	return "BEGIN " + strings.Join(this.body, " ") + " END"
}

func (this expectedIdentity) present() bool { return this.generation != "" }

func (this expectedIdentity) rendering() string {
	return "increment=" + strconv.Itoa(this.increment) + " cache=" + strconv.Itoa(this.cache) +
		" cycle=" + strconv.FormatBool(this.cycle)
}

func collapsed(value string) string { return strings.Join(strings.Fields(value), " ") }

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// The statements a deployment's migration step runs, in order. Every one is
// transactional DDL at schema version 1 — there is no CREATE INDEX CONCURRENTLY
// among them — so the whole list is safe inside one transaction, which is what
// MIGRATIONS.md says and what makes a half-created schema unrepresentable.
func MigrationStatements(schema Schema) ([]string, error) {
	resolved, err := schema.Resolved()
	if err != nil {
		return nil, err
	}
	fingerprint, err := resolved.Fingerprint()
	if err != nil {
		return nil, err
	}
	return migrationStatements(expected(resolved), fingerprint), nil
}
