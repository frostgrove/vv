package projection_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

// The application's ownership row: one row per projection across all of its
// generations, moved by a fenced compare-and-set and read by the read path. It
// counts both calls, because "neither reads the row and then writes it in two
// statements" is a property of the caller rather than of this fake, and a count
// is the only thing that can say so.
type ownership struct {
	mutex sync.Mutex
	held  map[string]projection.Generation

	reads       atomic.Int64
	activations atomic.Int64

	// Called inside Activate, so a case can cause the contention two operators
	// race for rather than hope for it.
	arrive func()
}

func owning(name string, generation projection.Generation) *ownership {
	return &ownership{held: map[string]projection.Generation{name: generation}}
}

func (this *ownership) Active(_ context.Context, name string) (projection.Generation, error) {
	this.reads.Add(1)
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.held[name], nil
}

func (this *ownership) Activate(_ context.Context, name string, from, to projection.Generation) error {
	this.activations.Add(1)
	if this.arrive != nil {
		this.arrive()
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.held[name] != from {
		return fmt.Errorf("%w: %q holds generation %d and this call names %d", event.ErrConflict, name, this.held[name], from)
	}
	this.held[name] = to
	return nil
}

func (this *ownership) active(name string) projection.Generation {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.held[name]
}

// A checkpoint row written the way a drained partition leaves one, for the cases
// whose subject is the fold over a set of rows rather than the loop that wrote
// them.
func recorded(t *testing.T, held *stand, of projection.Identity, progress event.Progress) {
	t.Helper()
	ctx := context.Background()
	tracker, err := event.Track(held.checkpoints, of.String())
	if err != nil {
		t.Fatalf("tracking %q answered %v", of, err)
	}
	if _, err := tracker.Load(ctx); err != nil {
		t.Fatalf("loading %q answered %v", of, err)
	}
	cursor := event.Cursor("at-" + strconv.FormatUint(uint64(progress.Highest), 10))
	if _, err := tracker.Save(ctx, cursor, progress); err != nil {
		t.Fatalf("writing the row of %q answered %v", of, err)
	}
}

func observedOver(t *testing.T, checkpoints event.Checkpoints, of projection.Identity, over projection.Cover) projection.Barrier {
	t.Helper()
	barrier, err := projection.Observe(context.Background(), checkpoints, of, over)
	if err != nil {
		t.Fatalf("observing the barrier of %q over %v answered %v", of, over.Partitions(), err)
	}
	return barrier
}

func quartered(t *testing.T) projection.Cover {
	t.Helper()
	return coverOf(t, partitionOf(t, 0, 3), partitionOf(t, 1, 3), partitionOf(t, 2, 3), partitionOf(t, 3, 3))
}

// The rows of one generation over the four-member cover above, in the order the
// cover names them.
func across(t *testing.T, held *stand, of projection.Identity, cover projection.Cover, progress ...event.Progress) {
	t.Helper()
	for index, part := range cover.Partitions() {
		recorded(t, held, identityOf(t, of.Projection(), of.Generation(), part), progress[index])
	}
}

func watermarks(highest ...event.Position) []event.Progress {
	held := make([]event.Progress, 0, len(highest))
	for _, at := range highest {
		held = append(held, event.Progress{Highest: at, Applied: uint64(at)})
	}
	return held
}

// A projection's progress is a min across its partition set and never anything
// else: Highest is a completeness watermark for its own row, so a max, a sum or
// an average over the set is a number no partition ever reached and every one of
// them would be above what three of the four delivered.
func TestObserveAnswersTheMinimumAcrossTheCover(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	of := identityOf(t, "orders", 2, projection.Whole())
	cover := quartered(t)
	across(t, stand, of, cover, watermarks(900, 1200, 1150, 1300)...)

	barrier := observedOver(t, stand.checkpoints, of, cover)
	if barrier.At != 900 {
		t.Fatalf("four partitions at 900, 1200, 1150 and 1300 answered a barrier of %d, where the max is 1300, the sum is 4550, the average is 1137 and the one position the projection as a whole delivered past is 900", barrier.At)
	}
	if barrier.Projection != "orders" || barrier.Generation != 2 {
		t.Fatalf("the barrier names %q at generation %d, and it is evidence about the generation it was observed from", barrier.Projection, barrier.Generation)
	}

	t.Run("the control: a cover of one answers its own number", func(t *testing.T) {
		alone := identityOf(t, "payments", 2, projection.Whole())
		recorded(t, stand, alone, event.Progress{Highest: 1300, Applied: 1300})

		if answered := observedOver(t, stand.checkpoints, alone, coverOf(t, projection.Whole())); answered.At != 1300 {
			t.Fatalf("one partition at 1300 answered a barrier of %d, so the minimum above is a constant rather than the fold", answered.At)
		}
	})
}

// A member with no row is not a member at position zero, and the difference is
// the whole of the refusal: read as zero, one absent row answers a barrier at the
// origin for a generation that is live on the other three, and every arriving
// generation clears it.
func TestObserveRefusesACoverWhoseMemberHasNoRow(t *testing.T) {
	ctx := context.Background()
	stand := newStand(t, eventmemory.Spec{})
	of := identityOf(t, "orders", 2, projection.Whole())
	cover := quartered(t)
	for index, at := range []event.Position{900, 1200, 1150} {
		recorded(t, stand, identityOf(t, "orders", 2, partitionOf(t, uint32(index), 3)), event.Progress{Highest: at})
	}

	barrier, err := projection.Observe(ctx, stand.checkpoints, of, cover)
	if !errors.Is(err, projection.ErrTopology) {
		t.Fatalf("a cover with a member that has not reported answered %v, where a set whose rows do not agree on what it delivered is a topology refusal", err)
	}
	if !strings.Contains(err.Error(), "orders@2#3.3") {
		t.Fatalf("the refusal reads %q and does not name the member that holds no row", err)
	}
	if barrier != (projection.Barrier{}) {
		t.Fatalf("the refused observation answered %+v, and an absent row read as position zero is the barrier this refusal exists to withhold", barrier)
	}

	t.Run("the control: the same cover with all four rows answers 900", func(t *testing.T) {
		recorded(t, stand, identityOf(t, "orders", 2, partitionOf(t, 3, 3)), event.Progress{Highest: 1300})

		if answered := observedOver(t, stand.checkpoints, of, cover); answered.At != 900 {
			t.Fatalf("the completed cover answered %d, so the three arms are told apart by something other than the rows", answered.At)
		}
	})

	t.Run("the control: a cover all of whose rows are absent answers the origin", func(t *testing.T) {
		fresh := identityOf(t, "orders", 3, projection.Whole())

		answered := observedOver(t, stand.checkpoints, fresh, cover)
		if answered.At != 0 {
			t.Fatalf("a generation that has recorded nothing answered a barrier of %d, and what it delivered is nothing", answered.At)
		}
	})

	t.Run("an of that carries a partition is refused", func(t *testing.T) {
		partitioned := identityOf(t, "orders", 2, partitionOf(t, 1, 3))

		_, err := projection.Observe(ctx, stand.checkpoints, partitioned, cover)
		if !errors.Is(err, projection.ErrTopology) || !strings.Contains(err.Error(), "1.3") {
			t.Fatalf("an identity carrying its own partition beside a cover answered %v, and the two are two answers to one question", err)
		}
		if _, err := projection.Reached(ctx, stand.checkpoints, nil, projection.Barrier{Projection: "orders"}, partitioned, cover); !errors.Is(err, projection.ErrTopology) {
			t.Fatalf("readiness asked of a partition answered %v", err)
		}
	})

	t.Run("the zero Cover and the zero Identity are refused at both doors", func(t *testing.T) {
		for _, refused := range []struct {
			what string
			run  func() error
		}{
			{"an unbuilt identity", func() error {
				_, err := projection.Observe(ctx, stand.checkpoints, projection.Identity{}, cover)
				return err
			}},
			{"an unbuilt cover", func() error {
				_, err := projection.Observe(ctx, stand.checkpoints, of, projection.Cover{})
				return err
			}},
			{"an unbuilt identity at Reached", func() error {
				_, err := projection.Reached(ctx, stand.checkpoints, nil, projection.Barrier{}, projection.Identity{}, cover)
				return err
			}},
			{"an unbuilt cover at Reached", func() error {
				_, err := projection.Reached(ctx, stand.checkpoints, nil, projection.Barrier{Projection: "orders"}, of, projection.Cover{})
				return err
			}},
		} {
			if err := refused.run(); err == nil {
				t.Fatalf("%s was admitted, and a fold over a set nobody checked takes its minimum across a hole", refused.what)
			}
		}
	})
}

// The barrier and the question it answers: how far the arriving generation still
// is from what the retiring one delivered. It is a Position compared with >=
// against another row's watermark, and nothing here turns it into a cursor,
// resumes from it or writes it anywhere.
func TestABarrierIsObservedAndReached(t *testing.T) {
	ctx := context.Background()
	stand := newStand(t, eventmemory.Spec{})
	cover := quartered(t)
	retiring := identityOf(t, "orders", 1, projection.Whole())
	arriving := identityOf(t, "orders", 2, projection.Whole())
	across(t, stand, retiring, cover, watermarks(900, 1200, 1150, 1300)...)
	across(t, stand, arriving, cover,
		event.Progress{Highest: 800, Quarantined: 1},
		event.Progress{Highest: 900, Quarantined: 2},
		event.Progress{Highest: 900, Quarantined: 3},
		event.Progress{Highest: 900, Quarantined: 4})

	queue := newPark()
	barrier := observedOver(t, stand.points, retiring, cover)
	if barrier.At != 900 {
		t.Fatalf("the retiring generation's barrier is %d where the lowest of its four rows is 900", barrier.At)
	}
	var _ event.Position = barrier.At

	behind, err := projection.Reached(ctx, stand.points, queue, barrier, arriving, cover)
	if err != nil {
		t.Fatalf("asking whether the arriving generation reached the barrier answered %v", err)
	}
	switch {
	case behind.Reached:
		t.Fatalf("a generation whose lowest watermark is 800 reached a barrier at 900: %+v", behind)
	case behind.Behind != 100:
		t.Fatalf("it stands %d below the barrier where 900 - 800 is 100", behind.Behind)
	case behind.Quarantined != 10:
		t.Fatalf("the four partitions quarantined 1, 2, 3 and 4 and this answered %d, and what a generation quarantined is the sum across its set", behind.Quarantined)
	case behind.Holes != 0:
		t.Fatalf("an empty park answered %d holes", behind.Holes)
	}
	if asked := queue.holes.Load(); asked != 1 {
		t.Fatalf("Holes was asked %d times, and a park is keyed by a generation with the partition dropped, so a readiness asks it once and not once per member", asked)
	}

	recorded(t, stand, identityOf(t, "orders", 2, partitionOf(t, 0, 3)), event.Progress{Highest: 950, Quarantined: 1})
	reached, err := projection.Reached(ctx, stand.points, queue, barrier, arriving, cover)
	if err != nil {
		t.Fatalf("asking again answered %v", err)
	}
	if !reached.Reached || reached.Behind != 0 {
		t.Fatalf("a generation whose lowest watermark is 900 answered %+v against a barrier at 900, and the comparison is >=", reached)
	}
	if saves := stand.points.saves.Load(); saves != 0 {
		t.Fatalf("observing a barrier and asking about it issued %d checkpoint saves, and a barrier is not a cursor, a resume point or a row", saves)
	}
	if loads := stand.points.loads.Load(); loads == 0 {
		t.Fatal("neither call read a checkpoint row, so the assertions above are about nothing")
	}

	t.Run("a barrier of another projection, and the zero Barrier with it, is refused", func(t *testing.T) {
		for _, refused := range []struct {
			what string
			held projection.Barrier
		}{
			{"the zero Barrier", projection.Barrier{}},
			{"another projection's", projection.Barrier{Projection: "payments", Generation: 1, At: 900}},
			{"the arriving generation's own", projection.Barrier{Projection: "orders", Generation: 2, At: 900}},
		} {
			if _, err := projection.Reached(ctx, stand.points, queue, refused.held, arriving, cover); !errors.Is(err, projection.ErrTopology) {
				t.Fatalf("%s was admitted as evidence and answered %v", refused.what, err)
			}
		}
	})
}

type written struct{ key, payload string }

// The two generations a cutover is between, over one log: the live one drained
// whole, and the arriving one drained with a handler that refuses every event of
// one stream — so what it parked, and what an operator then did about it, is the
// only difference between the arms.
func twoGenerations(t *testing.T, poison string, rows ...written) (*stand, *park, *ledger) {
	t.Helper()
	stand := newStand(t, eventmemory.Spec{})
	for _, row := range rows {
		stand.append(t, row.key, row.payload)
	}

	live := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
		stand.model.write(payloadsOf(batch.Envelopes)...)
		return nil
	}))
	cancel, returned := running(t, newProjection(t, live))
	stand.observer.await(t, "the live generation read the whole log", func(state projection.State) bool {
		return state.Identity.Generation() == projection.Ungenerated && state.Phase == projection.PhaseFollowing
	})
	cancel()
	_ = returns(t, returned)

	queue, applied := newPark(), &ledger{}
	rebuild := stand.spec("orders", projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
		for _, envelope := range batch.Envelopes {
			if string(envelope.Stream.Key) == poison {
				return fmt.Errorf("%w: the fixture refuses every event of the stream %q", event.ErrPayload, poison)
			}
		}
		applied.write(ctx, payloadsOf(batch.Envelopes)...)
		return nil
	}))
	rebuild.Generation = 2
	cancel, returned = running(t, newProjection(t, stand.parking(rebuild, queue)))
	stand.observer.await(t, "the arriving generation read the whole log", func(state projection.State) bool {
		return state.Identity.Generation() == 2 &&
			(state.Phase == projection.PhaseFollowing || state.Phase == projection.PhaseDegraded)
	})
	cancel()
	_ = returns(t, returned)

	retiring, arriving := stand.row(t, "orders"), stand.row(t, "orders@2")
	if retiring.Fresh() || retiring.Progress.Highest != arriving.Progress.Highest {
		t.Fatalf("the live generation stands at %d and the arriving one at %d, and every arm below is about a generation that read the same log to the same place", retiring.Progress.Highest, arriving.Progress.Highest)
	}
	return stand, queue, applied
}

