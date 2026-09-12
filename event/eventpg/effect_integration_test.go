//go:build integration

package eventpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/projection"
)

// The sink, as a table in the schema the read model and the checkpoints live in:
// a staged job ([[D-118]]) and nothing a rollback cannot take back. It resolves
// the ambient transaction and refuses without one, because Stage is called
// inside the transaction that commits the advance and a row written beside it
// would be the dual write the capability exists to prevent.
type liveEffects struct {
	table  string
	pool   *sql.DB
	source crud.Source

	staged atomic.Int64
}

func (this *projectionCase) effects(t *testing.T, name string) *liveEffects {
	t.Helper()
	held := &liveEffects{
		table:  quoteIdentifier(this.schema.Name) + "." + quoteIdentifier(name),
		pool:   this.pool,
		source: this.source,
	}
	if _, err := this.pool.ExecContext(context.WithoutCancel(t.Context()),
		"CREATE TABLE "+held.table+" (id bigserial PRIMARY KEY, identity text NOT NULL, position bigint NOT NULL,"+
			" payload text NOT NULL, attempt integer NOT NULL)"); err != nil {
		t.Fatalf("the staged-job table this case reads effects out of could not be created: %v", err)
	}
	return held
}

var errStagedOutsideAUnit = errors.New("eventpg_test: Stage was called with no transaction of its own source bound, and an effect staged beside the advance is the dual write the capability exists to prevent")

func (this *liveEffects) Stage(ctx context.Context, effect projection.Effect) error {
	if len(effect.Envelopes) == 0 {
		return errors.New("eventpg_test: Stage was called with an empty slice, and a delivery with nothing to stage does not call Stage at all")
	}
	held, found := crud.ExecutorFor(ctx, this.source)
	if !found {
		return errStagedOutsideAUnit
	}
	tx, taken := crudsql.Transaction(held)
	if !taken {
		return errStagedOutsideAUnit
	}
	for _, envelope := range effect.Envelopes {
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+this.table+" (identity, position, payload, attempt) VALUES ($1,$2,$3,$4)",
			effect.Identity.String(), int64(envelope.Position), string(envelope.Payload), effect.Attempt); err != nil {
			return err
		}
	}
	this.staged.Add(1)
	return nil
}

// What the table holds, in the order it was written, cross-checked against psql.
func (this *liveEffects) rows(t *testing.T) []string {
	t.Helper()
	statement := "SELECT identity || '|' || payload FROM " + this.table + " ORDER BY id"
	held := this.read(t, statement)
	if printed, asked := psqlAnswers(t, statement); asked && !slices.Equal(printed, held) {
		t.Fatalf("psql prints %v for the staged jobs where this process read %v, so one of the two is not reading the database", printed, held)
	}
	return held
}

func (this *liveEffects) payloads(t *testing.T) []string {
	t.Helper()
	return this.read(t, "SELECT payload FROM "+this.table+" ORDER BY id")
}

func (this *liveEffects) read(t *testing.T, statement string) []string {
	t.Helper()
	rows, err := this.pool.QueryContext(context.WithoutCancel(t.Context()), statement)
	if err != nil {
		t.Fatalf("%.80q answered %v", statement, err)
	}
	defer func() { _ = rows.Close() }()
	var held []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("a staged job could not be read: %v", err)
		}
		held = append(held, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%.80q could not be read to its end: %v", statement, err)
	}
	return held
}

// A projection whose live effects are staged, with the barrier and the ownership
// row the gate rests on. Everything else is the tier the capability constructs
// at and nothing else: InUnit over the source all three tables live in.
func (this *projectionCase) staging(t *testing.T, name string, generation projection.Generation, into *destination,
	sink projection.Effects, after event.Position, rows projection.Generations) projection.Spec {
	t.Helper()
	spec := this.generational(t, name, generation, into)
	spec.Effects = sink
	spec.EffectsAfter = after
	spec.Generations = rows
	return spec
}

// The ownership row already holding the generation the spec names, which is what
// a live single-sender deployment has: the row answers this projection's own
// generation and it stages exactly as it would with no row at all. It is
// REQUIRED beside Effects at a generation other than Ungenerated, because that
// is the pair that admits two senders.
func (this *projectionCase) owning(t *testing.T, name string, generation projection.Generation) *liveGenerations {
	t.Helper()
	held := this.generations(t, name+"_generations")
	if err := held.Activate(context.WithoutCancel(t.Context()), name, projection.Ungenerated, generation); err != nil {
		t.Fatalf("standing the ownership row of %q up at generation %d answered %v", name, generation, err)
	}
	return held
}

