package catalog

import (
	"context"

	"github.com/shardit-io/vv/crud"
)

// MySQL 8.4 introspection, and the row-to-struct shaping MariaDB shares.
//
// The statements are written out per engine and not shared, because two of them
// cannot be: MariaDB's information_schema.STATISTICS has no EXPRESSION column
// and selecting it fails with error 1054, and its CHECK_CONSTRAINTS has the
// TABLE_NAME that MySQL's lacks. errs/sqlerr set the precedent with mysql.go and
// mariadb.go — two tables that agree on most rows and disagree on two, written
// out rather than merged, because merging is green today and wrong the first
// time either server moves. Here it would not even be green today.
//
// Every nullable value is wrapped in COALESCE and every predicate rendered as a
// comparison, so every destination is a plain string, int or bool: three drivers
// disagree about what a NULL scans into, and a portable statement is cheaper
// than a per-driver destination.
//
// Neither engine has partial indexes — CREATE UNIQUE INDEX ... WHERE is error
// 1064 on both — so Partial and Predicate stay empty here, and neither has
// deferred constraints. What they do have and PostgreSQL does not is the prefix
// index, and a prefix key is this family's unique key that cannot be reproduced
// from a value.

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

// One name can be two objects: UNIQUE KEY k (a) beside CONSTRAINT k FOREIGN KEY
// (a) is legal on both engines, because an index name and a foreign-key name
// live in different namespaces, and TABLE_CONSTRAINTS answers two rows for k.
// The two rows differ in nothing else, so an ORDER BY that stops at
// CONSTRAINT_NAME leaves which of them arrives first to the server — and the
// build keeps first sight, so that would decide which object Catalog.Constraint
// answers for k. Not a claim any server makes, and [[D-014]] does not let a read
// rest on one.
const myTableConstraints = `
SELECT TABLE_NAME, CONSTRAINT_NAME, CONSTRAINT_TYPE
FROM information_schema.TABLE_CONSTRAINTS
WHERE CONSTRAINT_SCHEMA = DATABASE() AND TABLE_SCHEMA = DATABASE()
ORDER BY TABLE_NAME, CONSTRAINT_NAME, CONSTRAINT_TYPE`

// STATISTICS is read for the key parts, not for the kind. §7 says a unique index
// that is not a constraint appears only here, and that is a PostgreSQL fact:
// measured on 8.4 and on MariaDB 11.4, TABLE_CONSTRAINTS lists a plain CREATE
// UNIQUE INDEX as UNIQUE. What STATISTICS alone has is the key columns, their
// order, SUB_PART and EXPRESSION.
const myStatistics = `
SELECT TABLE_NAME, INDEX_NAME, COALESCE(COLUMN_NAME, ''),
       COALESCE(SUB_PART, 0), COALESCE(EXPRESSION, ''), NON_UNIQUE = 0
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE()
ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`

// KEY_COLUMN_USAGE lists a unique key's parts under the same name and table as a
// foreign key of that name, and the join to REFERENTIAL_CONSTRAINTS cannot tell
// the two apart: those rows carry a NULL referenced table and would be read as
// key parts of the foreign key, which puts an empty entry into RefColumns and
// breaks the position parallel a probe reads by ([[D-042]]).
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

// MySQL's CHECK_CONSTRAINTS has four columns — catalog, schema, name and clause
// — and no TABLE_NAME. The join is the only way to learn which table a check
// belongs to, which is the whole of [[D-041]]'s "Constraint takes the table".
const myChecks = `
SELECT t.TABLE_NAME, c.CONSTRAINT_NAME, c.CHECK_CLAUSE
FROM information_schema.CHECK_CONSTRAINTS c
JOIN information_schema.TABLE_CONSTRAINTS t
  ON t.CONSTRAINT_SCHEMA = c.CONSTRAINT_SCHEMA
 AND t.CONSTRAINT_NAME = c.CONSTRAINT_NAME
WHERE c.CONSTRAINT_SCHEMA = DATABASE() AND t.CONSTRAINT_TYPE = 'CHECK'
ORDER BY t.TABLE_NAME, c.CONSTRAINT_NAME`

