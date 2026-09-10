package projection_test

import (
	"context"
	"errors"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

func keys(count int) []string {
	held := make([]string, 0, count)
	for index := range count {
		held = append(held, "orders/"+strconv.Itoa(index))
	}
	return held
}

// The ceiling is published so a deployment can read it, and it is a refusal
// rather than a wrap: a mask that wrapped would move every key at once, which is
// the modulus failure this whole shape exists to refuse.
func TestASplitAtTheCeilingIsRefusedAndOneBelowItSucceeds(t *testing.T) {
	ceiling := partitionOf(t, 0, projection.MaxPartitions-1)

	lower, higher, err := ceiling.Split()
	if err == nil {
		t.Fatalf("a partition at the finest mask split into %q and %q", lower, higher)
	}
	if !errors.Is(err, projection.ErrTopology) {
		t.Fatalf("a split at the ceiling was refused as %v, which is not the class a topology change carries", err)
	}
	if !strings.Contains(err.Error(), "MaxPartitions") {
		t.Fatalf("a split at the ceiling was refused with %q, which does not name the ceiling", err)
	}
	if lower != (projection.Partition{}) || higher != (projection.Partition{}) {
		t.Fatalf("the refused split answered %q and %q, and a mask that wrapped would move every key", lower, higher)
	}

	for _, beyond := range []struct{ id, mask uint32 }{
		{0, projection.MaxPartitions*2 - 1},
		{0, 4294967295},
		{projection.MaxPartitions, projection.MaxPartitions - 1},
		{0, 6},
		{2, 1},
	} {
		if held, err := projection.NewPartition(beyond.id, beyond.mask); err == nil {
			t.Fatalf("NewPartition(%d, %d) answered %q, and nothing beyond the ceiling or off the mask rule is constructible", beyond.id, beyond.mask, held)
		} else if !errors.Is(err, projection.ErrTopology) {
			t.Fatalf("NewPartition(%d, %d) was refused as %v", beyond.id, beyond.mask, err)
		}
	}

	t.Run("the control: one below the ceiling splits", func(t *testing.T) {
		parent := partitionOf(t, 1, projection.MaxPartitions/2-1)
		lower, higher, err := parent.Split()
		if err != nil {
			t.Fatalf("splitting %q one below the ceiling answered %v, so the refusal above refuses everything", parent, err)
		}
		if lower.Mask() != projection.MaxPartitions-1 || higher.Mask() != projection.MaxPartitions-1 {
			t.Fatalf("the children of %q are at masks %d and %d", parent, lower.Mask(), higher.Mask())
		}
		if lower.Count() != projection.MaxPartitions || lower.ID() != 1 || higher.ID() != 1+projection.MaxPartitions/2 {
			t.Fatalf("the children of %q are %q and %q at a count of %d", parent, lower, higher, lower.Count())
		}
	})
}

// The property the whole mechanism rests on, read over many keys rather than
// argued: a split divides its own share and touches nothing else. The hash arm is
// beside it because the published hash is what makes the property reproducible in
// another process — a different hash is the same failure as a different modulus.
func TestAMaskMovesNoKeyOutOfTheParentsHalfOfTheSpace(t *testing.T) {
	parent := partitionOf(t, 1, 1)
	sibling := partitionOf(t, 0, 1)
	lower, higher, err := parent.Split()
	if err != nil {
		t.Fatalf("splitting %q answered %v", parent, err)
	}

	moved, kept, split := 0, 0, [2]int{}
	for _, key := range keys(2000) {
		switch {
		case parent.Matches(key):
			inLower, inHigher := lower.Matches(key), higher.Matches(key)
			if inLower == inHigher {
				t.Fatalf("%q was in %q and is in %q:%v and %q:%v, and every key of a split parent is in exactly one child", key, parent, lower, inLower, higher, inHigher)
			}
			if inLower {
				split[0]++
			} else {
				split[1]++
			}
			kept++
		case sibling.Matches(key):
			if lower.Matches(key) || higher.Matches(key) {
				t.Fatalf("%q belongs to %q and a split of %q took it, so a split moved a key out of its own half", key, sibling, parent)
			}
			moved++
		default:
			t.Fatalf("%q matched neither half of the space, and {0,1} and {1,1} are a cover of it", key)
		}
	}
	if kept == 0 || moved == 0 || split[0] == 0 || split[1] == 0 {
		t.Fatalf("%d keys stayed with the parent (%d and %d across its children) and %d were the sibling's, so at least one arm above was never reached", kept, split[0], split[1], moved)
	}

	t.Run("the hash is FNV-1a/32 over the sequence key, and that is what a second process reproduces", func(t *testing.T) {
		for _, key := range keys(200) {
			digest := fnv.New32a()
			if _, err := digest.Write([]byte(key)); err != nil {
				t.Fatalf("hashing %q answered %v", key, err)
			}
			expected := digest.Sum32() & 3
			for id := range uint32(4) {
				part := partitionOf(t, id, 3)
				if matched := part.Matches(key); matched != (id == expected) {
					t.Fatalf("%q is matched by %q: %v, where FNV-1a/32 of it under the mask 3 is %d", key, part, matched, expected)
				}
			}
		}
	})

	t.Run("the whole key space matches every key and is what a spec names by default", func(t *testing.T) {
		whole := projection.Whole()
		if !whole.Whole() || whole.Count() != 1 || whole.String() != "" {
			t.Fatalf("the whole key space is %+v, counts %d and renders %q", whole, whole.Count(), whole)
		}
		for _, key := range keys(50) {
			if !whole.Matches(key) {
				t.Fatalf("the whole key space did not match %q", key)
			}
		}
		lower, higher, err := whole.Split()
		if err != nil {
			t.Fatalf("splitting the whole key space answered %v", err)
		}
		if lower.String() != "0.1" || higher.String() != "1.1" {
			t.Fatalf("the whole key space split into %q and %q", lower, higher)
		}
	})
}

// What a partition applied, recorded per envelope and per stream so the two
// questions a cover answers can be asked separately: every event applied exactly
// once across the set, and the events of one stream applied in that stream's
// order by one member.
type applications struct {
	mutex sync.Mutex
	by    map[string][]string
	order map[string][]event.Version
	calls atomic.Int64
}

func newApplications() *applications {
	return &applications{by: map[string][]string{}, order: map[string][]event.Version{}}
}

func (this *applications) records(part projection.Partition) projection.HandlerFunc {
	return func(_ context.Context, batch projection.Batch) error {
		this.calls.Add(1)
		this.mutex.Lock()
		defer this.mutex.Unlock()
		for _, envelope := range batch.Envelopes {
			key := string(envelope.Stream.Key)
			this.by[key+"/"+strconv.Itoa(int(envelope.Version))] = append(this.by[key+"/"+strconv.Itoa(int(envelope.Version))], part.String())
			this.order[key] = append(this.order[key], envelope.Version)
		}
		return nil
	}
}

// An envelope nobody applied has no entry at all, so "never" is counted against
// the log's own size rather than read off the map — the one of the three that a
// tally of applications cannot see by itself.
func (this *applications) counted(logged int) (once, twice, never int) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	for _, held := range this.by {
		if len(held) == 1 {
			once++
			continue
		}
		twice++
	}
	return once, twice, logged - len(this.by)
}

