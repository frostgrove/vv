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

// The ownership row, as one table with one row per projection across all of its
// generations. Active is the documented LOCKING read — `FOR SHARE` — so the
// cutover's UPDATE waits behind every unit that read it, which is the obligation
// [[D-126]] leaves to an implementation because no isolation level closes it.
// Activate is one fenced statement and never a read followed by a write.
type liveGenerations struct {
	table     string
	pool      *sql.DB
	source    crud.Source
	locking   bool
	elsewhere bool

	reads atomic.Int64
}

func (this *projectionCase) generations(t *testing.T, name string) *liveGenerations {
	t.Helper()
	held := &liveGenerations{
		table:   quoteIdentifier(this.schema.Name) + "." + quoteIdentifier(name),
		pool:    this.pool,
		source:  this.source,
		locking: true,
	}
	if _, err := this.pool.ExecContext(context.WithoutCancel(t.Context()),
		"CREATE TABLE "+held.table+" (projection text PRIMARY KEY, active bigint NOT NULL)"); err != nil {
		t.Fatalf("the ownership table this case cuts over in could not be created: %v", err)
	}
	return held
}

// The same table reached without the clause, which is what §UC-202 measures
// against: at every isolation level this repository names a plain read of this
// row and a concurrent Activate of it do not conflict, and both commit.
func (this *liveGenerations) unlocked() *liveGenerations {
	return &liveGenerations{table: this.table, pool: this.pool, source: this.source, locking: false}
}

// A second pool, which is the third way to lose the boundary (§UC-192): the
// framework holds a method set and no resource, so it cannot tell that this
// value is answering from outside the transaction the advance rides in.
func (this *liveGenerations) overASecondPool(t *testing.T) *liveGenerations {
	t.Helper()
	pool := checkpointPool(t, 4)
	return &liveGenerations{table: this.table, pool: pool, source: crudsql.Postgres(pool), locking: true, elsewhere: true}
}

func (this *liveGenerations) on(ctx context.Context) parkExecutor {
	if this.elsewhere {
		return this.pool
	}
	if held, found := crud.ExecutorFor(ctx, this.source); found {
		if tx, taken := crudsql.Transaction(held); taken {
			return tx
		}
	}
	return this.pool
}

func (this *liveGenerations) Active(ctx context.Context, name string) (projection.Generation, error) {
	this.reads.Add(1)
	query := "SELECT active FROM " + this.table + " WHERE projection = $1"
	if this.locking {
		query += " FOR SHARE"
	}
	var held int64
	switch err := this.on(ctx).QueryRowContext(ctx, query, name).Scan(&held); {
	case errors.Is(err, sql.ErrNoRows):
		return projection.Ungenerated, nil
	case err != nil:
		return projection.Ungenerated, err
	}
	return projection.Generation(held), nil
}

func (this *liveGenerations) Activate(ctx context.Context, name string, from, to projection.Generation) error {
	statement := "UPDATE " + this.table + " SET active = $3 WHERE projection = $1 AND active = $2"
	if from == projection.Ungenerated {
		statement = "INSERT INTO " + this.table + " (projection, active) VALUES ($1, $3)" +
			" ON CONFLICT (projection) DO UPDATE SET active = $3 WHERE " + this.table + ".active = $2"
	}
	answered, err := this.on(ctx).ExecContext(ctx, statement, name, int64(from), int64(to))
	if err != nil {
		return err
	}
	moved, err := answered.RowsAffected()
	if err != nil {
		return err
	}
	if moved != 1 {
		return fmt.Errorf("%w: the ownership row of %q does not hold %d, so this cutover has nothing to move", event.ErrConflict, name, from)
	}
	return nil
}

func (this *liveGenerations) recorded(t *testing.T, name string) projection.Generation {
	t.Helper()
	held, err := this.Active(context.WithoutCancel(t.Context()), name)
	if err != nil {
		t.Fatalf("the ownership row of %q could not be read: %v", name, err)
	}
	printed, asked := psqlAnswers(t, "SELECT active FROM "+this.table+" WHERE projection = '"+name+"'")
	if asked {
		want := []string(nil)
		if held != projection.Ungenerated {
			want = []string{strconv.FormatInt(int64(held), 10)}
		}
		if !slices.Equal(printed, want) {
			t.Fatalf("psql prints %v for the ownership row of %q where this process read %v", printed, name, want)
		}
	}
	return held
}

