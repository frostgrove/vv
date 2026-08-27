package modelscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSource(t *testing.T, root, name, source string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	source = strings.ReplaceAll(source, "@", "`")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func modelByName(t *testing.T, models []Model, name string) Model {
	t.Helper()
	for _, model := range models {
		if model.Name == name {
			return model
		}
	}
	t.Fatalf("no model named %s in %#v", name, models)
	return Model{}
}

func fieldByName(t *testing.T, model Model, name string) Field {
	t.Helper()
	for _, field := range model.Fields {
		if field.Name == name {
			return field
		}
	}
	t.Fatalf("no field named %s in %#v", name, model.Fields)
	return Field{}
}

func TestDiscoverFindsConventionalAndTaggedModels(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "src/user.model.go", `package user

import (
	"time"
	"github.com/frostgrove/vv/crud"
)

type user struct {
	ID        int64          @db:"id,pk,auto"@
	Email     string         @db:"email,immutable"@
	Alias     *string        @db:"alias"@
	SeenAt    crud.Opt[time.Time] @db:"seen_at"@
	Version   int            @db:"version,version"@
	Secret    string         @db:"-"@
	Manager   *user          @rel:"belongs_to"@
	private   string
}
`)
	writeSource(t, root, "src/audit.go", `package user

type Audit struct {
	ID int64 @db:"audit_id,noauto"@
	Body string
}
`)
	writeSource(t, root, "src/plain.go", `package user

type Helper struct { Value string }
`)

	models, err := Discover(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %#v, want user and Audit only", models)
	}

	user := modelByName(t, models, "user")
	if user.Table != "users" || user.ExplicitTable || !user.Tagged {
		t.Fatalf("user metadata = %+v", user)
	}
	if len(user.Fields) != 5 {
		t.Fatalf("user fields = %#v, want five mapped columns", user.Fields)
	}
	id := fieldByName(t, user, "ID")
	if !id.PrimaryKey || !id.Auto || id.Column != "id" {
		t.Fatalf("ID = %+v, want auto primary key", id)
	}
	if email := fieldByName(t, user, "Email"); !email.Immutable {
		t.Fatalf("Email = %+v, want immutable", email)
	}
	if alias := fieldByName(t, user, "Alias"); !alias.Nullable {
		t.Fatalf("Alias = %+v, want nullable pointer", alias)
	}
	if seen := fieldByName(t, user, "SeenAt"); !seen.Nullable || seen.GoType != "crud.Opt[time.Time]" {
		t.Fatalf("SeenAt = %+v, want nullable Opt", seen)
	}
	if version := fieldByName(t, user, "Version"); !version.Version {
		t.Fatalf("Version = %+v, want version marker", version)
	}

	audit := modelByName(t, models, "Audit")
	auditID := fieldByName(t, audit, "ID")
	if !auditID.PrimaryKey || auditID.Auto || !auditID.NoAuto || auditID.Column != "audit_id" {
		t.Fatalf("Audit.ID = %+v, want ID fallback with noauto", auditID)
	}
}

func TestDiscoverUsesConstantTableNameFromAnOrdinaryFile(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "entity.go", `package account

const tablePrefix = "auth_"
const usersTable = tablePrefix + "users"

type Account struct {
	ID int64
	Name string
}

func (*Account) TableName() string { return usersTable }

type NotAModel struct { ID int64 }
`)

	models, err := Discover(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %#v, want only Account", models)
	}
	account := models[0]
	if account.Name != "Account" || account.Table != "auth_users" || !account.ExplicitTable {
		t.Fatalf("Account = %+v", account)
	}
	if id := fieldByName(t, account, "ID"); !id.PrimaryKey || !id.Auto {
		t.Fatalf("conventional ID = %+v, want auto primary key", id)
	}
}

