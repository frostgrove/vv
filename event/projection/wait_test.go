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

// Long enough that two channel sends and a handful of in-memory reads finish
// inside it on any machine this suite runs on, and short enough that the cases
// which are about a deadline elapsing are about a deadline elapsing.
const elapses = 500 * time.Millisecond

type answered struct {
	vis projection.Visibility
	err error
}

// A wait on a goroutine of the test's, which is where a request handler's is,
// so the case can release its polls one at a time and read the answer when it
// comes.
func awaiting(ctx context.Context, spec projection.WaitSpec) <-chan answered {
	held := make(chan answered, 1)
	go func() {
		vis, err := projection.Wait(ctx, spec)
		held <- answered{vis: vis, err: err}
	}()
	return held
}

func answeredWithin(t *testing.T, held <-chan answered) answered {
	t.Helper()
	select {
	case one := <-held:
		return one
	case <-time.After(settle):
		t.Fatal("the wait never answered")
		return answered{}
	}
}

// The ownership row as a wait reads it, with the moment it moves under the
// case's control: a cutover that commits between the first poll and the poll
// that would reach is exactly the false success ES-05 exists to forbid, and it
// is reachable no other way from a test.
type ownedRows struct {
	mutex sync.Mutex
	held  projection.Generation
	reads atomic.Int64

	// The read on which a cutover lands, and what it lands on.
	after int64
	moves projection.Generation
}

func resolving(to projection.Generation) *ownedRows { return &ownedRows{held: to} }

func (this *ownedRows) Active(context.Context, string) (projection.Generation, error) {
	reads := this.reads.Add(1)
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.after > 0 && reads >= this.after {
		this.held = this.moves
	}
	return this.held, nil
}

func (this *ownedRows) Activate(_ context.Context, _ string, from, to projection.Generation) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.held != from {
		return event.ErrConflict
	}
	this.held = to
	return nil
}

// *«Scan checkpoint после parking не доказывает применение события.»* A parked
// sequence is the one case where the watermark is past the mark and the read
// model never received the event: the scan advanced, the envelope went to the
// queue, Quarantined rose and Highest rose with it. A wait that compares only the
// watermark answers reached, which is the whole of what this case forbids.
func TestTheParkIsAskedBeforeTheCensusOnEveryPoll(t *testing.T) {
	t.Run("parked with the watermark already past the mark, answered on the first poll", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		of := identityOf(t, "orders", 2, projection.Whole())
		cover := coverOf(t, projection.Whole())
		mark := markOf(t, stand, "a-17", "A")
		recorded(t, stand, of, event.Progress{Highest: mark.At() + 10, Quarantined: 1})

		held := newPark()
		parkOne(t, held, of, "A")
		spec := parking(t, stand, cover, held)
		spec.Until = mark
		spec.Ticks = neverBeating().Ticks

		vis, err := projection.Wait(context.Background(), spec)
		if !errors.Is(err, projection.ErrParked) {
			t.Fatalf("a wait whose own sequence is parked answered %v, where the scan passed the change and the read model never received it", err)
		}
		if vis != (projection.Visibility{Parked: true, Polls: 1}) {
			t.Fatalf("the answer is %+v, where the park is asked before the census and a true answer reads no census at all", vis)
		}
		if loads := stand.points.loads.Load(); loads != 0 {
			t.Fatalf("a parked answer cost %d checkpoint reads, and the census is not read on this path", loads)
		}

		t.Run("the control: the identical wiring with Park nil reports the parked event reached", func(t *testing.T) {
			spec.Park = nil
			vis, err := projection.Wait(context.Background(), spec)
			if err != nil || !vis.Reached {
				t.Fatalf("the same wait with no queue answered %+v and %v, and this arm is the failure the pair exists to pin", vis, err)
			}
		})
	})

	t.Run("parked in one partition while another lags, answered on the first poll and not at the deadline", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		of := identityOf(t, "orders", 2, projection.Whole())
		cover := quartered(t)
		mark := markOf(t, stand, "a-17", "A")
		across(t, stand, of, cover, watermarks(mark.At()-1, mark.At()+10, mark.At()+10, mark.At()+10)...)

		held := newPark()
		parkOne(t, held, of, "A")
		spec := parking(t, stand, cover, held)
		spec.Until = mark
		spec.Ticks = neverBeating().Ticks

		ctx, cancel := context.WithTimeout(context.Background(), elapses)
		defer cancel()
		vis, err := projection.Wait(ctx, spec)
		if !errors.Is(err, projection.ErrParked) {
			t.Fatalf("a wait whose partition parked the change while another lags answered %v, where the park is asked before the census and the lag is beside the point", err)
		}
		if vis.Polls != 1 {
			t.Fatalf("the parked answer came on poll %d, where asking the park only after reaching would have burned the whole deadline for a condition nameable at once", vis.Polls)
		}

		t.Run("the control: the same wiring with Park nil burns the deadline instead", func(t *testing.T) {
			spec.Park = nil
			ctx, cancel := context.WithTimeout(context.Background(), elapses)
			defer cancel()
			vis, err := projection.Wait(ctx, spec)
			if !errors.Is(err, projection.ErrNotVisible) || vis.Reached {
				t.Fatalf("the same wait with no queue answered %+v and %v, where one member is behind the mark and nothing else refuses", vis, err)
			}
		})
	})
}

// A healthy projection pays one count per poll and nothing else, and Holes is not
// asked at all — not on this generation, not on one with quarantined envelopes,
// and not on the reaching poll. A wait that asked it would have moved a second
// sentence of Park's contract outside a unit of work on a path neither make api
// nor check-event-kernel can see.
func TestAHealthyWaitAsksSequencesAndNeverHoldsOrHoles(t *testing.T) {
	for _, one := range []struct {
		what        string
		quarantined uint64
	}{
		{"a generation that has parked nothing", 0},
		{"a generation with quarantined envelopes", 3},
	} {
		t.Run(one.what, func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{})
			of := identityOf(t, "orders", 2, projection.Whole())
			cover := coverOf(t, projection.Whole())
			mark := markOf(t, stand, "a-17", "A")
			recorded(t, stand, of, event.Progress{Highest: mark.At(), Quarantined: one.quarantined})
			stand.points.onLoad = shortUntil(3)

			watched := watchingPark(newPark())
			spec := parking(t, stand, cover, watched)
			spec.Until = mark
			spec.Ticks = freeRunning().Ticks

			vis, err := projection.Wait(context.Background(), spec)
			if err != nil || !vis.Reached {
				t.Fatalf("a wait over an empty queue answered %+v and %v", vis, err)
			}
			if vis.Polls != 3 {
				t.Fatalf("the wait made %d polls where the checkpoint store answers the mark on the third, so the counts below are over the wrong number of polls", vis.Polls)
			}
			if vis.Quarantined != one.quarantined {
				t.Fatalf("the census reported %d quarantined where the row records %d", vis.Quarantined, one.quarantined)
			}
			watched.counted(t, "Sequences", 0, 3)
			watched.counted(t, "Holds", 0, 0)
			watched.counted(t, "Holes", 0, 0)
			watched.counted(t, "Park", 0, 0)
		})
	}
}

