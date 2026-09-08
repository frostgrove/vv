package event

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var (
	errUnwritableValue = errors.New("this value is not one this codec can write")
	errUnreadableBytes = errors.New("these bytes are not ones this codec can read")
)

type encodeRefuses struct{ Codec[opened] }

func (encodeRefuses) Encode(opened) ([]byte, error) { return nil, errUnwritableValue }

type decodeRefuses struct{ Codec[opened] }

func (decodeRefuses) Decode([]byte) (opened, error) { return opened{}, errUnreadableBytes }

func accountsAt(tenant, number string) Stream {
	return Stream{Family: "accounts.account", Key: Compose(tenant, number)}
}

func TestAStreamWithHistoryFoldsToItsCurrentState(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	other := accountID{tenant: "acme", number: "A-99"}
	ctx := context.Background()

	write := func(t *testing.T, repo *Repo[account, accountID], declared accountDeclaration, id accountID) {
		t.Helper()
		_, at, err := repo.Load(ctx, id)
		if err != nil {
			t.Fatalf("the empty stream this case writes to was refused with %v", err)
		}
		if _, _, err := repo.Append(ctx, at,
			declared.opened.New(id, opened{Owner: "acme"}),
			declared.credited.New(id, creditedV2{Minor: 5, Reason: "one"}),
			declared.credited.New(id, creditedV2{Minor: 2, Reason: "two"}),
			declared.closed.New(id, closed{Reason: "asked"})); err != nil {
			t.Fatalf("the history this case folds could not be written: %v", err)
		}
	}

	t.Run("the state is the fold of the whole stream, at the version the stream is at", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, declared := bindAccounts(t, store)
		write(t, repo, declared, acme)

		store.forget()
		state, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("a stream of four facts was refused with %v", err)
		}
		if at.Version() != 4 {
			t.Fatalf("a stream of four facts loaded at version %d, so the next append names a version the stream is not at", at.Version())
		}
		if state.Balance != 7 || !maps.Equal(state.Applied, map[string]bool{"one": true, "two": true}) || !slices.Equal(state.Tags, []string{"acme"}) {
			t.Fatalf("four facts folded to %+v, and the state of an aggregate is the fold of its stream and nothing else", state)
		}
		if reads := store.count("ReadStream"); reads != 3 {
			t.Fatalf("four events at a page of %d were read in %d reads, so the whole stream was materialised at once or the loop stopped early",
				store.limits.StreamPage, reads)
		}
	})

	t.Run("the same stream folds to the same state one event at a time", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, declared := bindAccounts(t, store)
		write(t, repo, declared, acme)

		paged, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the load this case is measured against was refused with %v", err)
		}
		page := store.limits.StreamPage
		store.limits.StreamPage = 1
		single, err := Bind(Open(store), declared.aggregate)
		if err != nil {
			t.Fatalf("a repository over a store that pages one envelope at a time was refused at Bind: %v", err)
		}
		store.forget()
		state, oneAtATime, err := single.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the same stream read one envelope at a time was refused with %v", err)
		}
		if reads := store.count("ReadStream"); reads != 5 {
			t.Fatalf("four events at a page of 1 were read in %d reads, so the two loads compared below were not paged differently", reads)
		}
		if oneAtATime.Version() != at.Version() || state.Balance != paged.Balance || !maps.Equal(state.Applied, paged.Applied) {
			t.Fatalf("one stream folded to %+v at version %d in pages of 1 and to %+v at version %d in pages of %d, so a page boundary drops, duplicates or reorders an event",
				state, oneAtATime.Version(), paged, at.Version(), page)
		}
	})

	t.Run("an older revision is read by its own codec and carried to the current type", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, declared := bindAccounts(t, store)
		store.history(accountsAt("acme", "A-17"), Record{Type: "accounts.credited", Revision: 1, Payload: []byte(`{"Minor":5}`)})

		_, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("a stream written at an older revision was refused with %v", err)
		}
		if _, _, err := repo.Append(ctx, at, declared.credited.New(acme, creditedV2{Minor: 2, Reason: "two"})); err != nil {
			t.Fatalf("the current revision could not be appended after an older one: %v", err)
		}
		mixed, mixedAt, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("a stream mixing two revisions was refused with %v", err)
		}

		_, at, err = repo.Load(ctx, other)
		if err != nil {
			t.Fatalf("the stream the comparison is against was refused with %v", err)
		}
		if _, _, err := repo.Append(ctx, at,
			declared.credited.New(other, creditedV2{Minor: 5, Reason: "migrated"}),
			declared.credited.New(other, creditedV2{Minor: 2, Reason: "two"})); err != nil {
			t.Fatalf("the equivalent stream at the current revision could not be written: %v", err)
		}
		current, currentAt, err := repo.Load(ctx, other)
		if err != nil {
			t.Fatalf("the equivalent stream at the current revision was refused with %v", err)
		}

		if mixed.Balance != current.Balance || !maps.Equal(mixed.Applied, current.Applied) || mixedAt.Version() != currentAt.Version() {
			t.Fatalf("a stream mixing revisions 1 and 2 folded to %+v and the equivalent revision-2 stream to %+v, so an older revision is read by the current codec rather than by its own and the upcaster that carries it",
				mixed, current)
		}
		if !mixed.Applied["migrated"] {
			t.Fatalf("the revision-1 fact folded as %+v, and the value the declared upcaster produces is what a fold of it must see", mixed)
		}
	})

	t.Run("a reload is the authority and the state a caller holds is not", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, declared := bindAccounts(t, store)
		write(t, repo, declared, acme)

		state, _, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the first load was refused with %v", err)
		}
		state.Balance = 1000
		state.Applied["invented"] = true
		state.Tags = append(state.Tags, "invented")

		again, _, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the second load was refused with %v", err)
		}
		if again.Balance != 7 || again.Applied["invented"] || slices.Contains(again.Tags, "invented") {
			t.Fatalf("a reload answered %+v after the caller wrote into the state it had been handed, so a replay is not the only authority and something the framework kept is", again)
		}
	})

	t.Run("the fold line between two decisions is load-bearing", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, declared := bindAccounts(t, store)
		credit := func(id accountID, state account, reason string) []Change[account] {
			if state.Applied[reason] {
				return nil
			}
			return []Change[account]{declared.credited.New(id, creditedV2{Minor: 5, Reason: reason})}
		}

		state, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the load this operation decides from was refused with %v", err)
		}
		first := credit(acme, state, "deposit")
		at, _, err = repo.Append(ctx, at, first...)
		if err != nil {
			t.Fatalf("the first of two decisions was refused with %v", err)
		}
		state, err = declared.aggregate.Fold(acme, state, first...)
		if err != nil {
			t.Fatalf("the fold between the two decisions was refused with %v", err)
		}
		if _, _, err := repo.Append(ctx, at, credit(acme, state, "deposit")...); err != nil {
			t.Fatalf("the second decision was refused with %v", err)
		}
		folded, _, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the stream the two decisions wrote was refused with %v", err)
		}
		if folded.Balance != 5 {
			t.Fatalf("an operation that folded between its two decisions left a balance of %d, so the second decision did not see the first one's fact", folded.Balance)
		}

		state, at, err = repo.Load(ctx, other)
		if err != nil {
			t.Fatalf("the load the control decides from was refused with %v", err)
		}
		first = credit(other, state, "deposit")
		at, _, err = repo.Append(ctx, at, first...)
		if err != nil {
			t.Fatalf("the control's first decision was refused with %v", err)
		}
		if _, _, err := repo.Append(ctx, at, credit(other, state, "deposit")...); err != nil {
			t.Fatalf("the control's second decision was refused with %v", err)
		}
		unfolded, _, err := repo.Load(ctx, other)
		if err != nil {
			t.Fatalf("the stream the control wrote was refused with %v", err)
		}
		if unfolded.Balance != 10 {
			t.Fatalf("the same operation with the fold line removed left a balance of %d rather than the doubled one, so the assertion above passes whether or not the line is there",
				unfolded.Balance)
		}
	})
}

