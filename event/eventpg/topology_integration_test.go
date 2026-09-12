//go:build integration

package eventpg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/projection"
)

// The rows a split moves, as PostgreSQL holds them and as psql prints them: the
// two children, the record of the retirement and the parent, named the way a
// checkpoint row is keyed. Every case in this file reads these rather than the
// Identity values the call answered, because what a handoff has to be right
// about is the table.
func splitRows(t *testing.T, held *projectionCase, parent projection.Identity) map[string]checkpointRow {
	t.Helper()
	lower, higher := childrenOf(t, parent)
	found := map[string]checkpointRow{}
	for _, name := range []string{parent.String(), lower.String(), higher.String(), parent.String() + "#split"} {
		if row, is := held.row(t, name); is {
			found[name] = row
		}
	}
	printed, asked := psqlAnswers(t, "SELECT projection FROM "+quoteIdentifier(held.schema.Name)+
		".checkpoints WHERE projection LIKE '"+parent.Projection()+"%' ORDER BY projection")
	if asked {
		named := make([]string, 0, len(found))
		for name := range found {
			named = append(named, name)
		}
		slices.Sort(named)
		slices.Sort(printed)
		if !slices.Equal(printed, named) {
			t.Fatalf("psql prints %v for the rows of %q where this process read %v, so one of the two is not reading the database", printed, parent.Projection(), named)
		}
	}
	return found
}

func childrenOf(t *testing.T, parent projection.Identity) (projection.Identity, projection.Identity) {
	t.Helper()
	below, above, err := parent.Partition().Split()
	if err != nil {
		t.Fatalf("the partition of %q cannot be split: %v", parent, err)
	}
	lower, err := projection.NewIdentity(parent.Projection(), parent.Generation(), below)
	if err != nil {
		t.Fatalf("the lower child of %q was refused: %v", parent, err)
	}
	higher, err := projection.NewIdentity(parent.Projection(), parent.Generation(), above)
	if err != nil {
		t.Fatalf("the higher child of %q was refused: %v", parent, err)
	}
	return lower, higher
}

func splitting(t *testing.T, held *projectionCase, checkpoints event.Checkpoints, parent projection.Identity) (projection.Identity, projection.Identity, error) {
	t.Helper()
	return projection.Split(context.WithoutCancel(t.Context()), projection.SplitSpec{
		Checkpoints: checkpoints,
		Identity:    parent,
		Unit: func(ctx context.Context, work func(context.Context) error) error {
			return crud.InNewTx(ctx, held.source, work)
		},
	})
}

// A checkpoint store that refuses its Nth save and answers everything else. The
// refusal is the only thing this adds: the three writes below it have already
// reached PostgreSQL inside the caller's transaction, so what rolls them back is
// the database rather than anything this value did.
type failingCheckpoints struct {
	event.Checkpoints
	failAt int
	saves  atomic.Int64
}

var errInjectedBetweenTheChildren = errors.New("eventpg_test: this save was refused between the two child writes of a split")

func (this *failingCheckpoints) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	if int(this.saves.Add(1)) == this.failAt {
		return event.Failure(event.Refused, errInjectedBetweenTheChildren)
	}
	return this.Checkpoints.Save(ctx, checkpoint)
}

