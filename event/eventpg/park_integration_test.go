//go:build integration

package eventpg

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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

// The application's own queue, as three tables in the schema the log and the
// checkpoints already live in. That is the whole reason a live one is worth
// writing: the blocking test, the park write, the handler's rows and the advance
// become rows in one PostgreSQL transaction rather than four opinions about what
// is parked, and a rollback either takes all four back or this file says so.
//
// Holds, Park and Evict resolve the ambient transaction and refuse without one,
// because that is where the contract says they run. Sequences resolves the pool,
// because the contract says it is asked before a pass opens a unit and an
// implementation that required one would turn a healthy projection into a
// transaction per pass.
type livePark struct {
	park   string
	skips  string
	claims string
	pool   *sql.DB
	source crud.Source

	maxSequences int
	maxLetters   int

	sequences atomic.Int64
	holds     atomic.Int64
}

func (this *projectionCase) park(t *testing.T, name string) *livePark {
	t.Helper()
	held := &livePark{
		park:   quoteIdentifier(this.schema.Name) + "." + quoteIdentifier(name),
		skips:  quoteIdentifier(this.schema.Name) + "." + quoteIdentifier(name+"_skips"),
		claims: quoteIdentifier(this.schema.Name) + "." + quoteIdentifier(name+"_claims"),
		pool:   this.pool,
		source: this.source,
	}
	for _, statement := range []string{
		"CREATE TABLE " + held.park + " (" +
			"id bigserial PRIMARY KEY, identity text NOT NULL, sequencer text NOT NULL, sequence text NOT NULL, " +
			"family text NOT NULL, stream_key text NOT NULL, version bigint NOT NULL, position bigint NOT NULL, " +
			"wire text NOT NULL, revision integer NOT NULL, payload bytea NOT NULL, cause text NOT NULL, " +
			"attempt integer NOT NULL, tried_at timestamptz NOT NULL DEFAULT now(), " +
			"UNIQUE (identity, sequence, position))",
		"CREATE TABLE " + held.skips + " (id bigserial PRIMARY KEY, identity text NOT NULL, sequence text NOT NULL)",
		"CREATE TABLE " + held.claims + " (identity text NOT NULL, sequence text NOT NULL, token text NOT NULL, " +
			"until timestamptz NOT NULL, PRIMARY KEY (identity, sequence))",
	} {
		if _, err := this.pool.ExecContext(context.WithoutCancel(t.Context()), statement); err != nil {
			t.Fatalf("the queue this case parks into could not be created: %v", err)
		}
	}
	return held
}

func (this *livePark) bounded(sequences, letters int) *livePark {
	this.maxSequences, this.maxLetters = sequences, letters
	return this
}

type parkExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var errParkOutsideAUnit = errors.New("eventpg_test: this park method was called with no transaction of its own source bound, and a queue written beside the read model is two opinions about what is parked")

func (this *livePark) inUnit(ctx context.Context) (parkExecutor, error) {
	held, found := crud.ExecutorFor(ctx, this.source)
	if !found {
		return nil, errParkOutsideAUnit
	}
	tx, taken := crudsql.Transaction(held)
	if !taken {
		return nil, errParkOutsideAUnit
	}
	return tx, nil
}

func (this *livePark) Sequences(ctx context.Context, of projection.Identity) (uint64, error) {
	this.sequences.Add(1)
	var held uint64
	if err := this.pool.QueryRowContext(ctx,
		"SELECT count(DISTINCT sequence) FROM "+this.park+" WHERE identity = $1", of.String()).Scan(&held); err != nil {
		return 0, err
	}
	return held, nil
}

func (this *livePark) Holds(ctx context.Context, of projection.Identity, sequence string) (bool, error) {
	this.holds.Add(1)
	on, err := this.inUnit(ctx)
	if err != nil {
		return false, err
	}
	var held bool
	if err := on.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM "+this.park+" WHERE identity = $1 AND sequence = $2)",
		of.String(), sequence).Scan(&held); err != nil {
		return false, err
	}
	return held, nil
}

// The bound is per sequence and never per queue: a new sequence is refused when
// the queue is at its own limit and an existing one when it holds its own limit
// of letters, which is the whole of §UC-151 as an implementation can carry it.
func (this *livePark) Park(ctx context.Context, letter projection.Letter) error {
	on, err := this.inUnit(ctx)
	if err != nil {
		return err
	}
	var letters, sequences int
	if err := on.QueryRowContext(ctx,
		"SELECT count(*) FILTER (WHERE sequence = $2), count(DISTINCT sequence) FROM "+this.park+" WHERE identity = $1",
		letter.Identity.String(), letter.Sequence).Scan(&letters, &sequences); err != nil {
		return err
	}
	switch {
	case letters == 0 && this.maxSequences > 0 && sequences >= this.maxSequences:
		return fmt.Errorf("%w: %q holds %d sequences and a letter for the new sequence %q needs a %d+1th",
			projection.ErrParkFull, letter.Identity, sequences, letter.Sequence, sequences)
	case this.maxLetters > 0 && letters >= this.maxLetters:
		return fmt.Errorf("%w: the sequence %q of %q holds %d letters, which is its own bound",
			projection.ErrParkFull, letter.Sequence, letter.Identity, letters)
	}
	cause := ""
	if letter.Cause != nil {
		cause = letter.Cause.Error()
	}
	_, err = on.ExecContext(ctx, "INSERT INTO "+this.park+
		" (identity, sequencer, sequence, family, stream_key, version, position, wire, revision, payload, cause, attempt)"+
		" VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)",
		letter.Identity.String(), letter.Sequencer, letter.Sequence,
		letter.Envelope.Stream.Family, string(letter.Envelope.Stream.Key), int64(letter.Envelope.Version),
		int64(letter.Envelope.Position), letter.Envelope.Type, letter.Envelope.Revision,
		letter.Envelope.Payload, cause, letter.Attempt)
	return err
}

