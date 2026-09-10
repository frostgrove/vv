package projection_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
	"github.com/frostgrove/vv/runtime"
)

// Which row every call reached, rather than how many calls there were: the loop
// keys its checkpoint calls by the identity, and the identity renders as the
// name only for a projection at Ungenerated over the whole key space — so a
// count says nothing and a name says everything.
type addressed struct {
	event.Checkpoints

	mutex sync.Mutex
	loads []string
	saves []string

	onSave func(checkpoint event.Checkpoint) error
}

func addressing(held event.Checkpoints) *addressed { return &addressed{Checkpoints: held} }

func (this *addressed) Load(ctx context.Context, projection string) (event.Checkpoint, error) {
	this.mutex.Lock()
	this.loads = append(this.loads, projection)
	this.mutex.Unlock()
	return this.Checkpoints.Load(ctx, projection)
}

func (this *addressed) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	this.mutex.Lock()
	this.saves = append(this.saves, checkpoint.Projection)
	this.mutex.Unlock()
	if this.onSave != nil {
		if err := this.onSave(checkpoint); err != nil {
			return err
		}
	}
	return this.Checkpoints.Save(ctx, checkpoint)
}

func (this *addressed) reached() ([]string, []string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return append([]string(nil), this.loads...), append([]string(nil), this.saves...)
}

// Every row the loop WROTE to that is not this identity's own. Reads are not
// asked about here: a resume reads the row of every coarser share on purpose,
// and it is a write landing anywhere but this row that hands one share of the log
// to two writers.
func (this *addressed) wroteBesides(identity projection.Identity) []string {
	_, saved := this.reached()
	var found []string
	for _, name := range saved {
		if name == identity.String() {
			continue
		}
		found = append(found, name)
	}
	return found
}

// The two identities that render as something other than their name, and the one
// that renders as its name. The third is the control: it is the shape every case
// in this package drove before this file existed, and it passes whichever row the
// settlement reads.
var settlements = []struct {
	what       string
	partition  func(t *testing.T) projection.Partition
	generation projection.Generation
	renders    string
}{
	{"a partition", func(t *testing.T) projection.Partition { return partitionOf(t, 0, 1) }, projection.Ungenerated, "orders#0.1"},
	{"a generation", func(*testing.T) projection.Partition { return projection.Whole() }, 2, "orders@2"},
	{"the control: a projection whose identity renders as its name", func(*testing.T) projection.Partition { return projection.Whole() }, projection.Ungenerated, "orders"},
}

// A settlement that re-read the row keyed by Spec.Name would decide a partition's
// or a generation's fate on a row belonging to another topology — absent in the
// ordinary case, so the lost fence falls to the arm that halts. [[D-133]] says a
// save the fence refuses does not halt, and the deployment that produces one is
// every rolling restart.
func TestALostFenceIsSettledOnTheRowOfThisRunnersOwnIdentity(t *testing.T) {
	ctx := context.Background()
	for _, settled := range settlements {
		t.Run(settled.what, func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{MaxRead: 1})
			rows := addressing(stand.checkpoints)
			part := settled.partition(t)
			payloads := []string{"one", "two", "three", "four"}
			for _, payload := range payloads {
				stand.append(t, keyMatching(t, part), payload)
			}

			// The second live instance: it presents the advance this one is about to
			// present, from the same fence, and gets there first every time.
			rows.onSave = func(checkpoint event.Checkpoint) error {
				return stand.checkpoints.Save(ctx, event.Checkpoint{
					Projection: checkpoint.Projection,
					Cursor:     checkpoint.Cursor,
					Advance:    checkpoint.Advance,
				})
			}

			spec := stand.spec("orders", applies(stand.model))
			spec.Checkpoints = rows
			spec.Partition, spec.Generation = part, settled.generation
			spec.Sequence = byKey()
			spec.Ticks, spec.Idle = runtime.SystemTicks, time.Millisecond
			spec.Backoff = projection.Backoff{First: time.Millisecond, Max: time.Millisecond}
			held := newProjection(t, spec)
			identity := identityOf(t, "orders", settled.generation, part)
			if identity.String() != settled.renders {
				t.Fatalf("the identity under test renders %q where the case is about %q", identity, settled.renders)
			}
			running(t, held)

			contended := stand.observer.await(t, "the lost fence was settled", func(state projection.State) bool {
				return state.Phase == projection.PhaseHalted ||
					(state.Phase == projection.PhaseRetrying && errors.Is(state.Err, projection.ErrOvertaken))
			})
			if contended.Phase == projection.PhaseHalted {
				t.Fatalf("%q lost the fence and halted: %v", identity, contended.Err)
			}

			// The reader is rebuilt from the winner's cursor rather than from this
			// pass's, so the loser goes on delivering: a settlement that read another
			// row would resume from that row's cursor instead.
			stand.observer.await(t, "the whole log was applied", func(projection.State) bool {
				return len(stand.model.rows()) == len(payloads)
			})
			if rows := stand.model.rows(); !same(rows, payloads) {
				t.Fatalf("%q applied %v, where a loser that takes the row it lost resumes at that row's own cursor", identity, rows)
			}
			if row := stand.row(t, identity.String()); row.Advance != uint64(len(payloads)) {
				t.Fatalf("the row %q stands at advance %d after %d pages one writer took every time", identity, row.Advance, len(payloads))
			}
			if elsewhere := rows.wroteBesides(identity); len(elsewhere) != 0 {
				t.Fatalf("%q wrote the rows %v, and every checkpoint a runner presents is keyed by its own identity", identity, elsewhere)
			}
			if identity.String() != "orders" {
				if coarse := stand.row(t, "orders"); !coarse.Fresh() {
					t.Fatalf("the row %q stands at advance %d and no runner of that identity ever ran", "orders", coarse.Advance)
				}
			}
			if counted := stand.observer.counted(projection.PhaseHalted); counted != 0 {
				t.Fatalf("%q halted %d times over a lost fence, and a rolling deploy runs two instances of a singleton on purpose", identity, counted)
			}
		})
	}
}

