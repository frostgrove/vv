package eventtest

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/event"
)

func transactionsSection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()

	plain := this.account("plain")
	_, at := this.load(ctx, repo, plain)
	this.append(ctx, repo, at, held.credited.New(plain, credited{Amount: 1}))
	if state, _ := this.load(ctx, repo, plain); state.Balance != 1 {
		this.refuse("an append with no transaction bound folded to %d, so nothing below is about the transaction", state.Balance)
	}
	if _, err := repo.Within(ctx); !errors.Is(err, event.ErrNoTransaction) {
		this.refuse("asking to be inside a transaction the context does not carry answered %v", err)
	}

	this.staged(ctx, store, repo, held)
	this.discarded(ctx, store, repo, held)
	this.contended(ctx, store, repo, held)
	this.crossed(ctx, store, repo, held)
	this.aftermath(ctx, store, repo, held)
	this.unbound(ctx, store, repo, held)
	this.claimed(ctx, store, repo, held)
	this.joined(ctx, store, repo, held)
	this.chained(ctx, held)
}

// The ordinary unit of work is two streams — an aggregate and the audit entry
// beside it — and what it holds while it is live it frees when it finishes. A
// store that tracks the streams of a transaction in one variable rather than in
// a set commits both and then releases one of them: the other stays held by a
// claim nothing will ever release, and every later writer of it is refused a
// conflict there is no longer a competitor for. Nothing else in this suite gives
// one transaction two streams, so nothing else can see it.
func (this *probe) claimed(ctx context.Context, store event.Store, repo *event.Repo[ledger, accountID], held declaration) {
	credits := []struct {
		id     accountID
		amount int64
	}{{this.account("h"), 1}, {this.account("i"), 2}}

	inside, tx := this.begin(ctx, store)
	for _, credit := range credits {
		_, at := this.load(inside, repo, credit.id)
		this.append(inside, repo, at, held.credited.New(credit.id, credited{Amount: credit.amount}))
	}
	this.commit(ctx, tx)

	for _, credit := range credits {
		state, at := this.load(ctx, repo, credit.id)
		if state.Balance != credit.amount {
			this.refuse("a stream one transaction wrote to beside another folds to %d after that transaction committed, where the decision taken inside it credited %d", state.Balance, credit.amount)
		}
		if _, _, err := repo.Append(ctx, at, held.credited.New(credit.id, credited{Amount: 3})); err != nil {
			this.refuse("an append to a stream a committed transaction wrote to, beside one other stream, answered %v — a unit of work that frees one of the streams it took leaves the other held by a claim nothing will ever release, and every writer of it after is refused for a competitor that has finished", err)
		}
	}
}

// A unit of work is the backing's and not the value that opened it: two store
// values over one backing are one store, and a request that composes a
// repository per feature module opens a transaction through one of them and
// writes through the others. A store that finds its transaction by the value
// that began it leaves every one of those writes on this store's autocommit —
// admitted, invisible to the rollback the caller believes in, and reported by
// nothing at any point.
func (this *probe) joined(ctx context.Context, store event.Store, repo *event.Repo[ledger, accountID], held declaration) {
	sibling := this.beside(store, held)
	if sibling == nil {
		this.unable("this factory builds no second store value over one backing, so no unit of work was carried through a value other than the one that opened it")
		return
	}
	if !sibling.Backing().Equal(store.Backing()) {
		this.refuse("the second store value this factory built writes to another backing, so nothing here was two values of one store")
	}
	elsewhere := this.bind(sibling, held)

	id := this.account("j")
	inside, tx := this.begin(ctx, store)
	here, err := repo.Authority(inside)
	if err != nil || !here.Valid() {
		this.refuse("the value that began the unit of work answered %v for it", err)
	}
	there, err := elsewhere.Authority(inside)
	if err != nil || !there.Valid() {
		this.refuse("a second store value over one backing answered %v for the unit of work the value beside it began, so a repository composed per feature module cannot tell it is inside one", err)
	}
	if !here.Same(there) {
		this.refuse("two store values over one backing answered two different authorities for one unit of work, so two subsystems writing through their own repositories cannot prove they wrote together")
	}

	_, at := this.load(inside, elsewhere, id)
	this.append(inside, elsewhere, at, held.credited.New(id, credited{Amount: 4}))
	if outside, _ := this.load(ctx, repo, id); outside.Balance != 0 {
		this.refuse("a reader outside the unit of work folds the stream to %d after a second store value over one backing appended inside it, so that append ran on this store's autocommit and no rollback can take it back", outside.Balance)
	}
	this.rollback(ctx, tx)
	if state, _ := this.load(ctx, repo, id); state.Balance != 0 {
		this.refuse("the stream folds to %d after the unit of work a second value appended inside rolled back", state.Balance)
	}
}

