package crud_test

import (
	"testing"
	"time"

	"github.com/shardit-io/ordo/crud"
)

type Timestamps struct {
	CreatedAt time.Time `db:"created_at,immutable"`
	UpdatedAt time.Time `db:"updated_at,generated"`
}

type Account struct {
	Timestamps                    // embedded structs are flattened
	AccountID   int64             `db:"id,pk,auto"`
	Slug        string            // untagged: snake_cased automatically
	OwnerUserID int64             // OwnerUserID -> owner_user_id
	Secret      string            `db:"-"`
	Balance     crud.Opt[float64] `db:"balance"`
}

func TestSchema(t *testing.T) {
	s, err := crud.SchemaOf[Account]()
	if err != nil {
		t.Fatal(err)
	}

	wantCols := []string{"id", "created_at", "updated_at", "slug", "owner_user_id", "balance"}
	if got := s.Columns(); !equal(got, wantCols) {
		t.Fatalf("columns = %v, want %v (primary key first)", got, wantCols)
	}
	if s.PK.Name != "AccountID" || !s.PK.Auto {
		t.Fatalf("pk = %+v", s.PK)
	}
	if s.Field("Secret") != nil {
		t.Error(`db:"-" should be ignored`)
	}
	if !s.HasGen {
		t.Error("updated_at is generated")
	}
	if f := s.Field("Balance"); f == nil || !f.Optional {
		t.Errorf("balance should be recognised as an Opt: %+v", f)
	}
	// Insert covers everything but generated columns; Update also drops the key
	// and immutable columns.
	if got := names(s.Insert); !equal(got, []string{"AccountID", "CreatedAt", "Slug", "OwnerUserID", "Balance"}) {
		t.Errorf("insert fields = %v", got)
	}
	if got := names(s.Update); !equal(got, []string{"Slug", "OwnerUserID", "Balance"}) {
		t.Errorf("update fields = %v", got)
	}
	// Fields resolve by Go name and by column name alike.
	if s.Field("OwnerUserID") != s.Field("owner_user_id") {
		t.Error("a field should resolve by either name")
	}
}

func TestTableNameFallback(t *testing.T) {
	m, err := crud.NewMeta[Account]("")
	if err != nil {
		t.Fatal(err)
	}
	if m.Table != "accounts" {
		t.Errorf("table = %q, want accounts", m.Table)
	}
}

func TestSchemaRejectsBadDeclarations(t *testing.T) {
	type NoPK struct {
		Name string `db:"name"`
	}
	type TwoPK struct {
		A int `db:"a,pk"`
		B int `db:"b,pk"`
	}
	type BadOption struct {
		A int `db:"a,pk,wat"`
	}
	type DupColumn struct {
		ID int    `db:"id,pk"`
		A  string `db:"x"`
		B  string `db:"x"`
	}

	if _, err := crud.SchemaOf[NoPK](); err == nil {
		t.Error("a model without a primary key must fail")
	}
	if _, err := crud.SchemaOf[TwoPK](); err == nil {
		t.Error("composite keys are not supported and must fail")
	}
	if _, err := crud.SchemaOf[BadOption](); err == nil {
		t.Error("an unknown tag option must fail")
	}
	if _, err := crud.SchemaOf[DupColumn](); err == nil {
		t.Error("duplicate columns must fail")
	}
}

// An assigned (non-integer) key is not treated as database-generated.
func TestAssignedKeyIsNotAuto(t *testing.T) {
	type Doc struct {
		ID   string `db:"id,pk"`
		Body string `db:"body"`
	}
	s, err := crud.SchemaOf[Doc]()
	if err != nil {
		t.Fatal(err)
	}
	if s.PK.Auto {
		t.Error("a string primary key must not default to auto")
	}
}

func TestNoAutoOptOut(t *testing.T) {
	type Snowflake struct {
		ID   int64 `db:"id,pk,noauto"`
		Body string
	}
	s, err := crud.SchemaOf[Snowflake]()
	if err != nil {
		t.Fatal(err)
	}
	if s.PK.Auto {
		t.Error("noauto should keep an integer key out of the generated path")
	}
}

func names(fs []*crud.Field) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
