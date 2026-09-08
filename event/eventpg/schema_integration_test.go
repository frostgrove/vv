//go:build integration

package eventpg

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
)

// The schema name every mutation statement below is written against, replaced
// rather than formatted so a statement naming the schema three times does not
// have to count its arguments.
const schemaMark = "@"

func against(schema Schema, statement string) string {
	return strings.ReplaceAll(statement, schemaMark, quoteIdentifier(schema.Name))
}

func mutate(t *testing.T, schema Schema, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		mustExecute(t, against(schema, statement))
	}
}

func TestMigrationStatementsBuildTheSchemaTheFingerprintDescribes(t *testing.T) {
	schema := deployed(t, "eventpg_s2_built")
	store := openStore(t, schema, VerifySchema)
	if err := store.Prepare(t.Context()); err != nil {
		t.Fatalf("the schema the operator's own list built does not verify against the build that printed the list: %v", err)
	}

	for _, table := range []string{metaTable, streamsTable, eventsTable} {
		var kind string
		row := liveDB(t).QueryRow(`SELECT c.relkind::text FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = $2`, schema.Name, table)
		if err := row.Scan(&kind); err != nil {
			t.Errorf("%s.%s is not there after the migration: %v", schema.Name, table, err)
		} else if kind != ordinaryTable {
			t.Errorf("%s.%s is a relation of kind %s and the store reads it as an ordinary table", schema.Name, table, kind)
		}
	}

	var triggers int
	row := liveDB(t).QueryRow(`SELECT count(*) FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND NOT t.tgisinternal`, schema.Name)
	if err := row.Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	if triggers != 3 {
		t.Errorf("the migrated schema carries %d triggers of its own and the three it needs are the two append-only ones and the xid-first one", triggers)
	}

	var rows int
	var version int
	if err := liveDB(t).QueryRow(`SELECT count(*), max(version) FROM `+quoteIdentifier(schema.Name)+`.schema_meta`).Scan(&rows, &version); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || version != SchemaVersion {
		t.Errorf("schema_meta holds %d rows at version %d and a migrated schema holds one at version %d", rows, version, SchemaVersion)
	}
	fingerprint, _ := metaRow(t, schema.Name)
	described, err := schema.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint != described {
		t.Errorf("the deployed schema records %s and this build describes %s", fingerprint, described)
	}

	t.Run("a build at another bound does not verify the same schema", func(t *testing.T) {
		other := openStore(t, Schema{Name: schema.Name, MaxPayload: 1024}, VerifySchema)
		if err := other.Prepare(t.Context()); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("a store configured for a payload bound this schema does not enforce verified it anyway: %v", err)
		}
	})
}

func TestRunningTheMigrationTwiceReissuesNoLog(t *testing.T) {
	schema := deployed(t, "eventpg_s2_twice")
	fingerprint, log := metaRow(t, schema.Name)
	if len(log) != logBytes {
		t.Fatalf("the deployed schema names a log of %d bytes and a log is %d", len(log), logBytes)
	}

	if err := migrate(t, schema); err != nil {
		t.Fatalf("the same list refused its own schema on a second run, and a migration step nobody can retry is worse than one that rewrites: %v", err)
	}
	again, sameLog := metaRow(t, schema.Name)
	if !bytes.Equal(sameLog, log) {
		t.Errorf("the second run minted a second log, so every cursor persisted against the first one is now unreadable")
	}
	if again != fingerprint {
		t.Errorf("the second run moved the fingerprint from %s to %s", fingerprint, again)
	}
	var rows int
	if err := liveDB(t).QueryRow(`SELECT count(*) FROM ` + quoteIdentifier(schema.Name) + `.schema_meta`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("schema_meta holds %d rows after two migrations", rows)
	}
	if err := openStore(t, schema, VerifySchema).Prepare(t.Context()); err != nil {
		t.Errorf("the twice-migrated schema no longer verifies: %v", err)
	}

	t.Run("a log is minted per schema, so two identical ones would be the store's constant", func(t *testing.T) {
		beside := deployed(t, "eventpg_s2_twice_beside")
		_, other := metaRow(t, beside.Name)
		if bytes.Equal(other, log) {
			t.Fatal("two schemas migrated by the same build carry one log, so the log above was equal to itself for a reason that has nothing to do with re-running")
		}
	})
}

