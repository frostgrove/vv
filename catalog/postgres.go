package catalog

import (
	"context"

	"github.com/shardit-io/vv/crud"
)

// PostgreSQL introspection, out of pg_catalog rather than information_schema.
//
// information_schema would answer most of this and cannot answer the part that
// matters: it has no row for a bare unique index, no partial-index predicate and
// no expression-index expression. pg_get_expr, pg_get_indexdef and
// pg_get_constraintdef hand all three back as the server's own text, so nothing
// here parses anything.
//
// Every statement is scoped by pg_table_is_visible, which is the server's own
// answer to "what does this bare name resolve to on this connection" — the
// search_path, applied once, on the connection the catalog loaded from, with the
// schema it resolved to recorded on every table ([[D-041]]).

const pgColumns = `
SELECT n.nspname, c.relname, a.attname, a.attnum::int,
       format_type(a.atttypid, a.atttypmod),
       a.attnotnull,
       a.attgenerated <> '',
       d.adbin IS NOT NULL,
       COALESCE(pg_get_expr(d.adbin, d.adrelid), ''),
       CASE WHEN t.typname IN ('varchar', 'bpchar') AND a.atttypmod > 4
            THEN a.atttypmod - 4 ELSE 0 END
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_type t ON t.oid = a.atttypid
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE c.relkind IN ('r', 'p')
  AND a.attnum > 0 AND NOT a.attisdropped
  AND pg_table_is_visible(c.oid)
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY n.nspname, c.relname, a.attnum`

// One row per constraint key column. confkey is joined on the same ordinal as
// conkey, so a composite foreign key's columns and the columns it references
// stay paired by position.
const pgConstraints = `
SELECT n.nspname, tc.relname, con.conname, con.contype::text, con.condeferrable,
       COALESCE(k.ord, 0)::int, COALESCE(a.attname, ''),
       COALESCE(fa.attname, ''),
       COALESCE(fn.nspname, ''), COALESCE(fc.relname, ''),
       CASE con.confdeltype WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL'
            WHEN 'd' THEN 'SET DEFAULT' WHEN 'r' THEN 'RESTRICT'
            WHEN 'a' THEN 'NO ACTION' ELSE '' END,
       CASE con.confupdtype WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL'
            WHEN 'd' THEN 'SET DEFAULT' WHEN 'r' THEN 'RESTRICT'
            WHEN 'a' THEN 'NO ACTION' ELSE '' END,
       pg_get_constraintdef(con.oid)
FROM pg_constraint con
JOIN pg_class tc ON tc.oid = con.conrelid
JOIN pg_namespace n ON n.oid = tc.relnamespace
LEFT JOIN pg_class fc ON fc.oid = con.confrelid
LEFT JOIN pg_namespace fn ON fn.oid = fc.relnamespace
LEFT JOIN LATERAL unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord) ON true
LEFT JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum
LEFT JOIN LATERAL unnest(con.confkey) WITH ORDINALITY AS fk(attnum, ord) ON fk.ord = k.ord
LEFT JOIN pg_attribute fa ON fa.attrelid = con.confrelid AND fa.attnum = fk.attnum
WHERE con.contype IN ('p', 'u', 'f', 'c')
  AND tc.relkind IN ('r', 'p')
  AND pg_table_is_visible(tc.oid)
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY n.nspname, tc.relname, con.conname, k.ord`

