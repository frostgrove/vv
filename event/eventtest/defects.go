package eventtest

import "github.com/frostgrove/vv/event"

// One inventory of the defects this suite is built to detect, in code, because a
// count kept in prose is what drifts: a test iterates this slice, runs the
// section each row names against the store that row describes, and fails when a
// section passes.
//
// Every section of the inventory is named by at least one row, and a test
// computes that rather than remembering it. A section no row names has no
// control that can fail: its assertions can be deleted one by one, or all at
// once, and every run stays green — which is the one failure a conformance suite
// cannot see about itself.
//
// Thirty-five of the forty-three are decorators over a store that is otherwise
// correct. Eight are stores rather than decorators, and that is not a
// preference: ignoring the expected version by forwarding would mean reading
// the stream to rewrite the version an append was decided at, keeping a
// rolled-back transaction's events readable by forwarding would mean serving an
// AppendRequest's records from a decorator's own memory, a second store value
// that has none of what the first wrote is a property of what a factory builds
// rather than of a call, reading from the beginning for a cursor that did
// not parse is a decision inside the store's own parser, which a decorator that
// forwards cannot reach without knowing the store's cursor format, and what a
// store does with a claim it took and with a unit of work a second value over
// its backing carries is a decision behind the seam that a decorator forwarding
// to one store value cannot take. All eight are things a decorator that claims
// to forward is forbidden, and a fixture that claims to be a broken store is
// not.
type defect struct {
	name    string
	section string
	over    func(event.Store) event.Store
}

