package projection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/runtime"
)

const defaultEvery = 50 * time.Millisecond

// Of names a projection and a generation at Whole(); Over is the cover its rows
// are recorded at, checked by NewCover. Park is optional and a nil one asks no
// park question — but a projection that parks and is waited on without its queue
// is answered "delivered" where the caller asked "applied", which is what
// Sequence beside Park exists to prevent. Sequence must be the sequencer the
// projection runs and nothing here can check that, which is why WaitOf is the
// spelling the module page gives and this struct is the one a host that assembles
// its runners by hand keeps.
//
// Generations is optional and is what scopes a wait to the generation a read
// resolves to: supplied, Wait reads Active on its first poll and again on the
// poll that would answer Reached, and refuses with ErrGeneration when it is not
// the generation Of names. Nil says the caller knows which generation it is
// reading, which a deployment tool waiting on an arriving one does.
//
// Every defaults to 50ms and the cost is one Sequences count plus one checkpoint
// Load per cover member per poll, per waiting caller — two per member while that
// member has recorded nothing yet, because the census asks an absent row whether
// a split handed it down. Ticks is runtime.Ticks so a test drives the interval
// rather than sleeping.
//
// The deadline is the caller's context. There is no stale-read field: a wait that
// did not reach returns a filled-in Visibility beside a refusal, so serving stale
// data is a branch somebody wrote rather than a flag everybody passes.
type WaitSpec struct {
	Checkpoints event.Checkpoints
	Park        Park
	Sequence    Sequencer
	Generations Generations
	Of          Identity
	Over        Cover
	Until       Mark
	Every       time.Duration
	Ticks       runtime.Ticks
}

// The five the projection's own Spec already declares, taken from it instead of
// restated by the request path: a wrong Sequence reports reached for a parked
// event, a nil Park reports delivered where the caller asked applied, and a cover
// that is not the one the rows are recorded at folds a minimum over the wrong
// set.
//
// It derives them through the same withDefaults New applies rather than through a
// second spelling of two of its clauses, which is what makes a Park dropped
// beside a policy that never writes to it dropped here too — a queue no pass
// writes a letter to is a queue whose Sequences may refuse outside a unit, and
// that refusal falls on the first poll, which is terminal.
//
// It performs no I/O, starts nothing and refuses the zero Cover and a spec that
// names no identity. Until, Every and Ticks are the caller's to fill in; Until is
// the only field a request path has any business writing, and Every and Ticks are
// deliberately not taken from the Spec: a projection's interval is its idle policy
// and a wait's is a request path's latency budget.
func WaitOf(spec Spec, over Cover) (WaitSpec, error) {
	if over.Count() == 0 {
		return WaitSpec{}, fmt.Errorf("%w: over is the zero Cover, which NewCover never answers, and a wait folds its minimum across the set the rows are recorded at", ErrTopology)
	}
	of, named := NewIdentity(spec.Name, spec.Generation, Whole())
	if named != nil {
		return WaitSpec{}, fmt.Errorf("Name and Generation name no identity: %w", named)
	}
	held := withDefaults(spec)
	return WaitSpec{
		Checkpoints: held.Checkpoints,
		Park:        held.Park,
		Sequence:    held.Sequence,
		Generations: held.Generations,
		Of:          of,
		Over:        over,
	}, nil
}

