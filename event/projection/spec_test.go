package projection_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

func applies(model *model) projection.HandlerFunc {
	return func(_ context.Context, batch projection.Batch) error {
		model.write(payloadsOf(batch.Envelopes)...)
		return nil
	}
}

// Every refusal New makes, each naming the field it is about. The set is
// enumerated rather than sampled, because a spec assembled wrong that is
// accepted is a projection that runs and does the wrong thing quietly — and the
// control below is the one that keeps the whole table from being satisfied by a
// New that refuses everything.
func TestNewRefusesEverySpecItCannotAssemble(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	legal := stand.spec("orders", applies(stand.model))

	for _, refused := range []struct {
		what  string
		names string
		build func(spec projection.Spec) projection.Spec
	}{
		{"a name no checkpoint row can be keyed by", "Name", func(spec projection.Spec) projection.Spec {
			spec.Name = ""
			return spec
		}},
		{"a name with a control character", "Name", func(spec projection.Spec) projection.Spec {
			spec.Name = "orders\x00v1"
			return spec
		}},
		{"a name over the identifier bound", "Name", func(spec projection.Spec) projection.Spec {
			spec.Name = strings.Repeat("o", event.MaxNameBytes+1)
			return spec
		}},
		{"no log", "Log", func(spec projection.Spec) projection.Spec {
			spec.Log = nil
			return spec
		}},
		{"no handler", "Handler", func(spec projection.Spec) projection.Spec {
			spec.Handler = nil
			return spec
		}},
		{"no checkpoint store", "Checkpoints", func(spec projection.Spec) projection.Spec {
			spec.Checkpoints = nil
			return spec
		}},
		{"a log it could append through", "event.ReadOnly", func(spec projection.Spec) projection.Spec {
			spec.Log = stand.store
			return spec
		}},
		{"an advance mode outside the enum", "Advance", func(spec projection.Spec) projection.Spec {
			spec.Advance = projection.Advance(9)
			return spec
		}},
		{"InUnit with no unit", "Unit", func(spec projection.Spec) projection.Spec {
			spec.Advance = projection.InUnit
			spec.Destination = projection.Unchecked
			return spec
		}},
		{"InUnit with no destination", "Destination", func(spec projection.Spec) projection.Spec {
			spec.Advance = projection.InUnit
			spec.Unit = runsTheWork
			return spec
		}},
		{"InUnit over a checkpoint store with no transactions", "InUnit", func(spec projection.Spec) projection.Spec {
			spec.Advance = projection.InUnit
			spec.Unit = runsTheWork
			spec.Destination = projection.Unchecked
			spec.Checkpoints = &watchedCheckpoints{Checkpoints: stand.checkpoints, transactions: event.Unsupported}
			return spec
		}},
		{"a unit beside AfterApply", "Unit", func(spec projection.Spec) projection.Spec {
			spec.Unit = runsTheWork
			return spec
		}},
		{"a permanent-failure policy outside the enum", "OnPermanentFailure", func(spec projection.Spec) projection.Spec {
			spec.OnPermanentFailure = projection.Failure(4)
			return spec
		}},
		{"ParkSequence with no queue", "Park", func(spec projection.Spec) projection.Spec {
			spec.OnPermanentFailure = projection.ParkSequence
			spec.Advance = projection.InUnit
			spec.Unit = runsTheWork
			spec.Destination = readModel{}
			return spec
		}},
		{"ParkSequence beside AfterApply", "ParkSequence", func(spec projection.Spec) projection.Spec {
			spec.OnPermanentFailure = projection.ParkSequence
			spec.Park = newPark()
			return spec
		}},
		{"ParkSequence beside a destination nothing can resolve", "Destination", func(spec projection.Spec) projection.Spec {
			spec.OnPermanentFailure = projection.ParkSequence
			spec.Park = newPark()
			spec.Advance = projection.InUnit
			spec.Unit = runsTheWork
			spec.Destination = projection.Unchecked
			return spec
		}},
		{"a backoff that shrinks", "Backoff", func(spec projection.Spec) projection.Spec {
			spec.Backoff = projection.Backoff{First: time.Minute, Max: time.Second}
			return spec
		}},
		{"a negative first backoff", "Backoff.First", func(spec projection.Spec) projection.Spec {
			spec.Backoff = projection.Backoff{First: -time.Second}
			return spec
		}},
		{"a negative poll", "Idle", func(spec projection.Spec) projection.Spec {
			spec.Idle = -time.Second
			return spec
		}},
		{"a negative tolerance", "Tolerate", func(spec projection.Spec) projection.Spec {
			spec.Tolerate = -time.Second
			return spec
		}},
		{"a negative attempt budget", "Attempts", func(spec projection.Spec) projection.Spec {
			spec.Attempts = -1
			return spec
		}},
		{"a name carrying the generation delimiter", "Name", func(spec projection.Spec) projection.Spec {
			spec.Name = "orders@2"
			return spec
		}},
		{"a name carrying the partition delimiter", "Name", func(spec projection.Spec) projection.Spec {
			spec.Name = "orders#3.7"
			return spec
		}},
		{"a name its generation and partition push over the bound", "Name", func(spec projection.Spec) projection.Spec {
			spec.Name = strings.Repeat("o", event.MaxNameBytes-4)
			spec.Generation = 12
			spec.Partition = partitionOf(t, 3, 7)
			return spec
		}},
		{"a sequencer that does not name itself", "Sequence", func(spec projection.Spec) projection.Spec {
			spec.Sequence = projection.SequenceBy("", func(event.Envelope) string { return "one" })
			return spec
		}},
		{"a SequenceBy over a nil function", "Sequence", func(spec projection.Spec) projection.Spec {
			spec.Sequence = projection.SequenceBy("by-order", nil)
			return spec
		}},
	} {
		t.Run(refused.what, func(t *testing.T) {
			built, err := projection.New(refused.build(legal))
			if err == nil {
				t.Fatalf("a spec carrying %s was accepted, and it assembles a projection that runs and is wrong", refused.what)
			}
			if built != nil {
				t.Fatal("a refused spec answered a projection as well as an error")
			}
			if !errors.Is(err, projection.ErrSpec) {
				t.Fatalf("%s was refused as %v, which is not the class a spec that cannot be assembled carries", refused.what, err)
			}
			if !strings.Contains(err.Error(), refused.names) {
				t.Fatalf("%s was refused with %q, which does not name %s and leaves the caller to find the field", refused.what, err, refused.names)
			}
		})
	}

	t.Run("a spec wrong in three places reports three problems", func(t *testing.T) {
		wrong := legal
		wrong.Name = "orders@2"
		wrong.Log = nil
		wrong.Sequence = projection.SequenceBy("", func(event.Envelope) string { return "one" })

		_, err := projection.New(wrong)
		joined, collected := err.(interface{ Unwrap() []error })
		if !collected {
			t.Fatalf("a spec wrong in three places was refused with %v, which is one problem rather than the collection a caller fixes in one pass", err)
		}
		if held := joined.Unwrap(); len(held) != 3 {
			t.Fatalf("a spec wrong in three places reported %d problems: %v", len(held), held)
		}
		for _, names := range []string{"Name", "Log", "Sequence"} {
			if !strings.Contains(err.Error(), names) {
				t.Fatalf("the three problems read %q and do not name %s", err, names)
			}
		}
	})

	t.Run("the control: the same spec with none of them", func(t *testing.T) {
		if _, err := projection.New(legal); err != nil {
			t.Fatalf("the legal spec every case above was built from was itself refused: %v", err)
		}
	})
}

