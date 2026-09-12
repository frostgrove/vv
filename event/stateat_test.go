package event

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func creditReason(version int64) string { return "r-" + strconv.FormatInt(version, 10) }

// A history written through the repository itself, so what the bounded reads
// below fold is what an append writes and not what a fixture invented: version
// v carries a credit of v minor units under the reason "r-v". The batches are
// the store's own MaxBatch, because a stream longer than one append is the only
// one a page boundary can fall inside.
func plantCredits(t *testing.T, repo *Repo[account, accountID], declared accountDeclaration, id accountID, events int) []Change[account] {
	t.Helper()
	ctx := context.Background()
	_, at, err := repo.Load(ctx, id)
	if err != nil {
		t.Fatalf("the empty stream these reads are bounded over was refused with %v", err)
	}
	var written []Change[account]
	for planted := 0; planted < events; {
		var batch []Change[account]
		for ; planted < events && len(batch) < repo.limits.MaxBatch; planted++ {
			version := int64(planted) + 1
			batch = append(batch, declared.credited.New(id, creditedV2{Minor: version, Reason: creditReason(version)}))
		}
		if at, _, err = repo.Append(ctx, at, batch...); err != nil {
			t.Fatalf("the history these reads are bounded over could not be written: %v", err)
		}
		written = append(written, batch...)
	}
	return written
}

func sameAccount(left, right account) bool {
	return left.Balance == right.Balance &&
		maps.Equal(left.Applied, right.Applied) &&
		slices.Equal(left.Tags, right.Tags)
}

func TestAPrefixFoldsToTheStateItsVersionHolds(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()
	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)

	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the empty stream this case writes to was refused with %v", err)
	}
	decided := []Change[account]{declared.opened.New(acme, opened{Owner: "acme"})}
	for version := int64(2); version <= 11; version++ {
		decided = append(decided, declared.credited.New(acme, creditedV2{Minor: version, Reason: creditReason(version)}))
	}
	decided = append(decided, declared.closed.New(acme, closed{Reason: "asked"}))
	if len(decided) != 12 {
		t.Fatalf("the stream this case reads prefixes of holds %d facts and the case is written for twelve", len(decided))
	}
	for written := 0; written < len(decided); written += repo.limits.MaxBatch {
		if at, _, err = repo.Append(ctx, at, decided[written:min(written+repo.limits.MaxBatch, len(decided))]...); err != nil {
			t.Fatalf("the history this case reads prefixes of could not be written: %v", err)
		}
	}

	t.Run("the prefix is the fold of the first seven facts, by the folds a load uses", func(t *testing.T) {
		state, err := repo.StateAt(ctx, acme, 7)
		if err != nil {
			t.Fatalf("a prefix of a twelve-fact stream was refused with %v", err)
		}
		want, err := declared.aggregate.Fold(acme, account{}, decided[:7]...)
		if err != nil {
			t.Fatalf("the declaration could not fold the first seven decisions this case compares against: %v", err)
		}
		if !sameAccount(state, want) {
			t.Fatalf("the state at version 7 is %+v where folding the first seven facts gives %+v, so a bounded read is not the fold of its prefix", state, want)
		}
		if state.Balance != 27 || len(state.Applied) != 6 || !slices.Equal(state.Tags, []string{"acme"}) {
			t.Fatalf("the state at version 7 is %+v, and the first seven facts are one opening and the credits of 2 through 7, which is a balance of 27 over six reasons", state)
		}
	})

	t.Run("the whole stream through the bound is the state a load answers", func(t *testing.T) {
		loaded, token, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the load this case is measured against was refused with %v", err)
		}
		if token.Version() != 12 {
			t.Fatalf("the stream loaded at version %d and the case wrote twelve facts", token.Version())
		}
		bounded, err := repo.StateAt(ctx, acme, 12)
		if err != nil {
			t.Fatalf("a bounded read at the version the stream is at was refused with %v", err)
		}
		if !sameAccount(bounded, loaded) {
			t.Fatalf("the bounded read at version 12 answered %+v and the load answered %+v, so reading a prefix that is the whole stream is not the same fold", bounded, loaded)
		}
	})

	t.Run("version 6 differs from version 7 by exactly the seventh fact's fold", func(t *testing.T) {
		sixth, err := repo.StateAt(ctx, acme, 6)
		if err != nil {
			t.Fatalf("the prefix at version 6 was refused with %v", err)
		}
		seventh, err := repo.StateAt(ctx, acme, 7)
		if err != nil {
			t.Fatalf("the prefix at version 7 was refused with %v", err)
		}
		if seventh.Balance-sixth.Balance != 7 {
			t.Fatalf("version 6 folded to a balance of %d and version 7 to %d; the seventh fact credits 7, so the bound is exclusive of the version asked for or it is not measured at all",
				sixth.Balance, seventh.Balance)
		}
		if !seventh.Applied[creditReason(7)] || sixth.Applied[creditReason(7)] {
			t.Fatalf("version 6 folded to %+v and version 7 to %+v, and the seventh fact is the only one between them", sixth, seventh)
		}
	})

	t.Run("a second read of one version costs the same reads as the first", func(t *testing.T) {
		store.forget()
		if _, err := repo.StateAt(ctx, acme, 7); err != nil {
			t.Fatalf("the first of two identical bounded reads was refused with %v", err)
		}
		first := store.count("ReadStream")
		store.forget()
		if _, err := repo.StateAt(ctx, acme, 7); err != nil {
			t.Fatalf("the second of two identical bounded reads was refused with %v", err)
		}
		if second := store.count("ReadStream"); first == 0 || second != first {
			t.Fatalf("one bounded read cost %d stream reads and the identical one after it cost %d, so something between two calls kept a fold", first, second)
		}
	})
}