// §6.3, §UC-138, §INV-087. The handoff is one transaction of the caller's, and a
// failure injected between the two child writes leaves NEITHER child row — not
// the one that was written before the refusal and not the one that was never
// reached. The parent's row is still exactly where it was, which is the half
// that makes the refusal recoverable rather than a topology nobody can read.
func TestASplitsThreeStatementsAreOneTransaction(t *testing.T) {
	held := newProjectionCase(t, 8, 0)
	into := held.destination(t, "handoff_read_model")

	parent := partitionedIdentity(t, "handoff", 0, 1)
	writePartitioned(t, held, "handoff", 12)
	running := held.run(t, partitionedSpec(t, held, "handoff", into, aPartition(t, 0, 1)))
	running.following(t, "the parent partition drained its share of the log")
	running.stop(t)

	before, found := held.row(t, parent.String())
	if !found {
		t.Fatalf("the parent %q holds no row, so there is nothing for a split to hand down", parent)
	}

	failing := &failingCheckpoints{Checkpoints: held.checkpoints(t), failAt: 2}
	_, _, err := splitting(t, held, failing, parent)
	if !errors.Is(err, projection.ErrTopology) || !errors.Is(err, errInjectedBetweenTheChildren) {
		t.Fatalf("the split whose second child write was refused answered %v, where the refusal travels as a topology change that did not happen", err)
	}

	rows := splitRows(t, held, parent)
	lower, higher := childrenOf(t, parent)
	for _, child := range []projection.Identity{lower, higher} {
		if row, is := rows[child.String()]; is {
			t.Fatalf("the child %q stands at advance %d after a split whose second child write was refused, so the two writes are not one transaction and one share of the log now has two writers",
				child, row.advance)
		}
	}
	if _, is := rows[parent.String()+"#split"]; is {
		t.Fatalf("the record retiring %q survives a split that did not happen, and a runner started at that share would halt over a handoff nothing made", parent)
	}
	after, still := rows[parent.String()]
	if !still {
		t.Fatalf("the parent %q lost its row to a split that was refused, which is the one absence [[D-133]] cannot tell from a restore", parent)
	}
	if after.advance != before.advance || !slices.Equal(after.cursor, before.cursor) {
		t.Fatalf("the parent stands at advance %d where it stood at %d before the refused split, so the rollback did not take everything back", after.advance, before.advance)
	}

	// The control: the same split with nothing injected writes the two children
	// at the parent's exact cursor, records the retirement and removes the
	// parent — so the emptiness above is the injection's and not the call's.
	t.Run("the control: the same split with nothing injected makes the handoff", func(t *testing.T) {
		answeredLower, answeredHigher, err := splitting(t, held, held.checkpoints(t), parent)
		if err != nil {
			t.Fatalf("the split that was not interfered with answered %v", err)
		}
		rows := splitRows(t, held, parent)
		if _, still := rows[parent.String()]; still {
			t.Fatalf("the parent %q still holds a row after its split, so the share it recorded has two writers", parent)
		}
		if _, recorded := rows[parent.String()+"#split"]; !recorded {
			t.Fatalf("nothing records that %q was retired by a split, so the release that ran before it is admitted at its next deploy", parent)
		}
		for _, child := range []projection.Identity{answeredLower, answeredHigher} {
			row, is := rows[child.String()]
			if !is {
				t.Fatalf("the child %q holds no row after the split that answered it", child)
			}
			if row.advance != 1 {
				t.Fatalf("the child %q stands at advance %d where a row nothing else wrote is written at 1", child, row.advance)
			}
			if !slices.Equal(row.cursor, before.cursor) {
				t.Fatalf("the child %q carries a cursor of %x where the parent's was %x, and a child that does not start at the parent's exact cursor skips or re-delivers its whole share",
					child, row.cursor, before.cursor)
			}
		}
		if got := rows[answeredLower.String()].applied + rows[answeredHigher.String()].applied; got != before.applied {
			t.Fatalf("the children account for %d applied where the parent accounted for %d, and the sum across a partition set is what a split does not change", got, before.applied)
		}
	})
}

// §6.4, §UC-139. A split committed under a running parent takes its row away,
// and the parent's next save finds no row at its advance: it HALTS rather than
// creating a fresh row beside its children, and the halt is [[D-133]]'s absent-row
// arm rather than contention.
func TestARunningParentHaltsWhenItsRowIsSplitAway(t *testing.T) {
	held := newProjectionCase(t, 8, 0)
	into := held.destination(t, "orphaned_read_model")

	parent := partitionedIdentity(t, "orphaned", 0, 1)
	writePartitioned(t, held, "orphaned", 12)
	running := held.run(t, partitionedSpec(t, held, "orphaned", into, aPartition(t, 0, 1)))
	running.following(t, "the parent partition drained its share of the log")

	if _, _, err := splitting(t, held, held.checkpoints(t), parent); err != nil {
		t.Fatalf("the split under a running parent answered %v, where a split reads rows and this parent has one", err)
	}
	// Enough of the parent's own share to guarantee its next pass has a page to
	// save for: a parent with nothing to apply never reaches its fence.
	writePartitioned(t, held, "orphaned-after", 24)

	failure := running.halted(t, "the parent whose row was split away halted at its next save")
	if errors.Is(failure, projection.ErrOvertaken) {
		t.Fatalf("the parent reports %v, and contention does not halt — a parent that read this as a second writer would take the row its own children are recording through", failure)
	}
	if row, found := held.row(t, parent.String()); found {
		t.Fatalf("the halted parent left a row at advance %d beside its children, which is one share of the log with two writers and two watermarks", row.advance)
	}

	// The two children cover the parent's whole key space and keep going, which
	// is what makes the halt a handoff rather than an outage.
	lower, higher := childrenOf(t, parent)
	for _, child := range []projection.Identity{lower, higher} {
		child := child
		running := held.run(t, partitionedSpec(t, held, "orphaned", into, child.Partition()))
		running.following(t, fmt.Sprintf("the child %q drained its share", child))
	}
	waitFor(t, "the two children applied everything written after the split", func() bool {
		return into.count(t) == 36
	})

	// The control: a split of a DRAINED parent halts nothing, so the halt above
	// is attributable to the missed drain and not to Split.
	t.Run("the control: a split of a drained parent halts nothing", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		into := held.destination(t, "drained_read_model")
		parent := partitionedIdentity(t, "drained", 0, 1)
		writePartitioned(t, held, "drained", 12)
		running := held.run(t, partitionedSpec(t, held, "drained", into, aPartition(t, 0, 1)))
		running.following(t, "the parent partition drained its share of the log")
		running.stop(t)

		lower, higher, err := splitting(t, held, held.checkpoints(t), parent)
		if err != nil {
			t.Fatalf("the split of a drained parent answered %v", err)
		}
		writePartitioned(t, held, "drained-after", 24)
		for _, child := range []projection.Identity{lower, higher} {
			child := child
			running := held.run(t, partitionedSpec(t, held, "drained", into, child.Partition()))
			running.following(t, fmt.Sprintf("the child %q of a drained parent drained its share", child))
		}
		waitFor(t, "the two children of a drained parent applied everything", func() bool {
			return into.count(t) == 36
		})
	})
}

