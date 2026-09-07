package eventtest

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/frostgrove/vv/event"
)

func bindingSection(this *probe) {
	ctx := this.context()
	store := this.store()
	held := this.declared()
	repo := this.bind(store, held)
	before := publishedBy(store)
	if _, at := this.load(ctx, repo, this.account("a")); at.Version() != 0 {
		this.refuse("a stream nothing has been appended to loaded at version %d", at.Version())
	}

	this.unchanged(store, before)

	binding := event.Open(store)
	if _, err := event.Bind(binding, held.aggregate); err != nil {
		this.refuse("binding one aggregate answered %v", err)
	}
	if _, err := event.Bind(binding, held.aggregate); err != nil {
		this.refuse("binding the same declaration a second time answered %v, and a composition root that binds per feature module does exactly that", err)
	}
	second, err := declare("eventtest.second." + this.mark)
	if err != nil {
		this.refuse("the suite cannot declare a second aggregate: %v", err)
	}
	if _, err := event.Bind(binding, second.aggregate); err != nil {
		this.refuse("binding a second family answered %v", err)
	}

	clash, err := declare(held.family)
	if err != nil {
		this.refuse("the suite cannot declare a second aggregate over one family: %v", err)
	}
	if _, err := event.Bind(binding, clash.aggregate); !errors.Is(err, event.ErrFamily) {
		this.refuse("a second declaration of the family %q through one binding answered %v, and two aggregates over one family fold each other's facts with no error at any point", held.family, err)
	}
	if _, err := event.Bind(event.Open(store), clash.aggregate); err != nil {
		this.refuse("the same collision through a second binding answered %v: the scope of that check is the binding value, and a suite that certified more than that would be certifying a guarantee nobody makes", err)
	}

	for _, dishonest := range this.dishonestStores(store) {
		if _, err := event.Bind(event.Open(dishonest.store), this.declared().aggregate); !errors.Is(err, event.ErrWrongStore) {
			this.refuse("a store that %s was bound with %v", dishonest.what, err)
		}
		if _, err := event.Read(dishonest.store, ""); !errors.Is(err, event.ErrWrongStore) {
			this.refuse("a store that %s was read through with %v, and a deployment that only reads never binds", dishonest.what, err)
		}
	}
	if _, err := event.Read(store, ""); err != nil {
		this.refuse("reading through this store answered %v, so the refusals above are not about the dishonesty they name", err)
	}
}

type dishonest struct {
	what  string
	store event.Store
}

func (this *probe) dishonestStores(store event.Store) []dishonest {
	limits := store.Limits()
	zeroed := limits
	zeroed.MaxPayload = 0
	unstated := store.Capabilities()
	unstated.Transactions = event.Unstated
	return []dishonest{
		{"says nothing about what it writes to", rebacked{over{store}, event.Backing{}}},
		{"publishes a zero MaxPayload", relimited{over{store}, zeroed}},
		{"states nothing about its transactions", restated{over{store}, unstated}},
	}
}