func (this *livePark) Holes(ctx context.Context, of projection.Identity) (uint64, error) {
	var held uint64
	if err := this.pool.QueryRowContext(ctx,
		"SELECT (SELECT count(*) FROM "+this.park+" WHERE identity = $1)"+
			" + (SELECT count(*) FROM "+this.skips+" WHERE identity = $1)", of.String()).Scan(&held); err != nil {
		return 0, err
	}
	return held, nil
}

// A claim is a row and not a read, which is what makes it exclusive: the upsert
// takes a sequence only when nobody holds it or the grant it holds has expired,
// so two operators contend in PostgreSQL rather than in this process.
func (this *livePark) Claim(ctx context.Context, of projection.Identity, sequence string) (projection.Claim, bool, error) {
	candidates, err := this.candidates(ctx, of, sequence)
	if err != nil {
		return projection.Claim{}, false, err
	}
	for _, named := range candidates {
		token := make([]byte, 8)
		if _, err := rand.Read(token); err != nil {
			return projection.Claim{}, false, err
		}
		granted := projection.Claim{Of: of, Sequence: named, Token: hex.EncodeToString(token), Until: time.Now().Add(2 * time.Minute)}
		taken, err := this.pool.ExecContext(ctx, "INSERT INTO "+this.claims+" (identity, sequence, token, until)"+
			" VALUES ($1,$2,$3,$4) ON CONFLICT (identity, sequence) DO UPDATE"+
			" SET token = EXCLUDED.token, until = EXCLUDED.until WHERE "+this.claims+".until < now()",
			of.String(), named, granted.Token, granted.Until)
		if err != nil {
			return projection.Claim{}, false, err
		}
		rows, err := taken.RowsAffected()
		if err != nil {
			return projection.Claim{}, false, err
		}
		if rows == 1 {
			return granted, true, nil
		}
	}
	return projection.Claim{}, false, nil
}

// The rotation lives in the table, where the clock does: the least recently
// tried sequence first, and a caller that names one is asking about that one.
func (this *livePark) candidates(ctx context.Context, of projection.Identity, sequence string) ([]string, error) {
	query := "SELECT sequence FROM " + this.park + " WHERE identity = $1 GROUP BY sequence ORDER BY min(tried_at), sequence"
	arguments := []any{of.String()}
	if sequence != "" {
		query = "SELECT sequence FROM " + this.park + " WHERE identity = $1 AND sequence = $2 GROUP BY sequence"
		arguments = append(arguments, sequence)
	}
	rows, err := this.pool.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var found []string
	for rows.Next() {
		var named string
		if err := rows.Scan(&named); err != nil {
			return nil, err
		}
		found = append(found, named)
	}
	return found, rows.Err()
}

func (this *livePark) Sequence(ctx context.Context, claim projection.Claim) ([]projection.Letter, error) {
	if err := this.owns(ctx, this.pool, claim); err != nil {
		return nil, err
	}
	rows, err := this.pool.QueryContext(ctx,
		"SELECT sequencer, sequence, family, stream_key, version, position, wire, revision, payload, cause, attempt"+
			" FROM "+this.park+" WHERE identity = $1 AND sequence = $2 ORDER BY id",
		claim.Of.String(), claim.Sequence)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var held []projection.Letter
	for rows.Next() {
		var letter projection.Letter
		var family, key, wire, cause string
		var version, position int64
		var revision, attempt int
		var payload []byte
		var sequencer, sequence string
		if err := rows.Scan(&sequencer, &sequence, &family, &key, &version, &position, &wire, &revision, &payload, &cause, &attempt); err != nil {
			return nil, err
		}
		letter.Identity, letter.Sequencer, letter.Sequence, letter.Attempt = claim.Of, sequencer, sequence, attempt
		letter.Envelope = event.Envelope{
			Stream:   event.Stream{Family: family, Key: event.Key(key)},
			Version:  event.Version(version),
			Position: event.Position(position),
			Type:     wire,
			Revision: revision,
			Payload:  payload,
		}
		if cause != "" {
			letter.Cause = errors.New(cause)
		}
		held = append(held, letter)
	}
	return held, rows.Err()
}

// Inside the caller's unit, and the token travels on the write rather than only
// on the read: a claim that expired between the load and here has to roll the
// apply back with it.
func (this *livePark) Evict(ctx context.Context, claim projection.Claim, letter projection.Letter) error {
	on, err := this.inUnit(ctx)
	if err != nil {
		return err
	}
	if err := this.owns(ctx, on, claim); err != nil {
		return err
	}
	_, err = on.ExecContext(ctx, "DELETE FROM "+this.park+" WHERE identity = $1 AND sequence = $2 AND position = $3",
		claim.Of.String(), claim.Sequence, int64(letter.Envelope.Position))
	return err
}

func (this *livePark) Touch(ctx context.Context, claim projection.Claim, cause error) error {
	if err := this.owns(ctx, this.pool, claim); err != nil {
		return err
	}
	_, err := this.pool.ExecContext(ctx, "UPDATE "+this.park+
		" SET tried_at = now(), attempt = attempt + 1, cause = CASE WHEN id = "+
		"(SELECT min(id) FROM "+this.park+" WHERE identity = $1 AND sequence = $2) THEN $3 ELSE cause END"+
		" WHERE identity = $1 AND sequence = $2", claim.Of.String(), claim.Sequence, cause.Error())
	return err
}

func (this *livePark) Release(ctx context.Context, claim projection.Claim) error {
	_, err := this.pool.ExecContext(ctx, "DELETE FROM "+this.claims+" WHERE identity = $1 AND sequence = $2 AND token = $3",
		claim.Of.String(), claim.Sequence, claim.Token)
	return err
}