func TestABoundedReadStopsAtThePageItNeeds(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()
	store := newRecordingStore(t)
	store.limits.StreamPage = 4
	repo, declared := bindAccounts(t, store)
	plantCredits(t, repo, declared, acme, 11)

	for _, bound := range []struct {
		version Version
		pages   int
		why     string
	}{
		{1, 1, "the first version of the first page reads that page and discards three of its four"},
		{4, 1, "the last version of the first page reads that page and discards nothing"},
		{5, 2, "the first version of the second page reads two pages and discards three, and a loop that read the whole stream would cost three"},
		{8, 2, "the last version of the second page reads two pages"},
		{11, 3, "the version the stream is at reads all three, the last of them short"},
	} {
		store.forget()
		state, err := repo.StateAt(ctx, acme, bound.version)
		if err != nil {
			t.Fatalf("the prefix at version %d was refused with %v", bound.version, err)
		}
		if pages := store.count("ReadStream"); pages != bound.pages {
			t.Fatalf("the prefix at version %d cost %d stream reads against the %d the bound needs — %s", bound.version, pages, bound.pages, bound.why)
		}
		var want int64
		for each := int64(1); each <= int64(bound.version); each++ {
			want += each
		}
		if state.Balance != want || len(state.Applied) != int(bound.version) {
			t.Fatalf("the prefix at version %d folded to %+v where the first %d credits sum to %d, so the page the bound falls inside is truncated at the wrong place",
				bound.version, state, bound.version, want)
		}
	}

	t.Run("the same reads through a load cost three pages each", func(t *testing.T) {
		for range 5 {
			store.forget()
			if _, _, err := repo.Load(ctx, acme); err != nil {
				t.Fatalf("the load the bounds above are measured against was refused with %v", err)
			}
			if pages := store.count("ReadStream"); pages != 3 {
				t.Fatalf("an unbounded load of eleven events at a page of %d cost %d stream reads rather than three, so the counts above are not evidence that the bound does work",
					store.limits.StreamPage, pages)
			}
		}
	})
}