// Which member applied each envelope, which is the only place a sequencer's
// answer is observable from outside the loop: a test that compared the keys
// would be re-deriving the mapping rather than reading where the events landed.
func (this *applications) placed() map[string]string {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held := map[string]string{}
	for envelope, parts := range this.by {
		held[envelope] = strings.Join(parts, "+")
	}
	return held
}

func (this *applications) members() map[string]int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held := map[string]int{}
	for _, applied := range this.by {
		for _, part := range applied {
			held[part]++
		}
	}
	return held
}

// The whole of what a cover is for, read over events rather than argued from the
// arithmetic: four runners over one log apply every event exactly once and keep
// each stream in its own order. Both controls are what make the positive case
// evidence — one breaks the key and the other breaks its composition, and each
// asserts the breakage is there.
func TestFourPartitionsApplyEveryEventOnceAndKeepEachKeyInOrder(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	streams := 12
	for index := range streams {
		stand.append(t, "k"+strconv.Itoa(index), "one", "two", "three")
	}
	cover := coverOf(t, partitionOf(t, 0, 3), partitionOf(t, 1, 3), partitionOf(t, 2, 3), partitionOf(t, 3, 3))
	seen := newApplications()

	for _, part := range cover.Partitions() {
		spec := stand.spec("orders", seen.records(part))
		spec.Partition = part
		running(t, newProjection(t, spec))
	}
	for _, part := range cover.Partitions() {
		stand.observer.await(t, "the partition "+part.String()+" followed", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing && state.Identity.Partition() == part
		})
	}

	once, twice, none := seen.counted(36)
	if once != streams*3 || twice != 0 || none != 0 {
		t.Fatalf("%d events were applied once, %d more than once and %d never, over a log of %d — a cover delivers every event to exactly one member", once, twice, none, streams*3)
	}
	for key, versions := range seen.order {
		for index := range versions {
			if versions[index] != event.Version(index+1) {
				t.Fatalf("the stream %q was applied in the order %v, and the events of one key are applied in the log's order", key, versions)
			}
		}
	}
	applied := uint64(0)
	for _, part := range cover.Partitions() {
		row := stand.row(t, "orders#"+part.String())
		if row.Fresh() {
			t.Fatalf("the partition %q recorded no checkpoint, so its share of the log is re-read from the origin at every restart", part)
		}
		if row.Progress.Highest != event.Position(streams*3) {
			t.Fatalf("the partition %q reports the watermark %d over a log of %d, and Highest is the read page's last position rather than what this member applied", part, row.Progress.Highest, streams*3)
		}
		applied += row.Progress.Applied
	}
	if applied != uint64(streams*3) {
		t.Fatalf("the four rows account for %d applied envelopes over a log of %d, and the sum across the set is what an operator reads", applied, streams*3)
	}
	if counts := seen.members(); len(counts) != 4 {
		t.Fatalf("%v applied the log, and four independent members were started", counts)
	}

	t.Run("the control: an unstable key leaves events applied twice or not at all", func(t *testing.T) {
		other := newStand(t, eventmemory.Spec{})
		for index := range streams {
			other.append(t, "k"+strconv.Itoa(index), "one", "two", "three")
		}
		fresh := newApplications()
		var minted atomic.Int64
		for _, part := range cover.Partitions() {
			spec := other.spec("orders", fresh.records(part))
			spec.Partition = part
			spec.Sequence = projection.SequenceBy("fresh-every-call", func(event.Envelope) string {
				return strconv.FormatInt(minted.Add(1), 10)
			})
			built := newProjection(t, spec)
			cancel, returned := running(t, built)
			other.observer.await(t, "the partition "+part.String()+" followed", func(state projection.State) bool {
				return state.Phase == projection.PhaseFollowing && state.Identity.Partition() == part
			})
			cancel()
			_ = returns(t, returned)
		}
		once, twice, none := fresh.counted(36)
		if twice == 0 && none == 0 && once == streams*3 {
			t.Fatalf("a key minted fresh on every call still delivered all %d events exactly once, so the positive case above is not measuring the key at all", once)
		}
	})

	t.Run("the control: a family-only key puts the whole family in one partition", func(t *testing.T) {
		other := newStand(t, eventmemory.Spec{})
		for index := range streams {
			other.append(t, "k"+strconv.Itoa(index), "one", "two", "three")
		}
		family := newApplications()
		for _, part := range cover.Partitions() {
			spec := other.spec("orders", family.records(part))
			spec.Partition = part
			spec.Sequence = projection.SequenceBy("family-only", func(envelope event.Envelope) string {
				return envelope.Stream.Family
			})
			running(t, newProjection(t, spec))
		}
		for _, part := range cover.Partitions() {
			other.observer.await(t, "the partition "+part.String()+" followed", func(state projection.State) bool {
				return state.Phase == projection.PhaseFollowing && state.Identity.Partition() == part
			})
		}
		counts := family.members()
		if len(counts) != 1 {
			t.Fatalf("a key that is the family alone spread the log over %v, and every stream of one family renders one key", counts)
		}
		for _, applied := range counts {
			if applied != streams*3 {
				t.Fatalf("the one partition a family-only key reaches applied %d of %d", applied, streams*3)
			}
		}
	})
}

