package projection_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

func partitionOf(t *testing.T, id, mask uint32) projection.Partition {
	t.Helper()
	held, err := projection.NewPartition(id, mask)
	if err != nil {
		t.Fatalf("the partition {%d, %d} was refused: %v", id, mask, err)
	}
	return held
}

func identityOf(t *testing.T, name string, generation projection.Generation, partition projection.Partition) projection.Identity {
	t.Helper()
	held, err := projection.NewIdentity(name, generation, partition)
	if err != nil {
		t.Fatalf("the identity %q@%d#%s was refused: %v", name, generation, partition, err)
	}
	return held
}

// The name a checkpoint row, a park and a runner are all keyed by, built in one
// place and parsed back out of one. The control is the whole migration story:
// every projection that exists today is generation zero over the whole key space,
// and its name is what it always was.
func TestAnIdentityRendersAndRoundTrips(t *testing.T) {
	held := identityOf(t, "orders", 2, partitionOf(t, 3, 7))

	if rendered := held.String(); rendered != "orders@2#3.7" {
		t.Fatalf("the identity rendered %q, and the name is what two processes agree on", rendered)
	}
	if strings.ContainsAny(held.String(), "[]") {
		t.Fatalf("%q carries a bracket, which is what closes the field a refusal's renderer opened", held)
	}
	if _, err := event.Track(newCheckpoints(t, newLog(t)), held.String()); err != nil {
		t.Fatalf("the kernel refused %q as a projection name, and the name is what the checkpoint row is keyed by: %v", held, err)
	}

	parsed, err := projection.ParseIdentity(held.String())
	if err != nil {
		t.Fatalf("%q did not parse back: %v", held, err)
	}
	if parsed != held {
		t.Fatalf("%q parsed back as %q, and a name that does not round-trip is one two processes disagree about", held, parsed)
	}
	if parsed.Projection() != "orders" || parsed.Generation() != 2 || parsed.Partition() != partitionOf(t, 3, 7) {
		t.Fatalf("%q parsed back as the projection %q, generation %d and partition %q", held, parsed.Projection(), parsed.Generation(), parsed.Partition())
	}

	if whole := held.Whole(); whole.String() != "orders@2" {
		t.Fatalf("the whole identity of %q renders %q, and the park it keys is the generation's rather than the partition's", held, whole)
	}
	if whole := held.Whole(); !whole.Partition().Whole() || whole.Generation() != 2 {
		t.Fatalf("the whole identity of %q is %+v, and it drops the partition and keeps the generation", held, whole)
	}

	t.Run("it is what the checkpoint row, the runner and the batch are keyed by", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		// The stream this case appends is one {3,7} matches, because the runner
		// below is that partition's and a page it does not match reaches no
		// handler at all.
		stand.append(t, "h", "one")

		seen := make(chan projection.Identity, 1)
		spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			select {
			case seen <- batch.Identity:
			default:
			}
			return nil
		}))
		spec.Generation, spec.Partition = 2, partitionOf(t, 3, 7)
		built := newProjection(t, spec)
		if name := built.Name(); name != "vv.event.projection.orders@2#3.7" {
			t.Fatalf("the runner is named %q, and two partitions of one projection are two runners the supervisor has to tell apart", name)
		}
		running(t, built)

		state := stand.observer.await(t, "the page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 1
		})
		if state.Identity != held || state.Projection != "orders" {
			t.Fatalf("the state names the identity %q and the projection %q", state.Identity, state.Projection)
		}
		if row := stand.row(t, "orders@2#3.7"); row.Advance != 1 {
			t.Fatalf("the checkpoint row keyed by the identity is at advance %d, so the row a generation records through is not its own", row.Advance)
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the row keyed by the bare name is at advance %d, so a generation wrote over the row the live projection resumes from", row.Advance)
		}
		select {
		case batch := <-seen:
			if batch != held {
				t.Fatalf("the handler was handed the identity %q", batch)
			}
		default:
			t.Fatal("the handler was never called, so the identity it is handed was never read")
		}
	})

	t.Run("the control: a projection that exists today keeps its name", func(t *testing.T) {
		plain := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		if rendered := plain.String(); rendered != "orders" {
			t.Fatalf("an ungenerated projection over the whole key space renders %q, so this phase moved every checkpoint row that exists", rendered)
		}
		if parsed, err := projection.ParseIdentity("orders"); err != nil || parsed != plain {
			t.Fatalf("%q parsed back as %q with %v", plain, parsed, err)
		}
	})
}

