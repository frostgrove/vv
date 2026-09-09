package eventpg

import (
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event"
)

const goldenFingerprint = "sha256:12f0154c05860113b0a28fafbee99ef3964aa4ac396617cd5a87dca068769359"

// The version-1 fingerprints, at both bound configurations, as literals. They
// are what the stamping UPDATE is guarded on, so a build that computed a
// different one would not restamp a version-1 schema — it would decline to
// migrate it, and every deployment on version 1 would be stuck at a red
// migration nobody could explain. They must never move.
const (
	versionOneFingerprint       = "sha256:fadfff742d234efabc84f46bccb1caa41aea00dc78237d1a1e7dbeabe097aaeb"
	versionOneNarrowFingerprint = "sha256:ac3891b8e1461ee46bc6d98db295424c61ce290acc4d1a6190433e41d6ee404e"
)

func TestTheVersionOneFingerprintIsTheOneEveryDeployedSchemaCarries(t *testing.T) {
	for _, pinned := range []struct {
		schema Schema
		at     string
	}{
		{Schema{}, versionOneFingerprint},
		{Schema{Name: "narrow_events", MaxPayload: 1024, MaxKey: 40}, versionOneNarrowFingerprint},
	} {
		print, err := pinned.schema.fingerprintAt(1)
		if err != nil {
			t.Fatalf("%+v has no version-1 fingerprint: %v", pinned.schema, err)
		}
		if print != pinned.at {
			t.Errorf("%+v fingerprints as %s at version 1 and every schema an earlier build deployed carries %s, so this build declines to migrate all of them", pinned.schema, print, pinned.at)
		}
	}

	t.Run("a version this build carries no model of has no fingerprint", func(t *testing.T) {
		for _, version := range []int{0, -1, SchemaVersion + 1} {
			if _, err := (Schema{}).fingerprintAt(version); !errors.Is(err, ErrSpec) {
				t.Errorf("version %d answered a fingerprint rather than %v: %v", version, ErrSpec, err)
			}
		}
	})
}

// Every one of them matches [a-z][a-z0-9_]* and every one of them is a syntax
// error unquoted.
var reservedWords = []string{"user", "table", "select", "order", "group", "all", "default", "do"}

// A whole identifier token, so gen_random_uuid does not read as a mention of a
// schema named "do".
func mentionedUnquoted(text, word string) bool {
	for index := 0; index+len(word) <= len(text); index++ {
		if text[index:index+len(word)] != word {
			continue
		}
		before, after := neighbour(text, index-1), neighbour(text, index+len(word))
		if identifierPart(before) || identifierPart(after) {
			continue
		}
		if before != '"' || after != '"' {
			return true
		}
	}
	return false
}

func neighbour(text string, index int) byte {
	if index < 0 || index >= len(text) {
		return 0
	}
	return text[index]
}