// A generation's own spec: its own checkpoint row key, its own destination
// tables, and nothing shared with the generation beside it but the log.
func (this *projectionCase) generational(t *testing.T, name string, generation projection.Generation, into *destination) projection.Spec {
	t.Helper()
	spec := inUnit(this.spec(t, name, into.attributing()), this.source, into.source)
	spec.Generation = generation
	return spec
}

func memberIdentity(t *testing.T, name string, generation projection.Generation, id, mask uint32) projection.Identity {
	t.Helper()
	held, err := projection.NewIdentity(name, generation, aPartition(t, id, mask))
	if err != nil {
		t.Fatalf("the identity of %q at generation %d and partition %d.%d was refused: %v", name, generation, id, mask, err)
	}
	return held
}

func generationalIdentity(t *testing.T, name string, generation projection.Generation) projection.Identity {
	t.Helper()
	held, err := projection.NewIdentity(name, generation, projection.Whole())
	if err != nil {
		t.Fatalf("the identity of %q at generation %d was refused: %v", name, generation, err)
	}
	return held
}

func cuttingOver(t *testing.T, held *projectionCase, spec projection.CutoverSpec) error {
	t.Helper()
	spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
		return crud.InNewTx(ctx, held.source, work)
	}
	return projection.Cutover(context.WithoutCancel(t.Context()), spec)
}

func observedBarrier(t *testing.T, checkpoints event.Checkpoints, of projection.Identity, over projection.Cover) projection.Barrier {
	t.Helper()
	held, err := projection.Observe(context.WithoutCancel(t.Context()), checkpoints, of, over)
	if err != nil {
		t.Fatalf("the barrier of %q was refused: %v", of, err)
	}
	return held
}

// A generation stopped mid-replay, and the stop is inside the handler: the first
// page commits and the second is held open, so for as long as the hold lasts the
// database holds a checkpoint row this generation really recorded and that row is
// below the barrier. It is DRIVEN rather than awaited because a drain of two
// dozen envelopes is over before a readiness can be asked of it, and an arm that
// waited for the state it measures would be measuring the scheduler.
type heldPage struct {
	pages     atomic.Int64
	reached   chan struct{}
	release   chan struct{}
	once      sync.Once
	releasing sync.Once
}

func holdingTheSecondPage() *heldPage {
	return &heldPage{reached: make(chan struct{}), release: make(chan struct{})}
}

func (this *heldPage) arrive(ctx context.Context) {
	if this.pages.Add(1) < 2 {
		return
	}
	this.once.Do(func() {
		close(this.reached)
		select {
		case <-this.release:
		case <-ctx.Done():
		case <-time.After(30 * time.Second):
		}
	})
}

func (this *heldPage) let() { this.releasing.Do(func() { close(this.release) }) }

// The attributing handler with the hold in front of it, so the page the second
// unit holds open has written nothing into the read model.
func (this *destination) attributingWhile(hold *heldPage) projection.Handler {
	applying := this.attributing()
	return projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
		hold.arrive(ctx)
		return applying.Apply(ctx, batch)
	})
}

