package sqlrepo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
)

func TestAFilteredWriteRefusesTheOptionsItWouldNotApply(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		option crud.Option
		blame  string
	}{
		{"a limit", crud.Limit(2), "Limit"},
		{"a page", crud.Page(3), "Page"},
		{"an offset", crud.Offset(10), "Offset"},
		{"a cursor", crud.After("token"), "After"},
		{"an order", crud.OrderBy(crud.Asc("Email")), "OrderBy"},
		{"a projection", crud.Select("Name"), "Select"},
		{"a preload", crud.Preload("Owner"), "Preload"},
	} {
		t.Run(tc.name+" on UpdateAll", func(t *testing.T) {
			rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 99})

			n, err := Users.Bind(rec).UpdateAll(ctx, UserUpdate{Name: ptr("Renamed")},
				crud.Where(crud.Eq("TenantID", int64(7))), tc.option)

			assertRefused(t, err, tc.blame)
			if n != 0 || len(rec.Statements()) != 0 {
				t.Fatalf("n = %d and %d statements ran: every matching row was written for a caller who asked for %s", n, len(rec.Statements()), tc.name)
			}
		})

		t.Run(tc.name+" on DeleteAll", func(t *testing.T) {
			rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 99})

			n, err := Users.Bind(rec).DeleteAll(ctx, crud.Where(crud.Eq("TenantID", int64(7))), tc.option)

			assertRefused(t, err, tc.blame)
			if n != 0 || len(rec.Statements()) != 0 {
				t.Fatalf("n = %d and %d statements ran: every matching row was deleted for a caller who asked for %s", n, len(rec.Statements()), tc.name)
			}
		})

		t.Run(tc.name+" on Update", func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows(userRow(1, "a@b.c", "Ann", 30, 7)))

			_, err := Users.Bind(rec).Update(ctx, 1, UserUpdate{Name: ptr("Renamed")}, tc.option)

			assertRefused(t, err, tc.blame)
			if len(rec.Statements()) != 0 {
				t.Fatalf("the mutation read ran anyway: %v", rec.SQL())
			}
		})
	}

	t.Run("a filter and a lock are still honoured", func(t *testing.T) {
		rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 3})
		if _, err := Users.Bind(rec).UpdateAll(ctx, UserUpdate{Name: ptr("Renamed")},
			crud.Where(crud.Eq("TenantID", int64(7)))); err != nil {
			t.Fatalf("a narrowed write was refused: %v", err)
		}

		rec = crudtest.Postgres().Push(crudtest.Rows(userRow(1, "a@b.c", "Ann", 30, 7)))
		if _, err := Users.Bind(rec).Update(ctx, 1, UserUpdate{Name: ptr("Ann")},
			crud.Where(crud.Eq("TenantID", 7)), crud.ForUpdate()); err != nil {
			t.Fatalf("a narrowed, locking update was refused: %v", err)
		}
	})
}

func TestAnAggregateRefusesTheOptionsItWouldNotApply(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		option crud.Option
		blame  string
	}{
		{"a cursor", crud.After("token"), "After"},
		{"a projection", crud.Select("Name"), "Select"},
		{"a preload", crud.Preload("Owner"), "Preload"},
		{"a row lock", crud.ForUpdate(), "ForUpdate"},
		{"DISTINCT", crud.Distinct(), "Distinct"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(2)}))

			_, err := Users.Bind(rec).Aggregate(ctx, crud.Aggregate(crud.CountAll("n")), tc.option)

			assertRefused(t, err, tc.blame)
			if len(rec.Statements()) != 0 {
				t.Fatalf("the aggregate ran and answered as if %s had been applied: %v", tc.name, rec.SQL())
			}
		})
	}

	t.Run("grouping, sorting and paging are still honoured", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(7), int64(2)}))
		if _, err := Users.Bind(rec).Aggregate(ctx,
			crud.Aggregate(crud.CountAll("n")), crud.GroupBy("TenantID"),
			crud.OrderBy(crud.Asc("TenantID")), crud.Limit(10)); err != nil {
			t.Fatalf("an aggregate the repository can answer was refused: %v", err)
		}
	})
}

func TestAReadRefusesAnAggregateItCannotAnswer(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(1, "a@b.c", "Ann", 30, 7)))

	_, err := Users.Bind(rec).GetAll(context.Background(), crud.Aggregate(crud.CountAll("n")))

	assertRefused(t, err, "Aggregate")
	if len(rec.Statements()) != 0 {
		t.Fatalf("the read ran and answered with rows, not the aggregation that was asked for: %v", rec.SQL())
	}
}

func assertRefused(t *testing.T, err error, blame string) {
	t.Helper()
	var schema *crud.SchemaError
	if err == nil {
		t.Fatalf("crud.%s was accepted and dropped", blame)
	}
	if !errors.As(err, &schema) {
		t.Fatalf("err = %T (%v), want the schema error a transport turns into a 400", err, err)
	}
	if schema.Field != blame {
		t.Fatalf("the refusal blames %q; the caller wrote crud.%s", schema.Field, blame)
	}
}
