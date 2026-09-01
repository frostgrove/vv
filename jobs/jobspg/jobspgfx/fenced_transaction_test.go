package jobspgfx_test

import (
	"database/sql"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobspg/jobspgfx"
)

func TestModulePublishesFencedTransactions(t *testing.T) {
	database := &sql.DB{}
	source := crudsql.Postgres(database)
	catalog := jobs.MustCatalog(testDefinition(t, "jobspgfx.fenced-transactions"))
	settings := jobspgfx.Settings{Namespace: testNamespace(t, "fenced-transactions")}
	var transactions jobs.FencedTransactions
	app := fx.New(
		fx.NopLogger,
		fx.Supply(database, catalog),
		fx.Provide(func() crud.Source { return source }),
		jobspgfx.Module(settings),
		fx.Populate(&transactions),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	if transactions == nil {
		t.Fatal("fenced transactions were not published")
	}
}
