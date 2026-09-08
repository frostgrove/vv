package event

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestAFreshStreamLoadsAsZero(t *testing.T) {
	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()

	state, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("a stream nobody has written to was refused with %v, so a caller has to tell a fresh aggregate from a missing one and the framework invented the difference", err)
	}
	if at.Version() != 0 {
		t.Fatalf("a stream with no events loaded at version %d, so the first append names a version the stream never had", at.Version())
	}
	if want := (Stream{Family: "accounts.account", Key: Compose("acme", "A-17")}); at.Stream() != want {
		t.Fatalf("the token names %v where the declared mapper renders %v", at.Stream(), want)
	}
	if state.Balance != 0 || state.Applied != nil || state.Tags != nil {
		t.Fatalf("a stream with no events folded to %+v rather than to the zero value of the state type", state)
	}

	if _, _, err := repo.Append(ctx, at,
		declared.opened.New(acme, opened{Owner: "acme"}),
		declared.credited.New(acme, creditedV2{Minor: 25, Reason: "deposit"})); err != nil {
		t.Fatalf("the control append was refused with %v, so the case above passes against a repository that loads nothing at all", err)
	}
	state, at, err = repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the stream those two facts were written to was refused with %v", err)
	}
	if at.Version() != 2 || state.Balance != 25 || !state.Applied["deposit"] {
		t.Fatalf("two facts folded to %+v at version %d, so a load answers the zero value whatever the stream holds", state, at.Version())
	}
}

// Each row breaks the step it names and every step after it, so the sentinel it
// asserts is the one the fixed order produces and not the one the shortest path
// happens to reach.
func TestAppendRefusesInItsStatedOrder(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	other := accountID{tenant: "acme", number: "A-99"}
	ctx := context.Background()

	store := newRecordingStore(t)
	elsewhere := newRecordingStore(t)
	aggregate := Define[account]("accounts.account", accountKey)
	opening := Declare(aggregate, "accounts.opened", From(JSON[opened]()), openAccount)
	measuring := Declare(aggregate, "accounts.measured", From(JSON[reading]()),
		func(this account, _ reading) account { return this })

	repo, err := Bind(Open(store), aggregate)
	if err != nil {
		t.Fatalf("the fixture store was refused at Bind: %v", err)
	}
	foreign, err := Bind(Open(elsewhere), aggregate)
	if err != nil {
		t.Fatalf("the second store was refused at Bind: %v", err)
	}

	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the token every row below starts from could not be minted: %v", err)
	}
	_, elsewhereAt, err := foreign.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the token minted over the second backing could not be minted: %v", err)
	}

	unencodable := measuring.New(acme, reading{Rate: math.Inf(1)})
	if !errors.Is(unencodable.Err(), ErrEncode) {
		t.Fatalf("the payload its codec cannot encode minted a change carrying %v, so the rows below carry no refusal to surface", unencodable.Err())
	}
	mine := opening.New(acme, opened{Owner: "acme"})
	theirs := opening.New(other, opened{Owner: "acme"})
	batch := []Change[account]{mine, mine, mine, mine, mine}
	if len(batch) <= store.limits.MaxBatch {
		t.Fatalf("the over-long batch holds %d changes against a bound of %d, so the bounds row asserts nothing", len(batch), store.limits.MaxBatch)
	}

	for _, one := range []struct {
		step    string
		at      At[account]
		changes []Change[account]
		want    error
	}{
		{"the token's key, against a change decided for another stream", At[account]{}, []Change[account]{theirs}, ErrKey},
		{"a change's stream, against a change carrying its own refusal", at, []Change[account]{theirs, unencodable}, ErrWrongStream},
		{"a carried refusal, against a batch over the bound", at, append([]Change[account]{unencodable}, batch...), ErrEncode},
		{"the bounds, against a token minted over another backing", elsewhereAt, batch, ErrTooLarge},
		{"the backing, against a context carrying no transaction of this store's", elsewhereAt, []Change[account]{mine}, ErrWrongStore},
		{"the transaction question", at, []Change[account]{mine}, ErrAmbientNotTransaction},
	} {
		store.forget()
		answered, _, err := repo.Append(withRecordingExecutor(ctx, store), one.at, one.changes...)
		if !errors.Is(err, one.want) {
			t.Fatalf("%s answered %v where %v is what the fixed order produces there", one.step, err, one.want)
		}
		if store.count("Append") != 0 {
			t.Fatalf("%s reached the store %d times, and a refusal before the store is what makes the token's version still the caller's",
				one.step, store.count("Append"))
		}
		if answered != one.at {
			t.Fatalf("%s answered a token other than the one it was given, so a caller assigning the result back loses the version it loaded at", one.step)
		}
	}

	store.forget()
	advanced, receipt, err := repo.Append(ctx, at, mine)
	if err != nil {
		t.Fatalf("the control append was refused with %v, so every row above passes against a repository that refuses everything", err)
	}
	if store.count("Append") != 1 || advanced.Version() != 1 || receipt.Count() != 1 {
		t.Fatalf("the control reached the store %d times and answered version %d with a receipt of %d",
			store.count("Append"), advanced.Version(), receipt.Count())
	}
}

