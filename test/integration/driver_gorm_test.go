//go:build integration

package integration

import (
	"context"
	"testing"

	gormmysql "gorm.io/driver/mysql"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/shardit-io/qq/adapter/crudsql"
	"github.com/shardit-io/qq/crud"
)

// GormUser is gorm's own view of the same table.
type GormUser struct {
	ID       int64 `gorm:"primaryKey"`
	TenantID int64
	Email    string
	Name     string
	Age      *int
	Active   bool
}

func (GormUser) TableName() string { return "users" }

// gormPGDialector reuses the shared *sql.DB, so gorm and rx-crud pool together.
func gormPGDialector() gorm.Dialector { return gormpg.New(gormpg.Config{Conn: pgDB}) }

func openGorm(t *testing.T, d gorm.Dialector) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(d, &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// The suite runs on the *sql.DB gorm is pooling, so both libraries share one
// connection pool.
func TestGorm(t *testing.T) {
	for _, tc := range []struct {
		name string
		dial gorm.Dialector
		d    crud.Dialect
	}{
		{"gorm+postgres", gormpg.New(gormpg.Config{Conn: pgDB}), crud.Postgres{}},
		{"gorm+mysql", gormmysql.New(gormmysql.Config{Conn: myDB}), crud.MySQL{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openGorm(t, tc.dial)
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			RunSuite(t, Target{Name: tc.name, DB: tc.name, Source: crudsql.Open(sqlDB, tc.d)})
		})
	}
}

// gorm owns the transaction. Inside db.Transaction, gorm swaps Statement.ConnPool
// for the *sql.Tx, and that is exactly what crudsql.From wants.
func TestGormSharedTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	db := openGorm(t, gormpg.New(gormpg.Config{Conn: pgDB}))
	repo := Users.Bind(crudsql.Postgres(pgDB))

	err := db.Transaction(func(tx *gorm.DB) error {
		txCtx := crud.WithExecutor(ctx, crudsql.From(tx.Statement.ConnPool))

		u := User{TenantID: 1, Email: "gorm@x.io", Name: "ByRxCrud", Active: true}
		if err := repo.Save(txCtx, &u); err != nil {
			return err
		}
		// gorm sees the row rx-crud wrote, in the same transaction.
		var got GormUser
		if err := tx.First(&got, u.ID).Error; err != nil {
			return err
		}
		if got.Name != "ByRxCrud" {
			t.Errorf("gorm read back %q", got.Name)
		}
		// And rx-crud sees gorm's write.
		if err := tx.Create(&GormUser{TenantID: 1, Email: "by-gorm@x.io", Name: "ByGorm", Active: true}).Error; err != nil {
			return err
		}
		n, err := repo.Count(txCtx)
		if err != nil {
			return err
		}
		if n != 2 {
			t.Errorf("count inside the transaction = %d, want 2", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	n, err := repo.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("after commit count = %d, want 2", n)
	}
}

// A gorm transaction that rolls back must take rx-crud's writes with it.
func TestGormRollbackTakesRxCrudWithIt(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	db := openGorm(t, gormpg.New(gormpg.Config{Conn: pgDB}))
	repo := Users.Bind(crudsql.Postgres(pgDB))

	boom := errNotNil("rollback please")
	err := db.Transaction(func(tx *gorm.DB) error {
		txCtx := crud.WithExecutor(ctx, crudsql.From(tx.Statement.ConnPool))
		u := User{TenantID: 1, Email: "doomed@x.io", Name: "Doomed"}
		if err := repo.Save(txCtx, &u); err != nil {
			return err
		}
		return boom
	})
	if err != boom {
		t.Fatalf("err = %v", err)
	}
	if n, _ := repo.Count(ctx); n != 0 {
		t.Fatalf("count = %d: the rollback did not reach rx-crud's write", n)
	}
}

type sentinel string

func (s sentinel) Error() string { return string(s) }
func errNotNil(s string) error   { return sentinel(s) }