func identifierPart(value byte) bool {
	return value == '_' || (value >= '0' && value <= '9') ||
		(value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}

func listing(statements []string) string {
	var out strings.Builder
	for index, statement := range statements {
		out.WriteString("-- statement " + strconv.Itoa(index+1) + "\n")
		out.WriteString(statement)
		out.WriteString(";\n")
	}
	return out.String()
}

func TestTheFingerprintIsTheRenderingItDigests(t *testing.T) {
	resolved, err := Schema{}.Resolved()
	if err != nil {
		t.Fatalf("the zero schema does not resolve, so nothing below was compared against anything: %v", err)
	}
	rendered := expected(resolved, SchemaVersion).rendering()

	t.Run("the rendering is the golden file byte for byte", func(t *testing.T) {
		golden, err := os.ReadFile("testdata/fingerprint.golden")
		if err != nil {
			t.Fatalf("the golden rendering could not be read, so the fingerprint is pinned by nothing: %v", err)
		}
		if string(golden) != rendered {
			t.Fatalf("the rendering the default schema digests is no longer the one in testdata/fingerprint.golden.\nread it as a diff and decide whether every deployed schema was meant to be invalidated.\n--- golden ---\n%s\n--- built ---\n%s", golden, rendered)
		}
	})

	t.Run("the digest is the one this build has always answered", func(t *testing.T) {
		print, err := Schema{}.Fingerprint()
		if err != nil {
			t.Fatalf("the zero schema has no fingerprint: %v", err)
		}
		if print != goldenFingerprint {
			t.Fatalf("the default schema fingerprints as %s and every schema deployed by an earlier build carries %s, so every one of them now fails verification", print, goldenFingerprint)
		}
		again, err := Schema{Name: DefaultSchema, MaxPayload: DefaultMaxPayload, MaxKey: DefaultMaxKey}.Fingerprint()
		if err != nil {
			t.Fatalf("the schema written out in full has no fingerprint: %v", err)
		}
		if again != print {
			t.Fatal("the zero schema and the same schema written out in full fingerprint differently, so the defaults are not what the zero value resolves to")
		}
	})

	t.Run("a bound is an input and one byte of it changes the answer", func(t *testing.T) {
		for _, changed := range []Schema{
			{MaxPayload: DefaultMaxPayload + 1},
			{MaxPayload: DefaultMaxPayload - 1},
			{MaxKey: DefaultMaxKey + 1},
			{MaxKey: DefaultMaxKey - 1},
			{Name: "other_events"},
		} {
			print, err := changed.Fingerprint()
			if err != nil {
				t.Fatalf("%+v has no fingerprint: %v", changed, err)
			}
			if print == goldenFingerprint {
				t.Fatalf("%+v fingerprints as the default schema does, so a deployment could migrate one and verify against the other", changed)
			}
		}
	})

	t.Run("a function body is an input and one statement of it changes the answer", func(t *testing.T) {
		if !strings.Contains(rendered, "history is append-only") || !strings.Contains(rendered, "pg_current_xact_id()") {
			t.Fatal("the rendering carries neither trigger function's body, so nothing below compares anything")
		}
		for _, changed := range []struct {
			what string
			body []string
		}{
			{"the append-only function returns instead of raising", []string{"RETURN NEW;"}},
			{"the append-only function raises another message", []string{"RAISE EXCEPTION 'eventpg: no';"}},
		} {
			model := expected(resolved, SchemaVersion)
			model.functions[0].body = changed.body
			if fingerprintOf(model.rendering()) == goldenFingerprint {
				t.Errorf("a build where %s fingerprints as this one does, so CREATE OR REPLACE at schema version 1 changes what the schema enforces with the digest unmoved", changed.what)
			}
		}
	})

	t.Run("the canonical form is three header lines and a sorted body", func(t *testing.T) {
		if !strings.HasSuffix(rendered, "\n") {
			t.Fatal("the rendering does not end in a newline, so appending a line to it would join it to the last one")
		}
		lines := strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
		if len(lines) < 4 {
			t.Fatalf("the rendering is %d lines and the schema alone has three tables, so this digests almost nothing", len(lines))
		}
		header := []string{
			"eventpg/schema/v2",
			"schema " + DefaultSchema,
			"bounds maxpayload=65536 maxkey=512",
		}
		if !slices.Equal(lines[:3], header) {
			t.Fatalf("the header is %v and the canonical form is %v", lines[:3], header)
		}
		if !slices.IsSorted(lines[3:]) {
			t.Fatal("the body is not sorted, so reordering the expectation model would produce a second fingerprint for one schema")
		}
		for _, line := range lines {
			for index := 0; index < len(line); index++ {
				if line[index] < 0x20 || line[index] > 0x7e {
					t.Fatalf("%q carries a byte outside printable ASCII, and a rendering an operator compares by eye is text", line)
				}
			}
		}
	})

	t.Run("a schema that cannot be resolved has no fingerprint", func(t *testing.T) {
		if _, err := (Schema{Name: "Events"}).Fingerprint(); !errors.Is(err, ErrSpec) {
			t.Fatalf("a schema name PostgreSQL would fold answered a fingerprint rather than %v: %v", ErrSpec, err)
		}
	})
}

func TestMigrationStatementsAreOrderedTransactionalDDL(t *testing.T) {
	statements, err := MigrationStatements(Schema{})
	if err != nil {
		t.Fatalf("the default schema has no migration: %v", err)
	}

	t.Run("the whole list is the golden file byte for byte", func(t *testing.T) {
		for _, pinned := range []struct {
			file   string
			schema Schema
		}{
			{"testdata/migration.golden", Schema{}},
			{"testdata/migration_narrow.golden", Schema{Name: "narrow_events", MaxPayload: 1024, MaxKey: 40}},
		} {
			golden, err := os.ReadFile(pinned.file)
			if err != nil {
				t.Fatalf("%s could not be read, so the statements are pinned by nothing: %v", pinned.file, err)
			}
			built, err := MigrationStatements(pinned.schema)
			if err != nil {
				t.Fatalf("%+v has no migration: %v", pinned.schema, err)
			}
			if rendered := listing(built); string(golden) != rendered {
				t.Fatalf("the statements %+v renders are no longer the ones in %s.\nread it as a diff: a column, a constraint, a trigger clause or a function body that moved here is a schema every deployment builds differently.\n--- golden ---\n%s\n--- built ---\n%s", pinned.schema, pinned.file, golden, rendered)
			}
		}
	})

	t.Run("thirteen statements in the order a deployment runs them", func(t *testing.T) {
		if len(statements) != 13 {
			t.Fatalf("the migration is %d statements and schema version 2 is thirteen — a statement was added or dropped and MIGRATIONS.md still says thirteen", len(statements))
		}
		for index, opening := range []string{
			`CREATE SCHEMA IF NOT EXISTS "frostgrove_events"`,
			`CREATE TABLE IF NOT EXISTS "frostgrove_events".schema_meta`,
			`CREATE TABLE IF NOT EXISTS "frostgrove_events".streams`,
			`CREATE TABLE IF NOT EXISTS "frostgrove_events".events`,
			`CREATE TABLE IF NOT EXISTS "frostgrove_events".checkpoints`,
			`CREATE OR REPLACE FUNCTION "frostgrove_events".events_are_append_only`,
			`CREATE OR REPLACE FUNCTION "frostgrove_events".events_assign_writer_xid`,
			"DO ", "DO ", "DO ",
			`INSERT INTO "frostgrove_events".schema_meta`,
			`UPDATE "frostgrove_events".schema_meta`,
			"DO ",
		} {
			if !strings.HasPrefix(statements[index], opening) {
				t.Errorf("statement %d begins %.60q and the order a table's own constraints and its triggers need is %q first", index+1, statements[index], opening)
			}
		}
	})

	t.Run("every statement is DDL one transaction can hold", func(t *testing.T) {
		for index, statement := range statements {
			if strings.Contains(statement, "CONCURRENTLY") {
				t.Errorf("statement %d builds an object concurrently, and MIGRATIONS.md promises the whole list is safe inside one transaction", index+1)
			}
		}
	})

	t.Run("the log is minted in the database and a re-run reissues none", func(t *testing.T) {
		insert := statements[10]
		for _, needed := range []string{
			"decode(replace(gen_random_uuid()::text, '-', ''), 'hex')",
			"ON CONFLICT (singleton) DO NOTHING",
		} {
			if !strings.Contains(insert, needed) {
				t.Errorf("the schema_meta insert does not carry %q, so a second migration mints a second log and orphans every persisted cursor", needed)
			}
		}
	})

	t.Run("the fingerprint the last three statements carry is this build's own", func(t *testing.T) {
		print, err := Schema{}.Fingerprint()
		if err != nil {
			t.Fatal(err)
		}
		for _, index := range []int{10, 11, 12} {
			if !strings.Contains(statements[index], "'"+print+"'") {
				t.Errorf("statement %d does not carry %s, so a schema this migration built would fail its own verification", index+1, print)
			}
		}
	})

	// The whole of D1: guarded on the version alone, this build's list meeting a
	// version-1 schema deployed at other bounds would create the fourth table,
	// stamp its own fingerprint over a schema it did not build, and then pass its
	// own assertion.
	t.Run("the stamping update is guarded on the version and the fingerprint it migrates from", func(t *testing.T) {
		update := statements[11]
		previous, err := (Schema{}).fingerprintAt(SchemaVersion - 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, needed := range []string{"version = 1", "fingerprint = '" + previous + "'", "SET version = 2"} {
			if !strings.Contains(update, needed) {
				t.Errorf("the stamping update does not carry %q, so a version-1 schema deployed at other bounds would be restamped and would then pass this list's own assertion", needed)
			}
		}
		if previous == goldenFingerprint {
			t.Error("the version-1 model and the version-2 model fingerprint alike, so the guard admits both and the version it migrates from is not a fact")
		}
	})

	t.Run("the last statement records nothing and refuses a schema this list cannot have built", func(t *testing.T) {
		last := statements[12]
		for _, write := range []string{"UPDATE ", "INSERT ", "DELETE ", "MERGE "} {
			if strings.Contains(last, write) {
				t.Errorf("the last statement carries %q. It runs after the stamping update and records nothing of its own, and a statement that writes schema_meta anyway records a fact that is false", write)
			}
		}
		for _, needed := range []string{"RAISE EXCEPTION", "'" + schemaMismatchState + "'", "IS DISTINCT FROM"} {
			if !strings.Contains(last, needed) {
				t.Errorf("the last statement does not carry %q, so a re-run over a drifted schema ends with exit 0 and the drift is the operator's to find", needed)
			}
		}
	})

	t.Run("the bounds a build configured are the constraint operands", func(t *testing.T) {
		narrow, err := MigrationStatements(Schema{Name: "narrow_events", MaxPayload: 1024, MaxKey: 40})
		if err != nil {
			t.Fatal(err)
		}
		events := narrow[3]
		for _, needed := range []string{
			"CONSTRAINT events_payload_check CHECK (octet_length(payload) <= 1024)",
			"CONSTRAINT events_key_check CHECK (octet_length(key) >= 1 AND octet_length(key) <= 40)",
		} {
			if !strings.Contains(events, needed) {
				t.Errorf("the events table does not carry %q, so Limits() would publish a bound the schema does not enforce", needed)
			}
		}
	})

	t.Run("a reserved word is a name this store deploys into, because every statement quotes it", func(t *testing.T) {
		for _, word := range reservedWords {
			quoted, err := MigrationStatements(Schema{Name: word})
			if err != nil {
				t.Errorf("a schema named %q was refused, and the rule the refusal states is about the characters in a name and not about PostgreSQL's grammar: %v", word, err)
				continue
			}
			if mentionedUnquoted(strings.Join(quoted, "\n"), word) {
				t.Errorf("a schema named %q reaches PostgreSQL unquoted somewhere in the list, and PostgreSQL answers a syntax error for every reserved word", word)
			}
		}
	})

	t.Run("a schema this store cannot deploy into has no migration", func(t *testing.T) {
		for _, refused := range []Schema{
			{Name: "Events"},
			{Name: "events; DROP TABLE x"},
			{Name: `events"`},
			{Name: "1events"},
			{Name: strings.Repeat("e", 64)},
			{MaxPayload: event.MaxPayloadBytes + 1},
		} {
			statements, err := MigrationStatements(refused)
			if !errors.Is(err, ErrSpec) {
				t.Errorf("%+v answered %v rather than %v", refused, err, ErrSpec)
			}
			if statements != nil {
				t.Errorf("%+v was refused and answered %d statements anyway", refused, len(statements))
			}
		}
	})

	t.Run("the migration lock is one number per schema", func(t *testing.T) {
		if migrationLock(DefaultSchema) != migrationLock(DefaultSchema) {
			t.Fatal("one schema name locks on two numbers, so two replicas of it would migrate at once")
		}
		if migrationLock(DefaultSchema) == migrationLock("other_events") {
			t.Fatal("two schemas lock on one number, so migrating one holds the other up")
		}
	})
}
