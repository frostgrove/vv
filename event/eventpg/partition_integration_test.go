//go:build integration

package eventpg

import (
	"context"
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event/projection"
)

func aPartition(t *testing.T, id, mask uint32) projection.Partition {
	t.Helper()
	held, err := projection.NewPartition(id, mask)
	if err != nil {
		t.Fatalf("the partition %d.%d was refused: %v", id, mask, err)
	}
	return held
}

func partitionedIdentity(t *testing.T, name string, id, mask uint32) projection.Identity {
	t.Helper()
	held, err := projection.NewIdentity(name, projection.Ungenerated, aPartition(t, id, mask))
	if err != nil {
		t.Fatalf("the identity of %q at %d.%d was refused: %v", name, id, mask, err)
	}
	return held
}

// A set that was checked, which is the one value that makes a gap and an overlap
// unconstructible: every runner below is built from Partitions() and from
// nothing else, which is §8.7's decision spelled as code.
func coverOf(t *testing.T, parts ...projection.Partition) projection.Cover {
	t.Helper()
	held, err := projection.NewCover(parts...)
	if err != nil {
		t.Fatalf("the cover over %v was refused: %v", parts, err)
	}
	return held
}

// Two events a key — placed then paid — because what a partition has to get
// right is the ORDER within a key and a log of one event a key cannot be wrong
// about it.
func writePartitioned(t *testing.T, held *projectionCase, tag string, keys int) {
	t.Helper()
	for index := range keys {
		stream := aStream(ordersFamily, fmt.Sprintf("%s/%d", tag, index))
		held.write(t, stream, ordersFamily+".placed", fmt.Sprintf("%s-%d-placed", tag, index))
		held.write(t, stream, ordersFamily+".paid", fmt.Sprintf("%s-%d-paid", tag, index))
	}
}

// The payload and the identity that applied it, in one column, so the table
// answers both questions this section asks of it: what was applied, and by
// which partition.
func (this *destination) attributing() projection.Handler {
	return projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
		for _, envelope := range batch.Envelopes {
			if _, err := this.on(ctx).ExecContext(ctx, "INSERT INTO "+this.table+" (payload) VALUES ($1)",
				string(envelope.Payload)+"|"+batch.Identity.String()); err != nil {
				return err
			}
		}
		return nil
	})
}

func partitionedSpec(t *testing.T, on *projectionCase, name string, into *destination, part projection.Partition) projection.Spec {
	t.Helper()
	spec := inUnit(on.spec(t, name, into.attributing()), on.source, into.source)
	spec.Partition = part
	return spec
}

func appliedBy(rows []string) map[string]string {
	held := map[string]string{}
	for _, row := range rows {
		payload, identity, _ := strings.Cut(row, "|")
		held[payload] = identity
	}
	return held
}

func payloadsOnly(rows []string) []string {
	held := make([]string, 0, len(rows))
	for _, row := range rows {
		payload, _, _ := strings.Cut(row, "|")
		held = append(held, payload)
	}
	return held
}