// The position the last event of this commit went in at, read back from the store
// AFTER the caller's transaction committed. Commit carries no position by design
// — inside an uncommitted transaction an envelope's position is unspecified, and
// one store has it already while another answers zero — so the map from a version
// to a position costs a read and this call is where that round trip is, rather
// than hidden inside the wait.
//
// It reads the commit's whole range and not only its last event, because a
// Sequencer is any function of an envelope and a commit of three facts can belong
// to three sequences: a mark that carried the last one's key would ask the park
// about one third of what it is waiting for. One ReadStream for any commit that
// fits a page, and one more per page beyond it.
//
// It is a method and not a free function because the sequence keys must come from
// the sequencer the projection runs, and the spec is where that is. Six refusals:
// an empty commit; a transaction of this store's bound to ctx, which would mint a
// mark for an append that can still roll back; a store that does not show
// commit.Last(), which is ErrUncommitted and tells "not committed yet" from
// "rolled back" no better than a second connection can; a zero position, which is
// a store that assigns at commit read before the commit; a page this store's own
// answer is not honest about — longer than the stream page it publishes, or
// carrying an envelope that is another stream's or not one version above the one
// before it — which is ErrBackend; and a nil Sequence beside a non-nil Park,
// which would mint a mark the park cannot be asked about. A store this call names
// none of is refused beside them, because a door that takes a value it reads
// through refuses the absent one here as everywhere else in this package.
func (this WaitSpec) Committed(ctx context.Context, store event.Store, commit event.Commit) (Mark, error) {
	if commit.Empty() {
		return Mark{}, fmt.Errorf("%w: this commit wrote nothing, so there is no change for a read model to make visible and no version to resolve a position from", ErrSpec)
	}
	if this.Sequence == nil && !absent(this.Park) {
		return Mark{}, fmt.Errorf("%w: Sequence names no sequencer beside a Park, so this mark would carry no key the queue could be asked about and the wait over it would answer delivered to a caller that supplied a queue precisely because it wanted applied", ErrSpec)
	}
	if absent(store) {
		return Mark{}, fmt.Errorf("%w: store names no store, and the position a commit went in at is read back from the one the append rode through", ErrSpec)
	}
	authority, err := store.Transaction(ctx)
	if err != nil {
		return Mark{}, fmt.Errorf("%w: what this context carries for this store is not a transaction, and a mark is resolved after the caller's own transaction has committed: %w", ErrSpec, err)
	}
	if authority.Valid() {
		return Mark{}, fmt.Errorf("%w: a transaction of this store's is bound to this context, and a position read inside the transaction that wrote it is a position for an append that can still roll back — a rolled-back append burns its position and no projection ever delivers it, so a wait on such a mark never reaches", ErrSpec)
	}
	return this.resolved(ctx, store, commit)
}

// The commit's own range and nothing else, page by page from the version before
// its first. The honesty checks inside are the kernel's own and cannot be
// borrowed, because Repo.checkPage is unexported: honest reproduces its three
// arms, and the last envelope's position must not be zero on top of them, which
// is what a store that assigns positions at commit answers for a read taken
// before that commit.
//
// A short page is the end of the stream and no confirming read is issued for it,
// which is the store contract's own rule: the loop stops there and the range that
// was not reached is ErrUncommitted.
func (this WaitSpec) resolved(ctx context.Context, store event.Store, commit event.Commit) (Mark, error) {
	held := Mark{projection: this.Of.Projection()}
	seen := map[string]bool{}
	page := store.Limits().StreamPage
	at := commit.First() - 1
	for at < commit.Last() {
		read, err := store.ReadStream(ctx, commit.Stream(), at)
		if err != nil {
			return Mark{}, err
		}
		if len(read) == 0 {
			break
		}
		if err := honest(read, commit.Stream(), at, page); err != nil {
			return Mark{}, err
		}
		for _, envelope := range read {
			if envelope.Version > commit.Last() {
				break
			}
			at = envelope.Version
			held.at = envelope.Position
			if this.Sequence == nil {
				continue
			}
			if key := this.Sequence.SequenceOf(envelope); !seen[key] {
				seen[key] = true
				held.sequences = append(held.sequences, key)
			}
		}
		if len(read) < page {
			break
		}
	}
	if at < commit.Last() {
		return Mark{}, fmt.Errorf("%w: this commit's own range was read back and the stream ends below the version the commit reports", ErrUncommitted)
	}
	if held.at == 0 {
		return Mark{}, fmt.Errorf("%w: this store answered the commit's last event at position zero, which is what a store that assigns positions at commit answers for a read taken before that commit landed", ErrUncommitted)
	}
	return held, nil
}

// Repo.checkPage's three arms, in this package's vocabulary: no more envelopes
// than the stream page the store published, every envelope the stream that was
// asked for, and every envelope one version above the one before it. Every, and
// not the first — a store answering another stream's row in any other position
// mints a mark at a foreign position carrying a foreign sequence key, and the
// park is then asked about somebody else's event and answers no.
func honest(read []event.Envelope, stream event.Stream, after event.Version, page int) error {
	if len(read) > page {
		return fmt.Errorf("%w: this store answered a page of more envelopes than the stream page it publishes, and a bound a store overruns is a bound it does not keep", event.ErrBackend)
	}
	for offset, envelope := range read {
		if envelope.Stream != stream || envelope.Version != after+event.Version(offset)+1 {
			return fmt.Errorf("%w: this commit's own range was asked for and an envelope of the page this store answered is another stream's or another version's, and a mark minted from it would be a mark for somebody else's event", event.ErrBackend)
		}
	}
	return nil
}

