package catalog

import (
	"context"

	"github.com/shardit-io/vv/crud"
)

// MariaDB 11.4 introspection. Its own statements; only the row-to-struct shaping
// in mysql.go is shared.
//
// It diverges from MySQL in both directions, which is why one statement set
// cannot serve both:
//
//   - information_schema.STATISTICS has no EXPRESSION column here — its columns
//     end INDEX_COMMENT, IGNORED — so MySQL's statement fails with error 1054.
//     MariaDB has no expression indexes to report either: CREATE UNIQUE INDEX ...
//     ((lower(alt))) is error 1064.
//   - information_schema.CHECK_CONSTRAINTS *has* TABLE_NAME here, which MySQL's
//     lacks, so the join MySQL needs is unnecessary work.
//   - COLUMN_DEFAULT is the default's expression text rather than its value, so
//     a nullable column with no DEFAULT clause reports the unquoted word NULL.
//     myReadColumns has the argument for what that costs.
//
// The two guards mysql.go's statements carry are here for the same measured
// reason and not for symmetry. KEY_COLUMN_USAGE lists a unique key's parts under
// the same name and table as a foreign key of that name, and those rows carry a
// NULL referenced table; without the guard MariaDB answered RefColumns ["" "id"]
// against Columns ["pid" "pid" "pid"] for one one-column key. And
// TABLE_CONSTRAINTS answers two rows for a name that is both, differing in
// nothing but CONSTRAINT_TYPE, so that column is in the ORDER BY — mysql.go
// states the argument.

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

func readMariaDB(ctx context.Context, src crud.Source) ([]Table, error) {
	b := newBuilder()
	for _, step := range []func() error{
		func() error { return myReadColumns(ctx, src, b, mariaColumns, true) },
		func() error { return myReadTableConstraints(ctx, src, b, mariaTableConstraints) },
		func() error { return myReadStatistics(ctx, src, b, mariaStatistics, false) },
		func() error { return myReadForeignKeys(ctx, src, b, mariaForeignKeys) },
		func() error { return myReadChecks(ctx, src, b, mariaChecks) },
	} {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return b.finish(), nil
}
