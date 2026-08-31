package crud_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

type batchFacadeModel struct {
	ID   int64  `db:"id,pk,auto"`
	Name string `db:"name"`
}

type batchFacadeUpdate struct{ Name *string }

var batchFacadeRows = sqlrepo.Define[batchFacadeModel, int64, batchFacadeUpdate]("batch_facade_rows")

type opaqueBatchCore[M any, ID comparable] struct{ crud.Core[M, ID] }

func TestUnknownRepositoryDecoratorFailsBatchInsertionClosed(t *testing.T) {
	recorder := crudtest.Postgres()
	plain := batchFacadeRows.Bind(recorder)
	opaque := crud.Wrap[batchFacadeModel, int64, batchFacadeUpdate](
		opaqueBatchCore[batchFacadeModel, int64]{Core: plain.Unwrap()})

	err := opaque.InsertBatch(context.Background(), []*batchFacadeModel{{Name: "must not pass through"}})
	if !errors.Is(err, crud.ErrNoBatchInsertSupport) {
		t.Fatalf("err = %v, want ErrNoBatchInsertSupport", err)
	}
	if len(recorder.Statements()) != 0 {
		t.Fatalf("opaque decorator was bypassed: %v", recorder.SQL())
	}
}

func TestEmptyInsertBatchIsAUniversalNoopWithoutACapability(t *testing.T) {
	recorder := crudtest.Postgres()
	plain := batchFacadeRows.Bind(recorder)
	opaque := crud.Wrap[batchFacadeModel, int64, batchFacadeUpdate](
		opaqueBatchCore[batchFacadeModel, int64]{Core: plain.Unwrap()})

	if err := opaque.InsertBatch(context.Background(), nil); err != nil {
		t.Fatalf("empty batch returned %v", err)
	}
	if len(recorder.Statements()) != 0 {
		t.Fatalf("empty batch reached storage: %v", recorder.SQL())
	}
}

func TestPortableBatchIsMonotonicAcrossComposedOptions(t *testing.T) {
	if !crud.UsesPortableBatch(crud.BatchOption{}, crud.PortableBatch(), crud.BatchOption{}) {
		t.Fatal("a zero-value option cancelled PortableBatch")
	}
	if crud.UsesPortableBatch(crud.BatchOption{}) {
		t.Fatal("the zero-value BatchOption enabled portable mode")
	}
}
