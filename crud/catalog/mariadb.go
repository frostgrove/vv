package catalog

import (
	"context"

	"github.com/frostgrove/vv/crud"
)

const mariaColumns = `
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

const mariaTableConstraints = `
SELECT TABLE_NAME, CONSTRAINT_NAME, CONSTRAINT_TYPE
FROM information_schema.TABLE_CONSTRAINTS
WHERE CONSTRAINT_SCHEMA = DATABASE() AND TABLE_SCHEMA = DATABASE()
ORDER BY TABLE_NAME, CONSTRAINT_NAME, CONSTRAINT_TYPE`

const mariaStatistics = `
SELECT TABLE_NAME, INDEX_NAME, COALESCE(COLUMN_NAME, ''),
       COALESCE(SUB_PART, 0), NON_UNIQUE = 0
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE()
ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`

const mariaForeignKeys = `
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

const mariaChecks = `
SELECT TABLE_NAME, CONSTRAINT_NAME, CHECK_CLAUSE
FROM information_schema.CHECK_CONSTRAINTS
WHERE CONSTRAINT_SCHEMA = DATABASE()
ORDER BY TABLE_NAME, CONSTRAINT_NAME`

func readMariaDB(ctx context.Context, source crud.Source) (*schemaRead, error) {
	b := newBuilder()
	for _, step := range []func() error{
		func() error { return myReadColumns(ctx, source, b, mariaColumns, true) },
		func() error { return myReadTableConstraints(ctx, source, b, mariaTableConstraints) },
		func() error { return myReadStatistics(ctx, source, b, mariaStatistics, false) },
		func() error { return myReadForeignKeys(ctx, source, b, mariaForeignKeys) },
		func() error { return myReadChecks(ctx, source, b, mariaChecks) },
	} {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return b.finish(), nil
}
