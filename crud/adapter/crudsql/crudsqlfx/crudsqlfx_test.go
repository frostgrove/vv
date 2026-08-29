package crudsqlfx_test

import (
	"database/sql"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql/crudsqlfx"
	"github.com/frostgrove/vv/utils/vvdb"
)

// fx.ValidateApp resolves the graph without opening anything, which is the half
// of this module that can be checked without a server. What happens once the
// constructors run — the ping, the schema read — is the integration suite's.

func TestTheSourceAndThePoolAreBothResolvable(t *testing.T) {
	err := fx.ValidateApp(
		crudsqlfx.Module(&vvdb.Config{Engine: vvdb.SQLite, Path: ":memory:"}),
		fx.Invoke(func(*sql.DB, crud.Source) {}),
	)
	if err != nil {
		t.Fatalf("the database graph is incomplete: %v", err)
	}
}

// The control on the test above: a component this module does not provide has to
// fail validation, or the check proves nothing.
func TestSomethingThisModuleDoesNotProvideIsRefused(t *testing.T) {
	err := fx.ValidateApp(
		crudsqlfx.Module(&vvdb.Config{Engine: vvdb.SQLite, Path: ":memory:"}),
		fx.Invoke(func(*sql.Tx) {}),
	)
	if err == nil {
		t.Fatal("a graph resolved a *sql.Tx nobody provides, so the check above proves nothing")
	}
}
