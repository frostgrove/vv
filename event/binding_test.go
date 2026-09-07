package event

import (
	"errors"
	"maps"
	"slices"
	"sync/atomic"
	"testing"
)

type countingCodec[V any] struct {
	Codec[V]
	asked *atomic.Int64
}

func (this countingCodec[V]) Encode(value V) ([]byte, error) {
	this.asked.Add(1)
	return this.Codec.Encode(value)
}

func (this countingCodec[V]) Decode(payload []byte) (V, error) {
	this.asked.Add(1)
	return this.Codec.Decode(payload)
}

func (this countingCodec[V]) CanEncode() error {
	this.asked.Add(1)
	return this.Codec.CanEncode()
}

// A store enters the kernel by exactly two calls, and both ask it the same
// questions. At Bind alone the checks are absent from the deployment that never
// binds: a projector holds a Log, a store answering a zero MaxRead reaches it
// unvalidated, honestly returns at most zero envelopes, and the drain terminates
// having read nothing.
func TestBothDoorsCheckTheStore(t *testing.T) {
	for _, one := range []struct {
		what      string
		dishonest func(*recordingStore)
	}{
		{"a store that does not say what it writes to", func(store *recordingStore) { store.backing = Backing{} }},
		{"a store that states no payload bound", func(store *recordingStore) { store.limits.MaxPayload = 0 }},
		{"a store that states no batch bound", func(store *recordingStore) { store.limits.MaxBatch = 0 }},
		{"a store that states no key bound", func(store *recordingStore) { store.limits.MaxKey = 0 }},
		{"a store that states no stream page", func(store *recordingStore) { store.limits.StreamPage = 0 }},
		{"a store that states no read bound", func(store *recordingStore) { store.limits.MaxRead = 0 }},
		{"a store whose payload bound is above the kernel's", func(store *recordingStore) { store.limits.MaxPayload = MaxPayloadBytes + 1 }},
		{"a store whose batch bound is above the kernel's", func(store *recordingStore) { store.limits.MaxBatch = MaxBatchCount + 1 }},
		{"a store whose key bound is above the kernel's", func(store *recordingStore) { store.limits.MaxKey = MaxKeyBytes + 1 }},
		{"a store whose stream page is above the kernel's", func(store *recordingStore) { store.limits.StreamPage = MaxPageCount + 1 }},
		{"a store whose read bound is above the kernel's", func(store *recordingStore) { store.limits.MaxRead = MaxPageCount + 1 }},
		{"a store whose stream page and payload bound hold more than one read may", func(store *recordingStore) {
			store.limits.MaxPayload, store.limits.StreamPage = MaxPayloadBytes, ResidentPage(MaxPayloadBytes)+1
		}},
		{"a store whose read bound and payload bound hold more than one read may", func(store *recordingStore) {
			store.limits.MaxPayload, store.limits.MaxRead = MaxPayloadBytes, ResidentPage(MaxPayloadBytes)+1
		}},
		{"a store that says nothing about transactions", func(store *recordingStore) { store.capabilities.Transactions = Unstated }},
		{"a store that says nothing about persistence", func(store *recordingStore) { store.capabilities.Persistence = Unstated }},
		{"a store that says nothing about monotone visibility", func(store *recordingStore) { store.capabilities.MonotoneVisibility = Unstated }},
		{"a store that says nothing about a shared backing", func(store *recordingStore) { store.capabilities.SharedBacking = Unstated }},
	} {
		store := newRecordingStore(t)
		one.dishonest(store)
		if _, err := Bind(Open(store), declareAccounts(t).aggregate); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("%s was bound and answered %v, so a bound the kernel enforces on the store's behalf is enforced by nobody", one.what, err)
		}
		if _, err := Read(store, ""); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("%s was read from and answered %v; a deployment that only reads never binds, so this is the only door it takes", one.what, err)
		}
	}

	var missing *recordingStore
	for _, one := range []struct {
		what  string
		store Store
	}{
		{"no store at all", nil},
		{"a store constructor whose error the composition root ignored", missing},
	} {
		if _, err := Bind(Open(one.store), declareAccounts(t).aggregate); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("binding %s answered %v rather than the sentinel every other dishonest store gets", one.what, err)
		}
		if _, err := Read(one.store, ""); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("reading through %s answered %v", one.what, err)
		}
	}
	if _, err := Read(ReadOnly(missing), ""); !errors.Is(err, ErrWrongStore) {
		t.Fatalf("reading through a read-only view of a store that is not there answered %v", err)
	}

	for _, one := range []struct {
		what      string
		atTheEdge func(*Limits)
	}{
		{"a payload bound of exactly the kernel's", func(limits *Limits) {
			limits.MaxPayload = MaxPayloadBytes
			limits.StreamPage, limits.MaxRead = ResidentPage(MaxPayloadBytes), ResidentPage(MaxPayloadBytes)
		}},
		{"a batch bound of exactly the kernel's", func(limits *Limits) { limits.MaxBatch = MaxBatchCount }},
		{"a key bound of exactly the kernel's", func(limits *Limits) { limits.MaxKey = MaxKeyBytes }},
		{"a stream page of exactly the kernel's", func(limits *Limits) {
			limits.MaxPayload, limits.StreamPage = MaxResidentBytes/MaxPageCount, MaxPageCount
		}},
		{"a read bound of exactly the kernel's", func(limits *Limits) {
			limits.MaxPayload, limits.MaxRead = MaxResidentBytes/MaxPageCount, MaxPageCount
		}},
	} {
		store := newRecordingStore(t)
		one.atTheEdge(&store.limits)
		if _, err := Bind(Open(store), declareAccounts(t).aggregate); err != nil {
			t.Fatalf("a store publishing %s was refused at Bind with %v; a deployment reads these ceilings to choose its numbers, and one that copied them would not start", one.what, err)
		}
		if _, err := Read(store, ""); err != nil {
			t.Fatalf("a store publishing %s was refused at Read with %v", one.what, err)
		}
	}

	honest := newRecordingStore(t)
	if _, err := Bind(Open(honest), declareAccounts(t).aggregate); err != nil {
		t.Fatalf("the control store was refused at Bind with %v, so every row above passes against a door that refuses everything", err)
	}
	reader, err := Read(ReadOnly(honest), "")
	if err != nil {
		t.Fatalf("the control store was refused at Read with %v", err)
	}
	if reader == nil {
		t.Fatal("Read answered no reader and no error")
	}
	if _, appendable := ReadOnly(honest).(Store); appendable {
		t.Fatal("a read-only log asserts back to a store, so a projector can append and a replay writes")
	}
}

