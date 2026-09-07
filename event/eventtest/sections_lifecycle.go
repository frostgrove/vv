package eventtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/frostgrove/vv/event"
)

func cancellationSection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	id := this.account("a")
	_, at := this.load(ctx, repo, id)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	expired, stopExpiring := context.WithDeadline(ctx, time.Now().Add(-time.Minute))
	defer stopExpiring()

	for _, window := range []struct {
		what     string
		ctx      context.Context
		sentinel error
	}{
		{"a context the caller cancelled", cancelled, context.Canceled},
		{"a context whose deadline has passed", expired, context.DeadlineExceeded},
	} {
		_, _, err := repo.Append(window.ctx, at, held.credited.New(id, credited{Amount: 1}))
		if !errors.Is(err, window.sentinel) {
			this.refuse("an append before any statement under %s answered %v, which a caller matching a cancellation cannot recognise as one", window.what, err)
		}
		if errors.Is(err, event.ErrUncertain) {
			this.refuse("an append that was never issued under %s answered an uncertainty, and a caller that reads one re-reads the stream and retries over a decision nothing ever wrote", window.what)
		}
		if _, _, err := repo.Load(window.ctx, id); !errors.Is(err, window.sentinel) {
			this.refuse("a load under %s answered %v", window.what, err)
		}
	}

	inside := this.bind(unconfirming{over{store}}, held)
	_, _, err := inside.Append(ctx, at, held.credited.New(id, credited{Amount: 1}))
	if !errors.Is(err, event.ErrUncertain) {
		this.refuse("an append cancelled after it was issued answered %v, and uncertainty outranks a cancellation because reporting an uncertain commit as a cancellation cannot be recovered", err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		this.refuse("an append that was issued and never confirmed matches a cancellation, so the branch every caller writes first — if errors.Is(err, context.Canceled) — fires on the one outcome that must not take it")
	}

	_, live := this.load(ctx, repo, id)
	if _, _, err := repo.Append(ctx, live, held.credited.New(id, credited{Amount: 1})); err != nil {
		this.refuse("the same append on a live context answered %v, so the refusals above are the cancellation's", err)
	}
}

func lifecycleSection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	limits := this.limits
	id := this.account("a")
	_, at := this.load(ctx, repo, id)
	at, _ = this.append(ctx, repo, at, held.credited.New(id, credited{Amount: 1}))

	beside := this.beside(store, held)
	staged := this.stage(ctx, store, repo, held)

	if err := store.Close(); err != nil {
		this.refuse("closing this store answered %v", err)
	}
	if err := store.Close(); err != nil {
		this.refuse("closing this store a second time answered %v, and a composition root that closes twice is an ordinary shutdown", err)
	}

	if _, _, err := repo.Load(ctx, id); !errors.Is(err, event.ErrClosed) {
		this.refuse("loading a legal identity through a closed store answered %v: its first act after the key check is the transaction question, which a closed store answers as an open one would", err)
	}
	if _, _, err := repo.Append(ctx, at, held.credited.New(id, credited{Amount: 1})); !errors.Is(err, event.ErrClosed) {
		this.refuse("appending through a closed store answered %v", err)
	}
	over := accountID{Tenant: this.run, Number: this.mark + strings.Repeat("k", limits.MaxKey)}
	if _, _, err := repo.Load(ctx, over); !errors.Is(err, event.ErrKey) {
		this.refuse("loading an identity that renders no legal key through a closed store answered %v, and the key check is the step before the store is asked anything", err)
	}
	this.closedWithin(ctx, repo)

	if beside == nil {
		this.unable("no second value over this backing read what a close left behind")
		return
	}
	seen := this.bind(beside, held)
	if state, _ := this.load(ctx, seen, id); state.Balance != 1 {
		this.refuse("work committed before the close folds to %d through another value over this backing, so what the assertion below calls invisible is a store that hides everything", state.Balance)
	}
	if staged != (accountID{}) {
		if state, _ := this.load(ctx, seen, staged); state.Balance != 0 {
			this.refuse("a transaction the close left unresolved is readable through another value at a balance of %d, and close neither commits nor rolls back", state.Balance)
		}
	}
}

