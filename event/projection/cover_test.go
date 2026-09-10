package projection_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event/projection"
)

func coverOf(t *testing.T, partitions ...projection.Partition) projection.Cover {
	t.Helper()
	held, err := projection.NewCover(partitions...)
	if err != nil {
		t.Fatalf("the cover %v was refused: %v", partitions, err)
	}
	return held
}

// Every key matches exactly one member of an admitted set, asserted over keys
// rather than argued from the arithmetic.
func matchesExactlyOnce(t *testing.T, cover projection.Cover, keys []string) {
	t.Helper()
	for _, key := range keys {
		matched := 0
		for _, part := range cover.Partitions() {
			if part.Matches(key) {
				matched++
			}
		}
		if matched != 1 {
			t.Fatalf("%q matched %d members of %v, and an admitted cover matches every key exactly one time", key, matched, cover.Partitions())
		}
	}
}

// The forgotten member is the failure no per-partition check can see: each runner
// is healthy, each checkpoint advances, and a quarter of the log is delivered to
// nothing for ever.
func TestACoverWithAGapIsRefusedAndACompleteOneIsNot(t *testing.T) {
	_, err := projection.NewCover(partitionOf(t, 0, 3), partitionOf(t, 1, 3), partitionOf(t, 3, 3))
	if err == nil {
		t.Fatal("a set with a quarter of the key space missing was admitted, and every key of that quarter is delivered to nothing")
	}
	if !errors.Is(err, projection.ErrTopology) {
		t.Fatalf("a set with a gap was refused as %v, which is not the class a topology refusal carries", err)
	}
	if !strings.Contains(err.Error(), "2.3") {
		t.Fatalf("a set missing {2,3} was refused with %q, which does not name the share nothing covers", err)
	}
	if _, err := projection.NewCover(); err == nil {
		t.Fatal("a cover of no partitions was admitted")
	}

	t.Run("the control: the complete set is admitted and covers every key once", func(t *testing.T) {
		held := coverOf(t, partitionOf(t, 0, 3), partitionOf(t, 1, 3), partitionOf(t, 2, 3), partitionOf(t, 3, 3))
		if held.Count() != 4 {
			t.Fatalf("the complete four count %d", held.Count())
		}
		matchesExactlyOnce(t, held, keys(500))
	})

	t.Run("the control: the whole key space alone is a cover of one", func(t *testing.T) {
		held := coverOf(t, projection.Whole())
		if held.Count() != 1 {
			t.Fatalf("the whole key space alone counts %d members", held.Count())
		}
		matchesExactlyOnce(t, held, keys(50))
	})

	t.Run("a caller cannot re-open a set that was checked", func(t *testing.T) {
		held := coverOf(t, partitionOf(t, 0, 1), partitionOf(t, 1, 1))
		taken := held.Partitions()
		taken[0] = partitionOf(t, 1, 1)
		if again := held.Partitions(); again[0] != partitionOf(t, 0, 1) {
			t.Fatalf("the cover holds %v after a caller wrote into the slice it was handed, so the set it checked is not the set it holds", again)
		}
	})
}

// Two members that overlap are two different names, so the supervisor admits
// both, their checkpoint rows never conflict and every key matching both is
// applied twice. The refusal is by mask arithmetic, and the control is the pair
// that proves it: a set whose masks differ just as much and which is exact.
func TestACoverWithAnOverlapIsRefusedByMaskArithmeticAndNotByName(t *testing.T) {
	_, err := projection.NewCover(
		partitionOf(t, 0, 3), partitionOf(t, 1, 3), partitionOf(t, 2, 3), partitionOf(t, 3, 3), partitionOf(t, 1, 7),
	)
	if err == nil {
		t.Fatal("{1,3} and {1,7} were admitted together, and every key of the finer one is applied by both")
	}
	if !errors.Is(err, projection.ErrTopology) {
		t.Fatalf("an overlapping set was refused as %v", err)
	}
	if !strings.Contains(err.Error(), "1.3") || !strings.Contains(err.Error(), "1.7") {
		t.Fatalf("an overlapping set was refused with %q, which does not name the two members that both match a key", err)
	}

	for _, refused := range [][]projection.Partition{
		{partitionOf(t, 0, 1), partitionOf(t, 1, 1), projection.Whole()},
		{projection.Whole(), projection.Whole()},
		{partitionOf(t, 0, 3), partitionOf(t, 0, 3), partitionOf(t, 1, 3), partitionOf(t, 2, 3), partitionOf(t, 3, 3)},
	} {
		if _, err := projection.NewCover(refused...); !errors.Is(err, projection.ErrTopology) {
			t.Fatalf("the overlapping set %v was answered %v", refused, err)
		}
	}

	t.Run("the control: the same space with one member split is admitted", func(t *testing.T) {
		held := coverOf(t,
			partitionOf(t, 0, 3), partitionOf(t, 1, 3), partitionOf(t, 2, 3), partitionOf(t, 3, 7), partitionOf(t, 7, 7),
		)
		if held.Count() != 5 {
			t.Fatalf("the split set counts %d members", held.Count())
		}
		matchesExactlyOnce(t, held, keys(500))
	})
}

// Both types carry a proof — a set somebody checked, a name somebody built — and
// a composite literal in any package is the one way to hold one without it. The
// zero value is exactly that value and nothing else, because neither type has an
// exported field, and it is the one value the constructor never answers beside a
// nil error. That is what makes Count() and Projection() an exact discriminator
// and a marker field unnecessary; every door in this phase that takes a Cover or
// an Identity refuses it.
func TestTheZeroCoverAndTheZeroIdentityAreTellableFromEveryCheckedOne(t *testing.T) {
	var cover projection.Cover
	if cover.Count() != 0 || len(cover.Partitions()) != 0 {
		t.Fatalf("the zero cover counts %d members, so the discriminator every door asks is not exact", cover.Count())
	}
	if _, err := projection.NewCover(); !errors.Is(err, projection.ErrTopology) {
		t.Fatalf("a cover of no partitions was answered %v, and the zero value is what a refused NewCover leaves in the caller's hands", err)
	}

	var identity projection.Identity
	if identity.Projection() != "" || identity.String() != "" {
		t.Fatalf("the zero identity names the projection %q and renders %q", identity.Projection(), identity)
	}
	if _, err := projection.NewIdentity("", projection.Ungenerated, projection.Whole()); !errors.Is(err, projection.ErrSpec) {
		t.Fatalf("an empty name was answered %v, and the zero value is what a refused NewIdentity leaves in the caller's hands", err)
	}

	t.Run("no value either constructor answers is tellable from a checked one", func(t *testing.T) {
		for _, checked := range [][]projection.Partition{
			{projection.Whole()},
			{partitionOf(t, 0, 1), partitionOf(t, 1, 1)},
			{partitionOf(t, 0, 3), partitionOf(t, 1, 3), partitionOf(t, 2, 3), partitionOf(t, 3, 3)},
		} {
			if held := coverOf(t, checked...); held.Count() == 0 {
				t.Fatalf("NewCover answered a set of %v that counts zero members, so a checked cover reads as an unchecked one", checked)
			}
		}
		for _, name := range []string{"orders", "o", "orders.v2"} {
			for _, generation := range []projection.Generation{projection.Ungenerated, 7} {
				for _, partition := range []projection.Partition{projection.Whole(), partitionOf(t, 3, 7)} {
					held := identityOf(t, name, generation, partition)
					if held.Projection() == "" || held == (projection.Identity{}) {
						t.Fatalf("NewIdentity answered %+v for %q, so a built identity reads as one nobody built", held, name)
					}
				}
			}
		}
	})
}