// One transaction, two appends and a load between them: the second is admitted
// at the version the first produced rather than at the committed one, and the
// load inside sees what the transaction has staged while a reader outside does
// not.
func (this *probe) staged(ctx context.Context, store event.Store, repo *event.Repo[ledger, accountID], held declaration) {
	id := this.account("a")
	inside, tx := this.begin(ctx, store)
	marked, err := repo.Within(inside)
	if err != nil {
		this.refuse("being inside a transaction this store answered for answered %v", err)
	}

	_, at := this.load(marked, repo, id)
	at, first := this.append(marked, repo, at, held.credited.New(id, credited{Amount: 2}))
	state, reached := this.load(marked, repo, id)
	if state.Balance != 2 || reached.Version() != 1 {
		this.refuse("a load inside the transaction that had just appended folds to %d at version %d, so a decision taken inside cannot be read by the one after it", state.Balance, reached.Version())
	}
	_, second := this.append(marked, repo, at, held.credited.New(id, credited{Amount: 3}))

	authority, err := repo.Authority(marked)
	if err != nil || !authority.Valid() {
		this.refuse("the authority two subsystems compare answered %v inside a transaction of this store's", err)
	}
	if !first.Authority().Same(second.Authority()) || !first.Authority().Same(authority) {
		this.refuse("two appends inside one transaction were written through authorities that do not compare the same, so a subsystem proving it wrote atomically with another cannot")
	}
	if outside, _ := this.load(ctx, repo, id); outside.Balance != 0 {
		this.refuse("a reader outside the transaction folds the stream to %d before anything was committed", outside.Balance)
	}
	this.commit(ctx, tx)
	if state, _ := this.load(ctx, repo, id); state.Balance != 5 {
		this.refuse("the stream folds to %d after a transaction that appended 2 and then 3 committed", state.Balance)
	}
	if err := tx.Commit(ctx); err == nil {
		this.refuse("committing a transaction that had already been committed answered no error at all")
	}
}

func (this *probe) discarded(ctx context.Context, store event.Store, repo *event.Repo[ledger, accountID], held declaration) {
	id := this.account("b")
	inside, tx := this.begin(ctx, store)
	_, at := this.load(inside, repo, id)
	this.append(inside, repo, at, held.credited.New(id, credited{Amount: 7}))
	this.rollback(ctx, tx)

	if state, reached := this.load(ctx, repo, id); state.Balance != 0 || reached.Version() != 0 {
		this.refuse("a stream a rolled-back transaction wrote to folds to %d at version %d", state.Balance, reached.Version())
	}
	_, at = this.load(ctx, repo, id)
	this.append(ctx, repo, at, held.credited.New(id, credited{Amount: 8}))
	if state, _ := this.load(ctx, repo, id); state.Balance != 8 {
		this.refuse("the stream folds to %d after a rollback and a fresh decision, so the rollback did not free what it had claimed", state.Balance)
	}
}

// Two transactions live over one stream at once, and the loser is refused rather
// than admitted. Whether a store waits for a competing transaction or refuses at
// once is its own business and is promised to nobody, so the case is built so
// that nothing here can be waited for: both transactions are open before either
// issues anything, the loser's decision is taken outside both of them, and the
// loser's first statement is issued after the winner has committed. After the
// refusal nothing further is issued on the transaction that was refused.
func (this *probe) contended(ctx context.Context, store event.Store, repo *event.Repo[ledger, accountID], held declaration) {
	id := this.account("g")
	winning, one := this.begin(ctx, store)
	losing, two := this.begin(ctx, store)
	_, theirs := this.load(ctx, repo, id)

	_, mine := this.load(winning, repo, id)
	this.append(winning, repo, mine, held.credited.New(id, credited{Amount: 1}))
	this.commit(ctx, one)
	if _, _, err := repo.Append(losing, theirs, held.credited.New(id, credited{Amount: 2})); !errors.Is(err, event.ErrConflict) {
		this.refuse("a transaction that had loaded the stream at the version another has since committed over answered %v, and both decided from a state that did not include the other's", err)
	}
	this.rollback(ctx, two)

	if state, _ := this.load(ctx, repo, id); state.Balance != 1 {
		this.refuse("the contended stream folds to %d where exactly one of two transactions was admitted", state.Balance)
	}
}

// Two live transactions, and the two things the contract says about them: their
// authorities are not the same value, and a context marked for one that has
// since acquired another is refused before any statement.
func (this *probe) crossed(ctx context.Context, store event.Store, repo *event.Repo[ledger, accountID], held declaration) {
	id := this.account("c")
	first, one := this.begin(ctx, store)
	second, two := this.begin(ctx, store)
	here, err := repo.Authority(first)
	if err != nil {
		this.refuse("the authority of one live transaction answered %v", err)
	}
	there, err := repo.Authority(second)
	if err != nil {
		this.refuse("the authority of a second live transaction answered %v", err)
	}
	if here.Same(there) {
		this.refuse("two live transactions of this store answer authorities that compare the same, so a subsystem proving it wrote inside another's transaction cannot tell them apart")
	}

	marked, err := repo.Within(first)
	if err != nil {
		this.refuse("being inside the first transaction answered %v", err)
	}
	_, at := this.load(ctx, repo, id)
	crossed, three := this.begin(marked, store)
	if _, _, err := repo.Load(crossed, id); !errors.Is(err, event.ErrTransactionMismatch) {
		this.refuse("a load through a context marked for one transaction and carrying another answered %v", err)
	}
	if _, _, err := repo.Append(crossed, at, held.credited.New(id, credited{Amount: 1})); !errors.Is(err, event.ErrTransactionMismatch) {
		this.refuse("an append through a context marked for one transaction and carrying another answered %v", err)
	}
	this.rollback(ctx, three)
	this.rollback(ctx, two)
	this.rollback(ctx, one)
}