func cuttingOver(t *testing.T, held *stand, queue *park, row *ownership, accept bool) error {
	t.Helper()
	return projection.Cutover(context.Background(), projection.CutoverSpec{
		Checkpoints:       held.checkpoints,
		Generations:       row,
		Park:              queue,
		Projection:        "orders",
		From:              projection.Ungenerated,
		To:                2,
		Retiring:          coverOf(t, projection.Whole()),
		Arriving:          coverOf(t, projection.Whole()),
		Unit:              held.unit,
		AcceptQuarantined: accept,
	})
}

func redrivingAt(t *testing.T, held *stand, queue *park, of projection.Identity, handler projection.Handler) *projection.Redrive {
	t.Helper()
	held.park = queue
	built, err := projection.NewRedrive(projection.RedriveSpec{
		Identity:    of,
		Handler:     handler,
		Park:        queue,
		Unit:        held.operating(&transaction{named: "one resource"}),
		Destination: readModel{pool: held},
	})
	if err != nil {
		t.Fatalf("a well-formed redrive over %q was refused: %v", of, err)
	}
	return built
}

// What a cutover reads is the holes the queue answers now, and never the
// cumulative count of what was ever quarantined: the ordinary recovery is park,
// fix, redrive, cut over, and a generation that walked it has a read model with
// no holes and a Quarantined that stands for ever.
func TestACutoverRefusesOnHolesAndNotOnQuarantined(t *testing.T) {
	ctx := context.Background()
	of := identityOf(t, "orders", 2, projection.Whole())

	t.Run("two letters still parked", func(t *testing.T) {
		stand, queue, _ := twoGenerations(t, "a", written{"a", "A1"}, written{"a", "A2"}, written{"b", "B1"})
		row := owning("orders", projection.Ungenerated)

		err := cuttingOver(t, stand, queue, row, false)
		if !errors.Is(err, projection.ErrTopology) {
			t.Fatalf("a cutover to a generation holding two parked letters answered %v", err)
		}
		if !strings.Contains(err.Error(), "2 envelopes") || !strings.Contains(err.Error(), "AcceptQuarantined") {
			t.Fatalf("the refusal reads %q and names neither the holes nor the override that admits them", err)
		}
		if row.active("orders") != projection.Ungenerated || row.activations.Load() != 0 {
			t.Fatalf("the refused cutover moved the read target to %d after %d activations", row.active("orders"), row.activations.Load())
		}
	})

	t.Run("one letter evicted without being applied", func(t *testing.T) {
		stand, queue, _ := twoGenerations(t, "a", written{"a", "A1"}, written{"b", "B1"})
		queue.skip(of, sequenceOf("a"))
		row := owning("orders", projection.Ungenerated)

		if names := queue.names(of); len(names) != 0 {
			t.Fatalf("the queue still holds %v, so this arm is the parked one over again", names)
		}
		err := cuttingOver(t, stand, queue, row, false)
		if !errors.Is(err, projection.ErrTopology) || !strings.Contains(err.Error(), "1 envelopes") {
			t.Fatalf("a cutover to a generation whose letter was evicted unapplied answered %v, and an empty queue is not an empty read model", err)
		}
	})

	t.Run("everything parked, redriven to completion, proceeds with no override", func(t *testing.T) {
		stand, queue, applied := twoGenerations(t, "a", written{"a", "A1"}, written{"a", "A2"}, written{"b", "B1"})
		row := owning("orders", projection.Ungenerated)

		drain := redrivingAt(t, stand, queue, of, appliesInto(applied))
		if answered, err := drain.Sequence(ctx, sequenceOf("a")); err != nil || answered.Applied != 2 {
			t.Fatalf("draining the parked sequence answered %+v and %v", answered, err)
		}

		ready, err := projection.Reached(ctx, stand.checkpoints, queue,
			observedOver(t, stand.checkpoints, identityOfWhole(t), coverOf(t, projection.Whole())), of, coverOf(t, projection.Whole()))
		if err != nil {
			t.Fatalf("asking about the recovered generation answered %v", err)
		}
		if ready.Quarantined != 2 || ready.Holes != 0 {
			t.Fatalf("the recovered generation reports %d quarantined and %d holes, where the durable count stands at 2 and the queue answers none", ready.Quarantined, ready.Holes)
		}
		if err := cuttingOver(t, stand, queue, row, false); err != nil {
			t.Fatalf("a generation that recovered completely was refused the cutover: %v", err)
		}
		if row.active("orders") != 2 {
			t.Fatalf("the read target holds generation %d after a cutover that answered nil", row.active("orders"))
		}
	})

	t.Run("the control: a cutover with nothing ever parked proceeds and both numbers are zero", func(t *testing.T) {
		stand, queue, _ := twoGenerations(t, "nothing", written{"a", "A1"}, written{"b", "B1"})
		row := owning("orders", projection.Ungenerated)

		ready, err := projection.Reached(ctx, stand.checkpoints, queue,
			observedOver(t, stand.checkpoints, identityOfWhole(t), coverOf(t, projection.Whole())), of, coverOf(t, projection.Whole()))
		if err != nil {
			t.Fatalf("asking about the generation that parked nothing answered %v", err)
		}
		if ready.Quarantined != 0 || ready.Holes != 0 {
			t.Fatalf("a generation that parked nothing reports %d quarantined and %d holes", ready.Quarantined, ready.Holes)
		}
		if err := cuttingOver(t, stand, queue, row, false); err != nil {
			t.Fatalf("the cutover of a generation that parked nothing answered %v", err)
		}
	})
}