// §6.9, §UC-159, §UC-161, §UC-164. The barrier is observed from the retiring
// generation's own rows, the arriving one is measured against it — below the
// barrier and then at it — the read target moves once and fenced, and the
// rollback is the same call with From and To exchanged. Every number below is
// read out of the database.
//
// The page size is eight over a log of twenty-four because the arm in the middle
// needs the arriving generation to commit a row and then stand still under the
// barrier, which is a state a single-page drain never passes through.
func TestTheBarrierTheCutoverAndTheRollback(t *testing.T) {
	held := newProjectionCase(t, 12, 8)
	first := held.destination(t, "barrier_g1")
	second := held.destination(t, "barrier_g2")
	rows := held.generations(t, "barrier_generations")
	whole := coverOf(t, projection.Whole())

	writePartitioned(t, held, "barrier", 12)
	live := held.run(t, held.generational(t, "barrier", 1, first))
	live.following(t, "generation 1 drained the log")
	if err := rows.Activate(context.WithoutCancel(t.Context()), "barrier", projection.Ungenerated, 1); err != nil {
		t.Fatalf("standing the ownership row up at generation 1 answered %v", err)
	}

	one := generationalIdentity(t, "barrier", 1)
	two := generationalIdentity(t, "barrier", 2)
	barrier := observedBarrier(t, held.checkpoints(t), one, whole)
	row := storedCheckpoint(t, held.schema, one.String())
	if int64(barrier.At) != row.highest {
		t.Fatalf("the barrier is at %d where generation 1's own row records a watermark of %d, and a barrier is that row's number", barrier.At, row.highest)
	}
	if barrier.At == 0 {
		t.Fatal("the barrier is at the origin over a generation that drained a log, so every arriving generation clears it and this case measures nothing")
	}

	// Not reached yet: generation 2 has not started, so it holds no row and the
	// cutover is refused on the rows rather than on trust.
	if err := cuttingOver(t, held, projection.CutoverSpec{
		Checkpoints: held.checkpoints(t), Generations: rows, Projection: "barrier",
		From: 1, To: 2, Retiring: whole, Arriving: whole,
	}); !errors.Is(err, projection.ErrRetired) {
		t.Fatalf("the cutover to a generation with no rows answered %v, where a generation nothing recorded for has nothing behind it", err)
	}
	if got := rows.recorded(t, "barrier"); got != 1 {
		t.Fatalf("the refused cutover left the ownership row at %d", got)
	}

	// §UC-159's Then, off the live rows: a generation that is running, holds a row
	// of its own and stands below the barrier answers Reached: false with the
	// distance, and a cutover onto it is refused rather than moving every read
	// backwards to where it stands.
	hold := holdingTheSecondPage()
	rebuilding := held.generational(t, "barrier", 2, second)
	rebuilding.Handler = second.attributingWhile(hold)
	arriving := held.run(t, rebuilding)
	t.Cleanup(hold.let)
	await(t, hold.reached, "generation 2 held its second page open under the barrier")

	short := storedCheckpoint(t, held.schema, two.String())
	if short.highest == 0 || event.Position(short.highest) >= barrier.At {
		t.Fatalf("generation 2's own row records a watermark of %d against a barrier at %d, and this arm needs a generation that recorded something and is still short of it", short.highest, barrier.At)
	}
	behind, err := projection.Reached(context.WithoutCancel(t.Context()), held.checkpoints(t), nil, barrier, two, whole)
	if err != nil {
		t.Fatalf("the readiness of generation 2 under the barrier was refused: %v", err)
	}
	if behind.Reached || behind.Behind != barrier.At-event.Position(short.highest) {
		t.Fatalf("generation 2 stands at %d under the barrier at %d and its readiness answers %+v, where a generation short of the barrier answers Reached: false and how far it still is", short.highest, barrier.At, behind)
	}
	if err := cuttingOver(t, held, projection.CutoverSpec{
		Checkpoints: held.checkpoints(t), Generations: rows, Projection: "barrier",
		From: 1, To: 2, Retiring: whole, Arriving: whole,
	}); !errors.Is(err, projection.ErrTopology) {
		t.Fatalf("the cutover from generation 1 to generation 2, which stands at %d under the barrier at %d, answered %v, where the read target is not pointed at a generation that has delivered less than the one it replaces", short.highest, barrier.At, err)
	}
	if got := rows.recorded(t, "barrier"); got != 1 {
		t.Fatalf("the cutover onto a generation under the barrier left the ownership row at %d", got)
	}
	hold.let()

	arriving.following(t, "generation 2 replayed the whole log from the origin")
	waitFor(t, "generation 2 reached the barrier generation 1 set", func() bool {
		readiness, err := projection.Reached(context.WithoutCancel(t.Context()), held.checkpoints(t), nil, barrier, two, whole)
		return err == nil && readiness.Reached
	})
	readiness, err := projection.Reached(context.WithoutCancel(t.Context()), held.checkpoints(t), nil, barrier, two, whole)
	if err != nil {
		t.Fatalf("the readiness of generation 2 was refused: %v", err)
	}
	if readiness.Quarantined != 0 || readiness.Holes != 0 {
		t.Fatalf("generation 2 reports %+v, and a destination with holes is not one a read target is pointed at", readiness)
	}

	if err := cuttingOver(t, held, projection.CutoverSpec{
		Checkpoints: held.checkpoints(t), Generations: rows, Projection: "barrier",
		From: 1, To: 2, Retiring: whole, Arriving: whole,
	}); err != nil {
		t.Fatalf("the cutover from 1 to 2 answered %v", err)
	}
	if got := rows.recorded(t, "barrier"); got != 2 {
		t.Fatalf("the ownership row holds %d after a cutover to 2", got)
	}

	// The rollback is the same call exchanged, and it re-checks the readiness in
	// the other direction rather than trusting that generation 1 is still there.
	if err := cuttingOver(t, held, projection.CutoverSpec{
		Checkpoints: held.checkpoints(t), Generations: rows, Projection: "barrier",
		From: 2, To: 1, Retiring: whole, Arriving: whole,
	}); err != nil {
		t.Fatalf("the rollback from 2 to 1 answered %v", err)
	}
	if got := rows.recorded(t, "barrier"); got != 1 {
		t.Fatalf("the ownership row holds %d after a rollback to 1", got)
	}

	// The control: a rollback to a generation whose rows were dropped is
	// ErrRetired and writes nothing, so what the two moves above rest on is the
	// rows and not the caller's word.
	mustExecute(t, "DELETE FROM "+quoteIdentifier(held.schema.Name)+".checkpoints WHERE projection = $1", one.String())
	live.stop(t)
	if err := cuttingOver(t, held, projection.CutoverSpec{
		Checkpoints: held.checkpoints(t), Generations: rows, Projection: "barrier",
		From: 1, To: 2, Retiring: whole, Arriving: whole,
	}); !errors.Is(err, projection.ErrRetired) {
		t.Fatalf("a cutover away from a generation whose rows are gone answered %v, where a dropped generation and one that never ran read alike", err)
	}
	if got := rows.recorded(t, "barrier"); got != 1 {
		t.Fatalf("the refused cutover left the ownership row at %d", got)
	}
}