// Every refusal a stored fact can raise, raised by the party the sentinel names
// and carrying no data of the store's: a type name that breaks the kernel's own
// identifier rule names no declared fact and can name none, so it is refused the
// same way a legal unknown one is — and it does not travel, which is what keeps
// a megabyte of it out of a log line.
func TestEveryHistoryClassRefusalIsRaisedByTheThingItNames(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	stream := accountsAt("acme", "A-17")
	ctx := context.Background()
	secret := "a-payload-fragment-nobody-may-see"
	bound := 64
	oversized := []byte(`{"Reason":"` + strings.Repeat("f", bound) + `"}`)
	if len(oversized) <= bound {
		t.Fatalf("the recorded payload the rows below use is %d bytes against a bound of %d, so nothing about the cap is under test", len(oversized), bound)
	}

	for _, one := range []struct {
		what     string
		record   Record
		want     error
		unspoken string
	}{
		{"a wire type this declaration does not know", Record{Type: "accounts.retired", Revision: 1, Payload: []byte(`{}`)}, ErrUnknownType, ""},
		{"a wire type name longer than a declared identifier may be",
			Record{Type: strings.Repeat("f", MaxNameBytes+1), Revision: 1, Payload: []byte(`{}`)}, ErrUnknownType, strings.Repeat("f", MaxNameBytes+1)},
		{"a wire type name carrying a control character",
			Record{Type: "accounts.\nopened", Revision: 1, Payload: []byte(`{}`)}, ErrUnknownType, "accounts.\nopened"},
		{"a wire type name that is not valid UTF-8",
			Record{Type: "accounts.\xffopened", Revision: 1, Payload: []byte(`{}`)}, ErrUnknownType, "accounts.\xffopened"},
		{"a wire type name that closes the field the stream beside it is rendered in",
			Record{Type: "accounts.opened] admin logged in [stream x", Revision: 1, Payload: []byte(`{}`)}, ErrUnknownType,
			"accounts.opened] admin logged in [stream x"},
		{"a revision below the first this fact retains",
			Record{Type: "accounts.credited", Revision: 0, Payload: []byte(`{"Minor":5}`)}, ErrRevision, ""},
		{"a revision above the last this fact retains",
			Record{Type: "accounts.credited", Revision: 3, Payload: []byte(`{"Minor":5}`)}, ErrRevision, ""},
		{"bytes this revision's codec cannot read",
			Record{Type: "accounts.credited", Revision: 2, Payload: []byte(`{"Minor":"` + secret + `"}`)}, ErrPayload, secret},
		{"a recorded payload over the bound the store published",
			Record{Type: "accounts.credited", Revision: 2, Payload: oversized}, ErrPayload, ""},
	} {
		store := newRecordingStore(t)
		store.limits.MaxPayload = bound
		repo, _ := bindAccounts(t, store)
		store.history(stream, one.record)

		state, at, err := repo.Load(ctx, acme)
		if !errors.Is(err, one.want) {
			t.Fatalf("%s answered %v where the party that refuses it raises %v", one.what, err, one.want)
		}
		if state.Balance != 0 || state.Applied != nil || at.Version() != 0 || at.Stream() != (Stream{}) {
			t.Fatalf("%s answered %+v at version %d, so a caller that ignores the error decides on a state a fact it cannot read is missing from", one.what, state, at.Version())
		}
		if escaped := strconv.Quote(one.unspoken); one.unspoken != "" &&
			(strings.Contains(err.Error(), one.unspoken) || strings.Contains(err.Error(), escaped[1:len(escaped)-1])) {
			t.Fatalf("%s rendered the store's own data in %q, and a refusal names the rule that was broken and never the value that broke it", one.what, err)
		}
	}

	t.Run("a recorded payload over the cap is not a caller's to correct", func(t *testing.T) {
		store := newRecordingStore(t)
		store.limits.MaxPayload = bound
		repo, _ := bindAccounts(t, store)
		store.history(stream, Record{Type: "accounts.credited", Revision: 2, Payload: oversized})

		if _, _, err := repo.Load(ctx, acme); errors.Is(err, ErrTooLarge) {
			t.Fatalf("a stored payload over the cap answered %v, which states an obligation — send different data — that the caller of a read cannot discharge", err)
		}
	})

	t.Run("a recorded payload of exactly the bound is read", func(t *testing.T) {
		atTheBound := []byte(`{"Minor":5,"Reason":"` + strings.Repeat("f", bound-len(`{"Minor":5,"Reason":""}`)) + `"}`)
		if len(atTheBound) != bound {
			t.Fatalf("the control record is %d bytes against a bound of %d, so it says nothing about where the cap falls", len(atTheBound), bound)
		}
		store := newRecordingStore(t)
		store.limits.MaxPayload = bound
		repo, _ := bindAccounts(t, store)
		store.history(stream, Record{Type: "accounts.credited", Revision: 2, Payload: atTheBound})

		state, at, err := repo.Load(ctx, acme)
		if err != nil || state.Balance != 5 || at.Version() != 1 {
			t.Fatalf("a recorded payload of exactly the %d bytes the store publishes answered %v at version %d; an append admits that payload, so the two ends of one bound would disagree and a stream written legally could never be read again",
				bound, err, at.Version())
		}
	})

	t.Run("an upcaster that refuses hands the caller its own error", func(t *testing.T) {
		store := newRecordingStore(t)
		aggregate := Define[account]("accounts.account", accountKey)
		Declare(aggregate, "accounts.credited",
			Then(From(JSON[creditedV1]()), JSON[creditedV2](), func(creditedV1) (creditedV2, error) {
				return creditedV2{}, errUnreadableShape
			}), creditAccount)
		repo, err := Bind(Open(store), aggregate)
		if err != nil {
			t.Fatalf("the declaration whose upcaster refuses was not bound: %v", err)
		}
		store.history(stream, Record{Type: "accounts.credited", Revision: 1, Payload: []byte(`{"Minor":5}`)})

		state, at, err := repo.Load(ctx, acme)
		if !errors.Is(err, ErrUpcast) || !errors.Is(err, errUnreadableShape) {
			t.Fatalf("an upcaster's refusal reached the caller as %v, and the application's own error is what says which stored value it was about", err)
		}
		if state.Balance != 0 || at.Version() != 0 {
			t.Fatalf("a load an upcaster refused answered %+v at version %d", state, at.Version())
		}
	})

	t.Run("the stored fact this declaration can read is the control", func(t *testing.T) {
		store := newRecordingStore(t)
		store.limits.MaxPayload = bound
		repo, _ := bindAccounts(t, store)
		store.history(stream, Record{Type: "accounts.credited", Revision: 2, Payload: []byte(`{"Minor":5,"Reason":"one"}`)})

		state, at, err := repo.Load(ctx, acme)
		if err != nil || state.Balance != 5 || at.Version() != 1 {
			t.Fatalf("a stored fact this declaration reads folded to %+v at version %d (%v), so every row above passes against a load that refuses everything", state, at.Version(), err)
		}
	})
}