func (this *livePark) owns(ctx context.Context, on parkExecutor, claim projection.Claim) error {
	var held bool
	if err := on.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM "+this.claims+
		" WHERE identity = $1 AND sequence = $2 AND token = $3 AND until > now())",
		claim.Of.String(), claim.Sequence, claim.Token).Scan(&held); err != nil {
		return err
	}
	if !held {
		return fmt.Errorf("%w: the grant over %q of %q is not the one this table holds", projection.ErrClaimLost, claim.Sequence, claim.Of)
	}
	return nil
}

// The operator's own two statements, and they are what tells a block from a
// halt: one DELETE makes room and one DELETE removes a letter nothing will ever
// apply. The second one records the hole, because Holes is what a cutover reads
// and an evicted letter is a hole for ever.
func (this *livePark) clear(t *testing.T, of projection.Identity, sequence string) {
	t.Helper()
	if _, err := this.pool.ExecContext(context.WithoutCancel(t.Context()),
		"DELETE FROM "+this.park+" WHERE identity = $1 AND sequence = $2", of.String(), sequence); err != nil {
		t.Fatalf("the operator's DELETE over %q of %q answered %v", sequence, of, err)
	}
}

func (this *livePark) skip(t *testing.T, of projection.Identity, sequence string) {
	t.Helper()
	this.clear(t, of, sequence)
	if _, err := this.pool.ExecContext(context.WithoutCancel(t.Context()),
		"INSERT INTO "+this.skips+" (identity, sequence) VALUES ($1, $2)", of.String(), sequence); err != nil {
		t.Fatalf("recording the hole %q left in %q answered %v", sequence, of, err)
	}
}

// A letter nothing parked, inserted by hand: it is how a case puts a queue at a
// bound without driving a failure through a whole projection first.
func (this *livePark) seed(t *testing.T, of projection.Identity, sequencer, sequence string, position int64) {
	t.Helper()
	if _, err := this.pool.ExecContext(context.WithoutCancel(t.Context()),
		"INSERT INTO "+this.park+" (identity, sequencer, sequence, family, stream_key, version, position, wire, revision, payload, cause, attempt)"+
			" VALUES ($1,$2,$3,'seeded','seeded',1,$4,'seeded',1,'\\x00','seeded',0)",
		of.String(), sequencer, sequence, position); err != nil {
		t.Fatalf("seeding %q of %q answered %v", sequence, of, err)
	}
}

// What the table holds, read on the pool and cross-checked against psql, so what
// a case asserts about a queue is not this process's account of a query it also
// issued.
func (this *livePark) letters(t *testing.T, of projection.Identity) []string {
	t.Helper()
	held := this.query(t, "SELECT sequence || ':' || position || ':' || coalesce(nullif(cause, ''), '-') FROM "+
		this.park+" WHERE identity = '"+of.String()+"' ORDER BY id")
	printed, asked := psqlAnswers(t, "SELECT sequence || ':' || position || ':' || coalesce(nullif(cause, ''), '-') FROM "+
		this.park+" WHERE identity = '"+of.String()+"' ORDER BY id")
	if asked && !slices.Equal(printed, held) {
		t.Fatalf("psql prints %v for the queue of %q where this process read %v, so one of the two is not reading the database", printed, of, held)
	}
	return held
}

func (this *livePark) attempts(t *testing.T, of projection.Identity) []int {
	t.Helper()
	held := []int{}
	for _, printed := range this.query(t, "SELECT attempt::text FROM "+this.park+" WHERE identity = '"+of.String()+"' ORDER BY id") {
		attempt, err := strconv.Atoi(printed)
		if err != nil {
			t.Fatalf("the queue records an attempt of %q, which is not a number: %v", printed, err)
		}
		held = append(held, attempt)
	}
	return held
}

func (this *livePark) query(t *testing.T, statement string) []string {
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
			t.Fatalf("a row of %.80q could not be read: %v", statement, err)
		}
		held = append(held, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%.80q could not be read to its end: %v", statement, err)
	}
	return held
}

// The one thing a handler in this file decides: which payload is poison. It is a
// value rather than a closure over a counter, so the same handler applied to a
// redrive after the cause is fixed is the same code with one map entry removed.
type poisonous struct {
	mutex   sync.Mutex
	refuses map[string]bool
	seen    []string
}

func poisoning(payloads ...string) *poisonous {
	held := &poisonous{refuses: map[string]bool{}}
	for _, payload := range payloads {
		held.refuses[payload] = true
	}
	return held
}

func (this *poisonous) fixed(payload string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	delete(this.refuses, payload)
}

func (this *poisonous) refused(payload string) bool {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.seen = append(this.seen, payload)
	return this.refuses[payload]
}

func (this *poisonous) reached() []string {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return slices.Clone(this.seen)
}

var errPoison = errors.New("eventpg_test: this payload cannot be applied and trying again will not change that")

// Writes every envelope of the batch that is not poison and refuses the whole
// batch at the first one that is, which is what a handler over a read model does:
// the unit rolls back, so the rows written before the refusal are not there
// either, and the isolation pass re-offers the page one envelope at a time.
func (this *destination) applying(poison *poisonous) projection.Handler {
	return projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
		for _, envelope := range batch.Envelopes {
			if poison.refused(string(envelope.Payload)) {
				return fmt.Errorf("%w: %s", errPoison, envelope.Payload)
			}
			if _, err := this.on(ctx).ExecContext(ctx, "INSERT INTO "+this.table+" (payload) VALUES ($1)", string(envelope.Payload)); err != nil {
				return err
			}
		}
		return nil
	})
}

func permanentlyPoisoned(err error) projection.Verdict {
	if errors.Is(err, errPoison) {
		return projection.Permanent
	}
	return projection.Classify(err)
}