func TestTheZeroSchemaManagementVerifiesAndMigratesNothing(t *testing.T) {
	schema := sharedSchema(t)
	store := openStore(t, schema, UnsetSchemaManagement)
	if store.SchemaManagement() != VerifySchema {
		t.Fatalf("a store built with nothing said about schema management is %s", store.SchemaManagement())
	}
	fingerprint, log := metaRow(t, schema.Name)
	if err := store.Prepare(t.Context()); err != nil {
		t.Fatalf("a store that says nothing about schema management does not verify a schema at the version and fingerprint it expects: %v", err)
	}
	if err := store.Check(t.Context()); err != nil {
		t.Fatalf("the store verified and is not ready: %v", err)
	}
	after, sameLog := metaRow(t, schema.Name)
	if after != fingerprint || !bytes.Equal(sameLog, log) {
		t.Errorf("verifying rewrote schema_meta, and nothing verifies by making the answer agree with the question")
	}

	t.Run("the migration door itself refuses", func(t *testing.T) {
		err := store.Migrate(t.Context())
		if !errors.Is(err, ErrSpec) {
			t.Fatalf("a store that was never asked to manage a schema migrated one anyway: %v", err)
		}
		for _, named := range []string{VerifySchema.String(), ManageSchema.String()} {
			if !strings.Contains(err.Error(), named) {
				t.Errorf("the refusal is %q and does not name %s, so an operator is told no and not what to set", err, named)
			}
		}
	})
}

func TestPrepareAgainstAMissingSchemaRefusesBeforeAnyAppend(t *testing.T) {
	name := scratch(t, "eventpg_s2_missing")
	schema := Schema{Name: name}
	store := openStore(t, schema, VerifySchema)

	err := store.Prepare(t.Context())
	if !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("a store over a schema that does not exist prepared with %v", err)
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("the refusal is %q and does not name the schema it looked for", err)
	}
	if schemaExists(t, name) {
		t.Error("verifying created the schema it was verifying, and nothing migrates unless a deployment asked for it by name")
	}
	if ready := store.Check(t.Context()); !errors.Is(ready, ErrNotReady) {
		t.Errorf("a store whose Prepare refused answers %v to a readiness probe", ready)
	}

	t.Run("the same store under ManageSchema builds it and serves", func(t *testing.T) {
		managing := openStore(t, schema, ManageSchema)
		if err := managing.Prepare(t.Context()); err != nil {
			t.Fatalf("a store asked to manage the schema did not build it: %v", err)
		}
		if !schemaExists(t, name) {
			t.Fatal("the store reported a migrated schema and there is none")
		}
		if err := managing.Check(t.Context()); err != nil {
			t.Errorf("the store migrated and verified and is not ready: %v", err)
		}
	})
}