// A page bound and a payload bound are each inside their own ceiling and their
// product is not: the store fills the page before the kernel sees a byte of it,
// so a page of legal envelopes is the one way a store can hold more than one
// read may. The rows are chosen so that the count is well inside MaxPageCount,
// which is what makes the refusal the product's rather than the ceiling's.
func TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused(t *testing.T) {
	maxPayload := 1 << 16
	page := ResidentPage(maxPayload)
	if page+1 > MaxPageCount {
		t.Fatalf("a page of %d envelopes at a payload bound of %d is at the kernel's page ceiling of %d, so the rows below would be refused for the count they name and not for the bytes they hold",
			page, maxPayload, MaxPageCount)
	}

	for _, one := range []struct {
		what  string
		bound func(*Limits, int)
	}{
		{"a stream page", func(limits *Limits, count int) { limits.StreamPage = count }},
		{"a read bound", func(limits *Limits, count int) { limits.MaxRead = count }},
	} {
		store := newRecordingStore(t)
		store.limits.MaxPayload = maxPayload
		one.bound(&store.limits, page+1)
		if _, err := Bind(Open(store), declareAccounts(t).aggregate); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("%s of %d envelopes at a payload bound of %d was bound and answered %v, and one such page is more bytes than the kernel's ceiling for what a read may hold",
				one.what, page+1, maxPayload, err)
		}
		if _, err := Read(store, ""); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("%s of %d envelopes at a payload bound of %d was read through and answered %v", one.what, page+1, maxPayload, err)
		}

		control := newRecordingStore(t)
		control.limits.MaxPayload = maxPayload
		one.bound(&control.limits, page)
		if _, err := Bind(Open(control), declareAccounts(t).aggregate); err != nil {
			t.Fatalf("%s of exactly the %d envelopes one read may hold was refused at Bind with %v, so the row above passes against a door that refuses every page bound",
				one.what, page, err)
		}
		if _, err := Read(control, ""); err != nil {
			t.Fatalf("%s of exactly the %d envelopes one read may hold was refused at Read with %v", one.what, page, err)
		}
	}
}