// A page holding nothing this partition matches still advances: the checkpoint
// is what says this runner has read past that position, and a member that saved
// nothing would re-walk the whole log at every restart. The control is the
// partition that matched the same page whole, so the two ends agree on the cursor
// and differ only in what was applied.
func TestAPageThatMatchesNothingAdvancesAndCallsNoHandler(t *testing.T) {
	matching, missing := projection.Whole(), projection.Whole()
	for id := range uint32(4) {
		part := partitionOf(t, id, 3)
		if part.Matches("all") {
			matching = part
			continue
		}
		missing = part
	}
	if matching.Whole() || missing.Whole() {
		t.Fatalf("the one key %q reaches %q and the arms below need a member that misses it too", "all", matching)
	}

	stand := newStand(t, eventmemory.Spec{})
	for index := range 4 {
		stand.append(t, "k"+strconv.Itoa(index), "one", "two")
	}
	seen := newApplications()
	spec := stand.spec("orders", seen.records(missing))
	spec.Partition, spec.Sequence = missing, projection.OneSequence()
	running(t, newProjection(t, spec))
	stand.observer.await(t, "the partition that matches nothing followed", func(state projection.State) bool {
		return state.Phase == projection.PhaseFollowing && state.Identity.Partition() == missing
	})

	if calls := seen.calls.Load(); calls != 0 {
		t.Fatalf("the handler was called %d times for a page none of whose envelopes this partition matches, and a batch of no envelopes is a delivery that never happened", calls)
	}
	row := stand.row(t, "orders#"+missing.String())
	if row.Fresh() {
		t.Fatalf("the partition %q saved nothing for a page it matched nothing in, so it re-walks the whole log at every restart", missing)
	}
	if row.Progress.Applied != 0 || row.Progress.Highest != 8 {
		t.Fatalf("the partition %q reports %d applied at the watermark %d, and a page it matched nothing in advances the cursor and applies none of it", missing, row.Progress.Applied, row.Progress.Highest)
	}

	t.Run("the control: the partition that matched the same page advances by the same cursor", func(t *testing.T) {
		took := newApplications()
		other := stand.spec("orders", took.records(matching))
		other.Partition, other.Sequence = matching, projection.OneSequence()
		running(t, newProjection(t, other))
		stand.observer.await(t, "the partition that matches everything followed", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing && state.Identity.Partition() == matching
		})

		held := stand.row(t, "orders#"+matching.String())
		if held.Progress.Applied != 8 || held.Progress.Highest != 8 {
			t.Fatalf("the partition %q reports %d applied at the watermark %d over a page of eight", matching, held.Progress.Applied, held.Progress.Highest)
		}
		if held.Cursor != row.Cursor {
			t.Fatalf("the two members stopped at different cursors, %q and %q, and the page they read is one page", held.Cursor, row.Cursor)
		}
	})
}