func (this *probe) beside(store event.Store, held declaration) event.Store {
	if this.capabilities.SharedBacking != event.Supported || this.factory.Sibling == nil {
		return nil
	}
	beside := this.factory.Sibling(this.t, store)
	if beside == nil {
		this.refuse("the factory answered no second store value over this backing")
	}
	return beside
}

func (this *probe) stage(ctx context.Context, store event.Store, repo *event.Repo[ledger, accountID], held declaration) accountID {
	if this.capabilities.Transactions != event.Supported || this.factory.Begin == nil {
		this.unable("no transaction was left unresolved across the close, because this store has no transactions")
		return accountID{}
	}
	id := this.account("staged")
	inside, _ := this.begin(ctx, store)
	_, at := this.load(inside, repo, id)
	this.append(inside, repo, at, held.credited.New(id, credited{Amount: 5}))
	return id
}

func (this *probe) closedWithin(ctx context.Context, repo *event.Repo[ledger, accountID]) {
	if this.capabilities.Transactions != event.Supported {
		return
	}
	if _, err := repo.Within(ctx); !errors.Is(err, event.ErrNoTransaction) {
		this.refuse("asking a closed store to be inside a transaction the context does not carry answered %v, and that question reads and writes nothing that closing changed", err)
	}
}

func refusalClassesSection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	id, other := this.account("a"), this.account("b")
	stream := this.streamOf(held, id)
	_, fresh := this.load(ctx, repo, id)
	at := this.wroteWhatItSaid(ctx, fixture{store: store, repo: repo, held: held, id: id, at: fresh})
	limits := this.limits

	_, _, conflicted := repo.Append(ctx, fresh, held.credited.New(id, credited{Amount: 1}))
	_, _, crossed := repo.Append(ctx, at, held.credited.New(other, credited{Amount: 1}))
	_, _, oversized := repo.Append(ctx, at, held.noted.New(id, noted{Bytes: make([]byte, limits.MaxPayload+1)}))
	_, _, unkeyed := repo.Load(ctx, accountID{Tenant: this.run, Number: strings.Repeat("k", limits.MaxKey)})
	for _, refusal := range []struct {
		doing string
		err   error
		is    error
	}{
		{"an append at a version the stream has left", conflicted, event.ErrConflict},
		{"an append of a change decided for another stream", crossed, event.ErrWrongStream},
		{"an append of a payload over this store's bound", oversized, event.ErrTooLarge},
		{"a load of an identity that renders no legal key", unkeyed, event.ErrKey},
	} {
		this.partitioned(refusal.doing, refusal.err, refusal.is)
	}

	quota := errors.New("eventtest: over the quota this decorator polices")
	before := this.drain(ctx, store, stream)
	for _, policy := range []struct {
		what    string
		refusal error
		is      error
		isNot   error
		carries error
	}{
		{"a decorator that refuses as a matter of its own policy", event.Failure(event.Refused, quota), event.ErrRefused, event.ErrUncertain, quota},
		{"a decorator that returns its own error and classifies nothing", quota, event.ErrUncertain, event.ErrRefused, nil},
		{"a decorator that names one of this vocabulary's own sentinels as its cause", event.Failure(event.Refused, event.ErrConflict), event.ErrRefused, event.ErrConflict, nil},
		{"a decorator whose cause carries both its own error and one of this vocabulary's", event.Failure(event.Refused, fmt.Errorf("%w: %w", quota, event.ErrConflict)), event.ErrRefused, event.ErrConflict, quota},
		{"a decorator that refuses with nothing to add", event.Failure(event.Refused, nil), event.ErrRefused, event.ErrConflict, nil},
	} {
		policed := this.bind(policing{over{store}, policy.refusal}, held)
		_, _, err := policed.Append(ctx, at, held.credited.New(id, credited{Amount: 1}))
		if !errors.Is(err, policy.is) {
			this.refuse("%s answered %v where the kernel answers %v", policy.what, err, policy.is)
		}
		if errors.Is(err, policy.isNot) {
			this.refuse("%s answered a refusal that also matches %v, and a caller branching on that sentinel takes the recovery for an outcome that never happened", policy.what, policy.isNot)
		}
		if err.Error() != policy.is.Error() {
			this.refuse("%s renders as %q rather than as its own sentinel, and a decorator's text — like a driver's — names the key, the version and sometimes the credentials it connected with", policy.what, err.Error())
		}
		if policy.carries != nil && !errors.Is(err, policy.carries) {
			this.refuse("%s answered a refusal its own error cannot be recognised through", policy.what)
		}
		if after := this.drain(ctx, store, stream); len(after) != len(before) {
			this.refuse("%s left %d events on the stream where the append it refused found %d, and a refusal that says nothing was written wrote", policy.what, len(after), len(before))
		}
	}
}

