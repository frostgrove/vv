package sqlfault

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
)

func duplicateKey() *pgconnish {
	return &pgconnish{
		Code:           "23505",
		Message:        `duplicate key value violates unique constraint "users_email_key"`,
		ConstraintName: "users_email_key",
		TableName:      "users",
		SchemaName:     "public",
		Detail:         "Key (email)=(a@b.c) already exists.",
	}
}

func TestAFaultIsBuiltOnlyWhenACodeAndItsKindAreKnown(t *testing.T) {
	err := duplicateKey()

	f, ok := New("postgres").Classify(err)
	if !ok {
		t.Fatal("a duplicate key on the engine the classifier was declared for produced no fault")
	}
	if f.Code != errs.CodeUnique {
		t.Fatalf("Code = %q, want unique", f.Code)
	}
	if f.Kind != errs.KindConflict {
		t.Fatalf("Kind = %v, want conflict", f.Kind)
	}
	if f.Detail.Dialect != "postgres" || f.Detail.SQLState != "23505" || f.Detail.Native != 0 {
		t.Fatalf("Detail = %+v, want the engine, the state and pgconn's zero number", f.Detail)
	}
	if f.Detail.Constraint != "users_email_key" || f.Detail.Table != "users" {
		t.Fatalf("Detail lost what the driver named: %+v", f.Detail)
	}

	if f.Detail.Driver != error(err) {
		t.Fatalf("Detail.Driver = %v, want the driver error the classifier was handed", f.Detail.Driver)
	}

	c := New("postgres", WithCodes(errs.NewCodes()))
	if f, ok := c.Classify(err); ok {
		t.Fatalf("an unwired vocabulary produced a fault carrying kind %v", f.Kind)
	}

	if got := Wrap(c, err); !errors.Is(got, crud.ErrConflict) {
		t.Fatalf("with no code learned the sentinel was dropped too: %v", got)
	}
}

func TestAnAlreadyClassifiedErrorIsNotClassifiedTwice(t *testing.T) {
	c := New("postgres")
	first := Wrap(c, duplicateKey())

	f, ok := errs.AsFault(first)
	if !ok || f.Code != errs.CodeUnique {
		t.Fatalf("the first call produced %v, and this test says nothing unless it produced a fault", first)
	}

	second := Wrap(c, first)
	if second != first {
		t.Fatalf("the second call rebuilt the error: %v", second)
	}
	again, ok := errs.AsFault(second)
	if !ok || again != f {
		t.Fatalf("errors.As now finds a different fault, so the first one is shadowed")
	}
}

type barefaced struct{}

func (barefaced) Classify(err error) (*errs.Fault, bool) {
	e := Extract(err)
	if e.SQLState != "23505" {
		return nil, false
	}
	return errs.Conflict().Code("their_own_code").Wrapping(err).Fault(), true
}

func TestASentinelIsAttachedWhateverTheClassifierReturned(t *testing.T) {
	got := Wrap(barefaced{}, duplicateKey())
	if !errors.Is(got, crud.ErrConflict) {
		t.Fatalf("a fault with no sentinel in it came back matching nothing: %v", got)
	}
	f, ok := errs.AsFault(got)
	if !ok || f.Code != "their_own_code" {
		t.Fatalf("the third party's fault is unreachable: %v", got)
	}

	other := pgish("42P01")
	if got := Wrap(barefaced{}, other); errors.Is(got, crud.ErrConflict) {
		t.Fatalf("an undefined table came back as a conflict: %v", got)
	}
}