// Bind refuses two declarations of one family through one Binding, so the
// crossing arrives through two, which INV-039 admits deliberately. What refuses
// it here is the change naming the aggregate it was decided on, and the sentinel
// is Bind's, because the two doors answer one question.
func TestAnAppendRefusesAChangeDecidedOnAnotherAggregateOfThisFamily(t *testing.T) {
	store := newRecordingStore(t)
	repo, _ := bindAccounts(t, store)
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()

	twin := Define[account]("accounts.account", accountKey)
	twinned := Declare(twin, "accounts.credited",
		Then(From(JSON[creditedV1]()), JSON[creditedV2](), toCreditedV2), creditAccount)
	elsewhere, err := Bind(Open(store), twin)
	if err != nil {
		t.Fatalf("a second declaration of one family through a second binding was refused at Bind: %v, and the escape this case is about is the one INV-039 admits", err)
	}

	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the token this append starts from could not be minted: %v", err)
	}
	crossing := twinned.New(acme, creditedV2{Minor: 250, Reason: "deposit"})
	if crossing.Stream() != at.Stream() {
		t.Fatalf("the change names %v and the token %v, so the two declarations no longer share a stream and the stream comparison is what refuses below", crossing.Stream(), at.Stream())
	}

	store.forget()
	answered, _, err := repo.Append(ctx, at, crossing)
	if !errors.Is(err, ErrFamily) || errors.Is(err, ErrWrongStream) {
		t.Fatalf("a change decided on another aggregate of this family appended with %v, and a fact recorded through it is unloadable by the declaration that wrote it", err)
	}
	if store.count("Append") != 0 {
		t.Fatalf("the refused append reached the store %d times", store.count("Append"))
	}
	if answered != at {
		t.Fatalf("the refusal answered a token other than the one it was given")
	}

	store.forget()
	if _, _, err := elsewhere.Append(ctx, at, crossing); err != nil {
		t.Fatalf("the same token and the same change, through the repository whose aggregate minted it, were refused with %v — so the refusal above is a repository that refuses every append rather than one that reads which aggregate decided", err)
	}
	if store.count("Append") != 1 {
		t.Fatalf("the control append reached the store %d times", store.count("Append"))
	}
}

func TestAForgedTokenIsRefusedBeforeAnyStatement(t *testing.T) {
	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()
	change := declared.opened.New(acme, opened{Owner: "acme"})

	for _, one := range []struct {
		what    string
		changes []Change[account]
	}{
		{"with a change to write", []Change[account]{change}},
		{"with nothing to write", nil},
	} {
		store.forget()
		if _, _, err := repo.Append(ctx, At[account]{}, one.changes...); !errors.Is(err, ErrKey) {
			t.Fatalf("a token no Load minted, %s, answered %v; its key is empty and the kernel's key rules are what refuse it", one.what, err)
		}
		if store.made() != 0 {
			t.Fatalf("a forged token %s made %d calls on the store, so a value the framework never produced reached the backing", one.what, store.made())
		}
	}

	store.forget()
	stream := Stream{Family: "accounts.account", Key: Compose("acme", "A-17")}
	if _, _, err := repo.Append(ctx, At[account]{stream: stream, version: 3}, change); !errors.Is(err, ErrWrongStore) {
		t.Fatalf("a token with a legal key and no backing answered %v, so the second defence is gone and only the key check stands between a hand-built token and a write", err)
	}
	if store.count("Append") != 0 {
		t.Fatalf("a token with an invalid backing reached the store's append")
	}

	store.forget()
	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the control load was refused with %v", err)
	}
	if _, _, err := repo.Append(ctx, at, change); err != nil {
		t.Fatalf("the control append through a token Load minted was refused with %v, so both cases above pass against a repository that refuses every append", err)
	}
}

