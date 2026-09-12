//go:build integration

package eventpg

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/projection"
)

// One runner's own walk of the log, counted where the walk is issued. Every
// runner gets its own, so what is summed at the end is N independent walks and
// not one shared reader — which is the thing Reject 3 would change and this
// phase does not.
type countedWalk struct {
	event.Log
	walks     atomic.Int64
	envelopes atomic.Int64
}

func countingWalk(log event.Log) *countedWalk { return &countedWalk{Log: log} }

func (this *countedWalk) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Log.ReadAll(ctx, after)
	this.walks.Add(1)
	this.envelopes.Add(int64(len(page)))
	return page, cursor, err
}

// §6.14, §UC-168. Eight walks cost eight times one projection's, measured
// against a one-projection baseline over the same log and RECORDED AS A NUMBER
// rather than stated — so that Reject 3's eventual shared-reader decision has
// evidence rather than an intuition ([SPEC] §8.5). No filter is added to
// Log.ReadAll to reduce it and no shared reader is introduced here.
func TestEightWalksCostEightTimesOneProjectionsReads(t *testing.T) {
	held := newProjectionCase(t, 24, 8)
	const keys = 20
	writePartitioned(t, held, "cost", keys)
	events := int64(2 * keys)

	baselineInto := held.destination(t, "cost_baseline")
	baseline := countingWalk(held.log)
	single := held.spec(t, "cost-baseline", baselineInto.attributing())
	single = inUnit(single, held.source, baselineInto.source)
	single.Log = baseline
	running := held.run(t, single)
	running.following(t, "the one-projection baseline drained the log")
	if got := baseline.envelopes.Load(); got != events {
		t.Fatalf("the baseline read %d envelopes over a log of %d, so the number the eight below are measured against is not one whole walk", got, events)
	}

	cover := coverOf(t, aPartition(t, 0, 3), aPartition(t, 1, 3), aPartition(t, 2, 3), aPartition(t, 3, 3))
	walks := make([]*countedWalk, 0, 8)
	for _, generation := range []projection.Generation{1, 2} {
		into := held.destination(t, fmt.Sprintf("cost_g%d", generation))
		for _, part := range cover.Partitions() {
			walk := countingWalk(held.log)
			walks = append(walks, walk)
			spec := inUnit(held.spec(t, "cost", into.attributing()), held.source, into.source)
			spec.Generation, spec.Partition, spec.Log = generation, part, walk
			running := held.run(t, spec)
			running.following(t, fmt.Sprintf("generation %d's partition %q drained its share", generation, part))
		}
	}

	var envelopes, issued int64
	for _, walk := range walks {
		envelopes += walk.envelopes.Load()
		issued += walk.walks.Load()
	}
	if envelopes < 8*events {
		t.Fatalf("two generations at four partitions each read %d envelopes over a log of %d, where eight independent walks read %d — so this case is not measuring eight walks",
			envelopes, events, 8*events)
	}
	t.Logf("N x M walks over one log: 8 runners (2 generations x 4 partitions) read %d envelopes in %d walks, against a one-projection baseline of %d envelopes in %d walks — %.1fx the read traffic",
		envelopes, issued, baseline.envelopes.Load(), baseline.walks.Load(), float64(envelopes)/float64(baseline.envelopes.Load()))

	// Every one of the eight read the whole log, which is what "independent" means
	// here and is the half a ratio alone would not say.
	for index, walk := range walks {
		if got := walk.envelopes.Load(); got < events {
			t.Fatalf("runner %d read %d envelopes of a log of %d, so the eight are not eight whole walks and the ratio above is not what it says", index, got, events)
		}
	}
}