// The read path, and the whole of the precondition: one transaction resolves the
// ownership row and then reads every table of the generation it names. What it
// answers is what a reader in ONE snapshot sees.
func resolvedRead(t *testing.T, pool *sql.DB, rows *liveGenerations, name string, table func(generation, suffix string) string) (projection.Generation, []int, error) {
	t.Helper()
	tx, err := pool.BeginTx(context.WithoutCancel(t.Context()), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var active int64
	switch err := tx.QueryRowContext(context.WithoutCancel(t.Context()),
		"SELECT active FROM "+rows.table+" WHERE projection = $1", name).Scan(&active); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return 0, nil, err
	}
	counted := make([]int, 0, 2)
	for _, suffix := range []string{"_a", "_b"} {
		var count int
		if err := tx.QueryRowContext(context.WithoutCancel(t.Context()),
			"SELECT count(*) FROM "+table(strconv.FormatInt(active, 10), suffix)).Scan(&count); err != nil {
			return 0, nil, err
		}
		counted = append(counted, count)
	}
	return projection.Generation(active), counted, nil
}

// §6.9, §UC-161. The switch is atomic for the whole declared set, measured by a
// reader rather than argued from the row count: two tables per generation,
// readers resolving the row and both tables in one snapshot across the commit,
// and not one of them ever sees one generation's table beside the other's.
//
// The negative arm is the precondition made visible: a reader that resolved the
// row BEFORE the commit goes on reading generation 1 afterwards, and that is
// correct until generation 1 is retired.
func TestTheCutoverSwitchesEveryTableAtOnceForAReaderInOneSnapshot(t *testing.T) {
	held := newProjectionCase(t, 16, 0)
	rows := held.generations(t, "atomic_generations")
	generationTable := func(generation, table string) string {
		return quoteIdentifier(held.schema.Name) + "." + quoteIdentifier("atomic_g"+generation+table)
	}
	for _, generation := range []string{"1", "2"} {
		for _, table := range []string{"_a", "_b"} {
			mustExecute(t, "CREATE TABLE "+generationTable(generation, table)+" (id bigserial PRIMARY KEY)")
		}
	}
	// Two tables of one generation carry different row counts, so a reader that
	// saw one generation's first table beside the other's second is told by the
	// numbers rather than by a name.
	for _, seeded := range []struct {
		generation, table string
		rows              int
	}{{"1", "_a", 3}, {"1", "_b", 5}, {"2", "_a", 7}, {"2", "_b", 11}} {
		for range seeded.rows {
			mustExecute(t, "INSERT INTO "+generationTable(seeded.generation, seeded.table)+" DEFAULT VALUES")
		}
	}
	if err := rows.Activate(context.WithoutCancel(t.Context()), "atomic", projection.Ungenerated, 1); err != nil {
		t.Fatalf("standing the ownership row up answered %v", err)
	}

	// A reader that resolved the row before the commit, held open across it.
	stale, err := held.pool.BeginTx(context.WithoutCancel(t.Context()), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatalf("the reader this case holds open could not be opened: %v", err)
	}
	defer func() { _ = stale.Rollback() }()
	var resolved int64
	if err := stale.QueryRowContext(context.WithoutCancel(t.Context()),
		"SELECT active FROM "+rows.table+" WHERE projection = $1", "atomic").Scan(&resolved); err != nil {
		t.Fatalf("the held reader could not resolve the ownership row: %v", err)
	}

	done := make(chan struct{})
	var seen sync.Mutex
	observed := map[string]int{}
	var failure atomic.Pointer[string]
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			generation, counted, err := resolvedRead(t, held.pool, rows, "atomic", generationTable)
			if err != nil {
				printed := err.Error()
				failure.Store(&printed)
				return
			}
			want := []int{3, 5}
			if generation == 2 {
				want = []int{7, 11}
			}
			if !slices.Equal(counted, want) {
				printed := fmt.Sprintf("a reader resolving the row at generation %d read %v where that generation's two tables hold %v, so the switch is not atomic for the declared set", generation, counted, want)
				failure.Store(&printed)
				return
			}
			seen.Lock()
			observed[strconv.FormatInt(int64(generation), 10)]++
			seen.Unlock()
		}
	}()

	// The readers get a head start, so what the switch lands in the middle of is a
	// stream of readers rather than an empty one.
	waitFor(t, "a reader resolved the row before the commit", func() bool {
		seen.Lock()
		defer seen.Unlock()
		return observed["1"] > 0
	})

	// The switch itself, as one fenced write of the ownership row.
	mustExecute(t, "UPDATE "+rows.table+" SET active = 2 WHERE projection = $1 AND active = 1", "atomic")
	waitFor(t, "a reader resolved the row after the commit", func() bool {
		seen.Lock()
		defer seen.Unlock()
		return observed["2"] > 0
	})
	close(done)
	if printed := failure.Load(); printed != nil {
		t.Fatal(*printed)
	}
	seen.Lock()
	before, after := observed["1"], observed["2"]
	seen.Unlock()
	if before == 0 {
		t.Fatal("no reader resolved the row before the commit, so the readers above never straddled the switch and the atomicity they measured is one snapshot's")
	}
	t.Logf("%d readers resolved generation 1 and %d resolved generation 2, and none of them saw one generation's table beside the other's", before, after)

	// The negative arm. It is correct, and it is why retirement is a second step
	// an operator orders against its own readers.
	if resolved != 1 {
		t.Fatalf("the held reader resolved generation %d before the commit", resolved)
	}
	var stillFirst int
	if err := stale.QueryRowContext(context.WithoutCancel(t.Context()),
		"SELECT count(*) FROM "+generationTable("1", "_a")).Scan(&stillFirst); err != nil {
		t.Fatalf("the held reader could not read generation 1's table: %v", err)
	}
	if stillFirst != 3 {
		t.Fatalf("the held reader reads %d rows of generation 1's first table where it holds 3", stillFirst)
	}
	var reResolved int64
	if err := stale.QueryRowContext(context.WithoutCancel(t.Context()),
		"SELECT active FROM "+rows.table+" WHERE projection = $1", "atomic").Scan(&reResolved); err != nil {
		t.Fatalf("the held reader could not re-read the ownership row: %v", err)
	}
	if reResolved != 1 {
		t.Fatalf("the reader held open across the commit re-reads the ownership row as %d, and a snapshot that moved under a reader is not one the atomicity sentence is about", reResolved)
	}
}

