package projection

import "github.com/frostgrove/vv/event"

// The position a wait is waiting for, and it is minted rather than named: the
// fields are unexported and both minting doors take a number a store produced. A
// caller that could write one would be waiting for evidence it invented, which is
// the same door Cutover closes by having no barrier field.
//
// What else it carries differs by door, and decides what a wait may promise.
// WaitSpec.Committed attaches the distinct sequence keys the spec's own sequencer
// answered for every envelope of the commit, in first-appearance order — so the
// park can be asked about this caller's own change — and MarkOf attaches none,
// because a barrier is folded from checkpoint rows and there is no envelope to
// ask.
//
// Both doors record the projection the mark was minted from, and Wait refuses one
// whose projection is not spec.Of's. One rule and not two: a barrier of another
// projection is evidence about another log, and a Committed mark's sequence keys
// are one projection's sequencer's answers, so asking a second projection's park
// under them answers false for an event it parked and reports Reached. The
// comparison is on the projection name alone and never on the generation, because
// a barrier of another generation of the same projection is the cutover case a
// wait admits.
//
// It holds no Envelope and no payload: the keys are computed once, at the mint,
// rather than by running the application's sequencer on every poll. String()
// answers "[mark]".
//
// The zero value is the one a caller can build and it is refused at Wait's door:
// positions are drawn from an identity sequence starting at one, so zero is never
// a real position and is exactly "not minted".
type Mark struct {
	at         event.Position
	sequences  []string
	projection string
}

func (this Mark) At() event.Position { return this.at }

func (this Mark) Zero() bool { return this.at == 0 }

func (this Mark) String() string { return "[mark]" }

// The mark a barrier was observed at. Observe folds it from a generation's own
// checkpoint rows, so it is store-issued in the only sense that matters here. It
// carries no sequence key, so Wait refuses it beside a non-nil Park rather than
// answering "delivered" to a caller that asked for "applied".
func MarkOf(barrier Barrier) Mark {
	return Mark{at: barrier.At, projection: barrier.Projection}
}
