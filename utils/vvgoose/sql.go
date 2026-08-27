package vvgoose

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"github.com/frostgrove/vv/utils/vvdb"
	"github.com/frostgrove/vv/utils/vvgoose/internal/modelscan"
)

func renderMigration(engine vvdb.Engine, table string, model *modelscan.Model) ([]byte, error) {
	if model == nil || len(model.Fields) == 0 {
		return renderEmptyMigration(table), nil
	}
	copy := *model
	if copy.Table == "" {
		copy.Table = table
	}
	return renderModelsMigration(engine, []modelscan.Model{copy})
}

func renderEmptyMigration(name string) []byte {
	return []byte(fmt.Sprintf(`-- +goose Up
-- Write the forward migration for %s here.

-- +goose Down
-- Write the rollback migration for %s here.
`, name, name))
}

// renderModelsMigration puts every supplied model into one Goose migration.
// Downs run in reverse creation order, which is the safe order once a model
// starts declaring foreign keys.
func renderModelsMigration(engine vvdb.Engine, models []modelscan.Model) ([]byte, error) {
	if len(models) == 0 {
		return nil, fmt.Errorf("vvgoose: no models were selected")
	}

	up := make([]string, 0, len(models))
	down := make([]string, 0, len(models))
	for _, model := range models {
		if len(model.Fields) == 0 {
			return nil, fmt.Errorf("vvgoose: model %s has no mapped columns", model.Label())
		}
		create, drop, err := renderTableStatements(engine, model.Table, &model)
		if err != nil {
			return nil, err
		}
		up = append(up, create)
		down = append(down, drop)
	}

	var out bytes.Buffer
	fmt.Fprintln(&out, "-- +goose Up")
	fmt.Fprintln(&out, strings.Join(up, "\n\n"))
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "-- +goose Down")
	for i := len(down) - 1; i >= 0; i-- {
		fmt.Fprintln(&out, down[i])
	}
	return out.Bytes(), nil
}

func renderTableStatements(engine vvdb.Engine, table string, model *modelscan.Model) (create, drop string, err error) {
	quotedTable := quoteQualified(engine, table)
	if quotedTable == "" {
		return "", "", fmt.Errorf("vvgoose: table %q is not a valid identifier", table)
	}
	primaryKeys := make([]modelscan.Field, 0, 1)
	autoPrimaryKeys := 0
	for _, field := range model.Fields {
		if field.PrimaryKey {
			primaryKeys = append(primaryKeys, field)
			if field.Auto {
				autoPrimaryKeys++
			}
		}
	}
	compositePrimaryKey := len(primaryKeys) > 1
	if autoPrimaryKeys > 1 {
		return "", "", fmt.Errorf("vvgoose: %s has more than one auto primary-key field", model.Name)
	}
	if compositePrimaryKey && autoPrimaryKeys > 0 && engine == vvdb.SQLite {
		return "", "", fmt.Errorf("vvgoose: %s has an auto field in a composite primary key, which SQLite cannot represent", model.Name)
	}
	if compositePrimaryKey && autoPrimaryKeys == 1 && (engine == vvdb.MySQL || engine == vvdb.MariaDB) {
		// InnoDB requires an AUTO_INCREMENT column to be the first column of
		// an index. Moving only the table constraint preserves the source column
		// order while keeping the same composite-key uniqueness semantics.
		ordered := make([]modelscan.Field, 0, len(primaryKeys))
		for _, field := range primaryKeys {
			if field.Auto {
				ordered = append(ordered, field)
			}
		}
		for _, field := range primaryKeys {
			if !field.Auto {
				ordered = append(ordered, field)
			}
		}
		primaryKeys = ordered
	}
	columns := make([]string, 0, len(model.Fields)+1)
	seen := make(map[string]bool, len(model.Fields))
	for _, field := range model.Fields {
		if field.Column == "" {
			return "", "", fmt.Errorf("vvgoose: %s.%s has an empty database column", model.Name, field.Name)
		}
		folded := strings.ToLower(field.Column)
		if seen[folded] {
			return "", "", fmt.Errorf("vvgoose: %s maps more than one field to column %q", model.Name, field.Column)
		}
		seen[folded] = true
		definition, err := renderColumn(engine, field, !compositePrimaryKey)
		if err != nil {
			return "", "", fmt.Errorf("vvgoose: %s.%s: %w", model.Name, field.Name, err)
		}
		columns = append(columns, "    "+definition)
	}
	if compositePrimaryKey {
		quoted := make([]string, 0, len(primaryKeys))
		for _, field := range primaryKeys {
			name := quoteIdentifier(engine, field.Column)
			if name == "" {
				return "", "", fmt.Errorf("vvgoose: composite primary key column %q is not a valid identifier", field.Column)
			}
			quoted = append(quoted, name)
		}
		columns = append(columns, "    PRIMARY KEY ("+strings.Join(quoted, ", ")+")")
	}

	return "CREATE TABLE " + quotedTable + " (\n" + strings.Join(columns, ",\n") + "\n);", "DROP TABLE IF EXISTS " + quotedTable + ";", nil
}

