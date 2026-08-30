//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"entgo.io/ent/dialect"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/test/ent"
	entuser "github.com/frostgrove/vv/test/ent/user"
	"github.com/frostgrove/vv/test/entstore"
)

// EntUserUpdate is generated from ent.User by cmd/vv; test/entstore holds
// the real output and this alias keeps the tests reading naturally.
type EntUserUpdate = entstore.UserUpdate

// ent's *generated* entity is the vv model: no second struct, no tags.
// The embedded config, the selectValues field and the Edges holder are all
// ignored, and the column names fall out of the Go names exactly as ent's own
// naming does.
var EntUsers = sqlrepo.Define[ent.User, int64, EntUserUpdate](entuser.Table)

// The mapping self-check every ent project should copy: vv derives column
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
		t.Fatalf("github.com/frostgrove/vv maps %v, ent has %v", got, sorted)
	}
	for i := range sorted {
		if got[i] != sorted[i] {
			t.Fatalf("github.com/frostgrove/vv maps %v, ent has %v", got, sorted)
		}
	}
	if EntUsers.Meta().Table != entuser.Table {
		t.Fatalf("table = %q, ent says %q", EntUsers.Meta().Table, entuser.Table)
	}
	if s.PK.Name != "ID" || !s.PK.Auto {
		t.Fatalf("pk = %+v", s.PK)
	}
	// Age is *int on the ent struct, so vv treats the column as nullable.
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
func TestEntStructReadsThroughVV(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)

	client := entClient(pgDB, dialect.Postgres)
	source := crudsql.Postgres(pgDB)
	users := EntUsers.Bind(source)

	// Written by ent, read by vv.
	for i, name := range []string{"Ann", "Bob", "Cid"} {
		if _, err := client.User.Create().
			SetTenantID(1).SetEmail(name + "@x.io").SetName(name).SetAge(30 + i).SetActive(i != 2).
			Save(ctx); err != nil {
			t.Fatal(err)
		}
	}

	var request query.Request
	if err := json.Unmarshal([]byte(`{
		"filter": {"active": true, "age": {"gte": 30}},
		"sort":   ["-age"],
		"page":   1, "limit": 10
	}`), &request); err != nil {
		t.Fatal(err)
	}
	options, err := request.Compile(EntUsers.Meta(), unpagedOK)
	if err != nil {
		t.Fatal(err)
	}
	page, err := users.Get(ctx, options...)
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

// The write path too: create through vv, read back through ent.
func TestEntStructWritesThroughVV(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)

	client := entClient(pgDB, dialect.Postgres)
	source := crudsql.Postgres(pgDB)
	users := EntUsers.Bind(source)

	// ent's schema puts the created_at default in Go, not in the column, so a
	// write that does not go through ent sets it itself.
	u := ent.User{TenantID: 1, Email: "new@x.io", Name: "New", Active: true, CreatedAt: time.Now()}
	if stored, err := users.Save(ctx, &u); err != nil {
		t.Fatal(err)
	} else {
		u = stored
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

// The point of it all: one ent transaction, ent's builders and vv's
// repository writing into it, over ent's own struct.
func TestEntStructInsideEntTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)

	client := entClient(pgDB, dialect.Postgres)
	source := crudsql.Postgres(pgDB)
	users := EntUsers.Bind(source)

	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	txCtx := source.BindExecutor(ctx, tx)

	byEnt, err := tx.User.Create().
		SetTenantID(1).SetEmail("ent@x.io").SetName("ByEnt").SetActive(true).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := "PatchedByVV"
	if _, err := users.Update(txCtx, byEnt.ID, EntUserUpdate{Name: &name}); err != nil {
		t.Fatal(err)
	}
	// ent sees vv's update inside the same transaction.
	seen, err := tx.User.Get(ctx, byEnt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if seen.Name != "PatchedByVV" {
		t.Fatalf("ent read back %q", seen.Name)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n, err := users.Count(ctx); err != nil || n != 0 {
		t.Fatalf("count = %d err = %v: the rollback did not reach vv's write", n, err)
	}
}

// docs/ent.md §16 warns that ent's Go-side defaults belong to ent's builders:
// a write that goes through vv is one INSERT and never sees them, so the
// column gets whatever the model carries. matrix_test.go works around it in a
// comment; this is the claim itself, from both sides of the same schema.
//
// `field.Bool("active").Default(true)` is the cleanest probe: ent fills it in,
// vv does not, and the difference is visible in the row.
func TestEntsGoSideDefaultsDoNotApplyToVVWrites(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	client := entClient(pgDB, dialect.Postgres)
	users := EntUsers.Bind(crudsql.Postgres(pgDB))

	// ent's own builder applies the default.
	byEnt, err := client.User.Create().
		SetTenantID(1).SetEmail("ent@x.io").SetName("ByEnt").SetCreatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !byEnt.Active {
		t.Fatal("ent did not apply its own default, so this test cannot tell the two paths apart")
	}

	// vv writes the model, and the model says false.
	byVV := ent.User{TenantID: 1, Email: "rx@x.io", Name: "ByVV", CreatedAt: time.Now()}
	if stored, err := users.Save(ctx, &byVV); err != nil {
		t.Fatal(err)
	} else {
		byVV = stored
	}
	if byVV.Active {
		t.Fatal("an ent Go-side default reached an vv write")
	}
	// From ent's side of the same row, so this is the stored value and not
	// something vv failed to read back.
	stored, err := client.User.Get(ctx, byVV.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Active {
		t.Fatalf("the stored row has active = true; put invariants in the column, in a security.Policy or in a service layer")
	}
}