// A mapper is application code and may render anything at all, so the rendered
// key crosses the kernel's own text rule before the store's bound is applied to
// it. Both refusals are ErrKey and only the message says which of the two ran,
// which is what a store author reads when a key their store would have accepted
// is refused anyway.
func TestTheRenderedKeyIsCheckedBeforeTheStoresBound(t *testing.T) {
	ctx := context.Background()

	for _, one := range []struct {
		what  string
		key   func(accountID) Key
		id    accountID
		names string
	}{
		{"a mapper that renders a control character", func(id accountID) Key { return Key(id.tenant) },
			accountID{tenant: "acme\nadmin"}, "the rendered key"},
		{"an identity over the bound this store published", accountKey,
			accountID{tenant: strings.Repeat("x", 200), number: "A-17"}, "the stream key"},
	} {
		store := newRecordingStore(t)
		aggregate := Define[account]("accounts.account", one.key)
		Declare(aggregate, "accounts.opened", From(JSON[opened]()), openAccount)
		repo, err := Bind(Open(store), aggregate)
		if err != nil {
			t.Fatalf("%s: the declaration was refused at Bind with %v", one.what, err)
		}

		store.forget()
		_, at, err := repo.Load(ctx, one.id)
		if !errors.Is(err, ErrKey) {
			t.Fatalf("%s answered %v, and a key no door refuses is a stream nobody can name twice", one.what, err)
		}
		if !strings.Contains(err.Error(), one.names) {
			t.Fatalf("%s answered %q, which does not name the rule that refused it, so a store author cannot tell the kernel's bound from their own", one.what, err)
		}
		if at.Stream() != (Stream{}) || store.made() != 0 {
			t.Fatalf("%s minted %v and made %d store calls", one.what, at.Stream(), store.made())
		}
	}

	store := newRecordingStore(t)
	repo, _ := bindAccounts(t, store)
	if _, _, err := repo.Load(ctx, accountID{tenant: "acme", number: "A-17"}); err != nil {
		t.Fatalf("an identity both rules admit was refused with %v, so both rows above pass against a door that refuses every key", err)
	}
}

func TestAnEmptyAppendChecksTheKeyAndNothingElse(t *testing.T) {
	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()

	if _, _, err := repo.Append(ctx, At[account]{}); !errors.Is(err, ErrKey) {
		t.Fatalf("an empty append through a forged token answered %v rather than the key rule that runs before the short circuit", err)
	}

	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the token this case appends nothing through could not be minted: %v", err)
	}
	if _, _, err := repo.Append(ctx, at, declared.opened.New(acme, opened{Owner: "acme"})); err != nil {
		t.Fatalf("the append this case is measured against was refused with %v", err)
	}
	_, at, err = repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the reload was refused with %v", err)
	}

	store.forget()
	answered, receipt, err := repo.Append(ctx, at)
	if err != nil {
		t.Fatalf("a decision that changed nothing was refused with %v, and every call site would then carry the branch that asks whether it decided anything", err)
	}
	if store.made() != 0 {
		t.Fatalf("an append of nothing made %d calls on the store, so a no-op decision costs a round trip and a version check it never performed", store.made())
	}
	if answered != at {
		t.Fatalf("an append of nothing answered a token other than the one it was given")
	}
	switch {
	case !receipt.Empty():
		t.Fatal("a receipt for an append that wrote nothing does not say so, so it cannot be told from one that wrote")
	case receipt.Stream() != at.Stream():
		t.Fatalf("an empty receipt names %v rather than the stream of the token it was produced for", receipt.Stream())
	case receipt.First() != 0:
		t.Fatalf("an empty receipt reports the first version it wrote as %d, and it wrote none", receipt.First())
	case receipt.Last() != at.Version():
		t.Fatalf("an empty receipt reports the stream at version %d where the token that went in names %d", receipt.Last(), at.Version())
	case receipt.Count() != 0:
		t.Fatalf("an empty receipt counts %d events", receipt.Count())
	case receipt.Authority().Valid():
		t.Fatal("an empty receipt carries a valid authority, so an append that wrote nothing claims to be atomic with something")
	}

	written, receipt, err := repo.Append(ctx, at, declared.credited.New(acme, creditedV2{Minor: 5, Reason: "deposit"}))
	if err != nil {
		t.Fatalf("the control append was refused with %v, so the assertions above pass against a receipt that is always empty: %v", err, receipt)
	}
	if receipt.Empty() || receipt.Count() != 1 || receipt.First() != at.Version()+1 || receipt.Last() != written.Version() {
		t.Fatalf("an append that wrote one event answered a receipt of %d events from %d to %d", receipt.Count(), receipt.First(), receipt.Last())
	}
}