// The other settlement a wrong row corrupts, and it corrupts it permanently: the
// three arms that resolve an unsettled save adopt the tracker they were handed,
// so one rollback under a mis-keyed settlement moves every later checkpoint of
// this runner to another topology's row — after which a restart finds a live row
// where `unclaimed` refuses one, and each of N partitions contending on the one
// row rebuilds its reader from another partition's cursor.
func TestAUnitThatRollsBackLeavesTheAdvanceOnThisRunnersOwnRow(t *testing.T) {
	for _, settled := range settlements {
		t.Run(settled.what, func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{MaxRead: 1})
			rows := addressing(stand.checkpoints)
			part := settled.partition(t)
			payloads := []string{"one", "two", "three", "four", "five"}
			for _, payload := range payloads {
				stand.append(t, keyMatching(t, part), payload)
			}

			refused := errors.New("projection_test: the handler refuses the first page it is given")
			pages := 0
			spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
				pages++
				if pages == 1 {
					return refused
				}
				stand.model.write(payloadsOf(batch.Envelopes)...)
				return nil
			}))
			spec.Checkpoints = rows
			spec.Partition, spec.Generation = part, settled.generation
			spec.Sequence = byKey()
			spec.Advance, spec.Destination = projection.InUnit, projection.Unchecked
			spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
				tx, err := stand.checkpoints.Begin(ctx)
				if err != nil {
					return err
				}
				if err := work(eventmemory.WithTransaction(ctx, tx)); err != nil {
					_ = tx.Rollback(ctx)
					return err
				}
				return tx.Commit(ctx)
			}
			spec.Ticks, spec.Idle = runtime.SystemTicks, time.Millisecond
			spec.Backoff = projection.Backoff{First: time.Millisecond, Max: time.Millisecond}
			identity := identityOf(t, "orders", settled.generation, part)
			running(t, newProjection(t, spec))

			// The control against a vacuous pass: the settlement has to have reached
			// the rolled-back arm, which is the one that hands a tracker over.
			back := stand.observer.await(t, "the unit rolled back and the page came back", func(state projection.State) bool {
				return state.Phase == projection.PhaseRetrying && errors.Is(state.Err, refused)
			})
			if back.Identity != identity {
				t.Fatalf("the rollback was published under the identity %q where this runner is %q", back.Identity, identity)
			}
			stand.observer.await(t, "the whole log was applied", func(state projection.State) bool {
				return state.Phase == projection.PhaseFollowing && len(stand.model.rows()) == len(payloads)
			})

			if row := stand.row(t, identity.String()); row.Advance != uint64(len(payloads)) {
				t.Fatalf("the row %q stands at advance %d where this runner finished %d pages", identity, row.Advance, len(payloads))
			}
			if elsewhere := rows.wroteBesides(identity); len(elsewhere) != 0 {
				t.Fatalf("%q wrote the rows %v after its unit rolled back, and a runner that adopts another topology's row writes every later checkpoint there", identity, elsewhere)
			}
			if identity.String() == "orders" {
				return
			}
			if coarse := stand.row(t, "orders"); !coarse.Fresh() {
				t.Fatalf("the row %q stands at advance %d and no runner of that identity ever ran", "orders", coarse.Advance)
			}
		})
	}
}

func byKey() projection.Sequencer {
	return projection.SequenceBy("by-key", func(envelope event.Envelope) string {
		return string(envelope.Stream.Key)
	})
}

func keyMatching(t *testing.T, part projection.Partition) string {
	t.Helper()
	for index := range 64 {
		if candidate := "k" + strconv.Itoa(index); part.Matches(candidate) {
			return candidate
		}
	}
	t.Fatalf("no key of the fixture's shape lands in the partition %q", part)
	return ""
}