// A name that is legal today and that this package refuses, because the
// alternative is worse: escaping the delimiter would render orders@2 as
// orders%402, which is a different checkpoint row under a live projection and a
// resume from the origin against a live read model. The control is what keeps the
// refusal from being "any punctuation".
func TestADelimiterInAProjectionNameIsRefusedAtConstruction(t *testing.T) {
	for _, refused := range []struct {
		name  string
		names string
	}{
		{"orders@2", "@"},
		{"orders#3.7", "#"},
	} {
		t.Run(refused.name, func(t *testing.T) {
			held, err := projection.NewIdentity(refused.name, projection.Ungenerated, projection.Whole())
			if err == nil {
				t.Fatalf("%q was accepted and renders %q, which parses back as another identity", refused.name, held)
			}
			if !errors.Is(err, projection.ErrSpec) {
				t.Fatalf("%q was refused as %v, which is not the class a spec that cannot be assembled carries", refused.name, err)
			}
			if !strings.Contains(err.Error(), "Name") || !strings.Contains(err.Error(), refused.names) {
				t.Fatalf("%q was refused with %q, which does not name the field or the delimiter", refused.name, err)
			}

			stand := newStand(t, eventmemory.Spec{})
			stand.append(t, "a", "one")
			built, err := projection.New(stand.spec(refused.name, applies(stand.model)))
			if built != nil || !errors.Is(err, projection.ErrSpec) {
				t.Fatalf("New answered %v for a name it cannot key a row by, and the refusal is at boot rather than on the first page", err)
			}
			if reads := stand.read.reads.Load(); reads != 0 {
				t.Fatalf("New read the log %d times before refusing, and a spec is refused before anything is read", reads)
			}
		})
	}

	t.Run("the control: only the two delimiters separate the parts", func(t *testing.T) {
		held := identityOf(t, "orders.v2", projection.Ungenerated, projection.Whole())
		if rendered := held.String(); rendered != "orders.v2" {
			t.Fatalf("orders.v2 renders %q, and a dot is reached only inside a partition", rendered)
		}
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one")
		running(t, newProjection(t, stand.spec("orders.v2", applies(stand.model))))
		stand.observer.await(t, "the page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 1
		})
	})
}

// Injectivity is what makes a rendered name safe as a primary key: two identities
// that rendered alike would share one checkpoint row, one park and one runner
// name. The second half is the same property read the other way — a string
// String never produces must not parse, or a name nobody rendered would resolve
// to a row somebody is using.
func TestTwoDistinctIdentitiesNeverRenderOneName(t *testing.T) {
	partitions := []projection.Partition{
		projection.Whole(),
		partitionOf(t, 0, 1), partitionOf(t, 1, 1),
		partitionOf(t, 0, 3), partitionOf(t, 1, 3), partitionOf(t, 2, 3), partitionOf(t, 3, 3),
		partitionOf(t, 7, 7), partitionOf(t, 1023, 1023),
	}
	rendered := map[string]projection.Identity{}
	for _, name := range []string{"orders", "orders.v2", "o", "ORDERS"} {
		for _, generation := range []projection.Generation{projection.Ungenerated, 1, 2, 10, 4294967295} {
			for _, partition := range partitions {
				held := identityOf(t, name, generation, partition)
				if collided, taken := rendered[held.String()]; taken {
					t.Fatalf("%+v and %+v both render %q, and a name that is a primary key cannot name two things", held, collided, held)
				}
				rendered[held.String()] = held
				parsed, err := projection.ParseIdentity(held.String())
				if err != nil || parsed != held {
					t.Fatalf("%q parsed back as %+v with %v", held, parsed, err)
				}
			}
		}
	}
	if len(rendered) != 4*5*len(partitions) {
		t.Fatalf("%d distinct names came out of %d identities, so this walked fewer than it thinks", len(rendered), 4*5*len(partitions))
	}

	for _, never := range []string{
		"orders@0", "orders#0.0", "orders@x", "orders#", "orders@", "orders#3",
		"orders#3.6", "orders#8.7", "orders@01", "orders#01.7", "orders@2@3",
		"orders#3.7#1.1", "orders@2#3.7#1.1", "", "@2", "#3.7", "orders#3.2047",
	} {
		if held, err := projection.ParseIdentity(never); err == nil {
			t.Fatalf("%q parsed as %+v, and String never renders it, so a name nobody wrote resolves to a row somebody is using", never, held)
		}
	}
}