func renderColumn(engine vvdb.Engine, field modelscan.Field, inlinePrimaryKey bool) (string, error) {
	name := quoteIdentifier(engine, field.Column)
	if name == "" {
		return "", fmt.Errorf("column %q is not a valid identifier", field.Column)
	}
	fieldType := modelFieldType(field)
	if field.Generated && !timestampType(fieldType) {
		return "", fmt.Errorf("generated column needs a database expression that source tags do not provide; use --empty and declare it explicitly")
	}
	typ, integer := sqlType(engine, fieldType)
	if typ == "" {
		return "", fmt.Errorf("cannot map Go type %q", field.GoType)
	}
	if field.Auto && !integer {
		return "", fmt.Errorf("auto column has non-integer Go type %q", fieldType)
	}

	if field.PrimaryKey && field.Auto && integer && engine == vvdb.Postgres && typ == "NUMERIC(20)" {
		// PostgreSQL identity sequences do not support NUMERIC. Go's uint is
		// gorm.Model's conventional key, and BIGINT is its practical SQL home.
		typ = "BIGINT"
	}
	if field.PrimaryKey && (engine == vvdb.MySQL || engine == vvdb.MariaDB) && typ == "TEXT" {
		// MySQL cannot index an unbounded TEXT primary key without an explicit
		// prefix. A bounded string key is the portable mapping.
		typ = "VARCHAR(255)"
	}
	parts := []string{name, typ}
	if field.PrimaryKey && field.Auto && integer && (inlinePrimaryKey || engine != vvdb.SQLite) {
		switch engine {
		case vvdb.Postgres:
			parts = append(parts, "GENERATED BY DEFAULT AS IDENTITY")
		case vvdb.MySQL, vvdb.MariaDB:
			parts = append(parts, "AUTO_INCREMENT")
		case vvdb.SQLite:
			// SQLite's automatically assigned rowid requires this declaration.
			parts[1] = "INTEGER"
		}
	}
	if field.PrimaryKey && inlinePrimaryKey {
		parts = append(parts, "PRIMARY KEY")
	}
	if field.PrimaryKey && !inlinePrimaryKey {
		parts = append(parts, "NOT NULL")
	} else if !field.Nullable && !field.PrimaryKey {
		parts = append(parts, "NOT NULL")
	}
	if field.Version {
		parts = append(parts, "DEFAULT 1")
	} else if field.Generated && timestampType(fieldType) {
		parts = append(parts, "DEFAULT CURRENT_TIMESTAMP")
	}
	return strings.Join(parts, " "), nil
}

func modelFieldType(field modelscan.Field) string {
	// UUID and Decimal are semantic scalar names, not merely their Go storage
	// representation. Preserve those exact names before resolving a local
	// declaration such as `type UUID [16]byte` to array/JSON SQL.
	for _, sourceType := range []string{field.CanonicalType, field.GoType} {
		switch semantic := baseGoType(sourceType); semantic {
		case "uuid.UUID", "decimal.Decimal":
			return semantic
		}
	}
	if field.UnderlyingType != "" {
		return field.UnderlyingType
	}
	if field.CanonicalType != "" {
		return field.CanonicalType
	}
	return field.GoType
}

