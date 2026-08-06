//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"entgo.io/ent/dialect"

	"rx-crud/adapter/crudsql"
	"rx-crud/crud"
	"rx-crud/query"
	"rx-crud/repo/basic"
	"rx-crud/repo/decorators/specs"
	"rx-crud/test/ent"
	entuser "rx-crud/test/ent/user"
	"rx-crud/test/entstore"
)

// EntUserUpdate is generated from ent.User by cmd/rxcrud; test/entstore holds
// the real output and this alias keeps the tests reading naturally.
type EntUserUpdate = entstore.UserUpdate

// ent's *generated* entity is the rx-crud model: no second struct, no tags.
// The embedded config, the selectValues field and the Edges holder are all
// ignored, and the column names fall out of the Go names exactly as ent's own
// naming does.
var EntUsers = basic.Define[ent.User, int64, EntUserUpdate](entuser.Table)

// The mapping self-check every ent project should copy: rx-crud derives column
// names from the Go field names, ent knows the real ones, so compare them and
// find out at build time rather than at runtime.
func TestEntGeneratedStructIsAModel(t *testing.T) {
	s, err := crud.SchemaOf[ent.User]()
	if err != nil {
		t.Fatalf("ent.User should be usable as a model: %v", err)
	}
	got, want := s.Columns(), entuser.Columns
	sort.Strings(got)
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)
	if len(got) != len(sorted) {
		t.Fatalf("rx-crud maps %v, ent has %v", got, sorted)
	}
	for i := range sorted {
		if got[i] != sorted[i] {
			t.Fatalf("rx-crud maps %v, ent has %v", got, sorted)
		}
	}
	if EntUsers.Meta().Table != entuser.Table {
		t.Fatalf("table = %q, ent says %q", EntUsers.Meta().Table, entuser.Table)
	}
	if s.PK.Name != "ID" || !s.PK.Auto {
		t.Fatalf("pk = %+v", s.PK)
	}
	// Age is *int on the ent struct, so rx-crud treats the column as nullable.
	if f := s.Field("Age"); f == nil || crud.ElemType(f.Type).Kind().String() != "int" {
		t.Fatalf("age = %+v", f)
	}
}

// The generated metamodel gives typed, compile-checked filters over ent's own
// entity type.
func TestEntGeneratedMetamodel(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	client := entClient(pgDB, dialect.Postgres)
	users := specs.Executor(EntUsers.Bind(crudsql.Postgres(pgDB)))

	for i, name := range []string{"Ann", "Bob"} {
		if _, err := client.User.Create().
			SetTenantID(1).SetEmail(name + "@x.io").SetName(name).SetAge(30 + i).SetActive(true).
			Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	found, err := users.FindAll(ctx,
		specs.Where(entstore.User_.Age.Gte(31)).And(entstore.User_.Name.StartsWith("B")))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "Bob" {
		t.Fatalf("found %d: %+v", len(found), found)
	}
}

// The full read path over ent's struct: filters, sort, pagination, projection.
func TestEntStructReadsThroughRxCrud(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)

	client := entClient(pgDB, dialect.Postgres)
	users := EntUsers.Bind(crudsql.Postgres(pgDB))

	// Written by ent, read by rx-crud.
	for i, name := range []string{"Ann", "Bob", "Cid"} {
		if _, err := client.User.Create().
			SetTenantID(1).SetEmail(name + "@x.io").SetName(name).SetAge(30 + i).SetActive(i != 2).
			Save(ctx); err != nil {
			t.Fatal(err)
		}
	}

	var req query.Request
	if err := json.Unmarshal([]byte(`{
		"filter": {"active": true, "age": {"gte": 30}},
		"sort":   ["-age"],
		"page":   1, "limit": 10
	}`), &req); err != nil {
		t.Fatal(err)
	}
	opts, err := req.Compile(EntUsers.Meta(), nil)
	if err != nil {
		t.Fatal(err)
	}
	page, err := users.Get(ctx, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("page = %d items, total %d", len(page.Items), page.Total)
	}
	if page.Items[0].Name != "Bob" {
		t.Fatalf("order = %s, %s", page.Items[0].Name, page.Items[1].Name)
	}
	if page.Items[0].Age == nil || *page.Items[0].Age != 31 {
		t.Fatalf("age = %v", page.Items[0].Age)
	}
}

// The write path too: create through rx-crud, read back through ent.
func TestEntStructWritesThroughRxCrud(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)

	client := entClient(pgDB, dialect.Postgres)
	users := EntUsers.Bind(crudsql.Postgres(pgDB))

	// ent's schema puts the created_at default in Go, not in the column, so a
	// write that does not go through ent sets it itself.
	u := ent.User{TenantID: 1, Email: "new@x.io", Name: "New", Active: true, CreatedAt: time.Now()}
	if err := users.Save(ctx, &u); err != nil {
		t.Fatal(err)
	}
	if u.ID == 0 {
		t.Fatal("the generated key was not read back")
	}

	back, err := client.User.Query().Where(entuser.IDEQ(u.ID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != "New" || back.Age != nil {
		t.Fatalf("ent read back %+v", back)
	}

	// A partial update, with the three-state nullable column.
	name := "Renamed"
	got, err := users.Update(ctx, u.ID, EntUserUpdate{Name: &name, Age: crud.Set(41)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" || got.Age == nil || *got.Age != 41 {
		t.Fatalf("updated = %+v", got)
	}
	if got.Email != "new@x.io" {
		t.Fatalf("an absent DTO field changed the row: %q", got.Email)
	}

	got, err = users.Update(ctx, u.ID, EntUserUpdate{Age: crud.Null[int]()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Age != nil {
		t.Fatalf("an explicit null should clear the column, got %v", *got.Age)
	}
}

// The point of it all: one ent transaction, ent's builders and rx-crud's
// repository writing into it, over ent's own struct.
func TestEntStructInsideEntTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)

	client := entClient(pgDB, dialect.Postgres)
	users := EntUsers.Bind(crudsql.Postgres(pgDB))

	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	txCtx := crud.WithExecutor(ctx, crudsql.From(tx))

	byEnt, err := tx.User.Create().
		SetTenantID(1).SetEmail("ent@x.io").SetName("ByEnt").SetActive(true).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := "PatchedByRxCrud"
	if _, err := users.Update(txCtx, byEnt.ID, EntUserUpdate{Name: &name}); err != nil {
		t.Fatal(err)
	}
	// ent sees rx-crud's update inside the same transaction.
	seen, err := tx.User.Get(ctx, byEnt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if seen.Name != "PatchedByRxCrud" {
		t.Fatalf("ent read back %q", seen.Name)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n, err := users.Count(ctx); err != nil || n != 0 {
		t.Fatalf("count = %d err = %v: the rollback did not reach rx-crud's write", n, err)
	}
}