// The kernel's identifier rule is duplicated in NewIdentity because an Identity
// is an input at doors that never reach event.Track, and a duplicated rule drifts
// unless something walks both. This walks both, over every byte a name can carry
// and over the shapes a table can hold, and reports a name only one of them
// takes. The two delimiters are the one deliberate difference and are asserted
// separately, in TestADelimiterInAProjectionNameIsRefusedAtConstruction.
func TestNewIdentityRefusesEveryNameTheKernelRefuses(t *testing.T) {
	checkpoints := newCheckpoints(t, newLog(t))
	agree := func(t *testing.T, name string) {
		t.Helper()
		_, mine := projection.NewIdentity(name, projection.Ungenerated, projection.Whole())
		_, kernels := event.Track(checkpoints, name)
		if (mine == nil) != (kernels == nil) {
			t.Fatalf("%q was answered %v here and %v by the door that keys the checkpoint row, and one rule spelled twice has drifted", name, mine, kernels)
		}
		if mine != nil && !errors.Is(mine, projection.ErrSpec) {
			t.Fatalf("%q was refused as %v, which is not the class a name a spec chose carries", name, mine)
		}
	}

	refused := 0
	for _, name := range []string{
		"", "a[b]c", "a]b", "[", "]", "a\x00b", "a\nb", "a\tb", "\x7f",
		"a\xffb", "\xc3\x28", strings.Repeat("o", event.MaxNameBytes+1),
	} {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			agree(t, name)
			if _, err := projection.NewIdentity(name, projection.Ungenerated, projection.Whole()); err == nil {
				t.Fatalf("%q was admitted, and it is a name the kernel's identifier rule refuses", name)
			}
			refused++
		})
	}
	if refused != 12 {
		t.Fatalf("%d of the twelve names above were walked, so this table proves less than it reads", refused)
	}

	t.Run("the control: the names both doors take", func(t *testing.T) {
		for _, name := range []string{
			"orders", "orders.v2", "orders/paid", "orders 2", "заказы", "o",
			"  ", strings.Repeat("o", event.MaxNameBytes),
		} {
			agree(t, name)
			if _, err := projection.NewIdentity(name, projection.Ungenerated, projection.Whole()); err != nil {
				t.Fatalf("%q was refused as %v, and this constructor is the kernel's rule and the two delimiters, not a stricter one", name, err)
			}
		}
	})

	t.Run("every byte a name can carry answers the same at both doors", func(t *testing.T) {
		for held := range 256 {
			name := "a" + string([]byte{byte(held)}) + "b"
			if strings.ContainsAny(name, "@#") {
				continue
			}
			agree(t, name)
		}
	})

	t.Run("a name its generation and its partition push over the bound is refused too", func(t *testing.T) {
		name := strings.Repeat("o", event.MaxNameBytes-4)
		if _, err := projection.NewIdentity(name, 12, partitionOf(t, 3, 7)); err == nil {
			t.Fatal("a name that renders past the identifier bound beside its parts was admitted, and the bound is on what the row is keyed by")
		}
		if _, err := projection.NewIdentity(name, projection.Ungenerated, projection.Whole()); err != nil {
			t.Fatalf("the same name alone was refused as %v, so the arm above is about the bound rather than about the name", err)
		}
	})
}
