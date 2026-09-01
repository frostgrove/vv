package jobspg

import (
	"context"
	"database/sql"
	"fmt"
)

func (r repository) intentKeysMigrationStatements() []string {
	return []string{
		`ALTER TABLE ` + r.deliveries + ` ADD COLUMN IF NOT EXISTS intent_keys bytea`,
	}
}

func (r repository) intentKeysColumnValidationStatements() []string {
	return []string{`DO $frostgrove$
BEGIN
IF NOT EXISTS (
  SELECT 1
  FROM pg_attribute
  WHERE attrelid = '` + r.deliveries + `'::regclass
    AND attname = 'intent_keys'
    AND attnum > 0
    AND NOT attisdropped
    AND atttypid = 'bytea'::regtype
    AND atttypmod = -1
    AND NOT attnotnull
    AND NOT atthasdef
    AND attgenerated = ''
) THEN
  RAISE EXCEPTION 'jobspg: intent_keys column contract mismatch';
END IF;
END
$frostgrove$`}
}

func (r repository) intentKeysColumnReady(ctx context.Context, querier indexStateQuerier) (bool, error) {
	var ready bool
	err := querier.QueryRowContext(ctx, `SELECT atttypid = 'bytea'::regtype
       AND atttypmod = -1
       AND NOT attnotnull
       AND NOT atthasdef
       AND attgenerated = ''
FROM pg_attribute
WHERE attrelid = '`+r.deliveries+`'::regclass
  AND attname = 'intent_keys'
  AND attnum > 0
	AND NOT attisdropped`).Scan(&ready)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !ready {
		return false, fmt.Errorf("%w: intent_keys column contract", ErrSchemaMismatch)
	}
	return true, nil
}

func (r repository) validateIntentKeysColumn(ctx context.Context, querier indexStateQuerier) error {
	ready, err := r.intentKeysColumnReady(ctx, querier)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("%w: intent_keys column is missing", ErrSchemaMismatch)
	}
	return nil
}
