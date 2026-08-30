package crud_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/utils"
)

type cursorShape struct {
	ID       int64 `db:"id,pk,noauto"`
	Optional utils.Opt[int]
	Pointer  *int
	SQLNull  sql.NullString
	Text     string
}

func TestCursorCapabilityRefusesEveryKnownNullableShape(t *testing.T) {
	m, err := crud.NewMeta[cursorShape]("cursor_shapes")
	if err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{"Optional", "Pointer", "SQLNull"} {
		t.Run(field, func(t *testing.T) {
			token, err := crud.EncodeCursor([]string{field, "ID"}, []any{nil, int64(1)})
			if err != nil {
				t.Fatal(err)
			}
			_, err = crud.CursorPredicate(m,
				[]crud.Order{crud.Asc(field), crud.Asc("ID")}, token, false)
			var schemaErr *crud.SchemaError
			if !errors.As(err, &schemaErr) {
				t.Fatalf("err = %T %v, want nullable cursor SchemaError", err, err)
			}
			if crud.CursorFieldSupported(m.Field(field)) {
				t.Fatal("nullable field was advertised as cursor-supported")
			}
		})
	}

	if !crud.CursorFieldSupported(m.Field("Text")) {
		t.Fatal("ordinary non-null text field lost cursor support")
	}
}