// There is no field through which a barrier of zero can be handed in, and the
// evidence is derived where the write is made. The fixture is a real Split and
// not a hand-written pair of rows: what a split writes into Progress.Highest is
// the whole of what this barrier is, and the control is the same two children
// written at a watermark of zero — rows the kernel's own door admits.
func TestACutoverTakesNoBarrierAndDerivesItsOwn(t *testing.T) {
	parent := identityOfWhole(t)
	arriving := identityOf(t, "orders", 2, projection.Whole())
	whole := coverOf(t, projection.Whole())

	t.Run("a CutoverSpec has no field a barrier can be handed through", func(t *testing.T) {
		spec, barrier := reflect.TypeFor[projection.CutoverSpec](), reflect.TypeFor[projection.Barrier]()
		if spec.NumField() == 0 {
			t.Fatal("CutoverSpec has no fields at all, so the walk below would pass over any shape")
		}
		for index := range spec.NumField() {
			field := spec.Field(index)
			if field.Name == "Barrier" || field.Type == barrier {
				t.Fatalf("CutoverSpec.%s is a %s, and a barrier a caller can hand in is one whose zero value admits a generation that has delivered nothing", field.Name, field.Type)
			}
		}
	})

	t.Run("a cutover that cannot derive its evidence in one snapshot is refused before it reads one", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		recorded(t, stand, parent, event.Progress{Highest: 900})
		recorded(t, stand, arriving, event.Progress{Highest: 900})
		legal := projection.CutoverSpec{
			Checkpoints: stand.checkpoints,
			Projection:  "orders",
			From:        projection.Ungenerated,
			To:          2,
			Retiring:    whole,
			Arriving:    whole,
			Unit:        stand.unit,
		}
		for _, refused := range []struct {
			what  string
			names string
			build func(spec projection.CutoverSpec) projection.CutoverSpec
		}{
			{"a unit that opened no transaction", "Unit", func(spec projection.CutoverSpec) projection.CutoverSpec {
				spec.Unit = runsTheWork
				return spec
			}},
			{"a unit that never ran the work", "not moved", func(spec projection.CutoverSpec) projection.CutoverSpec {
				spec.Unit = func(context.Context, func(context.Context) error) error { return nil }
				return spec
			}},
			{"no ownership row", "Generations", func(spec projection.CutoverSpec) projection.CutoverSpec {
				spec.Generations = nil
				return spec
			}},
			{"no checkpoint store", "Checkpoints", func(spec projection.CutoverSpec) projection.CutoverSpec {
				spec.Checkpoints = nil
				return spec
			}},
			{"one generation named twice", "From and To", func(spec projection.CutoverSpec) projection.CutoverSpec {
				spec.From = 2
				return spec
			}},
			{"an unbuilt retiring cover", "Retiring", func(spec projection.CutoverSpec) projection.CutoverSpec {
				spec.Retiring = projection.Cover{}
				return spec
			}},
			{"an unbuilt arriving cover", "Arriving", func(spec projection.CutoverSpec) projection.CutoverSpec {
				spec.Arriving = projection.Cover{}
				return spec
			}},
			{"a projection no row can be keyed by", "To", func(spec projection.CutoverSpec) projection.CutoverSpec {
				spec.Projection = "orders@2"
				return spec
			}},
		} {
			t.Run(refused.what, func(t *testing.T) {
				row := owning("orders", projection.Ungenerated)
				spec := legal
				spec.Generations = row

				err := projection.Cutover(context.Background(), refused.build(spec))
				if err == nil {
					t.Fatalf("a cutover carrying %s was made, and what it moved the read target on is evidence it could not read", refused.what)
				}
				if !strings.Contains(err.Error(), refused.names) {
					t.Fatalf("%s was refused with %q, which does not name %s", refused.what, err, refused.names)
				}
				if row.activations.Load() != 0 {
					t.Fatalf("the refused cutover issued %d writes", row.activations.Load())
				}
			})
		}

		t.Run("what the unit answered travels", func(t *testing.T) {
			spec := legal
			spec.Generations = owning("orders", projection.Ungenerated)
			refusal := errors.New("the caller's own transaction did not commit")
			spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
				_ = stand.unit(ctx, work)
				return refusal
			}

			if err := projection.Cutover(context.Background(), spec); !errors.Is(err, refusal) {
				t.Fatalf("a unit that answered its own failure was read as %v, and what a cutover knows is what its own body reached", err)
			}
		})

		t.Run("the control: the same spec with none of them", func(t *testing.T) {
			spec := legal
			spec.Generations = owning("orders", projection.Ungenerated)

			if err := projection.Cutover(context.Background(), spec); err != nil {
				t.Fatalf("the legal cutover every case above was built from was itself refused: %v", err)
			}
		})
	})

	t.Run("the barrier is the split parent's own watermark", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		twelveStreams(t, stand)
		before := drained(t, stand, parent, "two")
		lower, higher, err := splitOf(t, stand, stand.checkpoints, parent)
		if err != nil {
			t.Fatalf("splitting the drained parent answered %v", err)
		}
		children := coverOf(t, lower.Partition(), higher.Partition())

		barrier := observedOver(t, stand.checkpoints, parent, children)
		if barrier.At != before.Progress.Highest {
			t.Fatalf("the split children answer a barrier of %d where their parent stood at %d, and a barrier at the origin is reached by anything", barrier.At, before.Progress.Highest)
		}

		row := owning("orders", projection.Ungenerated)
		recorded(t, stand, arriving, event.Progress{Highest: before.Progress.Highest - 1})
		cutover := func() error {
			return projection.Cutover(context.Background(), projection.CutoverSpec{
				Checkpoints: stand.checkpoints,
				Generations: row,
				Projection:  "orders",
				From:        projection.Ungenerated,
				To:          2,
				Retiring:    children,
				Arriving:    whole,
				Unit:        stand.unit,
			})
		}
		err = cutover()
		if !errors.Is(err, projection.ErrTopology) || !strings.Contains(err.Error(), "has not delivered everything the barrier") {
			t.Fatalf("a cutover to a generation one position short of the barrier answered %v", err)
		}
		if row.activations.Load() != 0 {
			t.Fatalf("the refused cutover issued %d activations", row.activations.Load())
		}

		recorded(t, stand, arriving, event.Progress{Highest: before.Progress.Highest})
		if err := cutover(); err != nil {
			t.Fatalf("a generation at the barrier was refused the cutover: %v", err)
		}
		if row.active("orders") != 2 {
			t.Fatalf("the read target holds generation %d", row.active("orders"))
		}
	})

	t.Run("the control: the same two children at a watermark of zero make the same cutover proceed", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		twelveStreams(t, stand)
		before := drained(t, stand, parent, "two")
		lower, higher := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 0, 1)), identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
		recorded(t, stand, lower, event.Progress{Applied: before.Progress.Applied})
		recorded(t, stand, higher, event.Progress{})
		children := coverOf(t, lower.Partition(), higher.Partition())

		if barrier := observedOver(t, stand.checkpoints, parent, children); barrier.At != 0 {
			t.Fatalf("two children written at a watermark of zero answer a barrier of %d, so the assertion above is not about what Split writes", barrier.At)
		}
		row := owning("orders", projection.Ungenerated)
		recorded(t, stand, arriving, event.Progress{Highest: before.Progress.Highest - 1})

		err := projection.Cutover(context.Background(), projection.CutoverSpec{
			Checkpoints: stand.checkpoints,
			Generations: row,
			Projection:  "orders",
			From:        projection.Ungenerated,
			To:          2,
			Retiring:    children,
			Arriving:    whole,
			Unit:        stand.unit,
		})
		if err != nil {
			t.Fatalf("a barrier at the origin refused the cutover of a generation behind the parent: %v", err)
		}
		if row.active("orders") != 2 {
			t.Fatal("the control proves nothing: the cutover answered nil and the read target did not move")
		}
	})
}

