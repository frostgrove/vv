package eventtest

import (
	"errors"
	"fmt"
	"testing"

	"github.com/frostgrove/vv/event"
)

// Each of the three answers rather than fatals, and the exported wrapper is what
// turns an answer into a failed test. A branch that only ever runs inside
// t.Fatal cannot be driven by a test that must stay green, and the branch that
// finds the collision is the whole of what these three are for.

func RoundTrip[S, ID, E any](t *testing.T, fact *event.Fact[S, ID, E], byRevision ...any) {
	t.Helper()
	if err := roundTrips(fact, byRevision...); err != nil {
		t.Fatal(err)
	}
}

func roundTrips[S, ID, E any](fact *event.Fact[S, ID, E], byRevision ...any) error {
	if fact == nil {
		return errors.New("eventtest: there is no fact here to round-trip")
	}
	if _, err := fact.RoundTrip(byRevision...); err != nil {
		return fmt.Errorf("eventtest: %s does not survive its own declaration: %w", fact.Name(), err)
	}
	return nil
}

// The injectivity the framework cannot check. Two identities that render one key
// are two aggregates over one history: each folds the other's facts and appends
// at versions derived from the other's, with no error at any point, so the
// obligation is discharged by running it over the application's own identities.
func Keys[S, ID any](t *testing.T, a *event.Aggregate[S, ID], ids ...ID) {
	t.Helper()
	if err := keysRender(a, ids...); err != nil {
		t.Fatal(err)
	}
}

func keysRender[S, ID any](a *event.Aggregate[S, ID], ids ...ID) error {
	if a == nil {
		return errors.New("eventtest: there is no aggregate here whose identities could be rendered")
	}
	if len(ids) < 2 {
		return fmt.Errorf("eventtest: %s was given %d identities, and injectivity is a property of at least two", a.Family(), len(ids))
	}
	rendered := map[event.Key]ID{}
	for _, id := range ids {
		key, err := a.Key(id)
		if err != nil {
			return fmt.Errorf("eventtest: %s renders no legal stream key for %v: %w", a.Family(), id, err)
		}
		if held, taken := rendered[key]; taken {
			return fmt.Errorf("eventtest: %s renders one key for %v and for %v, so the two share one history and fold each other's facts", a.Family(), held, id)
		}
		rendered[key] = id
	}
	return nil
}

// One family names one aggregate — across processes, deployments and binaries,
// which is wider than the scope Bind can hold. This is the proxy for the rest of
// it, over every declaration an application composes whichever store each is
// bound to.
func Families(t *testing.T, declarations ...event.Declaration) {
	t.Helper()
	if err := familiesDiffer(declarations...); err != nil {
		t.Fatal(err)
	}
}

func familiesDiffer(declarations ...event.Declaration) error {
	if len(declarations) == 0 {
		return errors.New("eventtest: there are no declarations here whose families could collide")
	}
	held := map[string]int{}
	for index, declared := range declarations {
		if declared == nil {
			return fmt.Errorf("eventtest: the declaration at position %d is nothing", index)
		}
		family := declared.Family()
		if at, taken := held[family]; taken {
			return fmt.Errorf("eventtest: the declarations at positions %d and %d both name the family %q, so both load each other's facts and append at versions derived from the other's", at, index, family)
		}
		held[family] = index
	}
	return nil
}
