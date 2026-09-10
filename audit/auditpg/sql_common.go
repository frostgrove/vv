package auditpg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/frostgrove/vv/audit"
)

type queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readReady(ctx context.Context, on queryer, schema Schema) (readiness, error) {
	if err := ctx.Err(); err != nil {
		return readiness{}, err
	}
	fingerprint, err := schema.Fingerprint()
	if err != nil {
		return readiness{}, err
	}
	q := quoteIdentifier(schema.Name)
	var version int
	var deployed string
	var backingBytes, logBytes []byte
	var activeID sql.NullString
	var activeGeneration sql.NullInt64
	var activeDigest, setDigest []byte
	err = on.QueryRowContext(ctx, `SELECT version, fingerprint, backing_id, log_id,
	active_catalog_id, active_generation, active_digest, catalog_set_digest
FROM `+q+`.settings WHERE singleton`).Scan(
		&version, &deployed, &backingBytes, &logBytes,
		&activeID, &activeGeneration, &activeDigest, &setDigest,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return readiness{}, fmt.Errorf("%w: settings row is absent", ErrSchemaMismatch)
	}
	if err != nil {
		return readiness{}, fmt.Errorf("%w: reading settings: %v", ErrSchemaMismatch, err)
	}
	if version != SchemaVersion || deployed != fingerprint {
		return readiness{}, fmt.Errorf("%w: got version %d at %q", ErrSchemaMismatch, version, deployed)
	}
	if len(backingBytes) != 16 || len(logBytes) != 16 {
		return readiness{}, fmt.Errorf("%w: persisted identities have invalid lengths", ErrSchemaMismatch)
	}
	var state readiness
	copy(state.backingID[:], backingBytes)
	copy(state.logID[:], logBytes)
	if state.backingID == (audit.BackingID{}) || state.logID == (audit.LogID{}) {
		return readiness{}, fmt.Errorf("%w: persisted identities are zero", ErrSchemaMismatch)
	}
	if activeID.Valid != activeGeneration.Valid || activeID.Valid != (len(activeDigest) > 0) {
		return readiness{}, fmt.Errorf("%w: active catalog tuple is incomplete", ErrSchemaMismatch)
	}
	if len(setDigest) > 0 && len(setDigest) != 32 {
		return readiness{}, fmt.Errorf("%w: catalog-set digest has invalid length", ErrSchemaMismatch)
	}
	if activeID.Valid {
		if activeGeneration.Int64 <= 0 || len(activeDigest) != 32 || len(setDigest) != 32 {
			return readiness{}, fmt.Errorf("%w: active catalog tuple is invalid", ErrSchemaMismatch)
		}
		var catalogDigest audit.CatalogDigest
		var catalogSet audit.CatalogSetDigest
		copy(catalogDigest[:], activeDigest)
		copy(catalogSet[:], setDigest)
		state.catalogs, err = audit.NewStoreCatalogState(audit.CatalogRef{
			ID: audit.CatalogID(activeID.String), Generation: audit.CatalogGeneration(activeGeneration.Int64), Digest: catalogDigest,
		}, catalogSet)
		if err != nil {
			return readiness{}, fmt.Errorf("%w: active catalog is malformed", ErrSchemaMismatch)
		}
	} else if len(setDigest) > 0 {
		var catalogSet audit.CatalogSetDigest
		copy(catalogSet[:], setDigest)
		state.catalogs, err = audit.NewInactiveStoreCatalogState(catalogSet)
		if err != nil {
			return readiness{}, fmt.Errorf("%w: installed catalog set is malformed", ErrSchemaMismatch)
		}
	} else {
		state.catalogs = audit.NewEmptyStoreCatalogState()
	}
	if fullSchemaInspection(on) {
		if err := verifyPhysicalSchema(ctx, on, schema); err != nil {
			return readiness{}, err
		}
		if err := verifyCatalogIntegrity(ctx, on, schema, state); err != nil {
			return readiness{}, err
		}
	}
	return state, nil
}

