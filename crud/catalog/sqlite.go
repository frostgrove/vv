package catalog

import (
	"context"
	"sort"
	"strconv"

	"github.com/frostgrove/vv/crud"
)

// SQLite introspection, through the table-valued form of every PRAGMA.
//
// PRAGMA table_xinfo(?) is a parse error — a PRAGMA takes no bind parameters —
// so the only alternative to pragma_table_xinfo(?) is concatenating a table name
// into the statement text, which is the quoting problem [[D-003]] exists to
// avoid rather than to argue about. The table-valued form also joins against
// sqlite_master, so the whole schema is five statements rather than three per
// table.
//
// What SQLite cannot answer, and nothing here invents:
//
//   - The declared name of a unique constraint. CONSTRAINT uc UNIQUE(b, c)
//     comes back as sqlite_autoindex_t_2, and the name the author wrote is gone.
//   - A partial index's predicate. index_list reports partial = 1 and the WHERE
//     clause exists only inside the index's DDL, which nothing here parses.
//     Partial true with an empty Predicate is exactly that fact.
//   - An expression key's text. index_xinfo marks the key part with cid = -2 and
//     gives no name, so the part is an empty entry in Columns and Expressions
//     stays nil.
//   - CHECK constraints. No PRAGMA lists them; they exist only in the table's
//     DDL, which Table.Definition carries verbatim and nobody reads.
//   - A foreign key's name. SQLite does not record one, so the catalog uses the
//     position foreign_key_list reports, prefixed sqlite_ — a prefix the engine
//     itself refuses for user objects, so the synthetic name cannot collide with
//     a real one.
//   - The parent columns of a shorthand REFERENCES. `pid INTEGER REFERENCES p`
//     is a foreign key against p's primary key, and foreign_key_list reports
//     to = NULL rather than naming it. The empty RefColumns entry is that fact,
//     and it stays parallel to Columns by position. Filling it in from the
//     parent's own primary key would be inventing, which is what this list is.

const sqliteTables = `
SELECT name, COALESCE(sql, '')
FROM sqlite_master
WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\'
ORDER BY name`

const sqliteColumns = `
SELECT m.name, i.cid, i.name, i.type, i."notnull", i.pk, i.hidden,
       i.dflt_value IS NOT NULL, COALESCE(i.dflt_value, '')
FROM sqlite_master m
JOIN pragma_table_xinfo(m.name) i
WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'
ORDER BY m.name, i.cid`

const sqliteIndexes = `
SELECT m.name, l.name, l."unique", l.origin, l.partial, COALESCE(mi.sql, '')
FROM sqlite_master m
JOIN pragma_index_list(m.name) l
LEFT JOIN sqlite_master mi ON mi.type = 'index' AND mi.name = l.name
WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'
ORDER BY m.name, l.name`

// key = 1 drops the trailing rowid entry every index carries.
const sqliteIndexColumns = `
SELECT m.name, l.name, x.cid, COALESCE(x.name, '')
FROM sqlite_master m
JOIN pragma_index_list(m.name) l
JOIN pragma_index_xinfo(l.name) x
WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'
  AND x.key = 1
ORDER BY m.name, l.name, x.seqno`

// on_update comes before on_delete in the pragma's own column order, which is
// the opposite of the order the DDL is written in. Reading them in written order
// swaps the two actions and nothing complains.
//
// "to" is COALESCEd for the reason mysql.go states for its own columns: a NULL
// scanned into a string is a refusal, and this one is a refusal at start-up on a
// schema the engine accepts. REFERENCES parent with no column list is a foreign
// key against the parent's primary key, and the pragma answers NULL rather than
// naming it.
const sqliteForeignKeys = `
SELECT m.name, f.id, f."table", f."from", COALESCE(f."to", ''), f.on_update, f.on_delete
FROM sqlite_master m
JOIN pragma_foreign_key_list(m.name) f
WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'
ORDER BY m.name, f.id, f.seq`

// sqliteSchema is the only schema this loader reads. An ATTACHed database is a
// second schema on the same connection and is deliberately not walked: it is a
// different database, and merging it into this catalog is the silent merge the
// handle key exists to prevent ([[D-041]]).
const sqliteSchema = "main"