// What the wait saw, filled in on every path that made a poll.
//
// Behind is an upper bound and a hint, for the reason Readiness.Behind already
// carries: a log burns a position for a rolled-back append and for an
// optimistic-concurrency loser, so the distance between two positions is not a
// count of undelivered events.
//
// Moved is whether At changed across this wait's polls, and it is the one field
// that tells a slow projector from a stopped one. It costs nothing — the census
// read the number anyway. With Polls below two it is always false and means
// nothing, because one observation cannot show a change, and a projector between
// two slow passes has not moved either.
//
// Reached is the wait's own verdict and is true on one exit only. A poll whose
// census is at or above the mark and whose ownership row then answers another
// generation is a refusal, not a Reached beside an error.
type Visibility struct {
	Reached     bool
	At          event.Position
	Behind      event.Position
	Moved       bool
	Quarantined uint64
	Parked      bool
	Polls       int
}

// Polls until the named generation has delivered everything at or below the mark,
// on the caller's own goroutine, starting nothing.
//
// Each poll asks the park before the census and asks it every time, because the
// caller's event lives in one partition and that partition can have parked it
// while another lags: a wait that asked only after reaching would burn its
// deadline on a condition it could have named at once. A parked sequence is
// terminal — waiting longer cannot help and a redrive can. Sequences and Holds are
// the only two Park methods a wait calls; Holes is a cutover's question and this
// is not one.
//
// The first poll is immediate. A context that is already done reaches no store and
// answers ctx.Err(), because an expired context is not a poll.
//
// A POLL THAT CANNOT BE MADE IS TERMINAL ON THE FIRST POLL AND IS POLLED THROUGH
// AFTER IT, and the poll number is the whole of the rule. A cover that is not the
// one the rows are recorded at, a Checkpoints pointing elsewhere and a Park that
// refuses outside a unit fail the first poll and every poll after it, so the
// refusal is returned at once rather than after a deadline. A refusal that appears
// later is the deployment moving — a split in progress, a read of a table that
// blinked — and the deadline decides, with that last refusal wrapped into it, so
// errors.Is answers ErrTopology for a caller that asks why.
//
// A deadline answers ErrNotVisible wrapping context.DeadlineExceeded, with the
// last readable poll's Visibility. A cancellation answers ctx.Err() bare and the
// zero Visibility: the caller stopped asking, and a cancelled request stays one.
// When the two rules collide the context wins over the poll number: a poll that
// could not be made while the caller's own context was going done is that budget
// running out rather than the caller asking wrong, so the first poll takes the
// deadline exit or the cancellation exit exactly as the fourth does — and that
// refusal is not wrapped into the deadline either, because it is the budget
// itself: a store reached with a done context answers a store class, and a
// deadline carrying one is indistinguishable from a deadline reached over polls
// that really failed. A poll that
// ANSWERED still answers — a parked sequence and a generation reads do not
// resolve to are conclusions drawn from rows that were read.
//
// Before any of that, the door: the zero Mark, a mark whose projection is not
// spec.Of's, and a barrier-minted mark beside a non-nil Park are all ErrSpec, and
// the second is the one a deployment running two projections over one log reaches
// by assembling values that are each individually right. Of and Over are refused
// there too, on the terms Observe refuses them, because a wait polls the same
// census and a fold over a set nobody checked takes its minimum across a hole.
func Wait(ctx context.Context, spec WaitSpec) (Visibility, error) {
	if err := waitable(spec); err != nil {
		return Visibility{}, err
	}
	if err := ctx.Err(); err != nil {
		return Visibility{}, err
	}
	return waiting(ctx, spec)
}