func streamIdentitySection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()

	for _, pair := range []struct {
		what        string
		left, right accountID
	}{
		{
			"a separator that falls in two places",
			accountID{Tenant: this.run, Number: this.mark + "a/b"},
			accountID{Tenant: this.run + "/" + this.mark + "a", Number: "b"},
		},
		{
			"one grapheme spelled composed and decomposed",
			accountID{Tenant: this.run, Number: this.mark + "café"},
			accountID{Tenant: this.run, Number: this.mark + "café"},
		},
	} {
		left, right := this.streamOf(held, pair.left), this.streamOf(held, pair.right)
		if left == right {
			this.refuse("%s renders one stream for two identities, so one aggregate's facts fold into another's state", pair.what)
		}
		_, at := this.load(ctx, repo, pair.left)
		this.append(ctx, repo, at, held.credited.New(pair.left, credited{Amount: 11}))
		state, _ := this.load(ctx, repo, pair.right)
		if state.Balance != 0 {
			this.refuse("with %s, a fact appended to one identity folded into the other's state at %d", pair.what, state.Balance)
		}
	}

	unescaped, err := declareRaw("eventtest.raw." + this.mark)
	if err != nil {
		this.refuse("the suite cannot declare an aggregate over a mapper of its own: %v", err)
	}
	raw := this.bind(store, unescaped)
	illegal := accountID{Tenant: this.run, Number: this.mark + "a\x00b"}
	if _, _, err := raw.Load(ctx, illegal); !errors.Is(err, event.ErrKey) {
		this.refuse("loading an identity a mapper rendered with a NUL in it answered %v", err)
	}
	legal, at := accountID{Tenant: this.run, Number: this.mark + "ab"}, event.At[ledger]{}
	if _, at, err = raw.Load(ctx, legal); err != nil {
		this.refuse("loading the same identity without the NUL answered %v, so the refusal above is not about the NUL", err)
	}
	if _, _, err := raw.Append(ctx, at, unescaped.credited.New(illegal, credited{Amount: 1})); !errors.Is(err, event.ErrKey) {
		this.refuse("a change decided for an identity that renders no legal key answered %v", err)
	}

	capped := this.keyAtCap(held)
	state, at := this.load(ctx, repo, capped)
	if state.Balance != 0 || at.Version() != 0 {
		this.refuse("an identity whose key is exactly this store's MaxKey loaded at version %d", at.Version())
	}
	at, _ = this.append(ctx, repo, at, held.credited.New(capped, credited{Amount: 7}))
	if state, _ := this.load(ctx, repo, capped); state.Balance != 7 {
		this.refuse("an identity whose key is exactly this store's MaxKey folded to %d after one credit of 7", state.Balance)
	}
}

func (this *probe) keyAtCap(held declaration) accountID {
	room := this.limits.MaxKey - len(this.run) - len(this.mark) - 1
	if room < 1 {
		this.refuse("this store's MaxKey of %d has no room for a key of this suite's own", this.limits.MaxKey)
	}
	capped := accountID{Tenant: this.run, Number: this.mark + strings.Repeat("k", room)}
	if key, _ := held.aggregate.Key(capped); len(key) != this.limits.MaxKey {
		this.refuse("the suite built a key of %d bytes where this store's MaxKey is %d, so the case below is not at the bound", len(key), this.limits.MaxKey)
	}
	return capped
}