func TestVerificationRefusesEveryMutationAndPassesTheIntactSchema(t *testing.T) {
	for _, mutation := range []struct {
		what    string
		scratch string
		applied []string
		names   []string
	}{
		{
			what: "the schema is gone", scratch: "eventpg_s2_gone",
			applied: []string{`DROP SCHEMA @ CASCADE`},
			names:   []string{metaTable},
		},
		{
			what: "the version is one below", scratch: "eventpg_s2_below",
			applied: []string{
				`ALTER TABLE @.schema_meta DROP CONSTRAINT schema_meta_version_check`,
				`UPDATE @.schema_meta SET version = 0`,
			},
			names: []string{"version 0", "version " + strconv.Itoa(SchemaVersion)},
		},
		{
			what: "the version is one above", scratch: "eventpg_s2_above",
			applied: []string{`UPDATE @.schema_meta SET version = 2`},
			names:   []string{"version 2", "version " + strconv.Itoa(SchemaVersion)},
		},
		{
			what: "the fingerprint is another build's", scratch: "eventpg_s2_print",
			applied: []string{`UPDATE @.schema_meta SET fingerprint = 'sha256:' || repeat('0', 64)`},
			names:   []string{"sha256:0000000000"},
		},
		{
			what: "the unique constraint over a stream's versions is dropped", scratch: "eventpg_s2_unique",
			applied: []string{`ALTER TABLE @.events DROP CONSTRAINT events_stream_version_key`},
			names:   []string{"events_stream_version_key", "missing"},
		},
		{
			what: "the append-only trigger is dropped", scratch: "eventpg_s2_appendonly",
			applied: []string{`DROP TRIGGER events_append_only_row ON @.events`},
			names:   []string{"events_append_only_row", "missing"},
		},
		{
			what: "the truncate trigger is dropped", scratch: "eventpg_s2_truncate",
			applied: []string{`DROP TRIGGER events_append_only_truncate ON @.events`},
			names:   []string{"events_append_only_truncate", "missing"},
		},
		{
			what: "the xid-first trigger is dropped", scratch: "eventpg_s2_xid",
			applied: []string{`DROP TRIGGER events_position_needs_xid ON @.events`},
			names:   []string{"events_position_needs_xid", "missing"},
		},
		{
			what: "a check constraint is altered", scratch: "eventpg_s2_check",
			applied: []string{
				`ALTER TABLE @.events DROP CONSTRAINT events_payload_check`,
				`ALTER TABLE @.events ADD CONSTRAINT events_payload_check CHECK (octet_length(payload) <= 999)`,
			},
			names: []string{"events_payload_check", "999"},
		},
		{
			what: "a check constraint was never validated", scratch: "eventpg_s2_notvalid",
			applied: []string{
				`ALTER TABLE @.events DROP CONSTRAINT events_payload_check`,
				`ALTER TABLE @.events ADD CONSTRAINT events_payload_check CHECK (octet_length(payload) <= 65536) NOT VALID`,
			},
			names: []string{"events_payload_check", "never validated"},
		},
		{
			what: "a check constraint stops at the table it names", scratch: "eventpg_s2_noinherit",
			applied: []string{
				`ALTER TABLE @.events DROP CONSTRAINT events_payload_check`,
				`ALTER TABLE @.events ADD CONSTRAINT events_payload_check CHECK (octet_length(payload) <= 65536) NO INHERIT`,
			},
			names: []string{"events_payload_check", "NO INHERIT"},
		},
		{
			what: "the append-only trigger is disabled", scratch: "eventpg_s2_disabled",
			applied: []string{`ALTER TABLE @.events DISABLE TRIGGER events_append_only_row`},
			names:   []string{"events_append_only_row", "disabled"},
		},
		{
			what: "the append-only function is replaced with one that returns", scratch: "eventpg_s2_body",
			applied: []string{`CREATE OR REPLACE FUNCTION @.events_are_append_only() RETURNS trigger
				LANGUAGE plpgsql AS $meddle$ BEGIN RETURN NEW; END $meddle$`},
			names: []string{appendOnlyFunction, "RETURN NEW;", "RAISE EXCEPTION"},
		},
		{
			what: "the xid-first function draws no transaction id", scratch: "eventpg_s2_xidbody",
			applied: []string{`CREATE OR REPLACE FUNCTION @.events_assign_writer_xid() RETURNS trigger
				LANGUAGE plpgsql AS $meddle$ BEGIN RETURN NULL; END $meddle$`},
			names: []string{writerXidFunction, "pg_current_xact_id()"},
		},
		{
			what: "the append-only trigger fires only when a condition holds", scratch: "eventpg_s2_when",
			applied: []string{
				`DROP TRIGGER events_append_only_row ON @.events`,
				`CREATE TRIGGER events_append_only_row BEFORE UPDATE OR DELETE ON @.events
					FOR EACH ROW WHEN (false) EXECUTE FUNCTION @.events_are_append_only()`,
			},
			names: []string{"events_append_only_row", "condition"},
		},
		{
			what: "the append-only trigger watches one column", scratch: "eventpg_s2_columns_watched",
			applied: []string{
				`DROP TRIGGER events_append_only_row ON @.events`,
				`CREATE TRIGGER events_append_only_row BEFORE UPDATE OF recorded_at OR DELETE ON @.events
					FOR EACH ROW EXECUTE FUNCTION @.events_are_append_only()`,
			},
			names: []string{"events_append_only_row", "recorded_at"},
		},
		{
			what: "the history is unlogged", scratch: "eventpg_s2_unlogged",
			applied: []string{`ALTER TABLE @.events SET UNLOGGED`},
			names:   []string{"events", "unlogged"},
		},
		{
			what: "the xid-first trigger runs another function", scratch: "eventpg_s2_function",
			applied: []string{
				`CREATE FUNCTION @.meddle() RETURNS trigger LANGUAGE plpgsql AS $meddle$ BEGIN RETURN NULL; END $meddle$`,
				`DROP TRIGGER events_position_needs_xid ON @.events`,
				`CREATE TRIGGER events_position_needs_xid BEFORE INSERT ON @.events FOR EACH STATEMENT EXECUTE FUNCTION @.meddle()`,
			},
			names: []string{"events_position_needs_xid", "meddle", writerXidFunction},
		},
		{
			what: "the xid-first trigger fires after the insert", scratch: "eventpg_s2_after",
			applied: []string{
				`DROP TRIGGER events_position_needs_xid ON @.events`,
				`CREATE TRIGGER events_position_needs_xid AFTER INSERT ON @.events FOR EACH STATEMENT EXECUTE FUNCTION @.events_assign_writer_xid()`,
			},
			names: []string{"events_position_needs_xid", "BEFORE INSERT"},
		},
		{
			what: "the unique constraint covers another column", scratch: "eventpg_s2_columns",
			applied: []string{
				`ALTER TABLE @.events DROP CONSTRAINT events_stream_version_key`,
				`ALTER TABLE @.events ADD CONSTRAINT events_stream_version_key UNIQUE (family, key, position)`,
			},
			names: []string{"events_stream_version_key", "family,key,position", "family,key,version"},
		},
		{
			what: "the position column is no longer an identity", scratch: "eventpg_s2_identity",
			applied: []string{`ALTER TABLE @.events ALTER COLUMN position DROP IDENTITY`},
			names:   []string{"events.position", "generated"},
		},
		{
			what: "a column loses its NOT NULL", scratch: "eventpg_s2_nullable",
			applied: []string{`ALTER TABLE @.events ALTER COLUMN payload DROP NOT NULL`},
			names:   []string{"events.payload", "nullable"},
		},
		{
			what: "a column changes type", scratch: "eventpg_s2_type",
			applied: []string{`ALTER TABLE @.events ALTER COLUMN revision TYPE bigint`},
			names:   []string{"events.revision", "bigint", "integer"},
		},
		{
			what: "a column is added", scratch: "eventpg_s2_added_column",
			applied: []string{`ALTER TABLE @.events ADD COLUMN besides text`},
			names:   []string{"events", "9 columns", "8"},
		},
		{
			what: "the foreign key is dropped", scratch: "eventpg_s2_foreign",
			applied: []string{`ALTER TABLE @.events DROP CONSTRAINT events_stream_fkey`},
			names:   []string{"events_stream_fkey", "missing"},
		},
		{
			what: "the identity sequence caches", scratch: "eventpg_s2_cache",
			applied: []string{`ALTER SEQUENCE @.events_position_seq CACHE 32`},
			names:   []string{"cache=32", "cache=1"},
		},
		{
			what: "row-level security is enabled with a policy", scratch: "eventpg_s2_rls",
			applied: []string{
				`ALTER TABLE @.events ENABLE ROW LEVEL SECURITY`,
				`CREATE POLICY everything ON @.events USING (true)`,
			},
			names: []string{"row-level security"},
		},
		{
			what: "a third trigger fires before an insert on events", scratch: "eventpg_s2_meddle",
			applied: []string{
				`CREATE FUNCTION @.meddle() RETURNS trigger LANGUAGE plpgsql AS $meddle$ BEGIN RETURN NEW; END $meddle$`,
				`CREATE TRIGGER events_meddle BEFORE INSERT ON @.events FOR EACH ROW EXECUTE FUNCTION @.meddle()`,
			},
			names: []string{"events_meddle", "does not expect"},
		},
		{
			what: "a trigger fires before an insert on streams", scratch: "eventpg_s2_streams_trigger",
			applied: []string{
				`CREATE FUNCTION @.meddle() RETURNS trigger LANGUAGE plpgsql AS $meddle$ BEGIN RETURN NEW; END $meddle$`,
				`CREATE TRIGGER streams_meddle BEFORE INSERT ON @.streams FOR EACH ROW EXECUTE FUNCTION @.meddle()`,
			},
			names: []string{"streams_meddle", "does not expect"},
		},
		{
			what: "a rule redirects an insert", scratch: "eventpg_s2_rule",
			applied: []string{`CREATE RULE events_ignore AS ON INSERT TO @.events DO INSTEAD NOTHING`},
			names:   []string{"events_ignore", "rewrite rule"},
		},
		{
			what: "the payload column is given a default", scratch: "eventpg_s2_default",
			applied: []string{`ALTER TABLE @.events ALTER COLUMN payload SET DEFAULT '\x00'::bytea`},
			names:   []string{"events.payload", "defaults to"},
		},
		{
			what: "a child table inherits the events table", scratch: "eventpg_s2_child",
			applied: []string{`CREATE TABLE @.events_child () INHERITS (@.events)`},
			names:   []string{"events", "inherited from"},
		},
	} {
		t.Run(mutation.what, func(t *testing.T) {
			schema := deployed(t, mutation.scratch)
			mutate(t, schema, mutation.applied...)

			store := openStore(t, schema, VerifySchema)
			err := store.Prepare(t.Context())
			if !errors.Is(err, ErrSchemaMismatch) {
				t.Fatalf("a schema where %s verified with %v", mutation.what, err)
			}
			if !strings.Contains(err.Error(), schema.Name) {
				t.Errorf("the refusal is %q and does not name the schema it read", err)
			}
			for _, named := range mutation.names {
				if !strings.Contains(err.Error(), named) {
					t.Errorf("the refusal is %q and does not name %q, so an operator is told the schema is wrong and not which object is", err, named)
				}
			}
			if ready := store.Check(t.Context()); !errors.Is(ready, ErrNotReady) {
				t.Errorf("the store refused its schema and answers %v to a readiness probe", ready)
			}
		})
	}

	t.Run("the intact schema passes", func(t *testing.T) {
		if err := openStore(t, deployed(t, "eventpg_s2_intact"), VerifySchema).Prepare(t.Context()); err != nil {
			t.Fatalf("a schema this build's own list migrated does not verify, so every refusal above is a verifier that refuses everything: %v", err)
		}
	})

	t.Run("an extra index is exempt, because a wrong one fails at the first write", func(t *testing.T) {
		schema := deployed(t, "eventpg_s2_index")
		mutate(t, schema, `CREATE INDEX events_recorded_idx ON @.events (recorded_at)`)
		if err := openStore(t, schema, VerifySchema).Prepare(t.Context()); err != nil {
			t.Fatalf("an index nobody expected refused a schema, and the line is every class whose extra member is invisible: %v", err)
		}
	})

	t.Run("an unrelated table in the schema is exempt", func(t *testing.T) {
		schema := deployed(t, "eventpg_s2_beside")
		mutate(t, schema, `CREATE TABLE @.beside (id bigint PRIMARY KEY)`)
		if err := openStore(t, schema, VerifySchema).Prepare(t.Context()); err != nil {
			t.Fatalf("a table this store never reads refused the schema it sits beside: %v", err)
		}
	})

	t.Run("a build at another payload bound is refused by the fingerprint and names both", func(t *testing.T) {
		schema := Schema{Name: scratch(t, "eventpg_s2_bound"), MaxPayload: 1024}
		if err := migrate(t, schema); err != nil {
			t.Fatalf("the narrow schema did not deploy: %v", err)
		}
		wide := Schema{Name: schema.Name}
		err := openStore(t, wide, VerifySchema).Prepare(t.Context())
		if !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("a build configured for %d bytes verified a schema deployed for %d: %v", DefaultMaxPayload, 1024, err)
		}
		deployedPrint, _ := schema.Fingerprint()
		widePrint, _ := wide.Fingerprint()
		for _, named := range []string{deployedPrint, widePrint} {
			if !strings.Contains(err.Error(), named) {
				t.Errorf("the refusal is %q and does not name %s, so an operator is told two schemas differ and not which two", err, named)
			}
		}
	})
}