// The tier ParkSequence constructs at, spelled once: InUnit over the source the
// checkpoints and the queue both live in, a resolvable Destination, and the
// classifier that makes the poison permanent.
func (this *projectionCase) parking(t *testing.T, name string, into *destination, queue *livePark, poison *poisonous) projection.Spec {
	t.Helper()
	spec := inUnit(this.spec(t, name, into.applying(poison)), this.source, into.source)
	spec.OnPermanentFailure = projection.ParkSequence
	spec.Park = queue
	spec.Classifier = permanentlyPoisoned
	spec.Attempts = 1
	return spec
}

// §6.5, §UC-146, §UC-147, §INV-091. The page runs A1 A2 B1 A3 B2 with A2
// poisoned: A1 lands, A2 is parked with its cause, A3 is parked WITHOUT EVER
// REACHING THE HANDLER, and B keeps flowing. Then the one commit — a unit that
// rolls back after the work succeeded leaves neither the letter nor the advance,
// which is the only way to show the two moved together rather than in sequence.
func TestTheBlockingTestAndTheAdvanceAreOneCommit(t *testing.T) {
	held := newProjectionCase(t, 8, 0)
	into := held.destination(t, "blocking_read_model")
	queue := held.park(t, "blocking_park")
	poison := poisoning("A2")

	for _, payload := range []struct{ key, payload string }{
		{"A", "A1"}, {"A", "A2"}, {"B", "B1"}, {"A", "A3"}, {"B", "B2"},
	} {
		held.write(t, aStream(ordersFamily, "blocking/"+payload.key), ordersFamily+".placed", payload.payload)
	}

	// The rollback arm first, because it has to run against a queue and a
	// checkpoint that are both still where the drain would leave them.
	rolling := &rollback{}
	spec := held.parking(t, "blocking", into, queue, poison)
	spec.Unit = rolling.around(held.source)
	rolling.arm()
	running := held.run(t, spec)
	waitFor(t, "the pass whose unit rolled back after the park was written completed", rolling.fired)

	of := identityOf(t, "blocking")
	if letters := queue.letters(t, of); len(letters) != 0 {
		t.Fatalf("the unit that rolled back left %v in the queue, so the park write is not in the transaction the advance rides in", letters)
	}
	if row, found := held.row(t, "blocking"); found {
		t.Fatalf("the unit that rolled back left a checkpoint row at advance %d, so the advance is not in the transaction the park write rides in", row.advance)
	}
	if rows := into.rows(t); len(rows) != 0 {
		t.Fatalf("the unit that rolled back left %v in the read model", rows)
	}

	// The same pass, committing. Every assertion below is read out of the
	// database rather than off the projection's account of itself.
	rolling.disarm()
	running.degraded(t, "the projection read the whole log and holds a parked sequence")

	if got, want := into.rows(t), []string{"A1", "B1", "B2"}; !slices.Equal(got, want) {
		t.Fatalf("the read model holds %v where a parked A leaves %v: an envelope behind a blocker reached the handler", got, want)
	}
	parked := sequenceOf(ordersFamily, "blocking/A")
	letters := queue.letters(t, of)
	if len(letters) != 2 {
		t.Fatalf("the queue holds %v where the failing envelope and the one behind it are two letters", letters)
	}
	if !strings.HasPrefix(letters[0], parked+":") || !strings.Contains(letters[0], errPoison.Error()) {
		t.Fatalf("the first letter is %q and it carries no cause under %q, so an operator cannot read why the sequence stopped", letters[0], parked)
	}
	if !strings.HasSuffix(letters[1], ":-") {
		t.Fatalf("the second letter is %q and it carries a cause, where a letter parked behind a blocker never reached a handler and has none", letters[1])
	}
	if slices.Contains(poison.reached(), "A3") {
		t.Fatal("A3 reached the handler, and an envelope behind a parked one reaching a handler is the read-model corruption ES-03 exists to prevent")
	}

	row := storedCheckpoint(t, held.schema, "blocking")
	if row.quarantined != 2 {
		t.Fatalf("the checkpoint records %d quarantined where the failing envelope and the one behind it are two", row.quarantined)
	}
	if row.applied != 3 {
		t.Fatalf("the checkpoint records %d applied where A1, B1 and B2 are three", row.applied)
	}
	waitFor(t, "the live parked count reached the projection's own state", func() bool {
		return running.held.State().Parked == 1
	})

	// §UC-147, the cross-page half: a later page meets a sequence that is
	// already parked, and the envelope of it never reaches a handler.
	held.write(t, aStream(ordersFamily, "blocking/A"), ordersFamily+".placed", "A4")
	held.write(t, aStream(ordersFamily, "blocking/C"), ordersFamily+".placed", "C1")
	waitFor(t, "the later page was applied", func() bool { return into.count(t) == 4 })

	if got, want := into.rows(t), []string{"A1", "B1", "B2", "C1"}; !slices.Equal(got, want) {
		t.Fatalf("the later page left %v where A4 is parked behind its sequence and C1 is applied, which is %v", got, want)
	}
	if slices.Contains(poison.reached(), "A4") {
		t.Fatal("A4 reached the handler on a later page, so the blocking test is not made against the queue the earlier page wrote to")
	}
	if got := queue.letters(t, of); len(got) != 3 {
		t.Fatalf("the queue holds %v where A2, A3 and A4 are three", got)
	}
}

// The caller's unit, with one arming: the work runs, succeeds, and the
// transaction is rolled back anyway. It stays armed until it is disarmed rather
// than firing once, because a projection that keeps running would otherwise
// commit the next pass while a case was reading the rows the rolled-back one
// left — and then the case would be measuring the commit it was about to make.
type rollback struct {
	armed  atomic.Bool
	rolled atomic.Bool
}

var errArmedRollback = errors.New("eventpg_test: this unit rolled its own transaction back after the work inside it succeeded")

func (this *rollback) arm() { this.armed.Store(true) }