// A caller whose projection is already past its mark pays one round trip and no
// latency at all, and a test of one needs no sleep. The ticker is not asked for
// an interval, because a wait that reached on its first poll never went near one.
func TestAWaitReachesOnItsFirstPollAndNeverSleeps(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	of := identityOf(t, "orders", 2, projection.Whole())
	cover := coverOf(t, projection.Whole())
	mark := markOf(t, stand, "a-17", "A")
	recorded(t, stand, of, event.Progress{Highest: mark.At()})

	beat := neverBeating()
	spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag()}, cover)
	spec.Until = mark
	spec.Every = time.Hour
	spec.Ticks = beat.Ticks

	vis, err := projection.Wait(context.Background(), spec)
	if err != nil || !vis.Reached || vis.Polls != 1 {
		t.Fatalf("a wait over a generation already past its mark answered %+v and %v", vis, err)
	}
	if vis.Behind != 0 || vis.Moved {
		t.Fatalf("a reached wait reports %+v, where Behind is zero on reaching and one observation cannot show a change", vis)
	}
	if asked := beat.intervals(); len(asked) != 0 {
		t.Fatalf("the wait asked for the intervals %v, and one that reaches on its first poll must not wait an hour before making it", asked)
	}

	t.Run("the control: one event short takes at least two polls and does ask", func(t *testing.T) {
		// The load above is the store's first; the poll the row moves on is
		// counted from the arm's own first read rather than from the file's.
		stand.points.loads.Store(0)
		stand.points.onLoad = shortUntil(2)
		driven := neverBeating()
		spec.Ticks = driven.Ticks
		held := awaiting(context.Background(), spec)
		driven.fire(t)
		one := answeredWithin(t, held)
		if one.err != nil || !one.vis.Reached || one.vis.Polls != 2 {
			t.Fatalf("a wait one event short answered %+v and %v, so Polls == 1 above is a measurement and not a constant", one.vis, one.err)
		}
		if asked := driven.intervals(); len(asked) != 1 || asked[0] != time.Hour {
			t.Fatalf("the wait asked for the intervals %v, where a poll that did not reach waits the interval the spec names", asked)
		}
	})
}

// A barrier is a statement about a whole generation folded out of its checkpoint
// rows, so there is no envelope to ask a sequencer about and the mark carries no
// key. Beside a queue that is refused rather than answered: Reached at such a
// mark means delivered to a caller that supplied a Park precisely because it
// wanted applied.
func TestABarrierMintedMarkIsRefusedBesideAPark(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	repo := waitedRepo(t, stand.store)
	of := identityOf(t, "orders", 2, projection.Whole())
	cover := coverOf(t, projection.Whole())
	ctx := context.Background()
	recorded(t, stand, of, event.Progress{Highest: 90})

	plain := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag()}, cover)
	barrier := projection.MarkOf(observedOver(t, stand.checkpoints, of, cover))

	t.Run("an empty commit", func(t *testing.T) {
		_, at, err := repo.Load(ctx, "b-99")
		if err != nil {
			t.Fatalf("loading a stream nothing was appended to answered %v", err)
		}
		_, empty, err := repo.Append(ctx, at)
		if err != nil {
			t.Fatalf("an append of no changes answered %v", err)
		}
		if _, err := plain.Committed(ctx, stand.store, empty); !errors.Is(err, projection.ErrSpec) {
			t.Fatalf("a mint over an empty commit answered %v", err)
		}
	})

	t.Run("the zero mark", func(t *testing.T) {
		spec := plain
		if _, err := projection.Wait(ctx, spec); !errors.Is(err, projection.ErrSpec) {
			t.Fatalf("a wait with the zero Mark answered %v", err)
		}
	})

	t.Run("a barrier of the generation being waited on, with no queue, is admitted", func(t *testing.T) {
		spec := plain
		spec.Until = barrier
		vis, err := projection.Wait(ctx, spec)
		if err != nil || !vis.Reached {
			t.Fatalf("a barrier of the generation being waited on answered %+v and %v — Reached refuses one because that generation clears its own watermark by arithmetic, and a caller waiting for its own generation to pass a mark it observed earlier is asking a question that moves", vis, err)
		}
	})

	t.Run("the same barrier mark beside a queue is refused", func(t *testing.T) {
		spec := parking(t, stand, cover, newPark())
		spec.Until = barrier
		if _, err := projection.Wait(ctx, spec); !errors.Is(err, projection.ErrSpec) {
			t.Fatalf("a barrier-minted mark beside a Park answered %v, where the park cannot be asked about a change the mark carries no key for", err)
		}

		t.Run("the control: the fourth arm with Park nil reaches", func(t *testing.T) {
			spec.Park = nil
			vis, err := projection.Wait(ctx, spec)
			if err != nil || !vis.Reached {
				t.Fatalf("the same wait with no queue answered %+v and %v, so the refusal above is about the missing sequence key and not about the barrier", vis, err)
			}
		})
	})
}