func TestAnOverLongPageIsRefused(t *testing.T) {
	store, repo := aStreamOfThree(t)
	state, at, err := repo.Load(context.Background(), accountID{tenant: "acme", number: "A-17"})
	if err != nil || at.Version() != 3 || state.Balance != 7 {
		t.Fatalf("a stream of three events read in pages of %d folded to %+v at version %d (%v), so the case below cannot tell a refused page from a store that never serves one",
			store.limits.StreamPage, state, at.Version(), err)
	}

	store.page = func(page []Envelope) []Envelope {
		if len(page) == 0 {
			return page
		}
		over := page[len(page)-1]
		over.Version++
		over.Position++
		return append(page, over)
	}
	state, at, err = repo.Load(context.Background(), accountID{tenant: "acme", number: "A-17"})
	if !errors.Is(err, ErrBackend) {
		t.Fatalf("a page one envelope longer than the bound the store itself publishes answered %v, so the two bounds only the store can apply are applied by nobody", err)
	}
	if state.Balance != 0 || at.Version() != 0 {
		t.Fatalf("the refused load answered %+v at version %d rather than the zero state and the zero token", state, at.Version())
	}
}

// A page whose versions are not the ones the read asked for folds to a state
// that is wrong at the right version, with the right count and no refusal
// anywhere — the worst observable this subsystem has, and the one the length
// check alone does not see.
func TestAMisPagedStreamIsRefusedBeforeItIsFolded(t *testing.T) {
	elsewhere := Stream{Family: "orders.order", Key: Compose("acme", "O-1")}
	acme := accountID{tenant: "acme", number: "A-17"}

	for _, one := range []struct {
		what string
		page func([]Envelope) []Envelope
	}{
		{"a page whose versions are reversed", func(page []Envelope) []Envelope {
			if len(page) < 2 {
				return page
			}
			page[0], page[len(page)-1] = page[len(page)-1], page[0]
			return page
		}},
		{"a page that does not begin at the version it was read from", func(page []Envelope) []Envelope {
			for offset := range page {
				page[offset].Version += 7
			}
			return page
		}},
		{"a page carrying another stream's event", func(page []Envelope) []Envelope {
			if len(page) == 0 {
				return page
			}
			page[0].Stream = elsewhere
			return page
		}},
	} {
		store, repo := aStreamOfThree(t)
		store.page = one.page
		state, at, err := repo.Load(context.Background(), acme)
		if !errors.Is(err, ErrBackend) {
			t.Fatalf("%s answered %v, so a store, a decorator or a statement that lost its ordering folds to a state that is wrong with the right version and no refusal anywhere", one.what, err)
		}
		if state.Balance != 0 || at.Version() != 0 {
			t.Fatalf("%s answered %+v at version %d rather than the zero state and the zero token", one.what, state, at.Version())
		}
	}
}

func TestALoadThatFailsMidStreamReturnsNothing(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	stream := Stream{Family: "accounts.account", Key: Compose("acme", "A-17")}
	ctx := context.Background()

	store, repo := aStreamOfThree(t)
	state, at, err := repo.Load(ctx, acme)
	if err != nil || state.Balance != 7 || at.Version() != 3 {
		t.Fatalf("the control load answered %+v at version %d (%v), so the case below cannot tell a refusal from a load that never folds anything", state, at.Version(), err)
	}

	store.streams[stream][1].Type = "accounts.retired"
	state, at, err = repo.Load(ctx, acme)
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("a stream holding a type this declaration does not know answered %v", err)
	}
	if state.Balance != 0 || state.Applied != nil || at.Version() != 0 {
		t.Fatalf("a load that failed on the second of three events answered %+v at version %d, so a caller that ignores the error decides on a state that is missing every fact after the one that broke", state, at.Version())
	}
}