// §6.2, §UC-135, §UC-138, §INV-086. Four runners at mask 3 over one log and one
// destination: every event is applied exactly once, every key's two events are
// applied in the log's order, no envelope is applied by two partitions, and the
// four checkpoint rows advance independently.
//
// The control beside it is the whole reason the arm above means anything. It
// re-partitions the same log by `hash % N -> hash % (N+1)` — the arithmetic the
// mask replaced — with per-partition cursors of exactly the kind four
// independent checkpoint rows produce, and asserts that a key's `paid` lands
// BEFORE its `placed`, and that another key's events are never applied at all.
// A green positive case beside a control that found no breakage would be
// measuring nothing.
func TestFourPartitionsOverOneLogAndTheModulusControl(t *testing.T) {
	held := newProjectionCase(t, 16, 0)
	into := held.destination(t, "partitioned_read_model")
	cover := coverOf(t, aPartition(t, 0, 3), aPartition(t, 1, 3), aPartition(t, 2, 3), aPartition(t, 3, 3))

	const keys = 24
	writePartitioned(t, held, "part", keys)

	for _, part := range cover.Partitions() {
		running := held.run(t, partitionedSpec(t, held, "part", into, part))
		running.following(t, fmt.Sprintf("the partition %q drained its share of the log", part))
	}
	waitFor(t, "the four partitions between them applied the whole log", func() bool {
		return into.count(t) == 2*keys
	})

	rows := into.rows(t)
	if len(rows) != 2*keys {
		t.Fatalf("the four partitions left %d rows over a log of %d events, so an envelope was applied twice or by nobody", len(rows), 2*keys)
	}
	applied := appliedBy(rows)
	if len(applied) != 2*keys {
		t.Fatalf("the four partitions left %d distinct payloads over a log of %d events, so an envelope reached two partitions", len(applied), 2*keys)
	}

	// Every key's two events landed in the log's order, read off the bigserial
	// nothing here names, and both landed in the partition its own key matches.
	ordered := payloadsOnly(rows)
	for index := range keys {
		placed := fmt.Sprintf("part-%d-placed", index)
		paid := fmt.Sprintf("part-%d-paid", index)
		if slices.Index(ordered, placed) > slices.Index(ordered, paid) {
			t.Fatalf("%q was applied after %q, and two events of one key out of order is the read-model corruption a partition set exists to prevent", placed, paid)
		}
		key := sequenceOf(ordersFamily, fmt.Sprintf("part/%d", index))
		owner := ""
		for _, part := range cover.Partitions() {
			if part.Matches(key) {
				if owner != "" {
					t.Fatalf("the key %q matches %q and %q, and a checked cover admits no key that matches two members", key, owner, part)
				}
				owner = partitionedIdentity(t, "part", part.ID(), part.Mask()).String()
			}
		}
		for _, payload := range []string{placed, paid} {
			if applied[payload] != owner {
				t.Fatalf("%q was applied by %q where its key %q belongs to %q", payload, applied[payload], key, owner)
			}
		}
	}

	// Four rows, independently advanced, and the applied counts sum to the log.
	var summed int64
	for _, part := range cover.Partitions() {
		name := partitionedIdentity(t, "part", part.ID(), part.Mask()).String()
		row, found := held.row(t, name)
		if !found {
			t.Fatalf("the partition %q holds no checkpoint row, so a restart would walk the whole log again into a live read model", name)
		}
		summed += row.applied
	}
	if summed != 2*keys {
		t.Fatalf("the four checkpoint rows account for %d applied over a log of %d, and a partitioned projection's progress is the sum of its rows", summed, 2*keys)
	}

	t.Run("the control: hash % N to hash % (N+1) reorders and skips the same log", func(t *testing.T) {
		log := make([]string, 0, 2*keys)
		for index := range keys {
			log = append(log, fmt.Sprintf("part/%d", index), fmt.Sprintf("part/%d", index))
		}
		walk := walkingByModulus(log, 3)
		walk.advance()
		walk.repartitionTo(4)
		walk.advance()

		reordered, skipped := walk.damage(log)
		if reordered == 0 {
			t.Fatal("re-partitioning by a modulus applied no key's second event before its first, so the positive case above is measured against a control that found nothing")
		}
		if skipped == 0 {
			t.Fatal("re-partitioning by a modulus skipped no event, so the other half of what a mask buys is unmeasured")
		}
		t.Logf("hash %% 3 -> hash %% 4 over %d events: %d keys applied out of order and %d events never applied at all", len(log), reordered, skipped)

		// Beside it, the mask: every key the parent matched is matched by exactly
		// one child and by no partition of any other share, which is the whole
		// difference and is arithmetic rather than a measurement.
		parent := aPartition(t, 1, 1)
		below, above, err := parent.Split()
		if err != nil {
			t.Fatalf("the split of %q was refused: %v", parent, err)
		}
		for index := range keys {
			key := sequenceOf(ordersFamily, fmt.Sprintf("part/%d", index))
			matched := 0
			for _, child := range []projection.Partition{below, above} {
				if child.Matches(key) {
					matched++
				}
			}
			if parent.Matches(key) != (matched == 1) {
				t.Fatalf("the key %q is matched by the parent %v and by %d of its children, where a split moves no key out of the parent's half of the space", key, parent.Matches(key), matched)
			}
		}
	})
}

// The arithmetic a mask replaced, simulated over one log. It is not a projection
// and there is no spelling of it in this framework — which is the point: what is
// measured is what a partition count that CAN change costs, against a topology
// change that hands a cursor down instead.
type modulusWalk struct {
	log     []string
	count   int
	cursors []int
	applied []string
}

func walkingByModulus(log []string, count int) *modulusWalk {
	return &modulusWalk{log: log, count: count, cursors: make([]int, count)}
}

func modulusOf(key string, count int) int {
	held := fnv.New32a()
	_, _ = held.Write([]byte(key))
	return int(held.Sum32() % uint32(count))
}

// Each partition walks from its own cursor, applying what it owns, and stops
// after its own share of the work — which is what four independent checkpoint
// rows over one log look like from the outside. The first round leaves them at
// four different positions; that is the premise the re-partitioning below is
// measured against, and it is a fact about the design rather than a rigging of
// this fixture.
func (this *modulusWalk) advance() {
	for partition := range this.cursors {
		budget := partition + 2
		index := this.cursors[partition]
		for ; index < len(this.log) && budget > 0; index++ {
			if modulusOf(this.log[index], this.count) != partition%this.count {
				continue
			}
			this.applied = append(this.applied, fmt.Sprintf("%s#%d", this.log[index], index))
			budget--
		}
		this.cursors[partition] = index
	}
}

// The partition count changes and every cursor stays exactly where it was, which
// is the only thing a deployment doing this can do: a cursor is a store's bytes
// and there is no arithmetic that moves one to where another partition's key
// space now begins.
func (this *modulusWalk) repartitionTo(count int) {
	this.count = count
	for len(this.cursors) < count {
		this.cursors = append(this.cursors, 0)
	}
}

// Two numbers, and they are the two directions the damage runs in: a key whose
// later event was applied before its earlier one, and a key whose events were
// never applied at all.
func (this *modulusWalk) damage(log []string) (reordered, skipped int) {
	seen := map[string][]int{}
	for _, row := range this.applied {
		_, at, _ := strings.Cut(row, "#")
		index, _ := strconv.Atoi(at)
		key := log[index]
		seen[key] = append(seen[key], index)
	}
	for _, positions := range seen {
		if !slices.IsSorted(positions) {
			reordered++
		}
	}
	for index, key := range log {
		if !slices.Contains(seen[key], index) {
			skipped++
		}
	}
	return reordered, skipped
}