// The unique indexes no constraint backs. A unique index whose indexrelid has a
// pg_constraint row is that constraint and was already read above; one that has
// none is a key the database enforces under a name no constraint catalog knows,
// which is the distinction §7 asks for and PostgreSQL is one of the two engines
// that makes it.
//
// indkey holds 0 where the key part is an expression, and pg_get_indexdef with a
// column number renders that expression. indisvalid and indislive are checked
// because a CREATE INDEX CONCURRENTLY that failed leaves an index behind that
// enforces nothing.
//
// The anti-join names the three contypes and does not stop at conindid, because
// a foreign key carries a conindid too and it names the index it *references* on
// the parent table. Left unqualified, the clause deletes the parent's bare
// unique index — the one thing this statement exists to find — and the deletion
// is silent: Load succeeds and a live 23505 under that index's name resolves to
// nothing for the life of the process. Only p, u and x point at an index they
// are backed by.
const pgUniqueIndexes = `
SELECT n.nspname, tc.relname, ic.relname,
       COALESCE(a.attname, ''),
       CASE WHEN k.attnum = 0 THEN pg_get_indexdef(i.indexrelid, k.ord::int, true) ELSE '' END,
       i.indpred IS NOT NULL,
       COALESCE(pg_get_expr(i.indpred, i.indrelid), ''),
       pg_get_indexdef(i.indexrelid)
FROM pg_index i
JOIN pg_class ic ON ic.oid = i.indexrelid
JOIN pg_class tc ON tc.oid = i.indrelid
JOIN pg_namespace n ON n.oid = tc.relnamespace
CROSS JOIN LATERAL unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
LEFT JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
WHERE i.indisunique AND i.indisvalid AND i.indislive
  AND k.ord <= i.indnkeyatts
  AND tc.relkind IN ('r', 'p')
  AND pg_table_is_visible(tc.oid)
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND NOT EXISTS (SELECT 1 FROM pg_constraint con
                  WHERE con.conindid = i.indexrelid AND con.contype IN ('p', 'u', 'x'))
ORDER BY n.nspname, tc.relname, ic.relname, k.ord`

func readPostgres(ctx context.Context, src crud.Source) ([]Table, error) {
	b := newBuilder()

	var (
		schema, table, name, typ, def string
		pos, maxLen                   int
		notNull, generated, hasDef    bool
	)
	err := eachRow(ctx, src, "columns", pgColumns, nil, func(rows crud.Rows) error {
		if err := rows.Scan(&schema, &table, &name, &pos, &typ, &notNull, &generated, &hasDef, &def, &maxLen); err != nil {
			return err
		}
		tb := b.table(schema, table)
		col := Column{
			Name: name, Position: pos, Type: typ,
			Nullable: !notNull, MaxLength: maxLen, Generated: generated,
		}
		if hasDef {
			d := def
			col.Default = &d
		}
		tb.t.Columns = append(tb.t.Columns, col)
		return nil
	})
	if err != nil {
		return nil, err
	}

	var (
		conName, conType, col, refCol string
		refSchema, refTable           string
		onDelete, onUpdate, conDef    string
		deferrable                    bool
		ord                           int
	)
	err = eachRow(ctx, src, "constraints", pgConstraints, nil, func(rows crud.Rows) error {
		if err := rows.Scan(&schema, &table, &conName, &conType, &deferrable, &ord,
			&col, &refCol, &refSchema, &refTable, &onDelete, &onUpdate, &conDef); err != nil {
			return err
		}
		kind := pgKind(conType)
		if kind == 0 {
			return nil
		}
		c := b.table(schema, table).constraint(conName, kind)
		c.Deferrable = deferrable
		c.Definition = conDef
		if kind == KindForeignKey {
			c.RefSchema, c.RefTable = refSchema, refTable
			c.OnDelete, c.OnUpdate = onDelete, onUpdate
		}
		// A CHECK that names no column has one row with no ordinal. Appending
		// its empty name would claim a key part that does not exist, and
		// Columns is read by position.
		if ord == 0 {
			return nil
		}
		if kind == KindForeignKey {
			c.RefColumns = append(c.RefColumns, refCol)
		}
		c.Columns = append(c.Columns, col)
		return nil
	})
	if err != nil {
		return nil, err
	}

	var (
		idxName, expr, predicate, idxDef string
		partial                          bool
	)
	err = eachRow(ctx, src, "unique indexes", pgUniqueIndexes, nil, func(rows crud.Rows) error {
		// The key position is the row's place in the result, pinned by the
		// ORDER BY, so k.ord is in the statement and not in the SELECT.
		if err := rows.Scan(&schema, &table, &idxName, &col, &expr, &partial, &predicate, &idxDef); err != nil {
			return err
		}
		c := b.table(schema, table).constraint(idxName, KindUniqueIndex)
		c.Partial, c.Predicate, c.Definition = partial, predicate, idxDef
		c.Columns = append(c.Columns, col)
		c.Expressions = append(c.Expressions, expr)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return b.finish(), nil
}

// pgKind maps pg_constraint.contype. A CHECK arrives with the columns it names
// in conkey, which is why a check constraint here has columns at all.
func pgKind(contype string) Kind {
	switch contype {
	case "p":
		return KindPrimaryKey
	case "u":
		return KindUnique
	case "f":
		return KindForeignKey
	case "c":
		return KindCheck
	}
	return 0
}
