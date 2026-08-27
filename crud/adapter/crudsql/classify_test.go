package crudsql

import (
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/errs"
)

// The degradation [[D-046]]'s last forbid buys, made visible.
//
// A constructor that names its engine classifies; one that cannot name it does
// not. crud.Dialect is not the engine — crud.MySQL is MariaDB too, and the two
// answer a failed CHECK with different numbers — so Open, From and Source name
// nothing and get no classifier. The cost is a 409 with no code; the alternative
// is a wrong code on MariaDB, which is worse and silent.
//
// The pair is its own control in both directions. Without the Open/From half
// this says nothing about the degradation, and without the WithFaults half it
// passes for an option that is ignored. crudsql.From is [[UC-005]]'s headline
// path and appears all over the tree, so an invisible loss of the feature there
// is the failure this exists to catch.
func TestOnlyADeclaredEngineProducesACode(t *testing.T) {
	driver := &pgconnish{
		Code:           "23505",
		Message:        `duplicate key value violates unique constraint "users_email_key"`,
		ConstraintName: "users_email_key",
		TableName:      "users",
	}

	for _, tc := range []struct {
		name     string
		conflict func(error) error
		code     bool
	}{
		{"Postgres, which names its engine", Postgres(nil).conflict, true},
		{"MariaDB, which names the one crud.Dialect cannot", MariaDB(nil).conflict, false},
		{"Open, which is handed a dialect and not an engine", Open(nil, crud.Postgres{}).conflict, false},
		{"From, the joined-transaction path", From(nil).conflict, false},
		{"Source, the same for a foreign handle", Source(nil, crud.Postgres{}).(source).conflict, false},
		{"From with a classifier passed in", From(nil, WithFaults(sqlfault.New("postgres"))).conflict, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.conflict(driver)

			// Every one of them answers the sentinel. That half never degrades:
			// the gate is dialect-free.
			if !errors.Is(got, crud.ErrConflict) {
				t.Fatalf("a duplicate key stopped being a conflict: %v", got)
			}
			f, ok := errs.AsFault(got)
			if ok != tc.code {
				t.Fatalf("a fault was produced = %v, want %v", ok, tc.code)
			}
			if !ok {
				return
			}
			if f.Code != errs.CodeUnique || f.Kind != errs.KindConflict {
				t.Fatalf("the fault says %v/%v, want conflict/unique", f.Kind, f.Code)
			}
		})
	}
}

// MariaDB is a constructor of its own because the two servers disagree about a
// number while sharing a driver, a dialect and a wire protocol. Getting it wrong
// costs the code and never the status.
func TestAMariaDBNumberIsOnlyReadByTheMariaDBConstructor(t *testing.T) {
	check := newMySQLish(4025, "23000", "CONSTRAINT `ck_age` failed")

	f, ok := errs.AsFault(MariaDB(nil).conflict(check))
	if !ok || f.Code != errs.CodeCheck {
		t.Fatalf("MariaDB's 4025 did not classify as a check through MariaDB(): %v", f)
	}

	// The control. Through MySQL() the same violation is still a 409 — class 23
	// covers it — and carries no code, because 4025 is not in MySQL's table.
	// That is the whole reason the constructor exists.
	got := MySQL(nil).conflict(check)
	if !errors.Is(got, crud.ErrConflict) {
		t.Fatalf("the sentinel was lost as well as the code: %v", got)
	}
	if f, ok := errs.AsFault(got); ok {
		t.Fatalf("MySQL's table answered %q for a number only MariaDB reports", f.Code)
	}
}

func TestAMissingTableIsAClassifiedInternalSchemaFailure(t *testing.T) {
	driver := &pgconnish{
		Code:    "42P01",
		Message: `ERROR: relation "products" does not exist (SQLSTATE 42P01)`,
	}

	got := Postgres(nil).conflict(driver)
	if !errors.Is(got, crud.ErrSchemaNotReady) {
		t.Fatalf("a missing table does not match ErrSchemaNotReady: %v", got)
	}
	if errors.Is(got, crud.ErrConflict) {
		t.Fatalf("a missing table is an operational failure, not a conflict: %v", got)
	}
	if !errors.Is(got, driver) {
		t.Fatalf("the driver error is no longer reachable underneath the fault: %v", got)
	}

	f, ok := errs.AsFault(got)
	if !ok || f.Kind != errs.KindInternal || f.Code != errs.CodeSchemaNotReady {
		t.Fatalf("fault = %#v, want internal/schema_not_ready", f)
	}
	if got.Error() != "errs: internal: schema_not_ready (1 violation)" {
		t.Fatalf("Error() = %q, want the safe classification only", got.Error())
	}
}