// A fold has no error channel for a panic to be a second spelling of, so a
// recovered one would have to be reported as a refusal the contract denies —
// and the deterministic bug that panics on every load, in every process, would
// arrive as a per-request refusal a retry loop hammers. An upcaster is the one
// callback whose subject is data, and it is recovered.
func TestAFoldPanicUnwindsOutOfLoad(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()
	stream := Stream{Family: "ledgers.ledger", Key: Compose("acme", "A-17")}
	stored := Record{Type: "ledgers.credited", Revision: 1, Payload: []byte(`{"Minor":250,"Reason":"deposit"}`)}

	store := newRecordingStore(t)
	unguarded := Define[ledger]("ledgers.ledger", accountKey)
	Declare(unguarded, "ledgers.credited", From(JSON[creditedV2]()),
		func(this ledger, event creditedV2) ledger {
			this[event.Reason] += event.Minor
			return this
		})
	repo, err := Bind(Open(store), unguarded)
	if err != nil {
		t.Fatalf("the declaration whose fold panics was not bound: %v", err)
	}
	store.history(stream, stored)

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _, _ = repo.Load(ctx, acme)
	}()
	if recovered == nil {
		t.Fatal("a fold that writes into a nil map was recovered out of Load, so an application's own bug arrives as a framework refusal a retry loop will hammer")
	}
	if failed, isError := recovered.(error); isError {
		for _, sentinel := range vocabulary() {
			if errors.Is(failed, sentinel) {
				t.Fatalf("a fold's panic was reported as %v, which puts a sentinel to work outside its class", failed)
			}
		}
	}

	guarded := Define[ledger]("ledgers.ledger", accountKey)
	Declare(guarded, "ledgers.credited", From(JSON[creditedV2]()),
		func(this ledger, event creditedV2) ledger {
			if this == nil {
				this = ledger{}
			}
			this[event.Reason] += event.Minor
			return this
		})
	safe, err := Bind(Open(store), guarded)
	if err != nil {
		t.Fatalf("the declaration whose fold guards its own state was not bound: %v", err)
	}
	state, at, err := safe.Load(ctx, acme)
	if err != nil || state["deposit"] != 250 || at.Version() != 1 {
		t.Fatalf("the same stream under a fold that guards its state answered %v at version %d (%v), so the case above passes by breaking every load", state, at.Version(), err)
	}

	upcasting := Define[account]("accounts.account", accountKey)
	Declare(upcasting, "accounts.credited",
		Then(From(JSON[creditedV1]()), JSON[creditedV2](), func(creditedV1) (creditedV2, error) {
			panic(errUnreadableShape)
		}), creditAccount)
	carried, err := Bind(Open(store), upcasting)
	if err != nil {
		t.Fatalf("the declaration whose upcaster panics was not bound: %v", err)
	}
	store.history(accountsAt("acme", "A-17"), Record{Type: "accounts.credited", Revision: 1, Payload: []byte(`{"Minor":5}`)})

	folded, token, err := carried.Load(ctx, acme)
	if !errors.Is(err, ErrUpcast) {
		t.Fatalf("an upcaster's panic out of Load answered %v rather than the refusal its error would have been, so the two callbacks are treated alike and one of them wrongly", err)
	}
	if errors.Is(err, errUnreadableShape) || CauseOf(err) != nil {
		t.Fatalf("a recovered panic value travelled as an application error in %v", err)
	}
	if folded.Balance != 0 || token.Version() != 0 {
		t.Fatalf("a load an upcaster's panic ended answered %+v at version %d", folded, token.Version())
	}
}

func TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	stream := accountsAt("acme", "A-17")
	ctx := context.Background()

	bind := func(t *testing.T, store *recordingStore, codec Codec[opened]) (*Repo[account, accountID], *Fact[account, accountID, opened]) {
		t.Helper()
		aggregate := Define[account]("accounts.account", accountKey)
		opening := Declare(aggregate, "accounts.opened", From(codec), openAccount)
		repo, err := Bind(Open(store), aggregate)
		if err != nil {
			t.Fatalf("the declaration under test was not bound: %v", err)
		}
		return repo, opening
	}

	t.Run("a decoder that panics is the refusal its own error would have been, out of Load", func(t *testing.T) {
		for _, one := range []struct {
			what  string
			codec Codec[opened]
			cause error
		}{
			{"a decoder that panics", panickingDecode{JSON[opened]()}, nil},
			{"a decoder that returns its refusal", decodeRefuses{JSON[opened]()}, errUnreadableBytes},
		} {
			store := newRecordingStore(t)
			repo, _ := bind(t, store, one.codec)
			store.history(stream, Record{Type: "accounts.opened", Revision: 1, Payload: []byte(`{"Owner":"acme"}`)})

			state, at, err := repo.Load(ctx, acme)
			if !errors.Is(err, ErrPayload) {
				t.Fatalf("%s answered %v out of a load, and the recorded bytes are what cannot be read", one.what, err)
			}
			if CauseOf(err) != one.cause {
				t.Fatalf("%s reached the caller carrying the cause %v where %v is what the codec said", one.what, CauseOf(err), one.cause)
			}
			if state.Tags != nil || at.Version() != 0 {
				t.Fatalf("%s answered %+v at version %d rather than the zero state and the zero token", one.what, state, at.Version())
			}
		}
	})

	t.Run("an encoder that panics is the refusal its own error would have been, and Append surfaces it", func(t *testing.T) {
		for _, one := range []struct {
			what  string
			codec Codec[opened]
			cause error
		}{
			{"an encoder that panics", panickingEncode{JSON[opened]()}, nil},
			{"an encoder that returns its refusal", encodeRefuses{JSON[opened]()}, errUnwritableValue},
		} {
			store := newRecordingStore(t)
			repo, opening := bind(t, store, one.codec)
			_, at, err := repo.Load(ctx, acme)
			if err != nil {
				t.Fatalf("%s: the token this row appends through could not be minted: %v", one.what, err)
			}

			change := opening.New(acme, opened{Owner: "acme"})
			if !errors.Is(change.Err(), ErrEncode) {
				t.Fatalf("%s left the change carrying %v, so a decision nobody can encode is minted as a fact", one.what, change.Err())
			}
			if CauseOf(change.Err()) != one.cause {
				t.Fatalf("%s minted a change carrying the cause %v where %v is what the codec said", one.what, CauseOf(change.Err()), one.cause)
			}
			store.forget()
			answered, _, err := repo.Append(ctx, at, change)
			if !errors.Is(err, ErrEncode) {
				t.Fatalf("%s reached the caller as %v from an append", one.what, err)
			}
			if store.made() != 0 || answered != at {
				t.Fatalf("%s made %d calls on the store and answered a token other than the one it was given", one.what, store.made())
			}
		}
	})

	t.Run("a codec that panics when it is asked is the declaration refusal its own error would have been", func(t *testing.T) {
		for _, one := range []struct {
			what  string
			codec Codec[opened]
		}{
			{"a codec that panics when it is asked", panickingCodec{}},
			{"a codec that returns its refusal when it is asked", refusingCodec{answer: errUnwritableValue}},
		} {
			aggregate := Define[account]("accounts.account", accountKey)
			panicked := recoverDeclaration(t, one.what, func() {
				Declare(aggregate, "accounts.opened", From(one.codec), openAccount)
			})
			if !errors.Is(panicked, ErrCodecType) {
				t.Fatalf("%s answered %v rather than the refusal that names a codec which cannot encode its own reader type", one.what, panicked)
			}
		}
	})

	t.Run("the shipped codec is the control", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, opening := bind(t, store, JSON[opened]())
		_, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the control load was refused with %v", err)
		}
		if _, _, err := repo.Append(ctx, at, opening.New(acme, opened{Owner: "acme"})); err != nil {
			t.Fatalf("the control append was refused with %v", err)
		}
		state, at, err := repo.Load(ctx, acme)
		if err != nil || len(state.Tags) != 1 || at.Version() != 1 {
			t.Fatalf("the same declaration over the shipped codec folded to %+v at version %d (%v), so every case above passes against a repository that refuses everything",
				state, at.Version(), err)
		}
	})
}