// §6.10, §UC-162. Two operators cutting over at once, gated so the contention is
// caused: one commits, the other's fenced Activate matches nothing, and the row
// holds 2 exactly once.
func TestTwoOperatorsCuttingOverAtOnce(t *testing.T) {
	held := newProjectionCase(t, 16, 0)
	first := held.destination(t, "contest_g1")
	second := held.destination(t, "contest_g2")
	rows := held.generations(t, "contest_generations")
	whole := coverOf(t, projection.Whole())

	writePartitioned(t, held, "contest", 8)
	live := held.run(t, held.generational(t, "contest", 1, first))
	live.following(t, "generation 1 drained the log")
	arriving := held.run(t, held.generational(t, "contest", 2, second))
	arriving.following(t, "generation 2 replayed the log")
	if err := rows.Activate(context.WithoutCancel(t.Context()), "contest", projection.Ungenerated, 1); err != nil {
		t.Fatalf("standing the ownership row up answered %v", err)
	}
	live.stop(t)
	arriving.stop(t)

	gate := make(chan struct{})
	answers := make([]error, 2)
	var wait sync.WaitGroup
	for index := range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-gate
			answers[index] = cuttingOver(t, held, projection.CutoverSpec{
				Checkpoints: held.checkpoints(t), Generations: rows, Projection: "contest",
				From: 1, To: 2, Retiring: whole, Arriving: whole,
			})
		}()
	}
	close(gate)
	wait.Wait()

	won, refused := 0, 0
	for _, answered := range answers {
		switch {
		case answered == nil:
			won++
		case errors.Is(answered, event.ErrConflict):
			refused++
		default:
			t.Fatalf("a concurrent cutover answered %v, where the two outcomes are a commit and a refusal on the fence", answered)
		}
	}
	if won != 1 || refused != 1 {
		t.Fatalf("two concurrent cutovers left %d winners and %d refusals, where a fenced write leaves one of each", won, refused)
	}
	if got := rows.recorded(t, "contest"); got != 2 {
		t.Fatalf("the ownership row holds %d after two operators cut over to 2", got)
	}

	// The control: the sequential pair. The second is refused because the row no
	// longer holds `from`, which is the same refusal reached without contention.
	if err := cuttingOver(t, held, projection.CutoverSpec{
		Checkpoints: held.checkpoints(t), Generations: rows, Projection: "contest",
		From: 1, To: 2, Retiring: whole, Arriving: whole,
	}); !errors.Is(err, event.ErrConflict) {
		t.Fatalf("a third cutover from a generation the row no longer holds answered %v", err)
	}
}