// The two sequencers a projection with no ordering requirement and one with a
// total one are named by, told apart by where the log landed rather than by the
// keys they answered: Unordered spreads it over the whole cover, OneSequence
// puts it in one member, and the three that receive nothing still advance.
func TestUnorderedSpreadsAndOneSequenceConcentrates(t *testing.T) {
	cover := coverOf(t, partitionOf(t, 0, 3), partitionOf(t, 1, 3), partitionOf(t, 2, 3), partitionOf(t, 3, 3))

	spread := newStand(t, eventmemory.Spec{})
	for index := range 12 {
		spread.append(t, "k"+strconv.Itoa(index), "one", "two", "three")
	}
	scattered := newApplications()
	for _, part := range cover.Partitions() {
		spec := spread.spec("orders", scattered.records(part))
		spec.Partition, spec.Sequence = part, projection.Unordered()
		running(t, newProjection(t, spec))
	}
	followed(t, spread, cover)

	counts := scattered.members()
	if len(counts) != cover.Count() {
		t.Fatalf("Unordered put the log in %v, and a key of its own for every envelope reaches every member of a cover of %d", counts, cover.Count())
	}
	if once, twice, none := scattered.counted(36); once != 36 || twice != 0 || none != 0 {
		t.Fatalf("Unordered applied %d events once, %d more than once and %d never", once, twice, none)
	}

	together := newStand(t, eventmemory.Spec{})
	for index := range 12 {
		together.append(t, "k"+strconv.Itoa(index), "one", "two", "three")
	}
	concentrated := newApplications()
	for _, part := range cover.Partitions() {
		spec := together.spec("orders", concentrated.records(part))
		spec.Partition, spec.Sequence = part, projection.OneSequence()
		running(t, newProjection(t, spec))
	}
	followed(t, together, cover)

	held := concentrated.members()
	if len(held) != 1 {
		t.Fatalf("OneSequence put the log in %v, and one key is one member of the cover", held)
	}
	for _, applied := range held {
		if applied != 36 {
			t.Fatalf("the one member OneSequence reaches applied %d of 36", applied)
		}
	}
	for _, part := range cover.Partitions() {
		row := together.row(t, "orders#"+part.String())
		if row.Fresh() || row.Progress.Highest != 36 {
			t.Fatalf("the member %q holds %+v after a log of 36 it matched %d of, and a member that saved nothing re-reads the whole log at every restart", part, row, held[part.String()])
		}
		if held[part.String()] == 0 && row.Progress.Applied != 0 {
			t.Fatalf("the member %q applied nothing and reports %d applied", part, row.Progress.Applied)
		}
	}

	t.Run("the control: a page re-delivered after a restart lands where it landed before", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		for index := range 12 {
			stand.append(t, "k"+strconv.Itoa(index), "one", "two", "three")
		}
		stand.points.onSave = func(event.Checkpoint, int64) (bool, error) {
			return false, event.Failure(event.NotWritten, errRefusedSave)
		}
		lost := newApplications()
		var stopped []func()
		var awaiting []<-chan error
		for _, part := range cover.Partitions() {
			spec := stand.spec("orders", lost.records(part))
			spec.Partition, spec.Sequence = part, projection.Unordered()
			cancel, returned := running(t, newProjection(t, spec))
			stand.observer.await(t, "the member "+part.String()+" could not record", func(state projection.State) bool {
				return state.Phase == projection.PhaseRetrying && state.Identity.Partition() == part
			})
			stopped, awaiting = append(stopped, cancel), append(awaiting, returned)
		}
		for index := range stopped {
			stopped[index]()
			_ = returns(t, awaiting[index])
		}
		stand.points.onSave = nil

		before := lost.placed()
		if len(before) != 36 {
			t.Fatalf("%d of 36 envelopes reached a handler before the checkpoint store refused, and the restart below re-delivers what was never recorded", len(before))
		}
		for _, name := range cover.Partitions() {
			if row := stand.row(t, "orders#"+name.String()); !row.Fresh() {
				t.Fatalf("the member %q recorded advance %d, so nothing below is a re-delivery", name, row.Advance)
			}
		}

		again := newApplications()
		for _, part := range cover.Partitions() {
			spec := stand.spec("orders", again.records(part))
			spec.Partition, spec.Sequence = part, projection.Unordered()
			running(t, newProjection(t, spec))
		}
		followed(t, stand, cover)

		after := again.placed()
		for envelope, part := range before {
			if after[envelope] != part {
				t.Fatalf("%q was applied by %q before the restart and by %q after it, and a key that moves is an event applied twice in one member and never in another", envelope, part, after[envelope])
			}
		}
		if len(after) != len(before) {
			t.Fatalf("%d envelopes were applied after the restart where %d were before it", len(after), len(before))
		}
	})
}