// The one interleaving the boundary is decided by: a retiring pass held between
// its ownership read and its commit, with the cutover issued across it. The hold
// is inside Stage, which is the first thing that runs after the row is read and
// the last thing before the unit commits.
type heldStage struct {
	*liveEffects
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func holding(sink *liveEffects) *heldStage {
	return &heldStage{liveEffects: sink, reached: make(chan struct{}), release: make(chan struct{})}
}

func (this *heldStage) Stage(ctx context.Context, effect projection.Effect) error {
	if err := this.liveEffects.Stage(ctx, effect); err != nil {
		return err
	}
	this.once.Do(func() {
		close(this.reached)
		select {
		case <-this.release:
		case <-ctx.Done():
		case <-time.After(30 * time.Second):
		}
	})
	return nil
}

func await(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(30 * time.Second):
		t.Fatalf("%s did not happen inside thirty seconds, so the interleaving this case drives never occurred", what)
	}
}

// The whole of §6.11 in one shape, run twice: the retiring pass stages inside a
// transaction it has not committed, the cutover is issued across it, and what
// differs between the two runs is one clause of SQL — `FOR SHARE` — and nothing
// else. It is DRIVEN rather than awaited, because a cutover taken between two
// passes passes by scheduling luck and would be read afterwards as evidence the
// window is closed.
type twoSenders struct {
	held     *projectionCase
	into     *destination
	sink     *liveEffects
	rows     *liveGenerations
	barrier  event.Position
	envelope string
}

func standingTwoSenders(t *testing.T, name string) *twoSenders {
	t.Helper()
	held := newProjectionCase(t, 16, 0)
	into := held.destination(t, name+"_read_model")
	sink := held.effects(t, name+"_staged")
	rows := held.generations(t, name+"_generations")

	writePartitioned(t, held, name, 6)
	positions := storedPositions(t, held.schema)
	barrier := event.Position(positions[len(positions)-1])
	if err := rows.Activate(context.WithoutCancel(t.Context()), name, projection.Ungenerated, 1); err != nil {
		t.Fatalf("standing the ownership row up answered %v", err)
	}

	// Both generations drain the history below the barrier and stage nothing for
	// it, so the first Stage call either of them makes is for the envelope this
	// case writes next.
	for _, generation := range []projection.Generation{1, 2} {
		running := held.run(t, held.staging(t, name, generation, into, sink, barrier, rows))
		running.following(t, fmt.Sprintf("generation %d drained the history below the barrier", generation))
		running.stop(t)
	}
	if got := sink.staged.Load(); got != 0 {
		t.Fatalf("%d effects were staged for history at or below the barrier, so the warm-up did not suppress", got)
	}

	envelope := name + "-live"
	held.write(t, aStream(ordersFamily, name+"/live"), ordersFamily+".placed", envelope)
	return &twoSenders{held: held, into: into, sink: sink, rows: rows, barrier: barrier, envelope: envelope}
}