// §6.19, §UC-180, §UC-163. Three refusals and one admission, and what tells them
// apart is the rows: a cover with a member that holds no row is refused, a
// generation holding a live letter is refused on Holes, and a generation that
// parked and redrove COMPLETELY cuts over with no override — which is the
// ordinary recovery path and must not run through AcceptQuarantined.
func TestACutoverCannotBeHandedABarrier(t *testing.T) {
	held := newProjectionCase(t, 16, 0)
	first := held.destination(t, "evidence_g1")
	second := held.destination(t, "evidence_g2")
	queue := held.park(t, "evidence_park")
	rows := held.generations(t, "evidence_generations")
	whole := coverOf(t, projection.Whole())
	poison := poisoning("A2")

	writePartitioned(t, held, "evidence", 8)
	live := held.run(t, held.generational(t, "evidence", 1, first))
	live.following(t, "generation 1 drained the log")
	if err := rows.Activate(context.WithoutCancel(t.Context()), "evidence", projection.Ungenerated, 1); err != nil {
		t.Fatalf("standing the ownership row up answered %v", err)
	}

	one := generationalIdentity(t, "evidence", 1)
	barrier := observedBarrier(t, held.checkpoints(t), one, whole)

	// §UC-180: a cover with a member that has no row is refused, naming it, and
	// is not folded into a barrier at the origin.
	quartered := coverOf(t, aPartition(t, 0, 3), aPartition(t, 1, 3), aPartition(t, 2, 3), aPartition(t, 3, 3))
	silent, err := projection.Observe(context.WithoutCancel(t.Context()), held.checkpoints(t), one, quartered)
	if err != nil {
		t.Fatalf("a barrier over a cover no member of which holds a row answered %v, where the origin is the honest reading of no rows at all", err)
	}
	if silent.At != 0 {
		t.Fatalf("a barrier over a cover no member of which holds a row is at %d, where a generation that delivered nothing owes nothing", silent.At)
	}
	// And the origin is not evidence a read target may be moved on, which is the
	// door Cutover closes rather than Observe widening what it reports.
	if err := cuttingOver(t, held, projection.CutoverSpec{
		Checkpoints: held.checkpoints(t), Generations: rows, Projection: "evidence",
		From: 1, To: 2, Retiring: quartered, Arriving: whole,
	}); !errors.Is(err, projection.ErrRetired) {
		t.Fatalf("a cutover declaring a retiring cover the generation does not record at answered %v, where silence is refused rather than folded into a barrier at the origin", err)
	}
	// A cover with SOME members recording and one not is a different refusal, and
	// the two are told apart by the rows: this one names the member with no row.
	partial := coverOf(t, aPartition(t, 0, 1), aPartition(t, 1, 1))
	mustExecute(t, "INSERT INTO "+quoteIdentifier(held.schema.Name)+".checkpoints (projection, cursor, advance, highest, applied, quarantined, updated_at)"+
		" VALUES ($1, '\\x00', 1, 1, 0, 0, now())", memberIdentity(t, "evidence", 1, 0, 1).String())
	if _, err := projection.Observe(context.WithoutCancel(t.Context()), held.checkpoints(t), one, partial); !errors.Is(err, projection.ErrTopology) {
		t.Fatalf("a barrier over a cover one member of which holds no row answered %v, where an absent row read as position zero is a barrier every arriving generation clears", err)
	}
	if got := rows.recorded(t, "evidence"); got != 1 {
		t.Fatalf("a refused cutover left the ownership row at %d", got)
	}

	// Generation 2 with a park: it parks a sequence, so it holds a hole, and the
	// cutover is refused on it rather than on the cumulative count.
	spec := held.parking(t, "evidence", second, queue, poison)
	spec.Generation = 2
	held.write(t, aStream(ordersFamily, "evidence/A"), ordersFamily+".placed", "A1", "A2", "A3")
	arriving := held.run(t, spec)
	arriving.degraded(t, "generation 2 parked the poisoned sequence")

	two := generationalIdentity(t, "evidence", 2)
	waitFor(t, "generation 2 reached the barrier generation 1 set", func() bool {
		readiness, err := projection.Reached(context.WithoutCancel(t.Context()), held.checkpoints(t), queue, barrier, two, whole)
		return err == nil && readiness.Reached
	})
	holed, err := projection.Reached(context.WithoutCancel(t.Context()), held.checkpoints(t), queue, barrier, two, whole)
	if err != nil {
		t.Fatalf("the readiness of a generation holding a letter was refused: %v", err)
	}
	if holed.Holes == 0 {
		t.Fatalf("a generation holding parked letters reports %d holes, so the refusal below would be about nothing", holed.Holes)
	}
	if err := cuttingOver(t, held, projection.CutoverSpec{
		Checkpoints: held.checkpoints(t), Generations: rows, Park: queue, Projection: "evidence",
		From: 1, To: 2, Retiring: whole, Arriving: whole,
	}); err == nil {
		t.Fatal("a cutover onto a generation holding a live letter was admitted, and a read model with a hole in it is not one reads are pointed at")
	}
	if got := rows.recorded(t, "evidence"); got != 1 {
		t.Fatalf("the cutover refused on holes left the ownership row at %d", got)
	}

	// Redriven to completion: Quarantined stands for ever and Holes falls to
	// zero, and the cutover proceeds WITH NO OVERRIDE.
	arriving.stop(t)
	poison.fixed("A2")
	drain := redriving(t, held, two.Whole(), second, queue, poison)
	retried, err := drain.Sequence(context.WithoutCancel(t.Context()), sequenceOf(ordersFamily, "evidence/A"))
	if err != nil {
		t.Fatalf("the redrive of generation 2's queue answered %v", err)
	}
	if retried.Left != 0 {
		t.Fatalf("the redrive left %d letters, so the generation still has a hole and the admission below would prove nothing", retried.Left)
	}
	recovered, err := projection.Reached(context.WithoutCancel(t.Context()), held.checkpoints(t), queue, barrier, two, whole)
	if err != nil {
		t.Fatalf("the readiness of the recovered generation was refused: %v", err)
	}
	if recovered.Holes != 0 {
		t.Fatalf("the recovered generation reports %d holes after a complete redrive", recovered.Holes)
	}
	if recovered.Quarantined == 0 {
		t.Fatal("the recovered generation reports no quarantined events, so the cumulative counter this cutover must NOT be refused on was never raised and the admission below proves nothing")
	}
	if err := cuttingOver(t, held, projection.CutoverSpec{
		Checkpoints: held.checkpoints(t), Generations: rows, Park: queue, Projection: "evidence",
		From: 1, To: 2, Retiring: whole, Arriving: whole,
	}); err != nil {
		t.Fatalf("the cutover onto a generation that parked and redrove completely answered %v, and the ordinary recovery path must not run through an override", err)
	}
	if got := rows.recorded(t, "evidence"); got != 2 {
		t.Fatalf("the ownership row holds %d after the admitted cutover", got)
	}
}