func fullSchemaInspection(on queryer) bool {
	switch on.(type) {
	case *sql.Conn, *sql.DB:
		return true
	default:
		return false
	}
}

func verifyPhysicalSchema(ctx context.Context, on queryer, schema Schema) error {
	expected := expectedSchemaDescriptors(schema)
	rows, err := on.QueryContext(ctx, physicalSchemaQuery(), schema.Name)
	if err != nil {
		return fmt.Errorf("%w: inspecting PostgreSQL catalogs: %v", ErrSchemaMismatch, err)
	}
	defer rows.Close()
	remaining := make(map[string]int, len(expected))
	for _, descriptor := range expected {
		remaining[schemaDescriptorKey(descriptor)]++
	}
	seen := 0
	for rows.Next() {
		seen++
		if seen > len(expected) {
			return fmt.Errorf("%w: managed schema has unexpected objects", ErrSchemaMismatch)
		}
		var descriptor schemaDescriptor
		if err := rows.Scan(&descriptor.kind, &descriptor.object, &descriptor.member, &descriptor.detail); err != nil {
			return fmt.Errorf("%w: scanning PostgreSQL catalogs: %v", ErrSchemaMismatch, err)
		}
		key := schemaDescriptorKey(descriptor)
		if remaining[key] == 0 {
			return fmt.Errorf("%w: unexpected or damaged %s %s.%s (%q)", ErrSchemaMismatch, descriptor.kind, descriptor.object, descriptor.member, descriptor.detail)
		}
		remaining[key]--
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: reading PostgreSQL catalogs: %v", ErrSchemaMismatch, err)
	}
	if seen != len(expected) {
		return fmt.Errorf("%w: managed schema objects are incomplete", ErrSchemaMismatch)
	}
	return nil
}

func schemaDescriptorKey(value schemaDescriptor) string {
	return value.kind + "\x00" + value.object + "\x00" + value.member + "\x00" + value.detail
}