func TestNothingInExtractionOrClassificationReadsMessageDetailOrHint(t *testing.T) {
	for _, tc := range []struct {
		name string
		real *pgconnish
	}{
		{"a duplicate key", duplicateKey()},
		{"a NOT NULL violation", &pgconnish{Code: "23502", Message: "null value in column \"name\"", ColumnName: "name", TableName: "users", Detail: "Failing row contains (1, null).", Hint: "give it a value"}},
		{"a CHECK violation", &pgconnish{Code: "23514", Message: "new row violates check constraint \"ck_age\"", ConstraintName: "ck_age", TableName: "users", Detail: "Failing row contains (1, -3)."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			substituted := *tc.real
			substituted.Message = "нарушение уникального ограничения"
			substituted.Detail = "ключ уже существует"
			substituted.Hint = "попробуйте другой"

			for name, pair := range map[string][2]string{
				"Message": {tc.real.Message, substituted.Message},
				"Detail":  {tc.real.Detail, substituted.Detail},
				"Hint":    {tc.real.Hint, substituted.Hint},
			} {
				if name == "Hint" && tc.real.Hint == "" {
					continue
				}
				if pair[0] == "" {
					t.Fatalf("the fixture's %s is empty, so substituting it proves nothing", name)
				}
				if pair[0] == pair[1] {
					t.Fatalf("the substituted %s is the same text", name)
				}
			}

			c := New("postgres")
			a, ok := c.Classify(tc.real)
			if !ok {
				t.Fatal("the fixture did not classify at all")
			}
			b, ok := c.Classify(&substituted)
			if !ok {
				t.Fatal("the same violation stopped classifying once the server answered in another language")
			}
			if a.Code != b.Code || a.Kind != b.Kind {
				t.Fatalf("classification moved with the text: %v/%v against %v/%v", a.Code, a.Kind, b.Code, b.Kind)
			}
			if !reflect.DeepEqual(a.Violations[0].Source, b.Violations[0].Source) {
				t.Fatalf("the source moved with the text: %+v against %+v", a.Violations[0].Source, b.Violations[0].Source)
			}
			ad, bd := a.Detail, b.Detail
			ad.Driver, bd.Driver = nil, nil
			if fmt.Sprint(ad) != fmt.Sprint(bd) {
				t.Fatalf("the detail moved with the text:\n%+v\n%+v", ad, bd)
			}
		})
	}
}

func TestADriverViolationIsStateShapedAndHasNoPath(t *testing.T) {
	f, ok := New("postgres").Classify(duplicateKey())
	if !ok {
		t.Fatal("no fault")
	}
	if len(f.Violations) != 1 {
		t.Fatalf("%d violations, want exactly one — merging is the probe's job, not this layer's", len(f.Violations))
	}
	v := f.Violations[0]

	untouched := errs.Conflict().General().Fault().Violations[0]
	if untouched.Origin != errs.OriginInput {
		t.Fatal("the zero Origin is no longer OriginInput, so this test's control has stopped controlling anything")
	}
	if v.Origin != errs.OriginState {
		t.Fatal("a violation the database raised is marked input-shaped, so the never-echo default and the envelope's grouping both read it wrong")
	}

	if v.Path != nil {
		t.Fatalf("Path = %v: this layer owns no hop of the path and must not invent one", v.Path)
	}
	if v.Approximate {
		t.Fatal("Approximate is set, which says a path was attempted and could not be resolved; none was attempted")
	}

	if v.Source.Constraint != "users_email_key" || v.Source.Table != "users" || v.Source.Schema != "public" {
		t.Fatalf("Source = %+v, want what pgconn named", v.Source)
	}
	if v.Code != errs.CodeUnique {
		t.Fatalf("the violation's code is %q", v.Code)
	}
}