func readSQLite(ctx context.Context, source crud.Source) (*schemaRead, error) {
	b := newBuilder()
	b.schema = sqliteSchema

	var (
		table, name, typ, def, origin, idxName, idxDef, refTable, refCol, col string
		cid, pk, hidden, fkID                                                 int
		notNull, hasDef, unique, partial                                      bool
		onDelete, onUpdate                                                    string
	)

	err := eachRow(ctx, source, "tables", sqliteTables, nil, func(rows crud.Rows) error {
		if err := rows.Scan(&table, &def); err != nil {
			return err
		}
		tb := b.table(sqliteSchema, table)
		b.markBare(sqliteSchema, table)
		tb.t.Definition = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	// pk is the column's 1-based position in the primary key, and 0 for a column
	// outside it. A rowid primary key has no index at all, so this pragma is the
	// only place SQLite reports one.
	pkParts := map[string][]pkPart{}
	err = eachRow(ctx, source, "columns", sqliteColumns, nil, func(rows crud.Rows) error {
		if err := rows.Scan(&table, &cid, &name, &typ, &notNull, &pk, &hidden, &hasDef, &def); err != nil {
			return err
		}
		tb := b.table(sqliteSchema, table)
		c := Column{
			// cid is 0-based here and ORDINAL_POSITION is 1-based everywhere
			// else, so it is shifted and the field means one thing.
			Name: name, Position: cid + 1, Type: typ,
			// Reported as the engine reports it. An INTEGER PRIMARY KEY says
			// notnull = 0 and still refuses a NULL, and a TEXT PRIMARY KEY says
			// the same and genuinely accepts one — a catalog that "corrected"
			// the first would be wrong about the second.
			Nullable: !notNull,
			// hidden is 2 for a VIRTUAL and 3 for a STORED generated column.
			Generated: hidden == 2 || hidden == 3,
		}
		if hasDef {
			d := def
			c.Default = &d
		}
		tb.t.Columns = append(tb.t.Columns, c)
		if pk > 0 {
			pkParts[table] = append(pkParts[table], pkPart{pos: pk, name: name})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Walked in table order rather than in the map's, because a map's iteration
	// order is not an order ([[D-014]]).
	for _, tb := range b.tables {
		parts := pkParts[tb.t.Name]
		sort.Slice(parts, func(i, j int) bool { return parts[i].pos < parts[j].pos })
		for _, p := range parts {
			tb.t.PrimaryKey = append(tb.t.PrimaryKey, p.name)
		}
	}

	err = eachRow(ctx, source, "indexes", sqliteIndexes, nil, func(rows crud.Rows) error {
		if err := rows.Scan(&table, &idxName, &unique, &origin, &partial, &idxDef); err != nil {
			return err
		}
		if !unique {
			return nil
		}
		c := b.table(sqliteSchema, table).constraint(idxName, sqliteKind(origin))
		c.Partial, c.Definition = partial, idxDef
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = eachRow(ctx, source, "index key columns", sqliteIndexColumns, nil, func(rows crud.Rows) error {
		if err := rows.Scan(&table, &idxName, &cid, &col); err != nil {
			return err
		}
		tb := b.table(sqliteSchema, table)
		c, ok := tb.byCon[conBuildKey{name: idxName, family: famKey}]
		if !ok {
			// A non-unique index. It enforces nothing, so it is not a
			// constraint and the read above skipped it.
			return nil
		}
		// cid is -2 for an expression key part. SQLite gives no text for it, so
		// the empty column entry is the whole of what is known.
		if cid < 0 {
			col = ""
		}
		c.Columns = append(c.Columns, col)
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = eachRow(ctx, source, "foreign keys", sqliteForeignKeys, nil, func(rows crud.Rows) error {
		if err := rows.Scan(&table, &fkID, &refTable, &col, &refCol, &onUpdate, &onDelete); err != nil {
			return err
		}
		c := b.table(sqliteSchema, table).constraint(sqliteFKName(fkID), KindForeignKey)
		c.RefSchema, c.RefTable = sqliteSchema, refTable
		c.OnDelete, c.OnUpdate = onDelete, onUpdate
		c.Columns = append(c.Columns, col)
		c.RefColumns = append(c.RefColumns, refCol)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return b.finish(), nil
}

type pkPart struct {
	pos  int
	name string
}

// sqliteKind reads pragma_index_list.origin, which is the one thing SQLite says
// about a unique key that MySQL and MariaDB do not: "u" is a UNIQUE declared in
// the table, "c" is a CREATE UNIQUE INDEX, "pk" is the primary key.
func sqliteKind(origin string) Kind {
	switch origin {
	case "pk":
		return KindPrimaryKey
	case "u":
		return KindUnique
	}
	return KindUniqueIndex
}

func sqliteFKName(id int) string { return "sqlite_fk_" + strconv.Itoa(id) }