func physicalSchemaQuery() string {
	names := make([]string, 0, len(postgresTables()))
	for _, table := range postgresTables() {
		names = append(names, "("+quoteLiteral(table.name)+")")
	}
	return `WITH managed_tables(name) AS (VALUES ` + strings.Join(names, ",") + `),
descriptors AS (
	SELECT 'table'::text AS kind, c.relname::text AS object, ''::text AS member,
		concat_ws(chr(31), c.relkind::text, c.relpersistence::text, c.relrowsecurity::text, c.relforcerowsecurity::text) AS detail
	FROM pg_catalog.pg_namespace n
	JOIN pg_catalog.pg_class c ON c.relnamespace=n.oid
	JOIN managed_tables m ON m.name=c.relname
	WHERE n.nspname=$1
	UNION ALL
	SELECT 'column', c.relname, a.attname,
		concat_ws(chr(31), pg_catalog.format_type(a.atttypid,a.atttypmod), a.attnotnull::text,
			a.attidentity::text, a.attgenerated::text, COALESCE(pg_catalog.pg_get_expr(d.adbin,d.adrelid,true),''))
	FROM pg_catalog.pg_namespace n
	JOIN pg_catalog.pg_class c ON c.relnamespace=n.oid
	JOIN managed_tables m ON m.name=c.relname
	JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
	LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
	WHERE n.nspname=$1 AND c.relkind='r'
	UNION ALL
	SELECT 'constraint', c.relname, '',
		concat_ws(chr(31), COALESCE(con.contype::text,''),
			COALESCE((SELECT string_agg(a.attname,',' ORDER BY key.ordinality)
				FROM unnest(con.conkey) WITH ORDINALITY key(attnum,ordinality)
				JOIN pg_catalog.pg_attribute a ON a.attrelid=con.conrelid AND a.attnum=key.attnum),''),
			CASE WHEN con.contype='f' THEN COALESCE(rn.nspname,'') ELSE '' END,
			CASE WHEN con.contype='f' THEN COALESCE(rc.relname,'') ELSE '' END,
			CASE WHEN con.contype='f' THEN COALESCE((SELECT string_agg(a.attname,',' ORDER BY key.ordinality)
				FROM unnest(con.confkey) WITH ORDINALITY key(attnum,ordinality)
				JOIN pg_catalog.pg_attribute a ON a.attrelid=con.confrelid AND a.attnum=key.attnum),'') ELSE '' END,
			con.condeferrable::text, con.condeferred::text,
			CASE WHEN con.contype='f' THEN con.confupdtype::text ELSE '' END,
			CASE WHEN con.contype='f' THEN con.confdeltype::text ELSE '' END,
			CASE WHEN con.contype='f' THEN con.confmatchtype::text ELSE '' END,
			con.convalidated::text, con.connoinherit::text,
			CASE WHEN con.contype='c' THEN regexp_replace(lower(COALESCE(pg_catalog.pg_get_expr(con.conbin,con.conrelid,true),'')), '[[:space:]()"]', '', 'g') ELSE '' END)
	FROM pg_catalog.pg_namespace n
	JOIN pg_catalog.pg_class c ON c.relnamespace=n.oid
	JOIN managed_tables m ON m.name=c.relname
	JOIN pg_catalog.pg_constraint con ON con.conrelid=c.oid AND con.contype IN ('p','u','f','c')
	LEFT JOIN pg_catalog.pg_class rc ON rc.oid=con.confrelid
	LEFT JOIN pg_catalog.pg_namespace rn ON rn.oid=rc.relnamespace
	WHERE n.nspname=$1 AND c.relkind='r'
	UNION ALL
	SELECT 'index', c.relname, '',
		concat_ws(chr(31), COALESCE(con.contype::text,''),
			COALESCE((SELECT string_agg(a.attname,',' ORDER BY key.ordinality)
				FROM unnest(i.indkey::smallint[]) WITH ORDINALITY key(attnum,ordinality)
				JOIN pg_catalog.pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=key.attnum
				WHERE key.ordinality<=i.indnkeyatts),''),
			i.indisunique::text, i.indisprimary::text, i.indisvalid::text, i.indisready::text,
			i.indislive::text, i.indisexclusion::text, am.amname, i.indnkeyatts::text, i.indnatts::text,
			regexp_replace(lower(COALESCE(pg_catalog.pg_get_expr(i.indpred,i.indrelid,true),'')), '[[:space:]()"]', '', 'g'),
			(i.indexprs IS NOT NULL)::text)
	FROM pg_catalog.pg_namespace n
	JOIN pg_catalog.pg_class c ON c.relnamespace=n.oid
	JOIN managed_tables m ON m.name=c.relname
	JOIN pg_catalog.pg_index i ON i.indrelid=c.oid
	JOIN pg_catalog.pg_class ic ON ic.oid=i.indexrelid
	JOIN pg_catalog.pg_am am ON am.oid=ic.relam
	LEFT JOIN pg_catalog.pg_constraint con ON con.conindid=i.indexrelid AND con.conrelid=i.indrelid AND con.contype IN ('p','u','x')
	WHERE n.nspname=$1 AND c.relkind='r'
	UNION ALL
	SELECT 'function', p.proname, '',
		concat_ws(chr(31), pg_catalog.pg_get_function_result(p.oid), l.lanname, p.provolatile::text,
			p.prosecdef::text, p.proisstrict::text, p.prokind::text, p.proparallel::text,
			p.proleakproof::text, COALESCE(array_to_string(p.proconfig,','),''),
			trim(regexp_replace(p.prosrc, '[[:space:]]+', ' ', 'g')))
	FROM pg_catalog.pg_namespace n
	JOIN pg_catalog.pg_proc p ON p.pronamespace=n.oid
	JOIN pg_catalog.pg_language l ON l.oid=p.prolang
	WHERE n.nspname=$1 AND p.proname='deny_immutable_audit_row' AND p.pronargs=0
	UNION ALL
	SELECT 'trigger', c.relname, t.tgname,
		concat_ws(chr(31), t.tgenabled::text, t.tgtype::integer::text, pn.nspname, p.proname,
			pg_catalog.encode(t.tgargs,'hex'),
			regexp_replace(lower(COALESCE(pg_catalog.pg_get_expr(t.tgqual,t.tgrelid,true),'')), '[[:space:]()"]', '', 'g'),
			t.tgconstraint::text, t.tgdeferrable::text, t.tginitdeferred::text)
	FROM pg_catalog.pg_namespace n
	JOIN pg_catalog.pg_class c ON c.relnamespace=n.oid
	JOIN managed_tables m ON m.name=c.relname
	JOIN pg_catalog.pg_trigger t ON t.tgrelid=c.oid AND NOT t.tgisinternal
	JOIN pg_catalog.pg_proc p ON p.oid=t.tgfoid
	JOIN pg_catalog.pg_namespace pn ON pn.oid=p.pronamespace
	WHERE n.nspname=$1 AND c.relkind='r'
)
SELECT kind, object, member, detail FROM descriptors
ORDER BY kind, object, member, detail
LIMIT ` + strconv.Itoa(len(expectedSchemaDescriptors(Schema{Name: DefaultSchema}))+1)
}

