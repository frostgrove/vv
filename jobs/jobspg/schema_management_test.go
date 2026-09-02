package jobspg

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/frostgrove/vv/jobs"
)

func TestAnUnsetSchemaManagementVerifiesRatherThanMigrates(t *testing.T) {
	namespace, catalog, _ := testPlacement(t)
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if driver.SchemaManagement() != VerifySchema {
		t.Fatalf("a driver built without a schema management choice migrates: %v", driver.SchemaManagement())
	}
	var absent *Driver
	if absent.SchemaManagement() != UnsetSchemaManagement {
		t.Fatalf("a driver that does not exist claims a schema management choice: %v", absent.SchemaManagement())
	}
}

func TestAnExplicitSchemaManagementIsKeptAndAnUnknownOneIsRefused(t *testing.T) {
	namespace, catalog, _ := testPlacement(t)
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog, SchemaManagement: ManageSchema})
	if err != nil {
		t.Fatal(err)
	}
	if driver.SchemaManagement() != ManageSchema {
		t.Fatalf("explicit schema management = %v", driver.SchemaManagement())
	}
	if _, err = New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog, SchemaManagement: SchemaManagement(9)}); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("an unknown schema management was accepted: %v", err)
	}
}