// §6.11, §UC-173, §UC-202. Under the documented locking read the cutover's
// UPDATE waits behind the unit that read the row, so the generation that staged
// is the generation that owned the row for the whole of its unit and the
// envelope is staged EXACTLY ONCE. Under the plain read the same interleaving
// stages it twice, at every isolation level this repository names.
func TestARetiredGenerationStopsStagingAtTheCutover(t *testing.T) {
	whole := coverOf(t, projection.Whole())

	t.Run("the locking read: the cutover waits and the envelope is staged once", func(t *testing.T) {
		stand := standingTwoSenders(t, "locking")
		gatedSink := holding(stand.sink)

		retiring := stand.held.run(t, stand.held.staging(t, "locking", 1, stand.into, gatedSink, stand.barrier, stand.rows))
		await(t, gatedSink.reached, "the retiring generation read the ownership row, staged inside its unit and stopped short of committing")

		// The arriving generation reads the SAME row while the retiring unit is
		// still open, finds 1, and suppresses. That is the read the cutover is
		// about to be issued across.
		arriving := stand.held.run(t, stand.held.staging(t, "locking", 2, stand.into, stand.sink, stand.barrier, stand.rows))
		waitFor(t, "the arriving generation applied the envelope while the row still named the retiring one", func() bool {
			return stand.into.count(t) >= 14
		})

		cut := make(chan error, 1)
		go func() {
			cut <- cuttingOver(t, stand.held, projection.CutoverSpec{
				Checkpoints: stand.held.checkpoints(t), Generations: stand.rows, Projection: "locking",
				From: 1, To: 2, Retiring: whole, Arriving: whole,
			})
		}()

		select {
		case err := <-cut:
			t.Fatalf("the cutover returned %v while a unit that read the ownership row was still open, so the UPDATE did not wait and the boundary is not the one the module page states", err)
		case <-time.After(500 * time.Millisecond):
		}
		close(gatedSink.release)
		if err := <-cut; err != nil {
			t.Fatalf("the cutover answered %v once the unit that read the row committed", err)
		}
		if got := stand.rows.recorded(t, "locking"); got != 2 {
			t.Fatalf("the ownership row holds %d after the cutover", got)
		}

		retiring.stop(t)
		arriving.stop(t)
		staged := stand.sink.payloads(t)
		if got := slices.Contains(staged, stand.envelope); !got {
			t.Fatalf("the staged jobs are %v and the envelope was staged by neither generation, so this case measured a suppression rather than a boundary", staged)
		}
		if count := countOf(staged, stand.envelope); count != 1 {
			t.Fatalf("%q was staged %d times where the locking read leaves it staged exactly once — the generation that staged is the generation that owned the row for the whole of its unit",
				stand.envelope, count)
		}
	})

	t.Run("the control: the same interleaving over a plain read leaves two senders", func(t *testing.T) {
		stand := standingTwoSenders(t, "plain")
		plain := stand.rows.unlocked()
		gatedSink := holding(stand.sink)

		retiring := stand.held.run(t, stand.held.staging(t, "plain", 1, stand.into, gatedSink, stand.barrier, plain))
		await(t, gatedSink.reached, "the retiring generation read the plain ownership row and staged inside its unit")

		if err := cuttingOver(t, stand.held, projection.CutoverSpec{
			Checkpoints: stand.held.checkpoints(t), Generations: plain, Projection: "plain",
			From: 1, To: 2, Retiring: whole, Arriving: whole,
		}); err != nil {
			t.Fatalf("the cutover over a plain read answered %v, where nothing is holding the row and it commits at once", err)
		}

		arriving := stand.held.run(t, stand.held.staging(t, "plain", 2, stand.into, stand.sink, stand.barrier, plain))
		waitFor(t, "the arriving generation staged the envelope under a row that now names it", func() bool {
			return countOf(stand.sink.payloads(t), stand.envelope) >= 1
		})
		close(gatedSink.release)
		waitFor(t, "the retiring generation committed the effect it staged under a row that no longer names it", func() bool {
			return countOf(stand.sink.payloads(t), stand.envelope) == 2
		})
		retiring.stop(t)
		arriving.stop(t)

		if count := countOf(stand.sink.payloads(t), stand.envelope); count != 2 {
			t.Fatalf("%q was staged %d times where a plain read leaves two senders — and if it is one, the arm above is not measuring the clause it says it is", stand.envelope, count)
		}
	})

	// The no-ownership-row fixture: both WOULD stage, which is what shows the row
	// to be the thing that closes the window rather than something else in the
	// shape above doing it.
	t.Run("the fixture: with no ownership row at all both senders stage the same envelope", func(t *testing.T) {
		held := newProjectionCase(t, 16, 0)
		into := held.destination(t, "ungated_read_model")
		sink := held.effects(t, "ungated_staged")
		writePartitioned(t, held, "ungated", 4)
		positions := storedPositions(t, held.schema)
		barrier := event.Position(positions[len(positions)-1])

		for _, name := range []string{"ungated", "ungated-rebuild"} {
			spec := held.staging(t, name, projection.Ungenerated, into, sink, barrier, nil)
			running := held.run(t, spec)
			running.following(t, fmt.Sprintf("%q drained the history below the barrier", name))
		}
		envelope := "ungated-live"
		held.write(t, aStream(ordersFamily, "ungated/live"), ordersFamily+".placed", envelope)
		waitFor(t, "both senders staged the same envelope", func() bool {
			return countOf(sink.payloads(t), envelope) == 2
		})
	})
}