func TestTheDatabaseRefusesUpdateDeleteAndTruncateAndAdmitsInsert(t *testing.T) {
	schema := deployed(t, "eventpg_s2_appendonly_live")
	mutate(t, schema,
		`INSERT INTO @.streams (family, key, version) VALUES ('accounts', 'a', 1)`,
		`INSERT INTO @.events (family, key, version, type, revision, payload, recorded_at)
		 VALUES ('accounts', 'a', 1, 'accounts.opened', 1, '\x01', statement_timestamp())`)

	for _, refused := range []struct {
		verb      string
		statement string
	}{
		{"UPDATE", `UPDATE @.events SET payload = '\x02'`},
		{"DELETE", `DELETE FROM @.events`},
		{"TRUNCATE", `TRUNCATE @.events`},
	} {
		t.Run(refused.verb, func(t *testing.T) {
			err := execute(t, against(schema, refused.statement))
			if err == nil {
				t.Fatalf("%s on the events table was carried out, and append-only that rests on this package containing no such statement says nothing about the next package", refused.verb)
			}
			for _, named := range []string{refused.verb, "append-only"} {
				if !strings.Contains(err.Error(), named) {
					t.Errorf("%s was refused with %q, which does not name %q", refused.verb, err, named)
				}
			}
		})
	}

	// The control the body comparison rests on: with the function replaced by one
	// that returns, the database carries the UPDATE out. If it ever stops doing
	// so, the verification case that refuses this schema proves nothing.
	t.Run("a function that returns admits the UPDATE the deployed one refuses", func(t *testing.T) {
		neutered := deployed(t, "eventpg_s2_appendonly_neutered")
		mutate(t, neutered,
			`INSERT INTO @.streams (family, key, version) VALUES ('accounts', 'a', 1)`,
			`INSERT INTO @.events (family, key, version, type, revision, payload, recorded_at)
			 VALUES ('accounts', 'a', 1, 'accounts.opened', 1, '\x01', statement_timestamp())`,
			`CREATE OR REPLACE FUNCTION @.events_are_append_only() RETURNS trigger
			 LANGUAGE plpgsql AS $meddle$ BEGIN RETURN NEW; END $meddle$`,
			`UPDATE @.events SET payload = '\x02'`)
		var payload []byte
		if err := liveDB(t).QueryRow(`SELECT payload FROM ` + quoteIdentifier(neutered.Name) + `.events`).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(payload, []byte{0x02}) {
			t.Fatalf("the history holds %x after an UPDATE the neutered function let through, so nothing was rewritten and the case that refuses this schema guards no hole", payload)
		}
		if err := openStore(t, neutered, VerifySchema).Prepare(t.Context()); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("a schema whose history was just rewritten under the store verified with %v", err)
		}
	})

	t.Run("an insert beside them still lands", func(t *testing.T) {
		mutate(t, schema,
			`INSERT INTO @.events (family, key, version, type, revision, payload, recorded_at)
			 VALUES ('accounts', 'a', 2, 'accounts.credited', 1, '\x03', statement_timestamp())`)
		var rows int
		if err := liveDB(t).QueryRow(`SELECT count(*) FROM ` + quoteIdentifier(schema.Name) + `.events`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 2 {
			t.Fatalf("the events table holds %d rows, so a trigger that refused every verb would have passed the three cases above", rows)
		}
	})
}

