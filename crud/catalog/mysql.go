package catalog

import (
	"context"

	"github.com/frostgrove/vv/crud"
)

const myColumns = `
SELECT c.TABLE_SCHEMA, c.TABLE_NAME, c.COLUMN_NAME, c.ORDINAL_POSITION, c.COLUMN_TYPE,
       c.IS_NULLABLE = 'YES',
       c.COLUMN_DEFAULT IS NOT NULL, COALESCE(c.COLUMN_DEFAULT, ''),
       COALESCE(c.CHARACTER_MAXIMUM_LENGTH, 0),
       COALESCE(c.GENERATION_EXPRESSION, '') <> ''
FROM information_schema.COLUMNS c
JOIN information_schema.TABLES t
  ON t.TABLE_SCHEMA = c.TABLE_SCHEMA AND t.TABLE_NAME = c.TABLE_NAME
WHERE c.TABLE_SCHEMA = DATABASE() AND t.TABLE_TYPE = 'BASE TABLE'
ORDER BY c.TABLE_NAME, c.ORDINAL_POSITION`

const myTableConstraints = `
SELECT TABLE_NAME, CONSTRAINT_NAME, CONSTRAINT_TYPE
FROM information_schema.TABLE_CONSTRAINTS
WHERE CONSTRAINT_SCHEMA = DATABASE() AND TABLE_SCHEMA = DATABASE()
ORDER BY TABLE_NAME, CONSTRAINT_NAME, CONSTRAINT_TYPE`

const myStatistics = `
SELECT TABLE_NAME, INDEX_NAME, COALESCE(COLUMN_NAME, ''),
       COALESCE(SUB_PART, 0), COALESCE(EXPRESSION, ''), NON_UNIQUE = 0
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE()
ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`

const myForeignKeys = `
SELECT k.TABLE_NAME, k.CONSTRAINT_NAME, k.COLUMN_NAME,
       COALESCE(k.REFERENCED_TABLE_SCHEMA, ''), COALESCE(k.REFERENCED_TABLE_NAME, ''),
       COALESCE(k.REFERENCED_COLUMN_NAME, ''), r.DELETE_RULE, r.UPDATE_RULE
FROM information_schema.KEY_COLUMN_USAGE k
JOIN information_schema.REFERENTIAL_CONSTRAINTS r
  ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA
 AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME
 AND r.TABLE_NAME = k.TABLE_NAME
WHERE k.CONSTRAINT_SCHEMA = DATABASE() AND k.REFERENCED_TABLE_NAME IS NOT NULL
ORDER BY k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`

const myChecks = `
SELECT t.TABLE_NAME, c.CONSTRAINT_NAME, c.CHECK_CLAUSE
FROM information_schema.CHECK_CONSTRAINTS c
JOIN information_schema.TABLE_CONSTRAINTS t
  ON t.CONSTRAINT_SCHEMA = c.CONSTRAINT_SCHEMA
 AND t.CONSTRAINT_NAME = c.CONSTRAINT_NAME
WHERE c.CONSTRAINT_SCHEMA = DATABASE() AND t.CONSTRAINT_TYPE = 'CHECK'
ORDER BY t.TABLE_NAME, c.CONSTRAINT_NAME`

func readMySQL(ctx context.Context, source crud.Source) (*schemaRead, error) {
	b := newBuilder()

	for _, step := range []func() error{
		func() error { return myReadColumns(ctx, source, b, myColumns, false) },
		func() error { return myReadTableConstraints(ctx, source, b, myTableConstraints) },
		func() error { return myReadStatistics(ctx, source, b, myStatistics, true) },
		func() error { return myReadForeignKeys(ctx, source, b, myForeignKeys) },
		func() error { return myReadChecks(ctx, source, b, myChecks) },
	} {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return b.finish(), nil
}

func myReadColumns(ctx context.Context, source crud.Source, b *builder, stmt string, maria bool) error {
	var (
		schema, table, name, typ, def string
		pos, maxLen                   int
		nullable, hasDef, generated   bool
	)
	return eachRow(ctx, source, "columns", stmt, nil, func(rows crud.Rows) error {
		if err := rows.Scan(&schema, &table, &name, &pos, &typ,
			&nullable, &hasDef, &def, &maxLen, &generated); err != nil {
			return err
		}

		b.schema = schema
		tb := b.table(schema, table)
		b.markBare(schema, table)
		col := Column{
			Name: name, Position: pos, Type: typ,
			Nullable: nullable, MaxLength: maxLen, Generated: generated,
		}
		if hasDef && !(maria && def == "NULL") {
			d := def
			col.Default = &d
		}
		tb.t.Columns = append(tb.t.Columns, col)
		return nil
	})
}

func myReadTableConstraints(ctx context.Context, source crud.Source, b *builder, stmt string) error {
	var table, name, kind string
	return eachRow(ctx, source, "constraints", stmt, nil, func(rows crud.Rows) error {
		if err := rows.Scan(&table, &name, &kind); err != nil {
			return err
		}
		k := myKind(kind)
		if k == 0 {
			return nil
		}
		b.table(b.schema, table).constraint(name, k)
		return nil
	})
}

func myReadStatistics(ctx context.Context, source crud.Source, b *builder, stmt string, withExpression bool) error {
	var table, name, col, expr string
	var sub int
	var unique bool

	return eachRow(ctx, source, "index key columns", stmt, nil, func(rows crud.Rows) error {
		var err error
		if withExpression {
			err = rows.Scan(&table, &name, &col, &sub, &expr, &unique)
		} else {
			expr = ""
			err = rows.Scan(&table, &name, &col, &sub, &unique)
		}
		if err != nil {
			return err
		}
		if !unique {
			return nil
		}
		c := b.table(b.schema, table).constraint(name, KindUniqueIndex)
		c.Columns = append(c.Columns, col)
		c.Prefixes = append(c.Prefixes, sub)
		if withExpression {
			c.Expressions = append(c.Expressions, expr)
		}
		return nil
	})
}

func myReadForeignKeys(ctx context.Context, source crud.Source, b *builder, stmt string) error {
	var table, name, col, refSchema, refTable, refCol, onDelete, onUpdate string
	return eachRow(ctx, source, "foreign keys", stmt, nil, func(rows crud.Rows) error {
		if err := rows.Scan(&table, &name, &col, &refSchema, &refTable, &refCol,
			&onDelete, &onUpdate); err != nil {
			return err
		}
		c := b.table(b.schema, table).constraint(name, KindForeignKey)
		c.RefSchema, c.RefTable = refSchema, refTable
		c.OnDelete, c.OnUpdate = onDelete, onUpdate
		c.Columns = append(c.Columns, col)
		c.RefColumns = append(c.RefColumns, refCol)
		return nil
	})
}

func myReadChecks(ctx context.Context, source crud.Source, b *builder, stmt string) error {
	var table, name, clause string
	return eachRow(ctx, source, "check constraints", stmt, nil, func(rows crud.Rows) error {
		if err := rows.Scan(&table, &name, &clause); err != nil {
			return err
		}
		c := b.table(b.schema, table).constraint(name, KindCheck)
		c.Definition = clause
		return nil
	})
}

func myKind(t string) Kind {
	switch t {
	case "PRIMARY KEY":
		return KindPrimaryKey
	case "UNIQUE":
		return KindUnique
	case "FOREIGN KEY":
		return KindForeignKey
	case "CHECK":
		return KindCheck
	}
	return 0
}