func countOf(rows []string, payload string) int {
	held := 0
	for _, row := range rows {
		if row == payload {
			held++
		}
	}
	return held
}

// §UC-192. A third way to lose the same boundary, asserted rather than claimed:
// an implementation that opens its own connection answers correctly and out of
// the transaction the advance rides in, and the framework holds a method set and
// no resource, so it cannot tell.
func TestAnOwnershipRowOverASecondPoolLeavesTwoSenders(t *testing.T) {
	whole := coverOf(t, projection.Whole())
	stand := standingTwoSenders(t, "elsewhere")
	elsewhere := stand.rows.overASecondPool(t)
	gatedSink := holding(stand.sink)

	retiring := stand.held.run(t, stand.held.staging(t, "elsewhere", 1, stand.into, gatedSink, stand.barrier, elsewhere))
	await(t, gatedSink.reached, "the retiring generation read the ownership row over its own pool and staged inside the advance's unit")

	if err := cuttingOver(t, stand.held, projection.CutoverSpec{
		Checkpoints: stand.held.checkpoints(t), Generations: elsewhere, Projection: "elsewhere",
		From: 1, To: 2, Retiring: whole, Arriving: whole,
	}); err != nil {
		t.Fatalf("the cutover answered %v, where nothing in the advance's transaction is holding a row another pool read", err)
	}

	arriving := stand.held.run(t, stand.held.staging(t, "elsewhere", 2, stand.into, stand.sink, stand.barrier, elsewhere))
	waitFor(t, "the arriving generation staged the envelope", func() bool {
		return countOf(stand.sink.payloads(t), stand.envelope) >= 1
	})
	close(gatedSink.release)
	waitFor(t, "the retiring generation committed the effect it staged over a row read elsewhere", func() bool {
		return countOf(stand.sink.payloads(t), stand.envelope) == 2
	})
	retiring.stop(t)
	arriving.stop(t)

	if count := countOf(stand.sink.payloads(t), stand.envelope); count != 2 {
		t.Fatalf("%q was staged %d times where a row reached over a second pool leaves two senders", stand.envelope, count)
	}
}

