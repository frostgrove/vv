package tenancy

import (
	"context"
	"testing"
	"time"
)

func acceptedGrant(t *testing.T, members ...string) (*Authority, *Grant) {
	t.Helper()
	epoch, err := NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	known := map[Reference]Resolution{}
	cohort := make([]Reference, 0, len(members))
	for _, raw := range members {
		reference, err := ParseReference(raw)
		if err != nil {
			t.Fatal(err)
		}
		known[reference] = Resolution{Reference: reference, Lifecycle: Active, Epoch: epoch}
		cohort = append(cohort, reference)
	}
	authority, err := New(Spec{Resolver: knownTenants{known}})
	if err != nil {
		t.Fatal(err)
	}
	purpose, err := ParsePurpose("monthly-billing")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := authority.Accept(purpose, cohort, time.Now().Add(time.Hour), ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	return authority, grant
}

func TestEveryPartOfAGrantIsInsideItsBinding(t *testing.T) {
	authority, honest := acceptedGrant(t, "acme", "globex")
	if !authority.holds(honest) {
		t.Fatal("the authority does not hold the grant it just accepted, so nothing below proves anything")
	}

	stranger, err := ParseReference("initech")
	if err != nil {
		t.Fatal(err)
	}
	widerPurpose, err := ParsePurpose("everything")
	if err != nil {
		t.Fatal(err)
	}

	for name, edit := range map[string]func(*Grant){
		"a member appended after acceptance": func(g *Grant) { g.cohort = append(g.cohort, stranger) },
		"a member swapped for another":       func(g *Grant) { g.cohort[1] = stranger },
		"the cohort truncated":               func(g *Grant) { g.cohort = g.cohort[:1] },
		"the deadline pushed out":            func(g *Grant) { g.until = g.until.Add(time.Hour) },
		"the purpose widened":                func(g *Grant) { g.purpose = widerPurpose },
		"a class the grant never named":      func(g *Grant) { g.classes = g.classes.Merge(Admit(ClassDurable, Active)) },
	} {
		t.Run(name, func(t *testing.T) {
			forged := &Grant{
				purpose: honest.purpose,
				cohort:  append([]Reference(nil), honest.cohort...),
				classes: honest.classes,
				until:   honest.until,
				binding: honest.binding,
			}
			edit(forged)
			if authority.holds(forged) {
				t.Fatal("the binding did not cover this, so a grant can be edited after it was accepted")
			}
			if _, err := authority.Each(context.Background(), forged, ClassWrite, func(context.Context) error {
				t.Fatal("edited cohort work ran")
				return nil
			}); err == nil {
				t.Fatal("an edited grant was honoured")
			}
		})
	}
}

// A tenant may be called "write". Without a count in front of each run, a grant
// that reads for two tenants and a grant that reads and writes for one write the
// same fields in the same order, and one authority holds both.
func TestAClassCannotBeSpelledAsACohortMember(t *testing.T) {
	epoch, err := NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	known := map[Reference]Resolution{}
	cohort := make([]Reference, 0, 2)
	for _, raw := range []string{"write", "acme"} {
		reference, err := ParseReference(raw)
		if err != nil {
			t.Fatal(err)
		}
		known[reference] = Resolution{Reference: reference, Lifecycle: Active, Epoch: epoch}
		cohort = append(cohort, reference)
	}
	authority, err := New(Spec{Resolver: knownTenants{known}})
	if err != nil {
		t.Fatal(err)
	}
	purpose, err := ParsePurpose("monthly-billing")
	if err != nil {
		t.Fatal(err)
	}
	reading, err := authority.Accept(purpose, cohort, time.Now().Add(time.Hour), ClassRead)
	if err != nil {
		t.Fatal(err)
	}
	if !authority.holds(reading) {
		t.Fatal("the authority does not hold the grant it just accepted, so nothing below proves anything")
	}

	writing := &Grant{
		purpose: reading.purpose,
		cohort:  reading.cohort[1:],
		classes: reading.classes.Merge(Admit(ClassWrite, Active)),
		until:   reading.until,
		binding: reading.binding,
	}
	if authority.holds(writing) {
		t.Fatal("a grant naming one tenant and two classes carries the binding of a grant naming two tenants and one class")
	}
}
