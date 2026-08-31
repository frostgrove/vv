//go:build integration

package integration

import (
	"context"
	"testing"

	gormmysql "gorm.io/driver/mysql"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

type GormUser struct {
	ID       int64 `gorm:"primaryKey"`
	TenantID int64
	Email    string
	Name     string
	Age      *int
	Active   bool
}

func (GormUser) TableName() string { return "users" }

func gormPGDialector() gorm.Dialector { return gormpg.New(gormpg.Config{Conn: pgDB}) }

func openGorm(t *testing.T, d gorm.Dialector) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(d, &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	return database
}

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
			database := openGorm(t, tc.dial)
			sqlDB, err := database.DB()
			if err != nil {
				t.Fatal(err)
			}
			RunSuite(t, Target{Name: tc.name, DB: tc.name, Source: crudsql.Open(sqlDB, tc.d)})
		})
	}
}

func TestGormSharedTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	database := openGorm(t, gormpg.New(gormpg.Config{Conn: pgDB}))
	source := crudsql.Postgres(pgDB)
	repository := Users.Bind(source)

	err := database.Transaction(func(tx *gorm.DB) error {
		txCtx := source.BindExecutor(ctx, tx.Statement.ConnPool)

		u := User{TenantID: 1, Email: "gorm@x.io", Name: "ByVV", Active: true}
		if stored, err := repository.Save(txCtx, &u); err != nil {
			return err
		} else {
			u = stored
		}

		var got GormUser
		if err := tx.First(&got, u.ID).Error; err != nil {
			return err
		}
		if got.Name != "ByVV" {
			t.Errorf("gorm read back %q", got.Name)
		}

		if err := tx.Create(&GormUser{TenantID: 1, Email: "by-gorm@x.io", Name: "ByGorm", Active: true}).Error; err != nil {
			return err
		}
		n, err := repository.Count(txCtx)
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

	n, err := repository.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("after commit count = %d, want 2", n)
	}
}

func TestGormRollbackTakesVVWithIt(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	database := openGorm(t, gormpg.New(gormpg.Config{Conn: pgDB}))
	source := crudsql.Postgres(pgDB)
	repository := Users.Bind(source)

	boom := errNotNil("rollback please")
	err := database.Transaction(func(tx *gorm.DB) error {
		txCtx := source.BindExecutor(ctx, tx.Statement.ConnPool)
		u := User{TenantID: 1, Email: "doomed@x.io", Name: "Doomed"}
		if _, err := repository.Save(txCtx, &u); err != nil {
			return err
		}
		return boom
	})
	if err != boom {
		t.Fatalf("err = %v", err)
	}
	if n, _ := repository.Count(ctx); n != 0 {
		t.Fatalf("count = %d: the rollback did not reach vv's write", n)
	}
}

type sentinel string

func (this sentinel) Error() string { return string(this) }
func errNotNil(s string) error      { return sentinel(s) }