// A bare timeout is the same answer for a slow projector, a stopped one, a
// stopped daemon and a poison event, and the maintainers of the mechanism this
// appendix cites have that open against themselves as a defect. Moved is the
// answer, and it costs nothing because the census read the number anyway.
func TestADeadlineSaysWhichKindOfNotYetItWas(t *testing.T) {
	for _, one := range []struct {
		what  string
		slow  bool
		moved bool
	}{
		{"a projector that is advancing but slow", true, true},
		{"a projector whose runner is stopped", false, false},
	} {
		t.Run(one.what, func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{})
			of := identityOf(t, "orders", 2, projection.Whole())
			ahead := identityOf(t, "orders", 3, projection.Whole())
			cover := coverOf(t, projection.Whole())
			recorded(t, stand, of, event.Progress{Highest: 1})
			recorded(t, stand, ahead, event.Progress{Highest: 10000})

			mark := projection.MarkOf(observedOver(t, stand.checkpoints, ahead, cover))
			if one.slow {
				stand.points.onLoad = func(held event.Checkpoint, loads int64) (event.Checkpoint, error) {
					held.Progress.Highest = event.Position(loads)
					return held, nil
				}
			}

			driven := neverBeating()
			spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points}, cover)
			spec.Until = mark
			spec.Ticks = driven.Ticks

			ctx, cancel := context.WithTimeout(context.Background(), elapses)
			defer cancel()
			held := awaiting(ctx, spec)
			driven.fire(t)
			driven.fire(t)
			got := answeredWithin(t, held)

			if !errors.Is(got.err, projection.ErrNotVisible) || !errors.Is(got.err, context.DeadlineExceeded) {
				t.Fatalf("the deadline answered %v, where a caller that branches on either gets what it expects", got.err)
			}
			if got.vis.Reached || got.vis.Polls < 2 {
				t.Fatalf("the deadline answered %+v, where the Visibility is the last poll that could be read and there were at least three", got.vis)
			}
			if got.vis.Moved != one.moved {
				t.Fatalf("%s reported Moved=%v over %d polls at %d, and that field is the whole of the difference between behind and broken", one.what, got.vis.Moved, got.vis.Polls, got.vis.At)
			}
			if got.vis.Behind != mark.At()-got.vis.At {
				t.Fatalf("the answer reports Behind %d at %d against a mark at %d, and Behind is the distance to the mark as an upper bound", got.vis.Behind, got.vis.At, mark.At())
			}
			if one.slow && got.vis.Behind >= mark.At()-1 {
				t.Fatalf("the advancing projector's Behind stayed at %d, where it shrank from the first poll's", got.vis.Behind)
			}
			if !one.slow && got.vis.Behind != mark.At()-1 {
				t.Fatalf("the stopped projector's Behind is %d where the row never moved off one", got.vis.Behind)
			}
		})
	}

	// The same physical event on the poll the poll number reserves for the caller
	// asking wrong. A tight per-request budget against a loaded checkpoint store
	// is the case UC-213's stale-read branch exists for, and a tight one runs out
	// during the first round trip rather than the fourth — so the exit it takes
	// must not depend on which poll the clock landed in.
	t.Run("a deadline that elapses inside the first poll", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		of := identityOf(t, "orders", 2, projection.Whole())
		cover := coverOf(t, projection.Whole())
		mark := markOf(t, stand, "a-17", "A")
		recorded(t, stand, of, event.Progress{Highest: 1})

		spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag()}, cover)
		spec.Until = mark
		spec.Ticks = neverBeating().Ticks

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		stand.points.onLoad = outlasting(ctx)

		vis, err := projection.Wait(ctx, spec)
		if !errors.Is(err, projection.ErrNotVisible) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("a budget that ran out during the first poll answered %v, where a handler branching on ErrNotVisible serves stale and one reading a topology refusal goes looking for a cover that is not misconfigured", err)
		}
		// The other half of §UC-244's rule, and the damaging direction: the failing
		// poll is rare and the slow one is not, so a deadline that carried the
		// store class it collected from a round trip made with a done context
		// would send a handler branching on §5.2's vocabulary to page an operator
		// over a request that was merely too slow.
		if errors.Is(err, projection.ErrTopology) || errors.Is(err, event.ErrBackend) {
			t.Fatalf("a budget that ran out during the first poll answered %v, where ErrTopology is the caller asking wrong and event.ErrBackend is a store in trouble and this wait was neither", err)
		}
		if vis != (projection.Visibility{Polls: 1}) {
			t.Fatalf("the answer is %+v, where one poll was made and nothing of it could be read", vis)
		}

		t.Run("the control: a parked answer that arrived after the budget elapsed is still the parked answer", func(t *testing.T) {
			bounded, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer stop()
			queue := newPark()
			parkOne(t, queue, of, "A")
			parked := parking(t, stand, cover, lateQueue{held: queue, until: bounded})
			parked.Until = mark
			parked.Ticks = neverBeating().Ticks

			vis, err := projection.Wait(bounded, parked)
			if !errors.Is(err, projection.ErrParked) || !vis.Parked {
				t.Fatalf("a queue that answered after the budget elapsed answered %+v and %v, where a conclusion drawn from rows that were read is not a poll that could not be made", vis, err)
			}
			// ErrParked travels wrapped into the deadline's error too, so the
			// discriminating half is that this one is not a deadline at all: the
			// caller can act on a redrive and cannot act on a timeout.
			if errors.Is(err, projection.ErrNotVisible) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("the parked answer was rendered as a deadline: %v", err)
			}
		})

		// Without this one the arm above proves nothing: a wait that never wrapped
		// a poll's refusal into its deadline would pass it and lose §UC-244's own
		// rule, so the two deadlines are told apart here rather than merged.
		t.Run("the control: a deadline reached over a poll that really failed carries the store's own class", func(t *testing.T) {
			stand.points.loads.Store(0)
			stand.points.onLoad = func(held event.Checkpoint, loads int64) (event.Checkpoint, error) {
				if loads >= 2 {
					return event.Checkpoint{}, errors.New("this checkpoint row could not be read")
				}
				return held, nil
			}
			driven := neverBeating()
			control := spec
			control.Ticks = driven.Ticks
			live, stop := context.WithTimeout(context.Background(), elapses)
			defer stop()

			answers := awaiting(live, control)
			driven.fire(t)
			got := answeredWithin(t, answers)
			if !errors.Is(got.err, projection.ErrNotVisible) || !errors.Is(got.err, context.DeadlineExceeded) {
				t.Fatalf("a deadline reached over a failing poll answered %v", got.err)
			}
			if !errors.Is(got.err, projection.ErrTopology) || !errors.Is(got.err, event.ErrBackend) {
				t.Fatalf("a deadline reached over a poll that could not be made answered %v, where the last poll's own refusal is wrapped into it and the arm above is what that is told apart from", got.err)
			}
		})

		t.Run("the control: the same first poll failing inside a live budget is still the caller asking wrong", func(t *testing.T) {
			stand.points.onLoad = func(event.Checkpoint, int64) (event.Checkpoint, error) {
				return event.Checkpoint{}, errors.New("this checkpoint row could not be read")
			}
			// A deadline this arm must never reach: the first-poll rule answers on
			// the round trip the caller was going to pay anyway, so a branch that
			// swallowed it fails here rather than hanging.
			live, stop := context.WithTimeout(context.Background(), elapses)
			defer stop()
			vis, err := projection.Wait(live, spec)
			if !errors.Is(err, projection.ErrTopology) || errors.Is(err, projection.ErrNotVisible) {
				t.Fatalf("a first poll that failed while the caller's budget was intact answered %v, where the context rule discriminates rather than swallowing the poll-number one", err)
			}
			if vis != (projection.Visibility{Polls: 1}) {
				t.Fatalf("the answer is %+v, where nothing was read and the wait made one poll", vis)
			}
		})
	})

	t.Run("the control: a cancelled context is not a deadline", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		of := identityOf(t, "orders", 2, projection.Whole())
		cover := coverOf(t, projection.Whole())
		recorded(t, stand, of, event.Progress{Highest: 1})
		mark := markOf(t, stand, "a-17", "A")

		spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag()}, cover)
		spec.Until = mark
		spec.Ticks = neverBeating().Ticks

		ctx, cancel := context.WithCancel(context.Background())
		held := awaiting(ctx, spec)
		time.AfterFunc(10*time.Millisecond, cancel)
		got := answeredWithin(t, held)
		if !errors.Is(got.err, context.Canceled) || errors.Is(got.err, projection.ErrNotVisible) {
			t.Fatalf("a cancelled wait answered %v, where the caller stopped asking and a cancelled request stays one", got.err)
		}
		if got.vis != (projection.Visibility{}) {
			t.Fatalf("a cancelled wait answered %+v, where it concluded nothing", got.vis)
		}

		t.Run("a cancellation that lands inside a poll rather than between two", func(t *testing.T) {
			inside, stop := context.WithCancel(context.Background())
			defer stop()
			stand.points.onLoad = outlasting(inside)
			held := awaiting(inside, spec)
			time.AfterFunc(10*time.Millisecond, stop)
			got := answeredWithin(t, held)
			if got.err != context.Canceled {
				t.Fatalf("a wait cancelled during its first poll answered %v, where the answer is ctx.Err() bare and nothing of this package is wrapped around it", got.err)
			}
			if got.vis != (projection.Visibility{}) {
				t.Fatalf("a wait cancelled during its first poll answered %+v, where it concluded nothing", got.vis)
			}
		})
	})
}