// The switch is one fenced write and never a read followed by one: two operators
// who both read the row would both find the generation they name and both write
// over it.
func TestTwoCutoversLeaveOneWinnerAndOneConflict(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	whole := coverOf(t, projection.Whole())
	recorded(t, stand, identityOf(t, "orders", 1, projection.Whole()), event.Progress{Highest: 900})
	recorded(t, stand, identityOf(t, "orders", 2, projection.Whole()), event.Progress{Highest: 900})

	var meeting sync.WaitGroup
	meeting.Add(2)
	row := owning("orders", 1)
	row.arrive = func() {
		meeting.Done()
		meeting.Wait()
	}

	answered := make([]error, 2)
	var finished sync.WaitGroup
	for index := range answered {
		finished.Add(1)
		go func() {
			defer finished.Done()
			answered[index] = projection.Cutover(context.Background(), projection.CutoverSpec{
				Checkpoints: stand.checkpoints,
				Generations: row,
				Projection:  "orders",
				From:        1,
				To:          2,
				Retiring:    whole,
				Arriving:    whole,
				Unit:        stand.unit,
			})
		}()
	}
	finished.Wait()

	won, lost := 0, 0
	for _, err := range answered {
		switch {
		case err == nil:
			won++
		case errors.Is(err, event.ErrConflict):
			lost++
		default:
			t.Fatalf("a cutover answered %v, where the loser of a fenced write is refused by the fence", err)
		}
	}
	if won != 1 || lost != 1 {
		t.Fatalf("two cutovers from 1 to 2 answered %d winners and %d fenced refusals: %v", won, lost, answered)
	}
	if row.active("orders") != 2 {
		t.Fatalf("the read target holds generation %d after two cutovers to 2", row.active("orders"))
	}
	if row.activations.Load() != 2 {
		t.Fatalf("the ownership row was written %d times, and each cutover issues one fenced write", row.activations.Load())
	}
	if row.reads.Load() != 0 {
		t.Fatalf("the ownership row was read %d times by a cutover, and a read followed by a write is what admits two winners", row.reads.Load())
	}
}