func defects() []defect {
	return []defect{
		{"publishes limits whose factors are legal and whose read product is not", "binding",
			func(store event.Store) event.Store { return illegalProduct(store) }},
		{"publishes one number on one call to a store value and another on the next", "binding",
			func(store event.Store) event.Store { return &drifting{over: over{store}} }},
		{"truncates a stream key to the width of the column it keeps keys in", "stream identity",
			func(store event.Store) event.Store { return truncatedKeys{over{store}} }},
		{"keys its streams under a collation that does not compare a combining mark", "stream identity",
			func(store event.Store) event.Store { return foldedAccents{over{store}} }},
		{"ignores AppendRequest.Expected and admits every append", "expected version", nil},
		{"answers every envelope with no recorded instant at all", "dense versions",
			func(store event.Store) event.Store { return unrecorded{over{store}} }},
		{"reuses a position it has already issued", "global order",
			func(store event.Store) event.Store { return reusedPositions{over{store}} }},
		{"answers one event at one position on one walk and at another on the next", "global order",
			func(store event.Store) event.Store { return &movingPositions{over: over{store}} }},
		{"answers one stream's own two events in the log in the order opposite to its own", "global order",
			func(store event.Store) event.Store { return reorderedStreams{over{store}} }},
		{"leaves out of its log events the stream they belong to still answers", "conservation",
			func(store event.Store) event.Store { return partialLog{over{store}} }},
		{"answers the log with a stream key other than the one the event was written to", "conservation",
			func(store event.Store) event.Store { return renamedInTheLog{over{store}} }},
		{"returns a short stream page while more events exist", "stream paging",
			func(store event.Store) event.Store { return shortPages{over{store}} }},
		{"returns a page whose versions are out of order", "stream paging",
			func(store event.Store) event.Store { return misOrderedPages{over{store}} }},
		{"answers a stream read with another stream's event", "stream paging",
			func(store event.Store) event.Store { return foreignPages{over{store}} }},
		{"reads a stream from its beginning whatever version it was asked to read after", "stream paging",
			func(store event.Store) event.Store { return ignoresTheVersionRead{over{store}} }},
		{"reads its log from where its own last read ended rather than from the cursor it was given", "global paging",
			func(store event.Store) event.Store { return &ownPlace{over: over{store}} }},
		{"returns its newest position as a cursor while a lower one can still commit", "resumption",
			func(store event.Store) event.Store { return unsafeCursors{over{store}} }},
		{"reads from the beginning of its log for a cursor it could not parse", "resumption", nil},
		{"answers a page of its log beside no cursor to resume the walk from", "resumption",
			func(store event.Store) event.Store { return cursorlessPages{over{store}} }},
		{"publishes a stream page shorter than the page it returns", "bounds",
			func(store event.Store) event.Store { return publishesAShorterPage(store) }},
		{"writes the first record of a batch and drops the rest", "bounds",
			func(store event.Store) event.Store { return clipsBatches{over{store}} }},
		{"writes into an AppendRequest's Record.Payload after the forwarded call returns", "payload ownership",
			func(store event.Store) event.Store { return writesTheInput{over{store}} }},
		{"hands out one pooled payload buffer and one pooled envelope slice per page", "payload ownership",
			func(store event.Store) event.Store { return &pooledPages{over: over{store}} }},
		{"hands out sub-slices of one fresh page buffer without cutting their capacity", "payload ownership",
			func(store event.Store) event.Store { return packedPages{over{store}} }},
		{"answers every page of a stream out of one array it keeps", "payload ownership",
			func(store event.Store) event.Store {
				return &retainedPages{over: over{store}, whole: map[event.Stream][]event.Envelope{}}
			}},
		{"forwards the append and then reports that it refused rather than tried", "refusal classes",
			func(store event.Store) event.Store { return refusesWhatItWrote{over{store}} }},
		{"reports an append it never issued under a cancelled context as one nobody confirmed", "cancellation",
			func(store event.Store) event.Store { return uncertainWhenCancelled{over{store}} }},
		{"issues an append under a context that was cancelled before it was handed one", "cancellation",
			func(store event.Store) event.Store { return deafToCancellation{over{store}} }},
		{"serves a read under a context that was cancelled before it was handed one", "cancellation",
			func(store event.Store) event.Store { return servesTheCancelled{over{store}} }},
		{"answers an error to the second close of one store value", "lifecycle",
			func(store event.Store) event.Store { return &closesOnce{over: over{store}} }},
		{"answers a close as a store that closed does and goes on serving every door", "lifecycle",
			func(store event.Store) event.Store { return closesNothing{over{store}} }},
		{"reads the stream itself to decide the version it admits an append at", "concurrency",
			func(store event.Store) event.Store { return &lastWriteWins{over: over{store}} }},
		{"answers every append it refused as a write that certainly did not land", "concurrency",
			func(store event.Store) event.Store { return lostAsNotWritten{over{store}} }},
		{"leaves a rolled-back transaction's events readable", "transactions", nil},
		{"admits an append inside a transaction at a version another has already committed over", "transactions",
			func(store event.Store) event.Store { return &lastWriteWins{over: over{store}} }},
		{"answers the second commit of one transaction as it answered the first", "transactions", nil},
		{"releases one of the two streams a committed transaction wrote to and keeps the other claimed", "transactions", nil},
		{"answers no unit of work through a second store value over the backing one was begun on", "transactions", nil},
		{"names a unit of work through a second store value over one backing and writes outside it there", "transactions", nil},
		{"claims persistence and builds a second value over its backing that has none of what the first wrote", "durability", nil},
		{"serves a stream out of a cache one value filled and another value has written past", "shared backing",
			func(store event.Store) event.Store {
				return &cachedStreams{over: over{store}, pages: map[reading][]event.Envelope{}}
			}},
		{"holds the newest event of its log back from a global read after the append that wrote it returned", "monotone visibility",
			func(store event.Store) event.Store { return withheldTails{over{store}} }},
		{"hands its driver's own error back at every door without classifying it", "store failure classification",
			func(store event.Store) event.Store { return unclassifying{over{store}} }},
	}
}

// A store that publishes a page it does not keep to, which is the number the
// kernel verifies on the store's behalf because only the store issues the read.
func publishesAShorterPage(store event.Store) event.Store {
	limits := store.Limits()
	limits.StreamPage = 1
	return relimited{over{store}, limits}
}

// Each factor is inside its own ceiling and the product is not: a page of this
// many envelopes at this payload bound is more than one read may hold resident.
func illegalProduct(store event.Store) event.Store {
	limits := store.Limits()
	limits.MaxPayload = event.MaxPayloadBytes
	limits.StreamPage = event.ResidentPage(event.MaxPayloadBytes) + 1
	return relimited{over{store}, limits}
}