// The poll number is the whole of the rule and there is no error classification
// under it. A poll that never worked is the caller asking wrong and is answered
// on the round trip it was going to pay anyway; a poll that stops working is the
// deployment moving — a split in progress, a read of a table that blinked — and a
// request path's wait must not be aborted by an operator's split.
func TestAPollThatCannotBeMadeIsTerminalFirstAndPolledThroughAfter(t *testing.T) {
	blinked := errors.New("this checkpoint row could not be read")

	of := identityOf(t, "orders", 2, projection.Whole())
	cover := coverOf(t, projection.Whole())
	standing := func(t *testing.T, highest func(projection.Mark) event.Position) (*stand, projection.WaitSpec) {
		t.Helper()
		stand := newStand(t, eventmemory.Spec{})
		mark := markOf(t, stand, "a-17", "A")
		recorded(t, stand, of, event.Progress{Highest: highest(mark)})
		spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag()}, cover)
		spec.Until = mark
		return stand, spec
	}
	reachable := func(mark projection.Mark) event.Position { return mark.At() }
	behind := func(projection.Mark) event.Position { return 1 }

	t.Run("a first poll that cannot be made returns at once", func(t *testing.T) {
		stand, spec := standing(t, reachable)
		stand.points.onLoad = func(event.Checkpoint, int64) (event.Checkpoint, error) {
			return event.Checkpoint{}, blinked
		}
		spec.Ticks = neverBeating().Ticks
		// A deadline this arm must never reach: a first-poll refusal is answered
		// on the round trip the caller was going to pay anyway, so an
		// implementation that polled through instead fails here rather than
		// hanging until the suite's own timeout.
		ctx, cancel := context.WithTimeout(context.Background(), elapses)
		defer cancel()

		vis, err := projection.Wait(ctx, spec)
		if !errors.Is(err, projection.ErrTopology) || !errors.Is(err, event.ErrBackend) {
			t.Fatalf("a first-poll failure answered %v, where the census names the member it could not read and the kernel's own read door classifies what the store said", err)
		}
		// What "unreclassified" is: the wait adds no sentence of its own, so the
		// refusal a caller reads is character for character the census's.
		if _, census := projection.Observe(context.Background(), stand.points, of, cover); census.Error() != err.Error() {
			t.Fatalf("the wait answered %q where the census it polls answers %q, and a first-poll refusal travels unwrapped", err, census)
		}
		if vis != (projection.Visibility{Polls: 1}) {
			t.Fatalf("a first-poll failure answered %+v, where nothing was read and the wait made one poll", vis)
		}
	})

	t.Run("a poll that stops working is polled through and the deadline decides", func(t *testing.T) {
		stand, spec := standing(t, behind)
		stand.points.onLoad = func(held event.Checkpoint, loads int64) (event.Checkpoint, error) {
			if loads >= 4 {
				return event.Checkpoint{}, blinked
			}
			return held, nil
		}
		driven := neverBeating()
		spec.Ticks = driven.Ticks
		ctx, cancel := context.WithTimeout(context.Background(), elapses)
		defer cancel()

		answers := awaiting(ctx, spec)
		for range 4 {
			driven.fire(t)
		}
		got := answeredWithin(t, answers)
		if !errors.Is(got.err, projection.ErrNotVisible) || !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("a deadline reached over failing polls answered %v, where a caller that branches on either gets what it expects", got.err)
		}
		if !errors.Is(got.err, projection.ErrTopology) || !errors.Is(got.err, event.ErrBackend) {
			t.Fatalf("a deadline reached over failing polls answered %v, where the last poll's own refusal is wrapped into it so a caller that asks why gets the reason rather than a timeout that means four things", got.err)
		}
		if got.vis.Polls <= 3 {
			t.Fatalf("the wait made %d polls where three of them succeeded before the first failure, so polled-through is assumed rather than measured", got.vis.Polls)
		}
		if got.vis.At != 1 {
			t.Fatalf("the answer is %+v, where the Visibility is the last poll that COULD be read and three of them could", got.vis)
		}
	})

	t.Run("a failure that clears before the deadline reaches", func(t *testing.T) {
		stand, spec := standing(t, reachable)
		stand.points.onLoad = func(held event.Checkpoint, loads int64) (event.Checkpoint, error) {
			if loads == 4 {
				return event.Checkpoint{}, blinked
			}
			if loads < 5 {
				held.Progress.Highest = 0
			}
			return held, nil
		}
		driven := neverBeating()
		spec.Ticks = driven.Ticks
		ctx, cancel := context.WithTimeout(context.Background(), elapses)
		defer cancel()

		answers := awaiting(ctx, spec)
		for range 4 {
			driven.fire(t)
		}
		got := answeredWithin(t, answers)
		if got.err != nil || !got.vis.Reached {
			t.Fatalf("a failure that cleared answered %+v and %v, where the deployment moved back and the wait was still inside its deadline", got.vis, got.err)
		}
		if got.vis.Polls != 5 {
			t.Fatalf("the wait reached on poll %d, where three short polls, one that could not be made and one that reached are five", got.vis.Polls)
		}
	})

	t.Run("the control: the same wiring against a healthy checkpoint store reaches at once", func(t *testing.T) {
		_, spec := standing(t, reachable)
		spec.Ticks = neverBeating().Ticks
		vis, err := projection.Wait(context.Background(), spec)
		if err != nil || !vis.Reached || vis.Polls != 1 {
			t.Fatalf("the wiring every arm above differs from in which load fails answered %+v and %v", vis, err)
		}
	})
}

