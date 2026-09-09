package eventtest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
)

// What a section asserts against, and the reason it is a value rather than a
// *testing.T: a failure here is a verdict the suite reports, and the suite's own
// falsification run has to observe one without the test that drives it turning
// red.
//
// The half that is about running a section rather than about a Store is
// recording, which the checkpoint suite's own subject embeds too: two verdict
// types would be two accounts of one rule, and the three anti-vacuity rules
// would drift apart the moment one of them was written twice.
type probe struct {
	recording
	factory Factory
	opening
}

type recording struct {
	t      *testing.T
	name   string
	mark   string
	window time.Duration
	unmet  []string
	broke  string
}

type abort struct{}

func (this *recording) walk(body func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, aborted := recovered.(abort); !aborted {
				panic(recovered)
			}
		}
	}()
	body()
}

// Every store call a section makes runs under this. The contract lets a store
// wait for a competing transaction rather than refuse at once, so a store that
// waits for one nothing will finish would otherwise hang the binary until
// go test's own ten-minute panic — which names a goroutine stack and no section.
// Twenty sections that each wait out this window still report inside that
// ten minutes, and no correct store spends a fraction of it answering one page.
// A store that needs longer says so through Factory.Window.
const sectionWindow = 20 * time.Second

func (this *recording) context() context.Context {
	window := this.window
	if window <= 0 {
		window = sectionWindow
	}
	ctx, stop := context.WithTimeout(context.Background(), window)
	this.t.Cleanup(stop)
	return ctx
}

func (this *recording) refuse(format string, args ...any) {
	if this.broke == "" {
		this.broke = fmt.Sprintf(format, args...)
	}
	panic(abort{})
}

func (this *recording) unable(format string, args ...any) {
	this.unmet = append(this.unmet, fmt.Sprintf(format, args...))
}

func (this *probe) store() event.Store {
	store := this.factory.New(this.t)
	if store == nil {
		this.refuse("the factory answered no store")
	}
	if store.Limits() != this.limits {
		this.refuse("this factory built a store publishing %+v where the one it was admitted on publishes %+v, and every count this suite writes is derived from the second", store.Limits(), this.limits)
	}
	if store.Capabilities() != this.capabilities {
		this.refuse("this factory built a store claiming %+v where the one it was admitted on claims %+v, and every section this run gated it on was gated on the second", store.Capabilities(), this.capabilities)
	}
	return store
}

// Capabilities, Limits and Backing are constant for a store's life, and the
// parties that read them read them at different moments: this suite keeps all
// three from the door, the kernel keeps the first two from Bind and re-reads the
// backing per operation. A store that derives its capabilities from whatever
// executor is bound, or its limits from a connection it reconfigured, answers
// one thing here and another there and nothing anywhere reports it.
type published struct {
	capabilities event.Capabilities
	limits       event.Limits
	backing      event.Backing
}

func publishedBy(store event.Store) published {
	return published{capabilities: store.Capabilities(), limits: store.Limits(), backing: store.Backing()}
}

func (this *probe) unchanged(store event.Store, before published) {
	after := publishedBy(store)
	if after.capabilities != before.capabilities {
		this.refuse("this store claimed %+v and then %+v, so what this run gated its sections on and what the kernel retained at Bind are two different claims", before.capabilities, after.capabilities)
	}
	if after.limits != before.limits {
		this.refuse("this store published %+v and then %+v, so the page a walk stops at and the page the kernel checks a read against are two different numbers", before.limits, after.limits)
	}
	if !after.backing.Equal(before.backing) {
		this.refuse("this store named one backing and then another, so a token minted through it is refused by the operation after it and two subsystems writing atomically cannot prove they did")
	}
}

func (this *probe) declared() declaration {
	held, err := declare("eventtest.ledger." + this.mark)
	if err != nil {
		this.refuse("the suite cannot declare its own aggregate: %v", err)
	}
	return held
}

func (this *probe) bind(store event.Store, held declaration) *event.Repo[ledger, accountID] {
	repo, err := event.Bind(event.Open(store), held.aggregate)
	if err != nil {
		this.refuse("binding the suite's own aggregate to this store answered %v", err)
	}
	return repo
}

func (this *probe) open() (event.Store, *event.Repo[ledger, accountID], declaration) {
	store := this.store()
	held := this.declared()
	return store, this.bind(store, held), held
}

func (this *probe) account(label string) accountID {
	if len(label) > widestLabel {
		this.refuse("this section named an account %q, where the suite reserves %d bytes of a store's MaxKey for a label", label, widestLabel)
	}
	return accountID{Tenant: this.run, Number: this.mark + label}
}

func (this *probe) streamOf(held declaration, id accountID) event.Stream {
	key, err := held.aggregate.Key(id)
	if err != nil {
		this.refuse("the suite's own identity %v renders no legal key: %v", id, err)
	}
	return event.Stream{Family: held.family, Key: key}
}

func (this *probe) load(ctx context.Context, repo *event.Repo[ledger, accountID], id accountID) (ledger, event.At[ledger]) {
	state, at, err := repo.Load(ctx, id)
	if err != nil {
		this.refuse("loading %v answered %v", id, err)
	}
	return state, at
}

