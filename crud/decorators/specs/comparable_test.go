package specs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/specs"
)

// Cmp exists because cmp.Ordered does not reach time.Time — the one type a
// range filter is asked for most. specs.Ordered[User, time.Time] does not
// compile; specs.Comparable does, and it has to produce the same four operators
// or the escape hatch is worse than the constraint it works around.

var createdAt = specs.Comparable[User, time.Time]("CreatedAt")

var (
	earlier = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	later   = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
)

func TestComparableGivesATimeAttributeTheFourRangeOperators(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec specs.Specification[User]
		want string
		args []any
	}{
		{"Gt", createdAt.Gt(earlier), `"created_at" > $1`, []any{earlier}},
		{"Gte", createdAt.Gte(earlier), `"created_at" >= $1`, []any{earlier}},
		{"Lt", createdAt.Lt(later), `"created_at" < $1`, []any{later}},
		{"Lte", createdAt.Lte(later), `"created_at" <= $1`, []any{later}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clause, args := where(t, specs.Where(tc.spec))
			if clause != tc.want {
				t.Fatalf("%s compiled to %s, want %s — the four comparisons are one character "+
					"apart and a wrong one silently returns the complement of what was asked for",
					tc.name, clause, tc.want)
			}
			if len(args) != len(tc.args) {
				t.Fatalf("%s bound %v, want %v", tc.name, args, tc.args)
			}
			for i := range tc.args {
				if args[i] != tc.args[i] {
					t.Fatalf("%s bound %v, want %v", tc.name, args, tc.args)
				}
			}
		})
	}
}

// A standalone declaration is checked against the model the same way a
// metamodel field is: an attribute that names the wrong Go type would compile
// every query it appears in and compare a column against a value the driver
// has to guess at.
func TestComparableIsCheckedAgainstTheModelAtDeclarationTime(t *testing.T) {
	assertPanics(t, "a time attribute declared over a bool",
		func() { specs.Comparable[User, bool]("CreatedAt") })
	assertPanics(t, "a field the model does not have",
		func() { specs.Comparable[User, time.Time]("Nope") })

	// The control: the honest declaration is accepted and lands on the column,
	// so the two refusals above are about the arguments and not about
	// Comparable refusing everything.
	if got := specs.Comparable[User, time.Time]("CreatedAt").Name(); got != "CreatedAt" {
		t.Fatalf("a correct declaration bound to %q, so the refusals above prove nothing", got)
	}
}

// Path is the seam between the two styles: an attribute declared in the
// metamodel is usable with the raw Criteria Builder. What it has to answer is
// the *canonical* name, not the Go field name the metamodel spelled it under —
// the control below shows the difference is not cosmetic.
func TestAMetamodelAttributeCarriesItsCanonicalNameIntoTheBuilder(t *testing.T) {
	byPath := specs.Of[User](func(_ specs.Root[User], cb specs.Builder) crud.Predicate {
		return cb.GreaterThan(User_.Created.Path(), earlier)
	})
	clause, args := where(t, specs.Where(byPath))
	if clause != `"created_at" > $1` {
		t.Fatalf("the attribute's Path compiled to %s, want the created_at column", clause)
	}
	if args[0] != earlier {
		t.Fatalf("args = %v, want the value the builder was given", args)
	}

	// The control. User_ declares that attribute as Created with an
	// `attr:"CreatedAt"` tag, so if Path answered the field name it was
	// declared under the query would not resolve at all — which is what the
	// literal spelling does here.
	rec := crudtest.Postgres().Push(crudtest.Rows())
	byName := specs.Of[User](func(r specs.Root[User], cb specs.Builder) crud.Predicate {
		return cb.GreaterThan(r.Get("Created"), earlier)
	})
	_, err := specs.Executor(Users.Bind(rec)).FindAll(context.Background(), specs.Where(byName))
	// The typed error and not merely "an error". A recorder that ran out of
	// pushed rows, a mistyped table, or any future refusal for an unrelated
	// reason would satisfy `err != nil` — and this leg exists to show that the
	// declared spelling is *unresolvable*, which is a different fact.
	if !errors.As(err, new(*crud.UnknownFieldError)) {
		t.Fatalf("the metamodel's own Go field name did not fail to resolve (%v), so the test above "+
			"cannot tell a canonical name from a declared one", err)
	}
}