// A rollback is the forward call with From and To exchanged and the two covers
// with them, so the path back is exercised by the tests the path forward is. It
// checks readiness by the same arithmetic: a generation that was stopped and
// fell behind is not one reads may be pointed at, and one whose rows are gone is
// not one at all.
func TestARollbackIsTheSameCallExchangedAndErrRetiredWhenTheRowsAreGone(t *testing.T) {
	whole := coverOf(t, projection.Whole())
	first, second := identityOf(t, "orders", 1, projection.Whole()), identityOf(t, "orders", 2, projection.Whole())
	cutover := func(held *stand, row *ownership, from, to projection.Generation) error {
		return projection.Cutover(context.Background(), projection.CutoverSpec{
			Checkpoints: held.checkpoints,
			Generations: row,
			Projection:  "orders",
			From:        from,
			To:          to,
			Retiring:    whole,
			Arriving:    whole,
			Unit:        held.unit,
		})
	}

	t.Run("forward and back over two generations that both kept up", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		recorded(t, stand, first, event.Progress{Highest: 900})
		recorded(t, stand, second, event.Progress{Highest: 900})
		row := owning("orders", 1)

		if err := cutover(stand, row, 1, 2); err != nil {
			t.Fatalf("the cutover answered %v", err)
		}
		if err := cutover(stand, row, 2, 1); err != nil {
			t.Fatalf("the rollback answered %v", err)
		}
		if row.active("orders") != 1 {
			t.Fatalf("the read target holds generation %d after a rollback to 1", row.active("orders"))
		}
		if row.activations.Load() != 2 {
			t.Fatalf("a cutover and a rollback issued %d writes", row.activations.Load())
		}
	})

	t.Run("a rollback to a generation that fell behind is refused by the same arithmetic", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		recorded(t, stand, first, event.Progress{Highest: 500})
		recorded(t, stand, second, event.Progress{Highest: 900})
		row := owning("orders", 2)

		err := cutover(stand, row, 2, 1)
		if !errors.Is(err, projection.ErrTopology) || !strings.Contains(err.Error(), "has not delivered everything the barrier") {
			t.Fatalf("a rollback to a generation 400 positions behind answered %v", err)
		}
		if row.active("orders") != 2 {
			t.Fatalf("the refused rollback moved the read target to %d", row.active("orders"))
		}
	})

	t.Run("the control: a rollback to a generation whose rows are gone is ErrRetired and writes nothing", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		recorded(t, stand, first, event.Progress{Highest: 900})
		recorded(t, stand, second, event.Progress{Highest: 900})
		row := owning("orders", 2)
		if err := stand.checkpoints.Forget(context.Background(), first.String()); err != nil {
			t.Fatalf("dropping the rows of %q answered %v", first, err)
		}

		err := cutover(stand, row, 2, 1)
		if !errors.Is(err, projection.ErrRetired) {
			t.Fatalf("a rollback to a generation whose rows were dropped answered %v", err)
		}
		if !strings.Contains(err.Error(), first.String()) {
			t.Fatalf("the refusal reads %q and does not name the generation with nothing behind it", err)
		}
		if row.active("orders") != 2 || row.activations.Load() != 0 {
			t.Fatalf("the refused rollback left the read target at %d after %d writes", row.active("orders"), row.activations.Load())
		}
	})
}