func TestDiscoverUnderstandsGormEvidenceAndEmbedding(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "product.go", `package product

import g "gorm.io/gorm"

type Product struct {
	g.Model
	SKU string @gorm:"column:sku_code;primaryKey;autoIncrement:false"@
	DisplayName string @gorm:"column:display_name"@
	Ignored string @gorm:"-"@
}
`)

	models, err := Discover(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	product := modelByName(t, models, "Product")
	if len(product.Fields) != 6 {
		t.Fatalf("gorm fields = %#v, want four embedded fields and two declared fields", product.Fields)
	}
	if deleted := fieldByName(t, product, "DeletedAt"); !deleted.Nullable {
		t.Fatalf("DeletedAt = %+v, want nullable", deleted)
	}
	sku := fieldByName(t, product, "SKU")
	if sku.Column != "sku_code" || !sku.PrimaryKey || sku.Auto || !sku.NoAuto {
		t.Fatalf("SKU = %+v, want natural gorm primary key", sku)
	}
}

func TestDiscoverFlattensLocalEmbeddedStructs(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "model.go", `package people

type Timestamps struct {
	CreatedAt int64 @db:"created_at,generated"@
}

type Person struct {
	ID int64
	Timestamps
}
`)

	models, err := Discover(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	person := modelByName(t, models, "Person")
	if len(person.Fields) != 2 || !fieldByName(t, person, "CreatedAt").Generated {
		t.Fatalf("Person fields = %#v, want flattened timestamps", person.Fields)
	}
}

func TestDiscoverSkipsTestsGeneratedFilesAndExcludedTrees(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "ok.model.go", "package app\ntype Kept struct { ID int64 }\n")
	writeSource(t, root, "model_test.go", "package app\ntype TestModel struct { ID int64 }\n")
	writeSource(t, root, "other_gen.go", "package app\ntype GeneratedByName struct { ID int64 `db:\"id\"` }\n")
	writeSource(t, root, "generated.model.go", "// Code generated by a tool. DO NOT EDIT.\npackage app\ntype GeneratedByHeader struct { ID int64 }\n")
	for _, dir := range []string{"vendor", ".git", "migrations", "test", "generated"} {
		writeSource(t, root, filepath.Join(dir, "hidden.model.go"), "package hidden\ntype Hidden struct { ID int64 }\n")
	}

	models, err := Discover(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Name != "Kept" {
		t.Fatalf("models = %#v, want only Kept", models)
	}
}

func TestDiscoverDeduplicatesOverlappingRoots(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src")
	writeSource(t, root, "src/model.go", "package app\ntype User struct { ID int64 }\n")

	models, err := Discover(Options{Roots: []string{root, sub}})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %#v, overlapping roots duplicated the model", models)
	}
}

func TestCandidatesSelectOnlyEquallyBestMatches(t *testing.T) {
	models := []Model{
		{Package: "a", Name: "User", Table: "users"},
		{Package: "b", Name: "User", Table: "users"},
		{Package: "c", Name: "Archive", Table: "users_archive", ExplicitTable: true},
		{Package: "d", Name: "LegacyUser", Table: "users", ExplicitTable: true},
	}

	got := Candidates(models, "create-users-table")
	if len(got) != 1 || got[0].Name != "LegacyUser" {
		t.Fatalf("candidates = %#v, explicit exact table should win", got)
	}

	models = models[:2]
	got = Candidates(models, "users")
	if len(got) != 2 || got[0].Package != "a" || got[1].Package != "b" {
		t.Fatalf("ambiguous candidates = %#v, want stable equal matches", got)
	}
	if got := Candidates(models, "orders"); len(got) != 0 {
		t.Fatalf("unrelated candidates = %#v, want none", got)
	}
	if got := Candidates([]Model{{Name: "APIUser", Table: "api_users"}}, "CreateAPIUsersTable"); len(got) != 1 {
		t.Fatalf("acronym candidates = %#v, want APIUser", got)
	}
}

func TestDiscoverReportsInvalidGoSource(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "bad.model.go", "package app\ntype Bad struct {")
	_, err := Discover(Options{Roots: []string{root}})
	if err == nil || !strings.Contains(err.Error(), "bad.model.go") {
		t.Fatalf("error = %v, want source filename", err)
	}
}