// §6.12, §UC-171. A warm-up killed between M and N resumes suppressed over
// (K, N] — the envelope's side of the comparison is durable, so a restart does
// not fire from the beginning. The §UC-203 arm beside it is what says what this
// proves and what it does not: the same restart with the barrier DROPPED stages
// all of (K, N], because the barrier's own side is a constant the deployment
// holds and nothing compares a restart's value against what it was warmed up
// under.
func TestAnInterruptedWarmUpResumesSuppressed(t *testing.T) {
	held := newProjectionCase(t, 16, 4)
	into := held.destination(t, "warmup_read_model")
	sink := held.effects(t, "warmup_staged")

	const keys = 20
	writePartitioned(t, held, "warmup", keys)
	positions := storedPositions(t, held.schema)
	barrier := event.Position(positions[29])
	past := make([]string, 0, len(positions)-30)
	for index := 30; index < len(positions); index++ {
		past = append(past, payloadAt(t, held, positions[index]))
	}

	kill := newKillPoint(t)
	owned := held.owning(t, "warmup", 2)
	interrupted := held.staging(t, "warmup", 2, into, sink, barrier, owned)
	interrupted.Handler = into.attributingUntil(kill, 12)
	running := held.runOn(t, kill.ctx, kill.cancel, interrupted)
	waitFor(t, "the warm-up was killed between the origin and the barrier", kill.struck)
	_ = running.stopped(t)

	name := generationalIdentity(t, "warmup", 2).String()
	partial, found := held.row(t, name)
	if !found || partial.highest == 0 {
		t.Fatalf("the killed warm-up left no row this case can resume from: %+v", partial)
	}
	if event.Position(partial.highest) >= barrier {
		t.Fatalf("the warm-up was killed at %d, which is at or past the barrier of %d, so there is no (K, N] left for the restart to be suppressed over", partial.highest, barrier)
	}
	if rows := sink.payloads(t); len(rows) != 0 {
		t.Fatalf("the warm-up staged %v below its barrier", rows)
	}

	restarted := held.run(t, held.staging(t, "warmup", 2, into, sink, barrier, owned))
	restarted.following(t, "the restarted warm-up drained the rest of the log")
	staged := sink.payloads(t)
	slices.Sort(staged)
	want := slices.Clone(past)
	slices.Sort(want)
	if !slices.Equal(staged, want) {
		t.Fatalf("the restart staged %v where everything past the barrier is %v, so it did not resume suppressed over the stretch it had already applied", staged, want)
	}

	// The control: the same generation started FRESH agrees on the boundary, so
	// what the restart proves is the resume and not the barrier alone.
	t.Run("the control: the same generation started fresh agrees on the boundary", func(t *testing.T) {
		fresh := held.effects(t, "fresh_staged")
		into := held.destination(t, "fresh_read_model")
		running := held.run(t, held.staging(t, "warmup-fresh", 3, into, fresh, barrier, held.owning(t, "warmup-fresh", 3)))
		running.following(t, "the fresh generation drained the whole log")
		_ = running
		staged := fresh.payloads(t)
		slices.Sort(staged)
		if !slices.Equal(staged, want) {
			t.Fatalf("a fresh generation staged %v where the resumed one staged %v, and the two must agree on the boundary", staged, want)
		}
	})

	// §UC-203, and it is what keeps §6.12 from being read as a durability the
	// barrier does not have.
	t.Run("the arm: the same restart with the barrier dropped stages the whole warm-up below it", func(t *testing.T) {
		dropped := held.effects(t, "dropped_staged")
		into := held.destination(t, "dropped_read_model")
		kill := newKillPoint(t)
		owned := held.owning(t, "warmup-dropped", 4)
		interrupted := held.staging(t, "warmup-dropped", 4, into, dropped, barrier, owned)
		interrupted.Handler = into.attributingUntil(kill, 12)
		running := held.runOn(t, kill.ctx, kill.cancel, interrupted)
		waitFor(t, "the second warm-up was killed below its barrier", kill.struck)
		_ = running.stopped(t)

		partial, _ := held.row(t, generationalIdentity(t, "warmup-dropped", 4).String())
		if event.Position(partial.highest) >= barrier {
			t.Fatalf("the second warm-up was killed at %d, at or past its barrier of %d", partial.highest, barrier)
		}
		if rows := dropped.payloads(t); len(rows) != 0 {
			t.Fatalf("the second warm-up staged %v below its barrier", rows)
		}

		restarted := held.run(t, held.staging(t, "warmup-dropped", 4, into, dropped, 0, owned))
		restarted.following(t, "the restart with no barrier drained the rest of the log")
		staged := dropped.payloads(t)
		if len(staged) <= len(want) {
			t.Fatalf("the restart with the barrier dropped staged %d jobs where everything past the barrier alone is %d, so the half of the barrier that is NOT durable is unmeasured", len(staged), len(want))
		}
		for _, payload := range staged {
			if slices.Contains(want, payload) {
				continue
			}
			// One below the barrier is enough to make the point, and there is one.
			return
		}
		t.Fatal("the restart with the barrier dropped staged nothing from below it, so one side of the comparison behaved as though it were durable")
	})
}

func payloadAt(t *testing.T, held *projectionCase, position int64) string {
	t.Helper()
	var payload []byte
	if err := liveDB(t).QueryRowContext(context.WithoutCancel(t.Context()),
		"SELECT payload FROM "+quoteIdentifier(held.schema.Name)+".events WHERE position = $1", position).Scan(&payload); err != nil {
		t.Fatalf("the payload at position %d could not be read: %v", position, err)
	}
	return string(payload)
}

// The attributing handler with a kill point in it: the process dies between the
// handler's write and the checkpoint once the read model holds `after` rows.
func (this *destination) attributingUntil(kill *killPoint, after int) projection.Handler {
	written := &atomic.Int64{}
	return projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
		for _, envelope := range batch.Envelopes {
			if _, err := this.on(ctx).ExecContext(ctx, "INSERT INTO "+this.table+" (payload) VALUES ($1)",
				string(envelope.Payload)+"|"+batch.Identity.String()); err != nil {
				return err
			}
			if written.Add(1) >= int64(after) {
				kill.fire(ctx)
			}
		}
		return nil
	})
}