func expectedVersionSection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	id := this.account("a")
	_, fresh := this.load(ctx, repo, id)

	at, receipt := this.append(ctx, repo, fresh, held.credited.New(id, credited{Amount: 10}))
	if receipt.First() != 1 || receipt.Last() != 1 || at.Version() != 1 {
		this.refuse("the first append to a fresh stream wrote versions %d..%d and left a token at %d", receipt.First(), receipt.Last(), at.Version())
	}
	if _, _, err := repo.Append(ctx, fresh, held.credited.New(id, credited{Amount: 20})); !errors.Is(err, event.ErrConflict) {
		this.refuse("a second append at the version a fresh stream was loaded at answered %v: version zero is the version a stream has never been appended at and never means any version, and two creations of one aggregate both carry it", err)
	}
	moved, _ := this.append(ctx, repo, at, held.credited.New(id, credited{Amount: 5}))
	if _, _, err := repo.Append(ctx, at, held.credited.New(id, credited{Amount: 5})); !errors.Is(err, event.ErrConflict) {
		this.refuse("an append through a token the stream has moved past answered %v", err)
	}

	other := this.account("b")
	if _, _, err := repo.Append(ctx, moved, held.credited.New(other, credited{Amount: 1})); !errors.Is(err, event.ErrWrongStream) {
		this.refuse("a change decided for one instance and appended through another instance's token answered %v, and every other check in the path passes for it", err)
	}
	mirror, err := declare("eventtest.mirror." + this.mark)
	if err != nil {
		this.refuse("the suite cannot declare a second aggregate over its own state type: %v", err)
	}
	if _, _, err := repo.Append(ctx, moved, mirror.credited.New(id, credited{Amount: 1})); !errors.Is(err, event.ErrWrongStream) {
		this.refuse("a change decided on another aggregate over the same state type answered %v, and the two are one Go type", err)
	}
	if _, _, err := repo.Append(ctx, event.At[ledger]{}, held.credited.New(id, credited{Amount: 1})); !errors.Is(err, event.ErrKey) {
		this.refuse("an append through a token no load minted answered %v", err)
	}
	if crossed := this.drain(ctx, store, this.streamOf(held, other)); len(crossed) != 0 {
		this.refuse("the stream a crossed change named holds %d events, so a refusal that says nothing was written wrote something", len(crossed))
	}

	_, at = this.load(ctx, repo, other)
	this.append(ctx, repo, at, held.credited.New(other, credited{Amount: 3}))
	state, _ := this.load(ctx, repo, other)
	if state.Balance != 3 {
		this.refuse("the correct pairing of an identity and its own change folded to %d after one credit of 3, so the refusals above are not about the crossing", state.Balance)
	}
	if state, _ := this.load(ctx, repo, id); state.Balance != 15 {
		this.refuse("the instance the crossed changes were refused for folded to %d rather than the 15 its own two appends decided", state.Balance)
	}
}

func denseVersionsSection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	id := this.account("a")
	stream := this.streamOf(held, id)
	_, at := this.load(ctx, repo, id)

	at = this.batched(ctx, repo, at,
		held.credited.New(id, credited{Amount: 1}),
		held.credited.New(id, credited{Amount: 2}),
		held.credited.New(id, credited{Amount: 3}))
	stale := at
	this.batched(ctx, repo, at,
		held.opened.New(id, opened{Owner: "acme"}),
		held.credited.New(id, credited{Amount: 4}))

	dense := []event.Version{1, 2, 3, 4, 5}
	if versions := this.versions(this.drain(ctx, store, stream)); !equalVersions(versions, dense) {
		this.refuse("five events appended in two decisions hold versions %v, and a fold that counts from one reads a different history than the store does", versions)
	}
	if _, _, err := repo.Append(ctx, stale, held.credited.New(id, credited{Amount: 9})); !errors.Is(err, event.ErrConflict) {
		this.refuse("an append at a version the stream has left answered %v", err)
	}
	if versions := this.versions(this.drain(ctx, store, stream)); !equalVersions(versions, dense) {
		this.refuse("the stream holds versions %v after a refused append, so a refusal wrote, reused or reassigned a version", versions)
	}
	this.instants(ctx, store, stream)
	if state, _ := this.load(ctx, repo, id); state.Balance != 10 || state.Owner != "acme" {
		this.refuse("the five events fold to a balance of %d and an owner of %q", state.Balance, state.Owner)
	}
}

// The instant is the store's own, from a clock this suite has nothing to check
// it against and no right to order by. What holds whatever clock it came from is
// that an event this run appended carries one and that the read after answers
// the same one: a store whose select list drops the column answers the zero
// time to everything that displays, exports or audits it, and one that fills it
// at read time answers an audit trail regenerated on every read. Neither is
// visible anywhere else, because the instant orders nothing.
func (this *probe) instants(ctx context.Context, store event.Store, stream event.Stream) {
	page := this.drain(ctx, store, stream)
	if len(page) == 0 {
		this.refuse("a stream this section appended to answered nothing to ask about the recorded instant of")
	}
	for _, envelope := range page {
		if envelope.RecordedAt.IsZero() {
			this.refuse("%s version %d was read back carrying no recorded instant at all", envelope.Stream, envelope.Version)
		}
	}
	again := this.drain(ctx, store, stream)
	if len(again) != len(page) {
		this.refuse("two reads of one stream answered %d and %d events", len(page), len(again))
	}
	for index, envelope := range again {
		if !envelope.RecordedAt.Equal(page[index].RecordedAt) {
			this.refuse("%s version %d answered two different recorded instants on two reads, so the instant is minted when the event is read rather than when it was written", envelope.Stream, envelope.Version)
		}
	}
}