// [[D-131]] is not reversed for blue/green: an unclaimed type of a family this
// router covers halts, and what makes the window survivable is a declaration in
// the OLD generation's own build, shipped a release before the new one.
func TestAnIgnoredTypeLetsTheOldGenerationKeepServing(t *testing.T) {
	written := func(t *testing.T) (*stand, event.Position) {
		t.Helper()
		held := newStand(t, eventmemory.Spec{})
		current := declareOrders(t)
		orders := writes(t, held.store, current.aggregate)
		wrote(t, orders, "a", current.placed.New("a", placed{Item: "desk", Quantity: 2}), current.paid.New("a", paid{Amount: 900}))
		ship := declareShipping(t)
		wrote(t, writes(t, held.store, ship.aggregate), "s", ship.dispatched.New("s", dispatched{Carrier: "dhl"}))
		wrote(t, orders, "a", current.archived.New("a", archived{Reason: "the type generation two introduced"}))

		page, _, err := held.store.ReadAll(context.Background(), "")
		if err != nil {
			t.Fatalf("reading the log this stand wrote answered %v", err)
		}
		return held, page[len(page)-1].Position
	}
	// Generation one's build: two routes on a family it therefore covers, and no
	// word about the type the next generation introduces on it.
	oldBuild := func(t *testing.T, into *model, ignoring bool) *projection.Router {
		t.Helper()
		facts := declareOrders(t)
		router := projection.NewRouter(projection.SkipForeign)
		projection.On(router, facts.placed, func(_ context.Context, carried placed, _ event.Envelope) error {
			into.write("placed:" + carried.Item)
			return nil
		})
		projection.On(router, facts.paid, func(_ context.Context, carried paid, _ event.Envelope) error {
			into.write("paid:" + strconv.FormatInt(carried.Amount, 10))
			return nil
		})
		if ignoring {
			projection.Ignore(router, "orders", facts.archived.Name())
		}
		return router
	}
	generation := func(held *stand, router *projection.Router, at projection.Generation) projection.Spec {
		spec := held.spec("orders", router)
		spec.Generation = at
		return spec
	}

	t.Run("without preparation the old generation halts on the new one's type", func(t *testing.T) {
		stand, _ := written(t)
		router := oldBuild(t, stand.model, false)
		running(t, newProjection(t, generation(stand, router, 1)))

		halted := stand.observer.await(t, "the old generation halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted
		})
		if !errors.Is(halted.Err, projection.ErrUnrouted) {
			t.Fatalf("the type the next generation introduced halted the old one with %v, where an unclaimed type of a covered family is ErrUnrouted", halted.Err)
		}
		if !strings.Contains(halted.Err.Error(), "orders.archived") {
			t.Fatalf("the refusal reads %q and does not name the type nobody routed", halted.Err)
		}
		if skipped := router.Skipped(); skipped != 1 {
			t.Fatalf("the same run skipped %d events of the family it covers nothing of, so the halt above is not scoped to covered families", skipped)
		}
	})

	t.Run("with Ignore in its own build it keeps serving beside the new generation", func(t *testing.T) {
		stand, last := written(t)
		router := oldBuild(t, stand.model, true)
		running(t, newProjection(t, generation(stand, router, 1)))
		stand.observer.await(t, "the old generation read the whole log", func(state projection.State) bool {
			return state.Identity.Generation() == 1 && state.Phase == projection.PhaseFollowing && state.Progress.Highest >= last
		})
		if rows := stand.model.rows(); !same(rows, []string{"placed:desk", "paid:900"}) {
			t.Fatalf("the old generation wrote %v, and an ignored type reaches no applier at all", rows)
		}

		arriving := &model{}
		facts := declareOrders(t)
		next := projection.NewRouter(projection.SkipForeign)
		projection.On(next, facts.placed, func(_ context.Context, carried placed, _ event.Envelope) error {
			arriving.write("placed:" + carried.Item)
			return nil
		})
		projection.On(next, facts.paid, func(_ context.Context, carried paid, _ event.Envelope) error {
			arriving.write("paid:" + strconv.FormatInt(carried.Amount, 10))
			return nil
		})
		projection.On(next, facts.archived, func(_ context.Context, carried archived, _ event.Envelope) error {
			arriving.write("archived:" + carried.Reason)
			return nil
		})
		running(t, newProjection(t, generation(stand, next, 2)))
		stand.observer.await(t, "the new generation read the whole log", func(state projection.State) bool {
			return state.Identity.Generation() == 2 && state.Phase == projection.PhaseFollowing && state.Progress.Highest >= last
		})
		if rows := arriving.rows(); len(rows) != 3 {
			t.Fatalf("the new generation wrote %v, and it is the build that routes the type the old one ignores", rows)
		}
		if skipped := router.Skipped(); skipped != 1 {
			t.Fatalf("the old generation skipped %d events, and what it may skip without a declaration is a family it covers nothing of", skipped)
		}
	})
}

