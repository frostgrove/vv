package jobspgfx_test

import (
	"database/sql"
	"testing"

	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobspg"
	"github.com/frostgrove/vv/jobs/jobspg/jobspgfx"
)

func TestSchemaManagementFlowsThroughModuleSettings(t *testing.T) {
	database := &sql.DB{}
	namespace, err := jobs.NamespaceOf("schema-management", "test")
	if err != nil {
		t.Fatal(err)
	}
	catalog := jobs.MustCatalog(testDefinition(t, "jobspgfx.schema-management"))
	driver, err := jobspgfx.New(jobspgfx.Settings{Namespace: namespace, SchemaManagement: jobspg.VerifySchema}, database, crudsql.Postgres(database), catalog)
	if err != nil {
		t.Fatal(err)
	}
	if driver.SchemaManagement() != jobspg.VerifySchema {
		t.Fatalf("schema management = %v", driver.SchemaManagement())
	}
}