// sqlType deliberately has a useful fallback for application-defined scalar
// types. Source analysis cannot know whether `Status` has string or integer as
// its underlying type without loading and type-checking the whole program; a
// TEXT column is editable and safe, while guessing an integer can destroy data.
func sqlType(engine vvdb.Engine, raw string) (typ string, integer bool) {
	base := baseGoType(raw)
	switch base {
	case "int8":
		return integerSQL(engine, "SMALLINT", "TINYINT"), true
	case "uint8":
		return integerSQL(engine, "SMALLINT", "TINYINT UNSIGNED"), true
	case "int16":
		return integerSQL(engine, "SMALLINT", "SMALLINT"), true
	case "uint16":
		return integerSQL(engine, "INTEGER", "SMALLINT UNSIGNED"), true
	case "int32", "rune":
		return integerSQL(engine, "INTEGER", "INT"), true
	case "uint32":
		return integerSQL(engine, "BIGINT", "INT UNSIGNED"), true
	case "int", "int64", "time.Duration":
		return integerSQL(engine, "BIGINT", "BIGINT"), true
	case "uint", "uint64":
		return integerSQL(engine, "NUMERIC(20)", "BIGINT UNSIGNED"), true
	case "sql.NullInt16":
		return integerSQL(engine, "SMALLINT", "SMALLINT"), true
	case "sql.NullInt32":
		return integerSQL(engine, "INTEGER", "INT"), true
	case "sql.NullInt64":
		return integerSQL(engine, "BIGINT", "BIGINT"), true
	case "float32":
		switch engine {
		case vvdb.Postgres:
			return "REAL", false
		case vvdb.MySQL, vvdb.MariaDB:
			return "FLOAT", false
		default:
			return "REAL", false
		}
	case "float64", "sql.NullFloat64":
		switch engine {
		case vvdb.Postgres:
			return "DOUBLE PRECISION", false
		case vvdb.MySQL, vvdb.MariaDB:
			return "DOUBLE", false
		default:
			return "REAL", false
		}
	case "bool", "sql.NullBool":
		return "BOOLEAN", false
	case "string", "sql.NullString", "url.URL", "net.IP":
		return "TEXT", false
	case "[]byte", "sql.RawBytes":
		if engine == vvdb.Postgres {
			return "BYTEA", false
		}
		return "BLOB", false
	case "time.Time", "sql.NullTime", "gorm.DeletedAt":
		switch engine {
		case vvdb.Postgres:
			return "TIMESTAMP WITH TIME ZONE", false
		case vvdb.MySQL, vvdb.MariaDB:
			return "DATETIME(6)", false
		default:
			return "DATETIME", false
		}
	case "uuid.UUID":
		switch engine {
		case vvdb.Postgres:
			return "UUID", false
		case vvdb.MySQL, vvdb.MariaDB:
			return "CHAR(36)", false
		default:
			return "TEXT", false
		}
	case "json.RawMessage", "map", "slice", "array":
		switch engine {
		case vvdb.Postgres:
			return "JSONB", false
		case vvdb.MySQL, vvdb.MariaDB:
			return "JSON", false
		default:
			return "TEXT", false
		}
	case "decimal.Decimal", "big.Int", "big.Rat":
		if engine == vvdb.SQLite {
			return "TEXT", false
		}
		return "NUMERIC", false
	case "":
		return "", false
	default:
		return "TEXT", false
	}
}

func integerSQL(engine vvdb.Engine, postgres, mysql string) string {
	switch engine {
	case vvdb.Postgres:
		return postgres
	case vvdb.MySQL, vvdb.MariaDB:
		return mysql
	default:
		return "INTEGER"
	}
}

func baseGoType(raw string) string {
	typ := strings.ReplaceAll(strings.TrimSpace(raw), " ", "")
	for strings.HasPrefix(typ, "*") {
		typ = strings.TrimPrefix(typ, "*")
	}
	for {
		open := strings.IndexByte(typ, '[')
		if open <= 0 || !strings.HasSuffix(typ, "]") {
			break
		}
		wrapper := typ[:open]
		if wrapper != "Opt" && !strings.HasSuffix(wrapper, ".Opt") && wrapper != "Null" && !strings.HasSuffix(wrapper, ".Null") {
			break
		}
		typ = typ[open+1 : len(typ)-1]
	}
	if typ == "byte" {
		return "uint8"
	}
	if typ == "[]uint8" {
		return "[]byte"
	}
	if strings.HasPrefix(typ, "map[") {
		return "map"
	}
	if strings.HasPrefix(typ, "[]") {
		return "slice"
	}
	if strings.HasPrefix(typ, "[") {
		return "array"
	}
	// UUID packages commonly use aliases (uuid, guuid, googleuuid). The
	// selector's declared name remains the stable evidence.
	if strings.HasSuffix(typ, ".UUID") || typ == "UUID" {
		return "uuid.UUID"
	}
	if strings.HasSuffix(typ, ".Decimal") || typ == "Decimal" {
		return "decimal.Decimal"
	}
	if strings.HasSuffix(typ, ".DeletedAt") {
		return "gorm.DeletedAt"
	}
	return typ
}

func timestampType(raw string) bool {
	switch baseGoType(raw) {
	case "time.Time", "sql.NullTime", "gorm.DeletedAt":
		return true
	default:
		return false
	}
}

func quoteQualified(engine vvdb.Engine, raw string) string {
	parts := strings.Split(raw, ".")
	if len(parts) == 0 || len(parts) > 2 {
		return ""
	}
	for i := range parts {
		parts[i] = quoteIdentifier(engine, parts[i])
		if parts[i] == "" {
			return ""
		}
	}
	return strings.Join(parts, ".")
}

func quoteIdentifier(engine vvdb.Engine, raw string) string {
	if raw == "" {
		return ""
	}
	for i, r := range raw {
		if !(unicode.IsLetter(r) || r == '_' || i > 0 && unicode.IsDigit(r)) {
			return ""
		}
	}
	if engine == vvdb.MySQL || engine == vvdb.MariaDB {
		return "`" + raw + "`"
	}
	return `"` + raw + `"`
}
