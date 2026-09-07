package eventtest

import (
	"bytes"
	"context"
	"fmt"

	"github.com/frostgrove/vv/event"
)

// The four hand-offs a store is party to, and the two directions are asserted
// differently for a reason. What a read returns belongs to whoever received it,
// including its capacity, so the outbound half writes into every byte it was
// handed and reads again. What an append is given stays the caller's, so the
// inbound half decides one fact twice, appends one copy, and folds both.
//
// The stream is one event longer than this store's own page and than one global
// read of it, so what a read answers here is a page rather than a history: an
// append to a page a store still has rows beyond lands on a row it will serve
// again, and a global read from before the stream has a page after its first.
func payloadOwnershipSection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	from := this.tail(ctx, store)
	id := this.account("a")
	stream := this.streamOf(held, id)
	_, at := this.load(ctx, repo, id)

	// One more than the wider of the two pages, which is the least that makes
	// every read below a page rather than a history, and it is the one count in
	// this suite with no ceiling over it: a store publishing the legal maximum of
	// four thousand is asked for four thousand appends. Narrowing it is not
	// available here, because what is under test is what the store's own slice
	// does, and a page this suite cut would be its own. A store whose appends are
	// a network away buys the window for them through Factory.Window.
	notes := max(this.limits.StreamPage, this.limits.MaxRead) + 1
	decided := make([]event.Change[ledger], 0, notes)
	for note := range notes {
		decided = append(decided, held.noted.New(id, noted{Bytes: []byte(fmt.Sprintf("note-%d", note))}))
	}
	this.batched(ctx, repo, at, decided...)

	written := payloadsOf(this.drain(ctx, store, stream))
	if len(written) != notes {
		this.refuse("a stream this section appended %d events to holds %d", notes, len(written))
	}
	page := this.readStream(ctx, store, stream, 0)
	if len(page) != this.limits.StreamPage {
		this.refuse("a stream of %d events answered a first page of %d where this store publishes a page of %d, so the hand-offs below were never under test", notes, len(page), this.limits.StreamPage)
	}
	if equal := this.readStream(ctx, store, stream, 0); !samePayloads(equal, written[:len(page)]) {
		this.refuse("two reads of one page answered two different pages")
	}

	this.spilled(ctx, store, stream, written)

	for _, envelope := range page {
		for index := range envelope.Payload {
			envelope.Payload[index] = 0xff
		}
	}
	if again := this.readStream(ctx, store, stream, 0); !samePayloads(again, written[:len(page)]) {
		this.refuse("a page read after every byte of the page before it was overwritten answered different bytes, so what this store returned was memory it still holds")
	}
	state, _ := this.load(ctx, repo, id)
	if !sameNotes(state, written) {
		this.refuse("the stream folds to %d notes that are not the ones it was appended, after a consumer wrote into a page this store handed it", len(state.Notes))
	}

	first := this.readStream(ctx, store, stream, 0)
	kept := clonePage(first)
	this.readStream(ctx, store, stream, event.Version(len(first)))
	if !samePage(first, kept) {
		this.refuse("the page a first read returned changed when the read after it was issued, so this store pools the slice or the bytes it hands out and a consumer that kept a page reads another page's events out of it")
	}

	firstAll, cursor := this.readAll(ctx, store, from)
	heldAll := clonePage(firstAll)
	this.readAll(ctx, store, cursor)
	if !samePage(firstAll, heldAll) {
		this.refuse("the page a first global read returned changed when the read after it was issued, and a *Reader hands that page straight to its consumer")
	}

	if tail := this.readStream(ctx, store, stream, event.Version(notes)); len(tail) != 0 {
		this.refuse("a read past the end of a stream of %d events answered %d envelopes", notes, len(tail))
	}

	this.inbound(ctx, store, repo, held)
	this.aliased(ctx, repo, held)
}

// The capacity half of the clause, and it is the half a page that has been
// overwritten cannot answer: a store that fills one buffer per page and hands
// out a sub-slice of it per row satisfies every other assertion here, and the
// spare capacity of each payload runs into the next event's bytes. So one byte
// is appended to the first payload and the witness is the neighbour that was
// cloned before it. The []Envelope is asked the same question against the page
// after it, because a slice cut from an array a store still serves rows out of
// has its own next row in its spare capacity.
func (this *probe) spilled(ctx context.Context, store event.Store, stream event.Stream, written [][]byte) {
	page := this.readStream(ctx, store, stream, 0)
	if len(page) == 0 {
		this.refuse("a stream of several events answered an empty first page")
	}
	kept := clonePage(page)
	page[0].Payload = append(page[0].Payload, 0xff)
	if !samePage(page[1:], kept[1:]) {
		this.refuse("appending one byte to the first payload of a page rewrote another envelope of the same page, so this store hands out sub-slices of one buffer and a consumer that appends to a payload it was given corrupts the next event's")
	}

	beside := this.readStream(ctx, store, stream, 0)
	after := event.Version(len(beside))
	next := clonePage(this.readStream(ctx, store, stream, after))
	if len(beside) == 0 || len(next) == 0 {
		this.refuse("a stream longer than this store's own page answered a first page of %d and %d envelopes after it", len(beside), len(next))
	}
	spilling := append(beside, beside[0])
	if again := this.readStream(ctx, store, stream, after); !samePage(again, next) {
		this.refuse("appending a %dth envelope to a page of %d changed the page the read after it answers, so the slice this store handed out has capacity that runs into rows it still serves",
			len(spilling), len(beside))
	}
	if again := this.readStream(ctx, store, stream, 0); !samePayloads(again, written[:len(beside)]) {
		this.refuse("a page read after one envelope was appended to the page before it answered different bytes, so the slice this store returned has capacity it still reads out of")
	}
}

