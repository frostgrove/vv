package crudsql

import (
	"context"
	"database/sql"
	"testing"

	"github.com/frostgrove/vv/crud"
)

func TestTransactionOptionsAreSnapshotted(t *testing.T) {
	original := &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true}
	db := Open(nil, crud.Postgres{}).WithTxOptions(original)

	original.Isolation = sql.LevelReadCommitted
	original.ReadOnly = false
	if db.txOptions == original {
		t.Fatal("WithTxOptions retained the caller-owned pointer")
	}
	if db.txOptions == nil || db.txOptions.Isolation != sql.LevelSerializable || !db.txOptions.ReadOnly {
		t.Fatalf("transaction options changed after construction: %+v", db.txOptions)
	}

	changed := db.WithTxOptions(&sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if db.txOptions.Isolation != sql.LevelSerializable || changed.txOptions.Isolation != sql.LevelReadCommitted {
		t.Fatal("configuring a copied source mutated the source it was copied from")
	}
}

func TestTransactionForExtractsOnlyAmbientSQLTransactions(t *testing.T) {
	raw := &sql.Tx{}
	source := Open(&sql.DB{}, crud.Postgres{})
	ctx := source.BindExecutor(context.Background(), raw, WithTransaction())
	got, ok := TransactionFor(ctx, source)
	if !ok || got != raw {
		t.Fatalf("ambient transaction = (%p, %t)", got, ok)
	}
	if got, ok := Transaction(source); ok || got != nil {
		t.Fatalf("database pool reported as transaction = (%p, %t)", got, ok)
	}
}

func TestNilTransactionOptionsSelectTheDriverDefault(t *testing.T) {
	db := Open(nil, crud.Postgres{}).WithTxOptions(nil)
	if db.txOptions != nil {
		t.Fatalf("nil options became %+v, want the database/sql default", db.txOptions)
	}
}