func runsTheWork(ctx context.Context, work func(context.Context) error) error { return work(ctx) }

// A projector that can append is how a replay writes, so the refusal is about
// the capability the value carries and not about the type name. The control is
// what makes that true: the same store through event.ReadOnly is accepted and
// serves a page.
func TestASpecCarryingAStoreIsRefusedAndReadOnlyIsAccepted(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "first")

	refused := stand.spec("orders", applies(stand.model))
	refused.Log = stand.store
	if _, err := projection.New(refused); !errors.Is(err, projection.ErrSpec) {
		t.Fatalf("a projection over a value it can append through was refused as %v", err)
	}

	accepted := stand.spec("orders", applies(stand.model))
	accepted.Log = event.ReadOnly(stand.store)
	held := newProjection(t, accepted)
	running(t, held)

	stand.observer.await(t, "the page was applied", func(state projection.State) bool {
		return state.Progress.Applied == 1
	})
	if rows := stand.model.rows(); !same(rows, []string{"first"}) {
		t.Fatalf("a projection over event.ReadOnly applied %v, so the accepted half serves nothing and the refusal above proves nothing", rows)
	}

	for index := range reflect.TypeFor[projection.Spec]().NumField() {
		field := reflect.TypeFor[projection.Spec]().Field(index)
		if field.Type == reflect.TypeFor[event.Store]() {
			t.Fatalf("Spec.%s is an event.Store, so a projector holds the append surface whatever New refuses", field.Name)
		}
	}
}

