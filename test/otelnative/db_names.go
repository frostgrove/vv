package otelnative

import (
	"errors"
	"strings"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
)

var ErrInvalidDatabasePoolName = errors.New("otelnative: database pool name must be a configured identifier")

const (
	pgxScopeName      = "github.com/exaring/otelpgx"
	sqlScopeName      = "github.com/XSAM/otelsql"
	pgxPoolScopeName  = "github.com/frostgrove/vv/test/otelnative/pgxpool"
	pgxScopeVersion   = "v0.11.1"
	pgxPoolVersion    = "0.1.0"
	databaseOperation = "db.operation"
	databasePoolKey   = "db.client.connection.pool.name"
)

type DatabasePoolName struct {
	value string
}

func NewDatabasePoolName(value string) (DatabasePoolName, error) {
	if !validDatabasePoolName(value) {
		return DatabasePoolName{}, ErrInvalidDatabasePoolName
	}
	return DatabasePoolName{value: value}, nil
}

func (n DatabasePoolName) String() string {
	return n.value
}

func validDatabasePoolName(value string) bool {
	if len(value) == 0 || len(value) > 63 {
		return false
	}
	for index, symbol := range []byte(value) {
		if symbol >= 'a' && symbol <= 'z' || symbol >= 'A' && symbol <= 'Z' || symbol >= '0' && symbol <= '9' {
			continue
		}
		if index > 0 && (symbol == '.' || symbol == '_' || symbol == '-') {
			continue
		}
		return false
	}
	return true
}

func normalizePGXSpanName(name string) string {
	switch name {
	case "query statement", "batch query statement", "db.query":
		return "db.query"
	case "prepare statement", "db.prepare":
		return "db.prepare"
	case "batch start", "db.batch":
		return "db.batch"
	case "connect", "db.connect":
		return "db.connect"
	case "pool.acquire", "db.acquire":
		return "db.acquire"
	case "db.copy":
		return "db.copy"
	case databaseOperation:
		return databaseOperation
	}
	if strings.HasPrefix(name, "copy_from ") {
		return "db.copy"
	}
	return databaseOperation
}

func normalizeSQLSpanName(method otelsql.Method) string {
	switch method {
	case otelsql.MethodConnectorConnect:
		return "db.connect"
	case otelsql.MethodConnPing:
		return "db.ping"
	case otelsql.MethodConnExec, otelsql.MethodStmtExec:
		return "db.exec"
	case otelsql.MethodConnQuery, otelsql.MethodStmtQuery:
		return "db.query"
	case otelsql.MethodConnPrepare:
		return "db.prepare"
	case otelsql.MethodConnBeginTx:
		return "db.begin"
	case otelsql.MethodConnResetSession:
		return "db.reset"
	case otelsql.MethodTxCommit:
		return "db.commit"
	case otelsql.MethodTxRollback:
		return "db.rollback"
	case otelsql.MethodRows:
		return "db.rows"
	default:
		return databaseOperation
	}
}

func databaseScope(scope instrumentation.Scope) (instrumentation.Scope, bool) {
	var expectedVersion string
	switch scope.Name {
	case pgxScopeName:
		expectedVersion = pgxScopeVersion
	case sqlScopeName:
		expectedVersion = otelsql.Version()
	case pgxPoolScopeName:
		expectedVersion = pgxPoolVersion
	default:
		return instrumentation.Scope{}, false
	}
	if !databaseScopeVersionAllowed(scope.Name, scope.Version, expectedVersion) || scope.SchemaURL != "" {
		return instrumentation.Scope{}, true
	}
	return instrumentation.Scope{
		Name:       scope.Name,
		Version:    expectedVersion,
		Attributes: attribute.NewSet(),
	}, true
}

func databaseScopeVersionAllowed(scope, actual, expected string) bool {
	return actual == expected || scope == pgxScopeName && actual == "unknown"
}