func TestAVersionPastTheEndAndAnEmptyStreamAreOneRefusal(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	untouched := accountID{tenant: "acme", number: "A-99"}
	ctx := context.Background()
	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)
	plantCredits(t, repo, declared, acme, 5)

	store.forget()
	state, pastTheEnd := repo.StateAt(ctx, acme, 9)
	reads := store.count("ReadStream")
	if !errors.Is(pastTheEnd, ErrVersion) {
		t.Fatalf("a bound past the end of a five-fact stream answered %v, and the version a caller asked for is data only the caller can correct", pastTheEnd)
	}
	if !sameAccount(state, account{}) {
		t.Fatalf("a bound past the end answered %+v, so a caller that ignores the error decides on the head state under the name of a historical one", state)
	}
	if reads != 3 {
		t.Fatalf("a bound past the end of five facts at a page of %d cost %d stream reads against the three the same load costs, so the refusal cost a confirming read",
			store.limits.StreamPage, reads)
	}

	store.forget()
	empty, neverWritten := repo.StateAt(ctx, untouched, 1)
	if !errors.Is(neverWritten, ErrVersion) {
		t.Fatalf("a bounded read of a stream nothing was ever appended to answered %v", neverWritten)
	}
	if !sameAccount(empty, account{}) {
		t.Fatalf("a bounded read of an empty stream answered %+v beside a refusal", empty)
	}
	if pastTheEnd.Error() != neverWritten.Error() {
		t.Fatalf("a bound past the end reads %q and an empty stream reads %q; an append-only log tells a stream that is empty and a stream that never existed apart nowhere a reader can see, so the two must not be told apart here either",
			pastTheEnd, neverWritten)
	}

	store.forget()
	zero, atVersionZero := repo.StateAt(ctx, acme, 0)
	if !errors.Is(atVersionZero, ErrVersion) {
		t.Fatalf("a bound of version zero answered %v rather than the refusal that names the caller's own off-by-one", atVersionZero)
	}
	if !sameAccount(zero, account{}) {
		t.Fatalf("a bound of version zero answered %+v beside a refusal, which is the answer that hides the off-by-one rather than reporting it", zero)
	}
	if made := store.made(); made != 0 {
		t.Fatalf("a bound of version zero issued %d store calls, and a version a caller cannot have meant is knowable before anything is asked of a store", made)
	}

	t.Run("the bound the stream does hold is the control", func(t *testing.T) {
		store.forget()
		state, err := repo.StateAt(ctx, acme, 5)
		if err != nil {
			t.Fatalf("the version the five-fact stream is at was refused with %v, so every arm above refuses everything", err)
		}
		if state.Balance != 1+2+3+4+5 {
			t.Fatalf("the prefix at version 5 folded to %+v where five credits sum to 15", state)
		}
	})
}

func TestAnUnreadableEventInThePrefixReturnsTheZeroState(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	stream := accountsAt("acme", "A-17")
	ctx := context.Background()
	bound := 64
	oversized := []byte(`{"Minor":5,"Reason":"` + strings.Repeat("f", bound) + `"}`)
	if len(oversized) <= bound {
		t.Fatalf("the recorded payload one row below uses is %d bytes against a bound of %d, so that row says nothing about the cap", len(oversized), bound)
	}

	refusingUpcaster := func(t *testing.T, store *recordingStore) *Repo[account, accountID] {
		t.Helper()
		aggregate := Define[account]("accounts.account", accountKey)
		Declare(aggregate, "accounts.credited",
			Then(From(JSON[creditedV1]()), JSON[creditedV2](), func(creditedV1) (creditedV2, error) {
				return creditedV2{}, errUnreadableShape
			}), creditAccount)
		repo, err := Bind(Open(store), aggregate)
		if err != nil {
			t.Fatalf("the declaration whose upcaster refuses was not bound: %v", err)
		}
		return repo
	}

	for _, one := range []struct {
		what   string
		broken Record
		want   error
		bind   func(*testing.T, *recordingStore) *Repo[account, accountID]
	}{
		{"a wire type this declaration does not know", Record{Type: "accounts.retired", Revision: 1, Payload: []byte(`{}`)}, ErrUnknownType, nil},
		{"a revision this fact does not retain", Record{Type: "accounts.credited", Revision: 3, Payload: []byte(`{"Minor":5,"Reason":"broken"}`)}, ErrRevision, nil},
		{"an upcaster that refuses the recorded payload", Record{Type: "accounts.credited", Revision: 1, Payload: []byte(`{"Minor":5}`)}, ErrUpcast, refusingUpcaster},
		{"a recorded payload over the bound the store published", Record{Type: "accounts.credited", Revision: 2, Payload: oversized}, ErrPayload, nil},
	} {
		store := newRecordingStore(t)
		store.limits.MaxPayload = bound
		repo, _ := bindAccounts(t, store)
		if one.bind != nil {
			repo = one.bind(t, store)
		}

		history := make([]Record, 0, 9)
		for version := int64(1); version <= 9; version++ {
			if version == 4 {
				history = append(history, one.broken)
				continue
			}
			history = append(history, Record{Type: "accounts.credited", Revision: 1,
				Payload: []byte(`{"Minor":` + strconv.FormatInt(version, 10) + `}`)})
		}
		store.history(stream, history...)

		bounded, refused := repo.StateAt(ctx, acme, 9)
		if !errors.Is(refused, one.want) {
			t.Fatalf("%s at version 4 of a nine-fact prefix answered %v where the party that refuses it raises %v", one.what, refused, one.want)
		}
		if !sameAccount(bounded, account{}) {
			t.Fatalf("%s answered %+v, and the accumulator holding versions 1 to 3 is the partial state a bounded read may never return", one.what, bounded)
		}

		loaded, _, throughALoad := repo.Load(ctx, acme)
		if !errors.Is(throughALoad, one.want) || throughALoad.Error() != refused.Error() {
			t.Fatalf("%s read to the bound answered %q and read through a load answered %q, so the bounded read carries a failure path of its own rather than the one a load already has",
				one.what, refused, throughALoad)
		}
		if !sameAccount(loaded, account{}) {
			t.Fatalf("%s through a load answered %+v, so the comparison above is between two wrong answers", one.what, loaded)
		}
	}

	t.Run("the same prefix with nothing broken in it is the control", func(t *testing.T) {
		store := newRecordingStore(t)
		store.limits.MaxPayload = bound
		repo, _ := bindAccounts(t, store)
		history := make([]Record, 0, 9)
		for version := int64(1); version <= 9; version++ {
			history = append(history, Record{Type: "accounts.credited", Revision: 1,
				Payload: []byte(`{"Minor":` + strconv.FormatInt(version, 10) + `}`)})
		}
		store.history(stream, history...)

		state, err := repo.StateAt(ctx, acme, 9)
		if err != nil || state.Balance != 45 {
			t.Fatalf("a nine-fact prefix this declaration reads folded to %+v (%v), so every row above passes against a bounded read that refuses everything", state, err)
		}
	})
}