// Hand-offs three and six, which no hand-out assertion reaches: the same fact is
// decided twice, one list is appended and the other never leaves the suite, and
// afterwards both must fold to one state. A store — or a decorator forwarding
// for it — that writes into a Record.Payload it was handed rewrites a fact that
// was already decided, and only the appended list's arrays are ever in its hands.
func (this *probe) inbound(ctx context.Context, store event.Store, repo *event.Repo[ledger, accountID], held declaration) {
	id := this.account("b")
	decided := func() []event.Change[ledger] {
		return []event.Change[ledger]{
			held.noted.New(id, noted{Bytes: []byte("inbound-one")}),
			held.noted.New(id, noted{Bytes: []byte("inbound-two")}),
		}
	}
	appended, kept := decided(), decided()

	before, err := held.aggregate.Fold(id, ledger{}, appended...)
	if err != nil {
		this.refuse("folding a change list the suite decided answered %v", err)
	}
	beside, err := held.aggregate.Fold(id, ledger{}, kept...)
	if err != nil {
		this.refuse("folding an identical change list answered %v", err)
	}
	if !sameLedger(before, beside) {
		this.refuse("one fact decided twice folds to two states before anything was appended, so the case below could not tell an append apart from a decision")
	}

	_, at := this.load(ctx, repo, id)
	this.batched(ctx, repo, at, appended...)

	after, err := held.aggregate.Fold(id, ledger{}, appended...)
	if err != nil {
		this.refuse("folding the appended change list after the append answered %v", err)
	}
	untouched, err := held.aggregate.Fold(id, ledger{}, kept...)
	if err != nil {
		this.refuse("folding the change list that never left the suite answered %v", err)
	}
	if !sameLedger(after, untouched) {
		this.refuse("the change list this append was given folds to a different state than the identical list beside it, so the store or a decorator forwarding for it wrote into a payload it was handed and the fact recorded is not the fact decided")
	}
	reloaded, _ := this.load(ctx, repo, id)
	if !sameLedger(reloaded, untouched) {
		this.refuse("what this store recorded folds to a different state than the decision that was appended")
	}
}

// A codec that decodes by aliasing its input is ordinary, so a state field can
// be the store's own bytes. The caller may write into it, and the next load must
// not know.
func (this *probe) aliased(ctx context.Context, repo *event.Repo[ledger, accountID], held declaration) {
	id := this.account("c")
	_, at := this.load(ctx, repo, id)
	this.append(ctx, repo, at, held.noted.New(id, noted{Bytes: []byte("aliased")}))

	state, _ := this.load(ctx, repo, id)
	if len(state.Notes) != 1 {
		this.refuse("a stream of one note folds to %d notes", len(state.Notes))
	}
	for index := range state.Notes[0] {
		state.Notes[0][index] = 'x'
	}
	again, _ := this.load(ctx, repo, id)
	if len(again.Notes) != 1 || !bytes.Equal(again.Notes[0], []byte("aliased")) {
		this.refuse("a load after the caller wrote into the state it was handed folds to %q, so the state a fold published points into memory the store still holds", again.Notes[0])
	}
}

func payloadsOf(page []event.Envelope) [][]byte {
	held := make([][]byte, 0, len(page))
	for _, envelope := range page {
		held = append(held, bytes.Clone(envelope.Payload))
	}
	return held
}

func clonePage(page []event.Envelope) []event.Envelope {
	held := make([]event.Envelope, 0, len(page))
	for _, envelope := range page {
		held = append(held, withPayload(envelope, bytes.Clone(envelope.Payload)))
	}
	return held
}

func samePage(page, held []event.Envelope) bool {
	if len(page) != len(held) {
		return false
	}
	for index := range page {
		if page[index].Stream != held[index].Stream || page[index].Version != held[index].Version ||
			page[index].Position != held[index].Position || !bytes.Equal(page[index].Payload, held[index].Payload) {
			return false
		}
	}
	return true
}

func samePayloads(page []event.Envelope, written [][]byte) bool {
	if len(page) != len(written) {
		return false
	}
	for index := range page {
		if !bytes.Equal(page[index].Payload, written[index]) {
			return false
		}
	}
	return true
}

func sameNotes(state ledger, written [][]byte) bool {
	if len(state.Notes) != len(written) {
		return false
	}
	for index := range written {
		if !bytes.Equal(state.Notes[index], written[index]) {
			return false
		}
	}
	return true
}