func concurrencySection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	id := this.account("a")
	stream := this.streamOf(held, id)

	const writers = 8
	var ready, done sync.WaitGroup
	gate := make(chan struct{})
	answers := make([]error, writers)
	loaded := make([]event.Version, writers)
	ready.Add(writers)
	done.Add(writers)
	for writer := range writers {
		go func() {
			defer done.Done()
			_, at, err := repo.Load(ctx, id)
			loaded[writer] = at.Version()
			ready.Done()
			<-gate
			if held.credited.Name() == "" || held.credited.Revisions() == 0 {
				err = errors.New("the declaration every writer reads answered no name")
			}
			if err == nil {
				_, _, err = repo.Append(ctx, at, held.credited.New(id, credited{Amount: 1}))
			}
			answers[writer] = err
		}()
	}
	ready.Wait()
	close(gate)
	done.Wait()

	winners := 0
	for writer, err := range answers {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, event.ErrConflict):
		default:
			this.refuse("one of %d writers that all loaded version %d answered %v, which is neither a win nor a conflict", writers, loaded[writer], err)
		}
	}
	if winners != 1 {
		this.refuse("%d of %d writers that had all loaded version 0 were admitted, and every one of them decided from a state that did not include the others", winners, writers)
	}
	if written := this.drain(ctx, store, stream); len(written) != 1 {
		this.refuse("the stream holds %d events after %d concurrent appends at one version", len(written), writers)
	}

	_, at := this.load(ctx, repo, id)
	if _, _, err := repo.Append(ctx, at, held.credited.New(id, credited{Amount: 1})); err != nil {
		this.refuse("a single writer at the version it loaded answered %v, so the refusals above are the contention's", err)
	}
}

func sharedBackingSection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	sibling := this.factory.Sibling(this.t, store)
	if sibling == nil {
		this.refuse("the factory answered no second store value over this backing")
	}
	if !sibling.Backing().Equal(store.Backing()) {
		this.refuse("the second store value this factory built writes to another backing, so two values over one backing were never under test")
	}
	beside := this.bind(sibling, held)

	id := this.account("a")
	_, at := this.load(ctx, repo, id)
	at, _ = this.append(ctx, repo, at, held.credited.New(id, credited{Amount: 4}))
	at, _ = this.append(ctx, beside, at, held.credited.New(id, credited{Amount: 6}))
	for _, through := range []struct {
		what string
		repo *event.Repo[ledger, accountID]
	}{{"the value that minted the token", repo}, {"the value beside it", beside}} {
		if state, _ := this.load(ctx, through.repo, id); state.Balance != 10 {
			this.refuse("%s folds the stream to %d after two appends of 4 and 6 through two values over one backing", through.what, state.Balance)
		}
	}

	foreign := this.bind(rebacked{over{store}, this.foreignBacking()}, held)
	if _, _, err := foreign.Append(ctx, at, held.credited.New(id, credited{Amount: 1})); !errors.Is(err, event.ErrWrongStore) {
		this.refuse("a token minted over this backing was appended through a repository over another backing with %v", err)
	}
}

func (this *probe) foreignBacking() event.Backing {
	backing, err := event.NewBacking(new(int))
	if err != nil {
		this.refuse("the suite cannot mint a backing of its own: %v", err)
	}
	return backing
}

func equalVersions(held, wanted []event.Version) bool {
	if len(held) != len(wanted) {
		return false
	}
	for index := range held {
		if held[index] != wanted[index] {
			return false
		}
	}
	return true
}