// The bytes and the identifiers a load reads belong to a store, and a store's
// are whatever an older deploy, a hand-edited row or a foreign writer left
// there. Whatever they are, the load folds them or refuses them whole: there is
// no third answer, and no refusal hands back what it had folded so far.
func FuzzALoadFoldsAStoredStreamOrRefusesItWhole(f *testing.F) {
	for _, seed := range []struct {
		name     string
		revision int
		payload  string
	}{
		{"accounts.credited", 2, `{"Minor":250,"Reason":"deposit"}`},
		{"accounts.credited", 1, `{"Minor":250}`},
		{"accounts.opened", 1, `{"Owner":"acme"}`},
		{"accounts.retired", 1, `{}`},
		{"accounts.credited", 3, `{"Minor":1}`},
		{"accounts.credited", 0, `{"Minor":1}`},
		{"accounts.credited", 2, `[1,2,3]`},
		{"accounts.credited", 2, ""},
		{"accounts.credited", 2, "\xff\xfe"},
		{"", 1, `{}`},
		{"accounts.\x00opened", 1, `{}`},
		{strings.Repeat("f", MaxNameBytes+1), 1, `{}`},
		{"accounts.credited", 2, `{"Reason":"` + strings.Repeat("f", 8<<10) + `"}`},
	} {
		f.Add(seed.name, seed.revision, []byte(seed.payload))
	}

	acme := accountID{tenant: "acme", number: "A-17"}
	stream := accountsAt("acme", "A-17")
	ctx := context.Background()
	history := []error{ErrUnknownType, ErrRevision, ErrPayload, ErrUpcast}

	f.Fuzz(func(t *testing.T, name string, revision int, payload []byte) {
		store := newRecordingStore(t)
		repo, _ := bindAccounts(t, store)
		store.history(stream,
			Record{Type: "accounts.opened", Revision: 1, Payload: []byte(`{"Owner":"acme"}`)},
			Record{Type: name, Revision: revision, Payload: payload})

		state, at, err := repo.Load(ctx, acme)
		if err != nil {
			if !slices.ContainsFunc(history, func(sentinel error) bool { return errors.Is(err, sentinel) }) {
				t.Fatalf("a stored fact of type %d bytes at revision %d was refused with %v, which is none of the four refusals a recorded fact this declaration cannot read is allowed to be",
					len(name), revision, err)
			}
			if state.Balance != 0 || state.Applied != nil || state.Tags != nil || at.Version() != 0 {
				t.Fatalf("a refused load answered %+v at version %d, so a caller that ignores the error decides on a state assembled out of the facts that came before the one that broke",
					state, at.Version())
			}
			return
		}
		if at.Version() != 2 {
			t.Fatalf("a stream of two stored facts folded at version %d", at.Version())
		}
		again, _, second := repo.Load(ctx, acme)
		if second != nil || again.Balance != state.Balance || !maps.Equal(again.Applied, state.Applied) {
			t.Fatalf("one stream folded to %+v and then to %+v (%v), so what a replay produces depends on something other than the bytes", state, again, second)
		}
	})
}