func readMySQL(ctx context.Context, src crud.Source) ([]Table, error) {
	b := newBuilder()
	// Kinds before key parts: a STATISTICS row is shaped by what
	// TABLE_CONSTRAINTS already said the index is.
	for _, step := range []func() error{
		func() error { return myReadColumns(ctx, src, b, myColumns, false) },
		func() error { return myReadTableConstraints(ctx, src, b, myTableConstraints) },
		func() error { return myReadStatistics(ctx, src, b, myStatistics, true) },
		func() error { return myReadForeignKeys(ctx, src, b, myForeignKeys) },
		func() error { return myReadChecks(ctx, src, b, myChecks) },
	} {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return b.finish(), nil
}

// ---------------------------------------------------------------------------
// the shaping both engines share

// myReadColumns turns one COLUMNS statement into columns.
//
// maria switches on one measured divergence and only one: MariaDB reports
// COLUMN_DEFAULT as the default's *expression text*, so a nullable column with
// no DEFAULT clause comes back as the unquoted word NULL rather than as SQL
// NULL. Read literally, every nullable MariaDB column would have a default of
// the four-character string "NULL". A column declared DEFAULT 'NULL' comes back
// quoted, so the two are still told apart.
func myReadColumns(ctx context.Context, src crud.Source, b *builder, stmt string, maria bool) error {
	var (
		schema, table, name, typ, def string
		pos, maxLen                   int
		nullable, hasDef, generated   bool
	)
	return eachRow(ctx, src, "columns", stmt, nil, func(rows crud.Rows) error {
		if err := rows.Scan(&schema, &table, &name, &pos, &typ,
			&nullable, &hasDef, &def, &maxLen, &generated); err != nil {
			return err
		}
		// Every statement after this one is scoped to DATABASE(), so the schema
		// is whatever this one said; asking the server again would be a round
		// trip for an answer already in hand.
		b.schema = schema
		tb := b.table(schema, table)
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

func myReadTableConstraints(ctx context.Context, src crud.Source, b *builder, stmt string) error {
	var table, name, kind string
	return eachRow(ctx, src, "constraints", stmt, nil, func(rows crud.Rows) error {
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

// myReadStatistics fills in the key parts of every unique index.
//
// A non-unique index is skipped: it enforces nothing, so it is not a constraint,
// and MySQL names the index it builds behind a foreign key after the constraint
// — reading those rows would overwrite that foreign key's columns with the
// index's.
//
// An index STATISTICS knows and TABLE_CONSTRAINTS does not becomes a
// KindUniqueIndex. Neither measured server produces one; the branch is what
// keeps the reading right if one ever stops listing them.
func myReadStatistics(ctx context.Context, src crud.Source, b *builder, stmt string, withExpression bool) error {
	var table, name, col, expr string
	var sub int
	var unique bool
	// SEQ_IN_INDEX is in the ORDER BY and not in the SELECT: the position is the
	// row's place in the result, and a scanned value nothing reads is one more
	// thing to keep in step with the statement.
	return eachRow(ctx, src, "index key columns", stmt, nil, func(rows crud.Rows) error {
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

func myReadForeignKeys(ctx context.Context, src crud.Source, b *builder, stmt string) error {
	var table, name, col, refSchema, refTable, refCol, onDelete, onUpdate string
	return eachRow(ctx, src, "foreign keys", stmt, nil, func(rows crud.Rows) error {
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

func myReadChecks(ctx context.Context, src crud.Source, b *builder, stmt string) error {
	var table, name, clause string
	return eachRow(ctx, src, "check constraints", stmt, nil, func(rows crud.Rows) error {
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