type elsewhere struct{ name string }

// The five halves of "InUnit is asked for and cannot be honoured", four of which
// are reachable with no database: three at New and one per pass, inside the unit
// and before the handler runs. None of them may fall back to AfterApply — a
// projection that asked for an atomic advance and silently got a separate one
// double-applies on every crash, and every test over a process that never
// crashes is green.
func TestInUnitIsRefusedAndNeverDowngraded(t *testing.T) {
	t.Run("a unit that binds nothing the checkpoint store recognises", func(t *testing.T) {
		refusedOnTheFirstPass(t, func(_ *stand, ctx context.Context, work func(context.Context) error) error {
			return work(ctx)
		}, projection.Unchecked)
	})

	t.Run("a unit that binds something that is not a transaction", func(t *testing.T) {
		refusedOnTheFirstPass(t, func(_ *stand, ctx context.Context, work func(context.Context) error) error {
			return work(eventmemory.WithTransaction(ctx, nil))
		}, projection.Unchecked)
	})

	t.Run("a destination the unit bound no executor for", func(t *testing.T) {
		refusedOnTheFirstPass(t, func(stand *stand, ctx context.Context, work func(context.Context) error) error {
			tx, err := stand.checkpoints.Begin(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback(ctx) }()
			return work(eventmemory.WithTransaction(ctx, tx))
		}, elsewhere{name: "the read model in another database"})
	})

	t.Run("the control: the same code path, wired right", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "first")

		spec := stand.spec("orders", applies(stand.model))
		spec.Advance = projection.InUnit
		spec.Destination = projection.Unchecked
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
		running(t, newProjection(t, spec))

		stand.observer.await(t, "the page was applied inside the unit", func(state projection.State) bool {
			return state.Progress.Applied == 1
		})
		if rows := stand.model.rows(); !same(rows, []string{"first"}) {
			t.Fatalf("a correctly wired InUnit projection applied %v, so the three refusals above are a check that refuses everything", rows)
		}
		if row := stand.row(t, "orders"); row.Advance != 1 {
			t.Fatalf("the advance inside a committed unit is %d", row.Advance)
		}
	})
}

func refusedOnTheFirstPass(t *testing.T, unit func(*stand, context.Context, func(context.Context) error) error, destination any) {
	t.Helper()
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "first")

	spec := stand.spec("orders", applies(stand.model))
	spec.Advance = projection.InUnit
	spec.Destination = destination
	spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
		return unit(stand, ctx, work)
	}
	running(t, newProjection(t, spec))

	halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
		return state.Phase == projection.PhaseHalted
	})
	if !errors.Is(halted.Err, projection.ErrHalted) || !errors.Is(halted.Err, projection.ErrSpec) {
		t.Fatalf("the pass was refused as %v, where a wiring InUnit cannot honour is a halt over a spec refusal", halted.Err)
	}
	if rows := stand.model.rows(); len(rows) != 0 {
		t.Fatalf("the handler applied %v before the unit was refused, and the check runs before the handler", rows)
	}
	if row := stand.row(t, "orders"); !row.Fresh() {
		t.Fatalf("the checkpoint row is at advance %d after a unit that was refused, so the advance was written outside the unit", row.Advance)
	}
	if saves := stand.points.saves.Load(); saves != 0 {
		t.Fatalf("%d saves were issued for a pass whose unit was refused", saves)
	}
}