// Refused and Closed are the two outcomes that mean refused rather than tried,
// and a caller reads either as nothing having happened. An operation that wrote
// and then reported one of them is the shape no hand-out assertion and no
// sentinel table can see, so it is asked here, where the stream is counted
// either side of the append.
func (this *probe) wroteWhatItSaid(ctx context.Context, on fixture) event.At[ledger] {
	stream := this.streamOf(on.held, on.id)
	before := len(this.drain(ctx, on.store, stream))
	moved, _, err := on.repo.Append(ctx, on.at, on.held.credited.New(on.id, credited{Amount: 1}))
	if err == nil {
		return moved
	}
	if written := len(this.drain(ctx, on.store, stream)); written != before {
		this.refuse("an append that answered %v left %d events on a stream that held %d, so an operation that reported it refused rather than tried had already written", err, written, before)
	}
	this.refuse("an ordinary append answered %v", err)
	return moved
}

// Every sentinel belongs to one class and errors.Is never crosses one, so a
// refusal is asked for one member of each of the other five and must answer no
// to all of them.
func (this *probe) partitioned(doing string, err error, is error) {
	if !errors.Is(err, is) {
		this.refuse("%s answered %v where the contract answers %v", doing, err, is)
	}
	for _, elsewhere := range []error{
		event.ErrDeclaration, event.ErrWrongStore, event.ErrKey, event.ErrPayload, event.ErrConflict, event.ErrBackend,
	} {
		if elsewhere == is || errors.Is(is, elsewhere) {
			continue
		}
		if errors.Is(err, elsewhere) {
			this.refuse("%s answered a refusal that matches both %v and %v, which are two classes and two different obligations on the caller", doing, is, elsewhere)
		}
	}
}

// What survives the value that wrote it, which is what a restart is. The second
// store is asked for after the write and not before, because that is the order
// a restart happens in: a factory whose New answers a value over the same
// backing is a store that persists, and one whose New starts again elsewhere
// says so through a backing that does not compare Equal.
func durabilitySection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	id := this.account("a")
	_, at := this.load(ctx, repo, id)
	this.append(ctx, repo, at, held.credited.New(id, credited{Amount: 12}))

	restarted := this.beside(store, held)
	if restarted == nil {
		restarted = this.store()
	}
	if !restarted.Backing().Equal(store.Backing()) {
		this.unable("this factory builds no second store value over the backing the first one wrote to, so nothing here outlived the value that wrote it")
		return
	}
	if state, _ := this.load(ctx, this.bind(restarted, held), id); state.Balance != 12 {
		this.refuse("a store value built after the one that wrote folds the stream to %d, and this store claims what it holds survives", state.Balance)
	}
}

// What a case is asked of, opened once and passed whole: a store, the value the
// case runs through, a repository bound to it and one identity of the suite's
// own. Which door a case opens is then a function rather than a word a later
// branch reads back out of a sentence written for a human.
type fixture struct {
	store event.Store
	log   event.Log
	repo  *event.Repo[ledger, accountID]
	held  declaration
	id    accountID
	at    event.At[ledger]
}