// Pace is a read throttle and not a concurrency cap, and its precedence is the
// whole of what it is: it gates the read while there is more to read, Idle gates
// the follow, and Backoff gates a retry.
func TestPaceThrottlesTheReadWhileDrainingAndNotWhileFollowing(t *testing.T) {
	paced := func(t *testing.T, pace time.Duration) *stand {
		t.Helper()
		held := newStand(t, eventmemory.Spec{MaxRead: 1})
		held.append(t, "a", "A1")
		held.append(t, "a", "A2")
		held.append(t, "a", "A3")
		spec := held.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			held.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		}))
		spec.Generation, spec.Pace = 2, pace
		running(t, newProjection(t, spec))
		held.ticks.expect(t, time.Second, "the poll")
		return held
	}
	applied := []string{"A1", "A2", "A3"}

	t.Run("a rebuild reads once per interval while it drains", func(t *testing.T) {
		stand := paced(t, 200*time.Millisecond)

		for range 3 {
			stand.ticks.expect(t, 200*time.Millisecond, "the paced read")
			stand.ticks.fire(t)
		}
		stand.observer.await(t, "the whole log was applied", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing
		})
		if rows := stand.model.rows(); !same(rows, applied) {
			t.Fatalf("the paced rebuild applied %v, and a throttle changes when a page is read and never which pages there are", rows)
		}

		// Following is the poll's own wait, so nothing is asked for while it lasts
		// — and the read the poll releases is not paced either, because the read
		// before it answered that there was no more.
		stand.ticks.quiet(t, 100*time.Millisecond)
		stand.ticks.fire(t)
		stand.ticks.quiet(t, 100*time.Millisecond)
	})

	t.Run("the control: the live generation is unpaced and reads as fast as the store answers", func(t *testing.T) {
		stand := paced(t, 0)

		stand.observer.await(t, "the whole log was applied", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing
		})
		if rows := stand.model.rows(); !same(rows, applied) {
			t.Fatalf("the unpaced projection applied %v", rows)
		}
		stand.ticks.quiet(t, 100*time.Millisecond)
	})
}

// No counter is decremented by a topology change either: the parent's two counts
// are divided between its children, so what the set accounts for is what the
// parent accounted for, and the barrier derived from it does not move.
func TestSplitPreservesTheSumAcrossThePartitionSet(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	twelveStreams(t, stand)
	parent := identityOfWhole(t)
	before := drained(t, stand, parent, "two")
	whole := coverOf(t, projection.Whole())

	appliedBefore, quarantinedBefore := summed(t, stand, parent, whole)
	barrierBefore := observedOver(t, stand.checkpoints, parent, whole)
	if appliedBefore != before.Progress.Applied || quarantinedBefore != before.Progress.Quarantined {
		t.Fatalf("the cover of one answers %d applied and %d quarantined where the parent's own row holds %d and %d", appliedBefore, quarantinedBefore, before.Progress.Applied, before.Progress.Quarantined)
	}
	if appliedBefore == 0 || quarantinedBefore == 0 || barrierBefore.At == 0 {
		t.Fatalf("the parent accounts for %d applied, %d quarantined at a barrier of %d, and a sum of zero is preserved by anything", appliedBefore, quarantinedBefore, barrierBefore.At)
	}

	lower, higher, err := splitOf(t, stand, stand.checkpoints, parent)
	if err != nil {
		t.Fatalf("splitting the drained parent answered %v", err)
	}
	children := coverOf(t, lower.Partition(), higher.Partition())

	appliedAfter, quarantinedAfter := summed(t, stand, parent, children)
	if appliedAfter != appliedBefore || quarantinedAfter != quarantinedBefore {
		t.Fatalf("the two children account for %d applied and %d quarantined where their parent accounted for %d and %d", appliedAfter, quarantinedAfter, appliedBefore, quarantinedBefore)
	}
	if barrier := observedOver(t, stand.checkpoints, parent, children); barrier.At != barrierBefore.At {
		t.Fatalf("the set's barrier moved from %d to %d across a split, and a handoff delivers nothing to move it", barrierBefore.At, barrier.At)
	}
}

func summed(t *testing.T, held *stand, of projection.Identity, over projection.Cover) (applied, quarantined uint64) {
	t.Helper()
	for _, part := range over.Partitions() {
		row := held.row(t, identityOf(t, of.Projection(), of.Generation(), part).String())
		applied += row.Progress.Applied
		quarantined += row.Progress.Quarantined
	}
	return applied, quarantined
}

