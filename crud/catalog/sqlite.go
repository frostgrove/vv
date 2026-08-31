package catalog

import (
	"context"
	"sort"
	"strconv"

	"github.com/frostgrove/vv/crud"
)

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

const sqliteIndexColumns = `
SELECT m.name, l.name, x.cid, COALESCE(x.name, '')
FROM sqlite_master m
JOIN pragma_index_list(m.name) l
JOIN pragma_index_xinfo(l.name) x
WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'
  AND x.key = 1
ORDER BY m.name, l.name, x.seqno`

const sqliteForeignKeys = `
SELECT m.name, f.id, f."table", f."from", COALESCE(f."to", ''), f.on_update, f.on_delete
FROM sqlite_master m
JOIN pragma_foreign_key_list(m.name) f
WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'
ORDER BY m.name, f.id, f.seq`

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

	pkParts := map[string][]pkPart{}
	err = eachRow(ctx, source, "columns", sqliteColumns, nil, func(rows crud.Rows) error {
		if err := rows.Scan(&table, &cid, &name, &typ, &notNull, &pk, &hidden, &hasDef, &def); err != nil {
			return err
		}
		tb := b.table(sqliteSchema, table)
		c := Column{
			Name: name, Position: cid + 1, Type: typ,

			Nullable: !notNull,

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
			return nil
		}

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
