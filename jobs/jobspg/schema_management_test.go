package jobspg

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/frostgrove/vv/jobs"
)

func TestSchemaManagementDefaultsToManagedAndRejectsUnknown(t *testing.T) {
	namespace, catalog, _ := testPlacement(t)
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if driver.SchemaManagement() != ManageSchema {
		t.Fatalf("default schema management = %v", driver.SchemaManagement())
	}
	driver, err = New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog, SchemaManagement: VerifySchema})
	if err != nil {
		t.Fatal(err)
	}
	if driver.SchemaManagement() != VerifySchema {
		t.Fatalf("explicit schema management = %v", driver.SchemaManagement())
	}
	_, err = New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog, SchemaManagement: SchemaManagement(2)})
	if !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("invalid schema management = %v", err)
	}
}