// §UC-160. The retiring generation cannot be TOLD to write into the arriving
// one: the checkpoint rows differ by name, the parks differ by name, and a
// handler resolves its table from Batch.Identity.Generation. What the framework
// does not claim is that it prevents a handler from writing anywhere, and the
// control is what measures that boundary rather than asserting it.
func TestARetiringGenerationCannotBeToldToWriteIntoTheArrivingOne(t *testing.T) {
	held := newProjectionCase(t, 16, 0)
	byGeneration := map[projection.Generation]*destination{
		1: held.destination(t, "byname_g1"),
		2: held.destination(t, "byname_g2"),
	}
	queue := held.park(t, "byname_park")

	writePartitioned(t, held, "byname", 6)
	for _, generation := range []projection.Generation{1, 2} {
		spec := inUnit(held.spec(t, "byname", resolvingByGeneration(byGeneration)), held.source, held.source)
		spec.Generation = generation
		running := held.run(t, spec)
		running.following(t, fmt.Sprintf("generation %d drained the log into its own table", generation))
	}

	for _, generation := range []projection.Generation{1, 2} {
		if got := byGeneration[generation].count(t); got != 12 {
			t.Fatalf("generation %d's table holds %d rows over a log of 12", generation, got)
		}
		name := generationalIdentity(t, "byname", generation).String()
		if _, found := held.row(t, name); !found {
			t.Fatalf("the checkpoint row %q does not exist, so the two generations are not recording through two names", name)
		}
	}
	// The two identities a park is keyed by differ, which is what keeps one
	// generation's letters out of the other's queue (§UC-189).
	for _, generation := range []projection.Generation{1, 2} {
		of := generationalIdentity(t, "byname", generation).Whole()
		counted, err := queue.Sequences(context.WithoutCancel(t.Context()), of)
		if err != nil {
			t.Fatalf("the queue of %q could not be counted: %v", of, err)
		}
		if counted != 0 {
			t.Fatalf("the queue of %q holds %d sequences where neither generation parked anything", of, counted)
		}
	}

	// The control: a handler that ignores the batch it was given writes into the
	// wrong table, asserted — so the guarantee's boundary is measured.
	t.Run("the control: a handler that ignores its batch writes where it was hard-coded", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		wrong := held.destination(t, "ignoring_target")
		writePartitioned(t, held, "ignoring", 4)
		spec := inUnit(held.spec(t, "ignoring", wrong.attributing()), held.source, held.source)
		spec.Generation = 2
		running := held.run(t, spec)
		running.following(t, "the generation whose handler ignores its batch drained the log")
		rows := wrong.rows(t)
		if len(rows) != 8 {
			t.Fatalf("the hard-coded table holds %d rows over a log of 8", len(rows))
		}
		for _, row := range rows {
			if _, identity, _ := strings.Cut(row, "|"); identity != "ignoring@2" {
				t.Fatalf("a row carries %q where the batch names ignoring@2, so the control is not reading what the handler was given", identity)
			}
		}
	})
}