func (this *rollback) disarm() { this.armed.Store(false) }

func (this *rollback) fired() bool { return this.rolled.Load() }

func (this *rollback) around(source crud.Source) func(context.Context, func(context.Context) error) error {
	return func(ctx context.Context, work func(context.Context) error) error {
		return crud.InNewTx(ctx, source, func(inner context.Context) error {
			if err := work(inner); err != nil {
				return err
			}
			if this.armed.Load() {
				this.rolled.Store(true)
				return errArmedRollback
			}
			return nil
		})
	}
}

func identityOf(t *testing.T, name string) projection.Identity {
	t.Helper()
	held, err := projection.NewIdentity(name, projection.Ungenerated, projection.Whole())
	if err != nil {
		t.Fatalf("the identity of %q was refused: %v", name, err)
	}
	return held
}

// The key ByStream() answers, asked of the sequencer rather than spelled out: it
// is the kernel's own composition of the family and the key, so a case that
// wrote it by hand would be asserting against its own guess at a wire format
// ([[D-125]]).
func sequenceOf(family, key string) string {
	return projection.ByStream().SequenceOf(event.Envelope{Stream: aStream(family, key)})
}

// §6.6, §UC-148. The count of Holds calls over a full drain with an empty queue
// is zero, and Sequences is asked once per resume rather than once per pass.
// After one park both numbers move, and after the queue is drained they come
// back — which is the one clearing rule, measured at both ends.
func TestTheFastPathCostsNothingLive(t *testing.T) {
	held := newProjectionCase(t, 8, 0)
	into := held.destination(t, "fastpath_read_model")
	queue := held.park(t, "fastpath_park")
	poison := poisoning("A2")

	for index := range 12 {
		held.write(t, aStream(ordersFamily, fmt.Sprintf("fastpath/clean-%d", index)), ordersFamily+".placed", fmt.Sprintf("clean-%02d", index))
	}

	running := held.run(t, held.parking(t, "fastpath", into, queue, poison))
	running.following(t, "the projection drained a log nothing is parked out of")

	// Three more pages, each waited for, so what the counts below are read after
	// is several passes and not one.
	for round := range 3 {
		payload := fmt.Sprintf("round-%d", round)
		held.write(t, aStream(ordersFamily, "fastpath/"+payload), ordersFamily+".placed", payload)
		waitFor(t, "the page "+payload+" was applied", func() bool { return slices.Contains(into.read(t), payload) })
	}

	if holds := queue.holds.Load(); holds != 0 {
		t.Fatalf("Holds was called %d times over a drain with an empty queue, and the fast path's whole claim is that a healthy projection pays nothing for the park", holds)
	}
	resumes := queue.sequences.Load()
	if resumes != 1 {
		t.Fatalf("Sequences was called %d times across a resume and four pages, where an empty queue is read once per RESUME and never once per pass", resumes)
	}

	// The other end: one park, and both counts move. The page after it is what
	// makes Holds reachable at all — a blocking test is asked of an envelope, and
	// a drained log offers none.
	held.write(t, aStream(ordersFamily, "fastpath/A"), ordersFamily+".placed", "A1", "A2", "A3")
	running.degraded(t, "the projection parked the poisoned sequence")
	held.write(t, aStream(ordersFamily, "fastpath/E"), ordersFamily+".placed", "E1")
	waitFor(t, "the degraded projection asked the queue about an envelope", func() bool {
		return queue.holds.Load() > 0
	})
	if after := queue.sequences.Load(); after <= resumes {
		t.Fatalf("Sequences stood at %d and is at %d, where a non-zero count is re-read once per pass", resumes, after)
	}

	// And the clearing rule: the queue drains, the next pass reads zero, and
	// Holds stops being called at all.
	queue.clear(t, identityOf(t, "fastpath"), sequenceOf(ordersFamily, "fastpath/A"))
	settled := queue.holds.Load()
	waitFor(t, "the projection read a zero count off the emptied queue", func() bool {
		return running.held.State().Parked == 0
	})
	held.write(t, aStream(ordersFamily, "fastpath/D"), ordersFamily+".placed", "D1")
	waitFor(t, "the page after the queue emptied was applied", func() bool { return slices.Contains(into.read(t), "D1") })
	if after := queue.holds.Load(); after != settled {
		t.Fatalf("Holds was called %d more times after the queue emptied, so the fast path did not come back", after-settled)
	}
}

// §6.7, §UC-150. A queue at its sequence bound refuses the letter, the whole
// pass rolls back, nothing is skipped and the phase is blocked rather than
// halted — and then one DELETE makes room and the very next pass advances, with
// no restart. That last clause is the whole difference between a block and a
// halt.
func TestAFullParkBlocksAndOneDeleteClearsIt(t *testing.T) {
	held := newProjectionCase(t, 8, 0)
	into := held.destination(t, "full_read_model")
	queue := held.park(t, "full_park").bounded(1, 0)
	poison := poisoning("A2")
	of := identityOf(t, "full")
	queue.seed(t, of, "by-stream", "Z", 9_000_000)

	held.write(t, aStream(ordersFamily, "full/A"), ordersFamily+".placed", "A1", "A2", "A3")
	held.write(t, aStream(ordersFamily, "full/B"), ordersFamily+".placed", "B1")

	running := held.run(t, held.parking(t, "full", into, queue, poison))
	waitFor(t, "the projection met a queue with no room for the letter it had to write", func() bool {
		return running.held.State().Phase == projection.PhaseBlocked
	})

	if row, found := held.row(t, "full"); found {
		t.Fatalf("the blocked projection saved a checkpoint at advance %d, and an advance over an envelope nothing parked is a lost event", row.advance)
	}
	if rows := into.rows(t); len(rows) != 0 {
		t.Fatalf("the blocked projection left %v in the read model, where the pass that could not park rolls back everything it did", rows)
	}
	if got := queue.letters(t, of); len(got) != 1 {
		t.Fatalf("the queue holds %v where the pass that was refused parks nothing, including the letters its own earlier envelopes wrote", got)
	}
	if state := running.held.State(); state.Phase == projection.PhaseHalted {
		t.Fatalf("the projection halted with %v, and a halt needs a redeploy to clear a condition one DELETE clears", state.Err)
	}

	// One DELETE, and no restart: the same running value picks it up.
	queue.clear(t, of, "Z")
	waitFor(t, "the very next pass after the DELETE advanced", func() bool {
		_, found := held.row(t, "full")
		return found
	})
	running.degraded(t, "the projection that was blocked is now merely degraded")
	if got, want := into.rows(t), []string{"A1", "B1"}; !slices.Equal(got, want) {
		t.Fatalf("the pass that ran after the DELETE left %v where A2 is parked, A3 is behind it and %v is what is applied", got, want)
	}
	if got := queue.letters(t, of); len(got) != 2 {
		t.Fatalf("the queue holds %v where A2 and its follower are two letters", got)
	}
	if slices.Contains(poison.reached(), "A3") {
		t.Fatal("A3 reached the handler, so an envelope behind a blocker was applied while the queue had no room")
	}
}

