package event

import (
	"context"
	"time"
)

// Three states rather than two, because a capability nobody stated and one a
// store denied are different answers: the first is refused at both doors, the
// second reports a conformance section as not certified rather than passed.
type Support uint8

const (
	Unstated Support = iota
	Unsupported
	Supported
)

func (this Support) String() string {
	switch this {
	case Unsupported:
		return "[support unsupported]"
	case Supported:
		return "[support supported]"
	default:
		return "[support unstated]"
	}
}

func (this Support) stated() bool { return this == Unsupported || this == Supported }

type Capabilities struct {
	Transactions       Support
	Persistence        Support
	MonotoneVisibility Support
	SharedBacking      Support
}

// The numbers the kernel enforces on this store's behalf. A store carries them
// and applies none of them, so two stores cannot enforce one bound differently;
// the exceptions are the two the store alone can apply, because only it issues
// the read: StreamPage caps a ReadStream page and MaxRead a ReadAll one.
type Limits struct {
	MaxPayload int
	MaxBatch   int
	MaxKey     int
	StreamPage int
	MaxRead    int
}

type Record struct {
	Type     string
	Revision int

	// The kernel's own array, and the kernel reads it again — at every later fold
	// of the change these bytes were frozen for, and at every later append of the
	// same list. Read it, clone it if you retain it, and never write into it, not
	// even temporarily: compressing, encrypting, redacting or padding in place
	// rewrites a fact that was already decided.
	Payload []byte
}

type AppendRequest struct {
	// Already validated by the kernel: the family is declared and the key is
	// legal text within Limits().MaxKey.
	Stream Stream

	// The version the caller's token was loaded at, and the only version this
	// batch may be admitted at. Zero means the stream has never been appended to
	// and never means "at any version": two creations of one fresh aggregate both
	// carry zero and exactly one of them may win, which is the one conflict a
	// re-read afterwards cannot see.
	Expected Version

	// Already encoded and already inside every bound the store published. The
	// kernel's own slice, under the same rule one level out: read it for the
	// duration of the call, never write into it, never retain it past the return.
	// A store that keeps rows for later takes its own slice.
	Records []Record
}

// What a store hands back. Its strings and numbers are a store's data and are
// checked before the kernel uses them for anything, a rendering included — the
// type name and Stream.Family against the kernel's identifier rule, the revision
// against the fact's own count, the payload against Limits().MaxPayload — so a
// store is trusted exactly as far as its numbers are, which is not at all.
type Envelope struct {
	Stream  Stream
	Version Version

	// The log's own order, and it means something only once the append that
	// carries it has committed. On an envelope a read hands back inside the
	// transaction that wrote it and has not committed it, it is unspecified: a
	// store drawing positions from a sequence has one already and a store
	// assigning them at commit answers zero, and both are conformant. Version is
	// not: it is the version the append was admitted at, before the commit and
	// after it.
	Position Position

	Type     string
	Revision int

	// Yours to give away, including its capacity: from the moment it is returned
	// it belongs to whoever received it, indefinitely, from any goroutine, and
	// that party may write into it. Clone what you retain and what you do not own
	// — a driver buffer valid until the next fetch, an mmap'ed page, a library
	// scratch — and reuse no buffer across two calls.
	Payload []byte

	RecordedAt time.Time
}

// Capabilities, Limits and Backing are pure and constant for the store's life:
// the same value on every call, computed from what the store writes to and
// never from the store value, and a decorator answers the wrapped store's
// values rather than its own. The kernel reads the first two once per door and
// retains them, which is what makes an empty append cost no store call at all;
// it reads Backing per operation, because a remembered backing cannot see a
// store that re-pointed at another database.
type Log interface {
	Capabilities() Capabilities
	Limits() Limits
	Backing() Backing

	// Envelopes at ascending positions after the cursor, at most
	// Limits().MaxRead of them, and a cursor that is safe to persist and resume
	// from in another process. Gaps in the positions are normal. A cursor minted
	// over another backing, one this store cannot parse, and one in a format it
	// no longer accepts are all Failure(BadCursor, ...).
	//
	// Unlike ReadStream, this does not read the caller's own writes: whether a
	// read issued while a transaction of this store's backing is bound returns
	// that transaction's uncommitted events is unspecified. A store reading
	// through the transaction it joined returns them, a store whose global order
	// is assigned at commit returns none, and both are conformant.
	//
	// No path the kernel initiates opens a transaction and then reads globally.
	// That is not a promise that no read runs inside one: Reader.Next issues this
	// call on whatever context the consumer hands it, and neither it nor Read
	// refuses a context carrying a transaction. What the consumer owes in that
	// case is written where the consumer reads it, on Reader.Cursor.
	ReadAll(ctx context.Context, after Cursor) ([]Envelope, Cursor, error)
}

// Every method is required, so the value a composition root handed over is the
// value that answers every question — including a decorator, which cannot be
// walked past. None of the eight opens, commits or rolls back anything: the
// transaction is the caller's throughout.
//
// All eight are safe for concurrent use. One store value is bound once and the
// Repo over it is one handle every request goroutine shares, so a store that
// needs a lock takes its own.
//
// A refusal from Append, ReadStream or ReadAll must be assumed to have made the
// caller's ambient transaction unusable, and the caller's obligation is to roll
// back before issuing anything else on it. The two exceptions are the two
// outcomes that mean refused rather than tried, Closed and Refused. A store may
// be more forgiving; it may not promise it.
//
// A store's panic is not recovered. It has an error channel and an outcome
// vocabulary, so recovering one would be the kernel classifying a failure the
// store did not classify.
type Store interface {
	Log

	// Which of THIS store's transactions the context carries. There are three
	// answers and no fourth:
	//
	//	a valid authority and no error    — a transaction of this store's backing
	//	an invalid authority and no error — nothing of this store's is bound
	//	an invalid authority and an error — something of this store's is bound and
	//	                                    it is not a transaction
	//
	// It issues no statement, opens nothing, mints nothing and memoises nothing.
	// A closed store answers exactly what an open one would and never reports
	// closure here, because this method writes and reads nothing and what the
	// context carries did not change when the store was closed; the refusal
	// comes from the operation that follows. Two calls that resolve to one live
	// transaction answer authorities that compare Same, and two live
	// transactions must not.
	Transaction(ctx context.Context) (Authority, error)

	// Envelopes of the stream that was asked for, at most Limits().StreamPage of
	// them, whose first version is after + 1 and each subsequent one exactly one
	// higher. A short page is the end of the stream and the kernel issues no
	// confirming read, so a store that returns a short page that is not the end
	// truncates a history silently. A read issued while a transaction of this
	// store's backing is bound returns that transaction's own staged appends as
	// well as the committed ones.
	ReadStream(ctx context.Context, s Stream, after Version) ([]Envelope, error)

	// Admits the batch if and only if the stream is at req.Expected; writes the
	// records, assigns dense versions and strictly increasing positions and
	// advances the stream in one atomic unit; records the instant from its own
	// injected clock. It never retries, never chunks and never deduplicates, and
	// a second append inside one transaction is admitted at the version the
	// first produced rather than at the committed one.
	//
	// A conflict is reported from here and never from the caller's own commit;
	// whether the store waits for a competing transaction or refuses at once is
	// the store's business and is promised to nobody. Every failure it returns
	// names its own Outcome: an unclassified one is read as uncertainty at this
	// door, so a store that can tell must.
	Append(ctx context.Context, req AppendRequest) error

	// Idempotent, returns nil every time, refuses nothing, and closes nothing it
	// did not open. It neither commits nor rolls back staged work.
	Close() error
}