func TestAPageOutOfOrderIsRefusedBeforeItIsTruncated(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()
	reorder := func(order ...int) func([]Envelope) []Envelope {
		return func(page []Envelope) []Envelope {
			if len(page) != len(order) {
				return page
			}
			shuffled := make([]Envelope, 0, len(page))
			for _, at := range order {
				shuffled = append(shuffled, page[at])
			}
			return shuffled
		}
	}

	for _, one := range []struct {
		what  string
		order []int
		bound Version
	}{
		{"a page whose disorder falls inside the bound", []int{2, 0, 1}, 2},
		{"a page whose disorder falls only in the part the bound discards", []int{0, 2, 1}, 1},
	} {
		store := newRecordingStore(t)
		store.limits.StreamPage = 3
		repo, declared := bindAccounts(t, store)
		plantCredits(t, repo, declared, acme, 3)
		store.page = reorder(one.order...)

		state, err := repo.StateAt(ctx, acme, one.bound)
		if !errors.Is(err, ErrBackend) {
			t.Fatalf("%s answered %v at a bound of %d; a page that does not begin where it was read from and rise by one folds to a state that is wrong at the right version, with the right count and no refusal anywhere",
				one.what, err, one.bound)
		}
		if !sameAccount(state, account{}) {
			t.Fatalf("%s answered %+v beside a refusal", one.what, state)
		}

		store.page = nil
		accepted, err := repo.StateAt(ctx, acme, one.bound)
		if err != nil {
			t.Fatalf("%s, with the same store answering the same page in order, was refused with %v", one.what, err)
		}
		var want int64
		for each := int64(1); each <= int64(one.bound); each++ {
			want += each
		}
		if accepted.Balance != want {
			t.Fatalf("%s, in order, folded to %+v where the first %d credits sum to %d", one.what, accepted, one.bound, want)
		}
	}
}

func TestABoundedReadYieldsNothingThatCanAppend(t *testing.T) {
	store := newRecordingStore(t)
	repo, _ := bindAccounts(t, store)
	token := reflect.TypeFor[At[account]]()

	bounded := reflect.ValueOf(repo.StateAt).Type()
	if bounded.NumOut() != 2 {
		t.Fatalf("StateAt returns %d values and it returns a state and an error; a third would be a value a caller reads as a load token and decides against a history this call left out", bounded.NumOut())
	}
	if bounded.Out(0) != reflect.TypeFor[account]() || bounded.Out(1) != reflect.TypeFor[error]() {
		t.Fatalf("StateAt returns (%s, %s) rather than the state and an error", bounded.Out(0), bounded.Out(1))
	}
	for out := range bounded.NumOut() {
		if bounded.Out(out) == token {
			t.Fatalf("StateAt returns a %s, and Append takes exactly that value — a historical read that yields one invites a load, a decision and an append made against the events the bound left out", token)
		}
	}
	if bounded.NumIn() != 3 || bounded.In(2) != reflect.TypeFor[Version]() {
		t.Fatalf("StateAt takes %d arguments ending in a %s, and a bound is an exact version — a predicate there is a query language over history", bounded.NumIn(), bounded.In(bounded.NumIn()-1))
	}

	loading := reflect.ValueOf(repo.Load).Type()
	if loading.NumOut() != 3 || loading.Out(1) != token {
		t.Fatalf("Load returns %d values with a %s second, so the absence above is a property of the type rather than of the bounded read", loading.NumOut(), loading.Out(1))
	}
}