var errRefusedSave = errors.New("projection_test: this checkpoint store is away")

func followed(t *testing.T, held *stand, cover projection.Cover) {
	t.Helper()
	for _, part := range cover.Partitions() {
		held.observer.await(t, "the partition "+part.String()+" followed", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing && state.Identity.Partition() == part
		})
	}
}

// Only the first start of a projection chooses its topology. The migration an
// operator reaches for — add Partition to the spec and deploy — is two writers
// over every key of the coarser row's space, and it is refused at the resume
// rather than discovered as a read model that took the whole log twice. The
// controls are the two ways the refusal must not fire: after the handoff, and on
// a projection that never ran.
func TestAPartitionedRunnerBesideALiveCoarserRowIsRefused(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	for index := range 12 {
		stand.append(t, "k"+strconv.Itoa(index), "one", "two", "three")
	}
	seen := newApplications()

	whole := newProjection(t, stand.spec("orders", seen.records(projection.Whole())))
	cancel, returned := running(t, whole)
	stand.observer.await(t, "the unpartitioned release drained", func(state projection.State) bool {
		return state.Phase == projection.PhaseFollowing && state.Progress.Applied == 36
	})
	cancel()
	_ = returns(t, returned)
	unpartitioned := stand.row(t, "orders")
	before, _, _ := seen.counted(36)
	if before != 36 {
		t.Fatalf("the unpartitioned release applied %d of 36 events, and the release below is what this case is about", before)
	}

	for _, part := range []projection.Partition{partitionOf(t, 0, 1), partitionOf(t, 1, 1)} {
		spec := stand.spec("orders", seen.records(part))
		spec.Partition = part
		running(t, newProjection(t, spec))
		halted := stand.observer.await(t, "the partitioned runner "+part.String()+" halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted && state.Identity.Partition() == part
		})
		if !errors.Is(halted.Err, projection.ErrHalted) || !errors.Is(halted.Err, projection.ErrTopology) {
			t.Fatalf("the partitioned runner %q was refused as %v, where a topology started over a live one is a halt over a topology refusal", part, halted.Err)
		}
		for _, names := range []string{`"orders"`, "Split"} {
			if !strings.Contains(halted.Err.Error(), names) {
				t.Fatalf("the refusal reads %q and does not name %s, which is the row it found and the route that hands its cursor down", halted.Err, names)
			}
		}
		if row := stand.row(t, "orders#"+part.String()); !row.Fresh() {
			t.Fatalf("the refused runner %q left a checkpoint row at advance %d, and a runner refused at its resume records nothing", part, row.Advance)
		}
	}
	if once, twice, _ := seen.counted(36); once != 36 || twice != 0 {
		t.Fatalf("%d events were applied once and %d more than once after the refused release, and a refused runner reaches no handler", once, twice)
	}

	t.Run("the control: the same release after the handoff is admitted and applies nothing twice", func(t *testing.T) {
		lower, higher, err := splitOf(t, stand, stand.checkpoints, identityOf(t, "orders", projection.Ungenerated, projection.Whole()))
		if err != nil {
			t.Fatalf("splitting the drained %q answered %v, and the handoff is the route the refusal above names", "orders", err)
		}
		if lower.String() != "orders#0.1" || higher.String() != "orders#1.1" {
			t.Fatalf("the handoff answered %q and %q, where the release that was refused runs at {0,1} and {1,1}", lower, higher)
		}
		for _, child := range []projection.Identity{lower, higher} {
			if row := stand.row(t, child.String()); row.Cursor != unpartitioned.Cursor {
				t.Fatalf("the child %q resumes from %q where the unpartitioned release stood at %q", child, row.Cursor, unpartitioned.Cursor)
			}
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the unpartitioned row still stands at advance %d beside its children", row.Advance)
		}

		for _, part := range []projection.Partition{partitionOf(t, 0, 1), partitionOf(t, 1, 1)} {
			spec := stand.spec("orders", seen.records(part))
			spec.Partition = part
			running(t, newProjection(t, spec))
			stand.observer.await(t, "the partition "+part.String()+" followed", func(state projection.State) bool {
				return state.Phase == projection.PhaseFollowing && state.Identity.Partition() == part
			})
		}
		if once, twice, _ := seen.counted(36); once != 36 || twice != 0 {
			t.Fatalf("%d events were applied once and %d more than once after the handoff, and two children that took the parent's cursor apply nothing again", once, twice)
		}
	})

	t.Run("the control: a projection that never ran is admitted at any topology", func(t *testing.T) {
		other := newStand(t, eventmemory.Spec{})
		for index := range 12 {
			other.append(t, "k"+strconv.Itoa(index), "one", "two", "three")
		}
		fresh := newApplications()
		cover := coverOf(t, partitionOf(t, 0, 1), partitionOf(t, 1, 1))
		for _, part := range cover.Partitions() {
			spec := other.spec("orders", fresh.records(part))
			spec.Partition = part
			running(t, newProjection(t, spec))
			other.observer.await(t, "the partition "+part.String()+" followed", func(state projection.State) bool {
				return state.Phase == projection.PhaseFollowing && state.Identity.Partition() == part
			})
		}
		if once, twice, none := fresh.counted(36); once != 36 || twice != 0 || none != 0 {
			t.Fatalf("a cover declared on a projection that never ran applied %d once, %d twice and %d never", once, twice, none)
		}
	})

	t.Run("a row for any coarser share is what is refused, not only the whole one", func(t *testing.T) {
		other := newStand(t, eventmemory.Spec{})
		other.append(t, "k0", "one")
		half := partitionOf(t, 0, 1)
		spec := other.spec("orders", applies(other.model))
		spec.Partition = half
		running(t, newProjection(t, spec))
		other.observer.await(t, "the half followed", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing && state.Identity.Partition() == half
		})

		quarter := partitionOf(t, 0, 3)
		finer := other.spec("orders", applies(other.model))
		finer.Partition = quarter
		running(t, newProjection(t, finer))
		halted := other.observer.await(t, "the quarter halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted && state.Identity.Partition() == quarter
		})
		if !errors.Is(halted.Err, projection.ErrTopology) || !strings.Contains(halted.Err.Error(), "orders#0.1") {
			t.Fatalf("a quarter started beside a live half was answered %v, and the row it found is the half's", halted.Err)
		}
	})
}

// The handoff a Split performs, spelled by hand because Split is not in this
// section: a child row at the parent's cursor, written through the tracker door
// like every other checkpoint write in this repository.
func handedDown(t *testing.T, stand *stand, name string, parent event.Checkpoint) {
	t.Helper()
	tracker, err := event.Track(stand.checkpoints, name)
	if err != nil {
		t.Fatalf("tracking %q answered %v", name, err)
	}
	if _, err := tracker.Load(context.Background()); err != nil {
		t.Fatalf("loading %q answered %v", name, err)
	}
	if _, err := tracker.Save(context.Background(), parent.Cursor, parent.Progress); err != nil {
		t.Fatalf("writing %q at the parent's cursor answered %v", name, err)
	}
}

func forget(t *testing.T, stand *stand, name string) {
	t.Helper()
	tracker, err := event.Track(stand.checkpoints, name)
	if err != nil {
		t.Fatalf("tracking %q answered %v", name, err)
	}
	if err := tracker.Forget(context.Background()); err != nil {
		t.Fatalf("retiring %q answered %v", name, err)
	}
}
