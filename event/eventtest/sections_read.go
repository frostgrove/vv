package eventtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/frostgrove/vv/event"
)

func globalOrderSection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	from := this.tail(ctx, store)
	written := this.spread(ctx, repo, held, "a", "b", "c")
	over := walking{from: from, held: held, want: written}

	first := this.walkLog(ctx, store, over)
	if len(first) != written {
		this.refuse("a walk over the log returned %d of this run's %d events", len(first), written)
	}
	this.ascending(first, "a walk over the log")
	this.subsequence(first)

	second := this.walkLog(ctx, store, over)
	places := map[event.Stream]map[event.Version]event.Position{}
	for _, envelope := range first {
		if places[envelope.Stream] == nil {
			places[envelope.Stream] = map[event.Version]event.Position{}
		}
		places[envelope.Stream][envelope.Version] = envelope.Position
	}
	for _, envelope := range second {
		if before := places[envelope.Stream][envelope.Version]; before != envelope.Position {
			this.refuse("%s version %d was at position %d and is now at position %d, so a position was reassigned and a consumer that checkpointed past it will never read it",
				envelope.Stream, envelope.Version, before, envelope.Position)
		}
	}
}

// One stream's order is a subsequence of the log's: a store may interleave two
// streams however it likes, and may not reorder one of them. A projector that
// reads the log and folds per stream reads a different history than the stream's
// own read gives if it does.
func (this *probe) subsequence(page []event.Envelope) {
	seen := map[event.Stream]event.Version{}
	for _, envelope := range page {
		if envelope.Version <= seen[envelope.Stream] {
			this.refuse("%s appears in the log at version %d after version %d, so the log reorders one stream against its own read",
				envelope.Stream, envelope.Version, seen[envelope.Stream])
		}
		seen[envelope.Stream] = envelope.Version
	}
	if len(seen) < 2 {
		this.refuse("this run's events reached %d streams in the log, so nothing above was asserted about two streams interleaving", len(seen))
	}
}

func (this *probe) ascending(page []event.Envelope, doing string) {
	for offset := 1; offset < len(page); offset++ {
		if page[offset].Position <= page[offset-1].Position {
			this.refuse("%s answered position %d after position %d, so a position was reused or the log went backwards", doing, page[offset].Position, page[offset-1].Position)
		}
	}
}

func conservationSection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	from := this.tail(ctx, store)
	labels := []string{"a", "b", "c"}
	written := this.spread(ctx, repo, held, labels...)

	global := map[string]bool{}
	for _, envelope := range this.walkLog(ctx, store, walking{from: from, held: held, want: written}) {
		global[named(envelope)] = true
	}
	streams := map[string]bool{}
	for _, label := range labels {
		stream := this.streamOf(held, this.account(label))
		for _, envelope := range this.drain(ctx, store, stream) {
			if envelope.Stream != stream {
				this.refuse("the read of %s answered an event of %s, so one stream's history holds another's decisions", stream, envelope.Stream)
			}
			streams[named(envelope)] = true
		}
	}
	if len(global) != written || len(streams) != written {
		this.refuse("this run wrote %d events, the global read returned %d and the stream reads returned %d", written, len(global), len(streams))
	}
	for name := range streams {
		if !global[name] {
			this.refuse("%s is in a stream's own read and in no global read, so a decision this store holds reaches no projector, outbox or export", name)
		}
	}
	for name := range global {
		if !streams[name] {
			this.refuse("%s is in the global read and in no stream's read, so an event this store publishes folds into no state", name)
		}
	}
}

func named(envelope event.Envelope) string {
	return fmt.Sprintf("%s/%s@%d", envelope.Stream.Family, envelope.Stream.Key, envelope.Version)
}

// The pages this stream is read in are this store's own and the narrower ones
// every store admits, so nothing here asks a store for a page it never published
// — a suite that narrowed past a store's own number would be building the
// dishonest store its own bounds defect exists to catch. The widest is capped
// because the stream is one event longer than it, and a store publishing a page
// of four thousand would otherwise be asked to write four thousand events to be
// certified on the kernel's paging rather than on its own.
const widestNarrowing = 4

func streamPagingSection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	id := this.account("a")
	_, at := this.load(ctx, repo, id)
	widest := min(this.limits.StreamPage, widestNarrowing)
	balance := int64(0)
	for credit := range widest + 1 {
		at, _ = this.append(ctx, repo, at, held.credited.New(id, credited{Amount: int64(credit) + 1}))
		balance += int64(credit) + 1
	}

	whole, at := this.load(ctx, repo, id)
	if at.Version() != event.Version(widest+1) || whole.Balance != balance {
		this.refuse("%d appends fold to a balance of %d at version %d, where they credited %d", widest+1, whole.Balance, at.Version(), balance)
	}
	for _, page := range narrowings(widest) {
		paged := this.bind(narrowed{over{store}, page}, held)
		state, reached := this.load(ctx, paged, id)
		if !sameLedger(state, whole) || reached.Version() != at.Version() {
			this.refuse("the same stream read in pages of %d folds to a balance of %d at version %d where its own store's pages fold to %d at %d",
				page, state.Balance, reached.Version(), whole.Balance, at.Version())
		}
	}
}

func narrowings(widest int) []int {
	held := []int{}
	for _, page := range []int{1, 2, widest} {
		if page <= widest && (len(held) == 0 || page > held[len(held)-1]) {
			held = append(held, page)
		}
	}
	return held
}