func verifyCatalogIntegrity(ctx context.Context, on queryer, schema Schema, expected readiness) error {
	var valid bool
	var backingBytes, logBytes []byte
	var activeID sql.NullString
	var activeGeneration sql.NullInt64
	var activeDigest, setDigest []byte
	err := on.QueryRowContext(ctx, catalogIntegrityQuery(schema), audit.MaxCatalogs+1, audit.MaxCatalogs, audit.MaxCatalogManifestBytes, audit.MaxCatalogSetBytes).Scan(
		&valid, &backingBytes, &logBytes, &activeID, &activeGeneration, &activeDigest, &setDigest,
	)
	if err != nil {
		return fmt.Errorf("%w: verifying installed catalog lineage: %v", ErrSchemaMismatch, err)
	}
	if !valid {
		return fmt.Errorf("%w: installed catalog lineage or digest is inconsistent", ErrSchemaMismatch)
	}
	if !catalogReadinessMatches(expected, backingBytes, logBytes, activeID, activeGeneration, activeDigest, setDigest) {
		return fmt.Errorf("%w: readiness changed during verification", ErrSchemaMismatch)
	}
	return nil
}

func catalogReadinessMatches(expected readiness, backingBytes, logBytes []byte, activeID sql.NullString, activeGeneration sql.NullInt64, activeDigest, setDigest []byte) bool {
	expectedSet := expected.catalogs.SetDigest()
	setMatches := bytes.Equal(expectedSet[:], setDigest)
	if expectedSet == (audit.CatalogSetDigest{}) {
		setMatches = len(setDigest) == 0
	}
	if !bytes.Equal(expected.backingID[:], backingBytes) || !bytes.Equal(expected.logID[:], logBytes) || !setMatches {
		return false
	}
	if expected.catalogs.HasActive() != activeID.Valid || activeID.Valid != activeGeneration.Valid || activeID.Valid != (len(activeDigest) > 0) {
		return false
	}
	if !activeID.Valid {
		return true
	}
	active := expected.catalogs.Active()
	return activeID.String == string(active.ID) && activeGeneration.Int64 == int64(active.Generation) && bytes.Equal(active.Digest[:], activeDigest)
}