// A declaration whose own error the composition root ignored is nil, and so is a
// binding nobody opened. Both are the shape of one line missing an error check,
// and both are refused rather than taking the process down at start-up.
func TestABindWithNothingToBindIsRefused(t *testing.T) {
	if _, err := Bind[account, accountID](Open(newRecordingStore(t)), nil); !errors.Is(err, ErrDeclaration) {
		t.Fatalf("binding no aggregate at all answered %v", err)
	}
	var unopened *Binding
	if _, err := Bind(unopened, declareAccounts(t).aggregate); !errors.Is(err, ErrWrongStore) {
		t.Fatalf("binding through a binding nobody opened answered %v", err)
	}
}

// What a bind may cost, in the one currency a per-request store cannot afford:
// a codec interrogation is O(facts x revisions) and would be paid on every
// request of the tenancy design this framework ships. The declaration is shared
// between every request goroutine, so a bind may also read it and never write
// it.
func TestABindInterrogatesNoCodecAndMutatesNoDeclaration(t *testing.T) {
	asked := &atomic.Int64{}
	aggregate := Define[account]("accounts.account", accountKey)
	opening := Declare(aggregate, "accounts.opened",
		From(countingCodec[opened]{Codec: JSON[opened](), asked: asked}), openAccount)
	Declare(aggregate, "accounts.credited",
		Then(From(countingCodec[creditedV1]{Codec: JSON[creditedV1](), asked: asked}),
			countingCodec[creditedV2]{Codec: JSON[creditedV2](), asked: asked}, toCreditedV2), creditAccount)

	aggregate.seal()
	declared := slices.Sorted(maps.Keys(aggregate.facts))
	asked.Store(0)

	for range 3 {
		if _, err := Bind(Open(newRecordingStore(t)), aggregate); err != nil {
			t.Fatalf("a per-request store was refused a repository over an already-bound declaration with %v", err)
		}
	}

	if got := asked.Load(); got != 0 {
		t.Fatalf("three binds asked the declaration's codecs %d questions, and a bind that walks the chain charges every request of a per-request store O(facts x revisions)", got)
	}
	if !aggregate.sealed.Load() || !slices.Equal(slices.Sorted(maps.Keys(aggregate.facts)), declared) {
		t.Fatalf("binding left the declaration holding %v, so a bind writes state every request goroutine reads", slices.Sorted(maps.Keys(aggregate.facts)))
	}

	if opening.New(accountID{tenant: "acme", number: "A-17"}, opened{Owner: "acme"}).err != nil {
		t.Fatal("the control decision was refused, so the count above is over codecs that answer nothing")
	}
	if asked.Load() == 0 {
		t.Fatal("a decision asked the declared codec nothing either, so the count above is a counter nobody increments")
	}
}

func TestOneFamilyNamesOneAggregate(t *testing.T) {
	store := newRecordingStore(t)
	binding := Open(store)
	first, second := declareAccounts(t), declareAccounts(t)

	if _, err := Bind(binding, first.aggregate); err != nil {
		t.Fatalf("the first aggregate was refused at Bind: %v", err)
	}
	if _, err := Bind(binding, second.aggregate); !errors.Is(err, ErrFamily) {
		t.Fatalf("a second declaration of the family %q was bound through one binding and answered %v; two aggregates over one family fold each other's facts and append at versions derived from the other's, with no error at any point",
			first.aggregate.Family(), err)
	}
	if _, err := Bind(binding, first.aggregate); err != nil {
		t.Fatalf("the declaration that was already bound was refused a second repository with %v, so a feature module that binds twice is refused for a collision that is not one", err)
	}
	if _, err := Bind(Open(store), second.aggregate); err != nil {
		t.Fatalf("a second binding over the same store refused the second declaration with %v, and what a binding scopes is the value Open returned", err)
	}
}