func waitable(spec WaitSpec) error {
	if spec.Of.Projection() == "" {
		return fmt.Errorf("%w: Of is the zero Identity, which NewIdentity never answers — WaitOf derives one from the projection's own Spec", ErrSpec)
	}
	if !spec.Of.Partition().Whole() {
		return fmt.Errorf("%w: Of names a partition, and a wait is answered over a whole generation — the partition set is Over, and an identity carrying one of its own is a second answer to the question the cover was asked", ErrTopology)
	}
	if spec.Over.Count() == 0 {
		return fmt.Errorf("%w: Over is the zero Cover, which NewCover never answers, and a minimum folded over a set nobody checked is taken across a hole", ErrTopology)
	}
	if spec.Until.Zero() {
		return fmt.Errorf("%w: Until is the zero Mark, and a position is drawn from an identity sequence starting at one — zero is never a number a store produced, so it is exactly a mark nobody minted. WaitSpec.Committed and MarkOf are the two doors", ErrSpec)
	}
	if spec.Until.projection != spec.Of.Projection() {
		return fmt.Errorf("%w: Until was minted from another projection and is being waited on this one, which is another set of rows and another sequencer's keys — the position is global so the census would reach, while the park would be asked under a key no letter of this projection was ever written under. Derive that projection's own WaitSpec and call its Committed", ErrSpec)
	}
	if len(spec.Until.sequences) == 0 && !absent(spec.Park) {
		return fmt.Errorf("%w: Until carries no sequence key and Park names a queue, so the park cannot be asked whether the change this wait is for is held in it — a barrier is folded from checkpoint rows and has no envelope to ask. Reaching such a mark means delivered to a caller that supplied a queue precisely because it wanted applied", ErrSpec)
	}
	return nil
}

// What one poll concluded. A terminal refusal is the wait's own answer — a parked
// sequence, or a generation reads do not resolve to — and a refusal that is not
// terminal is a poll that could not be made, which is terminal on the first poll
// and polled through after it.
type polled struct {
	reached  bool
	refusal  error
	terminal bool
}

type waiter struct {
	spec WaitSpec
	seen Visibility

	// Whether any poll's census has been read yet, which is what keeps Moved
	// false at one observation rather than true against a zero nobody measured.
	read bool
}

func waiting(ctx context.Context, spec WaitSpec) (Visibility, error) {
	held := &waiter{spec: spec}
	every := spec.Every
	if every <= 0 {
		every = defaultEvery
	}
	beats := spec.Ticks
	if beats == nil {
		beats = runtime.SystemTicks
	}
	var ticker runtime.Ticker
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()
	var last error
	for {
		held.seen.Polls++
		answered := held.poll(ctx)
		switch {
		case answered.reached:
			held.seen.Reached = true
			return held.seen, nil
		case answered.terminal:
			return held.seen, answered.refusal
		case answered.refusal != nil && ctx.Err() != nil:
			if !expired(ctx, answered.refusal) {
				last = answered.refusal
			}
			return held.stopped(ctx, last)
		case answered.refusal != nil && held.seen.Polls == 1:
			return held.seen, answered.refusal
		default:
			last = answered.refusal
		}
		if ticker == nil {
			ticker = beats(every)
		}
		select {
		case <-ctx.Done():
			return held.stopped(ctx, last)
		case <-ticker.Ticks():
		}
	}
}

// The order inside one poll is the whole of ES-05. The ownership row is read
// first on the first poll, because a wait scoped to a generation reads do not
// resolve to is asking about a read model this caller is not reading and every
// other answer would be beside the point. Then the park, before the census and on
// every poll. Then the census, whose minimum is the only aggregate a set of
// checkpoint rows has. Then the ownership row again, on the poll that would
// otherwise answer Reached — the read that turns a false success into a refusal
// when a cutover committed while this wait was running.
//
// The second read is not skipped when the first poll is the one that reaches. A
// caught-up deployment reaches on poll 1 every time, so exempting it would leave
// the protection only for the waits that were going to be slow anyway — and the
// window it closes is open there too: the park round trip and the census round
// trip are what a cutover commits inside.
func (this *waiter) poll(ctx context.Context) polled {
	if this.seen.Polls == 1 {
		if answered := this.resolves(ctx); answered.refusal != nil {
			return answered
		}
	}
	parked, err := this.parked(ctx)
	if err != nil {
		return polled{refusal: err}
	}
	if parked {
		this.seen.Parked = true
		return polled{refusal: fmt.Errorf("%w: one of the sequences this mark carries is held in this generation's queue, so the scan passed the change and the read model never received it. Waiting longer cannot help and a redrive can", ErrParked), terminal: true}
	}
	held, err := surveyed(ctx, this.spec.Checkpoints, this.spec.Of, this.spec.Over)
	if err != nil {
		return polled{refusal: err}
	}
	if this.read && held.lowest != this.seen.At {
		this.seen.Moved = true
	}
	this.read = true
	this.seen.At = held.lowest
	this.seen.Quarantined = held.quarantined
	this.seen.Behind = 0
	if held.lowest < this.spec.Until.At() {
		this.seen.Behind = this.spec.Until.At() - held.lowest
		return polled{}
	}
	if answered := this.resolves(ctx); answered.refusal != nil {
		return answered
	}
	return polled{reached: true}
}