// Both directions of naming the wrong generation are silent, and one of them is
// the exact outcome ES-05 exists to forbid: the wait answers Reached about a
// generation that is ahead while the caller's own read resolves to the one that
// is behind. The row is read twice and never per poll, and it is the second read
// that turns a false success into a refusal.
func TestACutoverUnderAWaitIsRefusedRatherThanAnswered(t *testing.T) {
	standing := func(t *testing.T, generation projection.Generation) (*stand, projection.Mark, projection.Cover) {
		t.Helper()
		stand := newStand(t, eventmemory.Spec{})
		cover := coverOf(t, projection.Whole())
		mark := markOf(t, stand, "a-17", "A")
		recorded(t, stand, identityOf(t, "orders", generation, projection.Whole()), event.Progress{Highest: mark.At()})
		stand.points.onLoad = shortUntil(2)
		return stand, mark, cover
	}

	t.Run("the retiring generation, cut over between the first poll and the poll that would reach", func(t *testing.T) {
		stand, mark, cover := standing(t, 2)
		rows := resolving(2)
		rows.after, rows.moves = 2, 3

		driven := neverBeating()
		spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag(), Generations: rows}, cover)
		spec.Until = mark
		spec.Ticks = driven.Ticks

		held := awaiting(context.Background(), spec)
		driven.fire(t)
		got := answeredWithin(t, held)
		if !errors.Is(got.err, projection.ErrGeneration) {
			t.Fatalf("a wait on the retiring generation answered %v, where a true statement about a read model the caller is no longer reading is the stale read this exists to forbid", got.err)
		}
		if got.vis.Reached {
			t.Fatalf("the refusal carries %+v, and a refused wait has concluded nothing about the read model", got.vis)
		}

		t.Run("the control: the same wait with Generations nil answers Reached", func(t *testing.T) {
			stand, mark, cover := standing(t, 2)
			driven := neverBeating()
			spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag()}, cover)
			spec.Until = mark
			spec.Ticks = driven.Ticks
			held := awaiting(context.Background(), spec)
			driven.fire(t)
			got := answeredWithin(t, held)
			if got.err != nil || !got.vis.Reached {
				t.Fatalf("the same wait with no ownership row answered %+v and %v, which is the behaviour the field exists to change", got.vis, got.err)
			}
		})
	})

	t.Run("the arriving generation before its cutover, refused on the first poll", func(t *testing.T) {
		stand, mark, cover := standing(t, 3)
		rows := resolving(2)
		spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 3, Checkpoints: stand.points, Sequence: byTag(), Generations: rows}, cover)
		spec.Until = mark
		spec.Ticks = neverBeating().Ticks

		ctx, cancel := context.WithTimeout(context.Background(), elapses)
		defer cancel()
		vis, err := projection.Wait(ctx, spec)
		if !errors.Is(err, projection.ErrGeneration) {
			t.Fatalf("a wait on a generation reads do not resolve to answered %v, where the caller pays one row read instead of a whole deadline", err)
		}
		if vis.Polls != 1 {
			t.Fatalf("the refusal came on poll %d, where the first read is what costs the caller nothing instead of a deadline", vis.Polls)
		}

		t.Run("the control: the same wait with Generations nil burns the deadline", func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{})
			cover := coverOf(t, projection.Whole())
			mark := markOf(t, stand, "a-17", "A")
			recorded(t, stand, identityOf(t, "orders", 2, projection.Whole()), event.Progress{Highest: mark.At()})
			spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 3, Checkpoints: stand.points, Sequence: byTag()}, cover)
			spec.Until = mark
			spec.Ticks = neverBeating().Ticks
			ctx, cancel := context.WithTimeout(context.Background(), elapses)
			defer cancel()
			vis, err := projection.Wait(ctx, spec)
			if !errors.Is(err, projection.ErrNotVisible) || vis.Reached {
				t.Fatalf("the same wait with no ownership row answered %+v and %v, and a nil Generations scopes the promise rather than widening it", vis, err)
			}
		})
	})

	// The poll a caught-up deployment takes every time. Everything the arm above
	// drives happens inside ONE poll here — the park round trip and the census
	// round trip are the window — so a wait that read the row only on the polls
	// after its first would answer Reached about a read model the caller is no
	// longer reading, on the common path and not the slow one.
	t.Run("the retiring generation, cut over inside the census of the first poll", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		cover := coverOf(t, projection.Whole())
		mark := markOf(t, stand, "a-17", "A")
		recorded(t, stand, identityOf(t, "orders", 2, projection.Whole()), event.Progress{Highest: mark.At()})
		rows := resolving(2)
		rows.after, rows.moves = 2, 3

		spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag(), Generations: rows}, cover)
		spec.Until = mark
		spec.Ticks = neverBeating().Ticks

		vis, err := projection.Wait(context.Background(), spec)
		if !errors.Is(err, projection.ErrGeneration) || vis.Reached {
			t.Fatalf("a cutover that committed inside the first poll answered %+v and %v, where the poll that would reach reads the ownership row whichever poll it is", vis, err)
		}
		if vis.Polls != 1 {
			t.Fatalf("the refusal came on poll %d, and this arm is about the first one", vis.Polls)
		}
		if vis.At < mark.At() {
			t.Fatalf("the refusal carries %+v against a mark at %d, so the census had not reached and this arm refused for something else", vis, mark.At())
		}
		if reads := rows.reads.Load(); reads != 2 {
			t.Fatalf("the ownership row was read %d times on the one poll this wait made, where the first read and the reaching read are both of them", reads)
		}

		t.Run("the control: the same first-poll reach with no cutover reaches, and the row is read twice there too", func(t *testing.T) {
			settled := resolving(2)
			held := spec
			held.Generations = settled
			vis, err := projection.Wait(context.Background(), held)
			if err != nil || !vis.Reached || vis.Polls != 1 {
				t.Fatalf("a wait on the generation reads resolve to answered %+v and %v", vis, err)
			}
			if reads := settled.reads.Load(); reads != 2 {
				t.Fatalf("the ownership row was read %d times over one poll, where the two moments the answer changes anything land on the same poll when that poll reaches", reads)
			}
		})
	})

	t.Run("the active generation with no cutover reaches, and the row is read exactly twice", func(t *testing.T) {
		stand, mark, cover := standing(t, 2)
		rows := resolving(2)
		driven := neverBeating()
		spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag(), Generations: rows}, cover)
		spec.Until = mark
		spec.Ticks = driven.Ticks

		held := awaiting(context.Background(), spec)
		driven.fire(t)
		got := answeredWithin(t, held)
		if got.err != nil || !got.vis.Reached {
			t.Fatalf("a wait on the generation reads resolve to answered %+v and %v", got.vis, got.err)
		}
		if reads := rows.reads.Load(); reads != 2 {
			t.Fatalf("the ownership row was read %d times over %d polls, where the first poll and the poll that would reach are the two moments the answer changes anything", reads, got.vis.Polls)
		}
	})
}