// The framework holds no receiver that remembers an append, so what it does on
// a caller's behalf is exactly what the store was asked, and a conflict is the
// case that matters: re-appending a decision taken on a state that is now stale
// is the one recovery no framework can perform.
func TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()
	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)

	credit := func(reason string) Change[account] {
		return declared.credited.New(acme, creditedV2{Minor: 1, Reason: reason})
	}
	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the empty stream this case writes to was refused with %v", err)
	}
	if _, _, err := repo.Append(ctx, at, credit("one"), credit("two"), credit("three")); err != nil {
		t.Fatalf("the stream this case reads back could not be written: %v", err)
	}

	store.forget()
	_, at, err = repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("a load of a stream of three was refused with %v", err)
	}
	store.exactly(t, "a load of three events at a page of two", map[string]int{"Backing": 1, "Transaction": 1, "ReadStream": 2})

	store.forget()
	stale := at
	advanced, _, err := repo.Append(ctx, at, credit("four"))
	if err != nil {
		t.Fatalf("an append through the token that load minted was refused with %v", err)
	}
	store.exactly(t, "an append of one change", map[string]int{"Backing": 1, "Transaction": 1, "Append": 1})

	store.forget()
	answered, receipt, err := repo.Append(ctx, stale, credit("five"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("an append at a version the stream has moved past answered %v, so the case below counts the attempts of something that never conflicted", err)
	}
	if answered != stale || !receipt.Empty() {
		t.Fatalf("a conflict advanced the caller's token or produced a receipt for an append that did not land")
	}
	store.exactly(t, "an append the store refused as a conflict", map[string]int{"Backing": 1, "Transaction": 1, "Append": 1})

	store.forget()
	if _, _, err := repo.Append(ctx, advanced, credit("six")); err != nil {
		t.Fatalf("the first control append was refused with %v", err)
	}
	_, at, err = repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the control reload was refused with %v", err)
	}
	if _, _, err := repo.Append(ctx, at, credit("seven")); err != nil {
		t.Fatalf("the second control append was refused with %v", err)
	}
	store.exactly(t, "two appends and a load", map[string]int{"Backing": 3, "Transaction": 3, "ReadStream": 3, "Append": 2})
}

// Two identical changes are a normal history — two credits of the same amount,
// two identical lines — and it has to be said out loud, because it is what makes
// the accidental double append undetectable by any framework rule.
func TestOneAppendCarriesTwoIdenticalChanges(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()
	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)

	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the stream this case writes to was refused with %v", err)
	}
	deposit := declared.credited.New(acme, creditedV2{Minor: 5, Reason: "deposit"})

	store.forget()
	advanced, receipt, err := repo.Append(ctx, at, deposit, deposit)
	if err != nil {
		t.Fatalf("an append carrying the same fact twice was refused with %v, and equal facts are a normal history", err)
	}
	if receipt.Count() != 2 || receipt.First() != 1 || receipt.Last() != 2 || advanced.Version() != 2 {
		t.Fatalf("two identical changes were written as %d events from %d to %d, leaving the token at version %d",
			receipt.Count(), receipt.First(), receipt.Last(), advanced.Version())
	}
	if store.count("Append") != 1 {
		t.Fatalf("two changes reached the store in %d appends, and one append is one atomic unit", store.count("Append"))
	}

	state, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the stream those two facts were written to was refused with %v", err)
	}
	if state.Balance != 10 || at.Version() != 2 {
		t.Fatalf("two identical facts folded to a balance of %d at version %d, so a layer collapsed two events into one", state.Balance, at.Version())
	}

	_, receipt, err = repo.Append(ctx, at, deposit)
	if err != nil {
		t.Fatalf("the same fact appended a second time was refused with %v, and whether a repeat is legitimate is the domain's question", err)
	}
	if receipt.Count() != 1 {
		t.Fatalf("an append of one change answered a receipt of %d, so the count above is a constant rather than a measurement", receipt.Count())
	}
	state, _, err = repo.Load(ctx, acme)
	if err != nil || state.Balance != 15 {
		t.Fatalf("the same fact in two appends folded to a balance of %d (%v), so a framework layer deduplicated a repeat only the domain can judge", state.Balance, err)
	}

	if _, _, err := repo.Append(ctx, advanced, deposit); !errors.Is(err, ErrConflict) {
		t.Fatalf("an append through the token the first of the two decisions returned answered %v, and the second decision must not reuse the first's version", err)
	}
}