// §6.8, §UC-153, §INV-094. A redrive stops at the first letter that fails again
// and touches no checkpoint — the fence is the loop's, so the redrive's evidence
// is the queue and nothing else.
func TestARedriveStopsAtTheRepeatFailureAndSavesNothing(t *testing.T) {
	held := newProjectionCase(t, 8, 0)
	into := held.destination(t, "redrive_read_model")
	queue := held.park(t, "redrive_park")
	poison := poisoning("A2", "A3")
	of := identityOf(t, "redrive")

	held.write(t, aStream(ordersFamily, "redrive/A"), ordersFamily+".placed", "A1", "A2", "A3", "A4")
	saves := &savesCounted{Checkpoints: held.checkpoints(t)}
	spec := held.parking(t, "redrive", into, queue, poison)
	spec.Checkpoints = saves
	running := held.run(t, spec)
	running.degraded(t, "the projection parked A2 and the two letters behind it")

	if got := queue.letters(t, of); len(got) != 3 {
		t.Fatalf("the queue holds %v where A2, A3 and A4 are three letters", got)
	}
	running.stop(t)
	before := storedCheckpoint(t, held.schema, "redrive")
	counted := saves.saves.Load()

	poison.fixed("A2")
	attemptsBefore := queue.attempts(t, of)
	parked := sequenceOf(ordersFamily, "redrive/A")
	drain := redriving(t, held, of, into, queue, poison)
	retried, err := drain.Sequence(context.WithoutCancel(t.Context()), parked)
	if err != nil {
		t.Fatalf("the redrive answered %v, where a letter that fails again is the letter failing and not the redrive", err)
	}
	if retried.Applied != 1 || retried.Left != 2 {
		t.Fatalf("the redrive answered %+v where A2 applies, A3 fails again and A4 is not touched, which is one applied and two left", retried)
	}
	if retried.Cause == nil || !errors.Is(retried.Cause, errPoison) {
		t.Fatalf("the redrive answered a cause of %v, and an operator has nothing to read without the letter's own new failure", retried.Cause)
	}

	if got, want := into.rows(t), []string{"A1", "A2"}; !slices.Equal(got, want) {
		t.Fatalf("the read model holds %v where the redrive applied A2 alone and stopped, which leaves %v", got, want)
	}
	letters := queue.letters(t, of)
	if len(letters) != 2 || !strings.HasPrefix(letters[0], parked+":") {
		t.Fatalf("the queue holds %v where A3 and A4 are what a redrive that stopped at A3 leaves", letters)
	}
	if !strings.Contains(letters[0], errPoison.Error()) {
		t.Fatalf("the requeued letter is %q and carries no new cause, so the rotation orders on a failure nothing recorded", letters[0])
	}
	if attempts := queue.attempts(t, of); attempts[0] <= attemptsBefore[1] {
		t.Fatalf("the requeued letter is at attempt %d where it stood at %d before the redrive, and a letter that failed again has had its attempt raised so the implementation can decide when to stop trying", attempts[0], attemptsBefore[1])
	}

	if saved := saves.saves.Load(); saved != counted {
		t.Fatalf("the checkpoint store took %d saves across the redrive, and a redrive that moves the scan's record has taken the loop's fence", saved-counted)
	}
	after := storedCheckpoint(t, held.schema, "redrive")
	if after.advance != before.advance || after.applied != before.applied || after.quarantined != before.quarantined {
		t.Fatalf("the checkpoint row moved from %+v to %+v across a redrive, and the redrive's evidence is the queue", before, after)
	}
}

// A Checkpoints that counts what a redrive is asserted not to do. It is the
// shipped store with one counter around Save, so what the projection did before
// the redrive is real and what the redrive did is measured.
type savesCounted struct {
	event.Checkpoints
	saves atomic.Int64
}

func (this *savesCounted) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	this.saves.Add(1)
	return this.Checkpoints.Save(ctx, checkpoint)
}

// The operator's half, over the same handler the loop ran: a redrive whose
// handler is a second value would be draining a queue against code the failure
// was never measured on.
func redriving(t *testing.T, held *projectionCase, of projection.Identity, into *destination, queue *livePark, poison *poisonous) *projection.Redrive {
	t.Helper()
	return redrivingWith(t, held, projection.RedriveSpec{
		Identity:    of,
		Handler:     into.applying(poison),
		Sequencer:   projection.ByStream(),
		Park:        queue,
		Destination: into.source,
	})
}