// §6.16, §UC-140. A partition whose row is absent is refused whatever the reason
// for the absence, and the refusal writes nothing — read in psql, because the
// dangerous outcome is two children at the origin against a live read model and
// that is a pair of rows rather than an answer.
func TestASplitWithNoParentRowWritesNothing(t *testing.T) {
	held := newProjectionCase(t, 8, 0)

	for _, absence := range []struct {
		what  string
		stand func(t *testing.T, parent projection.Identity)
	}{
		{"a partition that has genuinely never run", func(*testing.T, projection.Identity) {}},
		{"a partition that stood at a known position and whose row was lost", func(t *testing.T, parent projection.Identity) {
			into := held.destination(t, "lost_read_model_"+strconv.Itoa(int(parent.Partition().ID())))
			writePartitioned(t, held, "lost", 12)
			running := held.run(t, partitionedSpec(t, held, parent.Projection(), into, parent.Partition()))
			running.following(t, "the partition whose row is about to be lost drained its share")
			running.stop(t)
			if row, found := held.row(t, parent.String()); !found || row.highest == 0 {
				t.Fatalf("the partition %q recorded nothing, so the row this case is about to drop was never there", parent)
			}
			mustExecute(t, "DELETE FROM "+quoteIdentifier(held.schema.Name)+".checkpoints WHERE projection = $1", parent.String())
		}},
	} {
		t.Run(absence.what, func(t *testing.T) {
			parent := partitionedIdentity(t, "absent-"+strings.Fields(absence.what)[2], 0, 1)
			absence.stand(t, parent)

			lower, higher, err := splitting(t, held, held.checkpoints(t), parent)
			if !errors.Is(err, projection.ErrTopology) {
				t.Fatalf("the split of a partition with no row answered %v, where both readings of the absence are refused", err)
			}
			if lower.Projection() != "" || higher.Projection() != "" {
				t.Fatalf("the refused split answered the children %q and %q, and an answer nobody wrote rows for is the arm that made the dangerous reading look like the safe one", lower, higher)
			}
			for _, reading := range []string{"never ran", "forgotten, dropped or restored in part"} {
				if !strings.Contains(err.Error(), reading) {
					t.Fatalf("the refusal does not name the reading %q, so an operator is told which remedy to reach for by nothing: %v", reading, err)
				}
			}
			if rows := splitRows(t, held, parent); len(rows) != 0 {
				t.Fatalf("the refused split left %v in the checkpoint table, and a child at the origin walks the whole log into a live read model", rows)
			}
		})
	}

	// The control: a split of a partition that HAS run writes its children and
	// answers them, so the refusal above is about the absence rather than about
	// every split.
	t.Run("the control: a split of a partition that has run answers its children", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		into := held.destination(t, "hasrun_read_model")
		parent := partitionedIdentity(t, "hasrun", 0, 1)
		writePartitioned(t, held, "hasrun", 12)
		running := held.run(t, partitionedSpec(t, held, "hasrun", into, aPartition(t, 0, 1)))
		running.following(t, "the partition that has run drained its share")
		running.stop(t)

		lower, higher, err := splitting(t, held, held.checkpoints(t), parent)
		if err != nil {
			t.Fatalf("the split of a partition that has run answered %v", err)
		}
		rows := splitRows(t, held, parent)
		if len(rows) != 3 {
			t.Fatalf("the split left %v where the two children and the record of the retirement are three rows", rows)
		}
		if _, is := rows[lower.String()]; !is {
			t.Fatalf("the lower child %q holds no row", lower)
		}
		if _, is := rows[higher.String()]; !is {
			t.Fatalf("the higher child %q holds no row", higher)
		}
	})
}