// Three of a wait's five declared facts are silently wrong-able by a request
// handler that does not read the projector's wiring, and nothing in this
// framework can check any of them: it holds a Checkpoints and a Park and never a
// Spec. The three arms assert the WRONG answers, so the day one becomes checkable
// the arm fails and says so.
func TestTheDerivedSpecAnswersWhatThreeHandWrittenOnesGetWrong(t *testing.T) {
	t.Run("a Sequence that is not the projection's, and a nil Park", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		of := identityOf(t, "orders", 2, projection.Whole())
		cover := quartered(t)
		mark := markOf(t, stand, "a-17", "A")
		// Above both marks, so the census reaches for every arm and what each
		// one differs in is the only thing deciding its answer.
		across(t, stand, of, cover, watermarks(mark.At()+100, mark.At()+100, mark.At()+100, mark.At()+100)...)

		held := newPark()
		parkOne(t, held, of, "A")
		derived := parking(t, stand, cover, held)
		derived.Until = mark
		derived.Ticks = neverBeating().Ticks

		if _, err := projection.Wait(context.Background(), derived); !errors.Is(err, projection.ErrParked) {
			t.Fatalf("the derived spec answered %v, where the queue holds the caller's own sequence", err)
		}

		foreign := derived
		foreign.Sequence = projection.Unordered()
		elsewhere, err := foreign.Committed(context.Background(), stand.store, committing(t, waitedRepo(t, stand.store), "a-17", "A"))
		if err != nil {
			t.Fatalf("minting a mark under a foreign sequencer answered %v", err)
		}
		foreign.Until = elsewhere
		vis, err := projection.Wait(context.Background(), foreign)
		if err != nil || !vis.Reached {
			t.Fatalf("a hand-written spec naming another sequencer answered %+v and %v, and the arm asserts the wrong answer because nothing here can check that field", vis, err)
		}

		unqueued := derived
		unqueued.Park = nil
		vis, err = projection.Wait(context.Background(), unqueued)
		if err != nil || !vis.Reached {
			t.Fatalf("a hand-written spec with a nil Park answered %+v and %v, and the arm asserts the wrong answer because the field's zero value is the mistake", vis, err)
		}
	})

	t.Run("an Over that is not the cover the rows are recorded at", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		of := identityOf(t, "orders", 2, projection.Whole())
		cover := quartered(t)
		mark := markOf(t, stand, "a-17", "A")
		across(t, stand, of, cover, watermarks(mark.At()-1, mark.At(), mark.At(), mark.At())...)
		// The row the generation recorded at before it was partitioned, which
		// nothing removes and which a hand-written cover of Whole() folds a
		// minimum over.
		recorded(t, stand, of, event.Progress{Highest: mark.At() + 100})

		derived := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag()}, cover)
		derived.Until = mark
		derived.Ticks = neverBeating().Ticks
		ctx, cancel := context.WithTimeout(context.Background(), elapses)
		defer cancel()
		if vis, err := projection.Wait(ctx, derived); !errors.Is(err, projection.ErrNotVisible) || vis.Reached {
			t.Fatalf("the derived spec answered %+v and %v, where one of the four members is behind the mark", vis, err)
		}

		coarse := derived
		coarse.Over = coverOf(t, projection.Whole())
		vis, err := projection.Wait(context.Background(), coarse)
		if err != nil || !vis.Reached {
			t.Fatalf("a hand-written spec naming a cover the rows are not recorded at answered %+v and %v, and the arm asserts the wrong answer because a plausible wrong cover folds a minimum over the wrong set", vis, err)
		}
	})

	t.Run("the fourth arm: WaitOf refuses the zero Cover and a spec that names no identity", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		if _, err := projection.WaitOf(projection.Spec{Name: "orders", Checkpoints: stand.points}, projection.Cover{}); !errors.Is(err, projection.ErrTopology) {
			t.Fatalf("WaitOf over the zero Cover answered %v", err)
		}
		if _, err := projection.WaitOf(projection.Spec{Checkpoints: stand.points}, coverOf(t, projection.Whole())); !errors.Is(err, projection.ErrSpec) {
			t.Fatalf("WaitOf over a spec that names no identity answered %v", err)
		}
	})
}

// New applies its defaults in one place and WaitOf reads that one place, so a
// third spelling of either is unwritable. The Park clause is the one that would
// have shipped a defect: a composition root that fills in Spec.Park beside a
// policy that is not ParkSequence has a projection New drops the queue from, and
// a wait that copied the field verbatim would ask a queue no pass ever writes to.
func TestWaitOfDerivesTheSameDefaultsNewApplies(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	cover := coverOf(t, projection.Whole())
	held := newPark()

	beside := waitingOver(t, projection.Spec{
		Name: "orders", Generation: 2, Checkpoints: stand.points,
		Park: held, OnPermanentFailure: projection.Halt,
	}, cover)
	if beside.Park != nil {
		t.Fatal("a Park supplied beside a policy that never writes to it survived the derivation, and a wait would then ask a queue no pass writes a letter to")
	}
	if beside.Sequence == nil || beside.Sequence.Name() != "by-stream" {
		t.Fatalf("a nil Sequence derived %v, where New's own default is ByStream()", beside.Sequence)
	}

	t.Run("the control: the same spec with ParkSequence keeps its queue", func(t *testing.T) {
		kept := waitingOver(t, projection.Spec{
			Name: "orders", Generation: 2, Checkpoints: stand.points,
			Park: held, OnPermanentFailure: projection.ParkSequence,
		}, cover)
		if kept.Park == nil {
			t.Fatal("a Park supplied beside the policy that writes to it was dropped, so the clause above drops every queue rather than the ones New drops")
		}
	})

	t.Run("a park that refuses outside a unit fails the first poll under the naive derivation and not under this one", func(t *testing.T) {
		of := identityOf(t, "orders", 2, projection.Whole())
		mark := markOf(t, stand, "a-17", "A")
		recorded(t, stand, of, event.Progress{Highest: mark.At()})

		demanding := watchingPark(newPark())
		demanding.demanding = true
		derived := waitingOver(t, projection.Spec{
			Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag(),
			Park: demanding, OnPermanentFailure: projection.Halt,
		}, cover)
		derived.Until = mark
		derived.Ticks = neverBeating().Ticks

		vis, err := projection.Wait(context.Background(), derived)
		if err != nil || !vis.Reached {
			t.Fatalf("the derived spec answered %+v and %v, where the queue it would have asked is one New itself drops", vis, err)
		}

		naive := derived
		naive.Park = demanding
		if _, err := projection.Wait(context.Background(), naive); err == nil {
			t.Fatal("a wait that copied Spec.Park verbatim reached, where a Sequences that refuses outside a unit fails the first poll and the first poll is terminal")
		}
	})
}