func catalogIntegrityQuery(schema Schema) string {
	q := quoteIdentifier(schema.Name)
	return `WITH inventory_rows AS MATERIALIZED (
	SELECT catalog_id, generation, octet_length(canonical)::bigint AS canonical_size
	FROM ` + q + `.catalogs
	ORDER BY generation, catalog_id
	LIMIT $1
),
inventory AS MATERIALIZED (
	SELECT count(*)::integer AS count, COALESCE(sum(canonical_size),0)::bigint AS total_bytes,
		COALESCE(max(canonical_size),0)::bigint AS largest
	FROM inventory_rows
),
bounded AS MATERIALIZED (
	SELECT *, count<=$2 AND largest<=$3 AND total_bytes<=$4 AS valid FROM inventory
),
ordered AS MATERIALIZED (
	SELECT c.*, row_number() OVER (ORDER BY c.generation,c.catalog_id)::bigint AS ordinal,
		lag(c.catalog_id) OVER (ORDER BY c.generation,c.catalog_id) AS prior_id,
		lag(c.generation) OVER (ORDER BY c.generation,c.catalog_id) AS prior_generation,
		lag(c.digest) OVER (ORDER BY c.generation,c.catalog_id) AS prior_digest,
		pg_catalog.convert_to(c.catalog_id,'UTF8') AS id_wire,
		pg_catalog.convert_to(COALESCE(c.previous_catalog_id,''),'UTF8') AS previous_id_wire
	FROM ` + q + `.catalogs c, bounded b
	WHERE b.valid
),
frames AS MATERIALIZED (
	SELECT o.*, octet_length(id_wire)::integer AS id_size,
		octet_length(previous_id_wire)::integer AS previous_id_size
	FROM ordered o
),
catalogs AS MATERIALIZED (
	SELECT count(*)::integer AS count,
		COALESCE(bool_and(
			catalog_id<>'' AND generation=ordinal AND octet_length(digest)=32
			AND CASE WHEN ordinal=1 THEN previous_catalog_id IS NULL AND previous_generation IS NULL AND previous_digest IS NULL
				ELSE previous_catalog_id=prior_id AND previous_generation=prior_generation AND previous_digest=prior_digest END
			AND substring(canonical FROM 1 FOR 8)=pg_catalog.int8send(33)
			AND substring(canonical FROM 9 FOR 33)=pg_catalog.convert_to('frostgrove.audit/manifest-wire/v1','UTF8')
			AND substring(canonical FROM 42 FOR 8)=pg_catalog.int8send(id_size::bigint)
			AND substring(canonical FROM 50 FOR id_size)=id_wire
			AND substring(canonical FROM 50+id_size FOR 8)=pg_catalog.int8send(generation)
			AND substring(canonical FROM 58+id_size FOR 8)=pg_catalog.int8send(32)
			AND substring(canonical FROM 66+id_size FOR 32)=digest
			AND substring(canonical FROM 98+id_size FOR 8)=pg_catalog.int8send(previous_id_size::bigint)
			AND substring(canonical FROM 106+id_size FOR previous_id_size)=previous_id_wire
			AND substring(canonical FROM 106+id_size+previous_id_size FOR 8)=pg_catalog.int8send(COALESCE(previous_generation,0))
			AND substring(canonical FROM 114+id_size+previous_id_size FOR 8)=pg_catalog.int8send(32)
			AND substring(canonical FROM 122+id_size+previous_id_size FOR 32)=COALESCE(previous_digest,decode(repeat('00',32),'hex'))
			AND digest=pg_catalog.sha256(
				pg_catalog.int8send(36)||pg_catalog.convert_to('frostgrove.audit/catalog-manifest/v1','UTF8')
				||pg_catalog.int8send(octet_length(canonical)::bigint)
				||overlay(canonical PLACING decode(repeat('00',32),'hex') FROM 66+id_size FOR 32)
			)
		),true) AS rows_valid,
		CASE WHEN count(*)=0 THEN NULL ELSE pg_catalog.sha256(
			pg_catalog.int8send(31)||pg_catalog.convert_to('frostgrove.audit/catalog-set/v1','UTF8')
			||pg_catalog.int4send(count(*)::integer)
			||string_agg(pg_catalog.int8send(octet_length(canonical)::bigint)||canonical,''::bytea ORDER BY generation,catalog_id)
		) END AS set_digest
	FROM frames
),
state AS MATERIALIZED (
	SELECT backing_id, log_id, active_catalog_id, active_generation, active_digest, catalog_set_digest
	FROM ` + q + `.settings WHERE singleton
)
SELECT b.valid AND c.count=b.count AND c.rows_valid
	AND CASE WHEN c.count=0 THEN s.catalog_set_digest IS NULL AND s.active_catalog_id IS NULL
		ELSE s.catalog_set_digest=c.set_digest AND (s.active_catalog_id IS NULL OR EXISTS (
			SELECT 1 FROM ordered o WHERE o.catalog_id=s.active_catalog_id AND o.generation=s.active_generation AND o.digest=s.active_digest
		)) END,
	s.backing_id, s.log_id, s.active_catalog_id, s.active_generation, s.active_digest, s.catalog_set_digest
FROM state s CROSS JOIN bounded b CROSS JOIN catalogs c`
}