func redrivingWith(t *testing.T, held *projectionCase, spec projection.RedriveSpec) *projection.Redrive {
	t.Helper()
	spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
		return crud.InNewTx(ctx, held.source, work)
	}
	drain, err := projection.NewRedrive(spec)
	if err != nil {
		t.Fatalf("a well-formed redrive was refused: %v", err)
	}
	return drain
}

// §6.17, §UC-191. Two operators draining one queue at the same moment, gated so
// the contention is caused rather than hoped for. What is read at the end is the
// destination's rows: one claim wins each sequence, no letter is applied twice,
// and every sequence's letters land in the order the queue holds them.
func TestTwoOperatorsRedrivingAtOnce(t *testing.T) {
	held := newProjectionCase(t, 12, 0)
	into := held.destination(t, "contended_read_model")
	queue := held.park(t, "contended_park")
	poison := poisoning("A2", "B2", "C2")
	of := identityOf(t, "contended")

	for _, key := range []string{"A", "B", "C"} {
		held.write(t, aStream(ordersFamily, "contended/"+key), ordersFamily+".placed",
			key+"1", key+"2", key+"3", key+"4")
	}
	running := held.run(t, held.parking(t, "contended", into, queue, poison))
	running.degraded(t, "the projection parked all three sequences")
	waitFor(t, "all three sequences reached the queue", func() bool {
		return len(queue.query(t, "SELECT DISTINCT sequence FROM "+queue.park+" WHERE identity = '"+of.String()+"'")) == 3
	})
	running.stop(t)

	for _, payload := range []string{"A2", "B2", "C2"} {
		poison.fixed(payload)
	}
	before := into.count(t)

	gate := make(chan struct{})
	var wait sync.WaitGroup
	answers := make([]projection.Retried, 2)
	failures := make([]error, 2)
	for index := range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			drain := redriving(t, held, of, into, queue, poison)
			<-gate
			for range 4 {
				retried, err := drain.Any(context.WithoutCancel(t.Context()))
				if err != nil {
					failures[index] = err
					return
				}
				answers[index] = retried
			}
		}()
	}
	close(gate)
	wait.Wait()

	for index, err := range failures {
		if err != nil {
			t.Fatalf("operator %d answered %v, where two operators over one queue are told apart by the claim rather than by a failure", index, err)
		}
	}
	rows := into.rows(t)[before:]
	if len(rows) != 9 {
		t.Fatalf("the two operators left %v, where three sequences of three parked letters each drain to nine rows — a letter applied twice or not at all", rows)
	}
	for _, key := range []string{"A", "B", "C"} {
		var held []string
		for _, row := range rows {
			if strings.HasPrefix(row, key) {
				held = append(held, row)
			}
		}
		want := []string{key + "2", key + "3", key + "4"}
		if !slices.Equal(held, want) {
			t.Fatalf("the sequence %s landed as %v where the queue holds it as %v, and a redrive that hands two operators one sequence lands the third letter before the second finished", key, held, want)
		}
	}
	if got := queue.letters(t, of); len(got) != 0 {
		t.Fatalf("the queue still holds %v after both operators drained it", got)
	}
	if claims := queue.query(t, "SELECT sequence FROM "+queue.claims+" WHERE identity = '"+of.String()+"'"); len(claims) != 0 {
		t.Fatalf("the claims table still holds %v, so a grant was not released on every exit path and the next rotation would skip those sequences", claims)
	}

	// The control: one operator alone over the same shape drains identically, so
	// the claim costs the single-operator case nothing.
	t.Run("the control: one operator alone drains the same queue in the same order", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		poison := poisoning("D2")
		into := held.destination(t, "uncontended_read_model")
		queue := held.park(t, "uncontended_park")
		of := identityOf(t, "uncontended")
		held.write(t, aStream(ordersFamily, "uncontended/D"), ordersFamily+".placed", "D1", "D2", "D3", "D4")
		running := held.run(t, held.parking(t, "uncontended", into, queue, poison))
		running.degraded(t, "the control projection parked its sequence")
		running.stop(t)
		poison.fixed("D2")

		drain := redriving(t, held, of, into, queue, poison)
		retried, err := drain.Any(context.WithoutCancel(t.Context()))
		if err != nil {
			t.Fatalf("one operator alone answered %v", err)
		}
		if retried.Applied != 3 || retried.Left != 0 {
			t.Fatalf("one operator alone answered %+v where three parked letters drain to three applied and none left", retried)
		}
		if got, want := into.rows(t), []string{"D1", "D2", "D3", "D4"}; !slices.Equal(got, want) {
			t.Fatalf("one operator alone left %v where the sequence drains to %v", got, want)
		}
	})
}