type failureCase struct {
	outcome event.Outcome
	door    string
	ask     func(*probe, context.Context, fixture) error
	is      error
	isNot   error
	through string
	build   func(event.Store) event.Store
}

func storeFailureSection(this *probe) {
	ctx := this.context()
	for _, injected := range []failureCase{
		{outcome: event.NotWritten, door: "append", ask: injectedAtAppend, is: event.ErrBackend, isNot: event.ErrUncertain},
		{outcome: event.Unconfirmed, door: "append", ask: injectedAtAppend, is: event.ErrUncertain, isNot: event.ErrBackend},
		{outcome: event.Conflict, door: "append", ask: injectedAtAppend, is: event.ErrConflict, isNot: event.ErrBackend},
		{outcome: event.Closed, door: "append", ask: injectedAtAppend, is: event.ErrClosed, isNot: event.ErrUncertain},
		{outcome: event.Refused, door: "append", ask: injectedAtAppend, is: event.ErrRefused, isNot: event.ErrUncertain},
		{outcome: event.Unclassified, door: "append", ask: injectedAtAppend, is: event.ErrUncertain, isNot: event.ErrBackend},
		{outcome: event.BadCursor, door: "read", ask: injectedAtRead, is: event.ErrCursor, isNot: event.ErrBackend},
		{outcome: event.Unclassified, door: "read", ask: injectedAtRead, is: event.ErrBackend, isNot: event.ErrUncertain},
	} {
		for _, through := range []struct {
			what  string
			build func(event.Store) event.Store
		}{
			{"the store itself", func(store event.Store) event.Store { return store }},
			{"a decorator that wraps the store's own error", func(store event.Store) event.Store { return wrapping{over{store}} }},
		} {
			injected.through, injected.build = through.what, through.build
			if !this.classified(ctx, injected) {
				break
			}
		}
	}
}

func (this *probe) classified(ctx context.Context, one failureCase) bool {
	store := this.store()
	held := this.declared()
	through := one.build(store)
	repo := this.bind(through, held)
	id := this.account("a")
	_, at := this.load(ctx, repo, id)
	at, _ = this.append(ctx, repo, at, held.credited.New(id, credited{Amount: 1}))
	if !this.factory.Fail(this.t, store, one.outcome) {
		this.unable("%v cannot be produced by this store", one.outcome)
		return false
	}

	doing := fmt.Sprintf("%v injected at the %s door through %s", one.outcome, one.door, one.through)
	err := one.ask(this, ctx, fixture{store: store, log: through, repo: repo, held: held, id: id, at: at})
	if !errors.Is(err, one.is) {
		this.refuse("%s answered %v where the kernel maps that classification to %v", doing, err, one.is)
	}
	if errors.Is(err, one.isNot) {
		this.refuse("%s answered a refusal that also matches %v, and the two state opposite obligations on the caller", doing, one.isNot)
	}
	if err.Error() != one.is.Error() {
		this.refuse("%s renders as %q rather than as its own classification, so a store's text — which names the key, the version and sometimes a credential — travels with it", doing, err.Error())
	}
	if one.outcome == event.NotWritten {
		if written := this.drain(ctx, store, this.streamOf(held, id)); len(written) != 1 {
			this.refuse("%s left %d events on a stream that held one, and the store said the write certainly did not land", doing, len(written))
		}
	}
	return true
}

func injectedAtAppend(this *probe, ctx context.Context, on fixture) error {
	_, _, err := on.repo.Append(ctx, on.at, on.held.credited.New(on.id, credited{Amount: 1}))
	return err
}

func injectedAtRead(this *probe, ctx context.Context, on fixture) error {
	reader, err := event.Read(on.log, "")
	if err != nil {
		this.refuse("reading through this store answered %v", err)
	}
	_, err = reader.Next(ctx)
	return err
}