// §6.13, §UC-172, §INV-106. A page whose positions straddle the barrier calls
// Stage ONCE with exactly the envelopes past it, and the handler with all of
// them: an effect belongs to an applied envelope and never to a page.
func TestAStraddlingPageStagesExactlyThePastBarrierEnvelopesLive(t *testing.T) {
	held := newProjectionCase(t, 8, 0)
	into := held.destination(t, "straddle_read_model")
	sink := held.effects(t, "straddle_staged")

	for index := range 5 {
		held.write(t, aStream(ordersFamily, fmt.Sprintf("straddle/%d", index)), ordersFamily+".placed", fmt.Sprintf("straddle-%d", index))
	}
	positions := storedPositions(t, held.schema)
	if len(positions) != 5 {
		t.Fatalf("the log holds %d positions where this case writes five", len(positions))
	}
	barrier := event.Position(positions[2])

	running := held.run(t, held.staging(t, "straddle", 2, into, sink, barrier, held.owning(t, "straddle", 2)))
	running.following(t, "the projection read the straddling page")

	if got := into.count(t); got != 5 {
		t.Fatalf("the handler applied %d envelopes of a page of five", got)
	}
	if calls := sink.staged.Load(); calls != 1 {
		t.Fatalf("Stage was called %d times over one straddling page, where an effect is owed for the delivery and not for each envelope", calls)
	}
	if got, want := sink.payloads(t), []string{"straddle-3", "straddle-4"}; !slices.Equal(got, want) {
		t.Fatalf("the straddling page staged %v where exactly the envelopes past the barrier are %v", got, want)
	}

	// The control: a page entirely at or below the barrier does not call Stage at
	// all — not even with an empty slice, which is what makes a suppression free.
	t.Run("the control: a page entirely below the barrier costs no call at all", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		into := held.destination(t, "below_read_model")
		sink := held.effects(t, "below_staged")
		for index := range 4 {
			held.write(t, aStream(ordersFamily, fmt.Sprintf("below/%d", index)), ordersFamily+".placed", fmt.Sprintf("below-%d", index))
		}
		positions := storedPositions(t, held.schema)
		running := held.run(t, held.staging(t, "below", 2, into, sink, event.Position(positions[len(positions)-1]), held.owning(t, "below", 2)))
		running.following(t, "the projection read a page entirely below its barrier")
		if calls := sink.staged.Load(); calls != 0 {
			t.Fatalf("Stage was called %d times over a page with nothing to stage", calls)
		}
	})
}

// §6.18, §UC-183. A stage followed by a lost fence: the loser's whole unit rolls
// back, so the staged row is gone, the advance did not move and the read model's
// rows are gone — and the winner's pass stages the same envelopes exactly once.
// Nothing outside the database happened, because Stage is contracted to make a
// durable write and nothing else.
func TestAStageRolledBackByALostFence(t *testing.T) {
	held := newProjectionCase(t, 16, 1)
	into := held.destination(t, "fence_read_model")
	sink := held.effects(t, "fence_staged")

	const keys = 8
	writePartitioned(t, held, "fence", keys)
	contention := newGate(2)
	overtaken := &atomic.Bool{}
	owned := held.owning(t, "fence", 2)
	// Both are started before either is waited for: the gate holds each
	// instance's first save until both have issued one, so waiting for the first
	// to follow before the second exists would be waiting for a gate nothing can
	// trip.
	instances := make([]*running, 0, 2)
	for range 2 {
		spec := held.staging(t, "fence", 2, into, sink, 0, owned)
		spec.Checkpoints = &gated{Checkpoints: held.checkpoints(t), gate: contention}
		spec.Observer = projection.ObserverFunc(func(state projection.State) {
			if errors.Is(state.Err, projection.ErrOvertaken) {
				overtaken.Store(true)
			}
		})
		instances = append(instances, held.run(t, spec))
	}
	waitFor(t, "the whole log was applied", func() bool { return into.count(t) == 2*keys })
	for _, instance := range instances {
		instance.following(t, "an instance of the contended identity is following rather than halted")
	}

	if !contention.opened() {
		t.Fatal("the two instances never issued a save at the same advance, so nothing here was contended and this case proves nothing")
	}
	if !overtaken.Load() {
		t.Fatal("neither instance ever published ErrOvertaken, so no fence was lost and the rollback below is unattributable")
	}

	staged := sink.payloads(t)
	if len(staged) != 2*keys {
		t.Fatalf("the staged table holds %d jobs over a log of %d, and a loser whose unit rolled back leaves none of its own", len(staged), 2*keys)
	}
	rows := into.rows(t)
	if len(rows) != 2*keys {
		t.Fatalf("the read model holds %d rows over a log of %d, so a loser's writes survived the fence it lost", len(rows), 2*keys)
	}
	for index := range keys {
		for _, payload := range []string{fmt.Sprintf("fence-%d-placed", index), fmt.Sprintf("fence-%d-paid", index)} {
			if count := countOf(staged, payload); count != 1 {
				t.Fatalf("%q was staged %d times, and an effect staged by a pass whose fence was refused is a job nothing accounts for", payload, count)
			}
		}
	}
	row := storedCheckpoint(t, held.schema, generationalIdentity(t, "fence", 2).String())
	if row.applied != int64(2*keys) {
		t.Fatalf("the checkpoint accounts for %d applied over a log of %d, so a loser's advance moved", row.applied, 2*keys)
	}
}