// The kernel is holding the payload bytes of an append while it assembles the
// records, so the bound on what one append may hold is the measured sum and not
// a product of two caps: a batch inside the count bound and inside the per-payload
// bound can still be a heap the process does not have.
func TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()

	notes := func(t *testing.T, store *recordingStore) (*Repo[carrier, accountID], *Fact[carrier, accountID, note], At[carrier]) {
		t.Helper()
		aggregate := Define[carrier]("notes.note", accountKey)
		written := Declare(aggregate, "notes.written", From[note](aliasCodec{}), carryNote)
		repo, err := Bind(Open(store), aggregate)
		if err != nil {
			t.Fatalf("the fixture store was refused at Bind: %v", err)
		}
		_, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the token this case appends through could not be minted: %v", err)
		}
		return repo, written, at
	}

	t.Run("one payload over the bound the store published", func(t *testing.T) {
		store := newRecordingStore(t)
		store.limits.MaxPayload = 64
		repo, written, at := notes(t, store)

		store.forget()
		answered, _, err := repo.Append(ctx, at, written.New(acme, note{Body: make([]byte, store.limits.MaxPayload+1)}))
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("a payload one byte over the bound the store published answered %v", err)
		}
		if store.made() != 0 || answered != at {
			t.Fatalf("a payload over the bound made %d calls on the store, so the bound is enforced by the store or by nobody", store.made())
		}

		store.forget()
		if _, _, err := repo.Append(ctx, at, written.New(acme, note{Body: make([]byte, store.limits.MaxPayload)})); err != nil {
			t.Fatalf("a payload of exactly the bound was refused with %v, so the case above passes against an append that refuses every payload", err)
		}
		if store.count("Append") != 1 {
			t.Fatalf("a payload of exactly the bound did not reach the store")
		}
	})

	t.Run("a batch whose bytes exceed what one append may hold", func(t *testing.T) {
		size := MaxResidentBytes/MaxBatchCount + 1
		store := newRecordingStore(t)
		store.limits.MaxPayload, store.limits.MaxBatch = size, MaxBatchCount
		repo, written, at := notes(t, store)

		one := written.New(acme, note{Body: make([]byte, size)})
		if err := one.Err(); err != nil {
			t.Fatalf("the change every row below repeats could not be decided: %v", err)
		}
		batch := make([]Change[carrier], MaxBatchCount)
		for index := range batch {
			batch[index] = one
		}
		if len(batch)*size <= MaxResidentBytes {
			t.Fatalf("%d payloads of %d bytes hold %d against a ceiling of %d, so the row below asserts nothing", len(batch), size, len(batch)*size, MaxResidentBytes)
		}
		if (len(batch)-1)*size > MaxResidentBytes {
			t.Fatalf("the control batch of %d payloads is over the ceiling too, so the two rows below cannot be told apart", len(batch)-1)
		}

		store.forget()
		answered, _, err := repo.Append(ctx, at, batch...)
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("an append holding %d bytes against a ceiling of %d answered %v", len(batch)*size, MaxResidentBytes, err)
		}
		if store.made() != 0 || answered != at {
			t.Fatalf("an append over what one append may hold made %d calls on the store", store.made())
		}

		store.forget()
		store.fail = Failure(Conflict, nil)
		if _, _, err := repo.Append(ctx, at, batch[:len(batch)-1]...); !errors.Is(err, ErrConflict) {
			t.Fatalf("a batch of %d payloads, which is inside what one append may hold, answered %v rather than reaching the store that refused it",
				len(batch)-1, err)
		}
		if store.count("Append") != 1 {
			t.Fatalf("a batch inside the ceiling did not reach the store, so the row above passes against an append that refuses every batch")
		}
	})
}

func aStreamOfThree(t *testing.T) (*recordingStore, *Repo[account, accountID]) {
	t.Helper()
	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()

	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the empty stream this case writes to was refused with %v", err)
	}
	if _, _, err := repo.Append(ctx, at,
		declared.credited.New(acme, creditedV2{Minor: 1, Reason: "one"}),
		declared.credited.New(acme, creditedV2{Minor: 2, Reason: "two"}),
		declared.credited.New(acme, creditedV2{Minor: 4, Reason: "four"})); err != nil {
		t.Fatalf("the stream this case reads back could not be written: %v", err)
	}
	return store, repo
}