// §6.15, §UC-179, §INV-090. A park is keyed by Identity.Whole(), so a split does
// not move it: the letters stay reachable under the generation that parked them,
// the child whose mask matches the parked key goes on blocking that sequence
// without ever calling the handler, and the other child applies its own keys.
//
// The half that matters most is the one about ORDER. A park that orphaned its
// letters would let A4..A6 be applied by a child and A2 be applied by a redrive
// afterwards, which is the read-model corruption running the other way — so what
// is asserted at the end is the order the destination holds.
func TestASplitLeavesTheParkedLettersReachable(t *testing.T) {
	held := newProjectionCase(t, 12, 0)
	into := held.destination(t, "parksplit_read_model")
	queue := held.park(t, "parksplit_park")
	poison := poisoning("A2")

	parent := generationalIdentity(t, "parksplit", 2)
	owned := held.owning(t, "parksplit", 2)
	parked := sequenceOf(ordersFamily, "parksplit/A")

	held.write(t, aStream(ordersFamily, "parksplit/A"), ordersFamily+".placed", "A1", "A2", "A3")
	spec := held.parking(t, "parksplit", into, queue, poison)
	spec.Generation = 2
	spec.Generations = owned
	running := held.run(t, spec)
	running.degraded(t, "the whole-key-space parent parked the poisoned sequence")
	running.stop(t)

	if got := queue.letters(t, parent.Whole()); len(got) != 2 {
		t.Fatalf("the queue of %q holds %v where A2 and A3 are two letters", parent.Whole(), got)
	}

	if _, _, err := splitting(t, held, held.checkpoints(t), parent); err != nil {
		t.Fatalf("the split of the parent holding a parked sequence answered %v", err)
	}
	lower, higher := childrenOf(t, parent)
	holder, other := lower, higher
	if !lower.Partition().Matches(parked) {
		holder, other = higher, lower
	}

	// One key in each child's share, so both children have work: the parked one
	// and a key the OTHER child owns.
	elsewhere := ""
	for index := range 40 {
		candidate := fmt.Sprintf("parksplit/other-%d", index)
		if other.Partition().Matches(sequenceOf(ordersFamily, candidate)) {
			elsewhere = candidate
			break
		}
	}
	if elsewhere == "" {
		t.Fatal("no key of forty landed in the child that does not hold the parked sequence, so this case cannot tell the two children apart")
	}
	held.write(t, aStream(ordersFamily, "parksplit/A"), ordersFamily+".placed", "A4", "A5", "A6")
	held.write(t, aStream(ordersFamily, elsewhere), ordersFamily+".placed", "C1", "C2")

	holding := queue.holds.Load()
	for _, child := range []projection.Identity{holder, other} {
		spec := held.parking(t, "parksplit", into, queue, poison)
		spec.Generation, spec.Partition, spec.Generations = 2, child.Partition(), owned
		// BOTH children report degraded, and that is §UC-189's control rather
		// than a surprise: two partitions of one generation share a park
		// deliberately, so the count either of them reads is the generation's.
		held.run(t, spec).degraded(t, fmt.Sprintf("the child %q read the whole of its share over a queue holding a sequence", child))
	}
	waitFor(t, "the child that holds no parked key applied its own events", func() bool {
		return slices.Contains(into.read(t), "C2")
	})

	if queue.holds.Load() <= holding {
		t.Fatalf("no child asked the queue whether a sequence was parked, so the letters were orphaned by the split and the events behind them went straight to a handler")
	}
	for _, payload := range []string{"A4", "A5", "A6"} {
		if slices.Contains(poison.reached(), payload) {
			t.Fatalf("%q reached the handler after the split, and the events behind a parked sequence never reach one", payload)
		}
	}
	if got, want := into.rows(t), []string{"A1", "C1", "C2"}; !slices.Equal(got, want) {
		t.Fatalf("the read model holds %v where %v is what the two children apply with A blocked", got, want)
	}
	letters := queue.letters(t, parent.Whole())
	if len(letters) != 5 {
		t.Fatalf("the queue of %q holds %v where A2..A6 are five letters, so the split orphaned some of them", parent.Whole(), letters)
	}

	// And the order: the redrive drains the sequence from A2, so what the
	// destination ends up holding is the log's order for that key and not a
	// recovery that landed A2 after A4.
	poison.fixed("A2")
	drain := redrivingWith(t, held, projection.RedriveSpec{
		Identity:    parent.Whole(),
		Handler:     into.applying(poison),
		Sequencer:   projection.ByStream(),
		Park:        queue,
		Destination: into.source,
	})
	retried, err := drain.Sequence(context.WithoutCancel(t.Context()), parked)
	if err != nil {
		t.Fatalf("the redrive of the split generation's queue answered %v", err)
	}
	if retried.Applied != 5 || retried.Left != 0 {
		t.Fatalf("the redrive answered %+v where five letters drain to five applied and none left", retried)
	}
	applied := []string{}
	for _, row := range into.rows(t) {
		if strings.HasPrefix(row, "A") {
			applied = append(applied, row)
		}
	}
	if want := []string{"A1", "A2", "A3", "A4", "A5", "A6"}; !slices.Equal(applied, want) {
		t.Fatalf("the key's events landed as %v where the log holds them as %v, and an earlier event applied after a later one is the corruption the letters staying reachable prevents", applied, want)
	}
}

// The control for the case above: the same split with an EMPTY park leaves both
// children paying nothing, so what the case measures is the queue and not the
// split.
func TestASplitOverAnEmptyParkLeavesBothChildrenPayingNothing(t *testing.T) {
	held := newProjectionCase(t, 12, 0)
	into := held.destination(t, "emptysplit_read_model")
	queue := held.park(t, "emptysplit_park")
	poison := poisoning()
	parent := generationalIdentity(t, "emptysplit", 2)
	owned := held.owning(t, "emptysplit", 2)

	writePartitioned(t, held, "emptysplit", 8)
	spec := held.parking(t, "emptysplit", into, queue, poison)
	spec.Generation, spec.Generations = 2, owned
	running := held.run(t, spec)
	running.following(t, "the parent drained the log with nothing parked")
	running.stop(t)

	if _, _, err := splitting(t, held, held.checkpoints(t), parent); err != nil {
		t.Fatalf("the split of a parent over an empty park answered %v", err)
	}
	lower, higher := childrenOf(t, parent)
	holding := queue.holds.Load()
	writePartitioned(t, held, "emptysplit-after", 8)
	for _, child := range []projection.Identity{lower, higher} {
		spec := held.parking(t, "emptysplit", into, queue, poison)
		spec.Generation, spec.Partition, spec.Generations = 2, child.Partition(), owned
		running := held.run(t, spec)
		running.following(t, fmt.Sprintf("the child %q drained its share over an empty park", child))
	}
	waitFor(t, "the two children applied everything written after the split", func() bool {
		return into.count(t) == 32
	})
	if got := queue.holds.Load(); got != holding {
		t.Fatalf("the children asked the queue about %d envelopes over an empty park, and the fast path is what makes a healthy split cost nothing", got-holding)
	}
}