// §UC-178. The staged job, the read model's rows and the advance commit
// together, and a rollback leaves none of the three. The control is the pass
// that commits, so what the emptiness measures is the rollback.
func TestAStagedJobTheReadModelAndTheAdvanceCommitTogether(t *testing.T) {
	held := newProjectionCase(t, 8, 0)
	into := held.destination(t, "together_read_model")
	sink := held.effects(t, "together_staged")

	writePartitioned(t, held, "together", 4)
	rolling := &rollback{}
	spec := held.staging(t, "together", 2, into, sink, 0, held.owning(t, "together", 2))
	spec.Unit = rolling.around(held.source)
	rolling.arm()
	running := held.run(t, spec)
	waitFor(t, "the pass whose unit rolled back after it staged completed", rolling.fired)

	if got := sink.rows(t); len(got) != 0 {
		t.Fatalf("the unit that rolled back left %v staged, and a job the advance does not account for is sent by whatever drains the stage", got)
	}
	if got := into.rows(t); len(got) != 0 {
		t.Fatalf("the unit that rolled back left %v in the read model", got)
	}
	if row, found := held.row(t, generationalIdentity(t, "together", 2).String()); found {
		t.Fatalf("the unit that rolled back left a checkpoint row at advance %d", row.advance)
	}

	rolling.disarm()
	running.following(t, "the pass that committed drained the log")
	if got, want := len(sink.payloads(t)), 8; got != want {
		t.Fatalf("the committing pass staged %d jobs over a log of %d", got, want)
	}
	if got := into.count(t); got != 8 {
		t.Fatalf("the committing pass left %d rows in the read model over a log of 8", got)
	}
	row, found := held.row(t, generationalIdentity(t, "together", 2).String())
	if !found {
		t.Fatal("the committing pass left no checkpoint row")
	}
	if row.applied != 8 {
		t.Fatalf("the checkpoint accounts for %d applied where the read model holds 8 rows and the stage holds 8 jobs", row.applied)
	}
}