func TestCheckIsAReadinessAnswerAndNamesNoImportance(t *testing.T) {
	// health.Probe, structurally. eventpg names no importance, no code and no
	// check name, and imports nothing of health to name them with.
	var probe func(context.Context) error = prepared(t, sharedSchema(t)).Check
	if err := probe(t.Context()); err != nil {
		t.Fatalf("a store over the schema it verified is not ready: %v", err)
	}

	t.Run("a store that never verified is not ready", func(t *testing.T) {
		if err := openStore(t, sharedSchema(t), VerifySchema).Check(t.Context()); !errors.Is(err, ErrNotReady) {
			t.Fatalf("a store whose Prepare was never called answers %v", err)
		}
	})

	t.Run("a closed store answers closed", func(t *testing.T) {
		store := prepared(t, sharedSchema(t))
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if err := store.Check(t.Context()); !errors.Is(err, event.ErrClosed) {
			t.Fatalf("a closed store answers %v to a readiness probe", err)
		}
	})

	t.Run("a schema that drifted under the store fails", func(t *testing.T) {
		schema := deployed(t, "eventpg_s2_drift")
		store := prepared(t, schema)
		mutate(t, schema, `UPDATE @.schema_meta SET fingerprint = 'sha256:' || repeat('f', 64)`)
		if err := store.Check(t.Context()); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("the deployed fingerprint moved under a ready store and the probe answers %v", err)
		}
	})

	t.Run("a schema migrated again under the store fails", func(t *testing.T) {
		schema := deployed(t, "eventpg_s2_reissued")
		store := prepared(t, schema)
		mustExecute(t, against(schema, `DROP SCHEMA @ CASCADE`))
		if err := migrate(t, schema); err != nil {
			t.Fatal(err)
		}
		if err := store.Check(t.Context()); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("the schema was dropped and built again under a ready store, so every cursor it minted is foreign, and the probe answers %v", err)
		}
	})

	t.Run("a store that was serving stops when a second Prepare refuses", func(t *testing.T) {
		schema := deployed(t, "eventpg_s2_reprepare")
		store := openStore(t, schema, ManageSchema)
		if err := store.Prepare(t.Context()); err != nil {
			t.Fatalf("a store asked to manage a schema its own build deployed refused it: %v", err)
		}
		mutate(t, schema, `UPDATE @.schema_meta SET fingerprint = 'sha256:' || repeat('a', 64)`)
		if err := store.Prepare(t.Context()); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("the migration met a schema_meta its own list cannot have written and answered %v", err)
		}
		if err := store.Check(t.Context()); !errors.Is(err, ErrNotReady) {
			t.Fatalf("a store whose second Prepare refused keeps answering %v, so it goes on serving a schema it just refused", err)
		}
	})

	t.Run("a pool that is gone fails", func(t *testing.T) {
		pool, err := sql.Open("pgx", os.Getenv(testDSN))
		if err != nil {
			t.Fatal(err)
		}
		store, err := New(Spec{DB: pool, Source: crudsql.Postgres(pool), Schema: sharedSchema(t)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Prepare(t.Context()); err != nil {
			t.Fatalf("a store over a pool of its own did not verify: %v", err)
		}
		if err := pool.Close(); err != nil {
			t.Fatal(err)
		}
		if err := store.Check(t.Context()); err == nil {
			t.Fatal("a store whose pool is closed answers a readiness probe with nil, so nothing this probe returns is an answer about a database")
		}
	})
}

func TestLimitsAreTheDeployedConstraintOperands(t *testing.T) {
	schema := Schema{Name: scratch(t, "eventpg_s2_bounds"), MaxPayload: 4096, MaxKey: 40}
	if err := migrate(t, schema); err != nil {
		t.Fatalf("the schema at the narrow bounds did not deploy: %v", err)
	}
	limits := prepared(t, schema).Limits()
	if limits.MaxPayload != schema.MaxPayload || limits.MaxKey != schema.MaxKey {
		t.Fatalf("the store publishes a payload bound of %d and a key bound of %d over a schema deployed at %d and %d",
			limits.MaxPayload, limits.MaxKey, schema.MaxPayload, schema.MaxKey)
	}

	for _, operand := range []struct {
		table      string
		constraint string
		published  int
	}{
		{eventsTable, "events_payload_check", limits.MaxPayload},
		{eventsTable, "events_key_check", limits.MaxKey},
		{streamsTable, "streams_key_check", limits.MaxKey},
	} {
		definition := constraintDefinition(t, schema.Name, operand.table, operand.constraint)
		if found := lastNumber(t, definition); found != operand.published {
			t.Errorf("%s is %q and the store publishes %d, so Limits() names a bound the deployed schema does not enforce",
				operand.constraint, definition, operand.published)
		}
	}

	t.Run("the database holds the row to the operand", func(t *testing.T) {
		mustExecute(t, against(schema, `INSERT INTO @.streams (family, key, version) VALUES ($1, $2, 1)`),
			"accounts", strings.Repeat("k", limits.MaxKey))
		if err := execute(t, against(schema, `INSERT INTO @.streams (family, key, version) VALUES ($1, $2, 1)`),
			"accounts", strings.Repeat("k", limits.MaxKey+1)); err == nil {
			t.Error("a key one byte over the deployed bound was accepted, so the constraint operand is not the bound")
		}
		mustExecute(t, against(schema, `INSERT INTO @.events (family, key, version, type, revision, payload, recorded_at)
			VALUES ($1, $2, 1, 'accounts.opened', 1, $3, statement_timestamp())`),
			"accounts", strings.Repeat("k", limits.MaxKey), make([]byte, limits.MaxPayload))
		if err := execute(t, against(schema, `INSERT INTO @.events (family, key, version, type, revision, payload, recorded_at)
			VALUES ($1, $2, 2, 'accounts.opened', 1, $3, statement_timestamp())`),
			"accounts", strings.Repeat("k", limits.MaxKey), make([]byte, limits.MaxPayload+1)); err == nil {
			t.Error("a payload one byte over the deployed bound was accepted")
		}
	})

	// The other half of INV-058: the kernel enforces the number this store
	// publishes, and it enforces it at its own door. The store here is
	// eventmemory rather than a fabricated one, published limits copied from the
	// PostgreSQL store's and compared before anything is appended, so what the
	// kernel refuses is refused at eventpg's deployed bound.
	t.Run("the kernel refuses one byte more before a store is asked", func(t *testing.T) {
		log, err := eventmemory.NewLog(eventmemory.LogSpec{MaxPayload: limits.MaxPayload, MaxKey: limits.MaxKey})
		if err != nil {
			t.Fatal(err)
		}
		beside, err := eventmemory.New(eventmemory.Spec{
			Log: log, MaxBatch: limits.MaxBatch, StreamPage: limits.StreamPage, MaxRead: limits.MaxRead,
		})
		if err != nil {
			t.Fatal(err)
		}
		if beside.Limits() != limits {
			t.Fatalf("the store the kernel is asked about publishes %+v and eventpg publishes %+v", beside.Limits(), limits)
		}
		aggregate, fact := declareHolding(t, "eventpg.bounded")
		repo, err := event.Bind(event.Open(beside), aggregate)
		if err != nil {
			t.Fatal(err)
		}
		_, at, err := repo.Load(t.Context(), "a")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := repo.Append(t.Context(), at, fact.New("a", held{Bytes: make([]byte, limits.MaxPayload+1)})); err == nil {
			t.Fatal("the kernel admitted a payload one byte over the bound the deployed schema enforces, so the database would have refused it with a 23514 at the worst moment")
		}
		page, err := beside.ReadStream(t.Context(), event.Stream{Family: "eventpg.bounded", Key: "a"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 0 {
			t.Fatal("the refused append reached the store, so the bound was enforced after the write rather than before it")
		}
		if _, _, err := repo.Append(t.Context(), at, fact.New("a", held{Bytes: make([]byte, limits.MaxPayload)})); err != nil {
			t.Fatalf("a payload of exactly the bound was refused, so the door refuses everything: %v", err)
		}
	})
}

func lastNumber(t *testing.T, definition string) int {
	t.Helper()
	end := strings.LastIndexFunc(definition, func(held rune) bool { return held >= '0' && held <= '9' })
	if end < 0 {
		t.Fatalf("%q carries no number, so this test read the wrong constraint", definition)
	}
	start := end
	for start > 0 && definition[start-1] >= '0' && definition[start-1] <= '9' {
		start--
	}
	found, err := strconv.Atoi(definition[start : end+1])
	if err != nil {
		t.Fatal(err)
	}
	return found
}