func (this *probe) append(ctx context.Context, repo *event.Repo[ledger, accountID], at event.At[ledger], changes ...event.Change[ledger]) (event.At[ledger], event.Commit) {
	moved, receipt, err := repo.Append(ctx, at, changes...)
	if err != nil {
		this.refuse("appending %d change(s) at version %d answered %v", len(changes), at.Version(), err)
	}
	return moved, receipt
}

// One decision, in as many appends as this store admits changes at a time: a
// case that needs five events in two decisions reaches five on a store whose
// MaxBatch is one, and two on a store whose MaxBatch is three.
func (this *probe) batched(ctx context.Context, repo *event.Repo[ledger, accountID], at event.At[ledger], changes ...event.Change[ledger]) event.At[ledger] {
	for chunk := range slices.Chunk(changes, this.limits.MaxBatch) {
		at, _ = this.append(ctx, repo, at, chunk...)
	}
	return at
}

// Every raw read a section makes goes through here, so a store that refuses one
// of them fails the section that was reading rather than panicking through it.
func (this *probe) readStream(ctx context.Context, store event.Store, stream event.Stream, after event.Version) []event.Envelope {
	page, err := store.ReadStream(ctx, stream, after)
	if err != nil {
		this.refuse("reading %s after version %d answered %v", stream, after, err)
	}
	return page
}

// A page shorter than the one this store publishes is the end of the stream and
// a full one moves the version the next read continues from, so the walk takes
// as many pages as the stream is long and there is no count to guess at. A store
// that answers a full page forever is answering an endless stream, and the
// window every section runs under is what reports that rather than spinning.
func (this *probe) drain(ctx context.Context, store event.Store, stream event.Stream) []event.Envelope {
	page := store.Limits().StreamPage
	held := []event.Envelope{}
	var after event.Version
	for {
		read := this.readStream(ctx, store, stream, after)
		if len(read) > page {
			this.refuse("reading %s answered %d envelopes where this store publishes a page of %d", stream, len(read), page)
		}
		held = append(held, read...)
		after += event.Version(len(read))
		if len(read) < page {
			return held
		}
	}
}

func (this *probe) readAll(ctx context.Context, store event.Store, after event.Cursor) ([]event.Envelope, event.Cursor) {
	page, cursor, err := store.ReadAll(ctx, after)
	if err != nil {
		this.refuse("reading the log answered %v", err)
	}
	return page, cursor
}

// Where the log already is. A section takes one of these before the writes it
// means to walk over and walks from it afterwards, so what it reads is its own
// tail rather than every event the backing has ever held — a database that has
// been used for anything, this suite's own previous runs included, holds more
// than a walk from the beginning would care to reach. A factory that answers the end says so and
// costs one call; one that does not is read to it, which is the whole log, and a
// log another process is appending to has no end for that read to arrive at.
func (this *probe) tail(ctx context.Context, store event.Store) event.Cursor {
	if this.factory.Tail != nil {
		return this.factory.Tail(this.t, store)
	}
	var cursor event.Cursor
	for {
		page, next, err := store.ReadAll(ctx, cursor)
		if err != nil {
			this.refuse("finding the end of this store's log answered %v, and a store whose log holds more than this run wrote answers a cursor at its end through Factory.Tail rather than being read to it", err)
		}
		if len(page) == 0 {
			return cursor
		}
		this.advanced(cursor, next, len(page))
		cursor = next
	}
}

// What a section walks over: where the log was before the section wrote
// anything, whose events in it are the section's own, and how many of them it
// wrote. The last is what ends a walk over a log somebody else is still
// appending to, where the empty page a walk would otherwise stop on never comes.
type walking struct {
	from event.Cursor
	held declaration
	want int
}

func (this *probe) walkLog(ctx context.Context, store event.Store, over walking) []event.Envelope {
	mine := []event.Envelope{}
	cursor := over.from
	for len(mine) < over.want {
		page, next := this.readAll(ctx, store, cursor)
		if len(page) == 0 {
			return mine
		}
		this.advanced(cursor, next, len(page))
		mine = append(mine, this.mine(page, over.held)...)
		cursor = next
	}
	return mine
}

// A walk ends at the events it came for or at an empty page, never at a count
// nobody could derive. The one answer that would end neither is a page beside the
// cursor it was read from, which a consumer resuming from that cursor reads
// forever.
func (this *probe) advanced(from, next event.Cursor, page int) {
	if next == from {
		this.refuse("a page of %d envelopes came with the cursor the read that answered it was issued from, so a consumer that persists that cursor resumes at the page it has already read", page)
	}
}

// Every store this suite runs against holds more than this run wrote: two
// sections over one backing, a second process, and a database somebody else's
// suite ran against last week. A global read answers all of it, and only this
// run's own streams are a section's business.
func (this *probe) owns(held declaration, stream event.Stream) bool {
	prefix := string(event.Compose(this.run)) + "/"
	return stream.Family == held.family && strings.HasPrefix(string(stream.Key), prefix)
}

func (this *probe) versions(page []event.Envelope) []event.Version {
	held := make([]event.Version, 0, len(page))
	for _, envelope := range page {
		held = append(held, envelope.Version)
	}
	return held
}