// A wait is a caller's read and nothing else: no goroutine, no transaction, no
// line, no save, and no tracker reused across one. The second arm is the cost
// the module page states — a member that has recorded nothing costs two reads
// rather than one, because the census asks an absent row whether a split handed
// it down.
func TestAWaitStartsNothingAndSavesNothing(t *testing.T) {
	t.Run("one Load per cover member per poll for a recorded generation", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		of := identityOf(t, "orders", 2, projection.Whole())
		cover := quartered(t)
		mark := markOf(t, stand, "a-17", "A")
		across(t, stand, of, cover, watermarks(mark.At(), mark.At(), mark.At(), mark.At())...)

		spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag()}, cover)
		spec.Until = mark
		spec.Ticks = neverBeating().Ticks

		vis, err := projection.Wait(context.Background(), spec)
		if err != nil || !vis.Reached || vis.Polls != 1 {
			t.Fatalf("a wait over four recorded members answered %+v and %v", vis, err)
		}
		if loads := stand.points.loads.Load(); loads != 4 {
			t.Fatalf("one poll over a four-member cover cost %d reads, where a recorded member costs one", loads)
		}
		if saves, forgets := stand.points.saves.Load(), stand.points.forgets.Load(); saves != 0 || forgets != 0 {
			t.Fatalf("a wait wrote %d saves and %d forgets, and it holds the writer's own value only to read through it", saves, forgets)
		}
	})

	t.Run("two per member while that member has recorded nothing", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		cover := quartered(t)
		mark := markOf(t, stand, "a-17", "A")

		spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag()}, cover)
		spec.Until = mark
		spec.Ticks = neverBeating().Ticks

		ctx, cancel := context.WithTimeout(context.Background(), elapses)
		defer cancel()
		vis, err := projection.Wait(ctx, spec)
		if !errors.Is(err, projection.ErrNotVisible) || vis.Polls != 1 {
			t.Fatalf("a wait over a generation that has recorded nothing answered %+v and %v, where the origin is the honest reading of no rows at all", vis, err)
		}
		if loads := stand.points.loads.Load(); loads != 8 {
			t.Fatalf("one poll over a four-member cover that has recorded nothing cost %d reads, where a fresh member costs two — its own row and the retirement row a split would have written", loads)
		}
		if saves, forgets := stand.points.saves.Load(), stand.points.forgets.Load(); saves != 0 || forgets != 0 {
			t.Fatalf("a wait wrote %d saves and %d forgets", saves, forgets)
		}
	})
}

// «stale-read разрешается явно» is served by the return shape and not by a field:
// a wait that did not reach answers a filled-in Visibility beside a refusal, so
// reading anyway is an `if errors.Is(err, ErrNotVisible)` a reviewer can see. A
// boolean that turns the refusal into a success is the field every caller ends up
// passing, after which the name of the call is a lie.
func TestThereIsNoFieldThatTurnsARefusalIntoASuccess(t *testing.T) {
	walked := 0
	for _, form := range []reflect.Type{reflect.TypeFor[projection.WaitSpec](), reflect.TypeFor[projection.Visibility]()} {
		walked += form.NumField()
		for _, complaint := range switchesOffAWait(form) {
			t.Error(complaint)
		}
	}
	if walked < 16 {
		t.Fatalf("%d fields were read across WaitSpec and Visibility, so this walked the wrong forms", walked)
	}

	t.Run("the control: a form carrying one is reported", func(t *testing.T) {
		type fixture struct {
			Until      int
			AllowStale bool
			OnTimeout  func()
		}
		reported := strings.Join(switchesOffAWait(reflect.TypeFor[fixture]()), "\n")
		for _, named := range []string{"AllowStale", "OnTimeout"} {
			if !strings.Contains(reported, named) {
				t.Fatalf("the fixture's %s was not reported, so the arm above proves nothing:\n%s", named, reported)
			}
		}
		if strings.Contains(reported, "Until") {
			t.Fatalf("the fixture's Until was reported, so this walk reports every field rather than the ones that switch a check off: %s", reported)
		}
	})
}

func switchesOffAWait(form reflect.Type) []string {
	var complaints []string
	for index := range form.NumField() {
		named := form.Field(index).Name
		for _, forbidden := range []string{"stale", "allow", "ontimeout", "ignore", "degrade", "fallback", "besteffort", "force"} {
			if strings.Contains(strings.ToLower(named), forbidden) {
				complaints = append(complaints, fmt.Sprintf("%s.%s turns a refusal into a success, and a check that can be switched off is one every caller switches off", form.Name(), named))
			}
		}
	}
	return complaints
}

// A refusal names the rule that was broken and never the data that broke it. The
// paths a store's own refusal travels through are not read here: those are
// returned unwrapped and unreclassified by design, and what they say is that
// store's business.
func TestNoRefusalOfAWaitNamesAPositionOrAKey(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	repo := waitedRepo(t, stand.store)
	of := identityOf(t, "orders", 2, projection.Whole())
	cover := coverOf(t, projection.Whole())
	ctx := context.Background()

	// Padded so the position under test is two digits, which is what makes
	// searching a message for it mean something: a single digit would match
	// wording that is not a number at all.
	committing(t, repo, "padding", "pad", "pad", "pad", "pad", "pad", "pad", "pad", "pad", "pad", "pad", "pad", "pad")
	commit := committing(t, repo, "zulu-key", "quebec-tag")
	mark := markOf(t, stand, "zulu-key", "quebec-tag")
	recorded(t, stand, of, event.Progress{Highest: mark.At()})
	at := strconv.FormatUint(uint64(mark.At()), 10)
	if len(at) < 2 {
		t.Fatalf("the mark under test is at position %s, and a one-digit needle finds wording rather than data", at)
	}
	identity := []string{"zulu-key", "quebec-tag", "orders@2", at}

	spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag()}, cover)
	spec.Until = mark
	spec.Ticks = neverBeating().Ticks

	// Every row runs under a deadline it must not reach: a door refusal that
	// stopped refusing would otherwise poll until the suite's own timeout instead
	// of answering the wrong sentinel where this table can read it.
	waiting := func(change func(*projection.WaitSpec)) error {
		held := spec
		change(&held)
		bounded, stop := context.WithTimeout(ctx, 50*time.Millisecond)
		defer stop()
		_, err := projection.Wait(bounded, held)
		return err
	}
	minting := func(change func(*projection.WaitSpec), store event.Store, commit event.Commit) error {
		held := spec
		change(&held)
		_, err := held.Committed(ctx, store, commit)
		return err
	}

	queue := newPark()
	parkOne(t, queue, of, "quebec-tag")
	shortPage := watchingStore(stand.store)
	shortPage.answer = func(page []event.Envelope, _ int64) ([]event.Envelope, error) { return nil, nil }
	longPage := watchingStore(stand.store)
	longPage.page = 1
	longPage.answer = func(page []event.Envelope, _ int64) ([]event.Envelope, error) {
		return []event.Envelope{page[0], page[0]}, nil
	}

	rows := resolving(3)

	behind := newStand(t, eventmemory.Spec{})
	recorded(t, behind, of, event.Progress{Highest: 1})
	lagging := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: behind.points, Sequence: byTag()}, cover)
	lagging.Until = mark
	lagging.Ticks = neverBeating().Ticks
	deadline, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, elapsed := projection.Wait(deadline, lagging)

	for _, one := range []struct {
		what    string
		refused error
		want    error
	}{
		{"a wait with the zero Mark", waiting(func(held *projection.WaitSpec) { held.Until = projection.Mark{} }), projection.ErrSpec},
		{"a wait over a mark of another projection",
			waiting(func(held *projection.WaitSpec) {
				held.Of = identityOf(t, "invoices", 2, projection.Whole())
			}), projection.ErrSpec},
		{"a wait over a barrier-minted mark beside a queue",
			waiting(func(held *projection.WaitSpec) {
				held.Until = projection.MarkOf(observedOver(t, stand.checkpoints, of, cover))
				held.Park = newPark()
			}), projection.ErrSpec},
		{"a wait whose Of is the zero Identity", waiting(func(held *projection.WaitSpec) { held.Of = projection.Identity{} }), projection.ErrSpec},
		{"a wait whose Of names a partition",
			waiting(func(held *projection.WaitSpec) {
				held.Of = identityOf(t, "orders", 2, partitionOf(t, 1, 3))
			}), projection.ErrTopology},
		{"a wait whose Over is the zero Cover", waiting(func(held *projection.WaitSpec) { held.Over = projection.Cover{} }), projection.ErrTopology},
		{"a wait whose own sequence is parked", waiting(func(held *projection.WaitSpec) { held.Park = queue }), projection.ErrParked},
		{"a wait on a generation reads do not resolve to", waiting(func(held *projection.WaitSpec) { held.Generations = rows }), projection.ErrGeneration},
		{"a wait that reached its deadline", elapsed, projection.ErrNotVisible},
		{"a mint over an empty commit", minting(func(*projection.WaitSpec) {}, stand.store, event.Commit{}), projection.ErrSpec},
		{"a mint with no sequencer beside a queue",
			minting(func(held *projection.WaitSpec) { held.Sequence, held.Park = nil, newPark() }, stand.store, commit), projection.ErrSpec},
		{"a mint through no store", minting(func(*projection.WaitSpec) {}, nil, commit), projection.ErrSpec},
		{"a mint over a store that shows no event at the commit's last version",
			minting(func(*projection.WaitSpec) {}, shortPage, commit), projection.ErrUncommitted},
		{"a mint over a page whose first envelope is another stream's",
			minting(func(*projection.WaitSpec) {}, crossed(t, stand, repo), commit), event.ErrBackend},
		{"a mint over a page longer than the stream page the store publishes",
			minting(func(*projection.WaitSpec) {}, longPage, commit), event.ErrBackend},
		{"a mint inside the transaction that wrote the append",
			func() error {
				tx, err := stand.store.Begin(ctx)
				if err != nil {
					t.Fatalf("opening a transaction of the store answered %v", err)
				}
				defer func() { _ = tx.Rollback(ctx) }()
				_, err = spec.Committed(eventmemory.WithTransaction(ctx, tx), stand.store, commit)
				return err
			}(), projection.ErrSpec},
	} {
		if !errors.Is(one.refused, one.want) {
			t.Fatalf("%s answered %v where the rule it broke is %v, so the row below reads a message nobody published", one.what, one.refused, one.want)
		}
		matched := 0
		for _, sentinel := range waitVocabulary() {
			if errors.Is(one.refused, sentinel) {
				matched++
			}
		}
		if matched != 1 {
			t.Fatalf("%s answers %d of the published sentinels, and a refusal belongs to exactly one class", one.what, matched)
		}
		for _, named := range identity {
			if strings.Contains(one.refused.Error(), named) {
				t.Fatalf("%s rendered %q into %q, and a refusal names the rule that was broken and never the data that broke it", one.what, named, one.refused)
			}
		}
	}
}