func globalPagingSection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	from := this.tail(ctx, store)
	written := this.spread(ctx, repo, held, "a", "b", "c")

	whole := this.walkLog(ctx, store, walking{from: from, held: held, want: written})
	if len(whole) != written {
		this.refuse("an uninterrupted walk over the log returned %d of this run's %d events", len(whole), written)
	}
	this.ascending(whole, "an uninterrupted walk")

	reader, err := event.Read(store, from)
	if err != nil {
		this.refuse("reading through this store answered %v", err)
	}
	if _, err := reader.Next(ctx); err != nil {
		this.refuse("the first page of a walk answered %v", err)
	}
	seen := this.mine(reader.Events(), held)
	seen = append(seen, this.from(ctx, store, walking{from: reader.Cursor(), held: held, want: written - len(seen)}, "a walk resumed from the cursor of its own first page")...)
	if !sameEvents(seen, whole) {
		this.refuse("a walk taken in two readers returned %d events where one uninterrupted walk returned %d, so a page a store hands out and the cursor beside it describe two different logs",
			len(seen), len(whole))
	}
}

func (this *probe) from(ctx context.Context, log event.Log, over walking, doing string) []event.Envelope {
	reader, err := event.Read(log, over.from)
	if err != nil {
		this.refuse("%s answered %v", doing, err)
	}
	seen := []event.Envelope{}
	for len(seen) < over.want {
		more, err := reader.Next(ctx)
		if err != nil {
			this.refuse("%s answered %v", doing, err)
		}
		if !more {
			return seen
		}
		seen = append(seen, this.mine(reader.Events(), over.held)...)
	}
	return seen
}

func (this *probe) keyFor(held declaration, id accountID) event.Key {
	return this.streamOf(held, id).Key
}

func boundsSection(this *probe) {
	ctx := this.context()
	_, repo, held := this.open()
	limits := this.limits
	id := this.account("a")
	_, at := this.load(ctx, repo, id)

	at, _ = this.append(ctx, repo, at, held.noted.New(id, noted{Bytes: bytes.Repeat([]byte("p"), limits.MaxPayload)}))
	if _, _, err := repo.Append(ctx, at, held.noted.New(id, noted{Bytes: bytes.Repeat([]byte("p"), limits.MaxPayload+1)})); !errors.Is(err, event.ErrTooLarge) {
		this.refuse("a payload one byte over this store's MaxPayload of %d answered %v", limits.MaxPayload, err)
	}

	batch := make([]event.Change[ledger], 0, limits.MaxBatch+1)
	for range limits.MaxBatch + 1 {
		batch = append(batch, held.credited.New(id, credited{Amount: 1}))
	}
	if _, _, err := repo.Append(ctx, at, batch...); !errors.Is(err, event.ErrTooLarge) {
		this.refuse("a batch of %d against this store's MaxBatch of %d answered %v", len(batch), limits.MaxBatch, err)
	}
	_, receipt := this.append(ctx, repo, at, batch[:limits.MaxBatch]...)
	if receipt.Count() != limits.MaxBatch {
		this.refuse("a batch of exactly this store's MaxBatch wrote %d events", receipt.Count())
	}

	over := accountID{Tenant: this.run, Number: this.mark + strings.Repeat("k", limits.MaxKey)}
	if _, _, err := repo.Load(ctx, over); !errors.Is(err, event.ErrKey) {
		this.refuse("an identity whose key is longer than this store's MaxKey of %d answered %v", limits.MaxKey, err)
	}
	capped := this.keyAtCap(held)
	_, room := this.load(ctx, repo, capped)
	this.append(ctx, repo, room, held.credited.New(capped, credited{Amount: 1}))

	state, reached := this.load(ctx, repo, id)
	if reached.Version() != event.Version(limits.MaxBatch)+1 {
		this.refuse("a stream of %d events read back at version %d", limits.MaxBatch+1, reached.Version())
	}
	if state.Balance != int64(limits.MaxBatch) || len(state.Notes) != 1 {
		this.refuse("a stream read through this store's own pages folds to a balance of %d and %d notes", state.Balance, len(state.Notes))
	}
}

func monotoneVisibilitySection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	this.spread(ctx, repo, held, "a")

	end := this.tail(ctx, store)
	id := this.account("b")
	_, at := this.load(ctx, repo, id)
	this.append(ctx, repo, at, held.credited.New(id, credited{Amount: 3}))
	committed := fmt.Sprintf("%s/%s@%d", held.family, this.keyFor(held, id), 1)

	seen := this.from(ctx, store, walking{from: end, held: held, want: 1}, "a walk resumed from a cursor at the end of the log")
	if !contains(seen, committed) {
		this.refuse("an append that had already returned is not after a cursor taken at the end of the log before it, so a store that promises monotone visibility published a commit later than it acknowledged it")
	}
}

func (this *probe) spread(ctx context.Context, repo *event.Repo[ledger, accountID], held declaration, labels ...string) int {
	written := 0
	for round := range 2 {
		for _, label := range labels {
			id := this.account(label)
			_, at := this.load(ctx, repo, id)
			this.batched(ctx, repo, at,
				held.credited.New(id, credited{Amount: int64(round) + 1}),
				held.opened.New(id, opened{Owner: label}))
			written += 2
		}
	}
	return written
}

func (this *probe) mine(page []event.Envelope, held declaration) []event.Envelope {
	kept := make([]event.Envelope, 0, len(page))
	for _, envelope := range page {
		if this.owns(held, envelope.Stream) {
			kept = append(kept, envelope)
		}
	}
	return kept
}

func sameEvents(first, second []event.Envelope) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if named(first[index]) != named(second[index]) || first[index].Position != second[index].Position {
			return false
		}
	}
	return true
}

func contains(page []event.Envelope, name string) bool {
	for _, envelope := range page {
		if named(envelope) == name {
			return true
		}
	}
	return false
}