func TestAFaultCarriesNothingTheDriverSaidInItsErrorText(t *testing.T) {
	driver := &pgconnish{
		Code:           "23505",
		Message:        `ERROR: duplicate key value violates unique constraint "users_email_key" (SQLSTATE 23505) [host=db.internal user=vv password=hunter2]`,
		ConstraintName: "users_email_key",
		TableName:      "users",
		SchemaName:     "public",
		ColumnName:     "email",
		Detail:         "Key (email)=(a@b.c) already exists.",
	}
	f, ok := New("postgres").Classify(driver)
	if !ok {
		t.Fatal("no fault")
	}
	text := f.Error()

	for name, leak := range map[string]string{
		"the constraint":        "users_email_key",
		"the table":             "users",
		"the schema":            "public",
		"the column":            "email",
		"the SQLSTATE":          "23505",
		"the connection string": "host=db.internal",
		"the offending value":   "a@b.c",
	} {
		if !fixtureCarries(driver, leak) {
			t.Fatalf("%s (%q) is in neither the driver's message nor its fields, so searching for it says nothing", name, leak)
		}
		if strings.Contains(text, leak) {
			t.Fatalf("Error() names %s: %q", name, text)
		}
	}

	my, ok := New("mysql").Classify(myish(1062, "23000", "Duplicate entry 'a@b.c' for key 'users.email'"))
	if !ok {
		t.Fatal("the MySQL fixture did not classify")
	}
	if my.Detail.Native != 1062 {
		t.Fatalf("Detail.Native = %d, so searching for it in Error() proves nothing", my.Detail.Native)
	}
	if strings.Contains(my.Error(), strconv.Itoa(my.Detail.Native)) {
		t.Fatalf("Error() names the engine's own number: %q", my.Error())
	}

	if !strings.Contains(text, "conflict") || !strings.Contains(text, "unique") {
		t.Fatalf("Error() = %q, and a client on a 409 reads this: it has to name the kind and the code", text)
	}
}

func fixtureCarries(e *pgconnish, s string) bool {
	for _, v := range []string{
		e.Message, e.Detail, e.Hint, e.Code,
		e.ConstraintName, e.TableName, e.SchemaName, e.ColumnName, e.DataTypeName,
	} {
		if strings.Contains(v, s) {
			return true
		}
	}
	return false
}

func TestAnUnknownDialectStillAnswersTheIntegrityGate(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    errs.Classifier
	}{
		{"an engine nothing has a table for", New("cockroach")},
		{"no engine at all", New("")},
		{"no classifier at all", nil},
		{"a nil classifier of this type", (*Classifier)(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Wrap(tc.c, duplicateKey())
			if !errors.Is(got, crud.ErrConflict) {
				t.Fatalf("a duplicate key stopped being a conflict: %v", got)
			}
			if f, ok := errs.AsFault(got); ok {
				t.Fatalf("a code was invented for an engine nothing was measured on: %v", f.Code)
			}

			other := pgish("42601")
			if got := Wrap(tc.c, other); got != error(other) {
				t.Fatalf("a syntax error was not returned untouched: %v", got)
			}
		})
	}
	if Wrap(New("postgres"), nil) != nil {
		t.Fatal("a nil error came back as something")
	}

	if got := New("cockroach").Engine(); got != "cockroach" {
		t.Fatalf("Engine() = %q", got)
	}
	if got := (*Classifier)(nil).Engine(); got != "" {
		t.Fatalf("a nil classifier answered %q rather than degrading", got)
	}
}

func TestAClassifiedConflictIsItsOwnOutermostError(t *testing.T) {
	driver := duplicateKey()
	got := Wrap(New("postgres"), driver)

	f, ok := errs.AsFault(got)
	if !ok {
		t.Fatalf("no fault: %v", got)
	}
	if got != error(f) {
		t.Fatalf("the fault is wrapped in a %T, so a 409 body carries that wrapper's text and not the fault's: %q", got, got.Error())
	}
	if want := "errs: conflict: unique (1 violation)"; got.Error() != want {
		t.Fatalf("a classified 409's body reads %q, want %q", got.Error(), want)
	}

	plain := Wrap(New("cockroach"), driver)
	if _, ok := errs.AsFault(plain); ok {
		t.Fatal("an engine nothing has a table for produced a fault")
	}
	if !strings.Contains(plain.Error(), driver.ConstraintName) {
		t.Fatalf("an unclassified conflict no longer carries the driver's message (%q), so the leg above measures nothing", plain.Error())
	}
}