// Two reads over the whole wait and never one per poll: a per-poll read buys a
// faster refusal for a case the caller cannot act on any sooner, at one more
// SELECT per interval per waiter on a row every read path of the deployment
// already contends for. Two over the whole wait and not two polls: a wait that
// reaches at once pays both on its first poll. What may not move is the read on
// the reaching poll — that one turns a true statement about a read model the
// caller is no longer reading into a refusal.
func (this *waiter) resolves(ctx context.Context) polled {
	if absent(this.spec.Generations) {
		return polled{}
	}
	active, err := this.spec.Generations.Active(ctx, this.spec.Of.Projection())
	if err != nil {
		return polled{refusal: err}
	}
	if active != this.spec.Of.Generation() {
		return polled{refusal: fmt.Errorf("%w: the ownership row of this projection resolves reads to another generation than the one this wait names, so reaching the mark in it would be a true statement about a read model this caller is not reading. Read Active and wait again on the generation it answers", ErrGeneration), terminal: true}
	}
	return polled{}
}

// Sequences first and Holds only behind a non-zero count, which is what keeps a
// healthy projection paying one round trip and the widened half of Holds's
// contract unreached. The keys are the mark's own, asked in order and stopped at
// the first true answer, so a commit that spans three sequences asks three times
// and a commit that spans one asks once.
func (this *waiter) parked(ctx context.Context) (bool, error) {
	if absent(this.spec.Park) {
		return false, nil
	}
	of := this.spec.Of.Whole()
	count, err := this.spec.Park.Sequences(ctx, of)
	if err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}
	for _, key := range this.spec.Until.sequences {
		held, err := this.spec.Park.Holds(ctx, of, key)
		if err != nil {
			return false, err
		}
		if held {
			return true, nil
		}
	}
	return false, nil
}

// Whether a poll's refusal is the caller's own budget coming back through the
// store rather than a poll that could not be made. A store reached with a done
// context answers what it answers — the census names the member and the read
// door classifies the rest, so an ordinary deadline arrives as ErrTopology over
// event.ErrBackend — and wrapping that into the deadline tells a caller reading
// §5.2's vocabulary that its cover is misconfigured and its database is in
// trouble over a request that was merely too slow. The cause is asked for beside
// the chain because a store that classifies its failure carries the context
// error there and errors.Is never reaches it: a refusal answers false for
// context.DeadlineExceeded by design.
//
// A refusal that does NOT carry the caller's context is evidence and is kept:
// the deployment really was moving, and the deadline wraps it.
func expired(ctx context.Context, refusal error) bool {
	stopped := ctx.Err()
	if stopped == nil {
		return false
	}
	return errors.Is(refusal, stopped) || errors.Is(event.CauseOf(refusal), stopped)
}

func (this *waiter) stopped(ctx context.Context, last error) (Visibility, error) {
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Visibility{}, ctx.Err()
	}
	if last != nil {
		return this.seen, fmt.Errorf("%w: the generation this wait names had not delivered everything at or below its mark when the deadline elapsed, and the last poll it made could not be read at all: %w: %w", ErrNotVisible, context.DeadlineExceeded, last)
	}
	return this.seen, fmt.Errorf("%w: the generation this wait names had not delivered everything at or below its mark when the deadline elapsed, and Visibility carries what the last poll that could be read saw: %w", ErrNotVisible, context.DeadlineExceeded)
}