func waitVocabulary() []error {
	return []error{
		projection.ErrSpec, projection.ErrHalted, projection.ErrOvertaken, projection.ErrUnrouted,
		projection.ErrTopology, projection.ErrParkFull, projection.ErrClaimLost, projection.ErrRetired,
		projection.ErrNotVisible, projection.ErrParked, projection.ErrUncommitted, projection.ErrGeneration,
		event.ErrBackend,
	}
}

// A store answering a page that begins with another stream's row, which is the
// one honesty check the mint cannot borrow from the kernel.
func crossed(t *testing.T, stand *stand, repo *event.Repo[waited, waitedID]) event.Store {
	t.Helper()
	elsewhere := committing(t, repo, "yankee-key", "sierra-tag")
	watched := watchingStore(stand.store)
	watched.answer = func(page []event.Envelope, _ int64) ([]event.Envelope, error) {
		other, err := stand.store.ReadStream(context.Background(), elsewhere.Stream(), 0)
		if err != nil {
			return nil, err
		}
		return append(other, page...), nil
	}
	return watched
}

// A checkpoint row that answers the origin until the poll named, so a case
// decides how many polls a wait makes without a clock and without a sleep.
func shortUntil(poll int64) func(event.Checkpoint, int64) (event.Checkpoint, error) {
	return func(held event.Checkpoint, loads int64) (event.Checkpoint, error) {
		if loads < poll {
			held.Progress.Highest = 0
		}
		return held, nil
	}
}

// A queue whose count outlasts the caller's budget, so the parked answer lands
// after the context is done and the two exits are made to collide. The interface
// is held in a named field rather than embedded, because Park names both a
// method of its own and the field an embedded one would take.
type lateQueue struct {
	held  projection.Park
	until context.Context
}

func (this lateQueue) Sequences(ctx context.Context, of projection.Identity) (uint64, error) {
	<-this.until.Done()
	return this.held.Sequences(ctx, of)
}

func (this lateQueue) Holds(ctx context.Context, of projection.Identity, sequence string) (bool, error) {
	return this.held.Holds(ctx, of, sequence)
}

func (this lateQueue) Park(ctx context.Context, letter projection.Letter) error {
	return this.held.Park(ctx, letter)
}

func (this lateQueue) Holes(ctx context.Context, of projection.Identity) (uint64, error) {
	return this.held.Holes(ctx, of)
}

// A checkpoint store a round trip of which outlasts the caller's budget: the
// load blocks until the context is done and then answers what it was given,
// which is what a real one does under load against a short per-request deadline
// and is the only way a case lands the caller's own context INSIDE a poll.
func outlasting(ctx context.Context) func(event.Checkpoint, int64) (event.Checkpoint, error) {
	return func(event.Checkpoint, int64) (event.Checkpoint, error) {
		<-ctx.Done()
		return event.Checkpoint{}, ctx.Err()
	}
}

// A wait spec with the projection's own queue and its own sequencer, which is
// what WaitOf derives for a projection that parks.
func parking(t *testing.T, stand *stand, cover projection.Cover, held projection.Park) projection.WaitSpec {
	t.Helper()
	return waitingOver(t, projection.Spec{
		Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag(),
		Park: held, OnPermanentFailure: projection.ParkSequence,
	}, cover)
}

// A mark over one appended fact, minted the way a caller mints one: the append
// commits, and the position is read back afterwards.
func markOf(t *testing.T, stand *stand, id waitedID, tags ...string) projection.Mark {
	t.Helper()
	repo := waitedRepo(t, stand.store)
	// A mark at position one leaves no room for a generation that is one behind
	// it, and the state half of these cases are about is a generation that is.
	committing(t, repo, "before-the-mark", "pad", "pad", "pad", "pad", "pad")
	commit := committing(t, repo, id, tags...)
	spec := waitingOver(t, projection.Spec{
		Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag(),
	}, coverOf(t, projection.Whole()))
	mark, err := spec.Committed(context.Background(), stand.store, commit)
	if err != nil {
		t.Fatalf("minting a mark over %v answered %v", tags, err)
	}
	return mark
}
