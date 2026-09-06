package tenancy

import (
	"testing"
)

// A deployment that asks for less must not silently get more. Admit used to
// answer the zero value for a class it did not recognise, for a state it did not
// recognise and for no states at all, and New read that zero value as "nothing
// was configured" and substituted the permissive default — so an operator who
// wrote Admit(ClassRead) intending a read-only deployment got Active admitted for
// reads, writes and durable work, with no error and nothing to print.
func TestAnAdmissionThatNamesNothingThisPackageHasIsRefusedRatherThanWidened(t *testing.T) {
	for name, admission := range map[string]Admission{
		"a class with no states at all": Admit(ClassRead),
		"a class that does not exist":   Admit(Class(9), Active),
		"a state that does not exist":   Admit(ClassRead, Lifecycle(99)),
		"the unknown lifecycle":         Admit(ClassRead, LifecycleUnknown),
		"a refusal merged with a good one": Admit(ClassRead, Active).
			Merge(Admit(ClassWrite, Lifecycle(99))),
	} {
		t.Run(name, func(t *testing.T) {
			authority, err := New(Spec{Resolver: Fixed{}, Admission: admission})
			if err == nil {
				t.Fatalf("the admission was accepted, and it admits write/Active = %v — "+
					"a policy nobody could read was widened instead of refused",
					authority.Admits(ClassWrite, Active))
			}
		})
	}
}

// The control. Without it the test above passes just as well against a New that
// refuses every admission, and the two defaults it protects are the ones every
// deployment relies on.
func TestAnAdmissionThisPackageUnderstandsIsStillAccepted(t *testing.T) {
	stated, err := New(Spec{
		Resolver: Fixed{},
		Admission: Admit(ClassRead, Active, Suspended).
			Merge(Admit(ClassWrite, Active)).
			Merge(Admit(ClassDurable, Active)),
	})
	if err != nil {
		t.Fatalf("a well-formed admission was refused: %v", err)
	}
	if !stated.Admits(ClassRead, Suspended) {
		t.Fatal("a suspended tenant was not admitted for reads, which is what the policy said")
	}
	if stated.Admits(ClassWrite, Suspended) {
		t.Fatal("a suspended tenant was admitted for writes, which the policy did not say")
	}

	unstated, err := New(Spec{Resolver: Fixed{}})
	if err != nil {
		t.Fatalf("an authority with no admission at all was refused: %v", err)
	}
	if !unstated.Admits(ClassWrite, Active) || unstated.Admits(ClassWrite, Suspended) {
		t.Fatal("the default is no longer Active for every class and nothing else")
	}
}
