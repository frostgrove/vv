package crudsqlfx_test

import (
	"database/sql"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql/crudsqlfx"
	"github.com/frostgrove/vv/utils/vvdb"
)

func TestTheSourceAndThePoolAreBothResolvable(t *testing.T) {
	err := fx.ValidateApp(
		crudsqlfx.Module(&vvdb.Config{Engine: vvdb.SQLite, Path: ":memory:"}),
		fx.Invoke(func(*sql.DB, crud.Source) {}),
	)
	if err != nil {
		t.Fatalf("the database graph is incomplete: %v", err)
	}
}

func TestSomethingThisModuleDoesNotProvideIsRefused(t *testing.T) {
	err := fx.ValidateApp(
		crudsqlfx.Module(&vvdb.Config{Engine: vvdb.SQLite, Path: ":memory:"}),
		fx.Invoke(func(*sql.Tx) {}),
	)
	if err == nil {
		t.Fatal("a graph resolved a *sql.Tx nobody provides, so the check above proves nothing")
	}
}