func resolvingByGeneration(tables map[projection.Generation]*destination) projection.Handler {
	return projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
		into, named := tables[batch.Identity.Generation()]
		if !named {
			return fmt.Errorf("this handler was handed the generation %d and holds no table for it", batch.Identity.Generation())
		}
		for _, envelope := range batch.Envelopes {
			if _, err := into.on(ctx).ExecContext(ctx, "INSERT INTO "+into.table+" (payload) VALUES ($1)",
				string(envelope.Payload)+"|"+batch.Identity.String()); err != nil {
				return err
			}
		}
		return nil
	})
}

// §UC-165. A cancelled rebuild resumes from its own row and never from the
// origin: there is no reset, the drain is acknowledged between passes, and the
// row a cancellation leaves is either unchanged or at a real partial position.
func TestACancelledRebuildResumesFromItsRowAndNeverFromTheOrigin(t *testing.T) {
	held := newProjectionCase(t, 12, 4)
	into := held.destination(t, "cancelled_g2")

	writePartitioned(t, held, "cancelled", 24)
	first := held.run(t, held.generational(t, "cancelled", 2, into))
	waitFor(t, "the rebuild applied its first pages", func() bool { return into.count(t) >= 8 })
	first.stop(t)

	name := generationalIdentity(t, "cancelled", 2).String()
	partial, found := held.row(t, name)
	if !found {
		t.Fatalf("the cancelled rebuild %q left no row, so there is nothing for it to resume from", name)
	}
	if partial.highest == 0 {
		t.Fatal("the cancelled rebuild's row stands at the origin, so this case cannot tell a resume from a restart")
	}
	applied := into.count(t)
	if int64(applied) != partial.applied {
		t.Fatalf("the read model holds %d rows where the row accounts for %d applied, and a torn pair is what a drain between passes exists to prevent", applied, partial.applied)
	}

	// The same generation started again: it resumes from the row rather than
	// walking the log a second time into the same table.
	second := held.run(t, held.generational(t, "cancelled", 2, into))
	second.following(t, "the restarted rebuild drained the rest of the log")
	if got := into.count(t); got != 48 {
		t.Fatalf("the restarted rebuild left %d rows over a log of 48, where a resume from the origin would leave %d", got, 48+applied)
	}
	resumed, _ := held.row(t, name)
	if resumed.advance <= partial.advance {
		t.Fatalf("the restarted rebuild stands at advance %d where it stood at %d, so it did not resume", resumed.advance, partial.advance)
	}
}