func sameReady(left, right readiness) bool {
	return left.backingID == right.backingID && left.logID == right.logID &&
		left.catalogs.HasActive() == right.catalogs.HasActive() &&
		left.catalogs.Active() == right.catalogs.Active() && left.catalogs.SetDigest() == right.catalogs.SetDigest()
}

func requireReady(current *readiness, closed bool) error {
	if closed {
		return audit.Failure(audit.Closed, ErrNotReady)
	}
	if current == nil {
		return audit.Failure(audit.Refused, ErrNotReady)
	}
	return nil
}

func migrationLock(schema string) int64 {
	digest := fnv.New64a()
	_, _ = digest.Write([]byte("frostgrove.audit.postgres.migration.v1\x00" + schema))
	return int64(digest.Sum64())
}

func positionOf(value int64) (audit.StorePosition, error) {
	if value <= 0 {
		return audit.StorePosition{}, fmt.Errorf("%w: position is not positive", ErrSchemaMismatch)
	}
	wire := make([]byte, 8)
	binary.BigEndian.PutUint64(wire, uint64(value))
	return audit.NewStorePosition(wire)
}

func digest32[T ~[32]byte](wire []byte) (T, bool) {
	var value T
	if len(wire) != len(value) {
		return value, false
	}
	copy(value[:], wire)
	return value, true
}

func id16[T ~[16]byte](wire []byte) (T, bool) {
	var value T
	if len(wire) != len(value) {
		return value, false
	}
	copy(value[:], wire)
	return value, true
}

func classifySQL(err error, uncertain bool) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, driver.ErrBadConn) {
		if uncertain {
			return audit.Failure(audit.Unconfirmed, err)
		}
		return audit.Failure(audit.NotWritten, err)
	}
	state := ""
	var stateError interface{ SQLState() string }
	if errors.As(err, &stateError) {
		state = stateError.SQLState()
	}
	switch {
	case state == "40001" || state == "40P01":
		return audit.Failure(audit.NotWritten, err)
	case strings.HasPrefix(state, "08"):
		if uncertain {
			return audit.Failure(audit.Unconfirmed, err)
		}
		return audit.Failure(audit.NotWritten, err)
	case strings.HasPrefix(state, "23"):
		return audit.Failure(audit.Conflict, err)
	case state == "42P01" || state == "3F000":
		return audit.Failure(audit.Refused, ErrSchemaMismatch)
	default:
		return audit.Failure(audit.Unconfirmed, err)
	}
}

func catalogSetDigest(canonical [][]byte) (audit.CatalogSetDigest, error) {
	if len(canonical) == 0 || len(canonical) > audit.MaxCatalogs {
		return audit.CatalogSetDigest{}, fmt.Errorf("%w: invalid catalog count", ErrSpec)
	}
	total := 0
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/catalog-set/v1"))
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(canonical)))
	_, _ = digest.Write(count[:])
	for _, manifest := range canonical {
		total += len(manifest)
		if len(manifest) == 0 || total > audit.MaxCatalogSetBytes {
			return audit.CatalogSetDigest{}, fmt.Errorf("%w: catalog set is too large", ErrSpec)
		}
		writeFrame(digest, manifest)
	}
	var result audit.CatalogSetDigest
	copy(result[:], digest.Sum(nil))
	return result, nil
}

func writeFrame(output interface{ Write([]byte) (int, error) }, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = output.Write(size[:])
	_, _ = output.Write(value)
}
