package event

import "fmt"

// What a decision is, once it has been decided. A change keeps the frozen
// bytes, the stream it was decided for and an adapter that decodes those bytes
// and applies that fact's fold — and no application value at all: not the
// payload the caller passed to Fact.New, not one decoded back from the bytes,
// and not the identity. So mutating the payload after deciding cannot change
// what was recorded, and the adapter closes over the declaration and nothing
// else.
//
// The frozen array is written by nobody. Every fold decodes a clone of it, so
// two folds of one change cannot disagree even through a codec that decodes by
// aliasing its input, and a store may read it but never write into it.
type Change[S any] struct {
	stream   Stream
	origin   Declaration
	name     string
	revision int
	payload  []byte
	apply    func(S, int, []byte) (S, error)
	err      error
}

func (this Change[S]) Stream() Stream { return this.stream }

func (this Change[S]) Err() error { return this.err }

// A change whose identity rendered no legal key never reached a stream at all,
// and its zero stream equals no legal one — so asking for stream equality first
// would answer a request-class ErrKey with a wiring-class crossing, and tell a
// caller the server broke over data only the caller can correct. A carried
// refusal is the older fact about such a change and outranks the comparison.
//
// The declaration is compared beside the stream, and it is a second question
// rather than the same one: a stream is {Family, Key}, so two aggregates
// declared over one family and one state type name each other's streams
// exactly, and the change of one folded through the other would run a fold that
// aggregate never declared. That is the crossing Bind refuses with ErrFamily,
// arriving at the doors Bind is not on.
func (this Change[S]) decidedFor(stream Stream, on Declaration) error {
	switch {
	case this.stream == stream && this.origin == on:
		return nil
	case this.err != nil:
		return this.err
	case this.name == "":
		return fmt.Errorf("%w: a change no fact ever decided was folded", ErrWrongStream)
	case this.stream != stream:
		return fmt.Errorf("%w: %q was decided for a stream this fold is not over", ErrWrongStream, this.name)
	}
	return fmt.Errorf("%w: %q was decided on another aggregate of this family", ErrFamily, this.name)
}