// §UC-134, §INV-084. What a foreign destination is promised, and what it is not.
// The four promises are asserted against a handler writing to a second database
// under Unchecked, across a process killed mid-drain; the fifth — that the
// foreign effect happened exactly once — is the one the framework refuses to
// make, and the tier A control is what measures the difference rather than
// arguing it.
func TestAForeignDestinationGetsFourPromisesAndNotTheFifth(t *testing.T) {
	held := newProjectionCase(t, 12, 3)
	elsewhere := held.elsewhere(t, "foreign_read_model")

	const keys = 12
	writePartitioned(t, held, "foreign", keys)

	// The kill lands between the foreign write and the advance, which is the
	// window Unchecked names: the foreign rows commit and the advance does not.
	kill := newKillPoint(t)
	spec := inUnit(held.spec(t, "foreign", elsewhere.identifyingUntil(kill, 7)), held.source, projection.Unchecked)
	running := held.runOn(t, kill.ctx, kill.cancel, spec)
	waitFor(t, "the process was killed between a foreign write and the advance", kill.struck)
	_ = running.stopped(t)

	restarted := held.run(t, inUnit(held.spec(t, "foreign", elsewhere.identifying()), held.source, projection.Unchecked))
	restarted.following(t, "the restarted projection drained the rest of the log")

	delivered := payloadsOnly(elsewhere.rows(t))
	// 1. Every committed event reached the handler at least once.
	for index := range keys {
		for _, payload := range []string{fmt.Sprintf("foreign-%d-placed", index), fmt.Sprintf("foreign-%d-paid", index)} {
			if !slices.Contains(delivered, payload) {
				t.Fatalf("%q reached the handler zero times, and at least once is the one delivery promise this tier does make", payload)
			}
		}
	}
	// 2. The order within each stream is the stream's.
	for index := range keys {
		placed := slices.Index(delivered, fmt.Sprintf("foreign-%d-placed", index))
		paid := slices.Index(delivered, fmt.Sprintf("foreign-%d-paid", index))
		if placed > paid {
			t.Fatalf("the first delivery of foreign-%d-paid came before foreign-%d-placed, and the order within a stream is the stream's at every tier", index, index)
		}
	}
	// 3. The resume point skipped nothing: the row accounts for the whole log.
	row := storedCheckpoint(t, held.schema, "foreign")
	positions := storedPositions(t, held.schema)
	if row.highest != positions[len(positions)-1] {
		t.Fatalf("the checkpoint stands at %d where the log ends at %d, so the resume skipped part of it", row.highest, positions[len(positions)-1])
	}
	// 4. Every delivery carried a (Stream, Version) unique across the whole run.
	identities := map[string]int{}
	for _, row := range elsewhere.identities(t) {
		identities[row]++
	}
	if len(identities) != 2*keys {
		t.Fatalf("the deliveries carried %d distinct (Stream, Version) pairs over a log of %d, and that pair is what makes an upsert the whole of a handler's idempotency", len(identities), 2*keys)
	}
	// And the fifth, which is NOT asserted: the run is deliberately allowed to
	// have delivered a payload twice, and this is the number that says so.
	t.Logf("a foreign destination under Unchecked took %d deliveries over a log of %d committed events", len(delivered), 2*keys)

	// The control: the same run at tier A leaves each payload in exactly one row,
	// so the two tiers are told apart by measurement rather than by prose.
	t.Run("the control: the same kill at tier A leaves each payload in exactly one row", func(t *testing.T) {
		held := newProjectionCase(t, 12, 3)
		into := held.destination(t, "tiera_read_model")
		writePartitioned(t, held, "tiera", keys)
		kill := newKillPoint(t)
		spec := inUnit(held.spec(t, "tiera", into.attributingUntil(kill, 7)), held.source, into.source)
		running := held.runOn(t, kill.ctx, kill.cancel, spec)
		waitFor(t, "the process was killed inside a tier A unit", kill.struck)
		_ = running.stopped(t)

		restarted := held.run(t, inUnit(held.spec(t, "tiera", into.attributing()), held.source, into.source))
		restarted.following(t, "the restarted tier A projection drained the rest of the log")
		rows := payloadsOnly(into.rows(t))
		if len(rows) != 2*keys {
			t.Fatalf("tier A left %d rows over a log of %d, and the advance claimed before the handler is what makes that number exact", len(rows), 2*keys)
		}
	})
}

// The identity the framework hands a handler in place of the deduplication it
// does not do: (Stream, Version) is unique and stable for every event ever
// written, which is what makes an upsert keyed on it the whole of a handler's
// idempotency.
func (this *destination) identifying() projection.Handler { return this.identifyingUntil(nil, 0) }

func (this *destination) identifyingUntil(kill *killPoint, after int) projection.Handler {
	written := &atomic.Int64{}
	return projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
		for _, envelope := range batch.Envelopes {
			// Family and Key, not Stream.String(): the rendering carries the family
			// alone, so every stream of one family would render one identity.
			identity := envelope.Stream.Family + "/" + string(envelope.Stream.Key) + "#" + strconv.FormatInt(int64(envelope.Version), 10)
			if _, err := this.on(ctx).ExecContext(ctx, "INSERT INTO "+this.table+" (payload) VALUES ($1)",
				string(envelope.Payload)+"|"+identity); err != nil {
				return err
			}
			if kill != nil && written.Add(1) >= int64(after) {
				kill.fire(ctx)
			}
		}
		return nil
	})
}

func (this *destination) identities(t *testing.T) []string {
	t.Helper()
	held := []string{}
	for _, row := range this.read(t) {
		_, identity, _ := strings.Cut(row, "|")
		held = append(held, identity)
	}
	return held
}