// The recovery the contract states, driven rather than described: a conflict
// inside a bound transaction makes that transaction unusable, so the caller
// rolls back, begins again, reloads and decides again. Nothing else is issued on
// the transaction that was refused.
func (this *probe) aftermath(ctx context.Context, store event.Store, repo *event.Repo[ledger, accountID], held declaration) {
	id := this.account("d")
	_, at := this.load(ctx, repo, id)
	stale := at
	this.append(ctx, repo, at, held.credited.New(id, credited{Amount: 1}))

	inside, tx := this.begin(ctx, store)
	if _, _, err := repo.Append(inside, stale, held.credited.New(id, credited{Amount: 2})); !errors.Is(err, event.ErrConflict) {
		this.refuse("an append at a version the stream has left, issued inside a transaction, answered %v", err)
	}
	this.rollback(ctx, tx)

	again, second := this.begin(ctx, store)
	_, at = this.load(again, repo, id)
	this.append(again, repo, at, held.credited.New(id, credited{Amount: 2}))
	if state, _ := this.load(again, repo, id); state.Balance != 3 {
		this.refuse("a reload inside the transaction that had just recovered folds to %d", state.Balance)
	}
	this.commit(ctx, second)
	if state, _ := this.load(ctx, repo, id); state.Balance != 3 {
		this.refuse("the stream folds to %d after a conflict, a rollback, a fresh transaction and a fresh decision", state.Balance)
	}
}

// The obligation's control, asserted so nobody reads silence as a guarantee: a
// transaction this store opened and nothing bound holds nothing back, and its
// rollback discards nothing that was written outside it.
func (this *probe) unbound(ctx context.Context, store event.Store, repo *event.Repo[ledger, accountID], held declaration) {
	id := this.account("e")
	_, tx := this.begin(ctx, store)
	_, at := this.load(ctx, repo, id)
	this.append(ctx, repo, at, held.credited.New(id, credited{Amount: 9}))
	this.rollback(ctx, tx)

	if state, _ := this.load(ctx, repo, id); state.Balance != 9 {
		this.refuse("an append issued while a transaction of this store was open and bound to nothing folds to %d after that transaction rolled back, and what nothing was bound for ran on this store's own autocommit", state.Balance)
	}
}

// Two backings, one context: an operation that writes to both binds a
// transaction for each and neither shadows the other.
func (this *probe) chained(ctx context.Context, held declaration) {
	first, second := this.store(), this.store()
	if first.Backing().Equal(second.Backing()) {
		this.unable("this factory builds no two stores over two backings, so no context carried a transaction for each")
		return
	}
	here, there := this.bind(first, held), this.bind(second, held)
	inside, one := this.begin(ctx, first)
	beside, two := this.begin(inside, second)

	marked, err := here.Within(beside)
	if err != nil {
		this.refuse("being inside the first store's transaction answered %v", err)
	}
	marked, err = there.Within(marked)
	if err != nil {
		this.refuse("being inside the second store's transaction, in a context already marked for the first, answered %v", err)
	}
	id := this.account("f")
	_, at := this.load(marked, here, id)
	this.append(marked, here, at, held.credited.New(id, credited{Amount: 1}))
	if _, _, err := there.Load(marked, id); err != nil {
		this.refuse("a load through the second store, in a context marked for both, answered %v", err)
	}
	this.commit(ctx, two)
	this.commit(ctx, one)
}

// The disposal is registered here rather than left to the case, because a case
// that refuses leaves through a panic and one of them abandons its transaction
// on purpose. A transaction of a store with a connection pool holds a connection
// and every row lock it took until something finishes it, and the next section's
// store draws from that same pool.
func (this *probe) begin(ctx context.Context, store event.Store) (context.Context, Tx) {
	inside, tx := this.factory.Begin(this.t, ctx, store)
	if tx == nil {
		this.refuse("the factory began no transaction")
	}
	if inside == nil {
		this.refuse("the factory answered no context carrying the transaction it began")
	}
	this.t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return inside, tx
}

func (this *probe) commit(ctx context.Context, tx Tx) {
	if err := tx.Commit(ctx); err != nil {
		this.refuse("committing the transaction this case was inside answered %v", err)
	}
}

func (this *probe) rollback(ctx context.Context, tx Tx) {
	if err := tx.Rollback(ctx); err != nil {
		this.refuse("rolling back the transaction this case was inside answered %v", err)
	}
}
