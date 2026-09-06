package crud_test

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

type unscopedRow struct {
	ID       int64  `db:"id,pk,auto"`
	TenantID int64  `db:"tenant_id"`
	Name     string `db:"name"`
}

type unscopedRowUpdate struct{ Name *string }

var unscopedRows = sqlrepo.Define[unscopedRow, int64, unscopedRowUpdate]("unscoped_rows")

type opaqueUnscopedCore struct{ crud.Core[unscopedRow, int64] }

type declaredUnscopedCore struct{ crud.Base[unscopedRow, int64] }

func TestUnscopedExistenceIsAnsweredOnlyByTheExactOuterCore(t *testing.T) {
	ctx := context.Background()
	recorder := crudtest.Postgres()
	repository := unscopedRows.Bind(recorder).Unwrap()

	recorder.Push(crudtest.Rows([]any{int64(1)}))
	found, err, supported := crud.ExistsUnscopedOf(repository, ctx)
	if !supported {
		t.Fatal("the repository itself does not answer the unscoped question, so nothing below proves anything")
	}
	if err != nil || !found {
		t.Fatalf("the repository answered found=%v err=%v for a row it was handed", found, err)
	}

	for name, core := range map[string]crud.Core[unscopedRow, int64]{
		"a decorator that says nothing about what it wraps": opaqueUnscopedCore{Core: repository},
		"a decorator built on crud.Base":                    declaredUnscopedCore{crud.Base[unscopedRow, int64]{Core: repository}},
	} {
		t.Run(name, func(t *testing.T) {
			recorder.Reset()
			recorder.Push(crudtest.Rows([]any{int64(1)}))

			found, err, supported := crud.ExistsUnscopedOf(core, ctx)
			if supported {
				t.Fatalf("the walk reached an executable effect underneath a wrapper that never preserved it (found=%v err=%v)", found, err)
			}
			if len(recorder.Statements()) != 0 {
				t.Fatalf("a statement ran for a capability the outer core does not have: %v", recorder.SQL())
			}
		})
	}
}