// A barrier folded from silence is the origin, and every generation clears the
// origin. The arriving side of that absence was always refused; this is the
// retiring side, where the same silence admits a cutover to a generation that
// applied almost nothing and moves every reader onto it without an error.
func TestACutoverRefusesARetiringCoverNoMemberOfWhichHoldsARow(t *testing.T) {
	ctx := context.Background()
	arriving := identityOf(t, "orders", 2, projection.Whole())
	whole := coverOf(t, projection.Whole())

	t.Run("a cover whose member a split retired", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		parent := identityOfWhole(t)
		twelveStreams(t, stand)
		before := drained(t, stand, parent, "two")
		lower, higher, err := splitOf(t, stand, stand.checkpoints, parent)
		if err != nil {
			t.Fatalf("splitting the drained parent answered %v", err)
		}
		children := coverOf(t, lower.Partition(), higher.Partition())
		recorded(t, stand, arriving, event.Progress{Highest: 1})

		if _, err := projection.Observe(ctx, stand.checkpoints, parent, whole); !errors.Is(err, projection.ErrTopology) {
			t.Fatalf("observing the pre-split cover of a generation standing at %d answered %v, and a retired member read as position zero is a barrier anything clears", before.Progress.Highest, err)
		}

		row := owning("orders", projection.Ungenerated)
		cutover := func(retiring projection.Cover) error {
			return projection.Cutover(ctx, projection.CutoverSpec{
				Checkpoints: stand.checkpoints,
				Generations: row,
				Projection:  "orders",
				From:        projection.Ungenerated,
				To:          2,
				Retiring:    retiring,
				Arriving:    whole,
				Unit:        stand.unit,
			})
		}

		err = cutover(whole)
		if !errors.Is(err, projection.ErrTopology) {
			t.Fatalf("a cutover declaring the cover the split retired answered %v, and the live generation runs at %q and %q past position %d", err, lower, higher, before.Progress.Highest)
		}
		if !strings.Contains(err.Error(), parent.String()+"#split") {
			t.Fatalf("the refusal is %q, and what an operator needs from it is the row that records the retirement", err)
		}
		if row.activations.Load() != 0 || row.active("orders") != projection.Ungenerated {
			t.Fatalf("the refused cutover issued %d writes and left the read target at generation %d", row.activations.Load(), row.active("orders"))
		}

		t.Run("the control: the cover those children record at", func(t *testing.T) {
			recorded(t, stand, arriving, event.Progress{Highest: before.Progress.Highest})
			if err := cutover(children); err != nil {
				t.Fatalf("the cover the retiring generation actually records at was refused too, so the arm above is about the cover's size and not about the retirement: %v", err)
			}
			if row.active("orders") != 2 {
				t.Fatal("the control proves nothing: the cutover answered nil and the read target did not move")
			}
		})
	})

	t.Run("a live generation declared through a cover it does not record at", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		live := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		quarters := quartered(t)
		across(t, stand, live, quarters, watermarks(1000, 1000, 1000, 1000)...)
		recorded(t, stand, arriving, event.Progress{Highest: 3})

		row := owning("orders", projection.Ungenerated)
		cutover := func(retiring projection.Cover) error {
			return projection.Cutover(ctx, projection.CutoverSpec{
				Checkpoints: stand.checkpoints,
				Generations: row,
				Projection:  "orders",
				From:        projection.Ungenerated,
				To:          2,
				Retiring:    retiring,
				Arriving:    whole,
				Unit:        stand.unit,
			})
		}

		err := cutover(whole)
		if !errors.Is(err, projection.ErrRetired) {
			t.Fatalf("a four-partition generation declared as Whole() answered %v, and a read target moved on the origin lands on a generation that applied three events", err)
		}
		if !strings.Contains(err.Error(), live.String()) {
			t.Fatalf("the refusal is %q and it names neither the generation it could not observe nor what the operator is to do", err)
		}
		if row.activations.Load() != 0 || row.active("orders") != projection.Ungenerated {
			t.Fatalf("the refused cutover issued %d writes and left the read target at generation %d", row.activations.Load(), row.active("orders"))
		}

		t.Run("the control: a generation that genuinely never ran still answers the origin", func(t *testing.T) {
			barrier, err := projection.Observe(ctx, stand.checkpoints, identityOf(t, "orders", 9, projection.Whole()), whole)
			if err != nil || barrier.At != 0 {
				t.Fatalf("a cover with no rows and no retirement behind it answered %+v / %v, and the origin is the true reading of a generation that delivered nothing", barrier, err)
			}
		})

		t.Run("the control: the cover this generation does record at", func(t *testing.T) {
			recorded(t, stand, arriving, event.Progress{Highest: 1000})
			if err := cutover(quarters); err != nil {
				t.Fatalf("the cover the retiring generation records at was refused too, so the arm above is about a cover of four and not about the silence: %v", err)
			}
			if row.active("orders") != 2 {
				t.Fatal("the control proves nothing: the cutover answered nil and the read target did not move")
			}
		})
	})
}

// The window Cutover names and does not close, measured rather than argued. The
// retiring generation is a separate runner committing in its own transaction:
// nothing here claims its rows, so it advances past the barrier while the unit is
// open and the read target lands on a generation standing where it stood a moment
// ago. What this pins is the bound — the regression is exactly that advance and
// nothing more — and the decision beside it, that the cutover writes none of the
// retiring generation's rows and so never takes a live runner's fence away.
func TestTheCutoverWindowIsWhatTheRetiringGenerationAdvancedUnderIt(t *testing.T) {
	ctx := context.Background()
	retiring := identityOf(t, "orders", 1, projection.Whole())
	arriving := identityOf(t, "orders", 2, projection.Whole())
	whole := coverOf(t, projection.Whole())

	cutting := func(t *testing.T, advancing func(held *stand)) (*stand, *ownership, error) {
		t.Helper()
		stand := newStand(t, eventmemory.Spec{})
		recorded(t, stand, retiring, event.Progress{Highest: 1000})
		recorded(t, stand, arriving, event.Progress{Highest: 1000})
		row := owning("orders", 1)
		if advancing != nil {
			row.arrive = func() { advancing(stand) }
		}
		return stand, row, projection.Cutover(ctx, projection.CutoverSpec{
			Checkpoints: stand.points,
			Generations: row,
			Projection:  "orders",
			From:        1,
			To:          2,
			Retiring:    whole,
			Arriving:    whole,
			Unit:        stand.unit,
		})
	}

	stand, row, err := cutting(t, func(held *stand) {
		recorded(t, held, retiring, event.Progress{Highest: 1050})
	})
	if err != nil {
		t.Fatalf("a cutover of two generations at the same watermark answered %v", err)
	}
	if row.active("orders") != 2 {
		t.Fatalf("the read target holds generation %d", row.active("orders"))
	}

	left, reached := stand.row(t, retiring.String()), stand.row(t, arriving.String())
	behind := left.Progress.Highest - reached.Progress.Highest
	if behind != 50 {
		t.Fatalf("the retiring generation stands at %d and the one reads now resolve to at %d, and the window this call documents is the 50 positions it advanced while the unit was open", left.Progress.Highest, reached.Progress.Highest)
	}
	if left.Advance != 2 {
		t.Fatalf("the retiring generation's row stands at advance %d where its own runner wrote it twice, and a cutover that claimed or fenced it would have taken that runner's fence away", left.Advance)
	}
	if saves := stand.points.saves.Load(); saves != 0 {
		t.Fatalf("the cutover issued %d checkpoint saves, and the one write it is allowed is the ownership row", saves)
	}

	t.Run("the control: the same cutover with the retiring generation at rest", func(t *testing.T) {
		stand, row, err := cutting(t, nil)
		if err != nil {
			t.Fatalf("a cutover with nothing advancing under it answered %v", err)
		}
		left, reached := stand.row(t, retiring.String()), stand.row(t, arriving.String())
		if row.active("orders") != 2 || left.Progress.Highest != reached.Progress.Highest {
			t.Fatalf("the read target holds %d, the retiring generation stands at %d and the arriving one at %d — draining or stopping the retiring generation first is what closes the window, and here it closed nothing", row.active("orders"), left.Progress.Highest, reached.Progress.Highest)
		}
	})
}
